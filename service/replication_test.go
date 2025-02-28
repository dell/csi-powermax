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
	"testing"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"golang.org/x/net/context"
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
