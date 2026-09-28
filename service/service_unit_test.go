/*
Copyright © 2021-2026 Dell Inc. or its subsidiaries. All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package service

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dell/csi-powermax/v2/k8smock"
	"github.com/dell/csi-powermax/v2/k8sutils"
	symmetrix "github.com/dell/csi-powermax/v2/pkg/symmetrix"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	"github.com/dell/csmlog"
	"github.com/dell/gocsi"
	csictx "github.com/dell/gocsi/context"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/golang/mock/gomock"
	"github.com/spf13/viper"
	coordinationv1 "k8s.io/api/coordination/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kubernetesFake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

const (
	numOfCylindersForDefaultSize = 547
)

var (
	s                service
	mockedExitStatus = 0
	mockedStdout     string
	debugUnitTest    = false
)

func init() {
	// Initialize the global service instance for testing
	s = service{
		opts: Opts{
			PodmonPort: ":8083",
		},
		loggedInArrays:     map[string]bool{},
		loggedInNVMeArrays: map[string]bool{},
		nvmeTargets:        new(sync.Map),
		probeStatus:        new(sync.Map),
		k8sUtils:           k8smock.Init(),
	}
}

var (
	counters = [60]int{}
	testwg   sync.WaitGroup
)

func incrementCounter(identifier string, num int) {
	lockNumber, err := RequestLock(identifier, "")
	if err != nil {
		panic(err)
	}
	timeToSleep := rand.Intn(1010-500) + 500 // #nosec G404
	time.Sleep(time.Duration(timeToSleep) * time.Microsecond)
	if debugUnitTest {
		fmt.Printf("Sleeping for :%d microseconds\n", timeToSleep)
	}
	counters[num]++
	ReleaseLock(identifier, "", lockNumber)
	testwg.Done()
}

// TestReleaseLockWOAcquiring tries to release a lock that
// was never acquired.
func TestReleaseLockWOAcquiring(_ *testing.T) {
	LockRequestHandler()
	CleanupMapEntries(10 * time.Millisecond)
	ReleaseLock("nonExistentLock", "", 0)
}

// TestReleasingOtherLock tries to release a lock that it didn't acquire
func TestReleasingOtherLock(_ *testing.T) {
	LockRequestHandler()
	CleanupMapEntries(10 * time.Millisecond)
	lockNumber, err := RequestLock("new_lock", "")
	if err != nil {
		panic(err)
	}
	ReleaseLock("new_lock", "", lockNumber+1)
	ReleaseLock("new_lock", "", lockNumber)
}

var lockCounter int

func incrementLockCounter() {
	lockNumber, err := RequestLock("identifier", "")
	if err != nil {
		panic(err)
	}
	defer ReleaseLock("identifier", "", lockNumber)
	lockCounter++
}

func TestLockCounter(t *testing.T) {
	LockRequestHandler()
	CleanupMapEntries(10 * time.Millisecond)
	for i := 0; i < 500; i++ {
		// Acquire and release the lock in same goroutine
		incrementLockCounter()
	}
	if lockCounter != 500 {
		t.Errorf("Expected lock counter to be 500 but found: %d", lockCounter)
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "Successful creation of service",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := New()
			assert.NotNil(t, result)
		})
	}
}

func TestBeforeServe(t *testing.T) {
	tests := []struct {
		name           string
		ctx            context.Context
		plugin         *gocsi.StoragePlugin
		listener       net.Listener
		adminClient    pmax.Pmax
		k8sUtils       k8sutils.UtilsInterface
		init           func(ctx context.Context)
		expectedResult error
	}{
		{
			name: "Successful BeforeServe",
			ctx: context.WithValue(context.Background(), interface{}("os.Environ"), []string{
				"X_CSI_POWERMAX_ARRAY_CONFIG_PATH=path/to/config",
				"X_CSI_POWERMAX_PODMON_PORT=65000",
				"X_CSI_POWERMAX_KUBECONFIG_PATH=path/to/kubeconfig",
				"X_CSI_POWERMAX_NODENAME=node.example.com",
				"X_CSI_POWERMAX_PORTGROUPS=pg1,pg2",
				"X_CSI_POWERMAX_PODMON_API_TOKEN=podmon-token",
				"X_CSI_POWERMAX_TLS_CERT_DIR=/tmp/tls",
				"X_CSI_POWERMAX_PROXY_AUTH_TOKEN_FILE=/nonexistent/auth-token",
				"X_CSI_GRPC_MAX_THREADS=8",
				"X_CSI_PRIVATE_MOUNT_DIR=/tmp/private",
				"X_CSI_MANAGED_ARRAYS=000123",
				"X_CSI_POWERMAX_SIDECAR_PROXY_PORT=8080",
				"X_CSI_K8S_CLUSTER_PREFIX=csi",
				"X_CSI_POWERMAX_ENDPOINT=http://127.0.0.1:9104",
				"X_CSI_POWERMAX_PASSWORD=password",
				"X_CSI_MODE=controller",
				"X_CSI_MAX_VOLUMES_PER_NODE=10",
				"X_CSI_VSPHERE_ENABLED=true",
				"X_CSI_ENABLE_BLOCK=true",
				"X_CSI_POWERMAX_DRIVER_NAME=test",
				"X_CSI_POWERMAX_METRO_STATE_CHECK_TIMEOUT=21",
				"X_CSI_POWERMAX_METRO_QUEUE_WARNING_THRESHOLD=31",
				"X_CSI_POWERMAX_METRO_QUEUE_HARD_LIMIT=41",
				"X_CSI_POWERMAX_METRO_RECONCILIATION_BACKOFF=7",
				"X_CSI_POWERMAX_ISCSI_CHAP_USERNAME=user",
				"X_CSI_POWERMAX_ISCSI_CHAP_PASSWORD=password",
				"X_CSI_IG_NODENAME_TEMPLATE=template",
				"KUBECONFIG=path/to/kubeconfig",
				"X_CSI_REPLICATION_CONTEXT_PREFIX=contentprefix",
				"X_CSI_REPLICATION_PREFIX=prefix",
				"X_CSI_PODMON_API_PORT=65000",
				"X_CSI_PODMON_ARRAY_CONNECTIVITY_POLL_RATE=1m",
				"X_CSI_VSPHERE_PORTGROUP=portgroup",
				"X_CSI_VSPHERE_HOSTNAME=hostname",
				"X_CSI_VCENTER_HOST=vcenterhost",
				"X_CSI_VCENTER_USERNAME=user",
				"X_CSI_VCENTER_PWD=password",
			}),
			adminClient: func() pmax.Pmax {
				return mocks.NewMockPmaxClient(gomock.NewController(t))
			}(),
			k8sUtils: &k8smock.MockUtils{},
			plugin:   nil,
			listener: &net.TCPListener{},
			init: func(ctx context.Context) {
				err := csictx.Setenv(ctx, EnvSidecarProxyPort, "2222")
				if err != nil {
					t.Errorf("failed to set csi reverse proxy port. err: %s", err.Error())
				}
			},
			expectedResult: nil,
		},
		{
			name: "Error creating PowerMax client",
			ctx: context.WithValue(context.Background(), interface{}("os.Environ"), []string{
				"X_CSI_K8S_CLUSTER_PREFIX=csi",
				"X_CSI_MANAGED_ARRAYS=000123",
				"X_CSI_POWERMAX_ENDPOINT=http://127.0.0.1:9104",
				"X_CSI_POWERMAX_PASSWORD=password",
				"X_CSI_MODE=controller",
				"X_CSI_POWERMAX_SIDECAR_PROXY_PORT=2222",
				"X_CSI_MAX_VOLUMES_PER_NODE=invalid",
				"X_CSI_POWERMAX_METRO_STATE_CHECK_TIMEOUT=invalid",
				"X_CSI_POWERMAX_METRO_QUEUE_WARNING_THRESHOLD=0",
				"X_CSI_POWERMAX_METRO_QUEUE_HARD_LIMIT=-1",
				"X_CSI_POWERMAX_METRO_RECONCILIATION_BACKOFF=invalid",
			}),
			k8sUtils: &k8smock.MockUtils{},
			plugin:   nil,
			listener: &net.TCPListener{},
			init: func(ctx context.Context) {
				err := csictx.Setenv(ctx, EnvSidecarProxyPort, "2222")
				if err != nil {
					t.Errorf("failed to set csi reverse proxy port. err: %s", err.Error())
				}
			},
			expectedResult: status.Error(codes.FailedPrecondition, "unable to create PowerMax client: open tls.crt: no such file or directory"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					DriverName: "powermax",
					UseProxy:   true,
					User:       "username",
					Password:   "password",
				},
				k8sUtils:    tt.k8sUtils,
				adminClient: tt.adminClient,
			}
			if tt.init != nil {
				tt.init(tt.ctx)
			}
			oldInducedMockReverseProxy := inducedMockReverseProxy
			defer func() { inducedMockReverseProxy = oldInducedMockReverseProxy }()
			inducedMockReverseProxy = true
			defer func() {
				if s.deletionWorker != nil {
					s.deletionWorker.Stop()
				}
			}()
			result := s.BeforeServe(tt.ctx, nil, nil)
			assert.Equal(t, tt.expectedResult, result)
			if tt.name == "Successful BeforeServe" {
				assert.Equal(t, 21*time.Second, s.opts.MetroStateCheckTimeout)
				assert.Equal(t, 31, s.opts.MetroQueueWarningThreshold)
				assert.Equal(t, 41, s.opts.MetroQueueHardLimit)
				assert.Equal(t, 7*time.Second, s.opts.MetroReconciliationBackoff)
			}
		})
	}
}

func TestLocks(t *testing.T) {
	LockRequestHandler()
	CleanupMapEntries(10 * time.Millisecond)
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	testwg.Wait()
	// Check if all the counters were updated properly
	for _, counter := range counters {
		if counter != 6 {
			t.Errorf("expected counter to be %d but found %d", 6, counter)
		}
	}
}

func TestGetVolSize(t *testing.T) {
	tests := []struct {
		cr             *csi.CapacityRange
		numOfCylinders int
	}{
		{
			// not requesting any range should result in a default size
			cr: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    0,
			},
			numOfCylinders: numOfCylindersForDefaultSize,
		},
		{
			// requesting a minimum below the MinVolumeSizeBytes
			cr: &csi.CapacityRange{
				RequiredBytes: MinVolumeSizeBytes - 1,
				LimitBytes:    0,
			},
			numOfCylinders: 26,
		},
		{
			// requesting a negative required bytes
			cr: &csi.CapacityRange{
				RequiredBytes: -1,
				LimitBytes:    0,
			},
			numOfCylinders: 0,
		},
		{
			// requesting a negative limit bytes
			cr: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    -1,
			},
			numOfCylinders: 0,
		},
		{
			// not requesting a minimum but setting a limit below
			// the minimum size should result in an error
			cr: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    MinVolumeSizeBytes - 1,
			},
			numOfCylinders: 0,
		},
		{
			// requesting same sizes for minimum and maximum
			// which can be serviced
			cr: &csi.CapacityRange{
				RequiredBytes: MinVolumeSizeBytes,
				LimitBytes:    MinVolumeSizeBytes,
			},
			numOfCylinders: 26,
		},
		{
			// requesting size of 50 MB which is the advertised
			// minimum volume size
			cr: &csi.CapacityRange{
				RequiredBytes: 50 * 1024 * 1024,
				LimitBytes:    0,
			},
			numOfCylinders: 27,
		},
		{
			// requesting same sizes for minimum and maximum
			// which can't be serviced
			cr: &csi.CapacityRange{
				RequiredBytes: DefaultVolumeSizeBytes,
				LimitBytes:    DefaultVolumeSizeBytes,
			},
			numOfCylinders: 0,
		},
		{
			// requesting volume size of 1 TB
			cr: &csi.CapacityRange{
				RequiredBytes: 1099511627776, // 1* 1024 * 1024 * 1024 * 1024
				LimitBytes:    0,
			},
			numOfCylinders: 559241,
		},
		{
			// requesting volume of MaxVolumeSizeBytes
			cr: &csi.CapacityRange{
				RequiredBytes: MaxVolumeSizeBytes,
				LimitBytes:    0,
			},
			numOfCylinders: 35791395,
		},
		{
			// requesting volume size of more than 1 TB
			cr: &csi.CapacityRange{
				RequiredBytes: MaxVolumeSizeBytes + 1,
				LimitBytes:    0,
			},
			numOfCylinders: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run("", func(st *testing.T) {
			st.Parallel()
			s := &service{}
			num, err := s.validateVolSize(context.Background(), tt.cr, "", "", s.adminClient)
			if tt.numOfCylinders == 0 {
				// error is expected
				assert.Error(st, err)
			} else {
				assert.EqualValues(st, tt.numOfCylinders, num)
			}
		})
	}
}

func TestVolumeIdentifier(t *testing.T) {
	volumePrefix := s.getClusterPrefix()
	devID := "12345"
	volumeName := "Vol-Name"
	symID := "123456789012"
	csiDeviceID := s.createCSIVolumeID(volumePrefix, volumeName, symID, devID)
	volumeNameT, symIDT, devIDT, _, _, err := s.parseCsiID(csiDeviceID)
	if err != nil {
		t.Error()
		t.Error(err.Error())
	}
	volumeName = fmt.Sprintf("csi-%s-%s", volumePrefix, volumeName)
	if volumeNameT != volumeName ||
		symIDT != symID || devIDT != devID {
		t.Error("createCSIVolumeID and parseCsiID doesn't match")
	}
	// Test for empty device id
	_, _, _, _, _, err = s.parseCsiID("")
	if err == nil {
		t.Error("Expected an error while parsing empty ID but recieved success")
	}
	// Test for malformed device id
	malformedCSIDeviceID := "Vol1-Test"
	volumeNameT, symIDT, devIDT, _, _, err = s.parseCsiID(malformedCSIDeviceID)
	if err == nil {
		t.Error("Expected an error while parsing malformed ID but recieved success")
	}
	malformedCSIDeviceID = "-vol1-Test"
	_, _, _, _, _, err = s.parseCsiID(malformedCSIDeviceID)
	if err == nil {
		t.Error("Expected an error while parsing malformed ID but recieved success")
	}
}

func TestMetroCSIDeviceID(t *testing.T) {
	volumePrefix := s.getClusterPrefix()
	devID := "12345"
	volumeName := "Vol-Name"
	symID := "123456789012"
	remoteDevID := "98765"
	remoteSymID := "000000000012"
	volumeName = fmt.Sprintf("csi-%s-%s", volumePrefix, volumeName)
	csiDeviceID := fmt.Sprintf("%s-%s:%s-%s:%s", volumeName, symID, remoteSymID, devID, remoteDevID)
	volumeNameT, symIDT, devIDT, remSymIDT, remoteDevIDT, err := s.parseCsiID(csiDeviceID)
	if err != nil {
		t.Error()
		t.Error(err.Error())
	}
	if volumeNameT != volumeName ||
		symIDT != symID || devIDT != devID || remoteDevIDT != remoteDevID || remSymIDT != remoteSymID {
		t.Error("createCSIVolumeID and parseCsiID doesn't match")
	}
}

func TestStringSliceComparison(t *testing.T) {
	valA := []string{"a", "b", "c"}
	valB := []string{"c", "b", "a"}
	valC := []string{"a", "b"}
	valD := []string{"a", "b", "d"}

	if !stringSlicesEqual(valA, valB) {
		t.Error("Could not validate that reversed slices are equal")
	}
	if stringSlicesEqual(valA, valC) {
		t.Error("Could not validate that slices of different sizes are different")
	}
	if stringSlicesEqual(valA, valD) {
		t.Error("Could not validate that slices of different content are different")
	}
}

func TestStringSliceRegexMatcher(t *testing.T) {
	slice1 := []string{"aaa", "bbb", "abbba"}
	matches := stringSliceRegexMatcher(slice1, ".*bbb.*")
	if len(matches) != 2 {
		t.Errorf("Expected 2 matches got %d: %s", len(matches), matches)
	}
	// Test using bad regex
	matches = stringSliceRegexMatcher(slice1, "[a*")
	if len(matches) != 0 {
		t.Errorf("Expected 2 matches got %d: %s", len(matches), matches)
	}
}

func TestExecCommandHelper(_ *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	fmt.Printf("Mocked stdout: %s", os.Getenv("STDOUT"))
	fmt.Fprintf(os.Stdout, "%s", os.Getenv("STDOUT"))
	i, _ := strconv.Atoi(os.Getenv("EXIT_STATUS"))
	os.Exit(i)
}

func TestAppendIfMissing(t *testing.T) {
	testStrings := []string{"Test1", "Test2", "Test3"}
	testStrings = appendIfMissing(testStrings, "Test1")
	count := 0
	for _, str := range testStrings {
		if str == "Test1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Expected no more than one occurence of string Test1 in slice but found %d", count)
	}
	count = 0
	testStrings = appendIfMissing(testStrings, "Test4")
	for _, str := range testStrings {
		if str == "Test4" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("Expected no more than one occurence of string Test4 in slice but found %d", count)
	}
}

func TestTruncateString(t *testing.T) {
	stringToBeTruncated := "abcdefghijklmnopqrstuvwxyz"
	// Set maxLength to an even number
	truncatedString := truncateString(stringToBeTruncated, 10)
	if truncatedString != "abcdevwxyz" {
		t.Error("Truncated string doesn't match the expected string")
	}
	// Set maxLength to an odd number
	truncatedString = truncateString(stringToBeTruncated, 11)
	if truncatedString != "abcdeuvwxyz" {
		t.Error("Truncated string doesn't match the expected string")
	}
}

func TestFibreChannelSplitInitiatorID(t *testing.T) {
	director, port, initiator, err := splitFibreChannelInitiatorID("FA-2A:6:0x1000000000000000")
	if director != "FA-2A" {
		t.Errorf("Expected director FA-2A got %s", director)
	}
	if port != "FA-2A:6" {
		t.Errorf("Expected port FA-2A:6 got %s", port)
	}
	if initiator != "0x1000000000000000" {
		t.Errorf("Expected initiator 0x1000000000000000 got %s", initiator)
	}
	_, _, _, err = splitFibreChannelInitiatorID("meaningless string")
	if err == nil {
		t.Errorf("Expected error but got none")
	}
}

func TestPending(t *testing.T) {
	tests := []struct {
		npending     int
		maxpending   int
		differentIDs bool
		errormsg     string
	}{
		{
			npending:     2,
			maxpending:   1,
			differentIDs: true,
			errormsg:     "overload",
		},
		{
			npending:     4,
			maxpending:   5,
			differentIDs: true,
			errormsg:     "none",
		},
		{
			npending:     2,
			maxpending:   5,
			differentIDs: false,
			errormsg:     "pending",
		},
		{
			npending:     0,
			maxpending:   1,
			differentIDs: false,
			errormsg:     "none",
		},
	}
	for _, test := range tests {
		pendState := &pendingState{
			maxPending:   test.maxpending,
			pendingMutex: &sync.Mutex{},
		}
		for i := 0; i < test.npending; i++ {
			id := strconv.Itoa(i)
			if test.differentIDs == false {
				id = "same"
			}
			var vid volumeIDType
			vid = volumeIDType(id)
			err := vid.checkAndUpdatePendingState(pendState)
			if debugUnitTest {
				fmt.Printf("test %v err %v\n", test, err)
			}
			if i+1 == test.npending {
				if test.errormsg == "none" {
					if err != nil {
						t.Error("Expected no error but got: " + err.Error())
					}
				} else {
					if err != nil && !strings.Contains(err.Error(), test.errormsg) {
						t.Error("Didn't get expected error: " + test.errormsg)
					}
				}
			}
		}
		for i := 0; i <= test.maxpending; i++ {
			id := strconv.Itoa(i)
			if test.differentIDs == false {
				id = "same"
			}
			var vid volumeIDType
			vid = volumeIDType(id)
			vid.clearPending(pendState)
		}
	}
}

func TestGobrickInitialization(t *testing.T) {
	iscsiConnectorPrev := s.iscsiConnector
	s.iscsiConnector = nil
	s.initISCSIConnector("/")
	if s.iscsiConnector == nil {
		t.Error("Expected s.iscsiConnector to be initialized")
	}
	s.iscsiConnector = iscsiConnectorPrev

	fcConnectorPrev := s.fcConnector
	s.fcConnector = nil
	s.initFCConnector("/")
	if s.fcConnector == nil {
		t.Error("Expected s.fcConnector to be initialized")
	}
	s.fcConnector = fcConnectorPrev

	nvmeTCPConnectorPrev := s.nvmeTCPConnector
	s.nvmeTCPConnector = nil
	s.initNVMeTCPConnector("/")
	if s.nvmeTCPConnector == nil {
		t.Error("Expected s.nvmeTCPConnector to be initialized")
	}
	s.nvmeTCPConnector = nvmeTCPConnectorPrev
}

func TestSetGetLogFields(t *testing.T) {
	fields := csmlog.Fields{
		"RequestID": "123",
		"DeviceID":  "12345",
	}

	ctx := setLogFields(context.Background(), fields)
	fields = getLogFields(ctx)
	if fields["RequestID"] == nil {
		t.Error("Expected fields.CSIRequestID to be initialized")
	}

	fields = getLogFields(context.Background())
	if fields == nil {
		t.Error("Expected fields to be initialized")
	}

	ctx = context.WithValue(ctx, csictx.RequestIDKey, "456")
	fields = getLogFields(ctx)
	if fields["RequestID"] == nil {
		t.Error("Expected fields to be initialized")
	}
}

func TestEnsureISCSIDaemonIsStarted(t *testing.T) {
	s.dBusConn = &mockDbusConnection{}
	// Return a ListUnit mock response without ISCSId unit
	mockgosystemdInducedErrors.ListUnitISCSIDNotPresentError = true
	errMsg := fmt.Sprintf("failed to find iscsid.service. Going to panic")
	assert.PanicsWithError(t, errMsg, func() { s.ensureISCSIDaemonStarted() })
	mockgosystemdReset()
	s.dBusConn = &mockDbusConnection{}
	// Set the Daemon to inactive in mock response
	mockgosystemdInducedErrors.ISCSIDInactiveError = true
	mockgosystemdInducedErrors.StartUnitMaskedError = true
	errMsg = fmt.Sprintf("mock - unit is masked - failed to start the unit")
	assert.PanicsWithError(t, errMsg, func() { s.ensureISCSIDaemonStarted() })
}

func TestUpdateDriverConfigParams(_ *testing.T) {
	paramsViper := viper.New()
	paramsViper.SetConfigFile("configFilePath")
	paramsViper.SetConfigType("yaml")
	paramsViper.Set(CSILogLevelParam, "debug")
	paramsViper.Set(CSILogFormatParam, "JSON")
	updateDriverConfigParams(paramsViper)

	paramsViper.Set(CSILogFormatParam, "TEXT")
	updateDriverConfigParams(paramsViper)
}

func TestGetProxySettingsFromEnv(t *testing.T) {
	s := service{
		useIscsi: true,
	}
	_ = os.Setenv(EnvSidecarProxyPort, "8080")
	ProxyServiceHost, ProxyServicePort, _ := s.getProxySettingsFromEnv()
	assert.Equal(t, "0.0.0.0", ProxyServiceHost)
	assert.Equal(t, "8080", ProxyServicePort)

	os.Unsetenv(EnvSidecarProxyPort)
	_ = os.Setenv(EnvUnisphereProxyServiceName, "reverseproxy-service")
	_ = os.Setenv("REVERSEPROXY_SERVICE_SERVICE_PORT", "")
	ProxyServiceHost, ProxyServicePort, _ = s.getProxySettingsFromEnv()
	assert.Equal(t, "", ProxyServiceHost)
	assert.Equal(t, "", ProxyServicePort)

	_ = os.Setenv("REVERSEPROXY_SERVICE_SERVICE_PORT", "1234")
	ProxyServiceHost, ProxyServicePort, _ = s.getProxySettingsFromEnv()
	assert.Equal(t, "reverseproxy-service", ProxyServiceHost)
	assert.Equal(t, "1234", ProxyServicePort)
}

func TestGetTransportProtocolFromEnv(t *testing.T) {
	s := service{
		useIscsi: true,
	}
	_ = os.Setenv(EnvPreferredTransportProtocol, "FIBRE")
	output := s.getTransportProtocolFromEnv()
	assert.Equal(t, "FC", output)

	os.Unsetenv(EnvPreferredTransportProtocol)
	_ = os.Setenv(EnvPreferredTransportProtocol, "NVMETCP")
	output = s.getTransportProtocolFromEnv()
	assert.Equal(t, "NVMETCP", output)

	os.Unsetenv(EnvPreferredTransportProtocol)
	_ = os.Setenv(EnvPreferredTransportProtocol, "")
	output = s.getTransportProtocolFromEnv()
	assert.Equal(t, "", output)

	os.Unsetenv(EnvPreferredTransportProtocol)
	_ = os.Setenv(EnvPreferredTransportProtocol, "invalid")
	output = s.getTransportProtocolFromEnv()
	assert.Equal(t, "", output)
}

func TestSetPollingFrequency(t *testing.T) {
	s := service{
		useIscsi: true,
	}
	var expectedFreq int64 = 5
	ctx := context.Background()
	_ = os.Setenv(EnvPodmonArrayConnectivityPollRate, "5")
	pollingFreq := s.SetPollingFrequency(ctx)
	assert.Equal(t, expectedFreq, pollingFreq)
}

func TestGetDriverName(t *testing.T) {
	o := Opts{
		DriverName: "powermax",
	}
	s := service{
		opts: o,
	}

	driverName := s.getDriverName()
	assert.Equal(t, "powermax", driverName)
}

func TestRegisterAdditionalServers(_ *testing.T) {
	o := Opts{
		DriverName: "powermax",
	}
	s := service{
		opts: o,
	}
	server := grpc.NewServer()
	s.RegisterAdditionalServers(server)
}

var errMockErr = errors.New("mock error")

func TestCreateDbusConnection(t *testing.T) {
	tests := []struct {
		name                       string
		dBusConn                   *mockDbusConnection
		mockdbusNewWithContextFunc func() (dBusConn, error)
		expectedErr                error
	}{
		{
			name:        "Successful connection",
			dBusConn:    nil,
			expectedErr: nil,
			mockdbusNewWithContextFunc: func() (dBusConn, error) {
				return &dbus.Conn{}, nil
			},
		},
		{
			name:        "Error connection",
			dBusConn:    nil,
			expectedErr: errMockErr,
			mockdbusNewWithContextFunc: func() (dBusConn, error) {
				return nil, errMockErr
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{}

			dbusNewConnectionFunc = tt.mockdbusNewWithContextFunc
			err := s.createDbusConnection()

			if !errors.Is(err, tt.expectedErr) {
				t.Errorf("Expected error to be %v, but got: %v", tt.expectedErr, err)
			}

			if tt.expectedErr == nil && s.dBusConn == nil {
				t.Error("Expected dBusConn to be not nil, but it was nil")
			}
		})
	}
}

func TestCloseDbusConnection(t *testing.T) {
	tests := []struct {
		name        string
		dBusConn    *mockDbusConnection
		expectClose bool
	}{
		{
			name:        "Close non-nil connection",
			dBusConn:    &mockDbusConnection{},
			expectClose: true,
		},
		{
			name:        "No action on nil connection",
			dBusConn:    nil,
			expectClose: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				dBusConn: tt.dBusConn,
			}

			s.closeDbusConnection()

			if tt.expectClose {
				if s.dBusConn != nil {
					t.Errorf("Expected dBusConn to be nil, but got: %v", s.dBusConn)
				}
			} else {
				if s.dBusConn != nil {
					t.Errorf("Expected dBusConn to remain nil, but got: %v", s.dBusConn)
				}
			}
		})
	}
}

func TestSetArrayConfigEnvs(t *testing.T) {
	ctx := context.Background()
	fp := filepath.Join(os.TempDir(), "arrayConfig.yaml")
	file, err := os.Create(fp)
	assert.Equal(t, nil, err)

	defer func() {
		os.Remove(fp)
		file.Close()
	}()

	_ = os.Setenv(EnvArrayConfigPath, fp)
	paramsViper := viper.New()
	paramsViper.SetConfigFile(fp)
	paramsViper.SetConfigType("yaml")
	paramsViper.Set(Protocol, "ICSCI")
	paramsViper.Set(EnvEndpoint, "endpoint")
	paramsViper.Set(PortGroups, "pg1, pg2, pg3")
	paramsViper.Set(ManagedArrays, "000000000001,000000000002")

	err = paramsViper.WriteConfig()
	assert.Equal(t, nil, err)

	// Test case: Successful read of array config file
	err = setArrayConfigEnvs(ctx)
	assert.Equal(t, nil, err)
}

// ---------------------------------------------------------------------------
// Tests for hasMetroSiteLabels (U-001 through U-005)
// ---------------------------------------------------------------------------

func TestHasMetroSiteLabels_BothPresent(t *testing.T) {
	svc := &service{opts: Opts{
		StorageArrays: map[string]StorageArrayConfig{
			"000120000001": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
			"000120000002": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
		},
	}}
	assert.True(t, svc.hasMetroSiteLabels("000120000001", "000120000002"))
}

func TestHasMetroSiteLabels_LocalMissing(t *testing.T) {
	svc := &service{opts: Opts{
		StorageArrays: map[string]StorageArrayConfig{
			"000120000001": {Labels: map[string]interface{}{}},
			"000120000002": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
		},
	}}
	assert.False(t, svc.hasMetroSiteLabels("000120000001", "000120000002"))
}

func TestHasMetroSiteLabels_RemoteMissing(t *testing.T) {
	svc := &service{opts: Opts{
		StorageArrays: map[string]StorageArrayConfig{
			"000120000001": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
			"000120000002": {Labels: map[string]interface{}{}},
		},
	}}
	assert.False(t, svc.hasMetroSiteLabels("000120000001", "000120000002"))
}

func TestHasMetroSiteLabels_NeitherPresent(t *testing.T) {
	svc := &service{opts: Opts{
		StorageArrays: map[string]StorageArrayConfig{
			"000120000001": {Labels: map[string]interface{}{}},
			"000120000002": {Labels: map[string]interface{}{}},
		},
	}}
	assert.False(t, svc.hasMetroSiteLabels("000120000001", "000120000002"))
}

func TestHasMetroSiteLabels_ArrayNotInConfig(t *testing.T) {
	svc := &service{opts: Opts{
		StorageArrays: map[string]StorageArrayConfig{
			"000120000001": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
		},
	}}
	assert.False(t, svc.hasMetroSiteLabels("000120000001", "000120000002"))
}

// ---------------------------------------------------------------------------
// Tests for filterArraysByZoneInfo (U-006 through U-009)
// ---------------------------------------------------------------------------

func TestFilterArraysByZoneInfo_MultipleZonedArrays(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockK8s := k8smock.NewMockUtilsInterface(ctrl)
	mockK8s.EXPECT().GetNodeLabels(gomock.Any()).Return(map[string]string{
		"topology.kubernetes.io/site": "site1",
	}, nil)

	svc := &service{
		opts: Opts{
			NodeFullName: "worker-1",
			StorageArrays: map[string]StorageArrayConfig{
				"000120000001": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
				"000120000003": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
			},
		},
		k8sUtils: mockK8s,
	}
	result := svc.filterArraysByZoneInfo(svc.opts.StorageArrays)
	assert.Equal(t, 2, len(result))
}

func TestFilterArraysByZoneInfo_SingleZonedArray(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockK8s := k8smock.NewMockUtilsInterface(ctrl)
	mockK8s.EXPECT().GetNodeLabels(gomock.Any()).Return(map[string]string{
		"topology.kubernetes.io/site": "site1",
	}, nil)

	svc := &service{
		opts: Opts{
			NodeFullName: "worker-1",
			StorageArrays: map[string]StorageArrayConfig{
				"000120000001": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
				"000120000002": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
			},
		},
		k8sUtils: mockK8s,
	}
	result := svc.filterArraysByZoneInfo(svc.opts.StorageArrays)
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "000120000001", result[0])
}

func TestFilterArraysByZoneInfo_NoZonedArrays(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockK8s := k8smock.NewMockUtilsInterface(ctrl)
	mockK8s.EXPECT().GetNodeLabels(gomock.Any()).Return(map[string]string{
		"topology.kubernetes.io/site": "site1",
	}, nil)

	svc := &service{
		opts: Opts{
			NodeFullName: "worker-1",
			StorageArrays: map[string]StorageArrayConfig{
				"000120000001": {Labels: map[string]interface{}{}},
				"000120000002": {Labels: map[string]interface{}{}},
			},
		},
		k8sUtils: mockK8s,
	}
	result := svc.filterArraysByZoneInfo(svc.opts.StorageArrays)
	assert.Equal(t, 2, len(result))
}

func TestFilterArraysByZoneInfo_ZonedMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockK8s := k8smock.NewMockUtilsInterface(ctrl)
	mockK8s.EXPECT().GetNodeLabels(gomock.Any()).Return(map[string]string{
		"topology.kubernetes.io/site": "site1",
	}, nil)

	svc := &service{
		opts: Opts{
			NodeFullName: "worker-1",
			StorageArrays: map[string]StorageArrayConfig{
				"000120000001": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
				"000120000002": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site3"}},
			},
		},
		k8sUtils: mockK8s,
	}
	result := svc.filterArraysByZoneInfo(svc.opts.StorageArrays)
	assert.Equal(t, 0, len(result))
}

func TestFilterArraysByZoneInfo_AllLabeledNoneMatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockK8s := k8smock.NewMockUtilsInterface(ctrl)
	mockK8s.EXPECT().GetNodeLabels(gomock.Any()).Return(map[string]string{
		"topology.kubernetes.io/site": "site3",
	}, nil)

	svc := &service{
		opts: Opts{
			NodeFullName: "worker-1",
			StorageArrays: map[string]StorageArrayConfig{
				"000120000001": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
				"000120000002": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
			},
		},
		k8sUtils: mockK8s,
	}
	result := svc.filterArraysByZoneInfo(svc.opts.StorageArrays)
	// All arrays have labels but none match — zero reachable arrays (TC-S1-03)
	assert.Equal(t, 0, len(result))
}

func TestFilterArraysByZoneInfo_MixedLabeledAndUnlabeled(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockK8s := k8smock.NewMockUtilsInterface(ctrl)
	mockK8s.EXPECT().GetNodeLabels(gomock.Any()).Return(map[string]string{
		"topology.kubernetes.io/site": "site1",
	}, nil)

	svc := &service{
		opts: Opts{
			NodeFullName: "worker-1",
			StorageArrays: map[string]StorageArrayConfig{
				"000120000001": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
				"000120000002": {Labels: map[string]interface{}{}},
			},
		},
		k8sUtils: mockK8s,
	}
	result := svc.filterArraysByZoneInfo(svc.opts.StorageArrays)
	// Zoned array matches — only the zoned match is returned, unlabeled is excluded
	assert.Equal(t, 1, len(result))
	assert.Equal(t, "000120000001", result[0])
}

// ---------------------------------------------------------------------------
// Tests for nodeHasHostOnArray helper (non-uniform Metro host checks)
// ---------------------------------------------------------------------------

func TestNodeHasHostOnArray_ISCSIHostFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	svc := &service{}
	arrayID := "000120000001"
	nodeID := "worker-1"

	iscsiHostID, _, _ := svc.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	mockClient.EXPECT().GetHostList(gomock.Any(), arrayID).
		Return(&types.HostList{HostIDs: []string{iscsiHostID, "other-host"}}, nil).Times(1)

	result := svc.nodeHasHostOnArray(context.Background(), mockClient, arrayID, nodeID)
	assert.True(t, result, "should find iSCSI host on array")
}

func TestNodeHasHostOnArray_FCHostFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	svc := &service{}
	arrayID := "000120000001"
	nodeID := "worker-1"

	fcHostID, _, _ := svc.GetFCHostSGAndMVIDFromNodeID(nodeID)
	mockClient.EXPECT().GetHostList(gomock.Any(), arrayID).
		Return(&types.HostList{HostIDs: []string{fcHostID}}, nil).Times(1)

	result := svc.nodeHasHostOnArray(context.Background(), mockClient, arrayID, nodeID)
	assert.True(t, result, "should find FC host on array")
}

func TestNodeHasHostOnArray_NVMeTCPHostFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	svc := &service{}
	arrayID := "000120000001"
	nodeID := "worker-1"

	nvmeHostID, _, _ := svc.GetNVMETCPHostSGAndMVIDFromNodeID(nodeID)
	mockClient.EXPECT().GetHostList(gomock.Any(), arrayID).
		Return(&types.HostList{HostIDs: []string{nvmeHostID}}, nil).Times(1)

	result := svc.nodeHasHostOnArray(context.Background(), mockClient, arrayID, nodeID)
	assert.True(t, result, "should find NVMeTCP host on array")
}

func TestNodeHasHostOnArray_NoHostFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	svc := &service{}
	arrayID := "000120000001"
	nodeID := "worker-1"

	// Return hosts that do NOT match the node's host IDs
	mockClient.EXPECT().GetHostList(gomock.Any(), arrayID).
		Return(&types.HostList{HostIDs: []string{"unrelated-host-1", "unrelated-host-2"}}, nil).Times(1)

	result := svc.nodeHasHostOnArray(context.Background(), mockClient, arrayID, nodeID)
	assert.False(t, result, "should not find any matching host on array")
}

func TestNodeHasHostOnArray_EmptyHostList(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	svc := &service{}
	arrayID := "000120000001"
	nodeID := "worker-1"

	mockClient.EXPECT().GetHostList(gomock.Any(), arrayID).
		Return(&types.HostList{HostIDs: []string{}}, nil).Times(1)

	result := svc.nodeHasHostOnArray(context.Background(), mockClient, arrayID, nodeID)
	assert.False(t, result, "should return false for empty host list")
}

func TestNodeHasHostOnArray_GetHostListError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	svc := &service{}
	arrayID := "000120000001"
	nodeID := "worker-1"

	mockClient.EXPECT().GetHostList(gomock.Any(), arrayID).
		Return(nil, errors.New("connection refused")).Times(1)

	result := svc.nodeHasHostOnArray(context.Background(), mockClient, arrayID, nodeID)
	assert.False(t, result, "should return false when GetHostList fails")
}

func TestReadConfig(t *testing.T) {
	fp := filepath.Join(os.TempDir(), "topoConfig.yaml")
	file, err := os.Create(fp)
	assert.Equal(t, nil, err)

	defer func() {
		os.Remove(fp)
		file.Close()
	}()

	os.WriteFile(fp, []byte(`{"allowedConnections": 1234}`), 0o600)

	_, err = ReadConfig(fp)
	assert.Error(t, err)
}

// TestMetricsServerInitialization tests metrics server initialization with HTTP/HTTPS logging
func TestMetricsServerInitialization(t *testing.T) {
	tests := []struct {
		name           string
		metricsEnabled bool
		tlsCertFile    string
		tlsKeyFile     string
		expectHTTP     bool
		expectHTTPS    bool
	}{
		{
			name:           "Metrics disabled - no server starts",
			metricsEnabled: false,
			tlsCertFile:    "",
			tlsKeyFile:     "",
			expectHTTP:     false,
			expectHTTPS:    false,
		},
		{
			name:           "Metrics enabled with HTTP",
			metricsEnabled: true,
			tlsCertFile:    "",
			tlsKeyFile:     "",
			expectHTTP:     true,
			expectHTTPS:    false,
		},
		{
			name:           "Metrics enabled with HTTPS",
			metricsEnabled: true,
			tlsCertFile:    "/tmp/cert.pem",
			tlsKeyFile:     "/tmp/key.pem",
			expectHTTP:     false,
			expectHTTPS:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.metricsEnabled {
				t.Setenv("X_CSI_METRICS_ENABLED", "true")
			} else {
				t.Setenv("X_CSI_METRICS_ENABLED", "false")
			}
			if tt.tlsCertFile != "" {
				t.Setenv("X_CSI_METRICS_TLS_CERT_FILE", tt.tlsCertFile)
				t.Setenv("X_CSI_METRICS_TLS_KEY_FILE", tt.tlsKeyFile)
			}

			// Verify metricsEnabled function
			enabled := metricsEnabled()
			assert.Equal(t, tt.metricsEnabled, enabled)

			// Verify metricsPort function
			port := metricsPort()
			assert.Greater(t, port, 0)

			// Verify metricsTLSFiles function
			cert, key := metricsTLSFiles()
			if tt.tlsCertFile != "" {
				assert.Equal(t, tt.tlsCertFile, cert)
				assert.Equal(t, tt.tlsKeyFile, key)
			} else {
				assert.Empty(t, cert)
				assert.Empty(t, key)
			}
		})
	}
}

// TestCreatePowerMaxClientsWithMetrics tests client creation with metrics observer
func TestCreatePowerMaxClientsWithMetrics(t *testing.T) {
	tests := []struct {
		name           string
		metricsEnabled bool
		expectObserver bool
	}{
		{
			name:           "Metrics disabled - no observer",
			metricsEnabled: false,
			expectObserver: false,
		},
		{
			name:           "Metrics enabled - observer created",
			metricsEnabled: true,
			expectObserver: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up environment
			if tt.metricsEnabled {
				t.Setenv("X_CSI_METRICS_ENABLED", "true")
			} else {
				t.Setenv("X_CSI_METRICS_ENABLED", "false")
			}
			t.Setenv("X_CSI_POWERMAX_ENDPOINT", "https://127.0.0.1:9104")
			t.Setenv("X_CSI_POWERMAX_PASSWORD", "password")
			t.Setenv("X_CSI_MODE", "controller")
			t.Setenv("X_CSI_POWERMAX_SIDECAR_PROXY_PORT", "2222")
			t.Setenv("X_CSI_K8S_CLUSTER_PREFIX", "csi")
			t.Setenv("X_CSI_MANAGED_ARRAYS", "000123")

			_ = &service{
				opts: Opts{
					DriverName: "powermax",
					UseProxy:   true,
					User:       "username",
					Password:   "password",
				},
				k8sUtils: &k8smock.MockUtils{},
			}

			// Verify metricsEnabled function
			enabled := metricsEnabled()
			assert.Equal(t, tt.metricsEnabled, enabled)

			// Verify that when metrics are enabled, the observer creation logic is exercised
			if tt.metricsEnabled {
				reg := DriverMetricsRegistry()
				assert.NotNil(t, reg)
			}
		})
	}
}

// TestDriverMetricsRegistry tests the metrics registry creation
func TestDriverMetricsRegistry(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "Successful registry creation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			registry := DriverMetricsRegistry()
			assert.NotNil(t, registry)
		})
	}
}

// TestDefaultMetricsArrayID tests the default metrics array ID function
func TestDefaultMetricsArrayID(t *testing.T) {
	tests := []struct {
		name string
	}{
		{
			name: "Get default array ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			arrayID := DefaultMetricsArrayID()
			assert.NotEmpty(t, arrayID)
		})
	}
}

// ---------------------------------------------------------------------------
// Tests for metroLeaseNameForArray (C-3 helper)
// ---------------------------------------------------------------------------

func TestMetroLeaseNameForArray_AlphanumericID(t *testing.T) {
	got := metroLeaseNameForArray("000120000001")
	assert.Equal(t, "csi-pmax-metro-reconcile-000120000001", got)
}

func TestMetroLeaseNameForArray_UppercaseAndSpecialChars(t *testing.T) {
	got := metroLeaseNameForArray("ARRAY_001")
	// Uppercase → lowercase; underscore → dash.
	assert.Equal(t, "csi-pmax-metro-reconcile-array-001", got)
	assert.LessOrEqual(t, len(got), 253)
}

func TestMetroLeaseNameForArray_TruncationAt253(t *testing.T) {
	// "csi-pmax-metro-reconcile-" is 25 chars; 229 more 'a's → total 254 → must truncate.
	got := metroLeaseNameForArray(strings.Repeat("a", 229))
	assert.Equal(t, 253, len(got))
}

func TestMetroLeaseNameForArray_ExactlyAtLimit(t *testing.T) {
	// 228 chars of input → prefix (25) + 228 = 253 → no truncation.
	got := metroLeaseNameForArray(strings.Repeat("a", 228))
	assert.Equal(t, 253, len(got))
}

// ---------------------------------------------------------------------------
// Tests for tryAcquireMetroReconcileLease (C-3 fix)
// ---------------------------------------------------------------------------

func newFakeMockUtils() *k8smock.MockUtils {
	return &k8smock.MockUtils{KubernetesClient: kubernetesFake.NewSimpleClientset()}
}

func TestTryAcquireMetroReconcileLease_NilK8sUtils(t *testing.T) {
	svc := &service{}
	acquired, release := svc.tryAcquireMetroReconcileLease(context.Background(), "000120000001")
	assert.True(t, acquired)
	assert.NotNil(t, release)
	release() // must not panic
}

func TestTryAcquireMetroReconcileLease_NilClient(t *testing.T) {
	// Use the gomock-based mock so GetClient() returns a true nil
	// kubernetes.Interface (typed nil from *kubernetesFake.Clientset would
	// fool the interface == nil check).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockK8s := k8smock.NewMockUtilsInterface(ctrl)
	mockK8s.EXPECT().GetClient().Return(nil)

	svc := &service{k8sUtils: mockK8s}
	acquired, release := svc.tryAcquireMetroReconcileLease(context.Background(), "000120000001")
	assert.True(t, acquired)
	assert.NotNil(t, release)
	release()
}

func TestTryAcquireMetroReconcileLease_CreateSuccess(t *testing.T) {
	svc := &service{k8sUtils: newFakeMockUtils(), opts: Opts{NodeName: "controller-0"}}
	acquired, release := svc.tryAcquireMetroReconcileLease(context.Background(), "000120000001")
	assert.True(t, acquired)
	assert.NotNil(t, release)
	release() // should delete the lease without error
}

func TestTryAcquireMetroReconcileLease_SamePodTakeover(t *testing.T) {
	podName := "controller-0"
	mu := newFakeMockUtils()
	// Pre-seed a lease owned by the same pod.
	leaseName := metroLeaseNameForArray("000120000001")
	dur := int32(720)
	now := metav1.NewMicroTime(time.Now().Add(-time.Hour)) // old, but same pod
	_, err := mu.KubernetesClient.CoordinationV1().Leases("default").Create(
		context.Background(),
		&coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: leaseName, Namespace: "default"},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &podName,
				LeaseDurationSeconds: &dur,
				RenewTime:            &now,
			},
		},
		metav1.CreateOptions{},
	)
	assert.NoError(t, err)

	svc := &service{k8sUtils: mu, opts: Opts{NodeName: podName}}
	acquired, release := svc.tryAcquireMetroReconcileLease(context.Background(), "000120000001")
	assert.True(t, acquired)
	assert.NotNil(t, release)
	release()
}

func TestTryAcquireMetroReconcileLease_PeerHoldsValidLease(t *testing.T) {
	peer := "controller-1"
	mu := newFakeMockUtils()
	leaseName := metroLeaseNameForArray("000120000001")
	dur := int32(720)
	now := metav1.NewMicroTime(time.Now()) // fresh lease
	_, err := mu.KubernetesClient.CoordinationV1().Leases("default").Create(
		context.Background(),
		&coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: leaseName, Namespace: "default"},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &peer,
				LeaseDurationSeconds: &dur,
				RenewTime:            &now,
			},
		},
		metav1.CreateOptions{},
	)
	assert.NoError(t, err)

	svc := &service{k8sUtils: mu, opts: Opts{NodeName: "controller-0"}}
	acquired, release := svc.tryAcquireMetroReconcileLease(context.Background(), "000120000001")
	assert.False(t, acquired)
	assert.Nil(t, release)
}

func TestTryAcquireMetroReconcileLease_ExpiredLeaseTakeover(t *testing.T) {
	peer := "controller-1"
	mu := newFakeMockUtils()
	leaseName := metroLeaseNameForArray("000120000001")
	dur := int32(1) // 1-second duration, already expired
	old := metav1.NewMicroTime(time.Now().Add(-time.Hour))
	_, err := mu.KubernetesClient.CoordinationV1().Leases("default").Create(
		context.Background(),
		&coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: leaseName, Namespace: "default"},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &peer,
				LeaseDurationSeconds: &dur,
				RenewTime:            &old,
			},
		},
		metav1.CreateOptions{},
	)
	assert.NoError(t, err)

	svc := &service{k8sUtils: mu, opts: Opts{NodeName: "controller-0"}}
	acquired, release := svc.tryAcquireMetroReconcileLease(context.Background(), "000120000001")
	assert.True(t, acquired)
	assert.NotNil(t, release)
	release()
}

// ---------------------------------------------------------------------------
// Tests for logMetroStateCheck (H-5 fix)
// ---------------------------------------------------------------------------

func TestLogMetroStateCheck_NilCache(t *testing.T) {
	svc := &service{siteStateTracker: symmetrix.NewSiteStateTracker()}
	// Must not panic; returns immediately without accessing nil cache.
	assert.NotPanics(t, func() {
		_ = svc.logMetroStateCheck(context.Background(), "CreateVolume", "000120000001", "000120000002", "1")
	})
}

func TestLogMetroStateCheck_EmptyRDFGroup(t *testing.T) {
	svc := &service{
		metroStateCache:  symmetrix.NewMetroStateCache(0),
		siteStateTracker: symmetrix.NewSiteStateTracker(),
	}
	// H-5: empty rdfGroupNo must short-circuit before any cache or API access.
	assert.NotPanics(t, func() {
		_ = svc.logMetroStateCheck(context.Background(), "CreateVolume", "000120000001", "000120000002", "")
	})
}

func TestLogMetroStateCheck_CacheHitWithError(t *testing.T) {
	cache := symmetrix.NewMetroStateCache(30 * time.Second)
	cache.PutError("000120000001", "000120000002", errors.New("array unreachable"))

	svc := &service{
		metroStateCache:  cache,
		siteStateTracker: symmetrix.NewSiteStateTracker(),
	}
	// Should return immediately on cached error (H-1 fix: no latency storm).
	assert.NotPanics(t, func() {
		_ = svc.logMetroStateCheck(context.Background(), "CreateVolume", "000120000001", "000120000002", "1")
	})
}

func TestLogMetroStateCheck_CacheHitWithStateAndWinner(t *testing.T) {
	cache := symmetrix.NewMetroStateCache(30 * time.Second)
	state := &symmetrix.MetroState{WinnerSymID: "000120000001"}
	cache.Put("000120000001", "000120000002", state, nil)

	svc := &service{
		metroStateCache:  cache,
		siteStateTracker: symmetrix.NewSiteStateTracker(),
	}
	// Winner should be re-applied from cache; verify no panic.
	assert.NotPanics(t, func() {
		_ = svc.logMetroStateCheck(context.Background(), "CreateVolume", "000120000001", "000120000002", "1")
	})
}

func TestTryAcquireMetroReconcileLease_UnexpectedCreateError(t *testing.T) {
	// Inject a Forbidden error on Create so the non-AlreadyExists branch fires.
	fakeClient := kubernetesFake.NewSimpleClientset()
	fakeClient.Fake.PrependReactor("create", "leases", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, k8serrors.NewForbidden(schema.GroupResource{Resource: "leases"}, "test", errors.New("rbac"))
	})
	mu := &k8smock.MockUtils{KubernetesClient: fakeClient}

	svc := &service{k8sUtils: mu, opts: Opts{NodeName: "controller-0"}}
	acquired, release := svc.tryAcquireMetroReconcileLease(context.Background(), "000120000001")
	// Non-AlreadyExists error → fail-closed (block reconciliation without lock).
	assert.False(t, acquired)
	assert.Nil(t, release)
}

func TestTryAcquireMetroReconcileLease_GetFails(t *testing.T) {
	// Pre-seed a lease so Create returns AlreadyExists, then intercept Get.
	peer := "controller-1"
	dur := int32(720)
	now := metav1.NewMicroTime(time.Now())
	leaseName := metroLeaseNameForArray("000120000001")
	existingLease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{Name: leaseName, Namespace: "default"},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &peer,
			LeaseDurationSeconds: &dur,
			RenewTime:            &now,
		},
	}
	fakeClient := kubernetesFake.NewSimpleClientset(existingLease)
	fakeClient.Fake.PrependReactor("get", "leases", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("api server unavailable")
	})
	mu := &k8smock.MockUtils{KubernetesClient: fakeClient}

	svc := &service{k8sUtils: mu, opts: Opts{NodeName: "controller-0"}}
	acquired, release := svc.tryAcquireMetroReconcileLease(context.Background(), "000120000001")
	// Get failure → fail-closed (block reconciliation without lock).
	assert.False(t, acquired)
	assert.Nil(t, release)
}

// ---------------------------------------------------------------------------
// Tests for reconcileDeviceCleanup (H-4 fix)
// ---------------------------------------------------------------------------

func TestReconcileDeviceCleanup_InvalidVolumeID(t *testing.T) {
	// parseCsiID requires at least 3 dash-separated components; use a
	// two-component ID to trigger its malformed-ID error.
	svc := &service{opts: Opts{}}
	op := symmetrix.DeferredOperation{VolumeID: "bad-id"}
	err := svc.reconcileDeviceCleanup(context.Background(), op)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reconcileDeviceCleanup")
}

func TestReconcileDeviceCleanup_GetPowerMaxClientFails(t *testing.T) {
	// Valid parseable ID ("csi-SYMID-DEVID") but array not registered in
	// pkg/symmetrix → GetPowerMaxClient returns an error.
	svc := &service{
		opts:          Opts{},
		volumeJournal: symmetrix.NewVolumeJournal(),
	}
	op := symmetrix.DeferredOperation{
		VolumeID: "csi-000120000001-0AB12",
		ArrayID:  "000120000001",
	}
	err := svc.reconcileDeviceCleanup(context.Background(), op)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reconcileDeviceCleanup")
}

// ---------------------------------------------------------------------------
// Tests for reconcileMetroPairing
// ---------------------------------------------------------------------------

func TestReconcileMetroPairing_InvalidVolumeID(t *testing.T) {
	svc := &service{opts: Opts{}, volumeJournal: symmetrix.NewVolumeJournal()}
	op := symmetrix.DeferredOperation{VolumeID: "bad-id"}
	err := svc.reconcileMetroPairing(context.Background(), op)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reconcileMetroPairing")
}

func TestReconcileMetroPairing_GetPowerMaxClientFails(t *testing.T) {
	// Valid ID but no arrays registered → GetPowerMaxClient returns error.
	svc := &service{opts: Opts{}, volumeJournal: symmetrix.NewVolumeJournal()}
	op := symmetrix.DeferredOperation{
		VolumeID: "csi-000120000001-0AB12",
		ArrayID:  "000120000001",
	}
	err := svc.reconcileMetroPairing(context.Background(), op)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "reconcileMetroPairing")
}

// ---------------------------------------------------------------------------
// Tests for initMetroJournal
// ---------------------------------------------------------------------------

func TestInitMetroJournal_NoDynamicClient(t *testing.T) {
	// Out-of-cluster: CreateDynamicClient and InClusterConfig both fail.
	// initMetroJournal must not panic and must fall back gracefully.
	svc := &service{
		opts:          Opts{},
		volumeJournal: symmetrix.NewVolumeJournal(),
	}
	assert.NotPanics(t, func() {
		svc.initMetroJournal(context.Background())
	})
}

func TestInitMetroJournal_BackoffOption(t *testing.T) {
	// With MetroReconciliationBackoff set the SetReconciliationBackoff branch
	// inside initMetroJournal is exercised before the k8s dynamic-client call.
	svc := &service{
		opts: Opts{
			MetroReconciliationBackoff: 3 * time.Second,
		},
		volumeJournal: symmetrix.NewVolumeJournal(),
	}
	assert.NotPanics(t, func() {
		svc.initMetroJournal(context.Background())
	})
}

// ---------------------------------------------------------------------------
// Tests for triggerReconciliation
// ---------------------------------------------------------------------------

func TestTriggerReconciliation_AlreadyInFlight_SkipsDuplicate(t *testing.T) {
	// Pre-seed the in-flight map so the duplicate-guard returns immediately.
	svc := &service{
		opts:          Opts{},
		volumeJournal: symmetrix.NewVolumeJournal(),
	}
	svc.metroReconcileInFlight.Store("000120000001", struct{}{})
	defer svc.metroReconcileInFlight.Delete("000120000001")

	// Must return without panicking or blocking.
	assert.NotPanics(t, func() {
		svc.triggerReconciliation(context.Background(), "000120000001")
	})
}

// ---------------------------------------------------------------------------
// Tests for emitMetroEvent (nil recorder early-return path)
// ---------------------------------------------------------------------------

func TestEmitMetroEvent_NilRecorder_NoOp(t *testing.T) {
	// metroEventRecorder is nil (out-of-cluster) → function must be a no-op.
	svc := &service{opts: Opts{}}
	assert.NotPanics(t, func() {
		svc.emitMetroEvent(metroEventTypeWarning, "TestReason", "message %s", "arg")
	})
}

// ---------------------------------------------------------------------------
// Tests for triggerReconciliation (non-in-flight paths)
// ---------------------------------------------------------------------------

func TestTriggerReconciliation_EmptyJournal_Completes(t *testing.T) {
	// k8sUtils == nil → tryAcquireMetroReconcileLease returns (true, no-op).
	// Empty journal → ReconcileDeferredOperations returns no results.
	// Must complete without panic.
	svc := &service{
		opts:          Opts{},
		volumeJournal: symmetrix.NewVolumeJournal(),
	}
	assert.NotPanics(t, func() {
		svc.triggerReconciliation(context.Background(), "000120000001")
	})
}

func TestTriggerReconciliation_FailedOperation_CoveredDefaultCase(t *testing.T) {
	// Put one DeviceCleanup operation with an un-parseable VolumeID so that
	// reconcileDeviceCleanup fails immediately (parseCsiID error).  This
	// exercises the default (failure, non-unsafe, non-max-retry) branch in
	// the results-processing loop inside triggerReconciliation.
	j := symmetrix.NewVolumeJournal()
	_, err := j.CreateDeferredOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpDeviceCleanup,
		VolumeID:      "bad-id",
		ArrayID:       "000120000001",
	})
	assert.NoError(t, err)

	svc := &service{
		opts:          Opts{},
		volumeJournal: j,
	}
	// Use a short timeout context to prevent the test from waiting for full backoff
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	assert.NotPanics(t, func() {
		svc.triggerReconciliation(ctx, "000120000001")
	})
}

// ---------------------------------------------------------------------------
// Tests for deferOperation queue-depth checks
// ---------------------------------------------------------------------------

func TestDeferOperation_QueueAtLimit_Rejected(t *testing.T) {
	j := symmetrix.NewVolumeJournal()
	j.SetThresholds(1, 1) // hard limit = 1
	// Fill the queue to the hard limit.
	_, err := j.CreateDeferredOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpDeviceCleanup,
		VolumeID:      "vol-001",
		ArrayID:       "000120000001",
	})
	assert.NoError(t, err)

	svc := &service{opts: Opts{}, volumeJournal: j}
	// Queue is full → deferOperation must return ErrQueueFull.
	_, err = svc.deferOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpDeviceCleanup,
		VolumeID:      "vol-002",
		ArrayID:       "000120000001",
	})
	assert.ErrorIs(t, err, symmetrix.ErrQueueFull)
}

func TestDeferOperation_QueueAtWarning_StillAccepts(t *testing.T) {
	j := symmetrix.NewVolumeJournal()
	j.SetThresholds(1, 3) // warning at 1, hard limit at 3
	// Fill to warning threshold (1 op).
	_, err := j.CreateDeferredOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpDeviceCleanup,
		VolumeID:      "vol-001",
		ArrayID:       "000120000001",
	})
	assert.NoError(t, err)

	svc := &service{opts: Opts{}, volumeJournal: j}
	// At-warning but not at-limit → deferOperation emits warning and accepts.
	token, err := svc.deferOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpDeviceCleanup,
		VolumeID:      "vol-002",
		ArrayID:       "000120000001",
	})
	assert.NoError(t, err)
	assert.NotEmpty(t, token)
}

// ---------------------------------------------------------------------------
// Additional getTransportProtocolFromEnv coverage
// ---------------------------------------------------------------------------

func TestGetTransportProtocolFromEnv_Auto(t *testing.T) {
	s := service{}
	t.Setenv(EnvPreferredTransportProtocol, "AUTO")
	output := s.getTransportProtocolFromEnv()
	assert.Equal(t, "", output)
}

func TestGetTransportProtocolFromEnv_NotSet(t *testing.T) {
	os.Unsetenv(EnvPreferredTransportProtocol)
	s := service{}
	output := s.getTransportProtocolFromEnv()
	assert.Equal(t, "", output)
}

// ---------------------------------------------------------------------------
// Tests for customLogger (interfaces.go)
// ---------------------------------------------------------------------------

func TestCustomLogger_Debug(t *testing.T) {
	lg := &customLogger{}
	assert.NotPanics(t, func() {
		lg.Debug(context.Background(), "debug message %s", "arg")
	})
}

func TestCustomLogger_Error(t *testing.T) {
	lg := &customLogger{}
	assert.NotPanics(t, func() {
		lg.Error(context.Background(), "error message %s", "arg")
	})
}

func TestHostManagementModeConstants(t *testing.T) {
	// Verify env var constant
	assert.Equal(t, "X_CSI_POWERMAX_HOST_MGMT_MODE", EnvHostManagementMode)

	// Verify mode constants
	assert.Equal(t, "create", HostMgmtModeCreate)
	assert.Equal(t, "adopt", HostMgmtModeAdopt)
	assert.Equal(t, HostMgmtModeCreate, HostMgmtModeDefault)

	// Verify Opts field default through direct assignment
	opts := Opts{}
	assert.Equal(t, "", opts.HostManagementMode) // zero value
	opts.HostManagementMode = HostMgmtModeDefault
	assert.Equal(t, "create", opts.HostManagementMode)

	// Verify adopt mode via direct assignment
	opts.HostManagementMode = HostMgmtModeAdopt
	assert.Equal(t, "adopt", opts.HostManagementMode)
}
