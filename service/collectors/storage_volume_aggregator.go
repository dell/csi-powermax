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
	"errors"
	"fmt"

	"github.com/dell/csm-metrics-common/pkg/naming"
	"github.com/prometheus/client_golang/prometheus"
)

// StorageVolumeAggregator collects aggregated storage volume metrics for this cluster.
// It filters volumes by cluster prefix to show only volumes created by this driver instance.
type StorageVolumeAggregator struct {
	client        VolumeClient
	arrayID       string
	protocol      string
	clusterPrefix string

	// Aggregated metrics
	volumeTotal         *prometheus.GaugeVec
	volumeSizeDist      *prometheus.HistogramVec
	volumeAttachedTotal *prometheus.GaugeVec
	sgVolumeCount       *prometheus.GaugeVec
}

// NewStorageVolumeAggregator creates a new StorageVolumeAggregator.
func NewStorageVolumeAggregator(client VolumeClient, reg prometheus.Registerer, arrayID, clusterPrefix, protocol string) *StorageVolumeAggregator {
	if protocol == "" {
		protocol = "none"
	}

	// Register metrics using helper function that handles AlreadyRegisteredError
	volumeTotal := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_volume_total",
		Help: "Total number of CSI-managed volumes (filtered by cluster prefix).",
	}, []string{naming.LabelArrayID, "protocol"}))

	// Handle HistogramVec separately
	volumeSizeDist := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dell_powermax_volume_size_aggregated_distribution_bytes",
		Help:    "Distribution of CSI volume sizes in bytes (aggregated by cluster prefix).",
		Buckets: []float64{1 << 30, 5 << 30, 10 << 30, 50 << 30, 100 << 30, 500 << 30, 1 << 40},
	}, []string{naming.LabelArrayID, "protocol"})
	if err := reg.Register(volumeSizeDist); err != nil {
		var alreadyRegisteredErr prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegisteredErr) {
			if existing, ok := alreadyRegisteredErr.ExistingCollector.(*prometheus.HistogramVec); ok {
				volumeSizeDist = existing
			}
		}
	}

	volumeAttachedTotal := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_volume_attached_aggregated_total",
		Help: "Total number of attached CSI volumes (filtered by cluster prefix).",
	}, []string{naming.LabelArrayID, "protocol"}))

	sgVolumeCount := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_storage_group_volume_count",
		Help: "Number of volumes per storage group (filtered by cluster prefix).",
	}, []string{naming.LabelArrayID, "storage_group", "protocol"}))

	return &StorageVolumeAggregator{
		client:              client,
		arrayID:             arrayID,
		protocol:            protocol,
		clusterPrefix:       clusterPrefix,
		volumeTotal:         volumeTotal,
		volumeSizeDist:      volumeSizeDist,
		volumeAttachedTotal: volumeAttachedTotal,
		sgVolumeCount:       sgVolumeCount,
	}
}

// Collect fetches and aggregates volume metrics.
func (c *StorageVolumeAggregator) Collect(ctx context.Context) error {
	volumes, err := c.client.GetVolumes(ctx)
	if err != nil {
		return fmt.Errorf("StorageVolumeAggregator: failed to get volumes: %w", err)
	}

	// Reset histogram before observing to prevent indefinite accumulation
	c.volumeSizeDist.Reset()

	// Deduplicate volumes by VolumeID.
	// GetVolumes() can return multiple entries per volume (one per host in masking views).
	// We track seen VolumeIDs to avoid double-counting in totals and SG counts.
	seenVolumes := make(map[string]bool)
	totalCount := 0.0
	attachedCount := 0.0
	sgVolumeCount := make(map[string]float64)

	for _, v := range volumes {
		if seenVolumes[v.VolumeID] {
			// Already counted this volume; only update attached count if this entry shows attached
			if v.Attached {
				attachedCount++
			}
			continue
		}
		seenVolumes[v.VolumeID] = true

		// Total count (unique volumes only)
		totalCount++

		// Observe size in histogram
		c.volumeSizeDist.WithLabelValues(c.arrayID, c.protocol).Observe(v.SizeBytes)

		// Attachment status
		if v.Attached {
			attachedCount++
		}

		// Storage group count
		sg := v.StorageGroupID
		if sg == "" {
			sg = "none"
		}
		sgVolumeCount[sg]++
	}

	// Set total metrics
	c.volumeTotal.WithLabelValues(c.arrayID, c.protocol).Set(totalCount)
	c.volumeAttachedTotal.WithLabelValues(c.arrayID, c.protocol).Set(attachedCount)

	// Set storage group volume counts
	for sg, count := range sgVolumeCount {
		c.sgVolumeCount.WithLabelValues(c.arrayID, sg, c.protocol).Set(count)
	}

	return nil
}

// Name returns the collector name.
func (c *StorageVolumeAggregator) Name() string { return "StorageVolumeAggregator" }
