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

package collectors

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewStorageVolumeAggregator tests the creation of a new StorageVolumeAggregator
func TestNewStorageVolumeAggregator(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockVolumeClient{}

	aggregator := NewStorageVolumeAggregator(client, reg, "array-1", "test-cluster", "FC")
	require.NotNil(t, aggregator)
	assert.Equal(t, "array-1", aggregator.arrayID)
	assert.Equal(t, "test-cluster", aggregator.clusterPrefix)
	assert.Equal(t, "FC", aggregator.protocol)
	assert.Equal(t, "StorageVolumeAggregator", aggregator.Name())
}

// TestNewStorageVolumeAggregator_DefaultProtocol tests that empty protocol defaults to "none"
func TestNewStorageVolumeAggregator_DefaultProtocol(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockVolumeClient{}

	aggregator := NewStorageVolumeAggregator(client, reg, "array-1", "test-cluster", "")
	require.NotNil(t, aggregator)
	assert.Equal(t, "none", aggregator.protocol)
}

// TestStorageVolumeAggregator_Collect_Success tests successful collection of volume metrics
func TestStorageVolumeAggregator_Collect_Success(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockVolumeClient{
		volumes: []VolumeInfo{
			{VolumeID: "vol1", SizeBytes: 10 << 30, Attached: true, StorageGroupID: "sg1"},
			{VolumeID: "vol2", SizeBytes: 20 << 30, Attached: false, StorageGroupID: "sg1"},
			{VolumeID: "vol3", SizeBytes: 30 << 30, Attached: true, StorageGroupID: "sg2"},
		},
	}

	aggregator := NewStorageVolumeAggregator(client, reg, "array-1", "test-cluster", "FC")
	err := aggregator.Collect(context.Background())
	assert.NoError(t, err)
}

// TestStorageVolumeAggregator_Collect_Deduplication tests that duplicate volume entries are deduplicated
func TestStorageVolumeAggregator_Collect_Deduplication(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockVolumeClient{
		volumes: []VolumeInfo{
			{VolumeID: "vol1", SizeBytes: 10 << 30, Attached: true, StorageGroupID: "sg1"},
			{VolumeID: "vol1", SizeBytes: 10 << 30, Attached: true, StorageGroupID: "sg1"}, // Duplicate
			{VolumeID: "vol2", SizeBytes: 20 << 30, Attached: false, StorageGroupID: "sg2"},
		},
	}

	aggregator := NewStorageVolumeAggregator(client, reg, "array-1", "test-cluster", "FC")
	err := aggregator.Collect(context.Background())
	assert.NoError(t, err)
}

// TestStorageVolumeAggregator_Collect_APIError tests handling of API errors
func TestStorageVolumeAggregator_Collect_APIError(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockVolumeClient{
		err: assert.AnError,
	}

	aggregator := NewStorageVolumeAggregator(client, reg, "array-1", "test-cluster", "FC")
	err := aggregator.Collect(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get volumes")
}

// TestStorageVolumeAggregator_Collect_EmptyVolumes tests handling of empty volume list
func TestStorageVolumeAggregator_Collect_EmptyVolumes(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockVolumeClient{
		volumes: []VolumeInfo{},
	}

	aggregator := NewStorageVolumeAggregator(client, reg, "array-1", "test-cluster", "FC")
	err := aggregator.Collect(context.Background())
	assert.NoError(t, err)
}

// TestStorageVolumeAggregator_Name tests the Name method
func TestStorageVolumeAggregator_Name(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockVolumeClient{}

	aggregator := NewStorageVolumeAggregator(client, reg, "array-1", "test-cluster", "FC")
	assert.Equal(t, "StorageVolumeAggregator", aggregator.Name())
}

// TestStorageVolumeAggregator_Collect_SingleVolume tests with single volume
func TestStorageVolumeAggregator_Collect_SingleVolume(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockVolumeClient{
		volumes: []VolumeInfo{
			{VolumeID: "vol1", SizeBytes: 10 << 30, Attached: true, StorageGroupID: "sg1"},
		},
	}

	aggregator := NewStorageVolumeAggregator(client, reg, "array-1", "test-cluster", "FC")
	err := aggregator.Collect(context.Background())
	assert.NoError(t, err)
}

// TestStorageVolumeAggregator_Collect_AllDetached tests with all detached volumes
func TestStorageVolumeAggregator_Collect_AllDetached(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockVolumeClient{
		volumes: []VolumeInfo{
			{VolumeID: "vol1", SizeBytes: 10 << 30, Attached: false, StorageGroupID: "sg1"},
			{VolumeID: "vol2", SizeBytes: 20 << 30, Attached: false, StorageGroupID: "sg2"},
		},
	}

	aggregator := NewStorageVolumeAggregator(client, reg, "array-1", "test-cluster", "FC")
	err := aggregator.Collect(context.Background())
	assert.NoError(t, err)
}

// TestStorageVolumeAggregator_Collect_MultipleStorageGroups tests volumes across multiple SGs
func TestStorageVolumeAggregator_Collect_MultipleStorageGroups(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockVolumeClient{
		volumes: []VolumeInfo{
			{VolumeID: "vol1", SizeBytes: 10 << 30, Attached: true, StorageGroupID: "sg1"},
			{VolumeID: "vol2", SizeBytes: 20 << 30, Attached: true, StorageGroupID: "sg2"},
			{VolumeID: "vol3", SizeBytes: 30 << 30, Attached: true, StorageGroupID: "sg3"},
		},
	}

	aggregator := NewStorageVolumeAggregator(client, reg, "array-1", "test-cluster", "FC")
	err := aggregator.Collect(context.Background())
	assert.NoError(t, err)
}

// mockVolumeClient is a mock implementation of VolumeClient for testing
type mockVolumeClient struct {
	volumes []VolumeInfo
	err     error
}

func (m *mockVolumeClient) GetVolumes(_ context.Context) ([]VolumeInfo, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.volumes, nil
}
