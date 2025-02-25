/*
Copyright © 2021-2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/dell/csi-powermax/v2/k8smock"
	"github.com/dell/csi-powermax/v2/k8sutils"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	"github.com/dell/gocsi"
	csictx "github.com/dell/gocsi/context"
	pmax "github.com/dell/gopowermax/v2"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

var (
	counters = [60]int{}
	testwg   sync.WaitGroup
)

func incrementCounter(identifier string, num int) {
	lockNumber := RequestLock(identifier, "")
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
	lockNumber := RequestLock("new_lock", "")
	ReleaseLock("new_lock", "", lockNumber+1)
	ReleaseLock("new_lock", "", lockNumber)
}

var lockCounter int

func incrementLockCounter() {
	lockNumber := RequestLock("identifier", "")
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
		expectedResult error
	}{
		{
			name: "Successful BeforeServe",
			ctx: context.WithValue(context.Background(), interface{}("os.Environ"), []string{
				"X_CSI_POWERMAX_ARRAY_CONFIG_PATH=path/to/config",
				"X_CSI_POWERMAX_PODMON_PORT=65000",
				"X_CSI_POWERMAX_KUBECONFIG_PATH=path/to/kubeconfig",
				"X_CSI_MANAGED_ARRAYS=abc,def",
				"X_CSI_POWERMAX_SIDECAR_PROXY_PORT=8080",
				"X_CSI_K8S_CLUSTER_PREFIX=csi",
				"X_CSI_POWERMAX_ENDPOINT=http://127.0.0.1:8080",
				"X_CSI_POWERMAX_PASSWORD=password",
				"X_CSI_MODE=controller",
				"X_CSI_MAX_VOLUMES_PER_NODE=10",
				"X_CSI_VSPHERE_ENABLED=true",
				"X_CSI_ENABLE_BLOCK=true",
				"X_CSI_POWERMAX_DRIVER_NAME=test",
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
			k8sUtils:       &k8smock.MockUtils{},
			plugin:         nil,
			listener:       &net.TCPListener{},
			expectedResult: nil,
		},
		{
			name: "Error creating k8s utils",
			ctx: context.WithValue(context.Background(), interface{}("os.Environ"), []string{
				"X_CSI_K8S_CLUSTER_PREFIX=csi",
				"X_CSI_MANAGED_ARRAYS=abc,def",
				"X_CSI_POWERMAX_ENDPOINT=http://127.0.0.1:8080",
				"X_CSI_POWERMAX_PASSWORD=password",
				"X_CSI_MODE=controller",
			}),
			adminClient: func() pmax.Pmax {
				return mocks.NewMockPmaxClient(gomock.NewController(t))
			}(),
			plugin:         nil,
			listener:       &net.TCPListener{},
			expectedResult: errors.New("error creating k8sClient unable to load in-cluster configuration, KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT must be defined"),
		},
		{
			name: "Error creating PowerMax client",
			ctx: context.WithValue(context.Background(), interface{}("os.Environ"), []string{
				"X_CSI_K8S_CLUSTER_PREFIX=csi",
				"X_CSI_MANAGED_ARRAYS=abc,def",
				"X_CSI_POWERMAX_ENDPOINT=http://127.0.0.1:8080",
				"X_CSI_POWERMAX_PASSWORD=password",
				"X_CSI_MODE=controller",
			}),
			k8sUtils:       &k8smock.MockUtils{},
			plugin:         nil,
			listener:       &net.TCPListener{},
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
			oldInducedMockReverseProxy := inducedMockReverseProxy
			defer func() { inducedMockReverseProxy = oldInducedMockReverseProxy }()
			inducedMockReverseProxy = true
			result := s.BeforeServe(tt.ctx, nil, nil)
			assert.Equal(t, tt.expectedResult, result)
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
	fields := log.Fields{
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
		mockdbusNewWithContextFunc func() (*dbus.Conn, error)
		expectedErr                error
	}{
		{
			name:        "Successful connection",
			dBusConn:    nil,
			expectedErr: nil,
			mockdbusNewWithContextFunc: func() (*dbus.Conn, error) {
				return &dbus.Conn{}, nil
			},
		},
		{
			name:        "Error connection",
			dBusConn:    nil,
			expectedErr: errMockErr,
			mockdbusNewWithContextFunc: func() (*dbus.Conn, error) {
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
