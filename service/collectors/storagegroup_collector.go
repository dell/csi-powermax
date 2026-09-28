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
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
)

// StorageGroupInfo holds storage group data for metrics collection.
type StorageGroupInfo struct {
	StorageGroupID string
	NumOfVolumes   int
	CapacityGB     float64
	UsedGB         float64
	NumOfSnapshots int
	ProtocolCounts map[string]int // Volume count per protocol (FC, ISCSI, NVMETCP, etc.)
}

// StorageGroupClient is the minimal interface for storage group data.
type StorageGroupClient interface {
	GetStorageGroups(ctx context.Context) ([]StorageGroupInfo, error)
}

// StorageGroupCollector collects PowerMax storage group metrics.
type StorageGroupCollector struct {
	client        StorageGroupClient
	arrayID       string
	volumeCount   *prometheus.GaugeVec
	capacity      *prometheus.GaugeVec
	utilization   *prometheus.GaugeVec
	snapshotCount *prometheus.GaugeVec
}

// NewStorageGroupCollector creates a new StorageGroupCollector.
func NewStorageGroupCollector(client StorageGroupClient, reg prometheus.Registerer, arrayID string) *StorageGroupCollector {
	// Register metrics using helper function that handles AlreadyRegisteredError
	volumeCount := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_csi_storagegroup_volume_count",
		Help: "Number of volumes in the storage group.",
	}, []string{"array_id", "storage_group"}))

	capacity := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_csi_storagegroup_capacity_bytes",
		Help: "Storage group capacity in bytes (capacity_type: total, used, available).",
	}, []string{"array_id", "storage_group", "capacity_type"}))

	utilization := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_csi_storagegroup_utilization_ratio",
		Help: "Storage group utilization ratio.",
	}, []string{"array_id", "storage_group"}))

	snapshotCount := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_storagegroup_snapshot_count",
		Help: "Number of snapshots per storage group.",
	}, []string{"array_id", "storage_group"}))

	return &StorageGroupCollector{
		client:        client,
		arrayID:       arrayID,
		volumeCount:   volumeCount,
		capacity:      capacity,
		utilization:   utilization,
		snapshotCount: snapshotCount,
	}
}

// Collect fetches storage group metrics.
func (c *StorageGroupCollector) Collect(ctx context.Context) error {
	groups, err := c.client.GetStorageGroups(ctx)
	if err != nil {
		return fmt.Errorf("StorageGroupCollector: failed to get storage groups: %w", err)
	}

	for _, g := range groups {
		totalBytes := g.CapacityGB * 1024 * 1024 * 1024
		usedBytes := g.UsedGB * 1024 * 1024 * 1024
		availBytes := totalBytes - usedBytes
		if availBytes < 0 {
			availBytes = 0
		}
		utilization := 0.0
		if g.CapacityGB > 0 {
			utilization = g.UsedGB / g.CapacityGB
		}

		// Emit capacity and utilization once per storage group (shared across all protocols)
		c.capacity.WithLabelValues(c.arrayID, g.StorageGroupID, "total").Set(totalBytes)
		c.capacity.WithLabelValues(c.arrayID, g.StorageGroupID, "used").Set(usedBytes)
		c.capacity.WithLabelValues(c.arrayID, g.StorageGroupID, "available").Set(availBytes)
		c.utilization.WithLabelValues(c.arrayID, g.StorageGroupID).Set(utilization)

		// Emit volume count (sum across all protocols)
		totalVolumes := 0
		if len(g.ProtocolCounts) > 0 {
			for _, count := range g.ProtocolCounts {
				totalVolumes += count
			}
		} else {
			// Fallback: use NumOfVolumes if ProtocolCounts not available
			totalVolumes = g.NumOfVolumes
		}
		c.volumeCount.WithLabelValues(c.arrayID, g.StorageGroupID).Set(float64(totalVolumes))

		// Emit snapshot metrics once per storage group (storage-group-level, not protocol-specific)
		c.snapshotCount.WithLabelValues(c.arrayID, g.StorageGroupID).Set(float64(g.NumOfSnapshots))
	}

	return nil
}

// Name returns the collector name.
func (c *StorageGroupCollector) Name() string { return "StorageGroupCollector" }
