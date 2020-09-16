/*
 Copyright © 2020 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	pmax "github.com/dell/gopowermax"
	mock "github.com/dell/gopowermax/mock"

	"github.com/DATA-DOG/godog"
	"github.com/DATA-DOG/godog/gherkin"
	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/gofsutil"
	"github.com/dell/goiscsi"
	ptypes "github.com/golang/protobuf/ptypes"
	"golang.org/x/net/context"
	"google.golang.org/grpc/metadata"

	types "github.com/dell/gopowermax/types/v90"
)

const (
	goodVolumeID               = "11111"
	goodVolumeName             = "vol1"
	altVolumeID                = "22222"
	altVolumeName              = "vol2"
	goodNodeID                 = "node1"
	altNodeID                  = "7E012974-3651-4DCB-9954-25975A3C3CDF"
	datafile                   = "test/tmp/datafile"
	datafile2                  = "test/tmp/datafile2"
	datadir                    = "test/tmp/datadir"
	datadir2                   = "test/tmp/datadir2"
	volume1                    = "CSIXX-Int409498632-000197900046-00501"
	volume2                    = "CSIXX-Int409498632-000197900046-00502"
	volume0                    = "CSI-notfound-000197900046-00500"
	nodePublishBlockDevice     = "sdc"
	altPublishBlockDevice      = "sdd"
	nodePublishMultipathDevice = "dm-0"
	nodePublishDeviceDir       = "test/dev"
	nodePublishBlockDevicePath = "test/dev/sdc"
	nodePublishMultipathPath   = "test/dev/dm-0"
	altPublishBlockDevicePath  = "test/dev/sdd"
	nodePublishSymlinkDir      = "test/dev/disk/by-id"
	nodePublishPathSymlinkDir  = "test/dev/disk/by-path"
	nodePublishPrivateDir      = "test/tmp"
	nodePublishWWN             = "60000970000197900046533030300501"
	nodePublishAltWWN          = "60000970000197900046533030300502"
	nodePublishLUNID           = "3"
	iSCSIEtcDir                = "test/etc/iscsi"
	iSCSIEtcFile               = "initiatorname.iscsi"
	goodSnapID                 = "444-444"
	altSnapID                  = "555-555"
	defaultStorageGroup        = "DefaultStorageGroup"
	defaultIscsiInitiator      = "iqn.1993-08.org.debian:01:5ae293b352a2"
	defaultFcInitiator         = "0x10000090fa6603b7"
	defaultArrayTargetIQN      = "iqn.1992-04.com.emc:600009700bcbb70e3287017400000001"
	defaultFcInitiatorWWN      = "10000090fa6603b7"
	defaultFcStoragePortWWN    = "5000000000000001"
	portalIP                   = "1.2.3.4"
	altPortalIP                = "1.2.3.5"
	defaultFCDirPort           = "FA-1D:4"
	defaultISCSIDirPort1       = "SE1-E:6"
	defaultISCSIDirPort2       = "SE2-E:4"
	MaxRetries                 = 10
)

var allBlockDevices = [2]string{nodePublishBlockDevicePath, altPublishBlockDevicePath}

type feature struct {
	nGoRoutines int
	lastTime    time.Time
	server      *httptest.Server
	service     *service
	err         error // return from the preceeding call
	// replace this with the Unispher client
	adminClient                          pmax.Pmax
	symmetrixID                          string
	system                               *interface{}
	poolcachewg                          sync.WaitGroup
	getPluginInfoResponse                *csi.GetPluginInfoResponse
	getPluginCapabilitiesResponse        *csi.GetPluginCapabilitiesResponse
	probeResponse                        *csi.ProbeResponse
	createVolumeResponse                 *csi.CreateVolumeResponse
	publishVolumeResponse                *csi.ControllerPublishVolumeResponse
	unpublishVolumeResponse              *csi.ControllerUnpublishVolumeResponse
	nodeGetInfoResponse                  *csi.NodeGetInfoResponse
	nodeGetCapabilitiesResponse          *csi.NodeGetCapabilitiesResponse
	deleteVolumeResponse                 *csi.DeleteVolumeResponse
	getCapacityResponse                  *csi.GetCapacityResponse
	controllerGetCapabilitiesResponse    *csi.ControllerGetCapabilitiesResponse
	validateVolumeCapabilitiesResponse   *csi.ValidateVolumeCapabilitiesResponse
	createSnapshotResponse               *csi.CreateSnapshotResponse
	deleteSnapshotResponse               *csi.DeleteSnapshotResponse
	createVolumeRequest                  *csi.CreateVolumeRequest
	publishVolumeRequest                 *csi.ControllerPublishVolumeRequest
	unpublishVolumeRequest               *csi.ControllerUnpublishVolumeRequest
	deleteVolumeRequest                  *csi.DeleteVolumeRequest
	listVolumesRequest                   *csi.ListVolumesRequest
	listVolumesResponse                  *csi.ListVolumesResponse
	listSnapshotsRequest                 *csi.ListSnapshotsRequest
	listSnapshotsResponse                *csi.ListSnapshotsResponse
	getVolumeByIDResponse                *GetVolumeByIDResponse
	response                             string
	listedVolumeIDs                      map[string]bool
	listVolumesNextTokenCache            string
	noNodeID                             bool
	omitAccessMode, omitVolumeCapability bool
	wrongCapacity, wrongStoragePool      bool
	useAccessTypeMount                   bool
	capability                           *csi.VolumeCapability
	capabilities                         []*csi.VolumeCapability
	nodePublishVolumeRequest             *csi.NodePublishVolumeRequest
	createSnapshotRequest                *csi.CreateSnapshotRequest
	volumeIDList                         []string
	volumeNameToID                       map[string]string
	snapshotNameToID                     map[string]string
	snapshotIndex                        int
	selectedPortGroup                    string
	sgID                                 string
	mvID                                 string
	hostID                               string
	volumeID                             string
	initiators                           []string
	ninitiators                          int
	host                                 *types.Host
	allowedArrays                        []string
	iscsiTargets                         []maskingViewTargetInfo
	lastUnmounted                        bool
	errType                              string
	isSnapSrc                            bool
	addVolumeToSGMVResponse1             chan addVolumeToSGMVResponse
	addVolumeToSGMVResponse2             chan addVolumeToSGMVResponse
	removeVolumeFromSGMVResponse1        chan removeVolumeFromSGMVResponse
	removeVolumeFromSGMVResponse2        chan removeVolumeFromSGMVResponse
	lockChan                             chan bool
	iscsiTargetInfo                      []ISCSITargetInfo
	maxRetryCount                        int
	failedSnaps                          map[string]failedSnap
	doneChan                             chan bool
}

var inducedErrors struct {
	invalidSymID        bool
	invalidStoragePool  bool
	invalidServiceLevel bool
	rescanError         bool
	noDeviceWWNError    bool
	badVolumeIdentifier bool
	invalidVolumeID     bool
	noVolumeID          bool
	invalidSnapID       bool
	differentVolumeID   bool
	portGroupError      bool
	noSymID             bool
	noNodeName          bool
	noIQNs              bool
	nonExistentVolume   bool
	noVolumeSource      bool
}

type failedSnap struct {
	volID     string
	snapID    string
	operation string
}

func (f *feature) checkGoRoutines(tag string) {
	goroutines := runtime.NumGoroutine()
	fmt.Printf("goroutines %s new %d old groutines %d\n", tag, goroutines, f.nGoRoutines)
	f.nGoRoutines = goroutines
}

func (f *feature) aPowerMaxService() error {
	// Print the duration of the last operation so we can tell which tests are slow
	now := time.Now()
	if f.lastTime.IsZero() {
		dur := now.Sub(testStartTime)
		fmt.Printf("startup time: %v\n", dur)
	} else {
		dur := now.Sub(f.lastTime)
		fmt.Printf("time for last op: %v\n", dur)
	}
	f.lastTime = now
	induceOverloadError = false
	inducePendingError = false
	gofsutil.GOFSWWNPath = "test/dev/disk/by-id/wwn-0x"
	nodePublishSleepTime = 5 * time.Millisecond
	removeDeviceSleepTime = 5 * time.Millisecond
	targetMountRecheckSleepTime = 30 * time.Millisecond
	disconnectVolumeRetryTime = 10 * time.Millisecond
	removeWithRetrySleepTime = 10 * time.Millisecond
	getMVConnectionsDelay = 10 * time.Millisecond
	maxBlockDevicesPerWWN = 3
	maxAddGroupSize = 10
	maxRemoveGroupSize = 10
	f.maxRetryCount = MaxRetries
	enableBatchGetMaskingViewConnections = true
	f.checkGoRoutines("start aPowerMaxService")
	// Save off the admin client and the system
	if f.service != nil && f.service.adminClient != nil {
		f.adminClient = f.service.adminClient
		f.system = f.service.system
	}
	// Let the real code initialize it the first time, we reset the cache each test
	if pmaxCache != nil {
		pmaxCache = make(map[string]*pmaxCachedInformation)
	}
	nodeCache = sync.Map{}
	f.err = nil
	f.symmetrixID = mock.DefaultSymmetrixID
	f.getPluginInfoResponse = nil
	f.getPluginCapabilitiesResponse = nil
	f.probeResponse = nil
	f.createVolumeResponse = nil
	f.nodeGetInfoResponse = nil
	f.nodeGetCapabilitiesResponse = nil
	f.getCapacityResponse = nil
	f.controllerGetCapabilitiesResponse = nil
	f.validateVolumeCapabilitiesResponse = nil
	f.service = nil
	f.createVolumeRequest = nil
	f.publishVolumeRequest = nil
	f.unpublishVolumeRequest = nil
	f.noNodeID = false
	f.omitAccessMode = false
	f.omitVolumeCapability = false
	f.useAccessTypeMount = false
	f.wrongCapacity = false
	f.wrongStoragePool = false
	f.deleteVolumeRequest = nil
	f.deleteVolumeResponse = nil
	f.listVolumesRequest = nil
	f.listVolumesResponse = nil
	f.listVolumesNextTokenCache = ""
	f.listSnapshotsRequest = nil
	f.listSnapshotsResponse = nil
	f.listedVolumeIDs = make(map[string]bool)
	f.capability = nil
	f.capabilities = make([]*csi.VolumeCapability, 0)
	f.nodePublishVolumeRequest = nil
	f.createSnapshotRequest = nil
	f.createSnapshotResponse = nil
	f.volumeIDList = f.volumeIDList[:0]
	f.sgID = ""
	f.mvID = ""
	f.hostID = ""
	f.initiators = make([]string, 0)
	f.iscsiTargetInfo = make([]ISCSITargetInfo, 0)
	f.ninitiators = 0
	f.volumeNameToID = make(map[string]string)
	f.snapshotNameToID = make(map[string]string)
	f.snapshotIndex = 0
	f.allowedArrays = []string{}
	if f.adminClient != nil {
		f.adminClient.SetAllowedArrays(f.allowedArrays)
	}
	f.iscsiTargets = make([]maskingViewTargetInfo, 0)
	f.isSnapSrc = false
	f.addVolumeToSGMVResponse1 = nil
	f.addVolumeToSGMVResponse2 = nil
	f.removeVolumeFromSGMVResponse1 = nil
	f.removeVolumeFromSGMVResponse2 = nil
	f.lockChan = nil
	f.failedSnaps = make(map[string]failedSnap)
	f.doneChan = make(chan bool)

	inducedErrors.invalidSymID = false
	inducedErrors.invalidStoragePool = false
	inducedErrors.invalidServiceLevel = false
	inducedErrors.rescanError = false
	inducedErrors.noDeviceWWNError = false
	inducedErrors.badVolumeIdentifier = false
	inducedErrors.invalidVolumeID = false
	inducedErrors.noVolumeID = false
	inducedErrors.differentVolumeID = false
	inducedErrors.portGroupError = false
	inducedErrors.noSymID = false
	inducedErrors.noNodeName = false
	inducedErrors.noIQNs = false
	inducedErrors.nonExistentVolume = false
	inducedErrors.invalidSnapID = false
	inducedErrors.noVolumeSource = false

	// configure gofsutil; we use a mock interface
	gofsutil.UseMockFS()
	gofsutil.GOFSMock.InduceBindMountError = false
	gofsutil.GOFSMock.InduceMountError = false
	gofsutil.GOFSMock.InduceGetMountsError = false
	gofsutil.GOFSMock.InduceDevMountsError = false
	gofsutil.GOFSMock.InduceUnmountError = false
	gofsutil.GOFSMock.InduceFormatError = false
	gofsutil.GOFSMock.InduceGetDiskFormatError = false
	gofsutil.GOFSMock.InduceWWNToDevicePathError = false
	gofsutil.GOFSMock.InduceTargetIPLUNToDeviceError = false
	gofsutil.GOFSMock.InduceRemoveBlockDeviceError = false
	gofsutil.GOFSMock.InduceMultipathCommandError = false
	gofsutil.GOFSMock.InduceRescanError = false
	gofsutil.GOFSMock.InduceGetMountInfoFromDeviceError = false
	gofsutil.GOFSMock.InduceDeviceRescanError = false
	gofsutil.GOFSMock.InduceResizeMultipathError = false
	gofsutil.GOFSMock.InduceFSTypeError = false
	gofsutil.GOFSMock.InduceResizeFSError = false
	gofsutil.GOFSMock.InduceGetDiskFormatType = ""
	gofsutil.GOFSMockMounts = gofsutil.GOFSMockMounts[:0]
	gofsutil.GOFSMockWWNToDevice = make(map[string]string)
	gofsutil.GOFSMockTargetIPLUNToDevice = make(map[string]string)
	gofsutil.GOFSRescanCallback = nil

	// configure variables in the driver
	getMappedVolMaxRetry = 1

	// Get or reuse the cached service
	f.getService()
	f.service.storagePoolCacheDuration = 4 * time.Hour
	f.service.SetPmaxTimeoutSeconds(3)

	// create the mock iscsi client
	f.service.iscsiClient = goiscsi.NewMockISCSI(map[string]string{})
	goiscsi.GOISCSIMock.InduceDiscoveryError = false
	goiscsi.GOISCSIMock.InduceInitiatorError = false
	goiscsi.GOISCSIMock.InduceLoginError = false
	goiscsi.GOISCSIMock.InduceLogoutError = false
	goiscsi.GOISCSIMock.InduceRescanError = false
	goiscsi.GOISCSIMock.InduceSetCHAPError = false

	// Get the httptest mock handler. Only set
	// a new server if there isn't one already.
	handler := mock.GetHandler()
	if handler != nil {
		if f.server == nil {
			f.server = httptest.NewServer(handler)
		}
		f.service.opts.Endpoint = f.server.URL
		log.Printf("server url: %s\n", f.server.URL)
	} else {
		f.server = nil
	}

	// Make sure the snapshot cleanup thread is started.
	f.service.startSnapCleanupWorker()
	snapCleaner.PollingInterval = 2 * time.Second
	// Start the lock workers
	f.service.StartLockManager(1 * time.Minute)
	// Make sure the deletion worker is started.
	f.service.startDeletionWorker(false)
	f.checkGoRoutines("end aPowerMaxService")
	delWorker.Queue = make(deletionWorkerQueue, 0)
	delWorker.CompletedRequests = make(deletionWorkerQueue, 0)
	f.errType = ""
	return nil
}

func (f *feature) getService() *service {
	mock.InducedErrors.NoConnection = false
	svc := new(service)
	svc.sgSvc = newStorageGroupService(svc)
	if f.adminClient != nil {
		svc.adminClient = f.adminClient
	}
	if f.system != nil {
		svc.system = f.system
	}
	mock.Reset()
	// This is a temp fix and needs to be handled in a different way
	mock.Data.JSONDir = "../../gopowermax/mock"
	svc.loggedInArrays = map[string]bool{}

	var opts Opts
	opts.User = "username"
	opts.Password = "password"
	opts.SystemName = "14dbbf5617523654"
	opts.NodeName = "Node1"
	opts.Insecure = true
	opts.DisableCerts = true
	opts.EnableBlock = true
	opts.PortGroups = []string{"portgroup1", "portgroup2"}
	mock.AddPortGroup("portgroup1", "ISCSI", []string{defaultISCSIDirPort1, defaultISCSIDirPort2})
	mock.AddPortGroup("portgroup2", "ISCSI", []string{defaultISCSIDirPort1, defaultISCSIDirPort2})
	opts.AllowedArrays = []string{}
	opts.EnableSnapshotCGDelete = true
	opts.EnableListVolumesSnapshots = true
	opts.ClusterPrefix = "TST"
	opts.NonDefaultRetries = true
	opts.ModifyHostName = false
	opts.NodeNameTemplate = ""
	opts.Lsmod = `
Module                  Size  Used by
vsock_diag             12610  0
scini                 799210  0
ip6t_rpfilter          12595  1
`
	svc.opts = opts
	svc.arrayTransportProtocolMap = make(map[string]string)
	svc.arrayTransportProtocolMap[mock.DefaultSymmetrixID] = IscsiTransportProtocol
	svc.fcConnector = &mockFCGobrick{}
	svc.iscsiConnector = &mockISCSIGobrick{}
	svc.dBusConn = &mockDbusConnection{}
	mockGobrickReset()
	mockgosystemdReset()
	disconnectVolumeRetryTime = 10 * time.Millisecond
	f.service = svc
	return svc
}

func (f *feature) aPostELMSRArray() error {
	f.symmetrixID = mock.PostELMSRSymmetrixID
	return nil
}

// GetPluginInfo
func (f *feature) iCallGetPluginInfo() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.GetPluginInfoRequest)
	f.getPluginInfoResponse, f.err = f.service.GetPluginInfo(ctx, req)
	if f.err != nil {
		return f.err
	}
	return nil
}
func (f *feature) aValidGetPluginInfoResponseIsReturned() error {
	rep := f.getPluginInfoResponse
	url := rep.GetManifest()["url"]
	if rep.GetName() == "" || rep.GetVendorVersion() == "" || url == "" {
		return errors.New("Expected GetPluginInfo to return name and version")
	}
	log.Printf("Name %s Version %s URL %s", rep.GetName(), rep.GetVendorVersion(), url)
	return nil
}

func (f *feature) iCallGetPluginCapabilities() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.GetPluginCapabilitiesRequest)
	f.getPluginCapabilitiesResponse, f.err = f.service.GetPluginCapabilities(ctx, req)
	if f.err != nil {
		return f.err
	}
	return nil
}

func (f *feature) aValidGetPluginCapabilitiesResponseIsReturned() error {
	rep := f.getPluginCapabilitiesResponse
	capabilities := rep.GetCapabilities()
	var foundController bool
	for _, capability := range capabilities {
		if capability.GetService().GetType() == csi.PluginCapability_Service_CONTROLLER_SERVICE {
			foundController = true
		}
	}
	if !foundController {
		return errors.New("Expected PlugiinCapabilitiesResponse to contain CONTROLLER_SERVICE")
	}
	return nil
}

func (f *feature) iCallProbe() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.ProbeRequest)
	f.checkGoRoutines("before probe")
	f.probeResponse, f.err = f.service.Probe(ctx, req)
	f.checkGoRoutines("after probe")
	return nil
}

func (f *feature) aValidProbeResponseIsReturned() error {
	if f.probeResponse.GetReady().GetValue() != true {
		return errors.New("Probe returned Ready false")
	}
	return nil
}

func (f *feature) theErrorContains(arg1 string) error {
	f.checkGoRoutines("theErrorContains")
	// If arg1 is none, we expect no error, any error received is unexpected
	if arg1 == "none" {
		if f.err == nil {
			return nil
		}
		return fmt.Errorf("Unexpected error: %s", f.err)
	}
	// We expected an error... unless there is a none clause
	if f.err == nil {
		// Check to see if no error is allowed as alternative
		possibleMatches := strings.Split(arg1, "@@")
		for _, possibleMatch := range possibleMatches {
			if possibleMatch == "none" {
				return nil
			}
		}
		return fmt.Errorf("Expected error to contain %s but no error", arg1)
	}
	// Allow for multiple possible matches, separated by @@. This was necessary
	// because Windows and Linux sometimes return different error strings for
	// gofsutil operations. Note @@ was used instead of || because the Gherkin
	// parser is not smart enough to ignore vertical braces within a quoted string,
	// so if || is used it thinks the row's cell count is wrong.
	possibleMatches := strings.Split(arg1, "@@")
	for _, possibleMatch := range possibleMatches {
		if strings.Contains(f.err.Error(), possibleMatch) {
			return nil
		}
	}
	return fmt.Errorf("Expected error to contain %s but it was %s", arg1, f.err.Error())
}

func (f *feature) thePossibleErrorContains(arg1 string) error {
	if f.err == nil {
		return nil
	}
	return f.theErrorContains(arg1)
}

func (f *feature) theControllerHasNoConnection() error {
	mock.InducedErrors.NoConnection = true
	return nil
}

func (f *feature) thereIsANodeProbeLsmodError() error {
	f.service.opts.Lsmod = ""
	return nil
}

func (f *feature) getTypicalCreateVolumeRequest() *csi.CreateVolumeRequest {
	req := new(csi.CreateVolumeRequest)
	params := make(map[string]string)
	params[SymmetrixIDParam] = f.symmetrixID
	params[ServiceLevelParam] = mock.DefaultServiceLevel
	params[StoragePoolParam] = mock.DefaultStoragePool
	if inducedErrors.invalidSymID {
		params[SymmetrixIDParam] = ""
	}
	if inducedErrors.invalidServiceLevel {
		params[ServiceLevelParam] = "invalid"
	}
	if inducedErrors.invalidStoragePool {
		params[StoragePoolParam] = "invalid"
	}
	req.Parameters = params
	req.Name = "volume1"
	capacityRange := new(csi.CapacityRange)
	capacityRange.RequiredBytes = 100 * 1024 * 1024 * 1024
	req.CapacityRange = capacityRange
	block := new(csi.VolumeCapability_BlockVolume)
	capability := new(csi.VolumeCapability)
	accessType := new(csi.VolumeCapability_Block)
	accessType.Block = block
	capability.AccessType = accessType
	accessMode := new(csi.VolumeCapability_AccessMode)
	accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	capability.AccessMode = accessMode
	capabilities := make([]*csi.VolumeCapability, 0)
	capabilities = append(capabilities, capability)
	req.VolumeCapabilities = capabilities
	return req
}

func (f *feature) iSpecifyCreateVolumeMountRequest(fstype string) error {
	req := new(csi.CreateVolumeRequest)
	params := make(map[string]string)
	params["storagepool"] = "viki_pool_HDD_20181031"
	params[SymmetrixIDParam] = f.symmetrixID
	params[ServiceLevelParam] = mock.DefaultServiceLevel
	params[StoragePoolParam] = mock.DefaultStoragePool
	req.Parameters = params
	req.Name = "mount1"
	capacityRange := new(csi.CapacityRange)
	capacityRange.RequiredBytes = 8 * 1024 * 1024 * 1024
	req.CapacityRange = capacityRange
	capability := new(csi.VolumeCapability)
	mountVolume := new(csi.VolumeCapability_MountVolume)
	mountVolume.FsType = fstype
	mountVolume.MountFlags = make([]string, 0)
	mount := new(csi.VolumeCapability_Mount)
	mount.Mount = mountVolume
	capability.AccessType = mount
	accessMode := new(csi.VolumeCapability_AccessMode)
	accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	capability.AccessMode = accessMode
	capabilities := make([]*csi.VolumeCapability, 0)
	capabilities = append(capabilities, capability)
	req.VolumeCapabilities = capabilities
	f.createVolumeRequest = req
	return nil
}

func (f *feature) iCallCreateVolume(name string) error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	if f.createVolumeRequest == nil {
		req := f.getTypicalCreateVolumeRequest()
		f.createVolumeRequest = req
	}
	req := f.createVolumeRequest
	req.Name = name

	f.createVolumeResponse, f.err = f.service.CreateVolume(ctx, req)
	if f.err != nil {
		log.Printf("CreateVolume called failed: %s\n", f.err.Error())
	}
	if f.createVolumeResponse != nil {
		log.Printf("vol id %s\n", f.createVolumeResponse.GetVolume().VolumeId)
		f.volumeID = f.createVolumeResponse.GetVolume().VolumeId
		f.volumeNameToID[name] = f.volumeID
	}
	return nil
}

func (f *feature) aValidCreateVolumeResponseIsReturned() error {
	if f.err != nil {
		return f.err
	}
	if f.createVolumeResponse == nil || f.createVolumeResponse.Volume == nil {
		return errors.New("Expected a valid createVolumeResponse")
	}
	// Verify the Volume context
	params := f.createVolumeRequest.Parameters
	volumeContext := f.createVolumeResponse.GetVolume().VolumeContext
	fmt.Printf("volume:\n%#v\n", volumeContext)
	if params[StoragePoolParam] != volumeContext[StoragePoolParam] {
		return errors.New("StoragePoolParam in response should match the request")
	}
	if serviceLevel, ok := params[ServiceLevelParam]; ok {
		if serviceLevel != volumeContext[ServiceLevelParam] {
			return errors.New("ServiceLevelParam in response should match the request")
		}
	} else {
		if volumeContext[StoragePoolParam] != "Optimized" {
			return errors.New("ServiceLevelParam in response should be Optimized")
		}
	}
	f.volumeIDList = append(f.volumeIDList, f.createVolumeResponse.Volume.VolumeId)
	fmt.Printf("Service Level %s SRP %s\n",
		f.createVolumeResponse.Volume.VolumeContext[ServiceLevelParam],
		f.createVolumeResponse.Volume.VolumeContext[StoragePoolParam])
	return nil
}

func (f *feature) iSpecifyAccessibilityRequirements() error {
	req := new(csi.CreateVolumeRequest)
	params := make(map[string]string)
	params[SymmetrixIDParam] = f.symmetrixID
	params[ServiceLevelParam] = mock.DefaultServiceLevel
	params[StoragePoolParam] = mock.DefaultStoragePool
	req.Parameters = params
	req.Name = "accessability"
	capacityRange := new(csi.CapacityRange)
	capacityRange.RequiredBytes = 8 * 1024 * 1024 * 1024
	req.CapacityRange = capacityRange
	req.AccessibilityRequirements = new(csi.TopologyRequirement)
	f.createVolumeRequest = req
	return nil
}

func (f *feature) iSpecifyVolumeContentSource() error {
	req := f.getTypicalCreateVolumeRequest()
	req.Name = "volume_content_source"
	req.VolumeContentSource = new(csi.VolumeContentSource)
	req.VolumeContentSource.Type = &csi.VolumeContentSource_Volume{Volume: &csi.VolumeContentSource_VolumeSource{}}
	f.createVolumeRequest = req
	return nil
}

func (f *feature) iSpecifyMULTINODEWRITER() error {
	req := new(csi.CreateVolumeRequest)
	params := make(map[string]string)
	params[SymmetrixIDParam] = f.symmetrixID
	params[ServiceLevelParam] = mock.DefaultServiceLevel
	params[StoragePoolParam] = mock.DefaultStoragePool
	req.Parameters = params
	req.Name = "multinode_writer"
	capacityRange := new(csi.CapacityRange)
	capacityRange.RequiredBytes = 8 * 1024 * 1024 * 1024
	req.CapacityRange = capacityRange
	block := new(csi.VolumeCapability_BlockVolume)
	capability := new(csi.VolumeCapability)
	accessType := new(csi.VolumeCapability_Block)
	accessType.Block = block
	capability.AccessType = new(csi.VolumeCapability_Block)
	accessMode := new(csi.VolumeCapability_AccessMode)
	accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER
	capability.AccessMode = accessMode
	capabilities := make([]*csi.VolumeCapability, 0)
	capabilities = append(capabilities, capability)
	req.VolumeCapabilities = capabilities
	f.createVolumeRequest = req
	return nil
}

func (f *feature) iSpecifyABadCapacity() error {
	req := f.getTypicalCreateVolumeRequest()
	capacityRange := new(csi.CapacityRange)
	capacityRange.RequiredBytes = -8 * 1024 * 1024 * 1024
	req.CapacityRange = capacityRange
	req.Name = "bad capacity"
	f.createVolumeRequest = req
	return nil
}

func (f *feature) iSpecifyAApplicationPrefix() error {
	req := f.getTypicalCreateVolumeRequest()
	params := req.GetParameters()
	params["ApplicationPrefix"] = "UNI"
	req.Parameters = params
	f.createVolumeRequest = req
	return nil
}

func (f *feature) iSpecifyAStorageGroup() error {
	req := f.getTypicalCreateVolumeRequest()
	params := req.GetParameters()
	params["StorageGroup"] = "UnitTestSG"
	req.Parameters = params
	f.createVolumeRequest = req
	return nil
}

func (f *feature) iSpecifyNoStoragePool() error {
	req := f.getTypicalCreateVolumeRequest()
	req.Parameters = nil
	req.Name = "no storage pool"
	f.createVolumeRequest = req
	return nil
}

func (f *feature) iCallCreateVolumeSize(name string, size int64) error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := f.getTypicalCreateVolumeRequest()
	capacityRange := new(csi.CapacityRange)
	capacityRange.RequiredBytes = size * 1024 * 1024
	req.CapacityRange = capacityRange
	req.Name = name
	f.createVolumeRequest = req

	f.createVolumeResponse, f.err = f.service.CreateVolume(ctx, req)
	if f.err != nil {
		log.Printf("CreateVolumeSize called failed: %s\n", f.err.Error())
	}
	if f.createVolumeResponse != nil {
		log.Printf("vol id %s\n", f.createVolumeResponse.GetVolume().VolumeId)
		f.volumeID = f.createVolumeResponse.GetVolume().VolumeId
		f.volumeNameToID[name] = f.volumeID
	}
	return nil
}

func (f *feature) iChangeTheStoragePool(storagePoolName string) error {
	params := make(map[string]string)
	params[SymmetrixIDParam] = f.symmetrixID
	params[ServiceLevelParam] = "Diamond"
	params[StoragePoolParam] = mock.DefaultStoragePool
	f.createVolumeRequest.Parameters = params
	return nil
}

func (f *feature) iInduceError(errtype string) error {
	log.Printf("set induce error %s\n", errtype)
	f.errType = errtype
	switch errtype {
	case "InvalidSymID":
		inducedErrors.invalidSymID = true
	case "InvalidStoragePool":
		inducedErrors.invalidStoragePool = true
	case "InvalidServiceLevel":
		inducedErrors.invalidServiceLevel = true
	case "NoDeviceWWNError":
		inducedErrors.noDeviceWWNError = true
	case "PortGroupError":
		inducedErrors.portGroupError = true
	case "GetVolumeIteratorError":
		mock.InducedErrors.GetVolumeIteratorError = true
	case "GetVolumeError":
		mock.InducedErrors.GetVolumeError = true
	case "UpdateVolumeError":
		mock.InducedErrors.UpdateVolumeError = true
	case "DeleteVolumeError":
		mock.InducedErrors.DeleteVolumeError = true
	case "DeviceInSGError":
		mock.InducedErrors.DeviceInSGError = true
	case "GetJobError":
		mock.InducedErrors.GetJobError = true
	case "JobFailedError":
		mock.InducedErrors.JobFailedError = true
	case "UpdateStorageGroupError":
		mock.InducedErrors.UpdateStorageGroupError = true
	case "GetStorageGroupError":
		mock.InducedErrors.GetStorageGroupError = true
	case "CreateStorageGroupError":
		mock.InducedErrors.CreateStorageGroupError = true
	case "GetMaskingViewError":
		mock.InducedErrors.GetMaskingViewError = true
	case "CreateMaskingViewError":
		mock.InducedErrors.CreateMaskingViewError = true
	case "GetStoragePoolListError":
		mock.InducedErrors.GetStoragePoolListError = true
	case "GetHostError":
		mock.InducedErrors.GetHostError = true
	case "CreateHostError":
		mock.InducedErrors.CreateHostError = true
	case "UpdateHostError":
		mock.InducedErrors.UpdateHostError = true
	case "GetSymmetrixError":
		mock.InducedErrors.GetSymmetrixError = true
	case "GetStoragePoolError":
		mock.InducedErrors.GetStoragePoolError = true
	case "DeleteMaskingViewError":
		mock.InducedErrors.DeleteMaskingViewError = true
	case "GetMaskingViewConnectionsError":
		mock.InducedErrors.GetMaskingViewConnectionsError = true
	case "DeleteStorageGroupError":
		mock.InducedErrors.DeleteStorageGroupError = true
	case "GetPortGroupError":
		mock.InducedErrors.GetPortGroupError = true
	case "GetPortError":
		mock.InducedErrors.GetPortError = true
	case "GetDirectorError":
		mock.InducedErrors.GetDirectorError = true
	case "ResetAfterFirstError":
		mock.InducedErrors.ResetAfterFirstError = true
	case "GetInitiatorError":
		mock.InducedErrors.GetInitiatorError = true
	case "GetInitiatorByIDError":
		mock.InducedErrors.GetInitiatorByIDError = true
	case "CreateSnapshotError":
		mock.InducedErrors.CreateSnapshotError = true
	case "DeleteSnapshotError":
		mock.InducedErrors.DeleteSnapshotError = true
	case "RenameSnapshotError":
		mock.InducedErrors.RenameSnapshotError = true
	case "LinkSnapshotError":
		mock.InducedErrors.LinkSnapshotError = true
	case "SnapshotNotLicensed":
		mock.InducedErrors.SnapshotNotLicensed = true
	case "InvalidResponse":
		mock.InducedErrors.InvalidResponse = true
	case "UnisphereMismatchError":
		mock.InducedErrors.UnisphereMismatchError = true
	case "TargetNotDefinedError":
		mock.InducedErrors.TargetNotDefinedError = true
	case "SnapshotExpired":
		mock.InducedErrors.SnapshotExpired = true
	case "GetSymVolumeError":
		mock.InducedErrors.GetSymVolumeError = true
	case "InvalidSnapshotName":
		mock.InducedErrors.InvalidSnapshotName = true
	case "GetVolSnapsError":
		mock.InducedErrors.GetVolSnapsError = true
	case "GetPrivVolumeByIDError":
		mock.InducedErrors.GetPrivVolumeByIDError = true
	case "ExpandVolumeError":
		mock.InducedErrors.ExpandVolumeError = true
	case "MaxSnapSessionError":
		mock.InducedErrors.MaxSnapSessionError = true

	case "NoSymlinkForNodePublish":
		cmd := exec.Command("rm", "-rf", nodePublishSymlinkDir)
		_, err := cmd.CombinedOutput()
		if err != nil {
			return err
		}
	case "NoBlockDevForNodePublish":
		unitTestEmulateBlockDevice = false
		cmd := exec.Command("rm", nodePublishBlockDevicePath)
		_, err := cmd.CombinedOutput()
		if err != nil {
			return nil
		}
	case "TargetNotCreatedForNodePublish":
		err := os.Remove(datafile)
		if err != nil {
			return nil
		}
		cmd := exec.Command("rm", "-rf", datadir)
		_, err = cmd.CombinedOutput()
		if err != nil {
			return err
		}
	case "PrivateDirectoryNotExistForNodePublish":
		f.service.privDir = "xxx/yyy"
	case "BlockMkfilePrivateDirectoryNodePublish":
		f.service.privDir = datafile
	case "NodePublishNoVolumeCapability":
		f.nodePublishVolumeRequest.VolumeCapability = nil
	case "NodePublishNoAccessMode":
		f.nodePublishVolumeRequest.VolumeCapability.AccessMode = nil
	case "NodePublishNoAccessType":
		f.nodePublishVolumeRequest.VolumeCapability.AccessType = nil
	case "NodePublishNoTargetPath":
		f.nodePublishVolumeRequest.TargetPath = ""
		f.nodePublishVolumeRequest.StagingTargetPath = ""
	case "NodePublishBlockTargetNotFile":
		f.nodePublishVolumeRequest.TargetPath = datadir
	case "NodePublishFileTargetNotDir":
		f.nodePublishVolumeRequest.TargetPath = datafile
	case "GOFSMockBindMountError":
		gofsutil.GOFSMock.InduceBindMountError = true
	case "GOFSMockDevMountsError":
		gofsutil.GOFSMock.InduceDevMountsError = true
	case "GOFSMockMountError":
		gofsutil.GOFSMock.InduceMountError = true
	case "GOFSMockGetMountsError":
		gofsutil.GOFSMock.InduceGetMountsError = true
	case "GOFSMockUnmountError":
		gofsutil.GOFSMock.InduceUnmountError = true
	case "GOFSMockGetDiskFormatError":
		gofsutil.GOFSMock.InduceGetDiskFormatError = true
	case "GOFSMockGetDiskFormatType":
		gofsutil.GOFSMock.InduceGetDiskFormatType = "unknown-fs"
	case "GOFSMockFormatError":
		gofsutil.GOFSMock.InduceFormatError = true
	case "GOFSWWNToDevicePathError":
		gofsutil.GOFSMock.InduceWWNToDevicePathError = true
	case "GOFSTargetIPLUNToDeviceError":
		gofsutil.GOFSMock.InduceTargetIPLUNToDeviceError = true
	case "GOFSRemoveBlockDeviceError":
		gofsutil.GOFSMock.InduceRemoveBlockDeviceError = true
	case "GOFSMultipathCommandError":
		gofsutil.GOFSMock.InduceMultipathCommandError = true
	case "GOFSInduceGetMountInfoFromDeviceError":
		gofsutil.GOFSMock.InduceGetMountInfoFromDeviceError = true
	case "GOFSInduceDeviceRescanError":
		gofsutil.GOFSMock.InduceDeviceRescanError = true
	case "GOFSInduceResizeMultipathError":
		gofsutil.GOFSMock.InduceResizeMultipathError = true
	case "GOFSInduceFSTypeError":
		gofsutil.GOFSMock.InduceFSTypeError = true
	case "GOFSInduceResizeFSError":
		gofsutil.GOFSMock.InduceResizeFSError = true
	case "GOISCSIDiscoveryError":
		goiscsi.GOISCSIMock.InduceDiscoveryError = true
	case "GOISCSIRescanError":
		goiscsi.GOISCSIMock.InduceRescanError = true
	case "InduceSetCHAPError":
		goiscsi.GOISCSIMock.InduceSetCHAPError = true
	case "InduceLoginError":
		goiscsi.GOISCSIMock.InduceLoginError = true
	case "NodeUnpublishNoTargetPath":
		f.nodePublishVolumeRequest.TargetPath = ""
		f.nodePublishVolumeRequest.StagingTargetPath = ""
	case "NodeUnpublishBadVolume":
		f.nodePublishVolumeRequest.VolumeId = volume0
	case "NodePublishRequestReadOnly":
		f.nodePublishVolumeRequest.Readonly = true
	case "PrivMountAlreadyMounted":
		mkdir(nodePublishPrivateDir + "/" + volume1)
		mnt := gofsutil.Info{
			Device: nodePublishSymlinkDir + "/wwn-0x" + nodePublishWWN,
			Path:   nodePublishPrivateDir + "/" + volume1,
			Source: nodePublishSymlinkDir + "/wwn-0x" + nodePublishWWN,
		}
		gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
		fmt.Printf("GOFSMockMounts: %#v\n", gofsutil.GOFSMockMounts)
	case "PrivMountByDifferentDev":
		mkdir(nodePublishPrivateDir + "/" + volume1)
		mnt := gofsutil.Info{
			Device: altPublishBlockDevicePath,
			Path:   nodePublishPrivateDir + "/" + volume1,
			Source: altPublishBlockDevicePath,
		}
		gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
		fmt.Printf("GOFSMockMounts: %#v\n", gofsutil.GOFSMockMounts)
	case "PrivMountByDifferentDir":
		mkdir(datadir + "/" + "xxx")
		mnt := gofsutil.Info{
			Device: nodePublishSymlinkDir + "/wwn-0x" + nodePublishWWN,
			Path:   datadir + "/" + "xxx",
			Source: nodePublishSymlinkDir + "/wwn-0x" + nodePublishWWN,
		}
		gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
		fmt.Printf("GOFSMockMounts: %#v\n", gofsutil.GOFSMockMounts)
	case "MountTargetAlreadyMounted":
		mkdir(datadir)
		mnt := gofsutil.Info{
			Device: nodePublishSymlinkDir + "/wwn-0x" + nodePublishWWN,
			Path:   datadir,
			Source: nodePublishSymlinkDir + "/wwn-0x" + nodePublishWWN,
		}
		gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
		fmt.Printf("GOFSMockMounts: %#v\n", gofsutil.GOFSMockMounts)
	case "BadVolumeIdentifier":
		inducedErrors.badVolumeIdentifier = true
	case "InvalidVolumeID":
		inducedErrors.invalidVolumeID = true
	case "NoVolumeID":
		inducedErrors.noVolumeID = true
	case "DifferentVolumeID":
		inducedErrors.differentVolumeID = true
	case "UnspecifiedNodeName":
		f.service.opts.NodeName = ""
	case "NoArray":
		inducedErrors.noSymID = true
	case "NoNodeName":
		inducedErrors.noNodeName = true
	case "NoIQNs":
		inducedErrors.noIQNs = true
	case "RescanError":
		gofsutil.GOFSMock.InduceRescanError = true
		inducedErrors.rescanError = true
	case "GobrickConnectError":
		mockGobrickInducedErrors.ConnectVolumeError = true
	case "GobrickDisconnectError":
		mockGobrickInducedErrors.DisconnectVolumeError = true
	case "ListUnitsError":
		mockgosystemdInducedErrors.ListUnitsError = true
	case "ISCSIDInactiveError":
		mockgosystemdInducedErrors.ISCSIDInactiveError = true
	case "StartUnitError":
		mockgosystemdInducedErrors.ISCSIDInactiveError = true
		mockgosystemdInducedErrors.StartUnitError = true
	case "ListUnitISCSIDNotPresentError":
		mockgosystemdInducedErrors.ListUnitISCSIDNotPresentError = true
	case "StartUnitMaskedError":
		mockgosystemdInducedErrors.ISCSIDInactiveError = true
		mockgosystemdInducedErrors.StartUnitMaskedError = true
	case "JobFailure":
		mockgosystemdInducedErrors.ISCSIDInactiveError = true
		mockgosystemdInducedErrors.JobFailure = true
	case "InduceOverloadError":
		induceOverloadError = true
	case "InducePendingError":
		inducePendingError = true
	case "InvalidateNodeID":
		f.iInvalidateTheNodeID()
	case "none":
		return nil
	default:
		return fmt.Errorf("Don't know how to induce error %q", errtype)
	}
	return nil
}

func (f *feature) getControllerPublishVolumeRequest(accessType, nodeID string) *csi.ControllerPublishVolumeRequest {
	capability := new(csi.VolumeCapability)
	block := new(csi.VolumeCapability_Block)
	block.Block = new(csi.VolumeCapability_BlockVolume)
	if f.useAccessTypeMount {
		mountVolume := new(csi.VolumeCapability_MountVolume)
		mountVolume.FsType = "xfs"
		mountVolume.MountFlags = make([]string, 0)
		mount := new(csi.VolumeCapability_Mount)
		mount.Mount = mountVolume
		capability.AccessType = mount
	} else {
		capability.AccessType = block
	}
	accessMode := new(csi.VolumeCapability_AccessMode)
	switch accessType {
	case "single-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
		break
	case "multiple-reader":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY
		break
	case "multiple-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER
		break
	case "unknown":
		accessMode.Mode = csi.VolumeCapability_AccessMode_UNKNOWN
		break
	}
	if !f.omitAccessMode {
		capability.AccessMode = accessMode
	}
	fmt.Printf("capability.AccessType %v\n", capability.AccessType)
	fmt.Printf("capability.AccessMode %v\n", capability.AccessMode)
	req := new(csi.ControllerPublishVolumeRequest)
	if !inducedErrors.noVolumeID {
		if inducedErrors.invalidVolumeID || f.createVolumeResponse == nil {
			req.VolumeId = "000-000"
		} else {
			req.VolumeId = f.volumeID
		}
	}
	if !f.noNodeID {
		req.NodeId = nodeID
	}
	req.Readonly = false
	if !f.omitVolumeCapability {
		req.VolumeCapability = capability
	}
	// add in the context
	attributes := map[string]string{}
	attributes[StoragePoolParam] = mock.DefaultStoragePool
	attributes[ServiceLevelParam] = "Bronze"
	req.VolumeContext = attributes
	return req
}

func (f *feature) getControllerListVolumesRequest(maxEntries int32, startingToken string) *csi.ListVolumesRequest {
	return &csi.ListVolumesRequest{
		MaxEntries:    maxEntries,
		StartingToken: startingToken,
	}
}

func (f *feature) getControllerDeleteVolumeRequest(accessType string) *csi.DeleteVolumeRequest {
	capability := new(csi.VolumeCapability)
	block := new(csi.VolumeCapability_Block)
	block.Block = new(csi.VolumeCapability_BlockVolume)
	if f.useAccessTypeMount {
		mountVolume := new(csi.VolumeCapability_MountVolume)
		mountVolume.FsType = "xfs"
		mountVolume.MountFlags = make([]string, 0)
		mount := new(csi.VolumeCapability_Mount)
		mount.Mount = mountVolume
		capability.AccessType = mount
	} else {
		capability.AccessType = block
	}
	accessMode := new(csi.VolumeCapability_AccessMode)
	switch accessType {
	case "single-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
		break
	case "multiple-reader":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY
		break
	case "multiple-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER
		break
	case "unknown":
		accessMode.Mode = csi.VolumeCapability_AccessMode_UNKNOWN
		break
	}
	if !f.omitAccessMode {
		capability.AccessMode = accessMode
	}
	fmt.Printf("capability.AccessType %v\n", capability.AccessType)
	fmt.Printf("capability.AccessMode %v\n", capability.AccessMode)
	req := new(csi.DeleteVolumeRequest)
	if !inducedErrors.noVolumeID {
		if inducedErrors.invalidVolumeID {
			req.VolumeId = f.service.createCSIVolumeID(f.service.getClusterPrefix(), goodVolumeName, f.symmetrixID, "99999")
		} else {
			if f.volumeID != "" {
				req.VolumeId = f.volumeID
			} else {
				req.VolumeId = f.service.createCSIVolumeID(f.service.getClusterPrefix(), goodVolumeName, f.symmetrixID, goodVolumeID)
			}
		}
	}
	return req
}

func (f *feature) iHaveANodeWithInitiatorsWithMaskingView(nodeID, initList string) error {
	f.service.opts.NodeName = nodeID
	transportProtocol := f.service.opts.TransportProtocol
	if transportProtocol == "FC" {
		f.hostID, f.sgID, f.mvID = f.service.GetFCHostSGAndMVIDFromNodeID(nodeID)
		initiator := defaultFcInitiatorWWN + nodeID + initList
		initiators := []string{initiator}
		initID := defaultFCDirPort + ":" + initiator
		mock.AddInitiator(initID, initiator, "Fibre", []string{defaultFCDirPort}, "")
		mock.AddHost(f.hostID, "Fibre", initiators)
		mock.AddStorageGroup(f.sgID, "", "")
		portGroupID := ""
		if f.selectedPortGroup != "" {
			portGroupID = f.selectedPortGroup
		} else {
			portGroupID = "fc_ports"
		}
		mock.AddMaskingView(f.mvID, f.sgID, f.hostID, portGroupID)
	} else {
		f.hostID, f.sgID, f.mvID = f.service.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
		initiator := defaultIscsiInitiator + nodeID + initList
		initiators := []string{initiator}
		initID := defaultISCSIDirPort1 + ":" + initiator
		mock.AddInitiator(initID, initiator, "GigE", []string{defaultISCSIDirPort1}, "")
		mock.AddHost(f.hostID, "iSCSI", initiators)
		mock.AddStorageGroup(f.sgID, "", "")
		portGroupID := ""
		if f.selectedPortGroup != "" {
			portGroupID = f.selectedPortGroup
		} else {
			portGroupID = "iscsi_ports"
		}
		mock.AddMaskingView(f.mvID, f.sgID, f.hostID, portGroupID)
	}
	return nil
}
func (f *feature) iHaveANodeWithMaskingView(nodeID string) error {
	f.service.opts.NodeName = nodeID
	transportProtocol := f.service.opts.TransportProtocol
	if nodeID == "none" {
		return nil
	}
	if transportProtocol == "FC" {
		f.hostID, f.sgID, f.mvID = f.service.GetFCHostSGAndMVIDFromNodeID(nodeID)
		initiator := defaultFcInitiatorWWN
		initiators := []string{initiator}
		initID := defaultFCDirPort + ":" + initiator
		mock.AddInitiator(initID, initiator, "Fibre", []string{defaultFCDirPort}, "")
		mock.AddHost(f.hostID, "Fibre", initiators)
		mock.AddStorageGroup(f.sgID, "", "")
		portGroupID := ""
		if f.selectedPortGroup != "" {
			portGroupID = f.selectedPortGroup
		} else {
			portGroupID = "fc_ports"
		}
		mock.AddPortGroup(portGroupID, "Fibre", []string{defaultFCDirPort})
		mock.AddMaskingView(f.mvID, f.sgID, f.hostID, portGroupID)
	} else {
		f.hostID, f.sgID, f.mvID = f.service.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
		initiator := defaultIscsiInitiator
		initiators := []string{initiator}
		initID := defaultISCSIDirPort1 + ":" + initiator
		mock.AddInitiator(initID, initiator, "GigE", []string{defaultISCSIDirPort1}, "")
		mock.AddHost(f.hostID, "iSCSI", initiators)
		mock.AddStorageGroup(f.sgID, "", "")
		portGroupID := ""
		if f.selectedPortGroup != "" {
			portGroupID = f.selectedPortGroup
		} else {
			portGroupID = "iscsi_ports"
		}
		mock.AddPortGroup(portGroupID, "ISCSI", []string{defaultISCSIDirPort1, defaultISCSIDirPort2})
		mock.AddMaskingView(f.mvID, f.sgID, f.hostID, portGroupID)
	}
	return nil
}

func (f *feature) iHaveANodeWithHost(nodeID string) error {
	transportProtocol := f.service.opts.TransportProtocol
	if transportProtocol == "FC" {
		f.hostID, _, _ = f.service.GetFCHostSGAndMVIDFromNodeID(nodeID)
		initiator := defaultFcInitiatorWWN
		initiators := []string{initiator}
		initID := defaultFCDirPort + ":" + initiator
		mock.AddInitiator(initID, initiator, "Fibre", []string{defaultFCDirPort}, "")
		mock.AddHost(f.hostID, "Fibre", initiators)
	} else {
		f.hostID, _, _ = f.service.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
		initiator := defaultIscsiInitiator
		initiators := []string{initiator}
		initID := defaultISCSIDirPort1 + ":" + initiator
		mock.AddInitiator(initID, initiator, "GigE", []string{defaultISCSIDirPort1}, "")
		mock.AddHost(f.hostID, "iSCSI", initiators)
	}
	return nil
}

func (f *feature) iHaveANodeWithStorageGroup(nodeID string) error {
	_, f.sgID, _ = f.service.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	mock.AddStorageGroup(f.sgID, "", "")
	return nil
}

func (f *feature) iHaveANodeWithAFastManagedMaskingView(nodeID string) error {
	f.hostID, _, f.mvID = f.service.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	f.sgID = nodeID + "-Diamond-SRP_1-SG"
	initiator := defaultIscsiInitiator
	initiators := []string{initiator}
	initID := defaultISCSIDirPort1 + ":" + initiator
	mock.AddInitiator(initID, initiator, "GigE", []string{defaultISCSIDirPort1}, "")
	mock.AddHost(f.hostID, "iSCSI", initiators)
	mock.AddStorageGroup(f.sgID, "SRP_1", "Diamond")
	mock.AddMaskingView(f.mvID, f.sgID, f.hostID, f.selectedPortGroup)
	return nil
}

func (f *feature) iHaveANodeWithFastManagedStorageGroup(nodeID string) error {
	_, f.sgID, _ = f.service.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	mock.AddStorageGroup(f.sgID, "SRP_1", "Diamond")
	return nil
}

func (f *feature) iHaveANodeWithHostWithInitiatorMappedToMultiplePorts(nodeID string) error {
	f.hostID, _, _ = f.service.GetFCHostSGAndMVIDFromNodeID(nodeID)
	initiator := defaultFcInitiatorWWN
	initiators := []string{initiator}
	initID1 := "FA-1D:4" + ":" + initiator
	initID2 := "FA-1D:5" + ":" + initiator
	initID3 := "FA-2D:1" + ":" + initiator
	initID4 := "FA-3E:2" + ":" + initiator
	initID5 := "FA-4D:5" + ":" + initiator
	initID6 := "FA-4D:6" + ":" + initiator
	initID7 := "FA-3D:3" + ":" + initiator
	mock.AddInitiator(initID1, initiator, "Fibre", []string{"FA-1D:4"}, "")
	mock.AddInitiator(initID2, initiator, "Fibre", []string{"FA-1D:5"}, "")
	mock.AddInitiator(initID3, initiator, "Fibre", []string{"FA-2D:1"}, "")
	mock.AddInitiator(initID4, initiator, "Fibre", []string{"FA-3E:2"}, "")
	mock.AddInitiator(initID5, initiator, "Fibre", []string{"FA-4D:5"}, "")
	mock.AddInitiator(initID6, initiator, "Fibre", []string{"FA-4D:6"}, "")
	mock.AddInitiator(initID7, initiator, "Fibre", []string{"FA-3D:3"}, "")
	mock.AddHost(f.hostID, "Fibre", initiators)
	return nil
}

func (f *feature) iHaveAFCPortGroup(portGroupID string) error {
	dirPort := defaultFCDirPort
	tempPGID := "csi-" + f.service.getClusterPrefix() + "-" + portGroupID
	mock.AddPortGroup(tempPGID, "Fibre", []string{dirPort})
	return nil
}

func (f *feature) iAddTheVolumeTo(nodeID string) error {
	volumeIdentifier, _, devID, _ := f.service.parseCsiID(f.volumeID)
	mock.AddOneVolumeToStorageGroup(devID, volumeIdentifier, f.sgID, 1)
	return nil
}

func (f *feature) iCallPublishVolumeWithTo(accessMode, nodeID string) error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := f.publishVolumeRequest
	if f.publishVolumeRequest == nil {
		req = f.getControllerPublishVolumeRequest(accessMode, nodeID)
		f.publishVolumeRequest = req
	}
	log.Printf("Calling controllerPublishVolume")
	f.publishVolumeResponse, f.err = f.service.ControllerPublishVolume(ctx, req)
	if f.err != nil {
		log.Printf("PublishVolume call failed: %s\n", f.err.Error())
	}
	f.publishVolumeRequest = nil
	return nil
}

func (f *feature) aValidPublishVolumeResponseIsReturned() error {
	if f.err != nil {
		return errors.New("PublishVolume returned error: " + f.err.Error())
	}
	if f.publishVolumeResponse == nil {
		return errors.New("No PublishVolumeResponse returned")
	}
	for key, value := range f.publishVolumeResponse.PublishContext {
		fmt.Printf("PublishContext %s: %s", key, value)
	}
	return nil
}

func (f *feature) aValidVolume() error {
	devID := goodVolumeID
	volumeIdentifier := csiPrefix + f.service.getClusterPrefix() + "-" + goodVolumeName
	sgList := make([]string, 1)
	sgList[0] = defaultStorageGroup
	mock.AddStorageGroup(defaultStorageGroup, "SRP_1", "Optimized")
	mock.AddOneVolumeToStorageGroup(devID, volumeIdentifier, defaultStorageGroup, 1)
	f.volumeID = f.service.createCSIVolumeID(f.service.getClusterPrefix(), goodVolumeName, f.symmetrixID, goodVolumeID)
	return nil
}

func (f *feature) aValidVolumeWithSizeOfCYL(nCYL int) error {
	devID := goodVolumeID
	volumeIdentifier := csiPrefix + f.service.getClusterPrefix() + "-" + goodVolumeName
	sgList := make([]string, 1)
	sgList[0] = defaultStorageGroup
	mock.AddStorageGroup(defaultStorageGroup, "SRP_1", "Optimized")
	mock.AddOneVolumeToStorageGroup(devID, volumeIdentifier, defaultStorageGroup, nCYL)
	f.volumeID = f.service.createCSIVolumeID(f.service.getClusterPrefix(), goodVolumeName, f.symmetrixID, goodVolumeID)
	return nil
}

func (f *feature) anInvalidVolume() error {
	inducedErrors.invalidVolumeID = true
	return nil
}

func (f *feature) anInvalidSnapshot() error {
	inducedErrors.invalidSnapID = true
	return nil
}

func (f *feature) noVolume() error {
	inducedErrors.noVolumeID = true
	return nil
}

func (f *feature) noNode() error {
	f.noNodeID = true
	return nil
}

func (f *feature) noVolumeCapability() error {
	f.omitVolumeCapability = true
	return nil
}

func (f *feature) noAccessMode() error {
	f.omitAccessMode = true
	return nil
}

func (f *feature) thenIUseADifferentNodeID() error {
	f.publishVolumeRequest.NodeId = altNodeID
	if f.unpublishVolumeRequest != nil {
		f.unpublishVolumeRequest.NodeId = altNodeID
	}
	return nil
}

func (f *feature) iUseAccessTypeMount() error {
	f.useAccessTypeMount = true
	return nil
}

func (f *feature) noErrorWasReceived() error {
	if f.err != nil {
		return f.err
	}
	return nil
}

func (f *feature) getControllerUnpublishVolumeRequest(nodeID string) *csi.ControllerUnpublishVolumeRequest {
	req := new(csi.ControllerUnpublishVolumeRequest)
	if !inducedErrors.noVolumeID {
		if inducedErrors.invalidVolumeID {
			req.VolumeId = "9999-9999"
		} else {
			if !f.noNodeID {
				req.VolumeId = f.volumeID
			}
		}
	}
	if !f.noNodeID {
		req.NodeId = nodeID
	}
	return req
}

func (f *feature) iCallUnpublishVolumeFrom(nodeID string) error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := f.unpublishVolumeRequest
	if f.unpublishVolumeRequest == nil {
		req = f.getControllerUnpublishVolumeRequest(nodeID)
		f.unpublishVolumeRequest = req
	}
	log.Printf("Calling controllerUnpublishVolume: %s", req.VolumeId)
	f.unpublishVolumeResponse, f.err = f.service.ControllerUnpublishVolume(ctx, req)
	if f.err != nil {
		log.Printf("UnpublishVolume call failed: %s\n", f.err.Error())
	}
	return nil
}

func (f *feature) aValidUnpublishVolumeResponseIsReturned() error {
	if f.unpublishVolumeResponse == nil {
		return errors.New("expected unpublishVolumeResponse (with no contents)but did not get one")
	}
	return nil
}

func (f *feature) iCallNodeGetInfo() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.NodeGetInfoRequest)
	f.nodeGetInfoResponse, f.err = f.service.NodeGetInfo(ctx, req)
	return nil
}

func (f *feature) aValidNodeGetInfoResponseIsReturned() error {
	if f.err != nil {
		return f.err
	}
	if f.nodeGetInfoResponse.NodeId == "" {
		return errors.New("expected NodeGetInfoResponse to contain NodeID but it was null")
	}
	if f.nodeGetInfoResponse.MaxVolumesPerNode != 0 {
		return errors.New("expected NodeGetInfoResponse MaxVolumesPerNode to be 0")
	}
	fmt.Printf("NodeID %s\n", f.nodeGetInfoResponse.NodeId)
	return nil
}

func (f *feature) iCallDeleteVolumeWith(arg1 string) error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := f.deleteVolumeRequest
	if f.deleteVolumeRequest == nil {
		req = f.getControllerDeleteVolumeRequest(arg1)
		f.deleteVolumeRequest = req
	}
	log.Printf("Calling DeleteVolume")
	f.deleteVolumeResponse, f.err = f.service.DeleteVolume(ctx, req)
	if f.err != nil {
		log.Printf("DeleteVolume called failed: %s\n", f.err.Error())
	}
	return nil
}

func (f *feature) aValidDeleteVolumeResponseIsReturned() error {
	if f.deleteVolumeResponse == nil {
		return errors.New("expected deleteVolumeResponse (with no contents) but did not get one")
	}
	return nil
}

func (f *feature) aValidListVolumesResponseIsReturned() error {
	if f.listVolumesResponse == nil {
		return errors.New("expected a non-nil listVolumesResponse, but it was nil")
	}
	return nil
}

func getTypicalCapacityRequest(valid bool) *csi.GetCapacityRequest {
	req := new(csi.GetCapacityRequest)
	// Construct the volume capabilities
	capability := new(csi.VolumeCapability)
	// Set FS type to mount volume
	mount := new(csi.VolumeCapability_MountVolume)
	accessType := new(csi.VolumeCapability_Mount)
	accessType.Mount = mount
	capability.AccessType = accessType
	// A single mode writer
	accessMode := new(csi.VolumeCapability_AccessMode)
	if valid {
		accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	} else {
		accessMode.Mode = csi.VolumeCapability_AccessMode_UNKNOWN
	}
	capability.AccessMode = accessMode
	capabilities := make([]*csi.VolumeCapability, 0)
	capabilities = append(capabilities, capability)
	req.VolumeCapabilities = capabilities
	return req
}

func (f *feature) iCallGetCapacityWithStoragePool(srpID string) error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := getTypicalCapacityRequest(true)
	parameters := make(map[string]string)
	parameters[StoragePoolParam] = srpID
	parameters[SymmetrixIDParam] = f.symmetrixID
	req.Parameters = parameters

	fmt.Printf("Calling GetCapacity with %s and %s\n",
		req.Parameters[StoragePoolParam], req.Parameters[SymmetrixIDParam])
	f.getCapacityResponse, f.err = f.service.GetCapacity(ctx, req)
	if f.err != nil {
		log.Printf("GetCapacity call failed: %s\n", f.err.Error())
		return nil
	}
	return nil
}

func (f *feature) iCallGetCapacityWithoutSymmetrixID() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := getTypicalCapacityRequest(true)
	parameters := make(map[string]string)
	parameters[StoragePoolParam] = mock.DefaultStoragePool
	req.Parameters = parameters
	f.getCapacityResponse, f.err = f.service.GetCapacity(ctx, req)
	if f.err != nil {
		log.Printf("GetCapacity call failed: %s\n", f.err.Error())
		return nil
	}
	return nil
}

func (f *feature) iCallGetCapacityWithoutParameters() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := getTypicalCapacityRequest(true)
	req.Parameters = nil
	f.getCapacityResponse, f.err = f.service.GetCapacity(ctx, req)
	if f.err != nil {
		log.Printf("GetCapacity call failed: %s\n", f.err.Error())
		return nil
	}
	return nil
}

func (f *feature) iCallGetCapacityWithInvalidCapabilities() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := getTypicalCapacityRequest(false)
	f.getCapacityResponse, f.err = f.service.GetCapacity(ctx, req)
	if f.err != nil {
		log.Printf("GetCapacity call failed: %s\n", f.err.Error())
		return nil
	}
	return nil
}

func (f *feature) aValidGetCapacityResponseIsReturned() error {
	if f.err != nil {
		return f.err
	}
	if f.getCapacityResponse == nil {
		return errors.New("Received null response to GetCapacity")
	}
	if f.getCapacityResponse.AvailableCapacity <= 0 {
		return errors.New("Expected AvailableCapacity to be positive")
	}
	fmt.Printf("Available capacity: %d\n", f.getCapacityResponse.AvailableCapacity)
	return nil
}

func (f *feature) iCallControllerGetCapabilities() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.ControllerGetCapabilitiesRequest)
	log.Printf("Calling ControllerGetCapabilities")
	f.controllerGetCapabilitiesResponse, f.err = f.service.ControllerGetCapabilities(ctx, req)
	if f.err != nil {
		log.Printf("ControllerGetCapabilities call failed: %s\n", f.err.Error())
		return f.err
	}
	return nil
}

// parseListVolumesTable parses the given DataTable and ensures that it follows the
// format:
// | max_entries | starting_token |
// | <number>    | <string>       |
func parseListVolumesTable(dt *gherkin.DataTable) (int32, string, error) {
	if c := len(dt.Rows); c != 2 {
		return 0, "", fmt.Errorf("expected table with header row and single value row, got %d row(s)", c)
	}

	var (
		maxEntries    int32
		startingToken string
	)
	for i, v := range dt.Rows[0].Cells {
		switch h := v.Value; h {
		case "max_entries":
			str := dt.Rows[1].Cells[i].Value
			n, err := strconv.Atoi(str)
			if err != nil {
				return 0, "", fmt.Errorf("expected a valid number for max_entries, got %v", err)
			}
			maxEntries = int32(n)
		case "starting_token":
			startingToken = dt.Rows[1].Cells[i].Value
		default:
			return 0, "", fmt.Errorf(`want headers ["max_entries", "starting_token"], got %q`, h)
		}
	}

	return maxEntries, startingToken, nil
}

// iCallListVolumesAgainWith nils out the previous request before delegating
// to iCallListVolumesWith with the same table data.  This simulates multiple
// calls to ListVolume for the purpose of testing the pagination token.
func (f *feature) iCallListVolumesAgainWith(dt *gherkin.DataTable) error {
	f.listVolumesRequest = nil
	return f.iCallListVolumesWith(dt)
}

func (f *feature) iCallListVolumesWith(dt *gherkin.DataTable) error {
	maxEntries, startingToken, err := parseListVolumesTable(dt)
	if err != nil {
		return err
	}

	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := f.listVolumesRequest
	if f.listVolumesRequest == nil {
		switch st := startingToken; st {
		case "none":
			startingToken = ""
		case "next":
			startingToken = f.listVolumesNextTokenCache
		case "invalid":
			startingToken = "invalid-token"
		case "larger":
			startingToken = "9999"
		default:
			return fmt.Errorf(`want start token of "next", "none", "invalid", "larger", got %q`, st)
		}
		req = f.getControllerListVolumesRequest(maxEntries, startingToken)
		f.listVolumesRequest = req
	}
	log.Printf("Calling ListVolumes with req=%+v", f.listVolumesRequest)
	f.listVolumesResponse, f.err = f.service.ListVolumes(ctx, req)
	if f.err != nil {
		log.Printf("ListVolume called failed: %s\n", f.err.Error())
	} else if f.listVolumesResponse == nil {
		log.Printf("Received null response from ListVolumes")
	} else {
		f.listVolumesNextTokenCache = f.listVolumesResponse.NextToken
	}
	return nil
}

func (f *feature) aValidControllerGetCapabilitiesResponseIsReturned() error {
	rep := f.controllerGetCapabilitiesResponse
	if rep != nil {
		if rep.Capabilities == nil {
			return errors.New("no capabilities returned in ControllerGetCapabilitiesResponse")
		}
		count := 0
		for _, cap := range rep.Capabilities {
			typex := cap.GetRpc().Type
			switch typex {
			case csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME:
				count = count + 1
			case csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME:
				count = count + 1
			case csi.ControllerServiceCapability_RPC_GET_CAPACITY:
				count = count + 1
			case csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT:
				count = count + 1
			case csi.ControllerServiceCapability_RPC_CLONE_VOLUME:
				count = count + 1
			case csi.ControllerServiceCapability_RPC_EXPAND_VOLUME:
				count = count + 1
			default:
				return fmt.Errorf("received unexpected capability: %v", typex)
			}
		}
		if count != 6 {
			return fmt.Errorf("Did not retrieve all the expected capabilities")
		}
		return nil
	}
	return fmt.Errorf("expected ControllerGetCapabilitiesResponse but didn't get one")
}

func (f *feature) iCallValidateVolumeCapabilitiesWithVoltypeAccessFstype(voltype, access, fstype, pool, level string) error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.ValidateVolumeCapabilitiesRequest)
	if inducedErrors.invalidVolumeID || f.volumeID == "" {
		req.VolumeId = "000-000"
	} else if inducedErrors.differentVolumeID {
		req.VolumeId = f.service.createCSIVolumeID(f.service.getClusterPrefix(), altVolumeName, f.symmetrixID, goodVolumeID)
	} else {
		req.VolumeId = f.volumeID
	}
	// Construct the volume capabilities
	capability := new(csi.VolumeCapability)
	switch voltype {
	case "block":
		block := new(csi.VolumeCapability_BlockVolume)
		accessType := new(csi.VolumeCapability_Block)
		accessType.Block = block
		capability.AccessType = accessType
	case "mount":
		mount := new(csi.VolumeCapability_MountVolume)
		accessType := new(csi.VolumeCapability_Mount)
		accessType.Mount = mount
		capability.AccessType = accessType
	}
	accessMode := new(csi.VolumeCapability_AccessMode)
	switch access {
	case "single-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	case "multi-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER
	case "multi-reader":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY
	case "multi-node-single-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER
	}
	capability.AccessMode = accessMode
	capabilities := make([]*csi.VolumeCapability, 0)
	capabilities = append(capabilities, capability)
	req.VolumeCapabilities = capabilities
	// add in the context
	attributes := map[string]string{}
	if pool != "" {
		attributes[StoragePoolParam] = pool
	}
	if level != "" {
		attributes[ServiceLevelParam] = level
	}
	req.VolumeContext = attributes

	log.Printf("Calling ValidateVolumeCapabilities")
	f.validateVolumeCapabilitiesResponse, f.err = f.service.ValidateVolumeCapabilities(ctx, req)
	if f.err != nil || f.validateVolumeCapabilitiesResponse == nil {
		return nil
	}
	if f.validateVolumeCapabilitiesResponse.Message != "" {
		f.err = errors.New(f.validateVolumeCapabilitiesResponse.Message)
	} else {
		// Validate we get a Confirmed structure with VolumeCapabilities
		if f.validateVolumeCapabilitiesResponse.Confirmed == nil {
			return errors.New("Expected ValidateVolumeCapabilities to have a Confirmed structure but it did not")
		}
		confirmed := f.validateVolumeCapabilitiesResponse.Confirmed
		if len(confirmed.VolumeCapabilities) <= 0 {
			return errors.New("Expected ValidateVolumeCapabilities to return the confirmed VolumeCapabilities but it did not")
		}
	}
	return nil
}

// thereAreValidVolumes creates the requested number of volumes
// for the test scenario, using a suffix.
func (f *feature) thereAreValidVolumes(n int) error {
	idTemplate := "11111%d"
	nameTemplate := "vol%d"
	mock.AddStorageGroup(defaultStorageGroup, "SRP_1", "Diamond")
	for i := 0; i < n; i++ {
		name := fmt.Sprintf(nameTemplate, i)
		id := fmt.Sprintf(idTemplate, i)
		mock.AddOneVolumeToStorageGroup(id, name, defaultStorageGroup, 1)
	}
	return nil
}

func (f *feature) volumesAreListed(expected int) error {
	if f.listVolumesResponse == nil {
		return fmt.Errorf("expected a non-nil list volume response, but got nil")
	}

	if actual := len(f.listVolumesResponse.Entries); actual != expected {
		return fmt.Errorf("expected %d volumes to have been listed, got %d", expected, actual)
	}
	return nil
}

func (f *feature) anInvalidListVolumesResponseIsReturned() error {
	if f.err == nil {
		return fmt.Errorf("expected error response, but couldn't find it")
	}
	return nil
}

func (f *feature) aCapabilityWithVoltypeAccessFstype(voltype, access, fstype string) error {
	// Construct the volume capabilities
	capability := new(csi.VolumeCapability)
	switch voltype {
	case "block":
		blockVolume := new(csi.VolumeCapability_BlockVolume)
		block := new(csi.VolumeCapability_Block)
		block.Block = blockVolume
		capability.AccessType = block
	case "mount":
		mountVolume := new(csi.VolumeCapability_MountVolume)
		mountVolume.FsType = fstype
		mountVolume.MountFlags = make([]string, 0)
		mount := new(csi.VolumeCapability_Mount)
		mount.Mount = mountVolume
		capability.AccessType = mount
	}
	accessMode := new(csi.VolumeCapability_AccessMode)
	switch access {
	case "single-reader":
		accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY
	case "single-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	case "multiple-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER
	case "multiple-reader":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY
	case "multiple-node-single-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER
	}
	capability.AccessMode = accessMode
	f.capabilities = make([]*csi.VolumeCapability, 0)
	f.capabilities = append(f.capabilities, capability)
	f.capability = capability
	return nil
}

func (f *feature) makeDevDirectories() error {
	var err error
	// Make the directories; on Windows these show up in test/dev/...
	_, err = os.Stat(nodePublishSymlinkDir)
	if err != nil {
		err = os.MkdirAll(nodePublishSymlinkDir, 0777)
		if err != nil {
			fmt.Printf("by-id: " + err.Error())
			return err
		}
	}

	_, err = os.Stat(nodePublishPathSymlinkDir)
	if err != nil {
		err = os.MkdirAll(nodePublishPathSymlinkDir, 0777)
		if err != nil {
			fmt.Printf("by-path: " + err.Error())
			return err
		}
	}

	// Remove the private staging directory
	cmd := exec.Command("rm", "-rf", nodePublishPrivateDir)
	_, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("error removing private staging directory")
		return err
	}
	fmt.Printf("removed private staging directory\n")

	// Remake the private staging directory
	err = os.MkdirAll(nodePublishPrivateDir, 0777)
	if err != nil {
		fmt.Printf("error creating private staging directory: " + err.Error())
		return err
	}
	f.service.privDir = nodePublishPrivateDir
	return nil
}

func (f *feature) aControllerPublishedVolume() error {
	var err error
	fmt.Printf("setting up dev directory, block device, and symlink\n")

	// Make the directories; on Windows these show up in test/dev/...
	f.makeDevDirectories()

	// Make the block device and alternate
	for _, dev := range allBlockDevices {
		_, err = os.Stat(dev)
		if err != nil {
			fmt.Printf("stat error: %s\n", err.Error())
			cmd := exec.Command("mknod", dev, "b", "0", "0")
			output, err := cmd.CombinedOutput()
			if err != nil {
				fmt.Printf("A error creating device node: %s\n", string(output))
			}
		}
	}

	// Make the symlink
	symlinkString := fmt.Sprintf("wwn-0x%s", nodePublishWWN)
	_, err = os.Stat(nodePublishSymlinkDir + "/" + symlinkString)
	if err != nil {
		cmdstring := fmt.Sprintf("cd %s; ln -s ../../%s %s", nodePublishSymlinkDir, nodePublishBlockDevice, symlinkString)
		cmd := exec.Command("sh", "-c", cmdstring)
		output, err := cmd.CombinedOutput()
		fmt.Printf("symlink output: %s\n", output)
		if err != nil {
			fmt.Printf("link: " + err.Error())
		}
	}

	// Make the gofsutil entry
	gofsutil.GOFSMockWWNToDevice[symlinkString] = nodePublishBlockDevicePath

	// Set the callback function
	gofsutil.GOFSRescanCallback = rescanCallback

	// Make the target directory if required
	_, err = os.Stat(datadir)
	if err != nil {
		err = os.MkdirAll(datadir, 0777)
		if err != nil {
			fmt.Printf("Couldn't make datadir: %s\n", datadir)
		}
	}

	// Make the target file if required
	_, err = os.Stat(datafile)
	if err != nil {
		file, err := os.Create(datafile)
		if err != nil {
			fmt.Printf("Couldn't make datafile: %s\n", datafile)
		} else {
			file.Close()
		}
	}

	// Empty WindowsMounts in gofsutil
	// gofsutil.GOFSMockMounts = gofsutil.GOFSMockMounts[:0]
	// Set variables in mount for unit testing
	unitTestEmulateBlockDevice = true
	return nil
}

func (f *feature) aControllerPublishedMultipathVolume() error {
	err := f.aControllerPublishedVolume()
	if err != nil {
		return err
	}
	// Make the block device and alternate
	_, err = os.Stat(nodePublishMultipathPath)
	if err != nil {
		fmt.Printf("stat error: %s\n", err.Error())
		cmd := exec.Command("mknod", nodePublishMultipathPath, "b", "0", "7")
		output, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Printf("B error creating device node: %s\n", string(output))
		}
	}

	// Make the symlink
	symlinkString := fmt.Sprintf("dm-uuid-mpath-3%s", nodePublishWWN)
	_, err = os.Stat(nodePublishSymlinkDir + "/" + symlinkString)
	if err != nil {
		cmdstring := fmt.Sprintf("cd %s; ln -s ../../%s %s", nodePublishSymlinkDir, nodePublishMultipathDevice, symlinkString)
		cmd := exec.Command("sh", "-c", cmdstring)
		output, err := cmd.CombinedOutput()
		fmt.Printf("symlink output: %s\n", output)
		if err != nil {
			fmt.Printf("link: " + err.Error())
		}
	}
	// Make the gofsutil entry
	gofsutil.GOFSWWNPath = "test/dev/disk/by-id/dm-uuid-mpath-3"
	gofsutil.MultipathDevDiskByIDPrefix = "test/dev/disk/by-id/dm-uuid-mpath-3"
	gofsutil.GOFSMockWWNToDevice[symlinkString] = nodePublishMultipathPath
	return nil
}

func rescanCallback(scanstring string) {
	if gofsutil.GOFSMockWWNToDevice == nil {
		gofsutil.GOFSMockWWNToDevice = make(map[string]string)
	}
	if inducedErrors.rescanError {
		return
	}
	switch scanstring {
	case "3":
		symlink := fmt.Sprintf("%s/wwn-0x%s", nodePublishSymlinkDir, nodePublishWWN)
		gofsutil.GOFSMockWWNToDevice[nodePublishWWN] = symlink
		fmt.Printf("gofsutilRescanCallback publishing %s to %s\n", nodePublishWWN, symlink)
	}
}

// getPortIdentifiers returns #portCount portsIDs attached together
func (f *feature) getPortIdentifiers(portCount int) string {
	portIDs := ""
	if f.service.opts.TransportProtocol == FcTransportProtocol {
		portIDs = strings.Repeat(fmt.Sprintf("%s,", defaultFcInitiator), portCount)
	} else {
		portIDs = strings.Repeat(fmt.Sprintf("%s,", defaultArrayTargetIQN), portCount)
	}
	return portIDs
}

func (f *feature) getNodePublishVolumeRequest() error {
	req := new(csi.NodePublishVolumeRequest)
	req.VolumeId = volume1
	volName, _, devID, err := f.service.parseCsiID(volume1)
	if err != nil {
		return errors.New("couldn't parse volume1")
	}
	//mock.NewVolume(devID, volName, 1000, make([]string, 0))
	mock.AddOneVolumeToStorageGroup(devID, volName, f.sgID, 1000)
	req.Readonly = false
	req.VolumeCapability = f.capability
	req.PublishContext = make(map[string]string)
	req.PublishContext[PublishContextDeviceWWN] = nodePublishWWN
	req.PublishContext[PublishContextLUNAddress] = nodePublishLUNID
	keyCount := 2  // holds the count of port identifiers set are there
	portCount := 3 // holds the count of port identifiers present in one set of key count
	req.PublishContext[PortIdentifierKeyCount] = strconv.Itoa(keyCount)
	for i := 1; i <= keyCount; i++ {
		portIdentifierKey := fmt.Sprintf("%s_%d", PortIdentifiers, i)
		req.PublishContext[portIdentifierKey] = f.getPortIdentifiers(portCount)
	}
	block := f.capability.GetBlock()
	if block != nil {
		req.TargetPath = datafile
	}
	mount := f.capability.GetMount()
	if mount != nil {
		req.TargetPath = datadir
	}
	req.StagingTargetPath = nodePublishPrivateDir
	req.VolumeContext = make(map[string]string)
	req.VolumeContext["VolumeId"] = req.VolumeId
	f.nodePublishVolumeRequest = req
	return nil
}

func (f *feature) iChangeTheTargetPath() error {
	// Make the target directory if required
	_, err := os.Stat(datadir2)
	if err != nil {
		err = os.MkdirAll(datadir2, 0777)
		if err != nil {
			fmt.Printf("Couldn't make datadir: %s\n", datadir2)
		}
	}

	// Make the target file if required
	_, err = os.Stat(datafile2)
	if err != nil {
		file, err := os.Create(datafile2)
		if err != nil {
			fmt.Printf("Couldn't make datafile: %s\n", datafile2)
		} else {
			file.Close()
		}
	}
	req := f.nodePublishVolumeRequest
	block := f.capability.GetBlock()
	if block != nil {
		req.TargetPath = datafile2
	}
	mount := f.capability.GetMount()
	if mount != nil {
		req.TargetPath = datadir2
	}
	return nil
}

func (f *feature) iMarkRequestReadOnly() error {
	f.nodePublishVolumeRequest.Readonly = true
	return nil
}

func (f *feature) iCallNodePublishVolume() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := f.nodePublishVolumeRequest
	if inducedErrors.noDeviceWWNError {
		req.PublishContext[PublishContextDeviceWWN] = ""
	}
	if inducedErrors.badVolumeIdentifier {
		req.VolumeId = "bad volume identifier"
	}
	if req == nil {
		_ = f.getNodePublishVolumeRequest()
		req = f.nodePublishVolumeRequest
	}
	fmt.Printf("Calling NodePublishVolume\n")
	_, err := f.service.NodePublishVolume(ctx, req)
	if err != nil {
		fmt.Printf("NodePublishVolume failed: %s\n", err.Error())
		if f.err == nil {
			f.err = err
		}
	} else {
		fmt.Printf("NodePublishVolume completed successfully\n")
	}
	return nil
}

func (f *feature) iCallNodeUnpublishVolume() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.NodeUnpublishVolumeRequest)
	req.VolumeId = f.nodePublishVolumeRequest.VolumeId
	req.TargetPath = f.nodePublishVolumeRequest.TargetPath
	if inducedErrors.badVolumeIdentifier {
		req.VolumeId = "bad volume identifier"
	}
	fmt.Printf("Calling NodeUnpublishVolume\n")
	_, err := f.service.NodeUnpublishVolume(ctx, req)
	if err != nil {
		fmt.Printf("NodeUnpublishVolume failed: %s\n", err.Error())
		if f.err == nil {
			f.err = err
		}
	} else {
		fmt.Printf("NodeUnpublishVolume completed successfully\n")
	}
	return nil
}

func (f *feature) thereAreNoRemainingMounts() error {
	if len(gofsutil.GOFSMockMounts) > 0 {
		return errors.New("expected all mounts to be removed but one or more remained")
	}
	return nil
}

func (f *feature) getTypicalEnviron() []string {
	stringSlice := make([]string, 0)
	stringSlice = append(stringSlice, EnvEndpoint+"=unix_sock")
	stringSlice = append(stringSlice, EnvUser+"=admin")
	stringSlice = append(stringSlice, EnvPassword+"=password")
	stringSlice = append(stringSlice, EnvNodeName+"=Node1")
	stringSlice = append(stringSlice, EnvPortGroups+"=PortGroup1,PortGroup2")
	stringSlice = append(stringSlice, EnvArrayWhitelist+"=")
	stringSlice = append(stringSlice, EnvThick+"=bad")
	stringSlice = append(stringSlice, EnvInsecure+"=true")
	stringSlice = append(stringSlice, EnvGrpcMaxThreads+"=1")
	stringSlice = append(stringSlice, "X_CSI_PRIVATE_MOUNT_DIR=/csi")
	return stringSlice
}

func (f *feature) iCallBeforeServe() error {
	ctxOSEnviron := interface{}("os.Environ")
	stringSlice := f.getTypicalEnviron()
	stringSlice = append(stringSlice, EnvClusterPrefix+"=TST")
	ctx := context.WithValue(context.Background(), ctxOSEnviron, stringSlice)
	listener, err := net.Listen("tcp", "127.0.0.1:65000")
	if err != nil {
		return err
	}
	f.err = f.service.BeforeServe(ctx, nil, listener)
	listener.Close()
	return nil
}

func (f *feature) iCallBeforeServeWithoutClusterPrefix() error {
	ctxOSEnviron := interface{}("os.Environ")
	stringSlice := f.getTypicalEnviron()
	ctx := context.WithValue(context.Background(), ctxOSEnviron, stringSlice)
	listener, err := net.Listen("tcp", "127.0.0.1:65000")
	if err != nil {
		return err
	}
	f.err = f.service.BeforeServe(ctx, nil, listener)
	listener.Close()
	return nil
}

func (f *feature) iCallBeforeServeWithAnInvalidClusterPrefix() error {
	ctxOSEnviron := interface{}("os.Environ")
	stringSlice := f.getTypicalEnviron()
	stringSlice = append(stringSlice, EnvClusterPrefix+"=LONG")
	ctx := context.WithValue(context.Background(), ctxOSEnviron, stringSlice)
	listener, err := net.Listen("tcp", "127.0.0.1:65000")
	if err != nil {
		return err
	}
	f.err = f.service.BeforeServe(ctx, nil, listener)
	listener.Close()
	return nil
}

func (f *feature) iCallNodeStageVolume() error {
	//	_ = f.getNodePublishVolumeRequest()
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.NodeStageVolumeRequest)
	req.VolumeId = f.nodePublishVolumeRequest.VolumeId
	req.PublishContext = f.nodePublishVolumeRequest.PublishContext
	req.StagingTargetPath = f.nodePublishVolumeRequest.StagingTargetPath
	req.VolumeCapability = f.nodePublishVolumeRequest.VolumeCapability
	req.VolumeContext = f.nodePublishVolumeRequest.VolumeContext
	if inducedErrors.badVolumeIdentifier {
		req.VolumeId = "bad volume identifier"
	}
	fmt.Printf("calling NodeStageVolume %#v\n", req)
	_, f.err = f.service.NodeStageVolume(ctx, req)
	return nil
}

func (f *feature) iCallControllerExpandVolume(nCYL int64) error {
	var req *csi.ControllerExpandVolumeRequest
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	if nCYL != 0 {
		req = &csi.ControllerExpandVolumeRequest{
			VolumeId:      f.volumeID,
			CapacityRange: &csi.CapacityRange{RequiredBytes: nCYL * cylinderSizeInBytes},
		}
	} else {
		req = &csi.ControllerExpandVolumeRequest{
			VolumeId: f.volumeID,
		}
	}
	if inducedErrors.noVolumeID {
		req.VolumeId = ""
	}
	_, f.err = f.service.ControllerExpandVolume(ctx, req)
	return nil
}

func (f *feature) iCallNodeExpandVolume(volPath string) error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := &csi.NodeExpandVolumeRequest{
		VolumeId:   f.volumeID,
		VolumePath: volPath,
	}
	if volPath != "" {
		if err := os.MkdirAll(volPath, 0777); err != nil {
			return err
		}
	}
	if inducedErrors.noVolumeID {
		req.VolumeId = ""
	}
	_, f.err = f.service.NodeExpandVolume(ctx, req)
	return nil
}

func (f *feature) iCallNodeUnstageVolume() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.NodeUnstageVolumeRequest)
	req.VolumeId = f.nodePublishVolumeRequest.VolumeId
	if inducedErrors.invalidVolumeID {
		req.VolumeId = "badVolumeID"
	}
	req.StagingTargetPath = f.nodePublishVolumeRequest.StagingTargetPath
	log.Printf("iCallNodeUnstageVolume %s %s", req.VolumeId, req.StagingTargetPath)
	_, f.err = f.service.NodeUnstageVolume(ctx, req)
	return nil
}

func (f *feature) iCallNodeGetCapabilities() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.NodeGetCapabilitiesRequest)
	f.nodeGetCapabilitiesResponse, f.err = f.service.NodeGetCapabilities(ctx, req)
	return nil
}

func (f *feature) aValidNodeGetCapabilitiesResponseIsReturned() error {
	if f.err != nil {
		return f.err
	}
	if len(f.nodeGetCapabilitiesResponse.Capabilities) > 0 {
		return nil
	}
	return errors.New("expected NodeGetCapabilities to return some capabilities")
}

func (f *feature) iCallCreateSnapshotWith(SnapID string) error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)

	if len(f.volumeIDList) == 0 {
		f.volumeIDList = append(f.volumeIDList, "00000000")
	}
	req := &csi.CreateSnapshotRequest{
		SourceVolumeId: f.volumeIDList[0],
		Name:           SnapID,
	}
	if inducedErrors.invalidVolumeID {
		req.SourceVolumeId = "00000000"
	} else if inducedErrors.noVolumeID {
		req.SourceVolumeId = ""
	} else if inducedErrors.nonExistentVolume {
		req.SourceVolumeId = fmt.Sprintf("CSI-TST-00000000-%s-000000000", f.symmetrixID)
	}
	f.createSnapshotResponse, f.err = f.service.CreateSnapshot(ctx, req)
	if f.createSnapshotResponse != nil {
		f.snapshotNameToID[SnapID] = f.createSnapshotResponse.GetSnapshot().SnapshotId
	}
	return nil
}
func (f *feature) addFailedSnapshotToDoARetry(volID, SnapID, operation string) {
	f.failedSnaps[SnapID] = failedSnap{
		volID:     volID,
		snapID:    SnapID,
		operation: operation,
	}
}
func (f *feature) iCallCreateSnapshotOn(snapshotName, volumeName string) error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)

	req := &csi.CreateSnapshotRequest{
		SourceVolumeId: f.volumeNameToID[volumeName],
		Name:           snapshotName,
	}
	f.createSnapshotResponse, f.err = f.service.CreateSnapshot(ctx, req)
	if f.createSnapshotResponse != nil {
		f.snapshotNameToID[snapshotName] = f.createSnapshotResponse.GetSnapshot().SnapshotId
	}
	if f.err != nil && (strings.Contains(f.err.Error(), "pending") || strings.Contains(f.err.Error(), "overload")) {
		f.addFailedSnapshotToDoARetry(volumeName, snapshotName, "create")
	}
	return nil
}

func (f *feature) aValidCreateSnapshotResponseIsReturned() error {
	if f.err != nil {
		return f.err
	}
	if f.createSnapshotResponse == nil {
		return errors.New("Expected CreateSnapshotResponse to be returned")
	}
	return nil
}

func (f *feature) aValidSnapshot() error {
	return godog.ErrPending
}

func (f *feature) iCallDeleteSnapshot() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	snapshotID := ""
	if f.createSnapshotResponse != nil {
		snapshotID = f.createSnapshotResponse.GetSnapshot().GetSnapshotId()
	}
	if inducedErrors.invalidSnapID {
		snapshotID = "invalid_snap"
	}
	req := &csi.DeleteSnapshotRequest{
		SnapshotId: snapshotID,
		Secrets:    make(map[string]string),
	}
	req.Secrets["x"] = "y"
	f.deleteSnapshotResponse, f.err = f.service.DeleteSnapshot(ctx, req)
	if f.err != nil && (strings.Contains(f.err.Error(), "pending") || strings.Contains(f.err.Error(), "overload")) {
		f.addFailedSnapshotToDoARetry(f.createSnapshotResponse.GetSnapshot().GetSourceVolumeId(), snapshotID, "delete")
	}
	return nil
}

func (f *feature) iCallRemoveSnapshot(snapshotName string) error {
	snapshotName, arrayID, deviceID, err := f.service.parseCsiID(f.snapshotNameToID[snapshotName])
	if err != nil {
		f.err = err
		return nil
	}
	f.err = f.service.RemoveSnapshot(arrayID, deviceID, snapshotName, int64(0))
	return nil
}

func (f *feature) aValidSnapshotConsistencyGroup() error {
	return godog.ErrPending
}

func (f *feature) iCallCreateVolumeFromSnapshot() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := f.getTypicalCreateVolumeRequest()
	req.Name = "volumeFromSnap"
	if f.wrongCapacity {
		req.CapacityRange.RequiredBytes = 64 * 1024 * 1024 * 1024
	}
	if f.wrongStoragePool {
		req.Parameters["storagepool"] = "bad storage pool"
	}
	var snapshotID string
	if inducedErrors.invalidSnapID {
		snapshotID = "invalid_snapshot"
	} else {
		snapshotID = f.createSnapshotResponse.GetSnapshot().GetSnapshotId()
	}
	source := &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapshotID}
	req.VolumeContentSource = new(csi.VolumeContentSource)
	req.VolumeContentSource.Type = &csi.VolumeContentSource_Snapshot{Snapshot: source}
	f.createVolumeResponse, f.err = f.service.CreateVolume(ctx, req)
	if f.err != nil {
		fmt.Printf("Error on CreateVolume from snap: %s\n", f.err.Error())
	}
	return nil
}

func (f *feature) iCallCreateVolumeFromVolume() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := f.getTypicalCreateVolumeRequest()
	req.Name = "volumeFromVolume"
	if f.wrongCapacity {
		req.CapacityRange.RequiredBytes = 64 * 1024 * 1024 * 1024
	}
	if f.wrongStoragePool {
		req.Parameters["storagepool"] = "bad storage pool"
	}
	var volumeID string
	if inducedErrors.noVolumeSource {
		volumeID = ""
	} else if inducedErrors.nonExistentVolume {
		volumeID = fmt.Sprintf("CSI-TST-00000000-%s-000000000", f.symmetrixID)
	} else if inducedErrors.invalidVolumeID {
		volumeID = "000000000"
	} else {
		volumeID = f.volumeID
	}
	source := &csi.VolumeContentSource_VolumeSource{VolumeId: volumeID}
	req.VolumeContentSource = new(csi.VolumeContentSource)
	if volumeID != "" {
		req.VolumeContentSource.Type = &csi.VolumeContentSource_Volume{Volume: source}
	}
	f.createVolumeResponse, f.err = f.service.CreateVolume(ctx, req)
	if f.err != nil {
		fmt.Printf("Error in creating a volume from another volume: %s\n", f.err.Error())
	}
	return nil
}

func (f *feature) theWrongCapacity() error {
	f.wrongCapacity = true
	return nil
}

func (f *feature) theWrongStoragePool() error {
	f.wrongStoragePool = true
	return nil
}

func (f *feature) thereAreValidSnapshotsOfVolume(nsnapshots int, volume string) error {
	return godog.ErrPending
}

func (f *feature) iCallListSnapshotsWithMaxEntriesAndStartingToken(maxEntriesString, startingTokenString string) error {
	return godog.ErrPending
}

func (f *feature) iCallListSnapshotsForVolume(arg1 string) error {
	return godog.ErrPending
}

func (f *feature) iCallListSnapshotsForSnapshot(arg1 string) error {
	return godog.ErrPending
}

func (f *feature) theSnapshotIDIs(arg1 string) error {
	if len(f.listedVolumeIDs) != 1 {
		return errors.New("Expected only 1 volume to be listed")
	}
	if f.listedVolumeIDs[arg1] == false {
		return errors.New("Expected volume was not found")
	}
	return nil
}

func (f *feature) aValidListSnapshotsResponseIsReturnedWithListedAndNextToken(listed, nextTokenString string) error {
	if f.err != nil {
		return f.err
	}
	nextToken := f.listSnapshotsResponse.GetNextToken()
	if nextToken != nextTokenString {
		return fmt.Errorf("Expected nextToken %s got %s", nextTokenString, nextToken)
	}
	entries := f.listSnapshotsResponse.GetEntries()
	expectedEntries, err := strconv.Atoi(listed)
	if err != nil {
		return err
	}
	if entries == nil || len(entries) != expectedEntries {
		return fmt.Errorf("Expected %d List SnapshotResponse entries but got %d", expectedEntries, len(entries))
	}
	for j := 0; j < expectedEntries; j++ {
		entry := entries[j]
		id := entry.GetSnapshot().SnapshotId
		if expectedEntries <= 10 {
			ts := ptypes.TimestampString(entry.GetSnapshot().CreationTime)
			fmt.Printf("snapshot ID %s source ID %s timestamp %s\n", id, entry.GetSnapshot().SourceVolumeId, ts)
		}
		if f.listedVolumeIDs[id] {
			return fmt.Errorf("Got duplicate snapshot ID: " + id)
		}
		f.listedVolumeIDs[id] = true
	}
	fmt.Printf("Total snapshots received: %d\n", len(f.listedVolumeIDs))
	return nil
}

func (f *feature) theTotalSnapshotsListedIs(arg1 string) error {
	expectedSnapshots, err := strconv.Atoi(arg1)
	if err != nil {
		return err
	}
	if len(f.listedVolumeIDs) != expectedSnapshots {
		return fmt.Errorf("expected %d snapshots to be listed but got %d", expectedSnapshots, len(f.listedVolumeIDs))
	}
	return nil
}

func (f *feature) iInvalidateTheProbeCache() error {
	f.service.waitGroup.Wait()
	f.service.adminClient = nil
	f.service.system = nil
	return nil
}

func (f *feature) iInvalidateTheNodeID() error {
	f.service.opts.NodeName = ""
	return nil
}

func (f *feature) iQueueForDeletion(volumeName string) error {
	// First, we have to find the volumeID from the volumeName.
	volumeID := f.volumeNameToID[volumeName]
	if volumeID == "" {
		return fmt.Errorf("Could not find volumeID for volume %s", volumeName)
	}
	_, arrayID, devID, _ := f.service.parseCsiID(volumeID)
	var volumeSize float64
	if vol, ok := mock.Data.VolumeIDToVolume[devID]; ok {
		volumeSize = vol.CapacityGB
	} else {
		return fmt.Errorf("Could not find devID for volume %s", volumeName)
	}
	// Fetch the uCode version details
	isPostElmSR, err := f.service.isPostElmSR(arrayID)
	if err != nil {
		log.Error("Failed to get symmetrix uCode version details")
		return fmt.Errorf("Failed to fetch uCode version details")
	}
	//volumeSize := mock.Data.VolumeIDToVolume[devID].CapacityGB
	req := &deletionWorkerRequest{
		symmetrixID:           arrayID,
		volumeID:              devID,
		volumeName:            "csi-" + f.service.opts.ClusterPrefix + "-" + volumeName,
		volumeSizeInCylinders: int64(volumeSize),
		skipDeallocate:        isPostElmSR,
	}
	delWorker.requestDeletion(req)
	return nil
}

func (f *feature) deletionWorkerProcessesWhichResultsIn(volumeName, errormsg string) error {
	volumeName = "csi-" + f.service.opts.ClusterPrefix + "-" + volumeName
	// wait until the job completes
	for i := 1; i < 20; i++ {
		if delWorker == nil {
			return fmt.Errorf("delWorker nil")
		}
		// Look for the volumeName in the CompletedRequests
		for _, req := range delWorker.CompletedRequests {
			fmt.Printf("CompletedRequest: %#v\n", req)
			if req != nil && req.volumeName == volumeName {
				// Found the volume
				if errormsg == "none" {
					if req.err == nil {
						return nil
					}
					return fmt.Errorf("Expected no error but got: %s", req.err.Error())
				}
				// We expected an error
				if req.err == nil {
					return fmt.Errorf("Expected error %s but got none", errormsg)
				}
				if !strings.Contains(req.err.Error(), errormsg) {
					return fmt.Errorf("Expected error to contain %s: but got: %s", errormsg, req.err.Error())
				}
				return nil
			}
		}
		time.Sleep(3 * time.Second)
	}
	return fmt.Errorf("timed out looking for CompletedRequest for volume: %s", volumeName)
}

func (f *feature) existingVolumesToBeDeleted(nvols int) error {
	mock.AddStorageGroup(defaultStorageGroup, "SRP_1", "Diamond")
	for i := 0; i < nvols; i++ {
		id := fmt.Sprintf("0000%d", i)
		mock.AddOneVolumeToStorageGroup(id, volDeleteKey+"-"+f.service.getClusterPrefix()+id, defaultStorageGroup, 8)
		resourceLink := fmt.Sprintf("sloprovisioning/system/%s/volume/%s", f.symmetrixID, id)
		job := mock.NewMockJob("job"+id, types.JobStatusRunning, types.JobStatusRunning, resourceLink)
		job.Job.Status = types.JobStatusRunning
	}
	return nil
}

func (f *feature) iRepopulateTheDeletionQueues() error {
	// Add a goroutine to the wait group as populateDeletionQueuesThread calls a "Done" on the waitgroup
	f.service.waitGroup.Add(1)
	f.err = f.service.runPopulateDeletionQueuesThread()
	return nil
}

func (f *feature) iRestartTheDeletionWorker() error {
	f.err = f.service.startDeletionWorker(false)
	return nil
}

func (f *feature) volumesAreBeingProcessedForDeletion(nVols int) error {
	if f.err != nil {
		return nil
	}
	// Wait for the goroutine which populates the deletion queue
	f.service.waitGroup.Wait()
	// Count the number of volumes in the delWorker queue
	cnt := 0
	for i := 0; i < len(delWorker.Queue); i++ {
		fmt.Printf("%s\n", delWorker.Queue[i].symmetrixID)
		if delWorker.Queue[i].symmetrixID == f.symmetrixID {
			cnt++
		}
	}
	if cnt < (nVols-2) || cnt > nVols {
		return fmt.Errorf("Expected at least %d volumes and not more than %d volumes in deletion queue but got %d", nVols-2, nVols, cnt)
	}
	return nil
}

func (f *feature) iRequestAPortGroup() error {
	f.selectedPortGroup, f.err = f.service.SelectPortGroup()
	if f.err != nil {
		return fmt.Errorf("Error selecting a Port Group from list of (%s): %v", f.service.opts.PortGroups, f.err)
	}
	if inducedErrors.portGroupError {
		f.service.opts.PortGroups = make([]string, 0)
	}
	return nil
}

func (f *feature) aValidPortGroupIsReturned() error {
	if f.selectedPortGroup == "" {
		return fmt.Errorf("Error selecting a Port Group: %v", f.err)
	}
	return nil
}

func (f *feature) iInvokeCreateOrUpdateIscsiHost(hostName string) error {
	f.service.SetPmaxTimeoutSeconds(3)
	symID := f.symmetrixID
	if inducedErrors.noSymID {
		symID = ""
	}
	fmt.Println("Hostname: " + hostName)
	fmt.Println("f.hostID: " + f.hostID)
	hostID := hostName
	if hostName == "" {
		hostID = f.hostID
	}
	if inducedErrors.noNodeName {
		hostID = ""
	}
	fmt.Println("hostID: " + hostID)
	initiator := defaultIscsiInitiator + hostName
	initiators := []string{initiator}
	initID := defaultISCSIDirPort1 + ":" + initiator
	mock.AddInitiator(initID, initiator, "GigE", []string{defaultISCSIDirPort1}, "")
	if inducedErrors.noIQNs {
		initiators = initiators[:0]
	}
	f.host, f.err = f.service.createOrUpdateIscsiHost(symID, hostID, initiators)
	f.initiators = f.host.Initiators
	return nil
}

func (f *feature) iInvokeCreateOrUpdateFCHost(hostName string) error {
	f.service.SetPmaxTimeoutSeconds(3)
	symID := f.symmetrixID
	if inducedErrors.noSymID {
		symID = ""
	}
	fmt.Println("Hostname: " + hostName)
	fmt.Println("f.hostID: " + f.hostID)
	hostID := hostName
	if hostName == "" {
		hostID = f.hostID
	}
	if inducedErrors.noNodeName {
		hostID = ""
	}
	fmt.Println("hostID: " + hostID)
	initiator := defaultFcInitiator + hostName
	initiators := []string{initiator}
	initiatorWWN := defaultFcInitiatorWWN + hostName
	initID := defaultFCDirPort + ":" + initiatorWWN
	mock.AddInitiator(initID, initiatorWWN, "Fibre", []string{defaultFCDirPort}, "")
	if inducedErrors.noIQNs {
		initiators = initiators[:0]
	}
	f.host, f.err = f.service.createOrUpdateFCHost(symID, hostID, initiators)
	if f.host != nil {
		f.initiators = f.host.Initiators
	}
	return nil
}

func (f *feature) initiatorsAreFound(expected int) error {
	if expected != len(f.initiators) {
		return fmt.Errorf("Expected %d initiators but found %d", expected, len(f.initiators))
	}
	return nil
}

func (f *feature) iInvokeNodeHostSetupWithAService(mode string) error {
	iscsiInitiators := []string{defaultIscsiInitiator}
	fcInitiators := []string{defaultFcInitiator}
	symmetrixIDs := []string{f.symmetrixID}
	f.service.mode = mode
	f.service.SetPmaxTimeoutSeconds(30)
	f.err = f.service.nodeHostSetup(fcInitiators, iscsiInitiators, symmetrixIDs)
	return nil
}

func (f *feature) theErrorClearsAfterSeconds(seconds int64) error {

	go func(seconds int64) {
		time.Sleep(time.Duration(seconds) * time.Second)
		switch f.errType {
		case "GetSymmetrixError":
			mock.InducedErrors.GetSymmetrixError = false
		case "DeviceInSGError":
			mock.InducedErrors.DeviceInSGError = false
		case "GetStorageGroupError":
			mock.InducedErrors.GetStorageGroupError = false
		case "UpdateStorageGroupError":
			mock.InducedErrors.UpdateStorageGroupError = false
		case "DeleteVolumeError":
			mock.InducedErrors.DeleteVolumeError = false
		case "InduceOverloadError":
			induceOverloadError = false
		case "InducePendingError":
			inducePendingError = false
		}
		f.doneChan <- true
	}(seconds)
	return nil
}

func (f *feature) iEnsureTheErrorIsCleared() error {
	iscleared := <-f.doneChan
	if iscleared {
		return nil
	}
	return fmt.Errorf("The induced error is still not cleared")
}

func (f *feature) aProvidedArrayWhitelistOf(whitelist string) error {
	f.err = f.service.setArrayWhitelist(whitelist)
	return nil
}

func (f *feature) iInvokeGetArrayWhitelist() error {
	f.allowedArrays = f.service.getArrayWhitelist()
	return nil
}

func (f *feature) arraysAreFound(count int) error {
	if len(f.allowedArrays) != count {
		return fmt.Errorf("Expected %d arrays in the whitelist but found %d", count, len(f.allowedArrays))
	}
	return nil
}

type GetVolumeByIDResponse struct {
	sym string
	dev string
	vol *types.Volume
	err error
}

func (f *feature) iCallGetVolumeByID() error {
	var id string
	if !inducedErrors.noVolumeID {
		if inducedErrors.invalidVolumeID {
			id = f.service.createCSIVolumeID(f.service.getClusterPrefix(), goodVolumeName, f.symmetrixID, "99999")
		} else if inducedErrors.differentVolumeID {
			id = f.service.createCSIVolumeID(f.service.getClusterPrefix(), altVolumeName, f.symmetrixID, goodVolumeID)
		} else {
			id = f.service.createCSIVolumeID(f.service.getClusterPrefix(), goodVolumeName, f.symmetrixID, goodVolumeID)
		}
	}
	sym, dev, vol, err := f.service.GetVolumeByID(id)
	resp := &GetVolumeByIDResponse{
		sym: sym,
		dev: dev,
		vol: vol,
		err: err,
	}
	f.getVolumeByIDResponse = resp
	f.err = err
	return nil
}

func (f *feature) aValidGetVolumeByIDResultIsReturnedIfNoError() error {
	if f.err != nil {
		return nil
	}
	if f.getVolumeByIDResponse == nil {
		return errors.New("Expected a GetVolumeByIDResult")
	}
	if f.getVolumeByIDResponse.sym != f.symmetrixID {
		return fmt.Errorf("Expected sym %s but got %s", f.symmetrixID, f.getVolumeByIDResponse.sym)
	}
	if f.getVolumeByIDResponse.dev != goodVolumeID {
		return fmt.Errorf("Expected dev %s but got %s", goodVolumeID, f.getVolumeByIDResponse.dev)
	}
	return nil
}

func (f *feature) iCallNodeGetVolumeStats() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.NodeGetVolumeStatsRequest)
	_, f.err = f.service.NodeGetVolumeStats(ctx, req)
	return nil
}

func (f *feature) iCallCreateSnapshot() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.CreateSnapshotRequest)
	_, f.err = f.service.CreateSnapshot(ctx, req)
	return nil
}

func (f *feature) iCallListVolumes() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.ListVolumesRequest)
	_, f.err = f.service.ListVolumes(ctx, req)
	return nil
}

func (f *feature) iCallListSnapshots() error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	req := new(csi.ListSnapshotsRequest)
	_, f.err = f.service.ListSnapshots(ctx, req)
	return nil
}

func (f *feature) iHaveAVolumeWithInvalidVolumeIdentifier() error {
	devID := goodVolumeID
	volumeIdentifier := csiPrefix + f.service.getClusterPrefix() + "-" + "xyz"
	sgList := make([]string, 1)
	sgList[0] = defaultStorageGroup
	mock.AddStorageGroup(defaultStorageGroup, "SRP_1", "Optimized")
	mock.AddOneVolumeToStorageGroup(devID, volumeIdentifier, defaultStorageGroup, 1)
	f.volumeID = f.service.createCSIVolumeID(f.service.getClusterPrefix(), goodVolumeName, f.symmetrixID, goodVolumeID)
	return nil
}

func (f *feature) thereAreNoArraysLoggedIn() error {
	f.service.loggedInArrays = make(map[string]bool)
	return nil
}

func (f *feature) iInvokeEnsureLoggedIntoEveryArray() error {
	f.service.SetPmaxTimeoutSeconds(3)
	f.err = f.service.ensureLoggedIntoEveryArray(false)
	return nil
}

func (f *feature) arraysAreLoggedIn(count int) error {
	if count != len(f.service.loggedInArrays) {
		return fmt.Errorf("Expected %d arrays logged in but got %d", count, len(f.service.loggedInArrays))
	}
	return nil
}

func (f *feature) iCallGetTargetsForMaskingView() error {
	// First we have to read the masking view
	fmt.Printf("f.mvID %s\n", f.mvID)
	symID := f.symmetrixID
	if inducedErrors.noSymID {
		symID = ""
	}
	var view *types.MaskingView
	view, f.err = f.service.adminClient.GetMaskingViewByID(f.symmetrixID, f.mvID)
	if view == nil {
		f.err = fmt.Errorf("view is nil")
	}
	f.iscsiTargets, f.err = f.service.getIscsiTargetsForMaskingView(symID, view)
	return nil
}

func (f *feature) theResultHasPorts(expected string) error {
	if f.err != nil {
		return nil
	}
	portsExpected, _ := strconv.Atoi(expected)
	if len(f.iscsiTargets) != portsExpected {
		return fmt.Errorf("Expected %d ports but got %d ports", portsExpected, len(f.iscsiTargets))
	}
	return nil
}

func (f *feature) runValidateStoragePoolID(symID, storagePool string) {
	_ = f.service.validateStoragePoolID(symID, storagePool)
	f.poolcachewg.Done()
}

func (f *feature) iCallValidateStoragePoolIDInParallel(numberOfWorkers int) error {
	f.service.setStoragePoolCacheDuration(1 * time.Millisecond)
	for i := 0; i < numberOfWorkers; i++ {
		f.poolcachewg.Add(1)
		go f.runValidateStoragePoolID(f.symmetrixID, mock.DefaultStoragePool)
	}
	return nil
}

func (f *feature) iWaitForTheExecutionToComplete() error {
	f.poolcachewg.Wait()
	fmt.Println("All goroutines finished execution")
	return nil
}

func (f *feature) runGetPortIdentifier(symID, dirPortKey string) {
	_, _ = f.service.GetPortIdentifier(symID, dirPortKey)
	f.poolcachewg.Done()
}

func (f *feature) iCallGetPortIdentifierInParallel(numberOfWorkers int) error {
	f.service.setStoragePoolCacheDuration(1 * time.Millisecond)
	dirPortKeys := make([]string, 0)
	dirPortKeys = append(dirPortKeys, "FA-1D:4")
	dirPortKeys = append(dirPortKeys, "SE-1E:24")
	dirPortKeys = append(dirPortKeys, "SE-2E:01")
	dirPortKeys = append(dirPortKeys, "FA-2B:55")
	index := 0
	for i := 0; i < numberOfWorkers; i++ {
		if index == 4 {
			index = 0
		}
		f.poolcachewg.Add(1)
		go f.runValidateStoragePoolID(f.symmetrixID, dirPortKeys[index])
		index++
	}
	return nil
}

func (f *feature) aDevicePathLun(device, lun string) error {
	key := "ip-" + portalIP + ":-lun-" + lun
	value := nodePublishDeviceDir + "/" + device
	gofsutil.GOFSMockTargetIPLUNToDevice[key] = value
	gofsutil.GOFSMockWWNToDevice[nodePublishWWN] = value
	log.Printf("aDevicePath wwn %s dev %s", nodePublishWWN, value)
	return nil
}
func (f *feature) deviceIsMounted(device string) error {
	entry := gofsutil.Info{
		Device: nodePublishDeviceDir + "/" + device,
		Path:   "/tmp/xx",
	}
	gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, entry)
	return nil
}

func (f *feature) thereAreRemainingDeviceEntriesForLun(number int, lun string) error {
	if len(gofsutil.GOFSMockWWNToDevice) != number {
		return fmt.Errorf("Expected %d device entries got %d", number, len(gofsutil.GOFSMockWWNToDevice))
	}
	return nil
}

func (f *feature) aNodeRootWithMultipathConfigFile() error {
	os.MkdirAll("test/noderoot/etc", 0777)
	os.MkdirAll("test/root/etc", 0777)
	_, err := exec.Command("touch", "test/noderoot/etc/multipath.conf").CombinedOutput()
	return err
}

func (f *feature) iCallCopyMultipathConfigFileWithRoot(testRoot string) error {
	// TODO: remove
	//f.err = copyMultipathConfigFile("test/noderoot", testRoot)
	return nil
}

func (f *feature) aPrivateMount(path string) error {
	if path == "none" {
		return nil
	}
	info := gofsutil.Info{
		Device: "test/dev/sda",
		Path:   path,
	}
	gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, info)
	return nil
}

func (f *feature) iCallUnmountPrivMount() error {
	ctx := context.Background()
	dev := Device{
		RealDev: "test/dev/sda",
	}
	f.lastUnmounted, f.err = unmountPrivMount(ctx, &dev, "test/mnt1")
	return nil
}

func (f *feature) lastUnmountedShouldBe(expected string) error {
	switch expected {
	case "true":
		if f.lastUnmounted != true {
			return errors.New("Expected lastUnmounted to be " + expected)
		}
	case "false":
		if f.lastUnmounted != false {
			return errors.New("Expected lastUnmounted to be " + expected)
		}
	}
	return nil
}

func (f *feature) blockVolumesAreNotEnabled() error {
	f.service.opts.EnableBlock = false
	return nil
}

func (f *feature) iSetTransportProtocolTo(protocol string) error {
	os.Setenv("X_CSI_TRANSPORT_PROTOCOL", protocol)
	f.service.opts.TransportProtocol = protocol
	return nil
}

func (f *feature) iEnableISCSICHAP() error {
	f.service.opts.EnableCHAP = true
	return nil
}

func (f *feature) iHaveAPortCacheEntryForPort(portKey string) error {
	if portKey == "" {
		return nil
	}
	cache := getPmaxCache(f.symmetrixID)
	dirPortKeys := make(map[string]string)
	dirPortKeys[portKey] = defaultFcStoragePortWWN
	pair := &Pair{
		first:  dirPortKeys,
		second: time.Now(),
	}
	cache.portIdentifiers = pair
	return nil
}

func (f *feature) iCallGetPortIdenfierFor(portKey string) error {
	f.response, f.err = f.service.GetPortIdentifier(f.symmetrixID, portKey)
	return nil
}

func (f *feature) theResultIs(desired string) error {
	if f.err != nil {
		return nil
	}
	if f.response != desired {
		return fmt.Errorf("Expect GetPortIdentifer to return %s but got %s", desired, f.response)
	}
	return nil
}

func (f *feature) aNonExistentPort(portName string) error {
	mock.AddPort(portName, "", "")
	return nil
}

func (f *feature) iHaveAPortIdentifierType(portName, identifier, portType string) error {
	switch portType {
	case "FibreChannel":
		break
	case "GigE":
		break
	case "":
		break
	default:
		return fmt.Errorf("Unknown port type: %s", portType)
	}
	mock.AddPort(portName, identifier, portType)
	return nil
}

func (f *feature) iHaveSysblockDevices(cnt int) error {
	removeDeviceSleepTime = 1 * time.Millisecond
	switch cnt {
	case 1:
		gofsutil.GOFSMockWWNToDevice[nodePublishWWN] = "/dev/sdm"
	}
	return nil
}

func (f *feature) iCallLinearScanToRemoveDevices() error {
	// TODO: remove
	//f.err = linearScanToRemoveDevices("0", nodePublishWWN)
	return nil
}

func (f *feature) iCallverifyAndUpdateInitiatorsInADiffHostForNode(nodeID string) error {
	initiators := make([]string, 0)
	initiators = append(initiators, defaultIscsiInitiator)
	symID := f.symmetrixID
	hostID, _, _ := f.service.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	f.ninitiators, f.err = f.service.verifyAndUpdateInitiatorsInADiffHost(symID, initiators, hostID)
	return nil
}

func (f *feature) validInitiatorsAreReturned(expected int) error {
	if expected != f.ninitiators {
		return fmt.Errorf("expected %d initiators but got %d", expected, f.ninitiators)
	}
	return nil
}

func (f *feature) iCheckTheSnapshotLicense() error {
	f.err = f.service.IsSnapshotLicensed(f.symmetrixID)
	return nil
}

func (f *feature) iCallIsVolumeInSnapSessionOn(volumeName string) error {
	var volumeID string
	if inducedErrors.nonExistentVolume {
		volumeID = fmt.Sprintf("CSI-TST-00000000-%s-000000000", f.symmetrixID)
	} else {
		volumeID = f.volumeNameToID[volumeName]
	}
	_, arrayID, deviceID, err := f.service.parseCsiID(volumeID)
	if err != nil {
		return fmt.Errorf("Error parsing the CSI VolumeID: %s", err.Error())
	}
	isSource, isTarget, err := f.service.IsVolumeInSnapSession(arrayID, deviceID)
	if err != nil {
		f.err = err
	} else {
		fmt.Printf("Source = %t; Target=%t", isSource, isTarget)
	}
	return nil
}

func (f *feature) iCallExecSnapActionToSnapshotTo(action, snapshotName, volumeName string) error {
	snapshotName, arrayID, device1, err := f.service.parseCsiID(f.snapshotNameToID[snapshotName])
	if err != nil {
		f.err = err
		return nil
	}
	_, _, device2, err := f.service.parseCsiID(f.volumeNameToID[volumeName])
	if err != nil {
		f.err = err
		return nil
	}
	SourceList := []types.VolumeList{}
	TargetList := []types.VolumeList{}
	SourceList = append(SourceList, types.VolumeList{Name: device1})
	TargetList = append(TargetList, types.VolumeList{Name: device2})
	f.err = f.service.adminClient.ModifySnapshotS(arrayID, SourceList, TargetList, snapshotName, action, "", int64(0))
	return nil
}

func (f *feature) iCallUnlinkAndTerminate(volumeName string) error {
	_, arrayID, deviceID, err := f.service.parseCsiID(f.volumeNameToID[volumeName])
	if err != nil {
		f.err = err
		return nil
	}
	f.err = f.service.UnlinkAndTerminate(arrayID, deviceID, "")
	return nil
}

func (f *feature) iCallGetSnapSessionsOn(volumeName string) error {
	_, arrayID, deviceID, err := f.service.parseCsiID(f.volumeNameToID[volumeName])
	if err != nil {
		f.err = err
		return nil
	}
	sourceSessions, targetSession, err := f.service.GetSnapSessions(arrayID, deviceID)
	if err != nil {
		f.err = err
	}
	fmt.Printf("SourceSessions = %v, TargetSession = %v\n", sourceSessions, targetSession)
	return nil
}

func (f *feature) iCallRemoveTempSnapshotOn(volumeName string) error {
	_, arrayID, deviceID, err := f.service.parseCsiID(f.volumeNameToID[volumeName])
	if err != nil {
		f.err = err
		return nil
	}
	_, targetSession, err := f.service.GetSnapSessions(arrayID, deviceID)
	if err != nil {
		f.err = err
		return nil
	}
	f.err = f.service.UnlinkSnapshot(arrayID, targetSession, MaxUnlinkCount)
	return nil
}

func (f *feature) iCallCreateSnapshotFromVolume(snapshotName string) error {
	arrayID, _, volume, err := f.service.GetVolumeByID(f.volumeID)
	if err != nil {
		f.err = err
		return nil
	}
	reqID := strconv.Itoa(time.Now().Nanosecond())
	reqID = "req:" + reqID
	snapshot, err := f.service.CreateSnapshotFromVolume(arrayID, volume, snapshotName, int64(0), reqID)
	if err != nil {
		f.err = err
		return nil
	}
	fmt.Printf("VolumeSnapshot = %v\n", snapshot)
	return nil
}

func (f *feature) iCallCreateVolumeFrom(volumeName, snapshotName string) error {
	snapshotName, arrayID, sourceDevice, err := f.service.parseCsiID(f.snapshotNameToID[snapshotName])
	if err != nil {
		f.err = err
		return nil
	}
	reqID := strconv.Itoa(time.Now().Nanosecond())
	reqID = "req:" + reqID
	_, _, targetDevice, err := f.service.parseCsiID(f.volumeNameToID[volumeName])
	f.err = f.service.LinkVolumeToSnapshot(arrayID, sourceDevice, targetDevice, snapshotName, reqID)
	return nil
}

func (f *feature) iCallTerminateSnapshot() error {
	snapshotName, arrayID, deviceID, err := f.service.parseCsiID(f.createSnapshotResponse.GetSnapshot().GetSnapshotId())
	if err != nil {
		f.err = err
		return nil
	}
	f.err = f.service.TerminateSnapshot(arrayID, deviceID, snapshotName)
	return nil
}

func (f *feature) iCallUnlinkAndTerminateSnapshot() error {
	snapshotName, arrayID, deviceID, err := f.service.parseCsiID(f.createSnapshotResponse.GetSnapshot().GetSnapshotId())
	if err != nil {
		f.err = err
		return nil
	}
	f.err = f.service.UnlinkAndTerminate(arrayID, deviceID, snapshotName)
	return nil
}

func (f *feature) aValidDeleteSnapshotResponseIsReturned() error {
	if f.err != nil {
		return f.err
	}
	if f.deleteSnapshotResponse == nil {
		return errors.New("Expected a valid delete snapshot response")
	}
	return nil
}

func (f *feature) aNonexistentVolume() error {
	inducedErrors.nonExistentVolume = true
	return nil
}

func (f *feature) noVolumeSource() error {
	inducedErrors.noVolumeSource = true
	return nil
}

func (f *feature) iResetTheLicenseCache() error {
	licenseCached = false
	symmRepCapabilities = nil
	return nil
}

func (f *feature) iCallMarkSnapshotForDeletion() error {
	snapshotName, arrayID, deviceID, err := f.service.parseCsiID(f.createSnapshotResponse.GetSnapshot().GetSnapshotId())
	if err != nil {
		f.err = err
		return nil
	}
	newSnapID, err := f.service.MarkSnapshotForDeletion(arrayID, snapshotName, deviceID)
	f.createSnapshotResponse.Snapshot.SnapshotId = newSnapID
	if err != nil {
		f.err = err
		return nil
	}
	fmt.Printf("Snapshot(%s) marked for deletion as %s \n", snapshotName, newSnapID)
	return nil
}

func (f *feature) iCheckIfTheSnapshotHasBeenDeleted() error {
	MAXRETRIES := 5
	var snapshot *types.VolumeSnapshot
	for i := 0; i < MAXRETRIES; i++ {
		_, symID, deviceID, err := f.service.parseCsiID(f.createVolumeResponse.Volume.VolumeId)
		if err != nil {
			f.err = err
			return nil
		}
		snapshot, err = f.service.adminClient.GetSnapshotInfo(symID, deviceID, f.createSnapshotResponse.Snapshot.SnapshotId)
		if err != nil {
			f.err = err
			return nil
		}
		if snapshot.VolumeSnapshotSource == nil {
			return nil
		}
		if i < MAXRETRIES-1 {
			time.Sleep(time.Second * 1)
		}
	}
	if snapshot.VolumeSnapshotSource != nil {
		f.err = fmt.Errorf("Snapshot not deleted by the worker")
	}
	return nil
}

func (f *feature) iCallIsSnapshotSource() error {
	var arrayID, deviceID string
	if f.createSnapshotResponse != nil {
		_, arrayID, deviceID, f.err = f.service.parseCsiID(f.createSnapshotResponse.GetSnapshot().GetSnapshotId())
	} else {
		_, arrayID, deviceID, f.err = f.service.parseCsiID(f.createVolumeResponse.GetVolume().GetVolumeId())
	}

	f.isSnapSrc, f.err = f.service.IsSnapshotSource(arrayID, deviceID)
	return nil
}

func (f *feature) isSnapshotSourceReturns(isSnapSrcResponse string) error {
	if !(isSnapSrcResponse == strconv.FormatBool(f.isSnapSrc)) {
		f.err = fmt.Errorf("Incorrect response sent by IsSnapshotSource()")
	}
	return nil
}

func (f *feature) iCallDeleteSnapshotWith(snapshotName string) error {
	header := metadata.New(map[string]string{"csi.requestid": "1"})
	ctx := metadata.NewIncomingContext(context.Background(), header)
	snapshotID := ""
	if f.createSnapshotResponse != nil {
		snapshotID = f.snapshotNameToID[snapshotName]
	}
	if inducedErrors.invalidSnapID {
		snapshotID = "invalid_snap"
	}
	req := &csi.DeleteSnapshotRequest{
		SnapshotId: snapshotID,
		Secrets:    make(map[string]string),
	}
	req.Secrets["x"] = "y"
	f.deleteSnapshotResponse, f.err = f.service.DeleteSnapshot(ctx, req)
	return nil
}

func (f *feature) iQueueSnapshotsForTermination() error {
	snap1 := f.createSnapshotResponse.GetSnapshot().GetSnapshotId()
	snapDelReq1 := new(snapCleanupRequest)
	var err error
	snapDelReq1.snapshotID, snapDelReq1.symmetrixID, snapDelReq1.volumeID, err = f.service.parseCsiID(snap1)
	if err != nil {
		return fmt.Errorf("Invalid snapshot name")
	}
	snapCleaner.requestCleanup(snapDelReq1)
	snap2 := f.snapshotNameToID["snapshot2"]
	snapDelReq2 := new(snapCleanupRequest)
	snapDelReq2.snapshotID, snapDelReq2.symmetrixID, snapDelReq2.volumeID, err = f.service.parseCsiID(snap2)
	if err != nil {
		return fmt.Errorf("Invalid snapshot name")
	}
	snapCleaner.requestCleanup(snapDelReq2)
	snapshot := f.snapshotNameToID["snapshot3"]
	snapDelReq := new(snapCleanupRequest)
	snapDelReq.snapshotID, snapDelReq.symmetrixID, snapDelReq.volumeID, err = f.service.parseCsiID(snapshot)
	if err != nil {
		return fmt.Errorf("Invalid snapshot name")
	}
	time.Sleep(10 * time.Second)
	snapCleaner.requestCleanup(snapDelReq)
	return nil
}

func (f *feature) theDeletionWorkerProcessesTheSnapshotsSuccessfully() error {
	snapCount := len(f.snapshotNameToID)
	for {
		deletedSnapCount := 0
		for _, snapshot := range f.snapshotNameToID {
			snapName, symID, volumeID, err := f.service.parseCsiID(snapshot)
			if err != nil {
				continue
			}
			snapInfo, err := f.service.adminClient.GetSnapshotInfo(symID, volumeID, snapName)
			if err != nil {
				continue
			}
			if snapInfo.VolumeSnapshotLink == nil {
				deletedSnapCount++
			}
			fmt.Printf("%v\n", snapInfo)
		}
		if snapCount == deletedSnapCount {
			break
		}
		time.Sleep(2 * time.Second)
	}
	return nil
}

func (f *feature) iCallEnsureISCSIDaemonStarted() error {
	f.err = f.service.ensureISCSIDaemonStarted()
	return nil
}

func (f *feature) iCallRequestAddVolumeToSGMVMv(nodeID, maskingViewName string) error {
	if nodeID == "none" {
		return nil
	}
	accessMode := new(csi.VolumeCapability_AccessMode)
	accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	f.hostID, f.sgID, f.mvID = f.service.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	deviceIDComponents := strings.Split(f.volumeID, "-")
	deviceID := deviceIDComponents[len(deviceIDComponents)-1]
	fmt.Printf("deviceID %s\n", deviceID)
	if maskingViewName != "" && maskingViewName != "default" {
		f.addVolumeToSGMVResponse2, f.lockChan, f.err = f.service.sgSvc.requestAddVolumeToSGMV(f.sgID, maskingViewName, f.hostID, "0001", mock.DefaultSymmetrixID, deviceID, accessMode)
	} else {
		f.addVolumeToSGMVResponse1, f.lockChan, f.err = f.service.sgSvc.requestAddVolumeToSGMV(f.sgID, f.mvID, f.hostID, "0001", mock.DefaultSymmetrixID, deviceID, accessMode)
	}
	return nil
}

func (f *feature) iCallRequestRemoveVolumeFromSGMVMv(nodeID, maskingViewName string) error {
	if nodeID == "none" {
		return nil
	}
	accessMode := new(csi.VolumeCapability_AccessMode)
	accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	f.hostID, f.sgID, f.mvID = f.service.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	deviceIDComponents := strings.Split(f.volumeID, "-")
	deviceID := deviceIDComponents[len(deviceIDComponents)-1]
	fmt.Printf("deviceID %s\n", deviceID)
	if maskingViewName != "" && maskingViewName != "default" {
		f.removeVolumeFromSGMVResponse2, f.lockChan, f.err = f.service.sgSvc.requestRemoveVolumeFromSGMV(f.sgID, maskingViewName, "0001", mock.DefaultSymmetrixID, deviceID)
	} else {
		f.removeVolumeFromSGMVResponse1, f.lockChan, f.err = f.service.sgSvc.requestRemoveVolumeFromSGMV(f.sgID, f.mvID, "0001", mock.DefaultSymmetrixID, deviceID)
	}
	return nil
}

func (f *feature) iCallRunAddVolumesToSGMV() error {
	// If we've already errored or we didn't successfully call requestAddVolumeToSGMV, we still call runAddVolumesToSGMV,
	// which should return immediately logging a "sgstate missing" message
	if f.err != nil || f.addVolumeToSGMVResponse1 == nil {
		f.service.sgSvc.runAddVolumesToSGMV(mock.DefaultSymmetrixID, f.sgID)
		return nil
	}
	// This loop like the one in ControllerPublish where we wait on result or our turn to execute the service.
	done1 := false
	done2 := false
	var err2 error
	if f.addVolumeToSGMVResponse2 == nil {
		// no 2nd request so done2 is already done
		done2 = true
	}
	for !done1 || !done2 {
		select {
		case resp := <-f.addVolumeToSGMVResponse1:
			f.err = resp.err
			fmt.Printf("Received response1 err %v", f.err)
			done1 = true
		case resp := <-f.addVolumeToSGMVResponse2:
			err2 = resp.err
			fmt.Printf("Received response2 err %v", err2)
			done2 = true
		case <-f.lockChan:
			f.service.sgSvc.runAddVolumesToSGMV(mock.DefaultSymmetrixID, f.sgID)
		}
	}
	if f.addVolumeToSGMVResponse1 != nil {
		close(f.addVolumeToSGMVResponse1)
	}
	if f.addVolumeToSGMVResponse2 != nil {
		close(f.addVolumeToSGMVResponse2)
	}
	if err2 != nil {
		f.err = err2
	}
	return nil
}

func (f *feature) iCallRunRemoveVolumesFromSGMV() error {
	// If we've already errored or we didn't successfully call requestRemoveVolumeFromSGMV, we still call runRemoveVolumesFromSGMV,
	// which should return immediately logging a "sgstate missing" message
	if f.err != nil || f.removeVolumeFromSGMVResponse1 == nil {
		f.service.sgSvc.runRemoveVolumesFromSGMV(mock.DefaultSymmetrixID, f.sgID)
		return nil
	}
	// This loop like the one in ControllerPublish where we wait on result or our turn to execute the service.
	done1 := false
	done2 := false
	var err2 error
	if f.removeVolumeFromSGMVResponse2 == nil {
		// no 2nd request so done2 is already done
		done2 = true
	}
	for !done1 || !done2 {
		select {
		case resp := <-f.removeVolumeFromSGMVResponse1:
			f.err = resp.err
			fmt.Printf("Received waitChan1 err %v", f.err)
			done1 = true
		case resp := <-f.removeVolumeFromSGMVResponse2:
			err2 = resp.err
			fmt.Printf("Received response2 err %v", err2)
			done2 = true
		case <-f.lockChan:
			f.service.sgSvc.runRemoveVolumesFromSGMV(mock.DefaultSymmetrixID, f.sgID)
		}
	}
	if f.removeVolumeFromSGMVResponse1 != nil {
		close(f.removeVolumeFromSGMVResponse1)
	}
	if f.removeVolumeFromSGMVResponse2 != nil {
		close(f.removeVolumeFromSGMVResponse2)
	}
	if err2 != nil {
		f.err = err2
	}
	return nil
}

func (f *feature) iCallHandleAddVolumeToSGMVError() error {
	accessMode := new(csi.VolumeCapability_AccessMode)
	accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	deviceIDComponents := strings.Split(f.volumeID, "-")
	deviceID := deviceIDComponents[len(deviceIDComponents)-1]
	req := &addVolumeToSGMVRequest{
		tgtStorageGroupID: f.sgID,
		tgtMaskingViewID:  f.mvID,
		hostID:            f.hostID,
		reqID:             "0002",
		symID:             mock.DefaultSymmetrixID,
		devID:             deviceID,
		accessMode:        accessMode,
		respChan:          make(chan addVolumeToSGMVResponse, 1),
	}
	f.service.sgSvc.handleAddVolumeToSGMVError(req)
	resp := <-req.respChan
	f.err = resp.err
	return nil
}

func (f *feature) iCallHandleRemoveVolumeFromSGMVError() error {
	deviceIDComponents := strings.Split(f.volumeID, "-")
	deviceID := deviceIDComponents[len(deviceIDComponents)-1]
	req := &removeVolumeFromSGMVRequest{
		tgtStorageGroupID: f.sgID,
		tgtMaskingViewID:  f.mvID,
		reqID:             "0002",
		symID:             mock.DefaultSymmetrixID,
		devID:             deviceID,
		respChan:          make(chan removeVolumeFromSGMVResponse, 1),
	}
	f.service.sgSvc.handleRemoveVolumeFromSGMVError(req)
	resp := <-req.respChan
	f.err = resp.err
	return nil
}

func (f *feature) iCallGetAndConfigureArrayISCSITargets() error {
	arrayTargets := make([]string, 0)
	arrayTargets = append(arrayTargets, defaultArrayTargetIQN)
	f.iscsiTargetInfo = f.service.getAndConfigureArrayISCSITargets(arrayTargets, mock.DefaultSymmetrixID)
	fmt.Println(f.iscsiTargetInfo)
	return nil
}

func (f *feature) targetsAreReturned(count int) error {
	if len(f.iscsiTargetInfo) != count {
		return fmt.Errorf("expected %d iscsi targets but found %d", count, len(f.iscsiTargetInfo))
	}
	return nil
}

func (f *feature) iInvalidateSymToMaskingViewTargetCache() error {
	f.service.InvalidateSymToMaskingViewTargets()
	return nil
}

func (f *feature) iRetryOnFailedSnapshotToSucceed() error {
	for _, failedSnap := range f.failedSnaps {
		for i := 1; i <= f.maxRetryCount; i++ {
			if failedSnap.operation == "create" {
				_ = f.iCallCreateSnapshotOn(failedSnap.snapID, failedSnap.volID)
				if f.err == nil {
					delete(f.failedSnaps, failedSnap.snapID)
					break
				} else {
					fmt.Printf("Retry CreateSnapshot (%d) failed for SnapID (%s) with error (%s)\n", i, failedSnap.snapID, f.err.Error())
				}
			} else if failedSnap.operation == "delete" {
				_ = f.iCallDeleteSnapshot()
				if f.err == nil {
					delete(f.failedSnaps, failedSnap.snapID)
					break
				} else {
					fmt.Printf("Retry DeleteSnapshot (%d) failed for SnapID (%s) with error (%s)\n", i, failedSnap.snapID, f.err.Error())
				}
			}
			time.Sleep(2 * time.Second)
		}
	}
	if f.err != nil {
		fmt.Println("The snap operation even failed after Max Retries")
		f.err = errors.New("The snap operation even failed after Max Retries")
	}
	return nil
}
func (f *feature) iSetModifyHostNameToFalse() error {
	f.service.opts.ModifyHostName = false
	return nil
}

func (f *feature) iSetModifyHostNameToTrue() error {
	f.service.opts.ModifyHostName = true
	return nil
}

func (f *feature) iHaveANodeNameTemplate(template string) error {
	f.service.opts.NodeNameTemplate = template
	return nil
}

func (f *feature) iCallBuildHostIDFromTemplateForNodeHost(node string) error {
	_, f.err = f.service.buildHostIDFromTemplate(node)
	return nil
}

func FeatureContext(s *godog.Suite) {
	f := &feature{}
	s.Step(`^a PowerMax service$`, f.aPowerMaxService)
	s.Step(`^a PostELMSR Array$`, f.aPostELMSRArray)
	s.Step(`^I call GetPluginInfo$`, f.iCallGetPluginInfo)
	s.Step(`^a valid GetPluginInfoResponse is returned$`, f.aValidGetPluginInfoResponseIsReturned)
	s.Step(`^I call GetPluginCapabilities$`, f.iCallGetPluginCapabilities)
	s.Step(`^a valid GetPluginCapabilitiesResponse is returned$`, f.aValidGetPluginCapabilitiesResponseIsReturned)
	s.Step(`^I call Probe$`, f.iCallProbe)
	s.Step(`^a valid ProbeResponse is returned$`, f.aValidProbeResponseIsReturned)
	s.Step(`^the error contains "([^"]*)"$`, f.theErrorContains)
	s.Step(`^the possible error contains "([^"]*)"$`, f.thePossibleErrorContains)
	s.Step(`^the Controller has no connection$`, f.theControllerHasNoConnection)
	s.Step(`^there is a Node Probe Lsmod error$`, f.thereIsANodeProbeLsmodError)
	s.Step(`^I call CreateVolume "([^"]*)"$`, f.iCallCreateVolume)
	s.Step(`^a valid CreateVolumeResponse is returned$`, f.aValidCreateVolumeResponseIsReturned)
	s.Step(`^I specify AccessibilityRequirements$`, f.iSpecifyAccessibilityRequirements)
	s.Step(`^I specify MULTINODEWRITER$`, f.iSpecifyMULTINODEWRITER)
	s.Step(`^I specify a BadCapacity$`, f.iSpecifyABadCapacity)
	s.Step(`^I specify a ApplicationPrefix$`, f.iSpecifyAApplicationPrefix)
	s.Step(`^I specify a StorageGroup$`, f.iSpecifyAStorageGroup)
	s.Step(`^I specify NoStoragePool$`, f.iSpecifyNoStoragePool)
	s.Step(`^I call CreateVolumeSize "([^"]*)" "(\d+)"$`, f.iCallCreateVolumeSize)
	s.Step(`^I change the StoragePool "([^"]*)"$`, f.iChangeTheStoragePool)
	s.Step(`^I induce error "([^"]*)"$`, f.iInduceError)
	s.Step(`^I specify VolumeContentSource$`, f.iSpecifyVolumeContentSource)
	s.Step(`^I specify CreateVolumeMountRequest "([^"]*)"$`, f.iSpecifyCreateVolumeMountRequest)
	s.Step(`^I call PublishVolume with "([^"]*)" to "([^"]*)"$`, f.iCallPublishVolumeWithTo)
	s.Step(`^a valid PublishVolumeResponse is returned$`, f.aValidPublishVolumeResponseIsReturned)
	s.Step(`^a valid volume$`, f.aValidVolume)
	s.Step(`^a valid volume with size of (\d+) CYL$`, f.aValidVolumeWithSizeOfCYL)
	s.Step(`^an invalid volume$`, f.anInvalidVolume)
	s.Step(`^an invalid snapshot$`, f.anInvalidSnapshot)
	s.Step(`^no volume$`, f.noVolume)
	s.Step(`^no node$`, f.noNode)
	s.Step(`^no volume capability$`, f.noVolumeCapability)
	s.Step(`^no access mode$`, f.noAccessMode)
	s.Step(`^then I use a different nodeID$`, f.thenIUseADifferentNodeID)
	s.Step(`^I use AccessType Mount$`, f.iUseAccessTypeMount)
	s.Step(`^no error was received$`, f.noErrorWasReceived)
	s.Step(`^I call UnpublishVolume from "([^"]*)"$`, f.iCallUnpublishVolumeFrom)
	s.Step(`^a valid UnpublishVolumeResponse is returned$`, f.aValidUnpublishVolumeResponseIsReturned)
	s.Step(`^I call NodeGetInfo$`, f.iCallNodeGetInfo)
	s.Step(`^a valid NodeGetInfoResponse is returned$`, f.aValidNodeGetInfoResponseIsReturned)
	s.Step(`^I call DeleteVolume with "([^"]*)"$`, f.iCallDeleteVolumeWith)
	s.Step(`^a valid DeleteVolumeResponse is returned$`, f.aValidDeleteVolumeResponseIsReturned)
	s.Step(`^I call GetCapacity with storage pool "([^"]*)"$`, f.iCallGetCapacityWithStoragePool)
	s.Step(`^I call GetCapacity without Symmetrix ID$`, f.iCallGetCapacityWithoutSymmetrixID)
	s.Step(`^I call GetCapacity without Parameters$`, f.iCallGetCapacityWithoutParameters)
	s.Step(`^I call GetCapacity with Invalid capabilities$`, f.iCallGetCapacityWithInvalidCapabilities)
	s.Step(`^a valid GetCapacityResponse is returned$`, f.aValidGetCapacityResponseIsReturned)
	s.Step(`^I call ControllerGetCapabilities$`, f.iCallControllerGetCapabilities)
	s.Step(`^a valid ControllerGetCapabilitiesResponse is returned$`, f.aValidControllerGetCapabilitiesResponseIsReturned)
	s.Step(`^I call ValidateVolumeCapabilities with voltype "([^"]*)" access "([^"]*)" fstype "([^"]*)" pool "([^"]*)" level "([^"]*)"$`, f.iCallValidateVolumeCapabilitiesWithVoltypeAccessFstype)
	s.Step(`^a valid ListVolumesResponse is returned$`, f.aValidListVolumesResponseIsReturned)
	s.Step(`^I call(?:ed)? ListVolumes with$`, f.iCallListVolumesWith)
	s.Step(`^I call(?:ed)? ListVolumes again with$`, f.iCallListVolumesAgainWith)
	s.Step(`^I call ListVolumes$`, f.iCallListVolumes)
	s.Step(`^there (?:are|is) (\d+) valid volumes?$`, f.thereAreValidVolumes)
	s.Step(`^(\d+) volume(?:s)? (?:are|is) listed$`, f.volumesAreListed)
	s.Step(`^an invalid ListVolumesResponse is returned$`, f.anInvalidListVolumesResponseIsReturned)
	s.Step(`^a capability with voltype "([^"]*)" access "([^"]*)" fstype "([^"]*)"$`, f.aCapabilityWithVoltypeAccessFstype)
	s.Step(`^a controller published volume$`, f.aControllerPublishedVolume)
	s.Step(`^a controller published multipath volume$`, f.aControllerPublishedMultipathVolume)
	s.Step(`^I call NodePublishVolume$`, f.iCallNodePublishVolume)
	s.Step(`^get Node Publish Volume Request$`, f.getNodePublishVolumeRequest)
	s.Step(`^I mark request read only$`, f.iMarkRequestReadOnly)
	s.Step(`^I call NodeUnpublishVolume$`, f.iCallNodeUnpublishVolume)
	s.Step(`^there are no remaining mounts$`, f.thereAreNoRemainingMounts)
	s.Step(`^I call BeforeServe$`, f.iCallBeforeServe)
	s.Step(`^I call BeforeServe without ClusterPrefix$`, f.iCallBeforeServeWithoutClusterPrefix)
	s.Step(`^I call BeforeServe with an invalid ClusterPrefix$`, f.iCallBeforeServeWithAnInvalidClusterPrefix)
	s.Step(`^I call NodeStageVolume$`, f.iCallNodeStageVolume)
	s.Step(`^I call NodeUnstageVolume$`, f.iCallNodeUnstageVolume)
	s.Step(`^I call NodeGetCapabilities$`, f.iCallNodeGetCapabilities)
	s.Step(`^a valid NodeGetCapabilitiesResponse is returned$`, f.aValidNodeGetCapabilitiesResponseIsReturned)
	s.Step(`^I call CreateSnapshot$`, f.iCallCreateSnapshot)
	s.Step(`^I call CreateSnapshot With "([^"]*)"$`, f.iCallCreateSnapshotWith)
	s.Step(`^I call CreateSnapshot "([^"]*)" on "([^"]*)"$`, f.iCallCreateSnapshotOn)
	s.Step(`^a valid CreateSnapshotResponse is returned$`, f.aValidCreateSnapshotResponseIsReturned)
	s.Step(`^a valid snapshot$`, f.aValidSnapshot)
	s.Step(`^I call DeleteSnapshot$`, f.iCallDeleteSnapshot)
	s.Step(`^I call RemoveSnapshot "([^"]*)"$`, f.iCallRemoveSnapshot)
	s.Step(`^a valid snapshot consistency group$`, f.aValidSnapshotConsistencyGroup)
	s.Step(`^I call Create Volume from Snapshot$`, f.iCallCreateVolumeFromSnapshot)
	s.Step(`^the wrong capacity$`, f.theWrongCapacity)
	s.Step(`^the wrong storage pool$`, f.theWrongStoragePool)
	s.Step(`^there are (\d+) valid snapshots of "([^"]*)" volume$`, f.thereAreValidSnapshotsOfVolume)
	s.Step(`^I call ListSnapshots$`, f.iCallListSnapshots)
	s.Step(`^I call ListSnapshots with max_entries "([^"]*)" and starting_token "([^"]*)"$`, f.iCallListSnapshotsWithMaxEntriesAndStartingToken)
	s.Step(`^a valid ListSnapshotsResponse is returned with listed "([^"]*)" and next_token "([^"]*)"$`, f.aValidListSnapshotsResponseIsReturnedWithListedAndNextToken)
	s.Step(`^the total snapshots listed is "([^"]*)"$`, f.theTotalSnapshotsListedIs)
	s.Step(`^I call ListSnapshots for volume "([^"]*)"$`, f.iCallListSnapshotsForVolume)
	s.Step(`^I call ListSnapshots for snapshot "([^"]*)"$`, f.iCallListSnapshotsForSnapshot)
	s.Step(`^the snapshot ID is "([^"]*)"$`, f.theSnapshotIDIs)
	s.Step(`^I invalidate the Probe cache$`, f.iInvalidateTheProbeCache)
	s.Step(`^I invalidate the NodeID$`, f.iInvalidateTheNodeID)
	s.Step(`^I queue "([^"]*)" for deletion$`, f.iQueueForDeletion)
	s.Step(`^deletion worker processes "([^"]*)" which results in "([^"]*)"$`, f.deletionWorkerProcessesWhichResultsIn)
	s.Step(`^I request a PortGroup$`, f.iRequestAPortGroup)
	s.Step(`^a valid PortGroup is returned$`, f.aValidPortGroupIsReturned)
	s.Step(`^I invoke createOrUpdateIscsiHost "([^"]*)"$`, f.iInvokeCreateOrUpdateIscsiHost)
	s.Step(`^I invoke nodeHostSetup with a "([^"]*)" service$`, f.iInvokeNodeHostSetupWithAService)
	s.Step(`^the error clears after (\d+) seconds$`, f.theErrorClearsAfterSeconds)
	s.Step(`^I have a Node "([^"]*)" with initiators "([^"]*)" with MaskingView$`, f.iHaveANodeWithInitiatorsWithMaskingView)
	s.Step(`^I have a Node "([^"]*)" with MaskingView$`, f.iHaveANodeWithMaskingView)
	s.Step(`^I have a Node "([^"]*)" with Host$`, f.iHaveANodeWithHost)
	s.Step(`^I have a Node "([^"]*)" with StorageGroup$`, f.iHaveANodeWithStorageGroup)
	s.Step(`^I have a Node "([^"]*)" with a FastManagedMaskingView$`, f.iHaveANodeWithAFastManagedMaskingView)
	s.Step(`^I have a Node "([^"]*)" with FastManagedStorageGroup$`, f.iHaveANodeWithFastManagedStorageGroup)
	s.Step(`^I have a Node "([^"]*)" with Host with Initiator mapped to multiple ports$`, f.iHaveANodeWithHostWithInitiatorMappedToMultiplePorts)
	s.Step(`^I have a FC PortGroup "([^"]*)"$`, f.iHaveAFCPortGroup)
	s.Step(`^I add the Volume to "([^"]*)"$`, f.iAddTheVolumeTo)
	s.Step(`^(\d+) existing volumes to be deleted$`, f.existingVolumesToBeDeleted)
	s.Step(`^I repopulate the deletion queues$`, f.iRepopulateTheDeletionQueues)
	s.Step(`^I restart the deletionWorker$`, f.iRestartTheDeletionWorker)
	s.Step(`^(\d+) volumes are being processed for deletion$`, f.volumesAreBeingProcessedForDeletion)
	s.Step(`^I change the target path$`, f.iChangeTheTargetPath)
	s.Step(`^a provided array whitelist of "([^"]*)"$`, f.aProvidedArrayWhitelistOf)
	s.Step(`^I invoke getArrayWhitelist$`, f.iInvokeGetArrayWhitelist)
	s.Step(`^(\d+) arrays are found$`, f.arraysAreFound)
	s.Step(`^I call GetVolumeByID$`, f.iCallGetVolumeByID)
	s.Step(`^a valid GetVolumeByID result is returned if no error$`, f.aValidGetVolumeByIDResultIsReturnedIfNoError)
	s.Step(`^(\d+) initiators are found$`, f.initiatorsAreFound)
	s.Step(`^I call NodeGetVolumeStats$`, f.iCallNodeGetVolumeStats)
	s.Step(`^I have a volume with invalid volume identifier$`, f.iHaveAVolumeWithInvalidVolumeIdentifier)
	s.Step(`^there are no arrays logged in$`, f.thereAreNoArraysLoggedIn)
	s.Step(`^I invoke ensureLoggedIntoEveryArray$`, f.iInvokeEnsureLoggedIntoEveryArray)
	s.Step(`^(\d+) arrays are logged in$`, f.arraysAreLoggedIn)
	s.Step(`^I call GetTargetsForMaskingView$`, f.iCallGetTargetsForMaskingView)
	s.Step(`^the result has "([^"]*)" ports$`, f.theResultHasPorts)
	s.Step(`^I call validateStoragePoolID (\d+) in parallel$`, f.iCallValidateStoragePoolIDInParallel)
	s.Step(`^I call GetPortIdentifier (\d+) in parallel$`, f.iCallGetPortIdentifierInParallel)
	s.Step(`I call ControllerExpandVolume with Capacity Range set to (\d+)$`, f.iCallControllerExpandVolume)
	s.Step(`I call NodeExpandVolume with volumePath as "([^"]*)"$`, f.iCallNodeExpandVolume)
	s.Step(`^a device path "([^"]*)" lun "([^"]*)"$`, f.aDevicePathLun)
	s.Step(`^device "([^"]*)" is mounted$`, f.deviceIsMounted)
	s.Step(`^there are (\d+) remaining device entries for lun "([^"]*)"$`, f.thereAreRemainingDeviceEntriesForLun)
	s.Step(`^a nodeRoot with multipath config file$`, f.aNodeRootWithMultipathConfigFile)
	s.Step(`^I call copyMultipathConfigFile with root "([^"]*)"$`, f.iCallCopyMultipathConfigFileWithRoot)
	s.Step(`^a private mount "([^"]*)"$`, f.aPrivateMount)
	s.Step(`^I call unmountPrivMount$`, f.iCallUnmountPrivMount)
	s.Step(`^lastUnmounted should be "([^"]*)"$`, f.lastUnmountedShouldBe)
	s.Step(`^block volumes are not enabled$`, f.blockVolumesAreNotEnabled)
	s.Step(`^I set transport protocol to "([^"]*)"$`, f.iSetTransportProtocolTo)
	s.Step(`^I invoke createOrUpdateFCHost "([^"]*)"$`, f.iInvokeCreateOrUpdateFCHost)
	s.Step(`^I have a PortCache entry for port "([^"]*)"$`, f.iHaveAPortCacheEntryForPort)
	s.Step(`^I call GetPortIdenfier for "([^"]*)"$`, f.iCallGetPortIdenfierFor)
	s.Step(`^the result is "([^"]*)"$`, f.theResultIs)
	s.Step(`^a non existent port "([^"]*)"$`, f.aNonExistentPort)
	s.Step(`^I have a port "([^"]*)" identifier "([^"]*)" type "([^"]*)"$`, f.iHaveAPortIdentifierType)
	s.Step(`^I have (\d+) sysblock deviceso$`, f.iHaveSysblockDevices)
	s.Step(`^I call linearScanToRemoveDevices$`, f.iCallLinearScanToRemoveDevices)
	s.Step(`^I call verifyAndUpdateInitiatorsInADiffHost for node "([^"]*)"$`, f.iCallverifyAndUpdateInitiatorsInADiffHostForNode)
	s.Step(`^(\d+) valid initiators are returned$`, f.validInitiatorsAreReturned)
	s.Step(`^I check the snapshot license$`, f.iCheckTheSnapshotLicense)
	s.Step(`^I call IsVolumeInSnapSession on "([^"]*)"$`, f.iCallIsVolumeInSnapSessionOn)
	s.Step(`^I call ExecSnapAction to "([^"]*)" snapshot "([^"]*)" to "([^"]*)"$`, f.iCallExecSnapActionToSnapshotTo)
	s.Step(`^I call UnlinkAndTerminate on "([^"]*)"$`, f.iCallUnlinkAndTerminate)
	s.Step(`^I call GetSnapSessions on "([^"]*)"$`, f.iCallGetSnapSessionsOn)
	s.Step(`^I call RemoveTempSnapshot on "([^"]*)"$`, f.iCallRemoveTempSnapshotOn)
	s.Step(`^I call CreateSnapshotFromVolume "([^"]*)"$`, f.iCallCreateSnapshotFromVolume)
	s.Step(`^I call create volume "([^"]*)" from "([^"]*)"$`, f.iCallCreateVolumeFrom)
	s.Step(`^I call Create Volume from Volume$`, f.iCallCreateVolumeFromVolume)
	s.Step(`^I call TerminateSnapshot$`, f.iCallTerminateSnapshot)
	s.Step(`^I call UnlinkAndTerminate snapshot$`, f.iCallUnlinkAndTerminateSnapshot)
	s.Step(`^a valid DeleteSnapshotResponse is returned$`, f.aValidDeleteSnapshotResponseIsReturned)
	s.Step(`^a non-existent volume$`, f.aNonexistentVolume)
	s.Step(`^no volume source$`, f.noVolumeSource)
	s.Step(`^I reset the license cache$`, f.iResetTheLicenseCache)
	s.Step(`^I call MarkSnapshotForDeletion$`, f.iCallMarkSnapshotForDeletion)
	s.Step(`^I check if the snapshot has been deleted$`, f.iCheckIfTheSnapshotHasBeenDeleted)
	s.Step(`^I call IsSnapshotSource$`, f.iCallIsSnapshotSource)
	s.Step(`^I call DeleteSnapshot with "([^"]*)"$`, f.iCallDeleteSnapshotWith)
	s.Step(`^IsSnapshotSource returns "([^"]*)"$`, f.isSnapshotSourceReturns)
	s.Step(`^I queue snapshots for termination$`, f.iQueueSnapshotsForTermination)
	s.Step(`^the deletion worker processes the snapshots successfully$`, f.theDeletionWorkerProcessesTheSnapshotsSuccessfully)
	s.Step(`^I call ensureISCSIDaemonStarted$`, f.iCallEnsureISCSIDaemonStarted)
	s.Step(`^I call requestAddVolumeToSGMV "([^"]*)" mv "([^"]*)"$`, f.iCallRequestAddVolumeToSGMVMv)
	s.Step(`^I call runAddVolumesToSGMV$`, f.iCallRunAddVolumesToSGMV)
	s.Step(`^I call handleAddVolumeToSGMVError$`, f.iCallHandleAddVolumeToSGMVError)
	s.Step(`^I call requestRemoveVolumeFromSGMV "([^"]*)" mv "([^"]*)"$`, f.iCallRequestRemoveVolumeFromSGMVMv)
	s.Step(`^I call runRemoveVolumesFromSGMV$`, f.iCallRunRemoveVolumesFromSGMV)
	s.Step(`^I call handleRemoveVolumeFromSGMVError$`, f.iCallHandleRemoveVolumeFromSGMVError)
	s.Step(`^I enable ISCSI CHAP$`, f.iEnableISCSICHAP)
	s.Step(`^I wait for the execution to complete$`, f.iWaitForTheExecutionToComplete)
	s.Step(`^I call getAndConfigureArrayISCSITargets$`, f.iCallGetAndConfigureArrayISCSITargets)
	s.Step(`^(\d+) targets are returned$`, f.targetsAreReturned)
	s.Step(`^I invalidate symToMaskingViewTarget cache$`, f.iInvalidateSymToMaskingViewTargetCache)
	s.Step(`^I retry on failed snapshot to succeed$`, f.iRetryOnFailedSnapshotToSucceed)
	s.Step(`^I ensure the error is cleared$`, f.iEnsureTheErrorIsCleared)
	s.Step(`^I set ModifyHostName to false$`, f.iSetModifyHostNameToFalse)
	s.Step(`^I set ModifyHostName to true$`, f.iSetModifyHostNameToTrue)
	s.Step(`^I have a NodeNameTemplate "([^"]*)"$`, f.iHaveANodeNameTemplate)
	s.Step(`^I call buildHostIDFromTemplate for node "([^"]*)"$`, f.iCallBuildHostIDFromTemplateForNodeHost)
}
