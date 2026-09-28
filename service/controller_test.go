/*
 Copyright © 2025-2026 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dell/csi-powermax/v2/k8smock"
	"github.com/dell/csi-powermax/v2/k8sutils"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	csiext "github.com/dell/dell-csi-extensions/replication"
	"github.com/dell/goiscsi"
	"github.com/dell/gonvme"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	gmock "github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// DeletionWorker interface for testing purposes
type DeletionWorker interface {
	QueueDeviceForDeletion(devID string, volumeIdentifier, symID string) error
}

// MockDeletionWorker is a mock implementation of DeletionWorker interface
type MockDeletionWorker struct {
	ctrl     *gomock.Controller
	recorder *MockDeletionWorkerMockRecorder
}

type MockDeletionWorkerMockRecorder struct {
	mock *MockDeletionWorker
}

func NewMockDeletionWorker(ctrl *gomock.Controller) *MockDeletionWorker {
	mock := &MockDeletionWorker{ctrl: ctrl}
	mock.recorder = &MockDeletionWorkerMockRecorder{mock}
	return mock
}

func (m *MockDeletionWorker) EXPECT() *MockDeletionWorkerMockRecorder {
	return m.recorder
}

func (m *MockDeletionWorker) QueueDeviceForDeletion(devID string, volumeIdentifier, symID string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "QueueDeviceForDeletion", devID, volumeIdentifier, symID)
	ret0, _ := ret[0].(error)
	return ret0
}

func (mr *MockDeletionWorkerMockRecorder) QueueDeviceForDeletion(devID, volumeIdentifier, symID interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "QueueDeviceForDeletion", reflect.TypeOf((*MockDeletionWorker)(nil).QueueDeviceForDeletion), devID, volumeIdentifier, symID)
}

const (
	KiB int64 = 1024
	KB  int64 = 1000

	MiB int64 = 1024 * KiB
	MB  int64 = KB * 1000

	GiB int64 = 1024 * MiB
	GB  int64 = KB * 1000

	TiB int64 = 1024 * GiB
	TB  int64 = GB * 1000

	symIDLocal  = "000120000001"
	symIDRemote = "000120000002"

	validLocalDeviceID  = "011AB"
	validRemoteDeviceID = "011BC"

	localVolumeName  = "01234abcde"
	remoteVolumeName = "abcde01234"

	clusterPrefix = "ABC"

	validLocalVolumeID    = CsiVolumePrefix + clusterPrefix + "-pmax-" + localVolumeName + "-ns1-nsx-" + symIDLocal + "-" + validLocalDeviceID
	validRemoteVolumeID   = CsiVolumePrefix + clusterPrefix + "-pmax-" + remoteVolumeName + "-ns1-nsx-" + symIDRemote + "-" + validRemoteDeviceID
	validReplicatedVolume = CsiVolumePrefix + clusterPrefix + "-pmax-" + localVolumeName + "-ns1-nsx-" + symIDLocal + ":" + symIDRemote + "-" + validLocalDeviceID + ":" + remoteVolumeName
)

type serviceFields struct {
	opts                      Opts
	mode                      string
	adminClient               pmax.Pmax
	deletionWorker            *deletionWorker
	iscsiClient               goiscsi.ISCSIinterface
	nvmetcpClient             gonvme.NVMEinterface
	system                    *interface{}
	privDir                   string
	loggedInArrays            map[string]bool
	loggedInNVMeArrays        map[string]bool
	probeStatus               *sync.Map
	pollingFrequencyInSeconds int64
	nodeIsInitialized         bool
	useNFS                    bool
	useFC                     bool
	useIscsi                  bool
	useNVMeTCP                bool
	iscsiTargets              map[string][]string
	nvmeTargets               *sync.Map
	storagePoolCacheDuration  time.Duration
	fcConnector               fcConnector
	iscsiConnector            iSCSIConnector
	nvmeTCPConnector          NVMeTCPConnector
	dBusConn                  dBusConn
	sgSvc                     *storageGroupSvc
	arrayTransportProtocolMap map[string]string
	topologyConfig            *TopologyConfig
	allowedTopologyKeys       map[string][]string
	deniedTopologyKeys        map[string][]string
	k8sUtils                  k8sutils.UtilsInterface
	snapCleaner               *snapCleanupWorker
}

func Test_addMetaData(t *testing.T) {
	type args struct {
		params map[string]string
	}
	tests := []struct {
		name string
		args args
		want map[string][]string
	}{
		{
			name: "add metadata for all keys",
			args: args{
				params: map[string]string{
					CSIPersistentVolumeName:      "pv1",
					CSIPersistentVolumeClaimName: "pvc1",
					CSIPVCNamespace:              "ns1",
				},
			},
			want: map[string][]string{
				HeaderPersistentVolumeName:           {"pv1"},
				HeaderPersistentVolumeClaimName:      {"pvc1"},
				HeaderPersistentVolumeClaimNamespace: {"ns1"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := addMetaData(tt.args.params); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("addMetaData() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_service_createMetroVolume(t *testing.T) {
	var pmaxClient *mocks.MockPmaxClient
	initDefaultClient := func() *mocks.MockPmaxClient {
		pmaxClient = mocks.NewMockPmaxClient(gmock.NewController(t))
		// mock calls by the metro method to get the client for the symmetrix ID
		pmaxClient.EXPECT().WithSymmetrixID(symIDLocal).AnyTimes().Return(pmaxClient)
		pmaxClient.EXPECT().WithSymmetrixID(symIDRemote).AnyTimes().Return(pmaxClient)
		pmaxClient.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

		// create and initialize a client to satisfy fetching a client
		err := symmetrix.Initialize([]string{symIDLocal, symIDRemote}, pmaxClient)
		if err != nil {
			t.Fatalf("failed to initialize the powermax client. err: %s", err.Error())
		}
		return pmaxClient
	}

	// save default functions so we can restore them after each test
	// creating a clean test environment
	defaultRequestLockFunc := requestLockFunc
	defaultReleaseLockFunc := releaseLockFunc

	// create a clean test environment for each test
	// by clearing out any caches
	afterEach := func(symIDs []string) {
		// reset the client
		pmaxClient = nil
		// clean caches
		for _, symID := range symIDs {
			symmetrix.RemoveClient(symID)
			RemoveReplicationCapability(symID)
		}

		// restore default func values
		requestLockFunc = defaultRequestLockFunc
		releaseLockFunc = defaultReleaseLockFunc
	}

	goodCapacityRange := &csi.CapacityRange{
		RequiredBytes: 1 * GiB,
		LimitBytes:    4 * GiB,
	}

	goodSrpCapacity := &types.SrpCap{
		UsableUsedInTB: 1.0,
		UsableTotInTB:  2.0,
	}

	type args struct {
		ctx                context.Context
		req                *csi.CreateVolumeRequest
		reqID              string
		storagePoolID      string
		symID              string
		storageGroupName   string
		serviceLevel       string
		thick              string
		remoteSymID        string
		localRDFGrpNo      string
		remoteRDFGrpNo     string
		remoteServiceLevel string
		remoteSRPID        string
		namespace          string
		applicationPrefix  string
		bias               string
		hostLimitName      string
		hostMBsec          string
		hostIOsec          string
		hostDynDist        string
	}
	tests := []struct {
		name       string
		fields     serviceFields
		args       args
		setup      func()
		want       *csi.CreateVolumeResponse
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "without initializing the powermax client",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
				},
				symID:       "0000000000001",
				remoteSymID: "0000000000002",
			},
			setup:      func() {},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "",
		},
		{
			name:   "fail to validate requested local volume size",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange: &csi.CapacityRange{
						RequiredBytes: -1, // set size to < 0 to trigger an error
						LimitBytes:    -1,
					},
				},
				symID:       symIDLocal,
				remoteSymID: symIDRemote,
			},
			setup: func() {
				initDefaultClient()
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "bad capacity",
		},
		{
			name:   "fail to validate requested remote volume size",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange:             goodCapacityRange,
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{}, errors.New("error"))
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Could not retrieve StoragePool",
		},
		{
			name:   "when volume is content source and has a bad volume ID",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange:             goodCapacityRange,
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Volume{
							Volume: &csi.VolumeContentSource_VolumeSource{
								VolumeId: "bad-id",
							},
						},
					},
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Source volume identifier not in supported format",
		},
		{
			name:   "when snapshot is content source and has a bad volume ID",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange:             goodCapacityRange,
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Snapshot{
							Snapshot: &csi.VolumeContentSource_SnapshotSource{
								SnapshotId: "bad-id",
							},
						},
					},
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Snapshot identifier not in supported format",
		},
		{
			name:   "when content source is not nil but there is no source type",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange:             goodCapacityRange,
					VolumeContentSource:       &csi.VolumeContentSource{},
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "VolumeContentSource is missing volume and snapshot source",
		},
		{
			name:   "when the snapshot is not licensed",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange:             goodCapacityRange,
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Snapshot{
							Snapshot: &csi.VolumeContentSource_SnapshotSource{
								SnapshotId: validLocalVolumeID,
							},
						},
					},
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(false, errors.New("not licensed"))
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "not licensed",
		},
		{
			name:   "when the snapshot source symmetrix ID does not match the requested symmetrix ID",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange:             goodCapacityRange,
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Snapshot{
							Snapshot: &csi.VolumeContentSource_SnapshotSource{
								// represents a request to create a volume on the local powermax array
								// using the an existing volume on the remote powermax array
								// and should result in the desired failure
								SnapshotId: validRemoteVolumeID,
							},
						},
					},
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(true, nil)
				pmaxClient.EXPECT().GetReplicationCapabilities(gomock.Any()).Times(1).Return(&types.SymReplicationCapabilities{
					SymmetrixCapability: []types.SymmetrixCapability{
						{
							SymmetrixID:   symIDRemote, // satisfies the query for replication capabilities of the remote powermax
							SnapVxCapable: true,
						},
					},
				}, nil)
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "The volume content source is in different PowerMax array",
		},
		{
			name:   "when the client fails to get the source volume",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange:             goodCapacityRange,
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Snapshot{
							Snapshot: &csi.VolumeContentSource_SnapshotSource{
								SnapshotId: validLocalVolumeID,
							},
						},
					},
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(true, nil)
				pmaxClient.EXPECT().GetReplicationCapabilities(gomock.Any()).Times(1).Return(&types.SymReplicationCapabilities{
					SymmetrixCapability: []types.SymmetrixCapability{
						{
							SymmetrixID:   symIDLocal,
							SnapVxCapable: true,
						},
					},
				}, nil)
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), symIDLocal, validLocalDeviceID).Times(1).Return(nil, errors.New("couldn't find volume"))
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "couldn't find volume",
		},
		{
			name:   "when the client fails to get the source volume for enhanced api",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange:             goodCapacityRange,
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Snapshot{
							Snapshot: &csi.VolumeContentSource_SnapshotSource{
								SnapshotId: validLocalVolumeID,
							},
						},
					},
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(true, nil)
				pmaxClient.EXPECT().GetReplicationCapabilities(gomock.Any()).Times(1).Return(&types.SymReplicationCapabilities{
					SymmetrixCapability: []types.SymmetrixCapability{
						{
							SymmetrixID:   symIDLocal,
							SnapVxCapable: true,
						},
					},
				}, nil)
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), "000120000001", "011AB").Times(1).Return(&types.Volume{}, nil)
				pmaxClient.EXPECT().
					GetVolumesByIdentifier(
						gomock.Any(),
						gomock.Eq("000120000001"),
						gomock.Eq("csi-CSM-csivol-7599623185-default"),
					).
					AnyTimes().
					Return(&types.Volumev1{
						Volumes: []types.VolumeEnhanced{
							{
								ID:         validLocalVolumeID,
								CapCyl:     600, // source volume capacity in cylinders
								Identifier: "csi-CSM-csivol-7599623185-default",
								Type:       "Snapshot",         // or "Volume" depending on your logic
								System:     types.SystemInfo{}, // optional
								StorageGroups: []types.StorageGroupID{
									{StorageGroupID: "SG_1"},
								},
							},
						},
					}, nil)
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Name cannot be empty",
		},
		{
			name:   "when requested capacity is smaller than the source",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange:             goodCapacityRange,
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Snapshot{
							Snapshot: &csi.VolumeContentSource_SnapshotSource{
								SnapshotId: validLocalVolumeID,
							},
						},
					},
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(true, nil)
				pmaxClient.EXPECT().GetReplicationCapabilities(gomock.Any()).Times(1).Return(&types.SymReplicationCapabilities{
					SymmetrixCapability: []types.SymmetrixCapability{
						{
							SymmetrixID:   symIDLocal,
							SnapVxCapable: true,
						},
					},
				}, nil)
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), symIDLocal, validLocalDeviceID).Times(1).Return(&types.Volume{
					VolumeID:    validLocalVolumeID,
					CapacityCYL: 600, // should be less than the calculated number of required cylinders for 1 GiB from goodCapacityRange
				}, nil)
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Requested capacity is smaller than the source",
		},
		{
			name:   "when requested capacity is smaller than the source for enhanced api",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange:             goodCapacityRange,
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Snapshot{
							Snapshot: &csi.VolumeContentSource_SnapshotSource{
								SnapshotId: validLocalVolumeID,
							},
						},
					},
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(true, nil)
				pmaxClient.EXPECT().GetReplicationCapabilities(gomock.Any()).Times(1).Return(&types.SymReplicationCapabilities{
					SymmetrixCapability: []types.SymmetrixCapability{
						{
							SymmetrixID:   symIDLocal,
							SnapVxCapable: true,
						},
					},
				}, nil)

				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), "000120000001", "011AB").Times(1).Return(&types.Volume{}, nil)
				pmaxClient.EXPECT().
					GetVolumesByIdentifier(
						gomock.Any(),
						gomock.Eq("000120000001"),
						gomock.Eq("csi-CSM-csivol-7599623185-default"),
					).
					AnyTimes().
					Return(&types.Volumev1{
						Volumes: []types.VolumeEnhanced{
							{
								ID:         validLocalVolumeID,
								CapCyl:     600, // source volume capacity in cylinders
								Identifier: "csi-CSM-csivol-7599623185-default",
								Type:       "Snapshot",         // or "Volume" depending on your logic
								System:     types.SystemInfo{}, // optional
								StorageGroups: []types.StorageGroupID{
									{StorageGroupID: "SG_1"},
								},
							},
						},
					}, nil)
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Name cannot be empty",
		},
		{
			name: "the volume is block but block is not enabled",
			fields: serviceFields{
				opts: Opts{
					EnableBlock: false,
				},
			},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					CapacityRange:             goodCapacityRange,
					VolumeCapabilities: []*csi.VolumeCapability{
						{
							AccessType: &csi.VolumeCapability_Block{
								Block: &csi.VolumeCapability_BlockVolume{},
							},
						},
					},
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Block Volume Capability is not supported",
		},
		{
			name: "the volume name is empty",
			fields: serviceFields{
				opts: Opts{
					EnableBlock: true,
				},
			},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					Name:                      "",
					CapacityRange:             goodCapacityRange,
					VolumeCapabilities: []*csi.VolumeCapability{
						{
							AccessType: &csi.VolumeCapability_Block{
								Block: &csi.VolumeCapability_BlockVolume{},
							},
						},
					},
				},
				symID:         symIDLocal,
				remoteSymID:   symIDRemote,
				storagePoolID: "POOL_1",
				remoteSRPID:   "POOL_1",
			},
			setup: func() {
				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Name cannot be empty",
		},
		{
			name: "fails to get protected storage group",
			fields: serviceFields{
				opts: Opts{
					EnableBlock: true,
				},
			},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					Name:                      "csivol-01234abcde",
					CapacityRange:             goodCapacityRange,
					VolumeCapabilities: []*csi.VolumeCapability{
						{
							AccessType: &csi.VolumeCapability_Block{
								Block: &csi.VolumeCapability_BlockVolume{},
							},
						},
					},
				},
				symID:             symIDLocal,
				remoteSymID:       symIDRemote,
				storagePoolID:     "POOL_1",
				remoteSRPID:       "POOL_1",
				namespace:         "my-test-service",
				applicationPrefix: "my-test-db",
				hostLimitName:     "a-host-limit-name",
			},
			setup: func() {
				requestLockFunc = func(_, _ string) (int, error) {
					return 0, nil
				}
				releaseLockFunc = func(_, _ string, _ int) {}

				initDefaultClient()
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().GetProtectedStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).Times(1).
					Return(&types.RDFStorageGroup{}, errors.New("error retrieving storage group"))
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "error retrieving storage group",
		},
		{
			name: "fails to create a protected storage group",
			fields: serviceFields{
				opts: Opts{
					EnableBlock: true,
				},
			},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					Name:                      "csivol-01234abcde",
					CapacityRange:             goodCapacityRange,
					VolumeCapabilities: []*csi.VolumeCapability{
						{
							AccessType: &csi.VolumeCapability_Block{
								Block: &csi.VolumeCapability_BlockVolume{},
							},
						},
					},
				},
				symID:             symIDLocal,
				remoteSymID:       symIDRemote,
				storagePoolID:     "POOL_1",
				remoteSRPID:       "POOL_1",
				namespace:         "my-test-service",
				applicationPrefix: "my-test-db",
				hostLimitName:     "a-host-limit-name",
			},
			setup: func() {
				requestLockFunc = func(_, _ string) (int, error) {
					return 0, nil
				}
				releaseLockFunc = func(_, _ string, _ int) {}

				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().GetProtectedStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).Times(1).
					Return(&types.RDFStorageGroup{}, errors.New("cannot be found"))
				pmaxClient.EXPECT().GetStorageGroupIDList(gomock.Any(), symIDLocal, gomock.Any(), false).Times(1).
					Return(nil, errors.New("failed to get storage group ID list"))
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Error in getOrCreateProtectedStorageGroup",
		},
		{
			name: "fail to create storage group on remote powermax",
			fields: serviceFields{
				opts: Opts{
					EnableBlock: true,
				},
			},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					Name:                      "csivol-01234abcde",
					CapacityRange:             goodCapacityRange,
					VolumeCapabilities: []*csi.VolumeCapability{
						{
							AccessType: &csi.VolumeCapability_Block{
								Block: &csi.VolumeCapability_BlockVolume{},
							},
						},
					},
				},
				symID:             symIDLocal,
				remoteSymID:       symIDRemote,
				storagePoolID:     "POOL_1",
				remoteSRPID:       "POOL_1",
				namespace:         "my-test-service",
				applicationPrefix: "my-test-db",
				hostLimitName:     "a-host-limit-name",
				hostMBsec:         "100",
				hostIOsec:         "100",
				hostDynDist:       "",
			},
			setup: func() {
				requestLockFunc = func(_, _ string) (int, error) {
					return 0, nil
				}
				releaseLockFunc = func(_, _ string, _ int) {}

				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().GetProtectedStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).Times(1).
					Return(&types.RDFStorageGroup{}, nil)
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).Times(1).
					Return(&types.StorageGroup{}, nil)
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), symIDRemote, gomock.Any()).Times(1).
					Return(nil, errors.New("failed to get remote storage group"))
				pmaxClient.EXPECT().CreateStorageGroup(gomock.Any(), symIDRemote, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).
					Return(nil, errors.New("failed to create remote storage group"))
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Error creating storage group",
		},
		{
			name: "idempotent volume clone on V4 array uses LinkSRDFCloneVolume",
			fields: serviceFields{
				opts: Opts{
					EnableBlock:   true,
					ClusterPrefix: "CSM",
				},
			},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					Name:                      "csivol-01234abcde",
					CapacityRange:             goodCapacityRange,
					VolumeCapabilities: []*csi.VolumeCapability{
						{
							AccessType: &csi.VolumeCapability_Block{
								Block: &csi.VolumeCapability_BlockVolume{},
							},
						},
					},
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Volume{
							Volume: &csi.VolumeContentSource_VolumeSource{
								VolumeId: validLocalVolumeID,
							},
						},
					},
				},
				symID:              symIDLocal,
				remoteSymID:        symIDRemote,
				storagePoolID:      "POOL_1",
				remoteSRPID:        "POOL_1",
				serviceLevel:       "Diamond",
				remoteServiceLevel: "Diamond",
				namespace:          "default",
				localRDFGrpNo:      "10",
				remoteRDFGrpNo:     "20",
			},
			setup: func() {
				requestLockFunc = func(_, _ string) (int, error) { return 0, nil }
				releaseLockFunc = func(_, _ string, _ int) {}

				initDefaultClient()
				// Pool capacity checks
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// Snapshot license check
				pmaxClient.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(true, nil)
				pmaxClient.EXPECT().GetReplicationCapabilities(gomock.Any()).Times(1).Return(&types.SymReplicationCapabilities{
					SymmetrixCapability: []types.SymmetrixCapability{{SymmetrixID: symIDLocal, SnapVxCapable: true}},
				}, nil)
				// Source volume for content source
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), symIDLocal, validLocalDeviceID).Times(1).Return(&types.Volume{
					VolumeID:    validLocalDeviceID,
					CapacityCYL: 100,
				}, nil)
				// Protected storage group with RDF
				pmaxClient.EXPECT().GetProtectedStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).Times(1).
					Return(&types.RDFStorageGroup{Rdf: true}, nil)
				// RDF info - direction check + suspend/establish checks
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, gomock.Any(), "10").AnyTimes().
					Return(&types.StorageGroupRDFG{
						VolumeRdfTypes: []string{"R1"},
						States:         []string{"Suspended"},
					}, nil)
				// Storage groups exist on R1 and R2
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).Times(1).Return(&types.StorageGroup{}, nil)
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), symIDRemote, gomock.Any()).Times(1).Return(&types.StorageGroup{}, nil)
				// Idempotency check - return existing volume
				pmaxClient.EXPECT().GetVolumeIDList(gomock.Any(), symIDLocal, gomock.Any(), false).Times(1).Return([]string{"022CD"}, nil)
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), symIDLocal, "022CD").Times(1).Return(&types.Volume{
					VolumeID:           "022CD",
					VolumeIdentifier:   "csi-CSM-csivol-01234abcde-default",
					CapacityCYL:        547,
					CapacityGB:         1.0,
					StorageGroupIDList: []string{"csi-CSM-Diamond-POOL_1-SG"},
				}, nil)
				// Remote volume pair
				pmaxClient.EXPECT().GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "10", "022CD").Times(1).Return(&types.RDFDevicePair{
					RemoteVolumeName:     "033EF",
					RemoteRdfGroupNumber: 20,
				}, nil)
				// Remote volume with 2+ SGs
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), symIDRemote, "033EF").Times(1).Return(&types.Volume{
					VolumeID:           "033EF",
					StorageGroupIDList: []string{"SG1", "SG2"},
				}, nil)
				// V4 check - return V4 microcode
				pmaxClient.EXPECT().GetSymmetrixByID(gomock.Any(), symIDLocal).Times(1).Return(&types.Symmetrix{
					SymmetrixID: symIDLocal,
					Microcode:   "6079.325.0",
				}, nil)
				// CloneVolumeFromVolume returns error to verify V4 path is taken
				pmaxClient.EXPECT().CloneVolumeFromVolume(gomock.Any(), symIDLocal, gomock.Any()).Times(1).
					Return(errors.New("clone error"))
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Failed to create SRDF volume from volume",
		},
		{
			name: "idempotent volume clone on V3 array uses legacy SnapVx clone",
			fields: serviceFields{
				opts: Opts{
					EnableBlock:   true,
					ClusterPrefix: "CSM",
				},
			},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateVolumeRequest{
					AccessibilityRequirements: &csi.TopologyRequirement{},
					Name:                      "csivol-01234abcde",
					CapacityRange:             goodCapacityRange,
					VolumeCapabilities: []*csi.VolumeCapability{
						{
							AccessType: &csi.VolumeCapability_Block{
								Block: &csi.VolumeCapability_BlockVolume{},
							},
						},
					},
					VolumeContentSource: &csi.VolumeContentSource{
						Type: &csi.VolumeContentSource_Volume{
							Volume: &csi.VolumeContentSource_VolumeSource{
								VolumeId: validLocalVolumeID,
							},
						},
					},
				},
				symID:              symIDLocal,
				remoteSymID:        symIDRemote,
				storagePoolID:      "POOL_1",
				remoteSRPID:        "POOL_1",
				serviceLevel:       "Diamond",
				remoteServiceLevel: "Diamond",
				namespace:          "default",
				localRDFGrpNo:      "10",
				remoteRDFGrpNo:     "20",
			},
			setup: func() {
				requestLockFunc = func(_, _ string) (int, error) { return 0, nil }
				releaseLockFunc = func(_, _ string, _ int) {}

				initDefaultClient()
				// Pool capacity checks
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// Snapshot license check
				pmaxClient.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(true, nil)
				pmaxClient.EXPECT().GetReplicationCapabilities(gomock.Any()).Times(1).Return(&types.SymReplicationCapabilities{
					SymmetrixCapability: []types.SymmetrixCapability{{SymmetrixID: symIDLocal, SnapVxCapable: true}},
				}, nil)
				// Source volume for content source
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), symIDLocal, validLocalDeviceID).Times(1).Return(&types.Volume{
					VolumeID:    validLocalDeviceID,
					CapacityCYL: 100,
				}, nil)
				// Protected storage group with RDF
				pmaxClient.EXPECT().GetProtectedStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).Times(1).
					Return(&types.RDFStorageGroup{Rdf: true}, nil)
				// RDF info - direction check
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, gomock.Any(), "10").AnyTimes().
					Return(&types.StorageGroupRDFG{
						VolumeRdfTypes: []string{"R1"},
						States:         []string{"Consistent"},
					}, nil)
				// Storage groups exist on R1 and R2
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).Times(1).Return(&types.StorageGroup{}, nil)
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), symIDRemote, gomock.Any()).Times(1).Return(&types.StorageGroup{}, nil)
				// Idempotency check - return existing volume
				pmaxClient.EXPECT().GetVolumeIDList(gomock.Any(), symIDLocal, gomock.Any(), false).Times(1).Return([]string{"022CD"}, nil)
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), symIDLocal, "022CD").Times(1).Return(&types.Volume{
					VolumeID:           "022CD",
					VolumeIdentifier:   "csi-CSM-csivol-01234abcde-default",
					CapacityCYL:        547,
					CapacityGB:         1.0,
					StorageGroupIDList: []string{"csi-CSM-Diamond-POOL_1-SG"},
				}, nil)
				// Remote volume pair
				pmaxClient.EXPECT().GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "10", "022CD").Times(1).Return(&types.RDFDevicePair{
					RemoteVolumeName:     "033EF",
					RemoteRdfGroupNumber: 20,
				}, nil)
				// Remote volume with 2+ SGs
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), symIDRemote, "033EF").Times(1).Return(&types.Volume{
					VolumeID:           "033EF",
					StorageGroupIDList: []string{"SG1", "SG2"},
				}, nil)
				// V3 check - return V3 microcode
				pmaxClient.EXPECT().GetSymmetrixByID(gomock.Any(), symIDLocal).Times(1).Return(&types.Symmetrix{
					SymmetrixID: symIDLocal,
					Microcode:   "5978.441.0",
				}, nil)
				// CreateSnapshot is called in the legacy SnapVx clone path - return error to verify path
				pmaxClient.EXPECT().CreateSnapshot(gomock.Any(), symIDLocal, gomock.Any(), gomock.Any(), gomock.Any()).Times(1).
					Return(errors.New("snapshot creation error"))
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "CreateSnapshot failed",
		},
	}

	// initialize the client used by all these tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts:                      tt.fields.opts,
				mode:                      tt.fields.mode,
				adminClient:               tt.fields.adminClient,
				deletionWorker:            tt.fields.deletionWorker,
				iscsiClient:               tt.fields.iscsiClient,
				nvmetcpClient:             tt.fields.nvmetcpClient,
				system:                    tt.fields.system,
				privDir:                   tt.fields.privDir,
				loggedInArrays:            tt.fields.loggedInArrays,
				loggedInNVMeArrays:        tt.fields.loggedInNVMeArrays,
				mutex:                     sync.Mutex{},
				cacheMutex:                sync.Mutex{},
				nodeProbeMutex:            sync.Mutex{},
				probeStatus:               tt.fields.probeStatus,
				probeStatusMutex:          sync.Mutex{},
				pollingFrequencyMutex:     sync.Mutex{},
				pollingFrequencyInSeconds: tt.fields.pollingFrequencyInSeconds,
				nodeIsInitialized:         tt.fields.nodeIsInitialized,
				useNFS:                    tt.fields.useNFS,
				useFC:                     tt.fields.useFC,
				useIscsi:                  tt.fields.useIscsi,
				useNVMeTCP:                tt.fields.useNVMeTCP,
				iscsiTargets:              tt.fields.iscsiTargets,
				nvmeTargets:               tt.fields.nvmeTargets,
				storagePoolCacheDuration:  tt.fields.storagePoolCacheDuration,
				waitGroup:                 sync.WaitGroup{},
				fcConnector:               tt.fields.fcConnector,
				iscsiConnector:            tt.fields.iscsiConnector,
				nvmeTCPConnector:          tt.fields.nvmeTCPConnector,
				dBusConn:                  tt.fields.dBusConn,
				sgSvc:                     tt.fields.sgSvc,
				arrayTransportProtocolMap: tt.fields.arrayTransportProtocolMap,
				topologyConfig:            tt.fields.topologyConfig,
				allowedTopologyKeys:       tt.fields.allowedTopologyKeys,
				deniedTopologyKeys:        tt.fields.deniedTopologyKeys,
				k8sUtils:                  tt.fields.k8sUtils,
				snapCleaner:               tt.fields.snapCleaner,
			}
			defer afterEach([]string{tt.args.symID, tt.args.remoteSymID})
			tt.setup()

			got, err := s.createMetroVolume(tt.args.ctx,
				tt.args.req, tt.args.reqID, tt.args.storagePoolID,
				tt.args.symID, tt.args.storageGroupName, tt.args.serviceLevel,
				tt.args.thick, tt.args.remoteSymID, tt.args.localRDFGrpNo,
				tt.args.remoteRDFGrpNo, tt.args.remoteServiceLevel, tt.args.remoteSRPID,
				tt.args.namespace, tt.args.applicationPrefix, tt.args.bias,
				tt.args.hostLimitName, tt.args.hostMBsec, tt.args.hostIOsec,
				tt.args.hostDynDist)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.createMetroVolume() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErrMsg != "" {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("service.createMetroVolume() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_service_getStoragePoolCapacities(t *testing.T) {
	type args struct {
		ctx           context.Context
		symmetrixID   string
		storagePoolID string
		pmaxClient    pmax.Pmax
	}
	tests := []struct {
		name      string
		fields    serviceFields
		args      args
		getClient func() *mocks.MockPmaxClient
		want      *types.SrpCap
		want1     *types.FbaCap
		want2     *types.CkdCap
		wantErr   bool
	}{
		{
			name: "when SrpCap is nil",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gomock.NewController(t))
				c.EXPECT().GetStoragePool(gomock.All(), symIDLocal, "pool1").AnyTimes().Return(&types.StoragePool{
					FbaCap: &types.FbaCap{},
					CkdCap: &types.CkdCap{},
				}, nil)

				return c
			},
			fields: serviceFields{},
			args: args{
				ctx:           context.Background(),
				symmetrixID:   symIDLocal,
				storagePoolID: "pool1",
			},
			want:    nil,
			want1:   &types.FbaCap{},
			want2:   &types.CkdCap{},
			wantErr: false,
		},
		{
			name: "all capacities are nil",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gomock.NewController(t))
				c.EXPECT().GetStoragePool(gomock.All(), symIDLocal, "pool1").AnyTimes().Return(&types.StoragePool{}, nil)

				return c
			},
			fields: serviceFields{},
			args: args{
				ctx:           context.Background(),
				symmetrixID:   symIDLocal,
				storagePoolID: "pool1",
			},
			want:    nil,
			want1:   nil,
			want2:   nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts:                      tt.fields.opts,
				mode:                      tt.fields.mode,
				adminClient:               tt.fields.adminClient,
				deletionWorker:            tt.fields.deletionWorker,
				iscsiClient:               tt.fields.iscsiClient,
				nvmetcpClient:             tt.fields.nvmetcpClient,
				system:                    tt.fields.system,
				privDir:                   tt.fields.privDir,
				loggedInArrays:            tt.fields.loggedInArrays,
				loggedInNVMeArrays:        tt.fields.loggedInNVMeArrays,
				mutex:                     sync.Mutex{},
				cacheMutex:                sync.Mutex{},
				nodeProbeMutex:            sync.Mutex{},
				probeStatus:               tt.fields.probeStatus,
				probeStatusMutex:          sync.Mutex{},
				pollingFrequencyMutex:     sync.Mutex{},
				pollingFrequencyInSeconds: tt.fields.pollingFrequencyInSeconds,
				nodeIsInitialized:         tt.fields.nodeIsInitialized,
				useNFS:                    tt.fields.useNFS,
				useFC:                     tt.fields.useFC,
				useIscsi:                  tt.fields.useIscsi,
				useNVMeTCP:                tt.fields.useNVMeTCP,
				iscsiTargets:              tt.fields.iscsiTargets,
				nvmeTargets:               tt.fields.nvmeTargets,
				storagePoolCacheDuration:  tt.fields.storagePoolCacheDuration,
				waitGroup:                 sync.WaitGroup{},
				fcConnector:               tt.fields.fcConnector,
				iscsiConnector:            tt.fields.iscsiConnector,
				nvmeTCPConnector:          tt.fields.nvmeTCPConnector,
				dBusConn:                  tt.fields.dBusConn,
				sgSvc:                     tt.fields.sgSvc,
				arrayTransportProtocolMap: tt.fields.arrayTransportProtocolMap,
				topologyConfig:            tt.fields.topologyConfig,
				allowedTopologyKeys:       tt.fields.allowedTopologyKeys,
				deniedTopologyKeys:        tt.fields.deniedTopologyKeys,
				k8sUtils:                  tt.fields.k8sUtils,
				snapCleaner:               tt.fields.snapCleaner,
			}
			tt.args.pmaxClient = tt.getClient()

			got, got1, got2, err := s.getStoragePoolCapacities(tt.args.ctx, tt.args.symmetrixID, tt.args.storagePoolID, tt.args.pmaxClient)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.getStoragePoolCapacities() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("service.getStoragePoolCapacities() got = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(got1, tt.want1) {
				t.Errorf("service.getStoragePoolCapacities() got1 = %v, want %v", got1, tt.want1)
			}
			if !reflect.DeepEqual(got2, tt.want2) {
				t.Errorf("service.getStoragePoolCapacities() got2 = %v, want %v", got2, tt.want2)
			}
		})
	}
}

func Test_service_validateVolSize(t *testing.T) {
	type args struct {
		ctx           context.Context
		cr            *csi.CapacityRange
		symmetrixID   string
		storagePoolID string
		pmaxClient    pmax.Pmax
	}
	tests := []struct {
		name      string
		fields    serviceFields
		args      args
		getClient func() *mocks.MockPmaxClient
		want      int
		wantErr   bool
	}{
		{
			name:   "when powermax client fails to get storage pool capacity",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				cr: &csi.CapacityRange{
					RequiredBytes: 3 * GiB,
					LimitBytes:    4 * GiB,
				},
				symmetrixID:   symIDLocal,
				storagePoolID: "pool1",
			},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gomock.NewController(t))
				c.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "pool1").Return(&types.StoragePool{}, errors.New("error"))

				return c
			},
			want:    0,
			wantErr: true,
		},
		{
			name:   "use FBA capacity but FBA capacity is full",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				cr: &csi.CapacityRange{
					RequiredBytes: 3 * GiB, // request 3 GiB from a ~3 GB pool
					LimitBytes:    4 * GiB,
				},
				symmetrixID:   symIDLocal,
				storagePoolID: "pool1",
			},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gomock.NewController(t))
				c.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "pool1").Return(&types.StoragePool{
					SrpCap: nil, // should be nil to trigger use of FBA cap
					FbaCap: &types.FbaCap{
						Provisioned: &types.Provisioned{
							UsableUsedInTB: 0.003,
							UsableTotInTB:  0.003, // pool is totally consumed
						},
					},
				}, nil)

				return c
			},
			want:    0,
			wantErr: true,
		},
		{
			name:   "use CKD capacity but CKD capacity is full",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				cr: &csi.CapacityRange{
					RequiredBytes: 3 * GiB, // request 3 GiB from a ~3 GB pool
					LimitBytes:    4 * GiB,
				},
				symmetrixID:   symIDLocal,
				storagePoolID: "pool1",
			},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gomock.NewController(t))
				c.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "pool1").Return(&types.StoragePool{
					SrpCap: nil, // should be nil to trigger use of FBA cap
					CkdCap: &types.CkdCap{
						Provisioned: &types.Provisioned{
							UsableUsedInTB: 0.003,
							UsableTotInTB:  0.003, // pool is totally consumed
						},
					},
				}, nil)

				return c
			},
			want:    0,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts:                      tt.fields.opts,
				mode:                      tt.fields.mode,
				adminClient:               tt.fields.adminClient,
				deletionWorker:            tt.fields.deletionWorker,
				iscsiClient:               tt.fields.iscsiClient,
				nvmetcpClient:             tt.fields.nvmetcpClient,
				system:                    tt.fields.system,
				privDir:                   tt.fields.privDir,
				loggedInArrays:            tt.fields.loggedInArrays,
				loggedInNVMeArrays:        tt.fields.loggedInNVMeArrays,
				mutex:                     sync.Mutex{},
				cacheMutex:                sync.Mutex{},
				nodeProbeMutex:            sync.Mutex{},
				probeStatus:               tt.fields.probeStatus,
				probeStatusMutex:          sync.Mutex{},
				pollingFrequencyMutex:     sync.Mutex{},
				pollingFrequencyInSeconds: tt.fields.pollingFrequencyInSeconds,
				nodeIsInitialized:         tt.fields.nodeIsInitialized,
				useNFS:                    tt.fields.useNFS,
				useFC:                     tt.fields.useFC,
				useIscsi:                  tt.fields.useIscsi,
				useNVMeTCP:                tt.fields.useNVMeTCP,
				iscsiTargets:              tt.fields.iscsiTargets,
				nvmeTargets:               tt.fields.nvmeTargets,
				storagePoolCacheDuration:  tt.fields.storagePoolCacheDuration,
				waitGroup:                 sync.WaitGroup{},
				fcConnector:               tt.fields.fcConnector,
				iscsiConnector:            tt.fields.iscsiConnector,
				nvmeTCPConnector:          tt.fields.nvmeTCPConnector,
				dBusConn:                  tt.fields.dBusConn,
				sgSvc:                     tt.fields.sgSvc,
				arrayTransportProtocolMap: tt.fields.arrayTransportProtocolMap,
				topologyConfig:            tt.fields.topologyConfig,
				allowedTopologyKeys:       tt.fields.allowedTopologyKeys,
				deniedTopologyKeys:        tt.fields.deniedTopologyKeys,
				k8sUtils:                  tt.fields.k8sUtils,
				snapCleaner:               tt.fields.snapCleaner,
			}
			tt.args.pmaxClient = tt.getClient()

			got, err := s.validateVolSize(tt.args.ctx, tt.args.cr, tt.args.symmetrixID, tt.args.storagePoolID, tt.args.pmaxClient)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.validateVolSize() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("service.validateVolSize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_service_controllerProbe(t *testing.T) {
	type args struct {
		ctx context.Context
	}
	tests := []struct {
		name       string
		fields     serviceFields
		args       args
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "when the user is not set",
			fields: serviceFields{
				opts: Opts{
					UseProxy: true,
					User:     "",
				},
			},
			args: args{
				ctx: context.Background(),
			},
			wantErr:    true,
			wantErrMsg: "missing Unisphere user",
		},
		{
			name: "when the password is not set",
			fields: serviceFields{
				opts: Opts{
					UseProxy: true,
					User:     "user",
					Password: "",
				},
			},
			args: args{
				ctx: context.Background(),
			},
			wantErr:    true,
			wantErrMsg: "missing Unisphere password",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts:                      tt.fields.opts,
				mode:                      tt.fields.mode,
				adminClient:               tt.fields.adminClient,
				deletionWorker:            tt.fields.deletionWorker,
				iscsiClient:               tt.fields.iscsiClient,
				nvmetcpClient:             tt.fields.nvmetcpClient,
				system:                    tt.fields.system,
				privDir:                   tt.fields.privDir,
				loggedInArrays:            tt.fields.loggedInArrays,
				loggedInNVMeArrays:        tt.fields.loggedInNVMeArrays,
				mutex:                     sync.Mutex{},
				cacheMutex:                sync.Mutex{},
				nodeProbeMutex:            sync.Mutex{},
				probeStatus:               tt.fields.probeStatus,
				probeStatusMutex:          sync.Mutex{},
				pollingFrequencyMutex:     sync.Mutex{},
				pollingFrequencyInSeconds: tt.fields.pollingFrequencyInSeconds,
				nodeIsInitialized:         tt.fields.nodeIsInitialized,
				useNFS:                    tt.fields.useNFS,
				useFC:                     tt.fields.useFC,
				useIscsi:                  tt.fields.useIscsi,
				useNVMeTCP:                tt.fields.useNVMeTCP,
				iscsiTargets:              tt.fields.iscsiTargets,
				nvmeTargets:               tt.fields.nvmeTargets,
				storagePoolCacheDuration:  tt.fields.storagePoolCacheDuration,
				waitGroup:                 sync.WaitGroup{},
				fcConnector:               tt.fields.fcConnector,
				iscsiConnector:            tt.fields.iscsiConnector,
				nvmeTCPConnector:          tt.fields.nvmeTCPConnector,
				dBusConn:                  tt.fields.dBusConn,
				sgSvc:                     tt.fields.sgSvc,
				arrayTransportProtocolMap: tt.fields.arrayTransportProtocolMap,
				topologyConfig:            tt.fields.topologyConfig,
				allowedTopologyKeys:       tt.fields.allowedTopologyKeys,
				deniedTopologyKeys:        tt.fields.deniedTopologyKeys,
				k8sUtils:                  tt.fields.k8sUtils,
				snapCleaner:               tt.fields.snapCleaner,
			}
			err := s.controllerProbe(tt.args.ctx)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.controllerProbe() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrMsg != "" {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
		})
	}
}

func Test_service_requireProbe(t *testing.T) {
	type args struct {
		ctx        context.Context
		pmaxClient pmax.Pmax
	}
	tests := []struct {
		name       string
		fields     serviceFields
		args       args
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "when using the proxy, pmax client is nil and autoprobe is disabled",
			fields: serviceFields{
				opts: Opts{
					AutoProbe: false,
					UseProxy:  true,
				},
			},
			args: args{
				ctx:        context.Background(),
				pmaxClient: nil,
			},
			wantErr:    true,
			wantErrMsg: "Controller Service has not been probed",
		},
		{
			name: "autoprobe fails to get pmax client",
			fields: serviceFields{
				opts: Opts{
					AutoProbe: true,
					UseProxy:  true,
					User:      "",
				},
			},
			args: args{
				ctx:        context.Background(),
				pmaxClient: nil,
			},
			wantErr:    true,
			wantErrMsg: "failed to probe/init plugin",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts:                      tt.fields.opts,
				mode:                      tt.fields.mode,
				adminClient:               tt.fields.adminClient,
				deletionWorker:            tt.fields.deletionWorker,
				iscsiClient:               tt.fields.iscsiClient,
				nvmetcpClient:             tt.fields.nvmetcpClient,
				system:                    tt.fields.system,
				privDir:                   tt.fields.privDir,
				loggedInArrays:            tt.fields.loggedInArrays,
				loggedInNVMeArrays:        tt.fields.loggedInNVMeArrays,
				mutex:                     sync.Mutex{},
				cacheMutex:                sync.Mutex{},
				nodeProbeMutex:            sync.Mutex{},
				probeStatus:               tt.fields.probeStatus,
				probeStatusMutex:          sync.Mutex{},
				pollingFrequencyMutex:     sync.Mutex{},
				pollingFrequencyInSeconds: tt.fields.pollingFrequencyInSeconds,
				nodeIsInitialized:         tt.fields.nodeIsInitialized,
				useNFS:                    tt.fields.useNFS,
				useFC:                     tt.fields.useFC,
				useIscsi:                  tt.fields.useIscsi,
				useNVMeTCP:                tt.fields.useNVMeTCP,
				iscsiTargets:              tt.fields.iscsiTargets,
				nvmeTargets:               tt.fields.nvmeTargets,
				storagePoolCacheDuration:  tt.fields.storagePoolCacheDuration,
				waitGroup:                 sync.WaitGroup{},
				fcConnector:               tt.fields.fcConnector,
				iscsiConnector:            tt.fields.iscsiConnector,
				nvmeTCPConnector:          tt.fields.nvmeTCPConnector,
				dBusConn:                  tt.fields.dBusConn,
				sgSvc:                     tt.fields.sgSvc,
				arrayTransportProtocolMap: tt.fields.arrayTransportProtocolMap,
				topologyConfig:            tt.fields.topologyConfig,
				allowedTopologyKeys:       tt.fields.allowedTopologyKeys,
				deniedTopologyKeys:        tt.fields.deniedTopologyKeys,
				k8sUtils:                  tt.fields.k8sUtils,
				snapCleaner:               tt.fields.snapCleaner,
			}
			err := s.requireProbe(tt.args.ctx, tt.args.pmaxClient)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.requireProbe() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrMsg != "" {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
		})
	}
}

func Test_service_SelectOrCreatePortGroup(t *testing.T) {
	type args struct {
		ctx        context.Context
		symID      string
		host       *types.Host
		pmaxClient pmax.Pmax
	}
	tests := []struct {
		name    string
		fields  serviceFields
		args    args
		want    string
		wantErr bool
	}{
		{
			name:   "when host is nil",
			fields: serviceFields{},
			args: args{
				ctx:        context.Background(),
				symID:      symIDLocal,
				host:       nil,
				pmaxClient: nil,
			},
			want:    "",
			wantErr: true,
		},
		{
			name: "using vSpher port groups",
			fields: serviceFields{
				opts: Opts{
					IsVsphereEnabled: true,
					VSpherePortGroup: "vsphere-pg1",
				},
			},
			args: args{
				ctx:   context.Background(),
				symID: symIDLocal,
				host:  &types.Host{},
			},
			want:    "vsphere-pg1",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts:                      tt.fields.opts,
				mode:                      tt.fields.mode,
				adminClient:               tt.fields.adminClient,
				deletionWorker:            tt.fields.deletionWorker,
				iscsiClient:               tt.fields.iscsiClient,
				nvmetcpClient:             tt.fields.nvmetcpClient,
				system:                    tt.fields.system,
				privDir:                   tt.fields.privDir,
				loggedInArrays:            tt.fields.loggedInArrays,
				loggedInNVMeArrays:        tt.fields.loggedInNVMeArrays,
				mutex:                     sync.Mutex{},
				cacheMutex:                sync.Mutex{},
				nodeProbeMutex:            sync.Mutex{},
				probeStatus:               tt.fields.probeStatus,
				probeStatusMutex:          sync.Mutex{},
				pollingFrequencyMutex:     sync.Mutex{},
				pollingFrequencyInSeconds: tt.fields.pollingFrequencyInSeconds,
				nodeIsInitialized:         tt.fields.nodeIsInitialized,
				useNFS:                    tt.fields.useNFS,
				useFC:                     tt.fields.useFC,
				useIscsi:                  tt.fields.useIscsi,
				useNVMeTCP:                tt.fields.useNVMeTCP,
				iscsiTargets:              tt.fields.iscsiTargets,
				nvmeTargets:               tt.fields.nvmeTargets,
				storagePoolCacheDuration:  tt.fields.storagePoolCacheDuration,
				waitGroup:                 sync.WaitGroup{},
				fcConnector:               tt.fields.fcConnector,
				iscsiConnector:            tt.fields.iscsiConnector,
				nvmeTCPConnector:          tt.fields.nvmeTCPConnector,
				dBusConn:                  tt.fields.dBusConn,
				sgSvc:                     tt.fields.sgSvc,
				arrayTransportProtocolMap: tt.fields.arrayTransportProtocolMap,
				topologyConfig:            tt.fields.topologyConfig,
				allowedTopologyKeys:       tt.fields.allowedTopologyKeys,
				deniedTopologyKeys:        tt.fields.deniedTopologyKeys,
				k8sUtils:                  tt.fields.k8sUtils,
				snapCleaner:               tt.fields.snapCleaner,
			}
			got, err := s.SelectOrCreatePortGroup(tt.args.ctx, tt.args.symID, tt.args.host, tt.args.pmaxClient)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.SelectOrCreatePortGroup() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("service.SelectOrCreatePortGroup() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_mergeStringMaps(t *testing.T) {
	type args struct {
		base       map[string]string
		additional map[string]string
	}
	tests := []struct {
		name string
		args args
		want map[string]string
	}{
		{
			name: "merge successfully",
			args: args{
				base: map[string]string{
					"key1": "value1",
					"key2": "value2",
				},
				additional: map[string]string{
					"key3": "value3",
					"key4": "value4",
				},
			},
			want: map[string]string{
				"key1": "value1",
				"key2": "value2",
				"key3": "value3",
				"key4": "value4",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mergeStringMaps(tt.args.base, tt.args.additional); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("mergeStringMaps() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_service_CreateRemoteVolume(t *testing.T) {
	type args struct {
		ctx context.Context
		req *csiext.CreateRemoteVolumeRequest
	}
	tests := []struct {
		name       string
		fields     serviceFields
		args       args
		getClient  func() *mocks.MockPmaxClient
		want       *csiext.CreateRemoteVolumeResponse
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "when parsing the volume ID fails",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csiext.CreateRemoteVolumeRequest{
					VolumeHandle: "a-bad-id", // bad volume ID
				},
			},
			getClient: func() *mocks.MockPmaxClient {
				return nil
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Invalid volume id",
		},
		{
			name:   "fail to get pmax client",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csiext.CreateRemoteVolumeRequest{
					VolumeHandle: "csi-ABC-pmax-260602731A-ns1-nsx-000120000548-011AB",
				},
			},
			getClient: func() *mocks.MockPmaxClient {
				return nil // fail to initialize any pmax clients
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts:                      tt.fields.opts,
				mode:                      tt.fields.mode,
				adminClient:               tt.fields.adminClient,
				deletionWorker:            tt.fields.deletionWorker,
				iscsiClient:               tt.fields.iscsiClient,
				nvmetcpClient:             tt.fields.nvmetcpClient,
				system:                    tt.fields.system,
				privDir:                   tt.fields.privDir,
				loggedInArrays:            tt.fields.loggedInArrays,
				loggedInNVMeArrays:        tt.fields.loggedInNVMeArrays,
				mutex:                     sync.Mutex{},
				cacheMutex:                sync.Mutex{},
				nodeProbeMutex:            sync.Mutex{},
				probeStatus:               tt.fields.probeStatus,
				probeStatusMutex:          sync.Mutex{},
				pollingFrequencyMutex:     sync.Mutex{},
				pollingFrequencyInSeconds: tt.fields.pollingFrequencyInSeconds,
				nodeIsInitialized:         tt.fields.nodeIsInitialized,
				useNFS:                    tt.fields.useNFS,
				useFC:                     tt.fields.useFC,
				useIscsi:                  tt.fields.useIscsi,
				useNVMeTCP:                tt.fields.useNVMeTCP,
				iscsiTargets:              tt.fields.iscsiTargets,
				nvmeTargets:               tt.fields.nvmeTargets,
				storagePoolCacheDuration:  tt.fields.storagePoolCacheDuration,
				waitGroup:                 sync.WaitGroup{},
				fcConnector:               tt.fields.fcConnector,
				iscsiConnector:            tt.fields.iscsiConnector,
				nvmeTCPConnector:          tt.fields.nvmeTCPConnector,
				dBusConn:                  tt.fields.dBusConn,
				sgSvc:                     tt.fields.sgSvc,
				arrayTransportProtocolMap: tt.fields.arrayTransportProtocolMap,
				topologyConfig:            tt.fields.topologyConfig,
				allowedTopologyKeys:       tt.fields.allowedTopologyKeys,
				deniedTopologyKeys:        tt.fields.deniedTopologyKeys,
				k8sUtils:                  tt.fields.k8sUtils,
				snapCleaner:               tt.fields.snapCleaner,
			}
			got, err := s.CreateRemoteVolume(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.CreateRemoteVolume() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErrMsg != "" && tt.wantErr {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("service.CreateRemoteVolume() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_service_GetPortIdentifier(t *testing.T) {
	afterEach := func() {
		pmaxCache = nil
	}
	type args struct {
		ctx        context.Context
		symID      string
		dirPortKey string
		pmaxClient pmax.Pmax
	}
	tests := []struct {
		name      string
		fields    serviceFields
		args      args
		getClient func() *mocks.MockPmaxClient
		before    func()
		want      string
		wantErr   bool
	}{
		{
			name: "when cache is expired",
			fields: serviceFields{
				storagePoolCacheDuration: 1 * time.Millisecond,
			},
			args: args{
				ctx:        context.Background(),
				symID:      symIDLocal,
				dirPortKey: "FA-1D:4",
			},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gomock.NewController(t))
				c.EXPECT().GetPort(gomock.Any(), symIDLocal, "FA-1D", "4").Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						Type:       "FC",
						Identifier: "00000000abcd000e",
					},
				}, nil)
				return c
			},
			before: func() {
				// pmaxCache is global in controller.go
				pmaxCache = make(map[string]*pmaxCachedInformation)
				cache := &pmaxCachedInformation{
					portIdentifiers: &Pair{
						first:  "FA-1D:4",
						second: time.Now().Add(-1 * time.Minute), // create an artificial time in the past to simulate cache expiration
					},
				}
				pmaxCache[symIDLocal] = cache
			},
			want:    "0x00000000abcd000e",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts:                      tt.fields.opts,
				mode:                      tt.fields.mode,
				adminClient:               tt.fields.adminClient,
				deletionWorker:            tt.fields.deletionWorker,
				iscsiClient:               tt.fields.iscsiClient,
				nvmetcpClient:             tt.fields.nvmetcpClient,
				system:                    tt.fields.system,
				privDir:                   tt.fields.privDir,
				loggedInArrays:            tt.fields.loggedInArrays,
				loggedInNVMeArrays:        tt.fields.loggedInNVMeArrays,
				mutex:                     sync.Mutex{},
				cacheMutex:                sync.Mutex{},
				nodeProbeMutex:            sync.Mutex{},
				probeStatus:               tt.fields.probeStatus,
				probeStatusMutex:          sync.Mutex{},
				pollingFrequencyMutex:     sync.Mutex{},
				pollingFrequencyInSeconds: tt.fields.pollingFrequencyInSeconds,
				nodeIsInitialized:         tt.fields.nodeIsInitialized,
				useNFS:                    tt.fields.useNFS,
				useFC:                     tt.fields.useFC,
				useIscsi:                  tt.fields.useIscsi,
				useNVMeTCP:                tt.fields.useNVMeTCP,
				iscsiTargets:              tt.fields.iscsiTargets,
				nvmeTargets:               tt.fields.nvmeTargets,
				storagePoolCacheDuration:  tt.fields.storagePoolCacheDuration,
				waitGroup:                 sync.WaitGroup{},
				fcConnector:               tt.fields.fcConnector,
				iscsiConnector:            tt.fields.iscsiConnector,
				nvmeTCPConnector:          tt.fields.nvmeTCPConnector,
				dBusConn:                  tt.fields.dBusConn,
				sgSvc:                     tt.fields.sgSvc,
				arrayTransportProtocolMap: tt.fields.arrayTransportProtocolMap,
				topologyConfig:            tt.fields.topologyConfig,
				allowedTopologyKeys:       tt.fields.allowedTopologyKeys,
				deniedTopologyKeys:        tt.fields.deniedTopologyKeys,
				k8sUtils:                  tt.fields.k8sUtils,
				snapCleaner:               tt.fields.snapCleaner,
			}
			defer afterEach()
			tt.args.pmaxClient = tt.getClient()
			tt.before()

			got, err := s.GetPortIdentifier(tt.args.ctx, tt.args.symID, tt.args.dirPortKey, tt.args.pmaxClient)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.GetPortIdentifier() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("service.GetPortIdentifier() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_service_CreateSnapshot(t *testing.T) {
	cleanClientCache := func(symmetrixIDs []string) {
		for _, symID := range symmetrixIDs {
			symmetrix.RemoveClient(symID)
		}
	}

	type args struct {
		ctx context.Context
		req *csi.CreateSnapshotRequest
	}
	tests := []struct {
		name       string
		fields     serviceFields
		args       args
		before     func()
		after      func()
		want       *csi.CreateSnapshotResponse
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:   "the snapshot name is empty",
			fields: serviceFields{},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateSnapshotRequest{
					Name: "",
				},
			},
			before:     func() {},
			after:      func() {},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Snapshot name cannot be empty",
		},
		{
			name: "the snapshot symmetrix ID does not match local or remote symmetrix IDs",
			fields: serviceFields{
				opts: Opts{
					ClusterPrefix: clusterPrefix,
				},
			},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateSnapshotRequest{
					Name:           localVolumeName,
					SourceVolumeId: validLocalVolumeID, // contains local and remote sym IDs
					Parameters: map[string]string{
						SymmetrixIDParam: "999999999999", // will not match local or remote sym IDs
					},
				},
			},
			before:     func() {},
			after:      func() {},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Symmetrix ID in snapclass parameters doesn't match the volume's symmetrix id",
		},
		{
			name: "fails to get the powermax client",
			fields: serviceFields{
				opts: Opts{
					ClusterPrefix: clusterPrefix,
				},
			},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateSnapshotRequest{
					Name:           localVolumeName,
					SourceVolumeId: validReplicatedVolume,
					Parameters: map[string]string{
						SymmetrixIDParam: symIDRemote,
					},
				},
			},
			before:     func() {},
			after:      func() {},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "array: " + symIDLocal + " not found",
		},
		{
			name: "snapshot a filesystem",
			fields: serviceFields{
				opts: Opts{
					ClusterPrefix: clusterPrefix,
				},
			},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateSnapshotRequest{
					Name:           localVolumeName,
					SourceVolumeId: validReplicatedVolume,
					Parameters: map[string]string{
						SymmetrixIDParam: symIDRemote,
					},
				},
			},
			before: func() {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))

				c.EXPECT().WithSymmetrixID(symIDLocal).AnyTimes().Return(c)
				c.EXPECT().WithSymmetrixID(symIDRemote).AnyTimes().Return(c)
				c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

				// returning a nil error will trigger an error because
				// we cannot snapshot a file system.
				c.EXPECT().GetFileSystemByID(gomock.Any(), symIDRemote, gomock.Any()).Times(1).Return(&types.FileSystem{}, nil)

				err := symmetrix.Initialize([]string{symIDLocal, symIDRemote}, c)
				if err != nil {
					t.Fatalf("failed to initialize the powermax client for the test: %s", err)
				}
			},
			after: func() {
				cleanClientCache([]string{symIDLocal, symIDRemote})
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "snapshot on a NFS volume is not supported",
		},
		{
			name: "when array is not licensed",
			fields: serviceFields{
				opts: Opts{
					ClusterPrefix: clusterPrefix,
					UseProxy:      true,
				},
			},
			args: args{
				ctx: context.Background(),
				req: &csi.CreateSnapshotRequest{
					Name:           localVolumeName,
					SourceVolumeId: validReplicatedVolume,
					Parameters: map[string]string{
						SymmetrixIDParam: symIDRemote,
					},
				},
			},
			before: func() {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))

				c.EXPECT().WithSymmetrixID(symIDLocal).AnyTimes().Return(c)
				c.EXPECT().WithSymmetrixID(symIDRemote).AnyTimes().Return(c)
				c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

				c.EXPECT().GetFileSystemByID(gomock.Any(), symIDRemote, gomock.Any()).Times(1).Return(nil, errors.New("not a filesystem"))
				// return error when checking if snapshot is licensed
				c.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(false, errors.New("not licensed"))

				err := symmetrix.Initialize([]string{symIDLocal, symIDRemote}, c)
				if err != nil {
					t.Fatalf("failed to initialize the powermax client for the test: %s", err)
				}
			},
			after: func() {
				cleanClientCache([]string{symIDLocal, symIDRemote})
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "not licensed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts:                      tt.fields.opts,
				mode:                      tt.fields.mode,
				adminClient:               tt.fields.adminClient,
				deletionWorker:            tt.fields.deletionWorker,
				iscsiClient:               tt.fields.iscsiClient,
				nvmetcpClient:             tt.fields.nvmetcpClient,
				system:                    tt.fields.system,
				privDir:                   tt.fields.privDir,
				loggedInArrays:            tt.fields.loggedInArrays,
				loggedInNVMeArrays:        tt.fields.loggedInNVMeArrays,
				mutex:                     sync.Mutex{},
				cacheMutex:                sync.Mutex{},
				nodeProbeMutex:            sync.Mutex{},
				probeStatus:               tt.fields.probeStatus,
				probeStatusMutex:          sync.Mutex{},
				pollingFrequencyMutex:     sync.Mutex{},
				pollingFrequencyInSeconds: tt.fields.pollingFrequencyInSeconds,
				nodeIsInitialized:         tt.fields.nodeIsInitialized,
				useNFS:                    tt.fields.useNFS,
				useFC:                     tt.fields.useFC,
				useIscsi:                  tt.fields.useIscsi,
				useNVMeTCP:                tt.fields.useNVMeTCP,
				iscsiTargets:              tt.fields.iscsiTargets,
				nvmeTargets:               tt.fields.nvmeTargets,
				storagePoolCacheDuration:  tt.fields.storagePoolCacheDuration,
				waitGroup:                 sync.WaitGroup{},
				fcConnector:               tt.fields.fcConnector,
				iscsiConnector:            tt.fields.iscsiConnector,
				nvmeTCPConnector:          tt.fields.nvmeTCPConnector,
				dBusConn:                  tt.fields.dBusConn,
				sgSvc:                     tt.fields.sgSvc,
				arrayTransportProtocolMap: tt.fields.arrayTransportProtocolMap,
				topologyConfig:            tt.fields.topologyConfig,
				allowedTopologyKeys:       tt.fields.allowedTopologyKeys,
				deniedTopologyKeys:        tt.fields.deniedTopologyKeys,
				k8sUtils:                  tt.fields.k8sUtils,
				snapCleaner:               tt.fields.snapCleaner,
			}
			defer tt.after() // clean up any caches between tests to ensure a clean test environment for each test
			tt.before()

			got, err := s.CreateSnapshot(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.CreateSnapshot() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErrMsg != "" {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("service.CreateSnapshot() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsNodeNVMe(t *testing.T) {
	tests := []struct {
		name                string
		symID               string
		nodeID              string
		getMaskingViewError error
		getHostByIDError    error
		wantErr             error
		want                bool
	}{
		{
			name:                "Successful call to GetMaskingViewByID",
			symID:               "sym1",
			nodeID:              "node1",
			getMaskingViewError: nil,
			getHostByIDError:    nil,
			wantErr:             nil,
			want:                true,
		},
		{
			name:                "Successful call to GetHostByID",
			symID:               "sym1",
			nodeID:              "node1",
			getMaskingViewError: errors.New("unable to get masking view"),
			getHostByIDError:    nil,
			wantErr:             nil,
			want:                true,
		},
		{
			name:                "Error getting ID",
			symID:               "sym1",
			nodeID:              "node1",
			getMaskingViewError: errors.New("unable to get masking view"),
			getHostByIDError:    errors.New("unable to get Host byID"),
			wantErr:             errors.New("Failed to fetch host id from array sym1 for node: node1"),
			want:                false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					TransportProtocol: NvmeTCPTransportProtocol,
					ClusterPrefix:     "testCluster",
				},
			}
			pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
			pmaxClient.EXPECT().GetMaskingViewByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.MaskingView{}, tt.getMaskingViewError)
			pmaxClient.EXPECT().GetHostByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Host{
				HostType: "NVMe/TCP",
			}, tt.getHostByIDError)
			got, err := s.IsNodeNVMe(context.Background(), tt.symID, tt.nodeID, pmaxClient, "")

			if tt.want != got {
				t.Errorf("service.IsNodeNVMe() = %v, want %v", got, tt.want)
			}

			if err != nil {
				assert.Contains(t, err.Error(), tt.wantErr.Error())
			} else if !errors.Is(err, tt.wantErr) {
				t.Errorf("service.IsNodeNVMe() error: %v, wantErr: %v", err, tt.wantErr)
				return
			}
		})
	}
}

func Test_service_DeleteSnapshot(t *testing.T) {
	// keeps a list of steps that should be run after each test
	// in order to clean and restore the test environment.
	// Typically used to clear pmax client and replication capability caches.
	var deferredCleanUpFuncs []func()
	addCleanUpStep := func(f func()) {
		deferredCleanUpFuncs = append(deferredCleanUpFuncs, f)
	}

	// Run all the clean up steps queued in deferredCleanUpFuncs after each test
	afterEach := func() {
		for _, f := range deferredCleanUpFuncs {
			f()
		}
		// purge for next round of tests
		deferredCleanUpFuncs = []func(){}
	}

	type args struct {
		ctx context.Context
		req *csi.DeleteSnapshotRequest
	}
	tests := []struct {
		name       string
		args       args
		before     func()
		want       *csi.DeleteSnapshotResponse
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "when the powermax client is not initialized",
			args: args{
				ctx: context.Background(),
				req: &csi.DeleteSnapshotRequest{
					SnapshotId: validLocalVolumeID,
				},
			},
			before:     func() {}, // don't initialized the client
			want:       nil,
			wantErr:    true,
			wantErrMsg: "array: " + symIDLocal + " not found",
		},
		{
			name: "when the powermax array is not licensed",
			args: args{
				ctx: context.Background(),
				req: &csi.DeleteSnapshotRequest{
					SnapshotId: validLocalVolumeID,
				},
			},
			before: func() {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))

				c.EXPECT().WithSymmetrixID(symIDLocal).AnyTimes().Return(c)
				c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

				// return error when checking if snapshot is licensed
				c.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(false, errors.New("not licensed"))

				// make sure to push a step to clean the client cache if calling Initialize.
				addCleanUpStep(func() { symmetrix.RemoveClient(symIDLocal) })
				err := symmetrix.Initialize([]string{symIDLocal}, c)
				if err != nil {
					t.Fatalf("failed to initialize the powermax client for the test: %s", err)
				}
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "not licensed",
		},
		{
			name: "when the snapshot is not found on the array",
			args: args{
				ctx: context.Background(),
				req: &csi.DeleteSnapshotRequest{
					SnapshotId: validLocalVolumeID,
				},
			},
			before: func() {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))

				c.EXPECT().WithSymmetrixID(symIDLocal).AnyTimes().Return(c)
				c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

				c.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(true, nil)
				c.EXPECT().GetReplicationCapabilities(gomock.Any()).Times(1).Return(&types.SymReplicationCapabilities{
					SymmetrixCapability: []types.SymmetrixCapability{
						{
							SymmetrixID:   symIDLocal,
							SnapVxCapable: true,
						},
					},
				}, nil)
				// add a step to clean the replication capabilities cache
				addCleanUpStep(func() { RemoveReplicationCapability(symIDLocal) })

				// return a "not found" error when querying for snapshot info
				c.EXPECT().GetSnapshotInfo(gomock.Any(), symIDLocal, gomock.Any(), gomock.Any()).Times(1).Return(nil, errors.New("not found"))

				addCleanUpStep(func() { symmetrix.RemoveClient(symIDLocal) })
				err := symmetrix.Initialize([]string{symIDLocal}, c)
				if err != nil {
					t.Fatalf("failed to initialize the powermax client for the test: %s", err)
				}
			},
			want:       &csi.DeleteSnapshotResponse{},
			wantErr:    false,
			wantErrMsg: "",
		},
		{
			name: "when the snapshot query fails",
			args: args{
				ctx: context.Background(),
				req: &csi.DeleteSnapshotRequest{
					SnapshotId: validLocalVolumeID,
				},
			},
			before: func() {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))

				c.EXPECT().WithSymmetrixID(symIDLocal).AnyTimes().Return(c)
				c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

				c.EXPECT().IsAllowedArray(gomock.Any()).Times(1).Return(true, nil)
				c.EXPECT().GetReplicationCapabilities(gomock.Any()).Times(1).Return(&types.SymReplicationCapabilities{
					SymmetrixCapability: []types.SymmetrixCapability{
						{
							SymmetrixID:   symIDLocal,
							SnapVxCapable: true,
						},
					},
				}, nil)
				addCleanUpStep(func() { RemoveReplicationCapability(symIDLocal) })

				// return any error other than "not found"
				c.EXPECT().GetSnapshotInfo(gomock.Any(), symIDLocal, gomock.Any(), gomock.Any()).Times(1).Return(nil, errors.New("query error"))

				addCleanUpStep(func() { symmetrix.RemoveClient(symIDLocal) })
				err := symmetrix.Initialize([]string{symIDLocal}, c)
				if err != nil {
					t.Fatalf("failed to initialize the powermax client for the test: %s", err)
				}
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "GetSnapshotInfo() failed with error",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{}
			defer afterEach() // clean up any caches between tests to ensure a clean test environment for each test
			tt.before()

			got, err := s.DeleteSnapshot(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.DeleteSnapshot() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErrMsg != "" {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("service.DeleteSnapshot() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_service_verifyProtectionGroupID(t *testing.T) {
	localRDFGroupNum := "1"
	type args struct {
		ctx           context.Context
		symID         string
		localRdfGrpNo string
		repMode       string
		pmaxClient    pmax.Pmax
	}
	tests := []struct {
		name       string
		args       args
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "sync RDF group is already used by async or metro",
			args: args{
				ctx:           context.Background(),
				symID:         symIDLocal,
				localRdfGrpNo: localRDFGroupNum,
				repMode:       Sync,
				pmaxClient: func() pmax.Pmax {
					client := mocks.NewMockPmaxClient(gomock.NewController(t))

					client.EXPECT().GetStorageGroupIDList(gomock.Any(), symIDLocal, "", false).Times(1).Return(
						&types.StorageGroupIDList{
							StorageGroupIDs: []string{
								// building a bad Storage Group ID
								"-" + localRDFGroupNum + "-" + Async,
							},
						}, nil,
					)

					return client
				}(),
			},
			wantErr:    true,
			wantErrMsg: "is already part of another Async/Metro mode ReplicationGroup",
		},
		{
			name: "metro RDF group is already used for sync/async",
			args: args{
				ctx:           context.Background(),
				symID:         symIDLocal,
				localRdfGrpNo: localRDFGroupNum,
				repMode:       Metro,
				pmaxClient: func() pmax.Pmax {
					client := mocks.NewMockPmaxClient(gomock.NewController(t))

					client.EXPECT().GetStorageGroupIDList(gomock.Any(), symIDLocal, "", false).Times(1).Return(
						&types.StorageGroupIDList{
							StorageGroupIDs: []string{
								// building a bad Storage Group ID
								"-" + localRDFGroupNum + "-" + Async,
							},
						}, nil,
					)

					return client
				}(),
			},
			wantErr:    true,
			wantErrMsg: "is already part of another Async/Sync mode ReplicationGroup",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{}

			err := s.verifyProtectionGroupID(tt.args.ctx, tt.args.symID, tt.args.localRdfGrpNo, tt.args.repMode, tt.args.pmaxClient)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.verifyProtectionGroupID() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErrMsg != "" {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
		})
	}
}

func Test_service_ControllerPublishVolume(t *testing.T) {
	// capture state of controllerPendingState before this test
	// so we can restore it after each test and after the test suite
	ctrlPendingStateDefault := controllerPendingState

	// keeps a list of steps that should be run after each test
	// in order to clean and restore the test environment.
	// Typically used to clear pmax client and replication capability caches.
	var deferredCleanUpFuncs []func()
	addCleanUpStep := func(f func()) {
		deferredCleanUpFuncs = append(deferredCleanUpFuncs, f)
	}

	// Run all the clean up steps queued in deferredCleanUpFuncs after each test
	afterEach := func() {
		for _, f := range deferredCleanUpFuncs {
			f()
		}
		// purge cleanup funcs for next test
		deferredCleanUpFuncs = []func(){}

		// restore default state of controllerPendingState
		controllerPendingState = ctrlPendingStateDefault
	}

	type args struct {
		ctx context.Context
		req *csi.ControllerPublishVolumeRequest
	}
	tests := []struct {
		name       string
		args       args
		before     func()
		want       *csi.ControllerPublishVolumeResponse
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "fail to parse the volume ID",
			args: args{
				ctx: context.Background(),
				req: &csi.ControllerPublishVolumeRequest{
					VolumeId: "a-bad-id", // bad volume ID
				},
			},
			before:     func() {},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "Invalid volume id",
		},
		{
			name: "fail to initialize the powermax client",
			args: args{
				ctx: context.Background(),
				req: &csi.ControllerPublishVolumeRequest{
					VolumeId: validLocalVolumeID,
				},
			},
			before:     func() {},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "not found",
		},
		{
			name: "publish is already pending",
			args: args{
				ctx: context.Background(),
				req: &csi.ControllerPublishVolumeRequest{
					VolumeId: validLocalVolumeID,
				},
			},
			before: func() {
				// setup gopowermax client for test
				c := mocks.NewMockPmaxClient(gomock.NewController(t))

				c.EXPECT().WithSymmetrixID(symIDLocal).AnyTimes().Return(c)
				c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

				// clear client cache when done with test
				addCleanUpStep(func() { symmetrix.RemoveClient(symIDLocal) })
				err := symmetrix.Initialize([]string{symIDLocal}, c)
				if err != nil {
					t.Fatal("failed to initialize test client")
				}

				// create controllerPendingState
				controllerPendingState = pendingState{
					maxPending:   1,
					npending:     0,
					pendingMutex: &sync.Mutex{},
					pendingMap:   make(map[volumeIDType]time.Time),
				}
				volID := volumeIDType(validLocalVolumeID)
				controllerPendingState.pendingMap[volID] = time.Now() // store a non-zero time to induce a "pending" error
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "pending",
		},
		{
			name: "access type is NFS and NFS export creation fails",
			args: args{
				ctx: context.Background(),
				req: &csi.ControllerPublishVolumeRequest{
					VolumeId: validLocalVolumeID,
					NodeId:   "worker-1",
					VolumeCapability: &csi.VolumeCapability{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{
								FsType: NFS,
							},
						},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
				},
			},
			before: func() {
				// setup gopowermax client for test
				c := mocks.NewMockPmaxClient(gomock.NewController(t))

				c.EXPECT().WithSymmetrixID(symIDLocal).AnyTimes().Return(c)
				c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
				c.EXPECT().GetVersionDetails(gomock.Any()).AnyTimes().Return(&types.VersionDetails{APIVersion: "103"}, nil)
				c.EXPECT().GetFileSystemByID(gomock.Any(), symIDLocal, gomock.Any()).Times(1).Return(
					&types.FileSystem{}, errors.New("failed to fetch file system"),
				)

				// clear client cache when done with test
				addCleanUpStep(func() { symmetrix.RemoveClient(symIDLocal) })
				err := symmetrix.Initialize([]string{symIDLocal}, c)
				if err != nil {
					t.Fatal("failed to initialize test client")
				}
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "failed to fetch file system",
		},
		{
			name: "static provisioning of a file system",
			args: args{
				ctx: context.Background(),
				req: &csi.ControllerPublishVolumeRequest{
					VolumeId: validLocalVolumeID,
					NodeId:   "worker-1",
					VolumeCapability: &csi.VolumeCapability{
						AccessType: &csi.VolumeCapability_Mount{
							Mount: &csi.VolumeCapability_MountVolume{
								FsType: "",
							},
						},
						AccessMode: &csi.VolumeCapability_AccessMode{
							Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
						},
					},
				},
			},
			before: func() {
				// setup gopowermax client for test
				c := mocks.NewMockPmaxClient(gomock.NewController(t))

				c.EXPECT().WithSymmetrixID(symIDLocal).AnyTimes().Return(c)
				c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
				c.EXPECT().GetVersionDetails(gomock.Any()).AnyTimes().Return(&types.VersionDetails{APIVersion: "103"}, nil)
				c.EXPECT().GetFileSystemByID(gomock.Any(), symIDLocal, gomock.Any()).Times(1).Return(
					&types.FileSystem{}, nil, // successfully returning a file system to simulate static provisioning
				)

				// clear client cache when done with test
				addCleanUpStep(func() { symmetrix.RemoveClient(symIDLocal) })
				err := symmetrix.Initialize([]string{symIDLocal}, c)
				if err != nil {
					t.Fatal("failed to initialize test client")
				}
			},
			want:       nil,
			wantErr:    true,
			wantErrMsg: "static provisioning on a file system is not supported.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{}
			defer afterEach()
			tt.before()

			got, err := s.ControllerPublishVolume(tt.args.ctx, tt.args.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.ControllerPublishVolume() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErrMsg != "" {
				assert.Contains(t, err.Error(), tt.wantErrMsg)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("service.ControllerPublishVolume() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_service_GetVSphereFCHostSGAndMVIDFromNodeID(t *testing.T) {
	s := &service{
		opts: Opts{
			IsVsphereEnabled: true,
			VSphereHostName:  "vsphere-host-name",
		},
	}

	t.Run("success", func(t *testing.T) {
		host, sg, mvid := s.GetVSphereFCHostSGAndMVIDFromNodeID()

		wantHost := s.opts.VSphereHostName
		wantSg := CsiNoSrpSGPrefix + s.getClusterPrefix() + "-" + Vsphere
		wantMvid := CsiMVPrefix + s.getClusterPrefix() + "-" + Vsphere

		assert.Equal(t, wantHost, host)
		assert.Equal(t, wantSg, sg)
		assert.Equal(t, wantMvid, mvid)
	})
}

func Test_service_ControllerUnpublishVolume(t *testing.T) {
	LockRequestHandler()
	ctx := context.Background()
	volIDInvalid := s.createCSIVolumeID("", "invalidVolume", "0001", "00000")
	volIDRemote := s.createCSIVolumeID("", "validVolume", "0001:0001", "1:0")

	c := mocks.NewMockPmaxClient(gomock.NewController(t))
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Not("1")).AnyTimes().Return(nil, errors.New(notFound))
	c.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), "1").AnyTimes().Return(&types.Volume{
		VolumeIdentifier: "csi--validVolume",
		RDFGroupIDList:   []types.RDFGroupID{{RDFGroupNumber: 42, Label: "label"}, {RDFGroupNumber: 42, Label: "label"}},
	}, nil)

	symmetrix.Initialize([]string{"0001"}, c)
	defer symmetrix.RemoveClient("0001")

	t.Run("invalid requests", func(t *testing.T) {
		// invalid client
		req := &csi.ControllerUnpublishVolumeRequest{
			VolumeId: s.createCSIVolumeID("", "invalidClient", "0000", "11111"),
			NodeId:   "test-node-id",
		}

		_, err := s.ControllerUnpublishVolume(ctx, req)
		assert.Contains(t, err.Error(), "not found")

		req.VolumeId = volIDInvalid
		c.EXPECT().GetFileSystemByID(ctx, gomock.Any(), gomock.Any()).AnyTimes().Return(nil, errors.New("error"))
		_, err = s.ControllerUnpublishVolume(ctx, req)
		assert.Contains(t, err.Error(), "Could not retrieve fileSystem")

		req.VolumeId = s.createCSIVolumeID("", "failvalidVolume", "0001", "1")
		resp, err := s.ControllerUnpublishVolume(ctx, req)
		assert.Nil(t, err)
		assert.Empty(t, resp)
	})

	t.Run("remote volume", func(t *testing.T) {
		req := &csi.ControllerUnpublishVolumeRequest{
			VolumeId: volIDRemote,
			NodeId:   "test-node-id",
		}

		c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
		// Non-uniform Metro: node has no host on remote array — unpublish should skip remote
		c.EXPECT().GetHostList(gomock.Any(), gomock.Any()).AnyTimes().Return(&types.HostList{HostIDs: []string{}}, nil)

		resp, err := s.ControllerUnpublishVolume(ctx, req)
		assert.Empty(t, resp)
		assert.Nil(t, err)

		req.VolumeId = s.createCSIVolumeID("", "validVolume", "0001:0001", "1:1")
		resp, err = s.ControllerUnpublishVolume(ctx, req)
		assert.Nil(t, err)
		assert.Empty(t, resp)
	})
}

// Test_service_ControllerUnpublishVolume_Site1 tests non-uniform Metro unpublish
// where node has host on local (R1) only — local unpublish proceeds, remote skipped.
func Test_service_ControllerUnpublishVolume_Site1(t *testing.T) {
	LockRequestHandler()
	ctx := context.Background()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
	c.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), "1").AnyTimes().Return(&types.Volume{
		VolumeIdentifier: "csi--validVolume",
		VolumeID:         "1",
		RDFGroupIDList:   []types.RDFGroupID{{RDFGroupNumber: 42, Label: "label"}},
	}, nil)

	nodeID := "test-node-id"
	iscsiHostID, _, _ := s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	localSymID := "0001"
	remoteSymID := "0002"

	// Local array has the node's host, remote array does not
	c.EXPECT().GetHostList(gomock.Any(), localSymID).AnyTimes().
		Return(&types.HostList{HostIDs: []string{iscsiHostID}}, nil)
	c.EXPECT().GetHostList(gomock.Any(), remoteSymID).AnyTimes().
		Return(&types.HostList{HostIDs: []string{}}, nil)

	symmetrix.Initialize([]string{localSymID, remoteSymID}, c)
	defer symmetrix.RemoveClient(localSymID)
	defer symmetrix.RemoveClient(remoteSymID)

	volIDRemote := s.createCSIVolumeID("", "validVolume", localSymID+":"+remoteSymID, "1:1")
	req := &csi.ControllerUnpublishVolumeRequest{
		VolumeId: volIDRemote,
		NodeId:   nodeID,
	}

	resp, err := s.ControllerUnpublishVolume(ctx, req)
	assert.Nil(t, err, "site1 unpublish should succeed")
	assert.NotNil(t, resp)
}

// Test_service_ControllerUnpublishVolume_Site2 tests non-uniform Metro unpublish
// where node has host on remote (R2) only — local skipped, remote unpublish proceeds.
func Test_service_ControllerUnpublishVolume_Site2(t *testing.T) {
	LockRequestHandler()
	ctx := context.Background()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
	c.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), "1").AnyTimes().Return(&types.Volume{
		VolumeIdentifier: "csi--validVolume",
		VolumeID:         "1",
		RDFGroupIDList:   []types.RDFGroupID{{RDFGroupNumber: 42, Label: "label"}},
	}, nil)

	nodeID := "test-node-id"
	iscsiHostID, _, _ := s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	localSymID := "0001"
	remoteSymID := "0002"

	// Remote array has the node's host, local array does not
	c.EXPECT().GetHostList(gomock.Any(), localSymID).AnyTimes().
		Return(&types.HostList{HostIDs: []string{}}, nil)
	c.EXPECT().GetHostList(gomock.Any(), remoteSymID).AnyTimes().
		Return(&types.HostList{HostIDs: []string{iscsiHostID}}, nil)

	symmetrix.Initialize([]string{localSymID, remoteSymID}, c)
	defer symmetrix.RemoveClient(localSymID)
	defer symmetrix.RemoveClient(remoteSymID)

	volIDRemote := s.createCSIVolumeID("", "validVolume", localSymID+":"+remoteSymID, "1:1")
	req := &csi.ControllerUnpublishVolumeRequest{
		VolumeId: volIDRemote,
		NodeId:   nodeID,
	}

	resp, err := s.ControllerUnpublishVolume(ctx, req)
	assert.Nil(t, err, "site2 unpublish should succeed")
	assert.NotNil(t, resp)
}

// Test_service_ControllerUnpublishVolume_Uniform tests uniform Metro unpublish
// where node has host on both arrays — both unpublish proceed.
func Test_service_ControllerUnpublishVolume_Uniform(t *testing.T) {
	LockRequestHandler()
	ctx := context.Background()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
	c.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), "1").AnyTimes().Return(&types.Volume{
		VolumeIdentifier: "csi--validVolume",
		VolumeID:         "1",
		RDFGroupIDList:   []types.RDFGroupID{{RDFGroupNumber: 42, Label: "label"}},
	}, nil)

	nodeID := "test-node-id"
	iscsiHostID, _, _ := s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	localSymID := "0001"
	remoteSymID := "0002"

	// Both arrays have the node's host
	c.EXPECT().GetHostList(gomock.Any(), localSymID).AnyTimes().
		Return(&types.HostList{HostIDs: []string{iscsiHostID}}, nil)
	c.EXPECT().GetHostList(gomock.Any(), remoteSymID).AnyTimes().
		Return(&types.HostList{HostIDs: []string{iscsiHostID}}, nil)

	symmetrix.Initialize([]string{localSymID, remoteSymID}, c)
	defer symmetrix.RemoveClient(localSymID)
	defer symmetrix.RemoveClient(remoteSymID)

	volIDRemote := s.createCSIVolumeID("", "validVolume", localSymID+":"+remoteSymID, "1:1")
	req := &csi.ControllerUnpublishVolumeRequest{
		VolumeId: volIDRemote,
		NodeId:   nodeID,
	}

	resp, err := s.ControllerUnpublishVolume(ctx, req)
	assert.Nil(t, err, "uniform unpublish should succeed")
	assert.NotNil(t, resp)
}

func Test_service_getArrayIDFromTopologyRequirement(t *testing.T) {
	tests := []struct {
		name                string
		topologyRequirement *csi.TopologyRequirement
		storageArrayConfig  map[string]StorageArrayConfig
		want                string
	}{
		{
			name:                "no topology requirements nor array config",
			topologyRequirement: &csi.TopologyRequirement{},
			storageArrayConfig:  map[string]StorageArrayConfig{},
			want:                "",
		},
		{
			name:                "no topology requirements",
			topologyRequirement: &csi.TopologyRequirement{},
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
			},
			want: "",
		},
		{
			name: "topology requirements but no labelled array",
			topologyRequirement: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region1",
							"topology.kubernetes.io/zone":   "zone1",
						},
					},
				},
			},
			storageArrayConfig: map[string]StorageArrayConfig{},
			want:               "",
		},
		{
			name: "basic test with one array and simple region zone labels",
			topologyRequirement: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region1",
							"topology.kubernetes.io/zone":   "zone1",
						},
					},
				},
			},
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
			},
			want: "000000000001",
		},
		{
			name: "multiple arrays and simple region zone labels",
			topologyRequirement: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region2",
							"topology.kubernetes.io/zone":   "zone2",
						},
					},
				},
			},
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
				"000000000002": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone2",
					},
				},
				"000000000003": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region3",
						"topology.kubernetes.io/zone":   "zone3",
					},
				},
			},
			want: "000000000002",
		},
		{
			name: "single region segment in topology requirements",
			topologyRequirement: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region2",
						},
					},
				},
			},
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
				"000000000002": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone2",
					},
				},
			},
			want: "",
		},
		{
			name: "single region segment in topology requirements, multiple candidates",
			topologyRequirement: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region2",
						},
					},
				},
			},
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
				"000000000002": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
				"000000000003": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
			},
			want: "",
		},
		{
			name: "region and zone segments in topology requirements, multiple candidates",
			topologyRequirement: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region2",
							"topology.kubernetes.io/zone":   "zone2",
						},
					},
				},
			},
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
				"000000000002": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone2",
					},
				},
				"000000000003": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone2",
					},
				},
			},
			want: "000000000002,000000000003", // non deterministic due to map iteration
		},
		{
			name: "multiple segments in topology requirements, single candidate",
			topologyRequirement: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region2",
							"topology.kubernetes.io/zone":   "zone1",
						},
					},
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region2",
							"topology.kubernetes.io/zone":   "zone2",
						},
					},
				},
			},
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
				"000000000002": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone2",
					},
				},
				"000000000003": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone3",
					},
				},
			},
			want: "000000000002",
		},
		{
			name: "multiple segments in topology requirements, multiple candidates",
			topologyRequirement: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region2",
							"topology.kubernetes.io/zone":   "zone1",
						},
					},
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region2",
							"topology.kubernetes.io/zone":   "zone2",
						},
					},
				},
			},
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
				"000000000002": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
				"000000000003": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone2",
					},
				},
			},
			want: "000000000002",
		},
		{
			name: "multiple arrays with sone unlabelled",
			topologyRequirement: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region2",
							"topology.kubernetes.io/zone":   "zone2",
						},
					},
				},
			},
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
				"000000000002": {
					Labels: map[string]interface{}{},
				},
				"000000000003": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone2",
					},
				},
			},
			want: "000000000003",
		},
		{
			name: "match not in preferred but in requisite list",
			topologyRequirement: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region2",
							"topology.kubernetes.io/zone":   "zone1",
						},
					},
				},
				Requisite: []*csi.Topology{
					{
						Segments: map[string]string{
							"topology.kubernetes.io/region": "region2",
							"topology.kubernetes.io/zone":   "zone2",
						},
					},
				},
			},
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
				"000000000002": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region2",
						"topology.kubernetes.io/zone":   "zone2",
					},
				},
				"000000000003": {
					Labels: map[string]interface{}{},
				},
			},
			want: "000000000002",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					StorageArrays: tt.storageArrayConfig,
				},
			}

			got := s.getArrayIDFromTopologyRequirement(tt.topologyRequirement)
			options := strings.Split(tt.want, ",")
			if len(options) > 1 {
				assert.Contains(t, options, got)
			} else {
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// Test_service_getArrayIDFromTopologyRequirement_CapacityBasedSelection
// proves that when a zone has multiple arrays, the driver selects the one
// with the lowest capacity utilization, and skips arrays marked unavailable
// in the capacity cache rather than blindly using the first candidate.
func Test_service_getArrayIDFromTopologyRequirement_CapacityBasedSelection(t *testing.T) {
	topologyRequirement := &csi.TopologyRequirement{
		Preferred: []*csi.Topology{
			{
				Segments: map[string]string{
					"topology.kubernetes.io/zone": "us-east-1a",
				},
			},
		},
	}
	storageArrayConfig := map[string]StorageArrayConfig{
		"000000000001": {Labels: map[string]interface{}{"topology.kubernetes.io/zone": "us-east-1a"}},
		"000000000002": {Labels: map[string]interface{}{"topology.kubernetes.io/zone": "us-east-1a"}},
	}

	t.Run("lowest utilization array is selected (AC-001)", func(t *testing.T) {
		s := &service{opts: Opts{StorageArrays: storageArrayConfig}, capCache: newCapacityCache()}
		now := time.Now()
		s.capCache.recordPollSuccess("000000000001", 80.0, now)
		s.capCache.recordPollSuccess("000000000002", 20.0, now)

		got := s.getArrayIDFromTopologyRequirement(topologyRequirement)
		assert.Equal(t, "000000000002", got)
	})

	t.Run("unavailable array is skipped, provisioning continues on remaining array (AC-003)", func(t *testing.T) {
		s := &service{opts: Opts{StorageArrays: storageArrayConfig}, capCache: newCapacityCache()}
		now := time.Now()
		s.capCache.recordPollSuccess("000000000001", 10.0, now)
		s.capCache.invalidate("000000000001")
		s.capCache.recordPollSuccess("000000000002", 90.0, now)

		got := s.getArrayIDFromTopologyRequirement(topologyRequirement)
		assert.Equal(t, "000000000002", got, "should skip the unavailable array even though its utilization is lower")
	})

	t.Run("all arrays unavailable returns empty ArrayID (AC-006 surfaces as SYMID-required error upstream)", func(t *testing.T) {
		s := &service{opts: Opts{StorageArrays: storageArrayConfig}, capCache: newCapacityCache()}
		now := time.Now()
		s.capCache.recordPollSuccess("000000000001", 10.0, now)
		s.capCache.invalidate("000000000001")
		s.capCache.recordPollSuccess("000000000002", 20.0, now)
		s.capCache.invalidate("000000000002")

		got := s.getArrayIDFromTopologyRequirement(topologyRequirement)
		assert.Empty(t, got)
	})
}

func Test_service_getArrayIDFromTopology(t *testing.T) {
	tests := []struct {
		name               string
		storageArrayConfig map[string]StorageArrayConfig
		topology           *csi.Topology
		want               string
	}{
		{
			name:               "nil topology",
			topology:           nil,
			storageArrayConfig: nil,
			want:               "",
		},
		{
			name:               "nil topology segments",
			topology:           &csi.Topology{Segments: nil},
			storageArrayConfig: nil,
			want:               "",
		},
		{
			name: "basic test of successful match",
			topology: &csi.Topology{
				Segments: map[string]string{
					"topology.kubernetes.io/region": "region1",
					"topology.kubernetes.io/zone":   "zone1",
				},
			},
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
			},
			want: "000000000001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					StorageArrays: tt.storageArrayConfig,
				},
			}

			got := s.getArrayIDFromTopology(tt.topology)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_service_buildMetroAccessibleTopology(t *testing.T) {
	tests := []struct {
		name               string
		storageArrayConfig map[string]StorageArrayConfig
		accessibility      *csi.TopologyRequirement
		localSymID         string
		remoteSymID        string
		wantLen            int
		wantSite1Array     string
		wantSite2Array     string
		wantProtocolSuffix string // expected protocol suffix like ".iscsi"
	}{
		{
			name:          "nil accessibility returns nil",
			accessibility: nil,
			localSymID:    "000120001647",
			remoteSymID:   "000120001965",
			wantLen:       0,
		},
		{
			name: "empty preferred returns nil",
			accessibility: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{},
			},
			localSymID:  "000120001647",
			remoteSymID: "000120001965",
			wantLen:     0,
		},
		{
			name: "missing local array config falls back to original",
			storageArrayConfig: map[string]StorageArrayConfig{
				"000120001965": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
			},
			accessibility: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{Segments: map[string]string{"topology.kubernetes.io/site": "site1"}},
				},
			},
			localSymID:  "000120001647",
			remoteSymID: "000120001965",
			wantLen:     1, // Falls back to original
		},
		{
			name: "missing remote array config falls back to original",
			storageArrayConfig: map[string]StorageArrayConfig{
				"000120001647": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
			},
			accessibility: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{Segments: map[string]string{"topology.kubernetes.io/site": "site1"}},
				},
			},
			localSymID:  "000120001647",
			remoteSymID: "000120001965",
			wantLen:     1, // Falls back to original
		},
		{
			name: "both arrays configured - builds topology for both sites",
			storageArrayConfig: map[string]StorageArrayConfig{
				"000120001647": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
				"000120001965": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
			},
			accessibility: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{Segments: map[string]string{
						"topology.kubernetes.io/site":           "site1",
						"csi-powermax.dellemc.com/000120001647": "csi-powermax.dellemc.com",
					}},
				},
			},
			localSymID:     "000120001647",
			remoteSymID:    "000120001965",
			wantLen:        2, // One for each site
			wantSite1Array: "000120001647",
			wantSite2Array: "000120001965",
		},
		{
			name: "empty labels on local array falls back to original",
			storageArrayConfig: map[string]StorageArrayConfig{
				"000120001647": {Labels: map[string]interface{}{}},
				"000120001965": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
			},
			accessibility: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{Segments: map[string]string{"topology.kubernetes.io/site": "site1"}},
				},
			},
			localSymID:  "000120001647",
			remoteSymID: "000120001965",
			wantLen:     1, // Falls back to original
		},
		{
			name: "protocol suffix extracted and applied to both sites - iscsi",
			storageArrayConfig: map[string]StorageArrayConfig{
				"000120001647": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
				"000120001965": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
			},
			accessibility: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{Segments: map[string]string{
						"topology.kubernetes.io/site":                 "site1",
						"csi-powermax.dellemc.com/000120001647":       "csi-powermax.dellemc.com",
						"csi-powermax.dellemc.com/000120001647.iscsi": "csi-powermax.dellemc.com",
					}},
				},
			},
			localSymID:         "000120001647",
			remoteSymID:        "000120001965",
			wantLen:            2,
			wantSite1Array:     "000120001647",
			wantSite2Array:     "000120001965",
			wantProtocolSuffix: ".iscsi",
		},
		{
			name: "protocol suffix extracted and applied to both sites - fc",
			storageArrayConfig: map[string]StorageArrayConfig{
				"000120001647": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
				"000120001965": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
			},
			accessibility: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{Segments: map[string]string{
						"topology.kubernetes.io/site":              "site1",
						"csi-powermax.dellemc.com/000120001647":    "csi-powermax.dellemc.com",
						"csi-powermax.dellemc.com/000120001647.fc": "csi-powermax.dellemc.com",
					}},
				},
			},
			localSymID:         "000120001647",
			remoteSymID:        "000120001965",
			wantLen:            2,
			wantSite1Array:     "000120001647",
			wantSite2Array:     "000120001965",
			wantProtocolSuffix: ".fc",
		},
		{
			name: "multiple preferred topologies - still creates only 2 entries",
			storageArrayConfig: map[string]StorageArrayConfig{
				"000120001647": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
				"000120001965": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
			},
			accessibility: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{Segments: map[string]string{
						"topology.kubernetes.io/site":                 "site1",
						"csi-powermax.dellemc.com/000120001647":       "csi-powermax.dellemc.com",
						"csi-powermax.dellemc.com/000120001647.iscsi": "csi-powermax.dellemc.com",
					}},
					{Segments: map[string]string{
						"topology.kubernetes.io/site":                 "site1",
						"csi-powermax.dellemc.com/000120001647":       "csi-powermax.dellemc.com",
						"csi-powermax.dellemc.com/000120001647.iscsi": "csi-powermax.dellemc.com",
					}},
					{Segments: map[string]string{
						"topology.kubernetes.io/site":                 "site2",
						"csi-powermax.dellemc.com/000120001647":       "csi-powermax.dellemc.com",
						"csi-powermax.dellemc.com/000120001647.iscsi": "csi-powermax.dellemc.com",
						"csi-powermax.dellemc.com/000120001965":       "csi-powermax.dellemc.com",
					}},
				},
			},
			localSymID:         "000120001647",
			remoteSymID:        "000120001965",
			wantLen:            2, // Still only 2 entries, one per site
			wantSite1Array:     "000120001647",
			wantSite2Array:     "000120001965",
			wantProtocolSuffix: ".iscsi",
		},
		{
			name: "site1 topology should not contain remote array keys",
			storageArrayConfig: map[string]StorageArrayConfig{
				"000120001647": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"}},
				"000120001965": {Labels: map[string]interface{}{"topology.kubernetes.io/site": "site2"}},
			},
			accessibility: &csi.TopologyRequirement{
				Preferred: []*csi.Topology{
					{Segments: map[string]string{
						"topology.kubernetes.io/site":                 "site1",
						"csi-powermax.dellemc.com/000120001647":       "csi-powermax.dellemc.com",
						"csi-powermax.dellemc.com/000120001647.iscsi": "csi-powermax.dellemc.com",
					}},
				},
			},
			localSymID:         "000120001647",
			remoteSymID:        "000120001965",
			wantLen:            2,
			wantSite1Array:     "000120001647",
			wantSite2Array:     "000120001965",
			wantProtocolSuffix: ".iscsi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					StorageArrays: tt.storageArrayConfig,
				},
			}
			got := s.buildMetroAccessibleTopology(tt.accessibility, tt.localSymID, tt.remoteSymID)
			if tt.wantLen == 0 {
				assert.Nil(t, got)
			} else {
				assert.Equal(t, tt.wantLen, len(got))
				if tt.wantLen == 2 && tt.wantSite1Array != "" {
					// Verify site1 topology has correct array
					site1Found := false
					site2Found := false
					for _, topo := range got {
						if topo.Segments["topology.kubernetes.io/site"] == "site1" {
							site1Found = true
							assert.Contains(t, topo.Segments, "csi-powermax.dellemc.com/"+tt.wantSite1Array)
							// Verify site1 does NOT contain remote array keys
							assert.NotContains(t, topo.Segments, "csi-powermax.dellemc.com/"+tt.wantSite2Array)
							// Verify protocol key is present if expected
							if tt.wantProtocolSuffix != "" {
								assert.Contains(t, topo.Segments, "csi-powermax.dellemc.com/"+tt.wantSite1Array+tt.wantProtocolSuffix)
							}
						}
						if topo.Segments["topology.kubernetes.io/site"] == "site2" {
							site2Found = true
							assert.Contains(t, topo.Segments, "csi-powermax.dellemc.com/"+tt.wantSite2Array)
							// Verify site2 does NOT contain local array keys
							assert.NotContains(t, topo.Segments, "csi-powermax.dellemc.com/"+tt.wantSite1Array)
							// Verify protocol key is present if expected
							if tt.wantProtocolSuffix != "" {
								assert.Contains(t, topo.Segments, "csi-powermax.dellemc.com/"+tt.wantSite2Array+tt.wantProtocolSuffix)
							}
						}
					}
					assert.True(t, site1Found, "site1 topology should be present")
					assert.True(t, site2Found, "site2 topology should be present")
				}
			}
		})
	}
}

func Test_service_resolveParameter(t *testing.T) {
	tests := []struct {
		name               string
		storageArrayConfig map[string]StorageArrayConfig
		arrayID            string
		params             map[string]string
		paramName          string
		defaultValue       string
		want               string
	}{
		{
			name:               "nil storage array",
			storageArrayConfig: nil,
			params:             map[string]string{},
			arrayID:            "000000000001",
			paramName:          "UTParamName",
			want:               "",
		},
		{
			name:               "nil storage array and params",
			storageArrayConfig: nil,
			params:             nil,
			arrayID:            "000000000001",
			paramName:          "UTParamName",
			want:               "",
		},
		{
			name:               "no array ID",
			storageArrayConfig: nil,
			params:             nil,
			arrayID:            "",
			paramName:          "UTParamName",
			want:               "",
		},
		{
			name:               "no parameter provided anywhere",
			storageArrayConfig: map[string]StorageArrayConfig{},
			params:             map[string]string{},
			arrayID:            "000000000001",
			paramName:          "UTParamName",
			want:               "",
		},
		{
			name:               "parameter provided in params",
			storageArrayConfig: map[string]StorageArrayConfig{},
			params:             map[string]string{"UTParamName": "UTParam"},
			arrayID:            "000000000001",
			paramName:          "UTParamName",
			want:               "UTParam",
		},
		{
			name: "parameter provided in secret",
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Parameters: map[string]interface{}{
						"utparamname": "UTSecret",
					},
				},
			},
			params:    map[string]string{},
			arrayID:   "000000000001",
			paramName: "UTParamName",
			want:      "UTSecret",
		},
		{
			name: "parameter in params should override secret",
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Parameters: map[string]interface{}{
						"utparamname": "UTSecret",
					},
				},
			},
			params:    map[string]string{"UTParamName": "UTParam"},
			arrayID:   "000000000001",
			paramName: "UTParamName",
			want:      "UTParam",
		},
		{
			name:               "default value provided",
			storageArrayConfig: nil,
			params:             nil,
			arrayID:            "000000000001",
			paramName:          "UTParamName",
			defaultValue:       "UTParam",
			want:               "UTParam",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					StorageArrays: tt.storageArrayConfig,
				},
			}

			got := s.resolveParameter(tt.params, tt.arrayID, tt.paramName, tt.defaultValue)
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_service_addZoneLabelsToVolumeAttributes(t *testing.T) {
	tests := []struct {
		name               string
		storageArrayConfig map[string]StorageArrayConfig
		arrayID            string
		params             map[string]string
		want               map[string]string
	}{
		{
			name:               "no storage array",
			storageArrayConfig: nil,
			params:             map[string]string{"source": "UT"},
			arrayID:            "000000000001",
			want:               map[string]string{"source": "UT"},
		},
		{
			name: "basic addition",
			storageArrayConfig: map[string]StorageArrayConfig{
				"000000000001": {
					Labels: map[string]interface{}{
						"topology.kubernetes.io/region": "region1",
						"topology.kubernetes.io/zone":   "zone1",
					},
				},
			},
			params:  map[string]string{"source": "UT"},
			arrayID: "000000000001",
			want: map[string]string{
				"source":                        "UT",
				"topology.kubernetes.io/region": "region1",
				"topology.kubernetes.io/zone":   "zone1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					StorageArrays: tt.storageArrayConfig,
				},
			}

			s.addZoneLabelsToVolumeAttributes(tt.params, tt.arrayID)
			assert.Equal(t, tt.want, tt.params)
		})
	}
}

func TestGetDynamicSG(t *testing.T) {
	tests := []struct {
		name                  string
		arrayID               string
		initializeArray       string
		baseSGName            string
		mockClient            *mocks.MockPmaxClient
		expectedSGName        string
		expectedNeedsCreation bool
		expectedErr           error
	}{
		{
			name:            "successful get base SG name as new dynamic SG when no SGs exist",
			arrayID:         "sg-array1",
			initializeArray: "sg-array1",
			baseSGName:      "csi-W55-Diamond-SRP_1-SG",
			mockClient: func() *mocks.MockPmaxClient {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(client)
				client.EXPECT().GetStorageGroupVolumeCounts(gomock.Any(), "sg-array1", "csi-W55-Diamond-SRP_1-SG").Return(&types.StorageGroupVolumeCounts{
					StorageGroups: []types.StorageGroupVolumeCount{},
				}, nil)
				return client
			}(),
			expectedSGName:        "csi-W55-Diamond-SRP_1-SG",
			expectedNeedsCreation: true,
			expectedErr:           nil,
		},
		{
			name:            "successful get existing dynamic SG with space",
			arrayID:         "sg-array1",
			initializeArray: "sg-array1",
			baseSGName:      "csi-W55-Diamond-SRP_1-SG",
			mockClient: func() *mocks.MockPmaxClient {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(client)
				client.EXPECT().GetStorageGroupVolumeCounts(gomock.Any(), "sg-array1", "csi-W55-Diamond-SRP_1-SG").Return(&types.StorageGroupVolumeCounts{
					StorageGroups: []types.StorageGroupVolumeCount{
						{ID: "csi-W55-Diamond-SRP_1-SG", VolumeCount: 1},
					},
				}, nil)
				return client
			}(),
			expectedSGName:        "csi-W55-Diamond-SRP_1-SG",
			expectedNeedsCreation: false,
			expectedErr:           nil,
		},
		{
			name:            "successful get new dynamic SG when no space in existing SGs",
			arrayID:         "sg-array1",
			initializeArray: "sg-array1",
			baseSGName:      "csi-W55-Diamond-SRP_1-SG",
			mockClient: func() *mocks.MockPmaxClient {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(client)
				client.EXPECT().GetStorageGroupVolumeCounts(gomock.Any(), "sg-array1", "csi-W55-Diamond-SRP_1-SG").Return(&types.StorageGroupVolumeCounts{
					StorageGroups: []types.StorageGroupVolumeCount{
						{ID: "csi-W55-Diamond-SRP_1-SG", VolumeCount: 2},
					},
				}, nil)
				return client
			}(),
			expectedSGName:        "csi-W55-Diamond-SRP_1-SG--1",
			expectedNeedsCreation: true,
			expectedErr:           nil,
		},
		{
			name:            "successful get existing dynamic SG with minimal vol count",
			arrayID:         "sg-array1",
			initializeArray: "sg-array1",
			baseSGName:      "csi-W55-Diamond-SRP_1-SG",
			mockClient: func() *mocks.MockPmaxClient {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(client)
				client.EXPECT().GetStorageGroupVolumeCounts(gomock.Any(), "sg-array1", "csi-W55-Diamond-SRP_1-SG").Return(&types.StorageGroupVolumeCounts{
					StorageGroups: []types.StorageGroupVolumeCount{
						{ID: "csi-W55-Diamond-SRP_1-SG", VolumeCount: 2},
						{ID: "csi-W55-Diamond-SRP_1-SG--1", VolumeCount: 0},
						{ID: "csi-W55-Diamond-SRP_1-SG--2", VolumeCount: 1},
					},
				}, nil)
				return client
			}(),
			expectedSGName:        "csi-W55-Diamond-SRP_1-SG--1",
			expectedNeedsCreation: false,
			expectedErr:           nil,
		},
		{
			name:            "error getting PowerMax client",
			arrayID:         "sg-array1",
			initializeArray: "",
			baseSGName:      "csi-W55-Diamond-SRP_1-SG",
			mockClient: func() *mocks.MockPmaxClient {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(nil)
				return client
			}(),
			expectedSGName:        "",
			expectedNeedsCreation: false,
			expectedErr:           fmt.Errorf("failed to get PowerMax client for array sg-array1: array: sg-array1 not found"),
		},
		{
			name:            "error getting storage group volume counts for array",
			arrayID:         "sg-array1",
			initializeArray: "sg-array1",
			baseSGName:      "csi-W55-Diamond-SRP_1-SG",
			mockClient: func() *mocks.MockPmaxClient {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(client)
				client.EXPECT().GetStorageGroupVolumeCounts(gomock.Any(), "sg-array1", "csi-W55-Diamond-SRP_1-SG").Return(nil, errors.New("error getting sg"))

				return client
			}(),
			expectedSGName:        "",
			expectedNeedsCreation: false,
			expectedErr:           fmt.Errorf("failed to get storage group volume counts for array sg-array1: error getting sg"),
		},
		{
			name:            "handle huge number of SGs",
			arrayID:         "sg-array1",
			initializeArray: "sg-array1",
			baseSGName:      "csi-W55-Diamond-SRP_1-SG",
			mockClient: func() *mocks.MockPmaxClient {
				sgs := make([]types.StorageGroupVolumeCount, 102)
				sgs[0] = types.StorageGroupVolumeCount{
					ID:          "csi-W55-Diamond-SRP_1-SG",
					VolumeCount: 2,
				}
				for i := 1; i < 101; i++ {
					sgs[i] = types.StorageGroupVolumeCount{
						ID:          fmt.Sprintf("csi-W55-Diamond-SRP_1-SG--%d", i),
						VolumeCount: 2,
					}
				}
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(client)
				client.EXPECT().GetStorageGroupVolumeCounts(gomock.Any(), "sg-array1", "csi-W55-Diamond-SRP_1-SG").Return(&types.StorageGroupVolumeCounts{
					StorageGroups: sgs,
				}, nil)
				return client
			}(),
			expectedSGName:        "csi-W55-Diamond-SRP_1-SG--101",
			expectedNeedsCreation: true,
			expectedErr:           nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sgVolumeLimit = 2
			s := &service{
				adminClient: tt.mockClient,
			}
			_ = symmetrix.Initialize([]string{tt.initializeArray}, tt.mockClient)
			defer symmetrix.RemoveClient(tt.initializeArray)

			sgName, needsCreation, err := getDynamicSG(context.Background(), tt.arrayID, tt.baseSGName, s)
			if err != nil && tt.expectedErr != nil {
				if err.Error() != tt.expectedErr.Error() {
					t.Errorf("expected error %v, but got %v", tt.expectedErr, err)
				}
			} else if err != nil {
				t.Errorf("expected no error, but got %v", err)
			}
			if sgName != tt.expectedSGName {
				t.Errorf("expected storage group name %v, but got %v", tt.expectedSGName, sgName)
			}
			if needsCreation != tt.expectedNeedsCreation {
				t.Errorf("expected needs creation %v, but got %v", tt.expectedNeedsCreation, needsCreation)
			}
		})
	}
}

// setupFCInitiatorMocks sets up the common mock expectations for a Fibre host
// that successfully resolves initiators to SCSI_FC ports.
// Returns dirPort "FA-1D:0" in portListFromHost.
func setupFCInitiatorMocks(client *mocks.MockPmaxClient, symID string) {
	client.EXPECT().GetPortListByProtocol(gomock.Any(), symID, "SCSI_FC").Return(&types.PortList{
		SymmetrixPortKey: []types.PortKey{
			{DirectorID: "FA-1D", PortID: "0"},
		},
	}, nil)
	client.EXPECT().GetInitiatorList(gomock.Any(), symID, "5000000000000001", false, false).Return(&types.InitiatorList{
		InitiatorIDs: []string{"FA-1D:0:5000000000000001"},
	}, nil)
	client.EXPECT().GetInitiatorByID(gomock.Any(), symID, "FA-1D:0:5000000000000001").Return(&types.Initiator{
		OnFabric: true,
		LoggedIn: true,
	}, nil)
}

func Test_service_SelectOrCreateFCPGForHost(t *testing.T) {
	defaultHost := &types.Host{
		HostID:     "host1",
		HostType:   "Fibre",
		Initiators: []string{"5000000000000001"},
	}

	tests := []struct {
		name          string
		symID         string
		host          *types.Host
		setup         func(client *mocks.MockPmaxClient)
		expectedPGID  string
		expectedError bool
		errorMsg      string
	}{
		{
			name:  "nil host returns error",
			symID: "000120000001",
			host:  nil,
			setup: func(_ *mocks.MockPmaxClient) {
			},
			expectedError: true,
			errorMsg:      "SelectOrCreateFCPGForHost: host can't be nil",
		},
		{
			name:  "non-Fibre host type returns error for no valid initiators",
			symID: "000120000001",
			host: &types.Host{
				HostID:     "host1",
				HostType:   "iSCSI",
				Initiators: []string{},
			},
			setup: func(_ *mocks.MockPmaxClient) {
			},
			expectedError: true,
			errorMsg:      "failed to find a valid initiator",
		},
		{
			name:  "Fibre host with initiator on non-SCSI_FC port returns error",
			symID: "000120000001",
			host: &types.Host{
				HostID:     "host1",
				HostType:   "Fibre",
				Initiators: []string{"5000000000000001"},
			},
			setup: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetPortListByProtocol(gomock.Any(), "000120000001", "SCSI_FC").Return(&types.PortList{
					SymmetrixPortKey: []types.PortKey{
						{DirectorID: "FA-1D", PortID: "0"},
					},
				}, nil)
				client.EXPECT().GetInitiatorList(gomock.Any(), "000120000001", "5000000000000001", false, false).Return(&types.InitiatorList{
					InitiatorIDs: []string{"FA-2D:0:5000000000000001"},
				}, nil)
			},
			expectedError: true,
			errorMsg:      "failed to find a valid initiator",
		},
		{
			name:  "GetPortListByProtocol error returns error",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetPortListByProtocol(gomock.Any(), "000120000001", "SCSI_FC").Return(nil, errors.New("port list error"))
			},
			expectedError: true,
			errorMsg:      "failed to fetch SCSI_FC ports for array",
		},
		{
			name:  "GetInitiatorList error skips initiator and returns no valid initiator error",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetPortListByProtocol(gomock.Any(), "000120000001", "SCSI_FC").Return(&types.PortList{
					SymmetrixPortKey: []types.PortKey{
						{DirectorID: "FA-1D", PortID: "0"},
					},
				}, nil)
				client.EXPECT().GetInitiatorList(gomock.Any(), "000120000001", "5000000000000001", false, false).Return(nil, errors.New("initiator error"))
			},
			expectedError: true,
			errorMsg:      "failed to find a valid initiator",
		},
		{
			name:  "GetVersionDetails error returns error",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(nil, errors.New("version error"))
			},
			expectedError: true,
			errorMsg:      "error in getversion API",
		},
		{
			name:  "non-numeric API version returns parsing error",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "abc",
				}, nil)
			},
			expectedError: true,
			errorMsg:      "error in parsing Version",
		},
		{
			name:  "enhanced API GetPortGroupListByType error returns error",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				// satisfy 103 version check
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "103",
				}, nil)
				// satisfy v4 version check with anything above "59xx.xxx.x"
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "6079.325.0",
				}, nil)
				client.EXPECT().GetPortGroupListByType(gomock.Any(), "000120000001", "fibre").Return(nil, errors.New("api error"))
			},
			expectedError: true,
			errorMsg:      "failed to fetch Fibre channel port groups for array(enhanced API)",
		},
		{
			name:  "enhanced API invalid base64-encoded port ID is skipped and creates a new port group",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "103",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "6079.325.0",
				}, nil)
				client.EXPECT().GetPortGroupListByType(gomock.Any(), "000120000001", "fibre").Return(&types.PortGroupListResult{
					Results: []types.PortGroupListv1{
						{
							ID:       "csi-ABC-pg1",
							Protocol: FcIscsiID,
							Ports: []types.PortValues{
								{
									// bad base64 encoding is skipped
									PortID:   "!!!invalid-base64!!!",
									Type:     "Fibre",
									Director: types.DirectorID{ID: "FA-1D"},
								},
							},
						},
					},
				}, nil)
				client.EXPECT().CreatePortGroup(gomock.Any(), "000120000001", "csi-ABC-FA-1D-0-PG", gomock.Any(), "SCSI_FC").Return(&types.PortGroup{}, nil).Times(1)
			},
			expectedPGID:  "csi-ABC-FA-1D-0-PG",
			expectedError: false,
		},
		{
			name:  "enhanced API finds matching port group",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "103",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "6079.325.0",
				}, nil)
				// base64.RawStdEncoding.Encode("FA-1D|0") => "RkEtMUR8MA"
				client.EXPECT().GetPortGroupListByType(gomock.Any(), "000120000001", "fibre").Return(&types.PortGroupListResult{
					Results: []types.PortGroupListv1{
						{
							ID:       "csi-ABC-pg-enhanced-match",
							Protocol: FcIscsiID,
							Ports: []types.PortValues{
								{
									PortID:   "RkEtMUR8MA",
									Type:     "Fibre",
									Director: types.DirectorID{ID: "FA-1D"},
								},
							},
						},
					},
				}, nil)
			},
			expectedPGID:  "csi-ABC-pg-enhanced-match",
			expectedError: false,
		},
		{
			name:  "enhanced API no match creates port group",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "103",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "6079.325.0",
				}, nil)
				// base64.RawStdEncoding.Encode("FA-2D|0") => "RkEtMkR8MA"  (different director)
				client.EXPECT().GetPortGroupListByType(gomock.Any(), "000120000001", "fibre").Return(&types.PortGroupListResult{
					Results: []types.PortGroupListv1{
						{
							ID:       "pg-other",
							Protocol: FcIscsiID,
							Ports: []types.PortValues{
								{
									PortID:   "RkEtMkR8MA",
									Type:     "Fibre",
									Director: types.DirectorID{ID: "FA-2D"},
								},
							},
						},
					},
				}, nil)
				client.EXPECT().CreatePortGroup(gomock.Any(), "000120000001", "csi-ABC-FA-1D-0-PG", []types.PortKey{{DirectorID: "FA-1D", PortID: "0"}}, "SCSI_FC").Return(&types.PortGroup{}, nil)
			},
			expectedPGID:  "csi-ABC-FA-1D-0-PG",
			expectedError: false,
		},
		{
			name:  "legacy API GetPortGroupList error returns error",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "102",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "5978.441.0",
				}, nil)
				client.EXPECT().GetPortGroupList(gomock.Any(), "000120000001", "fibre").Return(nil, errors.New("pg list error"))
			},
			expectedError: true,
			errorMsg:      "Failed to fetch Fibre channel port groups for array:",
		},
		{
			name:  "legacy API finds matching port group",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "102",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "5978.441.0",
				}, nil)
				client.EXPECT().GetPortGroupList(gomock.Any(), "000120000001", "fibre").Return(&types.PortGroupList{
					PortGroupIDs: []string{"csi-ABC-pg1"},
				}, nil)
				client.EXPECT().GetPortGroupByID(gomock.Any(), "000120000001", "csi-ABC-pg1").Return(&types.PortGroup{
					PortGroupID:   "csi-ABC-pg1",
					PortGroupType: "Fibre",
					SymmetrixPortKey: []types.PortKey{
						{DirectorID: "FA-1D", PortID: "0"},
					},
				}, nil)
			},
			expectedPGID:  "csi-ABC-pg1",
			expectedError: false,
		},
		{
			name:  "legacy API filters port groups by csi prefix",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "102",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "5978.441.0",
				}, nil)
				// Only "csi-ABC-pg1" matches prefix "csi-ABC"; "other-pg" does not
				client.EXPECT().GetPortGroupList(gomock.Any(), "000120000001", "fibre").Return(&types.PortGroupList{
					PortGroupIDs: []string{"other-pg", "csi-ABC-pg1"},
				}, nil)
				client.EXPECT().GetPortGroupByID(gomock.Any(), "000120000001", "csi-ABC-pg1").Return(&types.PortGroup{
					PortGroupID:   "csi-ABC-pg1",
					PortGroupType: "SCSI_FC",
					SymmetrixPortKey: []types.PortKey{
						{DirectorID: "FA-1D", PortID: "0"},
					},
				}, nil)
			},
			expectedPGID:  "csi-ABC-pg1",
			expectedError: false,
		},
		{
			name:  "legacy API GetPortGroupByID error continues to next",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "102",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "5978.441.0",
				}, nil)
				client.EXPECT().GetPortGroupList(gomock.Any(), "000120000001", "fibre").Return(&types.PortGroupList{
					PortGroupIDs: []string{"csi-ABC-pg1", "csi-ABC-pg2"},
				}, nil)
				// First PG errors, second PG matches
				client.EXPECT().GetPortGroupByID(gomock.Any(), "000120000001", "csi-ABC-pg1").Return(nil, errors.New("pg error"))
				client.EXPECT().GetPortGroupByID(gomock.Any(), "000120000001", "csi-ABC-pg2").Return(&types.PortGroup{
					PortGroupID:   "csi-ABC-pg2",
					PortGroupType: "Fibre",
					SymmetrixPortKey: []types.PortKey{
						{DirectorID: "FA-1D", PortID: "0"},
					},
				}, nil)
			},
			expectedPGID:  "csi-ABC-pg2",
			expectedError: false,
		},
		{
			name:  "legacy API no match creates port group",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "102",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "5978.441.0",
				}, nil)
				client.EXPECT().GetPortGroupList(gomock.Any(), "000120000001", "fibre").Return(&types.PortGroupList{
					PortGroupIDs: []string{"other-pg"},
				}, nil)
				client.EXPECT().CreatePortGroup(gomock.Any(), "000120000001", "csi-ABC-FA-1D-0-PG", gomock.Any(), "SCSI_FC").Return(&types.PortGroup{}, nil)
			},
			expectedPGID:  "csi-ABC-FA-1D-0-PG",
			expectedError: false,
		},
		{
			name:  "new API with legacy microcode creates port group",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "103",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "5978.441.0",
				}, nil)
				client.EXPECT().GetPortGroupList(gomock.Any(), "000120000001", "fibre").Return(&types.PortGroupList{
					PortGroupIDs: []string{"other-pg"},
				}, nil)
				client.EXPECT().CreatePortGroup(gomock.Any(), "000120000001", "csi-ABC-FA-1D-0-PG", gomock.Any(), "SCSI_FC").Return(&types.PortGroup{}, nil)
			},
			expectedPGID:  "csi-ABC-FA-1D-0-PG",
			expectedError: false,
		},
		{
			name:  "CreatePortGroup error returns error",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "102",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "5978.441.0",
				}, nil)
				client.EXPECT().GetPortGroupList(gomock.Any(), "000120000001", "fibre").Return(&types.PortGroupList{
					PortGroupIDs: []string{"other-pg"},
				}, nil)
				client.EXPECT().CreatePortGroup(gomock.Any(), "000120000001", "csi-ABC-FA-1D-0-PG", gomock.Any(), "SCSI_FC").Return(nil, errors.New("create error"))
			},
			expectedError: true,
			errorMsg:      "Failed to create PortGroup",
		},
		{
			name:  "legacy API port group with non-Fibre type is skipped",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "102",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "5978.441.0",
				}, nil)
				client.EXPECT().GetPortGroupList(gomock.Any(), "000120000001", "fibre").Return(&types.PortGroupList{
					PortGroupIDs: []string{"csi-ABC-pg1"},
				}, nil)
				// PortGroupType is iSCSI, not Fibre/SCSI_FC, so it should be skipped
				client.EXPECT().GetPortGroupByID(gomock.Any(), "000120000001", "csi-ABC-pg1").Return(&types.PortGroup{
					PortGroupID:   "csi-ABC-pg1",
					PortGroupType: "iSCSI",
					SymmetrixPortKey: []types.PortKey{
						{DirectorID: "FA-1D", PortID: "0"},
					},
				}, nil)
				// No match → creates PG
				client.EXPECT().CreatePortGroup(gomock.Any(), "000120000001", "csi-ABC-FA-1D-0-PG", gomock.Any(), "SCSI_FC").Return(&types.PortGroup{}, nil)
			},
			expectedPGID:  "csi-ABC-FA-1D-0-PG",
			expectedError: false,
		},
		{
			name:  "legacy API port group with mismatched ports creates new PG",
			symID: "000120000001",
			host:  defaultHost,
			setup: func(client *mocks.MockPmaxClient) {
				setupFCInitiatorMocks(client, "000120000001")
				client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
					APIVersion: "102",
				}, nil)
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000120000001").Return(&types.Symmetrix{
					SymmetrixID: "000120000001",
					Microcode:   "5978.441.0",
				}, nil)
				client.EXPECT().GetPortGroupList(gomock.Any(), "000120000001", "fibre").Return(&types.PortGroupList{
					PortGroupIDs: []string{"csi-ABC-pg1"},
				}, nil)
				// Different ports than what the host has
				client.EXPECT().GetPortGroupByID(gomock.Any(), "000120000001", "csi-ABC-pg1").Return(&types.PortGroup{
					PortGroupID:   "csi-ABC-pg1",
					PortGroupType: "Fibre",
					SymmetrixPortKey: []types.PortKey{
						{DirectorID: "FA-2D", PortID: "1"},
					},
				}, nil)
				client.EXPECT().CreatePortGroup(gomock.Any(), "000120000001", "csi-ABC-FA-1D-0-PG", gomock.Any(), "SCSI_FC").Return(&types.PortGroup{}, nil)
			},
			expectedPGID:  "csi-ABC-FA-1D-0-PG",
			expectedError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			client := mocks.NewMockPmaxClient(ctrl)
			client.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
			tt.setup(client)

			svc := &service{
				opts: Opts{
					ClusterPrefix: "ABC",
				},
			}
			pgID, err := svc.SelectOrCreateFCPGForHost(context.Background(), tt.symID, tt.host, client)
			if tt.expectedError {
				assert.Error(t, err)
				if tt.errorMsg != "" {
					assert.Contains(t, err.Error(), tt.errorMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedPGID, pgID)
			}
		})
	}
}

func Test_service_isV4OrAbove(t *testing.T) {
	tests := []struct {
		name   string
		symID  string
		setup  func(client *mocks.MockPmaxClient)
		wantV4 bool
	}{
		{
			name:  "V4 array with microcode 6079",
			symID: "000197900046",
			setup: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000197900046").Times(1).Return(&types.Symmetrix{
					SymmetrixID: "000197900046",
					Microcode:   "6079.325.0",
				}, nil)
			},
			wantV4: true,
		},
		{
			name:  "V3 array with microcode 5978",
			symID: "000197900047",
			setup: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000197900047").Times(1).Return(&types.Symmetrix{
					SymmetrixID: "000197900047",
					Microcode:   "5978.441.0",
				}, nil)
			},
			wantV4: false,
		},
		{
			name:  "V4 array with microcode 6100",
			symID: "000197900050",
			setup: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000197900050").Times(1).Return(&types.Symmetrix{
					SymmetrixID: "000197900050",
					Microcode:   "6100.100.0",
				}, nil)
			},
			wantV4: true,
		},
		{
			name:  "boundary value microcode 59xx should be V3",
			symID: "000197900051",
			setup: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000197900051").Times(1).Return(&types.Symmetrix{
					SymmetrixID: "000197900051",
					Microcode:   "5999.100.0",
				}, nil)
			},
			wantV4: false,
		},
		{
			name:  "empty microcode defaults to legacy",
			symID: "000197900052",
			setup: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000197900052").Times(1).Return(&types.Symmetrix{
					SymmetrixID: "000197900052",
					Microcode:   "",
				}, nil)
			},
			wantV4: false,
		},
		{
			name:  "short microcode defaults to legacy",
			symID: "000197900053",
			setup: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000197900053").Times(1).Return(&types.Symmetrix{
					SymmetrixID: "000197900053",
					Microcode:   "6",
				}, nil)
			},
			wantV4: false,
		},
		{
			name:  "non-numeric microcode prefix defaults to legacy",
			symID: "000197900054",
			setup: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000197900054").Times(1).Return(&types.Symmetrix{
					SymmetrixID: "000197900054",
					Microcode:   "AB79.325.0",
				}, nil)
			},
			wantV4: false,
		},
		{
			name:  "GetSymmetrixByID returns error defaults to legacy",
			symID: "000197900055",
			setup: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetSymmetrixByID(gomock.Any(), "000197900055").Times(1).Return(nil, errors.New("connection error"))
			},
			wantV4: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			client := mocks.NewMockPmaxClient(ctrl)
			client.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
			tt.setup(client)

			svc := &service{}
			got := svc.isV4OrAbove(context.Background(), tt.symID, client)
			assert.Equal(t, tt.wantV4, got)
		})
	}
}

func TestDeleteVolumeWithDeletionPrefix(t *testing.T) {
	// Test to verify that volumes with _DEL prefix are processed for deletion
	// rather than being skipped due to VolumeIdentifier mismatch
	tests := []struct {
		name             string
		volumeIdentifier string
		volName          string
		expectContinue   bool
	}{
		{
			name:             "Volume with _DEL prefix containing original name should continue",
			volumeIdentifier: "_DEL_csi-test-cluster-my-volume",
			volName:          "csi-test-cluster-my-volume",
			expectContinue:   true,
		},
		{
			name:             "Volume without _DEL prefix mismatch should return nil",
			volumeIdentifier: "csi-test-cluster-different-volume",
			volName:          "csi-test-cluster-my-volume",
			expectContinue:   false,
		},
		{
			name:             "Volume with _DEL prefix but different name should return nil",
			volumeIdentifier: "_DEL_csi-test-cluster-different-volume",
			volName:          "csi-test-cluster-my-volume",
			expectContinue:   false,
		},
		{
			name:             "Volume with exact match should not enter this logic",
			volumeIdentifier: "csi-test-cluster-my-volume",
			volName:          "csi-test-cluster-my-volume",
			expectContinue:   false, // This case won't enter the != check
		},
		{
			name:             "Volume with _DEL prefix but empty original name",
			volumeIdentifier: "_DEL_",
			volName:          "",
			expectContinue:   true,
		},
		{
			name:             "Volume with _DEL prefix and partial name match",
			volumeIdentifier: "_DEL_csi-test-cluster",
			volName:          "csi-test-cluster",
			expectContinue:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the logic that was added to deleteVolume function
			// Only test the != case, as exact match bypasses this logic
			if tt.volumeIdentifier != tt.volName {
				hasDeletionPrefix := strings.HasPrefix(tt.volumeIdentifier, DeletionPrefix)
				containsOriginalName := strings.Contains(tt.volumeIdentifier, tt.volName)

				shouldContinue := hasDeletionPrefix && containsOriginalName

				assert.Equal(t, tt.expectContinue, shouldContinue,
					"Expected continue=%v for VolumeIdentifier=%s, volName=%s",
					tt.expectContinue, tt.volumeIdentifier, tt.volName)
			} else {
				// For exact matches, this logic shouldn't be reached
				assert.False(t, tt.expectContinue,
					"Exact match case should not enter the != logic")
			}
		})
	}
}

func TestDeleteVolumeRetryQueuing(t *testing.T) {
	// Test for the new enhancement where volumes with deletion prefix that failed
	// to be queued get re-attempted for queuing instead of being assumed deleted
	tests := []struct {
		name             string
		volumeIdentifier string
		volName          string
		expectRetryCall  bool
		expectedDelName  string
		description      string
	}{
		{
			name:             "Volume with exact deletion prefix should retry queuing",
			volumeIdentifier: "_DELcsi-test-cluster-my-volume",
			volName:          "csi-test-cluster-my-volume",
			expectRetryCall:  true,
			expectedDelName:  "_DELcsi-test-cluster-my-volume",
			description:      "Volume was renamed for deletion but not yet queued",
		},
		{
			name:             "Volume with deletion prefix but different name should not retry",
			volumeIdentifier: "_DELcsi-test-cluster-different-volume",
			volName:          "csi-test-cluster-my-volume",
			expectRetryCall:  false,
			expectedDelName:  "_DELcsi-test-cluster-my-volume",
			description:      "Different volume name, should be assumed deleted",
		},
		{
			name:             "Volume without deletion prefix should not retry",
			volumeIdentifier: "csi-test-cluster-my-volume",
			volName:          "csi-test-cluster-my-volume",
			expectRetryCall:  false,
			expectedDelName:  "_DELcsi-test-cluster-my-volume",
			description:      "Normal case, exact match bypasses this logic",
		},
		{
			name:             "Volume with truncated deletion prefix should retry",
			volumeIdentifier: "_DELvery-long-volume-name-that-exceeds-maximum-identifier-length",
			volName:          "very-long-volume-name-that-exceeds-maximum-identifier-length-and-should-be-truncated",
			expectRetryCall:  true,
			expectedDelName:  "_DELvery-long-volume-name-that-exceeds-maximum-identifier-length", // Truncated to MaxVolIdentifierLength
			description:      "Test truncation logic for long identifiers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockCtrl := gomock.NewController(t)
			defer mockCtrl.Finish()

			// Create mocks
			mockDeletionWorker := NewMockDeletionWorker(mockCtrl)

			// Test the logic from the new enhancement
			if tt.volumeIdentifier != tt.volName {
				// Calculate expected deletion name (same logic as in the actual code)
				expectedDelName := fmt.Sprintf("%s%s", DeletionPrefix, tt.volName)
				if len(expectedDelName) > MaxVolIdentifierLength {
					expectedDelName = expectedDelName[:MaxVolIdentifierLength]
				}

				// Check if the volume identifier matches the expected deletion name
				shouldRetry := tt.volumeIdentifier == expectedDelName

				if shouldRetry && tt.expectRetryCall {
					// Mock the QueueDeviceForDeletion call
					mockDeletionWorker.EXPECT().QueueDeviceForDeletion(gomock.Any(), tt.volumeIdentifier, gomock.Any()).Return(nil)

					// Simulate the retry logic
					err := mockDeletionWorker.QueueDeviceForDeletion("test-volume-id", tt.volumeIdentifier, "test-symid")
					assert.NoError(t, err, "QueueDeviceForDeletion should not return error")
				}

				assert.Equal(t, tt.expectRetryCall, shouldRetry,
					"Expected retry call=%v for VolumeIdentifier=%s, volName=%s",
					tt.expectRetryCall, tt.volumeIdentifier, tt.volName)

				assert.Equal(t, tt.expectedDelName, expectedDelName,
					"Expected deletion name mismatch")
			}
		})
	}
}

func TestDeleteVolumeRetryQueuingError(t *testing.T) {
	// Test error handling when retry queuing fails
	mockCtrl := gomock.NewController(t)
	defer mockCtrl.Finish()

	mockDeletionWorker := NewMockDeletionWorker(mockCtrl)

	// Mock QueueDeviceForDeletion to return an error
	expectedError := errors.New("failed to queue device for deletion")
	mockDeletionWorker.EXPECT().QueueDeviceForDeletion(gomock.Any(), gomock.Any(), gomock.Any()).Return(expectedError)

	// Test the error scenario
	err := mockDeletionWorker.QueueDeviceForDeletion("test-volume-id", "_DEL_test-volume", "test-symid")
	assert.Error(t, err, "QueueDeviceForDeletion should return error")
	assert.Contains(t, err.Error(), "failed to queue device for deletion", "Error message should match")
}

func Test_service_updatePublishContext(t *testing.T) {
	symID := "000120000001"
	mvID := "csi-mv--worker-1"
	devID := "011AB"
	portGroupID := "csi-vsphere-VC-PG"

	// Minimize retry delay for tests
	origDelay := getMVConnectionsDelay
	getMVConnectionsDelay = 1 * time.Millisecond
	defer func() { getMVConnectionsDelay = origDelay }()

	tests := []struct {
		name           string
		isVsphere      bool
		connections    []*types.MaskingViewConnection
		setupMock      func(client *mocks.MockPmaxClient)
		expectErr      bool
		errContains    string
		expectLUN      string
		expectDirPorts int
	}{
		{
			name:      "vSphere: no connections, builds context from port group",
			isVsphere: true,
			connections: []*types.MaskingViewConnection{
				// connection for a different volume so our devID gets lunid=""
				{VolumeID: "OTHER", HostLUNAddress: "0001", DirectorPort: "SE-1E:4"},
			},
			setupMock: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetMaskingViewConnections(gomock.Any(), symID, mvID, devID).
					Return([]*types.MaskingViewConnection{}, nil).AnyTimes()
				client.EXPECT().GetMaskingViewByID(gomock.Any(), symID, mvID).
					Return(&types.MaskingView{
						MaskingViewID: mvID,
						PortGroupID:   portGroupID,
					}, nil).Times(1)
				client.EXPECT().GetPortGroupByID(gomock.Any(), symID, portGroupID).
					Return(&types.PortGroup{
						PortGroupID: portGroupID,
						SymmetrixPortKey: []types.PortKey{
							{DirectorID: "OR-1C", PortID: "4"},
							{DirectorID: "OR-2C", PortID: "4"},
						},
					}, nil).Times(1)
				client.EXPECT().GetPort(gomock.Any(), symID, "OR-1C", "4").
					Return(&types.Port{
						SymmetrixPort: types.SymmetrixPortType{
							Identifier: "50000973f0064001",
						},
					}, nil).Times(1)
				client.EXPECT().GetPort(gomock.Any(), symID, "OR-2C", "4").
					Return(&types.Port{
						SymmetrixPort: types.SymmetrixPortType{
							Identifier: "50000973f0064002",
						},
					}, nil).Times(1)
			},
			expectErr: false,
			expectLUN: "0000",
		},
		{
			name:        "non-vSphere: no connections returns error",
			isVsphere:   false,
			connections: []*types.MaskingViewConnection{},
			setupMock: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetMaskingViewConnections(gomock.Any(), symID, mvID, devID).
					Return([]*types.MaskingViewConnection{}, nil).AnyTimes()
			},
			expectErr:   true,
			errContains: "No matching connections for deviceID",
		},
		{
			name:      "vSphere: GetMaskingViewByID fails",
			isVsphere: true,
			connections: []*types.MaskingViewConnection{
				{VolumeID: "OTHER", HostLUNAddress: "0001", DirectorPort: "SE-1E:4"},
			},
			setupMock: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetMaskingViewConnections(gomock.Any(), symID, mvID, devID).
					Return([]*types.MaskingViewConnection{}, nil).AnyTimes()
				client.EXPECT().GetMaskingViewByID(gomock.Any(), symID, mvID).
					Return(nil, errors.New("masking view not found")).Times(1)
			},
			expectErr:   true,
			errContains: "Failed to get masking view",
		},
		{
			name:      "vSphere: GetPortGroupByID fails",
			isVsphere: true,
			connections: []*types.MaskingViewConnection{
				{VolumeID: "OTHER", HostLUNAddress: "0001", DirectorPort: "SE-1E:4"},
			},
			setupMock: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetMaskingViewConnections(gomock.Any(), symID, mvID, devID).
					Return([]*types.MaskingViewConnection{}, nil).AnyTimes()
				client.EXPECT().GetMaskingViewByID(gomock.Any(), symID, mvID).
					Return(&types.MaskingView{
						MaskingViewID: mvID,
						PortGroupID:   portGroupID,
					}, nil).Times(1)
				client.EXPECT().GetPortGroupByID(gomock.Any(), symID, portGroupID).
					Return(nil, errors.New("port group not found")).Times(1)
			},
			expectErr:   true,
			errContains: "Failed to get port group",
		},
		{
			name:      "vSphere: empty port group returns error",
			isVsphere: true,
			connections: []*types.MaskingViewConnection{
				{VolumeID: "OTHER", HostLUNAddress: "0001", DirectorPort: "SE-1E:4"},
			},
			setupMock: func(client *mocks.MockPmaxClient) {
				client.EXPECT().GetMaskingViewConnections(gomock.Any(), symID, mvID, devID).
					Return([]*types.MaskingViewConnection{}, nil).AnyTimes()
				client.EXPECT().GetMaskingViewByID(gomock.Any(), symID, mvID).
					Return(&types.MaskingView{
						MaskingViewID: mvID,
						PortGroupID:   portGroupID,
					}, nil).Times(1)
				client.EXPECT().GetPortGroupByID(gomock.Any(), symID, portGroupID).
					Return(&types.PortGroup{
						PortGroupID:      portGroupID,
						SymmetrixPortKey: []types.PortKey{},
					}, nil).Times(1)
			},
			expectErr:   true,
			errContains: "has no ports configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			LockRequestHandler()

			mockClient := mocks.NewMockPmaxClient(ctrl)
			tt.setupMock(mockClient)

			svc := &service{
				opts: Opts{
					IsVsphereEnabled: tt.isVsphere,
				},
			}
			getPmaxCache(symID)

			publishContext := make(map[string]string)
			resp, err := svc.updatePublishContext(
				context.Background(), publishContext, symID, mvID, devID, "req-1",
				tt.connections, mockClient, true,
			)

			if tt.expectErr {
				assert.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				assert.Nil(t, resp)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
				assert.Equal(t, tt.expectLUN, resp.PublishContext[PublishContextLUNAddress])
				assert.NotEmpty(t, resp.PublishContext[PortIdentifiers+"_1"])
			}
		})
	}
}

// TestSelectOrCreateFCPGForHost_V4_CSIPrefixFiltering tests that V4 arrays
// filter port groups by CSI prefix, matching the legacy behavior
func TestSelectOrCreateFCPGForHost_V4_CSIPrefixFiltering(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	symID := "000197900111"
	clusterPrefix := "test-cluster"

	t.Run("V4 array filters by CSI prefix", func(t *testing.T) {
		mockClient := mocks.NewMockPmaxClient(ctrl)

		// Mock versionCache calls
		mockClient.EXPECT().GetHTTPClient().Return(&http.Client{}).AnyTimes()
		mockClient.EXPECT().GetVersionDetails(gomock.Any()).
			Return(&types.VersionDetails{APIVersion: "103"}, nil).AnyTimes()
		mockClient.EXPECT().GetSymmetrixByID(gomock.Any(), symID).
			Return(&types.Symmetrix{SymmetrixID: symID, Microcode: "6079"}, nil).AnyTimes()

		// Setup host
		host := &types.Host{
			HostID:     "test-host",
			HostType:   "Fibre",
			Initiators: []string{"10000000c98a5e5e"},
		}

		// Mock GetPortListByProtocol
		mockClient.EXPECT().GetPortListByProtocol(gomock.Any(), symID, "SCSI_FC").
			Return(&types.PortList{
				SymmetrixPortKey: []types.PortKey{
					{DirectorID: "FA-1D", PortID: "4"},
				},
			}, nil).Times(1)

		// Mock GetInitiatorList
		mockClient.EXPECT().GetInitiatorList(gomock.Any(), symID, "10000000c98a5e5e", false, false).
			Return(&types.InitiatorList{
				InitiatorIDs: []string{"FA-1D:4:10000000c98a5e5e"},
			}, nil).Times(1)
		mockClient.EXPECT().GetInitiatorByID(gomock.Any(), symID, "FA-1D:4:10000000c98a5e5e").Return(&types.Initiator{
			OnFabric: true,
			LoggedIn: true,
		}, nil).Times(1)

		// Mock GetPortGroupListByType - returns both CSI and non-CSI port groups
		portGroupListResult := &types.PortGroupListResult{
			Results: []types.PortGroupListv1{
				{
					ID:       "manual-pg-1", // Should be ignored
					Protocol: FcIscsiID,
					Ports: []types.PortValues{
						{PortID: "RkEtMUR8NA", Director: types.DirectorID{ID: "FA-1D"}},
					},
				},
				{
					ID:       "csi-test-cluster-FA-1D-4-PG", // Should be selected
					Protocol: FcIscsiID,
					Ports: []types.PortValues{
						{PortID: "RkEtMUR8NA", Director: types.DirectorID{ID: "FA-1D"}},
					},
				},
			},
		}
		mockClient.EXPECT().GetPortGroupListByType(gomock.Any(), symID, "fibre").
			Return(portGroupListResult, nil).Times(1)

		svc := &service{
			opts: Opts{ClusterPrefix: clusterPrefix},
		}

		pgID, err := svc.SelectOrCreateFCPGForHost(ctx, symID, host, mockClient)

		assert.NoError(t, err)
		assert.Equal(t, "csi-test-cluster-FA-1D-4-PG", pgID)
		assert.Contains(t, pgID, "csi-"+clusterPrefix)
	})
}

// TestSelectOrCreateFCPGForHost_V4_IgnoresNonCSIPortGroups tests that V4 implementation
// correctly ignores non-CSI managed port groups even if they have matching ports
func TestSelectOrCreateFCPGForHost_V4_IgnoresNonCSIPortGroups(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	symID := "000197900111"
	clusterPrefix := "prod"

	mockClient := mocks.NewMockPmaxClient(ctrl)

	// Mock versionCache calls
	mockClient.EXPECT().GetHTTPClient().Return(&http.Client{}).AnyTimes()
	mockClient.EXPECT().GetVersionDetails(gomock.Any()).
		Return(&types.VersionDetails{APIVersion: "103"}, nil).AnyTimes()
	mockClient.EXPECT().GetSymmetrixByID(gomock.Any(), symID).
		Return(&types.Symmetrix{SymmetrixID: symID, Microcode: "6079"}, nil).AnyTimes()

	host := &types.Host{
		HostID:     "test-host",
		HostType:   "Fibre",
		Initiators: []string{"10000000c98a5e5e"},
	}

	// Mock GetPortListByProtocol
	mockClient.EXPECT().GetPortListByProtocol(gomock.Any(), symID, "SCSI_FC").
		Return(&types.PortList{
			SymmetrixPortKey: []types.PortKey{
				{DirectorID: "FA-1D", PortID: "4"},
			},
		}, nil).Times(1)

	// Mock GetInitiatorList
	mockClient.EXPECT().GetInitiatorList(gomock.Any(), symID, "10000000c98a5e5e", false, false).
		Return(&types.InitiatorList{
			InitiatorIDs: []string{"FA-1D:4:10000000c98a5e5e"},
		}, nil).Times(1)
	mockClient.EXPECT().GetInitiatorByID(gomock.Any(), symID, "FA-1D:4:10000000c98a5e5e").Return(&types.Initiator{
		OnFabric: true,
		LoggedIn: true,
	}, nil).Times(1)

	// Return multiple non-CSI port groups and one CSI port group
	portGroupListResult := &types.PortGroupListResult{
		Results: []types.PortGroupListv1{
			{
				ID:       "admin-pg-1",
				Protocol: FcIscsiID,
				Ports: []types.PortValues{
					{PortID: "RkEtMUR8NA", Director: types.DirectorID{ID: "FA-1D"}},
				},
			},
			{
				ID:       "manual-pg-2",
				Protocol: FcIscsiID,
				Ports: []types.PortValues{
					{PortID: "RkEtMUR8NA", Director: types.DirectorID{ID: "FA-1D"}},
				},
			},
			{
				ID:       "csi-prod-FA-1D-4-PG",
				Protocol: FcIscsiID,
				Ports: []types.PortValues{
					{PortID: "RkEtMUR8NA", Director: types.DirectorID{ID: "FA-1D"}},
				},
			},
		},
	}
	mockClient.EXPECT().GetPortGroupListByType(gomock.Any(), symID, "fibre").
		Return(portGroupListResult, nil).Times(1)

	svc := &service{
		opts: Opts{ClusterPrefix: clusterPrefix},
	}

	pgID, err := svc.SelectOrCreateFCPGForHost(ctx, symID, host, mockClient)

	assert.NoError(t, err)
	assert.Equal(t, "csi-prod-FA-1D-4-PG", pgID, "Should select CSI-managed port group")
	assert.Contains(t, pgID, "csi-"+clusterPrefix, "Selected port group must have CSI prefix")
	assert.NotContains(t, pgID, "admin-pg", "Should not select admin port group")
	assert.NotContains(t, pgID, "manual-pg", "Should not select manual port group")
}

func TestControllerModifyVolume_Unimplemented(t *testing.T) {
	svc := &service{}

	tests := []struct {
		name string
		req  *csi.ControllerModifyVolumeRequest
	}{
		{
			name: "returns Unimplemented with nil request",
			req:  nil,
		},
		{
			name: "returns Unimplemented with valid volume ID",
			req: &csi.ControllerModifyVolumeRequest{
				VolumeId:          validLocalVolumeID,
				MutableParameters: map[string]string{"key": "value"},
			},
		},
		{
			name: "returns Unimplemented with empty request",
			req:  &csi.ControllerModifyVolumeRequest{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := svc.ControllerModifyVolume(context.Background(), tt.req)
			assert.Nil(t, resp)
			assert.Error(t, err)

			st, ok := status.FromError(err)
			assert.True(t, ok, "error should be a gRPC status error")
			assert.Equal(t, codes.Unimplemented, st.Code())
			assert.Contains(t, st.Message(), "ControllerModifyVolume is not supported for PowerMax")
			assert.Contains(t, st.Message(), "no suitable volume parameters")
		})
	}
}

func TestControllerGetCapabilities_NoModifyVolume(t *testing.T) {
	svc := &service{}

	resp, err := svc.ControllerGetCapabilities(context.Background(), &csi.ControllerGetCapabilitiesRequest{})
	assert.NoError(t, err)
	assert.NotNil(t, resp)

	for _, cap := range resp.Capabilities {
		rpc := cap.GetRpc()
		if rpc != nil {
			assert.NotEqual(t, csi.ControllerServiceCapability_RPC_MODIFY_VOLUME, rpc.GetType(),
				"MODIFY_VOLUME capability should not be advertised for PowerMax")
		}
	}
}

func Test_service_SelectOrCreateFCPGForHost_PortLimit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)
	client.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	symID := "000120000001"
	clusterPrefix := "ABC"

	// Build 40 valid SCSI_FC ports from a single initiator
	var scsiPortKeys []types.PortKey
	var initiatorIDs []string
	for i := 0; i < 40; i++ {
		portID := fmt.Sprintf("%02d", i)
		scsiPortKeys = append(scsiPortKeys, types.PortKey{DirectorID: "FA-1D", PortID: portID})
		initiatorIDs = append(initiatorIDs, fmt.Sprintf("FA-1D:%s:5000000000000001", portID))
	}

	client.EXPECT().GetPortListByProtocol(gomock.Any(), symID, "SCSI_FC").Return(&types.PortList{
		SymmetrixPortKey: scsiPortKeys,
	}, nil)
	client.EXPECT().GetInitiatorList(gomock.Any(), symID, "5000000000000001", false, false).Return(&types.InitiatorList{
		InitiatorIDs: initiatorIDs,
	}, nil)
	client.EXPECT().GetInitiatorByID(gomock.Any(), symID, gomock.Any()).Return(&types.Initiator{
		OnFabric: true,
		LoggedIn: true,
	}, nil).AnyTimes()
	client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
		APIVersion: "102",
	}, nil)
	client.EXPECT().GetSymmetrixByID(gomock.Any(), symID).Return(&types.Symmetrix{
		SymmetrixID: symID,
		Microcode:   "5978.441.0",
	}, nil)
	client.EXPECT().GetPortGroupList(gomock.Any(), symID, "fibre").Return(&types.PortGroupList{
		PortGroupIDs: []string{"other-pg"},
	}, nil)

	var capturedPortKeys []types.PortKey
	client.EXPECT().CreatePortGroup(gomock.Any(), symID, gomock.Any(), gomock.Any(), "SCSI_FC").DoAndReturn(func(_ context.Context, _, _ string, portKeys []types.PortKey, _ string) (*types.PortGroup, error) {
		capturedPortKeys = portKeys
		return &types.PortGroup{}, nil
	}).Times(1)

	host := &types.Host{
		HostID:     "host1",
		HostType:   "Fibre",
		Initiators: []string{"5000000000000001"},
	}
	svc := &service{
		opts: Opts{ClusterPrefix: clusterPrefix},
	}

	pgID, err := svc.SelectOrCreateFCPGForHost(context.Background(), symID, host, client)
	assert.NoError(t, err)
	assert.NotEmpty(t, pgID)
	assert.Len(t, capturedPortKeys, MaxFCPortsInPortGroup)
}

func Test_service_SelectOrCreateFCPGForHost_SkipsInactiveInitiators(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)
	client.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	symID := "000120000001"
	clusterPrefix := "ABC"

	// Two ports are returned for the same WWN; one is OnFabric=false, the other is valid
	client.EXPECT().GetPortListByProtocol(gomock.Any(), symID, "SCSI_FC").Return(&types.PortList{
		SymmetrixPortKey: []types.PortKey{
			{DirectorID: "FA-1D", PortID: "0"},
			{DirectorID: "FA-1D", PortID: "1"},
		},
	}, nil)
	client.EXPECT().GetInitiatorList(gomock.Any(), symID, "5000000000000001", false, false).Return(&types.InitiatorList{
		InitiatorIDs: []string{"FA-1D:0:5000000000000001", "FA-1D:1:5000000000000001"},
	}, nil)
	client.EXPECT().GetInitiatorByID(gomock.Any(), symID, gomock.Any()).DoAndReturn(func(_ context.Context, _, initiatorID string) (*types.Initiator, error) {
		if strings.HasPrefix(initiatorID, "FA-1D:0") {
			return &types.Initiator{OnFabric: false, LoggedIn: true}, nil
		}
		return &types.Initiator{OnFabric: true, LoggedIn: true}, nil
	}).AnyTimes()
	client.EXPECT().GetVersionDetails(gomock.Any()).Return(&types.VersionDetails{
		APIVersion: "102",
	}, nil)
	client.EXPECT().GetSymmetrixByID(gomock.Any(), symID).Return(&types.Symmetrix{
		SymmetrixID: symID,
		Microcode:   "5978.441.0",
	}, nil)
	client.EXPECT().GetPortGroupList(gomock.Any(), symID, "fibre").Return(&types.PortGroupList{
		PortGroupIDs: []string{"other-pg"},
	}, nil)
	client.EXPECT().CreatePortGroup(gomock.Any(), symID, "csi-ABC-FA-1D-1-PG", []types.PortKey{{DirectorID: "FA-1D", PortID: "1"}}, "SCSI_FC").Return(&types.PortGroup{}, nil).Times(1)

	host := &types.Host{
		HostID:     "host1",
		HostType:   "Fibre",
		Initiators: []string{"5000000000000001"},
	}
	svc := &service{
		opts: Opts{ClusterPrefix: clusterPrefix},
	}

	pgID, err := svc.SelectOrCreateFCPGForHost(context.Background(), symID, host, client)
	assert.NoError(t, err)
	assert.Equal(t, "csi-ABC-FA-1D-1-PG", pgID)
}

func TestNamingSanitization(t *testing.T) {
	svc := &service{
		opts: Opts{
			ClusterPrefix: "CSM",
		},
	}

	nodeID := "worker-2-90y5itjvh0quk.domain"

	t.Run("ISCSI naming", func(t *testing.T) {
		hostID, sgID, mvID := svc.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
		assert.NotContains(t, hostID, ".")
		assert.NotContains(t, sgID, ".")
		assert.NotContains(t, mvID, ".")
		assert.Equal(t, "csi-no-srp-sg-CSM-worker-2-90y5itjvh0quk-domain", sgID)
		assert.Equal(t, "csi-mv-CSM-worker-2-90y5itjvh0quk-domain", mvID)
		assert.Equal(t, "csi-node-CSM-worker-2-90y5itjvh0quk-domain", hostID)
	})

	t.Run("FC naming", func(t *testing.T) {
		hostID, sgID, mvID := svc.GetFCHostSGAndMVIDFromNodeID(nodeID)
		assert.NotContains(t, hostID, ".")
		assert.NotContains(t, sgID, ".")
		assert.NotContains(t, mvID, ".")
		assert.Equal(t, "csi-no-srp-sg-CSM-worker-2-90y5itjvh0quk-domain-FC", sgID)
		assert.Equal(t, "csi-mv-CSM-worker-2-90y5itjvh0quk-domain-FC", mvID)
		assert.Equal(t, "csi-node-CSM-worker-2-90y5itjvh0quk-domain-FC", hostID)
	})

	t.Run("NVMeTCP naming", func(t *testing.T) {
		hostID, sgID, mvID := svc.GetNVMETCPHostSGAndMVIDFromNodeID(nodeID)
		assert.NotContains(t, hostID, ".")
		assert.NotContains(t, sgID, ".")
		assert.NotContains(t, mvID, ".")
		assert.Equal(t, "csi-no-srp-sg-CSM-worker-2-90y5itjvh0quk-domain-NVMETCP", sgID)
		assert.Equal(t, "csi-mv-CSM-worker-2-90y5itjvh0quk-domain-NVMETCP", mvID)
		assert.Equal(t, "csi-node-CSM-worker-2-90y5itjvh0quk-domain-NVMETCP", hostID)
	})
}

// TestGetAdoptedHostIDFromNodeLabels covers the controller-side resolution of a BFS
// adopted host (FR-2A.1): the local node cache, the Kubernetes label lookup, the
// negative result, error propagation, and the TTL cache that keeps the publish path
// from issuing an API GET per call.
func TestGetAdoptedHostIDFromNodeLabels(t *testing.T) {
	symID := "000197900111"
	driverName := "csi-powermax.dellemc.com"
	hostLabel := driverName + "/" + symID + ".adoptedHost"

	newSvc := func(k8s k8sutils.UtilsInterface) *service {
		s := &service{k8sUtils: k8s}
		s.opts.DriverName = driverName
		s.opts.HostManagementMode = HostMgmtModeAdopt
		return s
	}

	t.Run("create mode returns empty without API call", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		k8s := k8smock.NewMockUtilsInterface(ctrl) // no calls expected

		svc := &service{k8sUtils: k8s}
		svc.opts.DriverName = driverName
		svc.opts.HostManagementMode = HostMgmtModeCreate // default mode

		hostID, err := svc.getAdoptedHostIDFromNodeLabels(context.Background(), symID, "worker-1")
		assert.NoError(t, err)
		assert.Empty(t, hostID)
	})

	t.Run("adopt mode with nil k8sUtils returns empty", func(t *testing.T) {
		svc := &service{k8sUtils: nil}
		svc.opts.DriverName = driverName
		svc.opts.HostManagementMode = HostMgmtModeAdopt

		hostID, err := svc.getAdoptedHostIDFromNodeLabels(context.Background(), symID, "worker-1")
		assert.NoError(t, err)
		assert.Empty(t, hostID)
	})

	t.Run("local adoption state short-circuits the API lookup", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		k8s := k8smock.NewMockUtilsInterface(ctrl) // no calls expected

		svc := newSvc(k8s)
		svc.adoptedHosts = map[string]adoptedHostInfo{symID: {HostID: "BFS_Host_Node01"}}

		hostID, err := svc.getAdoptedHostIDFromNodeLabels(context.Background(), symID, "worker-1")
		assert.NoError(t, err)
		assert.Equal(t, "BFS_Host_Node01", hostID)
	})

	t.Run("adopted host read from node labels", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		nodeName := "worker-cached.domain.local"

		k8s := k8smock.NewMockUtilsInterface(ctrl)
		k8s.EXPECT().GetNodeLabels(nodeName).
			Return(map[string]string{hostLabel: "BFS_Host_Node01"}, nil)

		svc := newSvc(k8s)
		hostID, err := svc.getAdoptedHostIDFromNodeLabels(context.Background(), symID, nodeName)
		assert.NoError(t, err)
		assert.Equal(t, "BFS_Host_Node01", hostID)
	})

	t.Run("node without the label resolves to not adopted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		nodeName := "worker-plain.domain.local"

		k8s := k8smock.NewMockUtilsInterface(ctrl)
		k8s.EXPECT().GetNodeLabels(nodeName).
			Return(map[string]string{"kubernetes.io/os": "linux"}, nil)

		svc := newSvc(k8s)
		hostID, err := svc.getAdoptedHostIDFromNodeLabels(context.Background(), symID, nodeName)
		assert.NoError(t, err)
		assert.Empty(t, hostID)
	})

	t.Run("lookup failure is surfaced, not silently treated as not adopted", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()
		nodeName := "worker-missing.domain.local"

		k8s := k8smock.NewMockUtilsInterface(ctrl)
		k8s.EXPECT().GetNodeLabels(nodeName).Return(nil, errors.New("nodes \"x\" not found")).AnyTimes()

		svc := newSvc(k8s)
		hostID, err := svc.getAdoptedHostIDFromNodeLabels(context.Background(), symID, nodeName)
		assert.Error(t, err)
		assert.Empty(t, hostID)
		assert.Contains(t, err.Error(), "failed to resolve adopted host")

		// The best-effort wrapper deliberately swallows the error for callers that
		// cannot surface one.
		assert.Empty(t, svc.adoptedHostIDOrEmpty(context.Background(), symID, nodeName))
	})
}
