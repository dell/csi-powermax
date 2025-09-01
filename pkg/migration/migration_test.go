/*
 *
 * Copyright © 2021-2024 Dell Inc. or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

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

package migration

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestStorageGroupCommit(t *testing.T) {
	tests := []struct {
		name                        string
		symID                       string
		action                      string
		getSGMigrationResponse      *types.MigrationStorageGroups
		getSGMigrationError         error
		getSGMigrationByIDResponse  *types.MigrationSession
		getSGMigrationByIDError     error
		modifyMigrationSessionError error
		expectedResult              bool
		expectedError               error
	}{
		{
			name:   "Successful commit",
			symID:  "000000000001",
			action: "csimgr.ActionTypes_MG_COMMIT",
			getSGMigrationResponse: &types.MigrationStorageGroups{
				MigratingNameList:  []string{"name-1"},
				StorageGroupIDList: []string{"000000000001"},
			},
			getSGMigrationByIDResponse: &types.MigrationSession{
				State: "Synchronized",
			},
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name:   "Failed commit",
			symID:  "000000000001",
			action: "csimgr.ActionTypes_MG_COMMIT",
			getSGMigrationResponse: &types.MigrationStorageGroups{
				MigratingNameList:  []string{"name-1"},
				StorageGroupIDList: []string{"000000000001"},
			},
			getSGMigrationByIDResponse: &types.MigrationSession{
				State: "Not Synchronized",
			},
			expectedResult: false,
			expectedError:  status.Errorf(codes.Internal, "Waiting for SG to be in SYNC state, current state of SG %s is %s", "name-1", "Not Synchronized"),
		},
		{
			name:                "Error in getting storage group migration",
			symID:               "000000000001",
			action:              "csimgr.ActionTypes_MG_COMMIT",
			getSGMigrationError: status.Errorf(codes.Internal, "error calling storage group migration"),
			expectedResult:      false,
			expectedError:       status.Errorf(codes.Internal, "error calling storage group migration"),
		},
		{
			name:                   "All migration completed",
			symID:                  "000000000001",
			action:                 "csimgr.ActionTypes_MG_COMMIT",
			getSGMigrationResponse: &types.MigrationStorageGroups{},
			expectedResult:         true,
			expectedError:          nil,
		},
		{
			name:   "Error calling get storage group migration by id",
			symID:  "000000000001",
			action: "csimgr.ActionTypes_MG_COMMIT",
			getSGMigrationResponse: &types.MigrationStorageGroups{
				MigratingNameList:  []string{"name-1"},
				StorageGroupIDList: []string{"000000000001"},
			},
			getSGMigrationByIDError: errors.New("some error"),
			expectedResult:          false,
			expectedError:           status.Errorf(codes.Internal, "error getting Migration session for SG %s on sym %s: %s", "name-1", "000000000001", "some error"),
		},
		{
			name:   "Error calling modify migration session",
			symID:  "000000000001",
			action: "csimgr.ActionTypes_MG_COMMIT",
			getSGMigrationResponse: &types.MigrationStorageGroups{
				MigratingNameList:  []string{"name-1"},
				StorageGroupIDList: []string{"000000000001"},
			},
			getSGMigrationByIDResponse: &types.MigrationSession{
				State: "Synchronized",
			},
			modifyMigrationSessionError: errors.New("some error"),
			expectedResult:              false,
			expectedError:               status.Errorf(codes.Internal, "error modifying Migration session for SG %s on sym %s: %s", "name-1", "000000000001", "some error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
			pmaxClient.EXPECT().GetStorageGroupMigration(gomock.Any(), tt.symID).AnyTimes().Return(tt.getSGMigrationResponse, tt.getSGMigrationError)
			pmaxClient.EXPECT().GetStorageGroupMigrationByID(gomock.Any(), tt.symID, gomock.Any()).AnyTimes().Return(tt.getSGMigrationByIDResponse, tt.getSGMigrationByIDError)
			pmaxClient.EXPECT().ModifyMigrationSession(gomock.Any(), tt.symID, tt.action, gomock.Any()).AnyTimes().Return(tt.modifyMigrationSessionError)

			result, err := StorageGroupCommit(context.Background(), tt.symID, tt.action, pmaxClient)
			if result != tt.expectedResult {
				t.Errorf("Expected result %v, got %v", tt.expectedResult, result)
			}
			if !errors.Is(err, tt.expectedError) {
				t.Errorf("Expected error %v, got %v", tt.expectedError, err)
			}
		})
	}
}

func TestAddVolumesToRemoteSG(t *testing.T) {
	tests := []struct {
		name            string
		remoteSymID     string
		sgToRemoteVols  map[string][]string
		expectedResult  bool
		addVolumesError error
		expectedError   error
	}{
		{
			name:           "Successful addition",
			remoteSymID:    "000000000001",
			sgToRemoteVols: map[string][]string{"csi-ABC-local-sg": {"0002D", "00031"}},
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name:           "No volumes to add",
			remoteSymID:    "000000000001",
			sgToRemoteVols: map[string][]string{},
			expectedResult: false,
			expectedError:  nil,
		},
		{
			name:            "Failed addition",
			remoteSymID:     "000000000001",
			sgToRemoteVols:  map[string][]string{"csi-ABC-local-sg": {"0002D", "00031"}},
			expectedResult:  false,
			addVolumesError: errors.New("some error"),
			expectedError:   status.Errorf(codes.Internal, "Could not add volume in SG on R2: %s: %s", "000000000001", "some error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
			pmaxClient.EXPECT().AddVolumesToStorageGroupS(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.addVolumesError)
			SGToRemoteVols = tt.sgToRemoteVols
			defer func() {
				SGToRemoteVols = map[string][]string{}
			}()
			result, err := AddVolumesToRemoteSG(context.Background(), tt.remoteSymID, pmaxClient)
			if result != tt.expectedResult {
				t.Errorf("Expected result %v, got %v", tt.expectedResult, result)
			}
			if !errors.Is(err, tt.expectedError) {
				t.Errorf("Expected error %v, got %v", tt.expectedError, err)
			}
		})
	}
}

func TestGetOrCreateMigrationEnvironment(t *testing.T) {
	tests := []struct {
		name                    string
		localSymID              string
		remoteSymID             string
		getMigrationResponse    *types.MigrationEnv
		getMigrationError       error
		createMigrationResponse *types.MigrationEnv
		createMigrationError    error
		expectedResult          *types.MigrationEnv
		expectedError           error
	}{
		{
			name:                 "Successful retrieval",
			localSymID:           "000000000001",
			remoteSymID:          "000000000001",
			getMigrationResponse: &types.MigrationEnv{},
			getMigrationError:    nil,
			expectedResult:       &types.MigrationEnv{},
			expectedError:        nil,
		},
		{
			name:                    "Failed retrieval but Successful creation",
			localSymID:              "000000000001",
			remoteSymID:             "000000000001",
			getMigrationError:       errors.New("some error"),
			createMigrationError:    nil,
			createMigrationResponse: &types.MigrationEnv{},
			expectedResult:          &types.MigrationEnv{},
			expectedError:           nil,
		},
		{
			name:                 "Failed retrieval and Failed creation",
			localSymID:           "000000000001",
			remoteSymID:          "000000000001",
			getMigrationError:    errors.New("retrieval error"),
			createMigrationError: status.Errorf(codes.Internal, "abc"),
			expectedError:        status.Errorf(codes.Internal, "abc"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
			pmaxClient.EXPECT().GetMigrationEnvironment(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getMigrationResponse, tt.getMigrationError)
			pmaxClient.EXPECT().CreateMigrationEnvironment(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.createMigrationResponse, tt.createMigrationError)
			result, err := GetOrCreateMigrationEnvironment(context.Background(), tt.localSymID, tt.remoteSymID, pmaxClient)
			if !reflect.DeepEqual(result, tt.expectedResult) {
				t.Errorf("Expected result %v, got %v", tt.expectedResult, result)
			}
			if !errors.Is(err, tt.expectedError) {
				t.Errorf("Expected error %v, got %v", tt.expectedError, err)
			}
		})
	}
}

func TestStorageGroupMigration(t *testing.T) {
	tests := []struct {
		name          string
		symID         string
		remoteSymID   string
		clusterPrefix string

		localSGVolumeList map[string]*types.VolumeIterator
		SGToRemoteVols    map[string][]string

		getLocalStorageGroupIDListResponse *types.StorageGroupIDList
		getLocalStorageGroupIDListError    error

		getRemoteStorageGroupIDListResponse *types.StorageGroupIDList
		getRemoteStorageGroupIDListError    error

		getVolumesInStorageGroupIteratorResponse *types.VolumeIterator
		getVolumesInStorageGroupIteratorError    error

		removeVolumesFromStorageGroupError error

		getStorageGroupResponse *types.StorageGroup
		getStorageGroupError    error

		getSGMigrationResponse *types.MigrationSession
		getSGMigrationError    error

		createSGMigrationResponse *types.MigrationSession
		createSGMigrationError    error

		createStorageGroupError error

		expectedResult bool
		expectedError  error
	}{
		{
			name:          "Successful migration",
			symID:         "000000000001",
			remoteSymID:   "000000000002",
			clusterPrefix: "cluster",
			localSGVolumeList: map[string]*types.VolumeIterator{
				"sg-1": {
					ResultList: types.VolumeResultList{VolumeList: []types.VolumeIDList{
						{VolumeIDs: "vol-1,vol-2"},
					}},
				},
			},
			SGToRemoteVols:                      map[string][]string{"sg-1": {"vol-1", "vol-2"}},
			getLocalStorageGroupIDListResponse:  &types.StorageGroupIDList{StorageGroupIDs: []string{"sg-1"}},
			getRemoteStorageGroupIDListResponse: &types.StorageGroupIDList{StorageGroupIDs: []string{"sg-1"}},
			getVolumesInStorageGroupIteratorResponse: &types.VolumeIterator{
				Count: 2,
				ResultList: types.VolumeResultList{
					VolumeList: []types.VolumeIDList{
						{
							VolumeIDs: "vol-1,vol-2",
						},
					},
				},
			},
			getStorageGroupResponse: &types.StorageGroup{NumOfVolumes: 2},
			getSGMigrationResponse: &types.MigrationSession{
				State: "Synchronized",
			},
			createSGMigrationResponse: &types.MigrationSession{
				DevicePairs: []types.MigrationDevicePairs{{SrcVolumeName: "name-1", TgtVolumeName: "name-1"}, {SrcVolumeName: "name-2", TgtVolumeName: "name-2"}},
			},
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name:          "SG not on remote array but returns success",
			symID:         "000000000001",
			remoteSymID:   "000000000002",
			clusterPrefix: "cluster",
			localSGVolumeList: map[string]*types.VolumeIterator{
				"sg-1": {
					ResultList: types.VolumeResultList{VolumeList: []types.VolumeIDList{
						{VolumeIDs: "vol-1,vol-2"},
					}},
				},
			},
			SGToRemoteVols:                     map[string][]string{"sg-1": {"vol-1", "vol-2"}},
			getLocalStorageGroupIDListResponse: &types.StorageGroupIDList{StorageGroupIDs: []string{"sg-1"}},
			// below is where the SG is not the same on the remote array
			getRemoteStorageGroupIDListResponse: &types.StorageGroupIDList{StorageGroupIDs: []string{"sg-2"}},
			getVolumesInStorageGroupIteratorResponse: &types.VolumeIterator{
				Count: 2,
				ResultList: types.VolumeResultList{
					VolumeList: []types.VolumeIDList{
						{
							VolumeIDs: "vol-1,vol-2",
						},
					},
				},
			},
			getStorageGroupResponse: &types.StorageGroup{
				NumOfVolumes: 2,
				HostIOLimit: &types.SetHostIOLimitsParam{
					HostIOLimitMBSec:    "100",
					HostIOLimitIOSec:    "100",
					DynamicDistribution: "true",
				},
			},
			createStorageGroupError: nil,
			getSGMigrationResponse: &types.MigrationSession{
				State: "Synchronized",
			},
			createSGMigrationResponse: &types.MigrationSession{
				DevicePairs: []types.MigrationDevicePairs{{SrcVolumeName: "name-1", TgtVolumeName: "name-1"}, {SrcVolumeName: "name-2", TgtVolumeName: "name-2"}},
			},
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name:          "GetStorageGroupMigrationByID returns an error and CreateSGMigration returns an error and call returns error",
			symID:         "000000000001",
			remoteSymID:   "000000000002",
			clusterPrefix: "cluster",
			localSGVolumeList: map[string]*types.VolumeIterator{
				"sg-1": {
					ResultList: types.VolumeResultList{VolumeList: []types.VolumeIDList{
						{VolumeIDs: "vol-1,vol-2"},
					}},
				},
			},
			SGToRemoteVols:                      map[string][]string{"sg-1": {"vol-1", "vol-2"}},
			getLocalStorageGroupIDListResponse:  &types.StorageGroupIDList{StorageGroupIDs: []string{"sg-1"}},
			getRemoteStorageGroupIDListResponse: &types.StorageGroupIDList{StorageGroupIDs: []string{"sg-1"}},
			getVolumesInStorageGroupIteratorResponse: &types.VolumeIterator{
				Count: 2,
				ResultList: types.VolumeResultList{
					VolumeList: []types.VolumeIDList{
						{
							VolumeIDs: "vol-1,vol-2",
						},
					},
				},
			},
			getStorageGroupResponse: &types.StorageGroup{NumOfVolumes: 2},
			getSGMigrationError:     errors.New("some error"),
			createSGMigrationError:  errors.New("some error"),
			expectedResult:          false,
			expectedError:           status.Errorf(codes.Internal, "Failed to create array migration session for target array(000000000002) - Error (some error)"),
		},
		{
			name:                                  "GetVolumesInStorageGroupIterator returns an error and call returns error",
			symID:                                 "000000000001",
			remoteSymID:                           "000000000002",
			clusterPrefix:                         "cluster",
			getLocalStorageGroupIDListResponse:    &types.StorageGroupIDList{StorageGroupIDs: []string{"sg-1"}},
			getRemoteStorageGroupIDListResponse:   &types.StorageGroupIDList{StorageGroupIDs: []string{"sg-1"}},
			getVolumesInStorageGroupIteratorError: errors.New("some error"),
			expectedResult:                        false,
			expectedError:                         status.Errorf(codes.Internal, "Error getting storage group volume list: some error"),
		},
		{
			name:          "CreateStorageGroup returns an error",
			symID:         "000000000001",
			remoteSymID:   "000000000002",
			clusterPrefix: "cluster",
			localSGVolumeList: map[string]*types.VolumeIterator{
				"sg-1": {
					ResultList: types.VolumeResultList{VolumeList: []types.VolumeIDList{
						{VolumeIDs: "vol-1,vol-2"},
					}},
				},
			},
			SGToRemoteVols:                      map[string][]string{"sg-1": {"vol-1", "vol-2"}},
			getLocalStorageGroupIDListResponse:  &types.StorageGroupIDList{StorageGroupIDs: []string{"sg-1"}},
			getRemoteStorageGroupIDListResponse: &types.StorageGroupIDList{StorageGroupIDs: []string{"sg-2"}},
			getVolumesInStorageGroupIteratorResponse: &types.VolumeIterator{
				Count: 2,
				ResultList: types.VolumeResultList{
					VolumeList: []types.VolumeIDList{
						{
							VolumeIDs: "vol-1,vol-2",
						},
					},
				},
			},
			getStorageGroupResponse: &types.StorageGroup{
				NumOfVolumes: 2,
				HostIOLimit: &types.SetHostIOLimitsParam{
					HostIOLimitMBSec:    "100",
					HostIOLimitIOSec:    "100",
					DynamicDistribution: "true",
				},
			},
			getSGMigrationResponse: &types.MigrationSession{
				State: "Synchronized",
			},
			createStorageGroupError: errors.New("some error"),
			expectedResult:          false,
			expectedError:           status.Errorf(codes.Internal, "Error creating storage group on remote array: some error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
			// local SYM call
			pmaxClient.EXPECT().GetStorageGroupIDList(gomock.Any(), tt.symID, gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getLocalStorageGroupIDListResponse, tt.getLocalStorageGroupIDListError)
			// remote SYM call
			pmaxClient.EXPECT().GetStorageGroupIDList(gomock.Any(), tt.remoteSymID, gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getRemoteStorageGroupIDListResponse, tt.getRemoteStorageGroupIDListError)
			pmaxClient.EXPECT().GetVolumesInStorageGroupIterator(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getVolumesInStorageGroupIteratorResponse, tt.getVolumesInStorageGroupIteratorError)
			pmaxClient.EXPECT().RemoveVolumesFromStorageGroup(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, tt.removeVolumesFromStorageGroupError)
			pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getStorageGroupResponse, tt.getStorageGroupError)
			pmaxClient.EXPECT().GetStorageGroupMigrationByID(gomock.Any(), tt.symID, gomock.Any()).AnyTimes().Return(tt.getSGMigrationResponse, tt.getSGMigrationError)
			pmaxClient.EXPECT().CreateSGMigration(gomock.Any(), tt.symID, tt.remoteSymID, gomock.Any()).AnyTimes().Return(tt.createSGMigrationResponse, tt.createSGMigrationError)

			pmaxClient.EXPECT().CreateStorageGroup(gomock.Any(), tt.remoteSymID, gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, tt.createStorageGroupError)

			localSGVolumeList = tt.localSGVolumeList
			defer func() { localSGVolumeList = nil }()
			SGToRemoteVols = tt.SGToRemoteVols
			defer func() { SGToRemoteVols = nil }()

			result, err := StorageGroupMigration(context.Background(), tt.symID, tt.remoteSymID, tt.clusterPrefix, pmaxClient)
			if result != tt.expectedResult {
				t.Errorf("Expected result %v, got %v", tt.expectedResult, result)
			}
			if !errors.Is(err, tt.expectedError) {
				t.Errorf("Expected error %v, got %v", tt.expectedError, err)
			}
		})
	}
}

func TestResetCache(t *testing.T) {
	tests := []struct {
		name               string
		cacheReset         bool
		expectedEmptyCache bool
	}{
		{
			name:               "Cache reset",
			cacheReset:         true,
			expectedEmptyCache: false,
		},
		{
			name:               "Cache reset",
			cacheReset:         false,
			expectedEmptyCache: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			CacheReset = tt.cacheReset
			NodePairs = make(map[string]string)
			NodePairs["key"] = "value"
			SGToRemoteVols = make(map[string][]string)
			SGToRemoteVols["key"] = []string{"value"}
			localSGVolumeList = make(map[string]*types.VolumeIterator)
			localSGVolumeList["key"] = &types.VolumeIterator{}
			ResetCache()

			if tt.expectedEmptyCache {
				assert.Empty(t, NodePairs)
				assert.Empty(t, SGToRemoteVols)
				assert.Empty(t, localSGVolumeList)
			} else {
				assert.NotEmpty(t, NodePairs)
				assert.NotEmpty(t, SGToRemoteVols)
				assert.NotEmpty(t, localSGVolumeList)
			}
		})
	}
}
