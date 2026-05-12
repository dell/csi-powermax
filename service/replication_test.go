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
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestValidateRDFState(t *testing.T) {
	tests := []struct {
		name           string
		action         string
		pmaxClient     pmax.Pmax
		expectedResult bool
		expectedError  error
	}{
		{
			name:   "Successful - Resume",
			action: Resume,
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroupRDFG{
					States: []string{Consistent},
				}, nil)
				return pmaxClient
			}(),
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name:   "Successful - Establish",
			action: Establish,
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroupRDFG{
					States: []string{Consistent},
				}, nil)
				return pmaxClient
			}(),
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name:   "Successful - Suspend",
			action: Suspend,
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroupRDFG{
					States: []string{Suspended},
				}, nil)
				return pmaxClient
			}(),
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name:   "Successful - FailOver",
			action: FailOver,
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroupRDFG{
					States: []string{FailedOver},
				}, nil)
				return pmaxClient
			}(),
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name:   "Successful - FailBack",
			action: FailBack,
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroupRDFG{
					States: []string{Consistent},
				}, nil)
				return pmaxClient
			}(),
			expectedResult: true,
			expectedError:  nil,
		},
		{
			name:   "Error - Invalid state for Resume",
			action: Resume,
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroupRDFG{
					States: []string{Suspended},
				}, nil)
				return pmaxClient
			}(),
			expectedResult: false,
			expectedError:  nil,
		},
		{
			name:   "Error calling GetStorageGroupRDFInfo",
			action: Resume,
			pmaxClient: func() pmax.Pmax {
				pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, errors.New("error callin GetStorageGroupRDFInfo"))
				return pmaxClient
			}(),
			expectedResult: false,
			expectedError:  status.Errorf(codes.Internal, "Failed to fetch replication state for SG (%s) - Error (%s)", "sg-1", errors.New("error callin GetStorageGroupRDFInfo")),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := validateRDFState(context.Background(), "array1", tc.action, "sg-1", "1", tc.pmaxClient)
			assert.Equal(t, tc.expectedResult, result)
			assert.Equal(t, tc.expectedError, err)
		})
	}
}

func TestGetQueryMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		expected string
	}{
		{
			name:     "Async",
			mode:     Async,
			expected: QueryAsync,
		},
		{
			name:     "Sync",
			mode:     Sync,
			expected: QuerySync,
		},
		{
			name:     "Metro",
			mode:     Metro,
			expected: QueryMetro,
		},
		{
			name:     "Invalid mode",
			mode:     "invalid",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getQueryMode(tt.mode)
			if result != tt.expected {
				t.Errorf("getQueryMode(%s) = %s, want %s", tt.mode, result, tt.expected)
			}
		})
	}
}

func TestService_GetOrCreateRDFGroup(t *testing.T) {
	tests := []struct {
		name               string
		localSymID         string
		remoteSymID        string
		repMode            string
		namespace          string
		pmaxClient         *mocks.MockPmaxClient
		expectedLocalRDFG  string
		expectedRemoteRDFG string
		expectedErr        error
		initializeArray    string
	}{
		{
			name:        "success_return_existing_matching_srdf_group",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "ns1",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				mockPmaxClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym-id", gomock.Any()).Return(&types.RDFGroupList{
					RDFGroupCount: 2,
					RDFGroupIDs: []types.RDFGroupIDL{
						{
							RDFGNumber:  2,
							Label:       "CPns1",
							RemoteSymID: "remote-sym-id",
							GroupType:   "Dynamic",
						},
						{
							RDFGNumber:  3,
							Label:       "CPns2",
							RemoteSymID: "remote-sym-id",
							GroupType:   "Dynamic",
						},
					},
				}, nil)

				mockPmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), "local-sym-id", "2").Return(&types.RDFGroup{
					RdfgNumber:       2,
					RemoteRdfgNumber: 20,
				}, nil)

				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "2",
			expectedRemoteRDFG: "20",
			expectedErr:        nil,
		},
		{
			name:        "eror_getting_existing_srdf_group",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "ns1",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				mockPmaxClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym-id", gomock.Any()).Return(nil, errors.New("error getting RDF group list"))
				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "",
			expectedRemoteRDFG: "",
			expectedErr:        fmt.Errorf("error getting RDF group list"),
		},
		{
			name:        "error_when_GetRDFGroupByID_called",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "ns1",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				mockPmaxClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym-id", gomock.Any()).Return(&types.RDFGroupList{
					RDFGroupCount: 2,
					RDFGroupIDs: []types.RDFGroupIDL{
						{
							RDFGNumber:  2,
							Label:       "CPns1",
							RemoteSymID: "remote-sym-id",
							GroupType:   "Dynamic",
						},
					},
				}, nil)

				mockPmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), "local-sym-id", "2").Return(nil, errors.New("error getting RDFGroup by ID"))

				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "",
			expectedRemoteRDFG: "",
			expectedErr:        fmt.Errorf("error getting RDFGroup by ID"),
		},
		{
			name:        "success_no_SRDF_groups_found_for_array_returns_default",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "ns1",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				mockPmaxClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym-id", gomock.Any()).Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "local-sym-id", "").Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "remote-sym-id", "").Return(nil, errors.New("No SRDF Groups found for Array"))

				mockPmaxClient.EXPECT().GetLocalOnlineRDFDirs(gomock.Any(), "local-sym-id").Return(&types.RDFDirList{RdfDirs: []string{"OR-1C"}}, nil)
				mockPmaxClient.EXPECT().GetLocalOnlineRDFPorts(gomock.Any(), "OR-1C", "local-sym-id").Return(&types.RDFPortList{RdfPorts: []string{}}, nil)
				mockPmaxClient.EXPECT().ExecuteCreateRDFGroup(gomock.Any(), "local-sym-id", gomock.Any()).Return(nil)

				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "1",
			expectedRemoteRDFG: "1",
			expectedErr:        nil,
		},
		{
			name:        "error_when_GetFreeLocalAndRemoteRDFg_called_for_local_sym",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "ns1",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				mockPmaxClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym-id", gomock.Any()).Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "local-sym-id", "").Return(nil, errors.New("error getting local and remote RDFg for local sym"))

				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "",
			expectedRemoteRDFG: "",
			expectedErr:        fmt.Errorf("error getting local and remote RDFg for local sym"),
		},
		{
			name:        "error_when_GetFreeLocalAndRemoteRDFg_called_for_remote_sym",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "ns1",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				mockPmaxClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym-id", gomock.Any()).Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "local-sym-id", "").Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "remote-sym-id", "").Return(nil, errors.New("error getting local and remote RDFg for remote sym"))

				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "",
			expectedRemoteRDFG: "",
			expectedErr:        fmt.Errorf("error getting local and remote RDFg for remote sym"),
		},
		{
			name:        "error_getting_local_ONLINE_RDF_Directors",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "ns1",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				mockPmaxClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym-id", gomock.Any()).Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "local-sym-id", "").Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "remote-sym-id", "").Return(nil, errors.New("No SRDF Groups found for Array"))

				mockPmaxClient.EXPECT().GetLocalOnlineRDFDirs(gomock.Any(), "local-sym-id").Return(nil, errors.New("error getting local ONLINE RDF Directors"))

				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "",
			expectedRemoteRDFG: "",
			expectedErr:        fmt.Errorf("error getting local ONLINE RDF Directors"),
		},
		{
			name:        "error_getting_local_ONLINE_RDF_Ports",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "ns1",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				mockPmaxClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym-id", gomock.Any()).Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "local-sym-id", "").Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "remote-sym-id", "").Return(nil, errors.New("No SRDF Groups found for Array"))

				mockPmaxClient.EXPECT().GetLocalOnlineRDFDirs(gomock.Any(), "local-sym-id").Return(&types.RDFDirList{RdfDirs: []string{"OR-1C"}}, nil)
				mockPmaxClient.EXPECT().GetLocalOnlineRDFPorts(gomock.Any(), "OR-1C", "local-sym-id").Return(nil, errors.New("error getting local ONLINE RDF ports"))

				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "",
			expectedRemoteRDFG: "",
			expectedErr:        fmt.Errorf("error getting local ONLINE RDF ports"),
		},
		{
			name:        "error_getting_remote_RDF_ports",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "ns1",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				mockPmaxClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym-id", gomock.Any()).Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "local-sym-id", "").Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "remote-sym-id", "").Return(nil, errors.New("No SRDF Groups found for Array"))

				mockPmaxClient.EXPECT().GetLocalOnlineRDFDirs(gomock.Any(), "local-sym-id").Return(&types.RDFDirList{RdfDirs: []string{"OR-1C"}}, nil)
				mockPmaxClient.EXPECT().GetLocalOnlineRDFPorts(gomock.Any(), "OR-1C", "local-sym-id").Return(&types.RDFPortList{RdfPorts: []string{"3"}}, nil)
				mockPmaxClient.EXPECT().GetRemoteRDFPortOnSAN(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("error getting remote RDF ports"))

				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "",
			expectedRemoteRDFG: "",
			expectedErr:        fmt.Errorf("error getting remote RDF ports"),
		},
		{
			name:        "error_when_rdf_label_length_is_greater_than_10",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "namespace_with_a_long_name",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "",
			expectedRemoteRDFG: "",
			expectedErr:        fmt.Errorf("rdfLabel: CPnamespace_with_a_long_name for mode: ASYNC exceeds 10 char limit, rename the namespace within 7 char or use pre-existing RDFG via storage class"),
		},
		{
			name:        "error_during_create_rdf_group",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "ns1",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				mockPmaxClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym-id", gomock.Any()).Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "local-sym-id", "").Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "remote-sym-id", "").Return(nil, errors.New("No SRDF Groups found for Array"))

				mockPmaxClient.EXPECT().GetLocalOnlineRDFDirs(gomock.Any(), "local-sym-id").Return(&types.RDFDirList{RdfDirs: []string{"OR-1C"}}, nil)
				mockPmaxClient.EXPECT().GetLocalOnlineRDFPorts(gomock.Any(), "OR-1C", "local-sym-id").Return(&types.RDFPortList{RdfPorts: []string{}}, nil)
				mockPmaxClient.EXPECT().ExecuteCreateRDFGroup(gomock.Any(), "local-sym-id", gomock.Any()).Return(errors.New("error during create rdf group"))

				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "",
			expectedRemoteRDFG: "",
			expectedErr:        fmt.Errorf("error during create rdf group"),
		},
		{
			name:        "success_no_SRDF_groups_found_for_array_but_free_group_nums_exists",
			localSymID:  "local-sym-id",
			remoteSymID: "remote-sym-id",
			repMode:     "ASYNC",
			namespace:   "ns1",
			pmaxClient: func() *mocks.MockPmaxClient {
				mockPmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
				mockPmaxClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym-id", gomock.Any()).Return(nil, errors.New("No SRDF Groups found for Array"))
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "local-sym-id", "").Return(&types.NextFreeRDFGroup{
					LocalRdfGroup: []int{10},
				}, nil)
				mockPmaxClient.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), "remote-sym-id", "").Return(&types.NextFreeRDFGroup{
					LocalRdfGroup: []int{2},
				}, nil)

				mockPmaxClient.EXPECT().GetLocalOnlineRDFDirs(gomock.Any(), "local-sym-id").Return(&types.RDFDirList{RdfDirs: []string{"OR-1C"}}, nil)
				mockPmaxClient.EXPECT().GetLocalOnlineRDFPorts(gomock.Any(), "OR-1C", "local-sym-id").Return(&types.RDFPortList{RdfPorts: []string{"4"}}, nil)
				mockPmaxClient.EXPECT().GetRemoteRDFPortOnSAN(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(
					&types.RemoteRDFPortDetails{
						RemotePorts: []types.RDFPortDetails{
							{
								SymmID: "remote-sym-id",
							},
						},
					}, nil,
				)

				mockPmaxClient.EXPECT().GetLocalRDFPortDetails(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(&types.RDFPortDetails{}, nil)
				mockPmaxClient.EXPECT().ExecuteCreateRDFGroup(gomock.Any(), "local-sym-id", gomock.Any()).Return(nil)

				return mockPmaxClient
			}(),
			initializeArray:    "local-sym-id",
			expectedLocalRDFG:  "10",
			expectedRemoteRDFG: "2",
			expectedErr:        nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					ClusterPrefix: "CP",
				},
			}
			_ = symmetrix.Initialize([]string{tt.initializeArray}, tt.pmaxClient)
			defer symmetrix.RemoveClient(tt.initializeArray)

			localRDFG, remoteRDFG, err := s.GetOrCreateRDFGroup(context.Background(), tt.localSymID, tt.remoteSymID, tt.repMode, tt.namespace, tt.pmaxClient)
			if localRDFG != tt.expectedLocalRDFG {
				t.Errorf("GetOrCreateRDFGroup() localRDFG = %v, want %v", localRDFG, tt.expectedLocalRDFG)
			}
			if remoteRDFG != tt.expectedRemoteRDFG {
				t.Errorf("GetOrCreateRDFGroup() remoteRDFG = %v, want %v", remoteRDFG, tt.expectedRemoteRDFG)
			}
			if !reflect.DeepEqual(err, tt.expectedErr) {
				t.Errorf("GetOrCreateRDFGroup() error = %v, want %v", err, tt.expectedErr)
			}
		})
	}
}
