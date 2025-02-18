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
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/csi-powermax/v2/k8sutils"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	csiext "github.com/dell/dell-csi-extensions/replication"
	"github.com/dell/goiscsi"
	"github.com/dell/gonvme"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"golang.org/x/net/context"
)

const (
	KiB int64 = 1024
	KB  int64 = 1000

	GiB int64 = 1024 * KiB
	GB  int64 = KB * 1000

	TiB int64 = 1024 * GiB
	TB  int64 = GB * 1000
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
			want:       nil,
			wantErr:    true,
			wantErrMsg: "",
		},
		// {
		// 	name:   "fail to validate volume size",
		// 	fields: fields{},
		// 	args: args{
		// 		ctx: context.Background(),
		// 		req: &csi.CreateVolumeRequest{
		// 			AccessibilityRequirements: &csi.TopologyRequirement{},
		// 			CapacityRange: &csi.CapacityRange{
		// 				RequiredBytes: 3 * GiB,
		// 				LimitBytes:    2 * GiB,
		// 			},
		// 		},
		// 		symID:       "0000000000001",
		// 		remoteSymID: "0000000000002",
		// 	},
		// 	want:       nil,
		// 	wantErr:    true,
		// 	wantErrMsg: "",
		// },
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
			got, err := s.createMetroVolume(tt.args.ctx, tt.args.req, tt.args.reqID, tt.args.storagePoolID, tt.args.symID, tt.args.storageGroupName, tt.args.serviceLevel, tt.args.thick, tt.args.remoteSymID, tt.args.localRDFGrpNo, tt.args.remoteRDFGrpNo, tt.args.remoteServiceLevel, tt.args.remoteSRPID, tt.args.namespace, tt.args.applicationPrefix, tt.args.bias, tt.args.hostLimitName, tt.args.hostMBsec, tt.args.hostIOsec, tt.args.hostDynDist)
			if (err != nil) != tt.wantErr {
				t.Errorf("service.createMetroVolume() error = %v, wantErr %v", err, tt.wantErr)
				return
			} else {
				if tt.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tt.wantErrMsg)
				}
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
				c.EXPECT().GetStoragePool(gomock.All(), "0000000000001", "pool1").AnyTimes().Return(&types.StoragePool{
					FbaCap: &types.FbaCap{},
					CkdCap: &types.CkdCap{},
				}, nil)

				return c
			},
			fields: serviceFields{},
			args: args{
				ctx:           context.Background(),
				symmetrixID:   "0000000000001",
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
				c.EXPECT().GetStoragePool(gomock.All(), "0000000000001", "pool1").AnyTimes().Return(&types.StoragePool{}, nil)

				return c
			},
			fields: serviceFields{},
			args: args{
				ctx:           context.Background(),
				symmetrixID:   "0000000000001",
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
				symmetrixID:   "0000000000001",
				storagePoolID: "pool1",
			},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gomock.NewController(t))
				c.EXPECT().GetStoragePool(gomock.Any(), "0000000000001", "pool1").Return(&types.StoragePool{}, errors.New("error"))

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
				symmetrixID:   "0000000000001",
				storagePoolID: "pool1",
			},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gomock.NewController(t))
				c.EXPECT().GetStoragePool(gomock.Any(), "0000000000001", "pool1").Return(&types.StoragePool{
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
				symmetrixID:   "0000000000001",
				storagePoolID: "pool1",
			},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gomock.NewController(t))
				c.EXPECT().GetStoragePool(gomock.Any(), "0000000000001", "pool1").Return(&types.StoragePool{
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
				symID:      "0000000000001",
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
				symID: "0000000000001",
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
