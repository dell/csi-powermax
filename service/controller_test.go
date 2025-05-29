/*
 Copyright © 2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/csi-powermax/v2/k8sutils"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	csiext "github.com/dell/dell-csi-extensions/replication"
	"github.com/dell/goiscsi"
	"github.com/dell/gonvme"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	gmock "go.uber.org/mock/gomock"
	"golang.org/x/net/context"
)

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
	pmaxTimeoutSeconds        int64
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
				requestLockFunc = func(_, _ string) int {
					return 0
				}
				releaseLockFunc = func(_, _ string, _ int) {}

				initDefaultClient()
				// local powermax has enough space to continue
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				// remote powermax does not have enough space and should trigger an error
				pmaxClient.EXPECT().GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").Times(1).Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
				pmaxClient.EXPECT().GetProtectedStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).Times(1).
					Return(&types.RDFStorageGroup{}, errors.New("failed to get storage group"))
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
				requestLockFunc = func(_, _ string) int {
					return 0
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
	}

	// initialize the client used by all these tests
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts:                      tt.fields.opts,
				mode:                      tt.fields.mode,
				pmaxTimeoutSeconds:        tt.fields.pmaxTimeoutSeconds,
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
				pmaxTimeoutSeconds:        tt.fields.pmaxTimeoutSeconds,
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
				pmaxTimeoutSeconds:        tt.fields.pmaxTimeoutSeconds,
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
				pmaxTimeoutSeconds:        tt.fields.pmaxTimeoutSeconds,
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
				pmaxTimeoutSeconds:        tt.fields.pmaxTimeoutSeconds,
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
				pmaxTimeoutSeconds:        tt.fields.pmaxTimeoutSeconds,
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
				pmaxTimeoutSeconds:        tt.fields.pmaxTimeoutSeconds,
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
				pmaxTimeoutSeconds:        tt.fields.pmaxTimeoutSeconds,
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
				pmaxTimeoutSeconds:        tt.fields.pmaxTimeoutSeconds,
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
			wantErr:             errors.New("Failed to fetch host id from array for node: node1"),
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
			got, err := s.IsNodeNVMe(context.Background(), tt.symID, tt.nodeID, pmaxClient)

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
						}, nil)

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
						}, nil)

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

		resp, err := s.ControllerUnpublishVolume(ctx, req)
		assert.Empty(t, resp)
		assert.Nil(t, err)

		req.VolumeId = s.createCSIVolumeID("", "validVolume", "0001:0001", "1:1")
		resp, err = s.ControllerUnpublishVolume(ctx, req)
		assert.Nil(t, err)
		assert.Empty(t, resp)
	})
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
