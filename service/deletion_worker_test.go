/*
 Copyright © 2024 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"sync"
	"testing"
	"time"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

func TestCaseUnlinkTarget(t *testing.T) {
	tests := []struct {
		name       string
		tgtVol     *types.Volume
		symID      string
		snapID     string
		pmaxClient pmax.Pmax
		expected   error
	}{
		{
			name: "Successful unlink",
			tgtVol: &types.Volume{
				VolumeID: "vol-12345",
			},
			symID:  "123",
			snapID: "",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetVolumeSnapInfo(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(&types.SnapshotVolumeGeneration{
					VolumeSnapshotLink: []types.VolumeSnapshotLink{
						{
							SnapshotName: "snapshot-123",
							Defined:      true,
							LinkSource:   "source-123",
						},
					},
				}, nil)
				client.EXPECT().ModifySnapshotS(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(nil)
				return client
			}(),
			expected: nil,
		},
		{
			name: "Failed to find snapshot info",
			tgtVol: &types.Volume{
				VolumeID: "vol-12345",
			},
			symID:  "123",
			snapID: "",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetVolumeSnapInfo(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(nil, errors.New("error"))
				return client
			}(),
			expected: errors.New("failed to find snapshot info for tgt vol. error: error"),
		},
		{
			name: "Can't unlink as link state is not defined",
			tgtVol: &types.Volume{
				VolumeID: "vol-12345",
			},
			symID:  "",
			snapID: "snapshot-123",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				return client
			}(),
			expected: errors.New("can't unlink as link state is not defined. retry after sometime"),
		},
		{
			name:     "Target volume is nil",
			tgtVol:   nil,
			expected: errors.New("target volume details can't be nil"),
		},
		{
			name: "Error calling ModifySnapshotS",
			tgtVol: &types.Volume{
				VolumeID: "vol-12345",
			},
			symID:  "123",
			snapID: "",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetVolumeSnapInfo(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(&types.SnapshotVolumeGeneration{
					VolumeSnapshotLink: []types.VolumeSnapshotLink{
						{
							SnapshotName: "snapshot-123",
							Defined:      true,
							LinkSource:   "source-123",
						},
					},
				}, nil)
				client.EXPECT().ModifySnapshotS(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(errors.New("error"))
				return client
			}(),
			expected: errors.New("failed to unlink snapshot. error: error"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := unlinkTarget(tc.tgtVol, tc.snapID, "0", tc.symID, 0, tc.pmaxClient)
			assert.Equal(t, tc.expected, err)
		})
	}
}

func TestUnlinkTargetsAndTerminateSnapshot(t *testing.T) {
	tests := []struct {
		name       string
		srcVol     *types.Volume
		symID      string
		pmaxClient pmax.Pmax
		expected   error
	}{
		{
			name: "Successful unlink",
			srcVol: &types.Volume{
				VolumeID: "vol-12345",
			},
			symID: "123",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetVolumeSnapInfo(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.SnapshotVolumeGeneration{
					VolumeSnapshotSource: []types.VolumeSnapshotSource{
						{
							SnapshotName: "DEL-snapshot-123",
							Generation:   1,
							LinkedVolumes: []types.LinkedVolumes{
								{
									TargetDevice: "target-123",
									Defined:      true,
								},
							},
						},
						{
							SnapshotName: "DEL-snapshot-456",
							Generation:   2,
							LinkedVolumes: []types.LinkedVolumes{
								{
									TargetDevice: "target-456",
									Defined:      true,
								},
							},
						},
					},
				}, nil)
				client.EXPECT().ModifySnapshotS(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
				client.EXPECT().DeleteSnapshotS(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
				return client
			}(),
			expected: nil,
		},
		{
			name: "Successful unlink with undefined links",
			srcVol: &types.Volume{
				VolumeID: "vol-12345",
			},
			symID: "123",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetVolumeSnapInfo(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(&types.SnapshotVolumeGeneration{
					VolumeSnapshotSource: []types.VolumeSnapshotSource{
						{
							SnapshotName: "DEL-snapshot-123",
							Generation:   1,
							LinkedVolumes: []types.LinkedVolumes{
								{
									TargetDevice: "target-123",
									Defined:      false,
								},
							},
						},
					},
				}, nil)
				client.EXPECT().DeleteSnapshotS(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(nil)
				return client
			}(),
			expected: nil,
		},
		{
			name: "Error calling GetVolumeSnapshotInfo",
			srcVol: &types.Volume{
				VolumeID: "vol-12345",
			},
			symID: "123",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetVolumeSnapInfo(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(nil, errors.New("error"))
				return client
			}(),
			expected: errors.New("error"),
		},
		{
			name: "Error calling ModifySnapshotS",
			srcVol: &types.Volume{
				VolumeID: "vol-12345",
			},
			symID: "123",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetVolumeSnapInfo(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.SnapshotVolumeGeneration{
					VolumeSnapshotSource: []types.VolumeSnapshotSource{
						{
							SnapshotName: "DEL-snapshot-123",
							Generation:   1,
							LinkedVolumes: []types.LinkedVolumes{
								{
									TargetDevice: "target-123",
									Defined:      true,
								},
							},
						},
					},
				}, nil)
				client.EXPECT().ModifySnapshotS(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(errors.New("error"))
				return client
			}(),
			expected: errors.New("failed to unlink snapshot. error: error"),
		},
		{
			name: "Failied calling DeleteSnapshotS",
			srcVol: &types.Volume{
				VolumeID: "vol-12345",
			},
			symID: "123",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetVolumeSnapInfo(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.SnapshotVolumeGeneration{
					VolumeSnapshotSource: []types.VolumeSnapshotSource{
						{
							SnapshotName: "DEL-snapshot-123",
							Generation:   1,
							LinkedVolumes: []types.LinkedVolumes{
								{
									TargetDevice: "target-123",
									Defined:      true,
								},
							},
						},
					},
				}, nil)
				client.EXPECT().ModifySnapshotS(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
				client.EXPECT().DeleteSnapshotS(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(errors.New("error"))
				return client
			}(),
			expected: errors.New("failed to terminate the snapshot. error: error"),
		},
		{
			name: "Ignoring snapshot as it can't be deleted by the deletion worker",
			srcVol: &types.Volume{
				VolumeID: "vol-12345",
			},
			symID: "123",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetVolumeSnapInfo(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.SnapshotVolumeGeneration{
					VolumeSnapshotSource: []types.VolumeSnapshotSource{
						{
							SnapshotName: "normal-snapshot-123",
							Generation:   1,
							LinkedVolumes: []types.LinkedVolumes{
								{
									TargetDevice: "target-123",
									Defined:      true,
								},
							},
						},
					},
				}, nil)
				return client
			}(),
			expected: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := unlinkTargetsAndTerminateSnapshot(tc.srcVol, tc.symID, tc.pmaxClient)
			assert.Equal(t, tc.expected, err)
		})
	}
}

func TestIsValid(t *testing.T) {
	tests := []struct {
		name     string
		req      deletionRequest
		expected error
	}{
		{
			name: "Valid request",
			req: deletionRequest{
				DeviceID:     "123",
				VolumeHandle: "_VOL_DEL_123",
				SymID:        "456",
			},
			expected: nil,
		},
		{
			name: "Invalid request - empty device ID",
			req: deletionRequest{
				DeviceID:     "",
				VolumeHandle: "_VOL_123",
				SymID:        "456",
			},
			expected: fmt.Errorf("device id can't be empty"),
		},
		{
			name: "Invalid request - empty sym ID",
			req: deletionRequest{
				DeviceID:     "123",
				VolumeHandle: "_VOL_123",
				SymID:        "",
			},
			expected: fmt.Errorf("sym id can't be empty"),
		},
		{
			name: "Invalid request - device ID without prefix",
			req: deletionRequest{
				DeviceID:     "123",
				VolumeHandle: "_INV_123",
				SymID:        "456",
			},
			expected: fmt.Errorf("device id doesn't contain the cluster prefix"),
		},
		{
			name: "Invalid request - device ID not marked for deletion",
			req: deletionRequest{
				DeviceID:     "123",
				VolumeHandle: "_VOL_123",
				SymID:        "456",
			},
			expected: fmt.Errorf("device has not been marked for deletion"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.req.isValid("_VOL")
			if (err == nil && tt.expected != nil) || (err != nil && tt.expected == nil) || (err != nil && tt.expected != nil && err.Error() != tt.expected.Error()) {
				t.Errorf("%s: expected error '%v', got '%v'", tt.name, tt.expected, err)
			}
		})
	}
}

func TestCleanupSnapshots(t *testing.T) {
	tests := []struct {
		name       string
		deviceList []*csiDevice
		symID      string
		pmaxClient pmax.Pmax
		expected   bool
	}{
		{
			name: "No devices in the queue",
			deviceList: []*csiDevice{
				{},
			},
			symID:      "symID1",
			pmaxClient: mocks.NewMockPmaxClient(gomock.NewController(t)),
			expected:   false,
		},
		{
			name: "Successful unlink of a source device",
			deviceList: []*csiDevice{
				{
					SymDeviceID: symDeviceID{
						DeviceID: "vol-123",
					},
					Status: deletionRequestStatus{
						State: deletionStateCleanupSnaps,
					},
				},
			},
			symID: "symID1",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID:   "vol-123",
					SnapTarget: true,
				}, nil)
				client.EXPECT().GetVolumeSnapInfo(gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(&types.SnapshotVolumeGeneration{
					VolumeSnapshotLink: []types.VolumeSnapshotLink{
						{
							SnapshotName: "snapshot-123",
							Defined:      true,
							LinkSource:   "source-123",
						},
					},
				}, nil)
				client.EXPECT().ModifySnapshotS(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).Return(nil)
				return client
			}(),
			expected: true,
		},
		{
			name: "Error calling GetVolumeByID",
			deviceList: []*csiDevice{
				{
					SymDeviceID: symDeviceID{
						DeviceID: "vol-123",
					},
					Status: deletionRequestStatus{
						State: deletionStateCleanupSnaps,
					},
				},
			},
			symID: "symID1",
			pmaxClient: func() pmax.Pmax {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, errors.New("error"))
				return client
			}(),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			queue := &deletionQueue{
				DeviceList: tt.deviceList,
				SymID:      tt.symID,
			}
			result := queue.cleanupSnapshots(tt.pmaxClient)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestUpdateStatus(t *testing.T) {
	tests := []struct {
		name     string
		device   *csiDevice
		state    string
		errorMsg string
		expected error
	}{
		{
			name:     "Valid update",
			device:   &csiDevice{Status: deletionRequestStatus{State: deletionStateCleanupSnaps}},
			state:    deletionStateDeleteVol,
			errorMsg: "",
			expected: nil,
		},
		{
			name:     "Invalid update - error message",
			device:   &csiDevice{Status: deletionRequestStatus{State: deletionStateCleanupSnaps}},
			state:    deletionStateCleanupSnaps,
			errorMsg: "some error",
			expected: fmt.Errorf("some error"),
		},
		{
			name: "Invalid update - max error count reached",
			device: &csiDevice{
				Status: deletionRequestStatus{
					State: deletionStateCleanupSnaps,
					ErrorMsgs: []string{
						"error 1", "error 2", "error 3", "error 4", "error 5",
					},
					nErrors: MaxErrorCount,
				},
			},
			state:    deletionStateCleanupSnaps,
			errorMsg: "some error",
			expected: fmt.Errorf("some error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.device.updateStatus(tt.state, tt.errorMsg)
		})
	}
}

func TestDeleteVolumes(t *testing.T) {
	tests := []struct {
		name           string
		deletionQueue  *deletionQueue
		pmaxClient     pmax.Pmax
		expectedResult bool
	}{
		{
			name: "Successful deletion",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDeleteVol,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{
							volume: &types.Volume{
								VolumeID: "vol-123",
							},
						},
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID: "vol-123",
				}, nil)
				pmaxClient.EXPECT().DeleteVolume(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
				return pmaxClient
			}(),
			expectedResult: true,
		},
		{
			name: "Error calling GetVolumeByID",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDeleteVol,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{
							volume: &types.Volume{
								VolumeID: "vol-123",
							},
						},
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, errors.New("error"))
				return pmaxClient
			}(),
			expectedResult: false,
		},
		{
			name: "Error due to volume belongs to storage group",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDeleteVol,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{
							volume: &types.Volume{
								VolumeID: "vol-123",
							},
						},
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID:           "vol-123",
					StorageGroupIDList: []string{"sg-1"},
				}, nil)
				return pmaxClient
			}(),
			expectedResult: false,
		},
		{
			name: "Error due to volume being deleted not the same as originally requested",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDeleteVol,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{
							volume: &types.Volume{
								VolumeID: "vol-123",
							},
						},
						VolumeIdentifier: "vol-identifier-1",
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID:           "vol-123",
					StorageGroupIDList: []string{"sg-1"},
					VolumeIdentifier:   "vol-identifier-2",
				}, nil)
				return pmaxClient
			}(),
			expectedResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.deletionQueue.deleteVolumes(tt.pmaxClient)
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestCaseGetDevice(t *testing.T) {
	csidev := getDevice("1234", []*csiDevice{{}, {}})
	var expectedoutput *csiDevice
	expectedoutput = nil
	assert.Equal(t, expectedoutput, csidev)
}

func TestCaseGetDeviceRanges(t *testing.T) {
	sdID1 := symDeviceID{
		DeviceID: "1234",
		IntVal:   1,
	}
	sdID2 := symDeviceID{
		DeviceID: "2345",
		IntVal:   2,
	}
	expectedOutput := []deviceRange{
		{
			Start: sdID1,
			End:   sdID2,
		},
	}
	devicerang := getDeviceRanges([]symDeviceID{{DeviceID: "1234", IntVal: 1}, {DeviceID: "2345", IntVal: 2}})
	assert.Equal(t, expectedOutput, devicerang)
}

func TestCaseQueueDeviceForDeletion(t *testing.T) {
	sdID1 := symDeviceID{
		DeviceID: "1234",
		IntVal:   2,
	}
	csiDev := csiDevice{
		SymDeviceID:      sdID1,
		SymID:            "123",
		VolumeIdentifier: "123",
	}
	DeviceList1 := []*csiDevice{&csiDev, &csiDev}
	deletionQueuevalue := deletionQueue{
		DeviceList: DeviceList1,
		SymID:      "123",
	}
	err := deletionQueuevalue.QueueDeviceForDeletion(csiDev)
	expectederror := fmt.Errorf("%s", fmt.Sprintf("%s: found existing entry in deletion queue with volume handle: %s, added at: %v",
		csiDev.print(), csiDev.VolumeIdentifier, csiDev.Status.AdditionTime))
	assert.Equal(t, expectederror, err)
}

func TestQueueDeviceForDeletion(t *testing.T) {
	tests := []struct {
		name             string
		worker           *deletionWorker
		devID            string
		volumeIdentifier string
		symID            string
		expected         error
	}{
		{
			name:             "Deletion request queue full",
			worker:           &deletionWorker{},
			devID:            "dev-123",
			volumeIdentifier: "vol-123",
			symID:            "sym-123",
			expected:         errors.New("deletion request queue full. retry after sometime"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.worker.QueueDeviceForDeletion(tt.devID, tt.volumeIdentifier, tt.symID)
			assert.Equal(t, tt.expected, err)
		})
	}
}

func TestRemoveVolumesFromStorageGroup(t *testing.T) {
	tests := []struct {
		name           string
		deletionQueue  *deletionQueue
		pmaxClient     pmax.Pmax
		expectedResult bool
	}{
		{
			name: "Successful removal",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDisAssociateSG,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{},
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID:           "vol-123",
					StorageGroupIDList: []string{"csi-rep-sg-namespace-rdf-METRO"},
				}, nil)
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroup{
					NumOfMaskingViews: 0,
				}, nil)
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroupRDFG{
					States: []string{"Consistent", "Synchronized"},
					VolumeRdfTypes: []string{
						"R1",
					},
				}, nil)
				pmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.RDFGroup{
					NumDevices: 1,
				}, nil)
				pmaxClient.EXPECT().RemoveVolumesFromProtectedStorageGroup(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroup{}, nil)
				pmaxClient.EXPECT().ExecuteReplicationActionOnSG(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
				return pmaxClient

			}(),
			expectedResult: true,
		},
		{
			name: "Error calling GetStorageGroupRDFInfo",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDisAssociateSG,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{},
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID:           "vol-123",
					StorageGroupIDList: []string{"csi-rep-sg-namespace-rdf-METRO"},
				}, nil)
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroup{
					NumOfMaskingViews: 0,
				}, nil)
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, errors.New("error"))
				return pmaxClient

			}(),
			expectedResult: false,
		},
		{
			name: "Error calling GetStorageGroupRDFInfo - No SRDF records found",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDisAssociateSG,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{},
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID:           "vol-123",
					StorageGroupIDList: []string{"csi-rep-sg-namespace-rdf-METRO"},
				}, nil)
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroup{
					NumOfMaskingViews: 0,
				}, nil)
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, errors.New("No SRDF records found"))
				return pmaxClient

			}(),
			expectedResult: true,
		},
		{
			name: "Skip removing volume from protected SG from R2 type",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDisAssociateSG,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{},
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID:           "vol-123",
					StorageGroupIDList: []string{"csi-rep-sg-namespace-rdf-METRO"},
				}, nil)
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroup{
					NumOfMaskingViews: 0,
				}, nil)
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroupRDFG{
					States: []string{"Consistent", "Synchronized"},
					VolumeRdfTypes: []string{
						"R2",
					},
				}, nil)
				return pmaxClient

			}(),
			expectedResult: true,
		},
		{
			name: "No Storage Group ID List Returned",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDisAssociateSG,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{},
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID:           "vol-123",
					StorageGroupIDList: []string{}, //empty list
				}, nil)
				return pmaxClient

			}(),
			expectedResult: false,
		},
		{
			name: "No Storage Group ID List Returned - Volume is Snapsource",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDisAssociateSG,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{},
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID:           "vol-123",
					StorageGroupIDList: []string{}, //empty list
					SnapSource:         true,
				}, nil)
				return pmaxClient

			}(),
			expectedResult: false,
		},
		{
			name: "Failure calling GetRDFGroupByID",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDisAssociateSG,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{},
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID:           "vol-123",
					StorageGroupIDList: []string{"csi-rep-sg-namespace-rdf-METRO"},
				}, nil)
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroup{
					NumOfMaskingViews: 0,
				}, nil)
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroupRDFG{
					States: []string{"Consistent", "Synchronized"},
					VolumeRdfTypes: []string{
						"R1",
					},
				}, nil)
				pmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, errors.New("error"))
				return pmaxClient
			}(),
			expectedResult: false,
		},
		{
			name: "Unable to remove due to masking views",
			deletionQueue: &deletionQueue{
				DeviceList: []*csiDevice{
					{
						SymID: "123",
						Status: deletionRequestStatus{
							State: deletionStateDisAssociateSG,
						},
						SymDeviceID: symDeviceID{
							DeviceID: "123",
							IntVal:   123,
						},
						SymVolumeCache: symVolumeCache{},
					},
				},
				lock: sync.Mutex{},
			},
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.Volume{
					VolumeID:           "vol-123",
					StorageGroupIDList: []string{"csi-rep-sg-namespace-rdf-metro"},
				}, nil)
				pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroup{
					NumOfMaskingViews: 1,
				}, nil)
				return pmaxClient
			}(),
			expectedResult: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			oldSyncInProgTime := WaitTillSyncInProgTime
			defer func() { WaitTillSyncInProgTime = oldSyncInProgTime }()
			WaitTillSyncInProgTime = 1 * time.Millisecond
			result := tc.deletionQueue.removeVolumesFromStorageGroup(tc.pmaxClient)
			assert.Equal(t, tc.expectedResult, result)
		})
	}
}
