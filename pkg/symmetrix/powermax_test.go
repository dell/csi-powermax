/*
 Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package symmetrix

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/golang/mock/gomock"
)

func TestGetPowerMaxClient(t *testing.T) {
	c, err := pmax.NewClientWithArgs("/", "test", true, true, "")
	if err != nil {
		t.Fatalf("Faild to create a pmax client: %s", err.Error())
	}

	Initialize([]string{"0001", "0002"}, c)

	_, err = GetPowerMaxClient("0001")
	if err != nil {
		t.Errorf("Faied to create client with only primary managed array specified")
	}
	_, err = GetPowerMaxClient("0003")
	if err == nil {
		t.Errorf("Should have failed with only primary unmanaged array specified")
	}
	_, err = GetPowerMaxClient("0001", "0002")
	if err != nil {
		t.Errorf("Faied to create client with both arrays managed")
	}
	_, err = GetPowerMaxClient("0001", "0003")
	if err == nil {
		t.Errorf("Should have failed with secondary unmanaged array specified")
	}
	_, err = GetPowerMaxClient("0003", "0002")
	if err == nil {
		t.Errorf("Should have failed with primary unmanaged array specified")
	}
	_, err = GetPowerMaxClient("0003", "0004")
	if err == nil {
		t.Errorf("Should have failed with none of the arrays managed")
	}
}

// ctx context.Context, client pmax.Pmax, symID string
func TestUpdate(t *testing.T) {
	symmetrixCapability := types.SymmetrixCapability{
		SymmetrixID:   "fakeSymmetrix",
		SnapVxCapable: true,
		RdfCapable:    true,
	}
	rep := ReplicationCapabilitiesCache{}
	rep.update(&symmetrixCapability)
	if rep.cap.SymmetrixID != "fakeSymmetrix" {
		t.Errorf("update call failed -- SymmetrixID not set properly in capability: cap.SymmetrixID: %+v", rep.cap.SymmetrixID)
		if !rep.cap.SnapVxCapable {
			t.Errorf("update call failed -- SnapVxCapable not set properly in capability: cap.SnapVxCapable: %+v", rep.cap.SnapVxCapable)
			if !rep.cap.RdfCapable {
				t.Errorf("update call failed -- RdfCapable not set properly in capability: cap.RdfCapable: %+v", rep.cap.RdfCapable)
			}
		}
	}
}

func TestCacheTime_IsValid(t *testing.T) {
	tests := []struct {
		name     string
		cache    CacheTime
		expected bool
	}{
		{
			name:     "cache not initialized",
			cache:    CacheTime{},
			expected: false,
		},
		{
			name:     "cache still valid",
			cache:    CacheTime{CreationTime: time.Now(), CacheValidity: time.Hour},
			expected: true,
		},
		{
			name:     "cache expired",
			cache:    CacheTime{CreationTime: time.Now().Add(-2 * time.Hour), CacheValidity: time.Hour},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := tt.cache.IsValid()
			if actual != tt.expected {
				t.Errorf("expected %v, but got %v", tt.expected, actual)
			}
		})
	}
}

func TestReplicationCapabilitiesCache_Get(t *testing.T) {
	tests := []struct {
		name     string
		cache    ReplicationCapabilitiesCache
		ctx      context.Context
		client   func() *mocks.MockPmaxClient
		symID    string
		expected *types.SymmetrixCapability
		err      error
	}{
		{
			name:  "cache not initialized",
			cache: ReplicationCapabilitiesCache{},
			ctx:   context.Background(),
			client: func() *mocks.MockPmaxClient {
				client := mocks.NewMockPmaxClient(gomock.NewController(t))
				client.EXPECT().GetReplicationCapabilities(gomock.Any()).Times(1).Return(&types.SymReplicationCapabilities{
					SymmetrixCapability: []types.SymmetrixCapability{{SymmetrixID: "symID"}},
				}, nil)
				return client
			},
			symID:    "symID",
			expected: &types.SymmetrixCapability{SymmetrixID: "symID"},
			err:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, err := tt.cache.Get(tt.ctx, tt.client(), tt.symID)
			if !reflect.DeepEqual(err, tt.err) {
				t.Errorf("expected error %v, but got %v", tt.err, err)
			}
			if !reflect.DeepEqual(actual, tt.expected) {
				t.Errorf("expected %v, but got %v", tt.expected, actual)
			}
		})
	}
}

func TestGetSGName(t *testing.T) {
	tests := []struct {
		name                   string
		applicationPrefix      string
		serviceLevel           string
		storageResourcePool    string
		expectedStorageGroupID string
	}{
		{
			name:                   "empty application prefix",
			applicationPrefix:      "",
			serviceLevel:           "serviceLevel",
			storageResourcePool:    "storageResourcePool",
			expectedStorageGroupID: "csi-clusterPrefix-serviceLevel-storageResourcePool-SG",
		},
		{
			name:                   "non-empty application prefix",
			applicationPrefix:      "applicationPrefix",
			serviceLevel:           "serviceLevel",
			storageResourcePool:    "storageResourcePool",
			expectedStorageGroupID: "csi-clusterPrefix-applicationPrefix-serviceLevel-storageResourcePool-SG",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PowerMax{
				SymID:         "symID",
				ClusterPrefix: "clusterPrefix",
				client:        mocks.NewMockPmaxClient(gomock.NewController(t)),
			}

			actual := p.GetSGName(tt.applicationPrefix, tt.serviceLevel, tt.storageResourcePool)
			if actual != tt.expectedStorageGroupID {
				t.Errorf("expected storage group ID %s, but got %s", tt.expectedStorageGroupID, actual)
			}
		})
	}
}

func TestGetVolumeIdentifier(t *testing.T) {
	tests := []struct {
		name           string
		volumeName     string
		clusterPrefix  string
		expectedResult string
	}{
		{
			name:           "short volume name",
			volumeName:     "short-volume-name",
			clusterPrefix:  "cluster-prefix",
			expectedResult: "csi-cluster-prefix-short-volume-name",
		},
		{
			name:           "long volume name",
			volumeName:     "this-is-a-very-long-volume-name-that-exceeds-the-maximum-length",
			clusterPrefix:  "cluster-prefix",
			expectedResult: "csi-cluster-prefix-this-is-a-very-long-voleeds-the-maximum-length",
		},
		{
			name:           "empty volume name",
			volumeName:     "",
			clusterPrefix:  "cluster-prefix",
			expectedResult: "csi-cluster-prefix-",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &PowerMax{
				SymID:         "symID",
				ClusterPrefix: tt.clusterPrefix,
				client:        mocks.NewMockPmaxClient(gomock.NewController(t)),
			}

			actual := p.GetVolumeIdentifier(tt.volumeName)
			if actual != tt.expectedResult {
				t.Errorf("expected %s, but got %s", tt.expectedResult, actual)
			}
		})
	}
}
