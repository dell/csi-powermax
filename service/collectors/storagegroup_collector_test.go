/*
 Copyright © 2026 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package collectors_test

import (
	"context"
	"testing"

	"github.com/dell/csi-powermax/v2/service/collectors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSGClient struct {
	groups []collectors.StorageGroupInfo
	err    error
}

func (m *mockSGClient) GetStorageGroups(_ context.Context) ([]collectors.StorageGroupInfo, error) {
	return m.groups, m.err
}

type mockSGClientErr struct{ err error }

func (m *mockSGClientErr) GetStorageGroups(_ context.Context) ([]collectors.StorageGroupInfo, error) {
	return nil, m.err
}

// U-SG-02: API error returns wrapped error.
func TestStorageGroupCollector_Collect_APIError(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewStorageGroupCollector(&mockSGClientErr{err: assert.AnError}, reg, "array-1")
	err := c.Collect(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "StorageGroupCollector")
}

// U-SG-07: Name() returns "StorageGroupCollector".
func TestStorageGroupCollector_Name(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewStorageGroupCollector(nil, reg, "array-1")
	assert.Equal(t, "StorageGroupCollector", c.Name())
}

// U-SG-08: NewStorageGroupCollector creates collector with correct configuration
func TestStorageGroupCollector_New(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockSGClient{
		groups: []collectors.StorageGroupInfo{},
	}
	c := collectors.NewStorageGroupCollector(client, reg, "array-1")
	assert.NotNil(t, c)
}

// U-SG-09: Collect with empty list returns no error
func TestStorageGroupCollector_Collect_Empty(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockSGClient{
		groups: []collectors.StorageGroupInfo{},
	}
	c := collectors.NewStorageGroupCollector(client, reg, "array-1")

	err := c.Collect(context.Background())
	assert.NoError(t, err)
}

// TestStorageGroupCollector_Collect_SingleGroup tests with single storage group
func TestStorageGroupCollector_Collect_SingleGroup(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockSGClient{
		groups: []collectors.StorageGroupInfo{
			{StorageGroupID: "sg-1", NumOfVolumes: 5, CapacityGB: 1000},
		},
	}
	c := collectors.NewStorageGroupCollector(client, reg, "array-1")

	err := c.Collect(context.Background())
	assert.NoError(t, err)
}

// TestStorageGroupCollector_Collect_MultipleGroups tests with multiple storage groups
func TestStorageGroupCollector_Collect_MultipleGroups(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockSGClient{
		groups: []collectors.StorageGroupInfo{
			{StorageGroupID: "sg-1", NumOfVolumes: 5, CapacityGB: 1000},
			{StorageGroupID: "sg-2", NumOfVolumes: 10, CapacityGB: 2000},
		},
	}
	c := collectors.NewStorageGroupCollector(client, reg, "array-1")

	err := c.Collect(context.Background())
	assert.NoError(t, err)
}

// TestStorageGroupCollector_New_DefaultProtocol tests default protocol
func TestStorageGroupCollector_New_DefaultProtocol(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockSGClient{}
	c := collectors.NewStorageGroupCollector(client, reg, "array-1")
	assert.NotNil(t, c)
}

// TestStorageGroupCollector_Collect_WithSnapshots tests with snapshot data
func TestStorageGroupCollector_Collect_WithSnapshots(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockSGClient{
		groups: []collectors.StorageGroupInfo{
			{StorageGroupID: "sg-1", NumOfVolumes: 5, NumOfSnapshots: 2, CapacityGB: 1000},
		},
	}
	c := collectors.NewStorageGroupCollector(client, reg, "array-1")

	err := c.Collect(context.Background())
	assert.NoError(t, err)
}
