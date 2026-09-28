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

	"github.com/dell/csmlog"
	"github.com/prometheus/client_golang/prometheus"
)

// VolumeInfo holds per-volume data for metrics collection.
type VolumeInfo struct {
	VolumeID       string
	StorageGroupID string
	SizeBytes      float64
	Attached       bool
	HostName       string
	Healthy        bool
}

// VolumeClient is the minimal interface for volume data.
type VolumeClient interface {
	GetVolumes(ctx context.Context) ([]VolumeInfo, error)
}

// VolumeValidator validates if volumes are managed by the driver
type VolumeValidator interface {
	IsDriverManaged(ctx context.Context, volumeID string) (bool, error)
	RefreshCache(ctx context.Context) error
}

// VolumeCollector collects PowerMax volume metrics with Kubernetes PV validation.
type VolumeCollector struct {
	client                 VolumeClient
	arrayID                string
	validator              VolumeValidator
	volumeTotal            *prometheus.GaugeVec
	volumeSizeDist         *prometheus.GaugeVec
	volumeSizeBytes        *prometheus.GaugeVec
	volumeAttachmentStatus *prometheus.GaugeVec
	attachedTotal          *prometheus.GaugeVec
	unhealthyTotal         *prometheus.GaugeVec
}

// NewVolumeCollector creates a new VolumeCollector with Kubernetes PV validation.
func NewVolumeCollector(client VolumeClient, reg prometheus.Registerer, arrayID string, validator VolumeValidator) (*VolumeCollector, error) {
	// Register metrics using helper function that handles AlreadyRegisteredError
	volumeTotal := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_csi_volume_total",
		Help: "Total number of CSI-managed volumes (Kubernetes PV-validated).",
	}, []string{"array_id"}))

	volumeSizeDist := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_volume_size_distribution_bytes",
		Help: "Distribution of CSI volume sizes in bytes represented as cumulative bucket gauges.",
	}, []string{"array_id", "bucket"}))

	volumeSizeBytes := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_volume_size_bytes",
		Help: "Size of individual CSI volumes in bytes (Kubernetes PV-validated).",
	}, []string{"volume_id", "array_id"}))

	volumeAttachmentStatus := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_volume_attachment_status",
		Help: "Attachment status of CSI volumes per host (0=detached, 1=attached) (Kubernetes PV-validated).",
	}, []string{"volume_id", "host_name", "array_id"}))

	attachedTotal := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_volume_attached_total",
		Help: "Total number of attached CSI volumes (Kubernetes PV-validated).",
	}, []string{"array_id"}))

	unhealthyTotal := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_volume_unhealthy_total",
		Help: "Total number of unhealthy CSI volumes (Kubernetes PV-validated).",
	}, []string{"array_id"}))

	return &VolumeCollector{
		client:                 client,
		arrayID:                arrayID,
		validator:              validator,
		volumeTotal:            volumeTotal,
		volumeSizeDist:         volumeSizeDist,
		volumeSizeBytes:        volumeSizeBytes,
		volumeAttachmentStatus: volumeAttachmentStatus,
		attachedTotal:          attachedTotal,
		unhealthyTotal:         unhealthyTotal,
	}, nil
}

// Bucket definitions for volume size distribution
var volumeSizeBuckets = []struct {
	label string
	upper int64
}{
	{label: "<=1GiB", upper: 1 << 30},
	{label: "<=5GiB", upper: 5 << 30},
	{label: "<=10GiB", upper: 10 << 30},
	{label: "<=50GiB", upper: 50 << 30},
	{label: "<=100GiB", upper: 100 << 30},
	{label: "<=500GiB", upper: 500 << 30},
	{label: "<=1TiB", upper: 1 << 40},
	{label: ">1TiB", upper: -1},
}

// Collect fetches volume metrics with Kubernetes PV validation.
func (c *VolumeCollector) Collect(ctx context.Context) error {
	skipValidation := false
	if c.validator != nil {
		if err := c.validator.RefreshCache(ctx); err != nil {
			csmlog.Warnf("VolumeCollector: failed to refresh validator cache: %v, skipping filtering for this cycle", err)
			skipValidation = true
		}
	}

	volumes, err := c.client.GetVolumes(ctx)
	if err != nil {
		return fmt.Errorf("VolumeCollector: failed to get volumes: %w", err)
	}

	var totalCount, attachedCount, unhealthyCount int
	bucketCounts := make(map[string]int, len(volumeSizeBuckets))

	// Reset bucket metrics before counting
	for _, bucket := range volumeSizeBuckets {
		c.volumeSizeDist.DeleteLabelValues(c.arrayID, bucket.label)
	}

	// Reset per-volume gauges before observing to prevent indefinite accumulation
	c.volumeSizeBytes.Reset()
	c.volumeAttachmentStatus.Reset()

	for _, vol := range volumes {
		if !skipValidation && c.validator != nil {
			isManaged, err := c.validator.IsDriverManaged(ctx, vol.VolumeID)
			if err != nil {
				csmlog.Warnf("VolumeCollector: failed to check if volume %s is driver managed: %v, skipping", vol.VolumeID, err)
				continue
			}
			if !isManaged {
				continue
			}
		}

		totalCount++

		// Count volume in appropriate size buckets (cumulative distribution)
		for i, bucket := range volumeSizeBuckets {
			if i == len(volumeSizeBuckets)-1 {
				// Last bucket (">1TiB"): volumes strictly greater than the previous upper bound
				prevUpper := volumeSizeBuckets[i-1].upper
				if vol.SizeBytes > float64(prevUpper) {
					bucketCounts[bucket.label]++
				}
			} else if vol.SizeBytes <= float64(bucket.upper) {
				bucketCounts[bucket.label]++
			}
		}

		c.volumeSizeBytes.WithLabelValues(vol.VolumeID, c.arrayID).Set(vol.SizeBytes)

		// Set per-volume per-host attachment status (spec requirement)
		// VolumeInfo represents volume-host combinations from the adapter
		if vol.HostName != "" {
			if vol.Attached {
				c.volumeAttachmentStatus.WithLabelValues(vol.VolumeID, vol.HostName, c.arrayID).Set(1)
			} else {
				c.volumeAttachmentStatus.WithLabelValues(vol.VolumeID, vol.HostName, c.arrayID).Set(0)
			}
		}

		if vol.Attached {
			attachedCount++
		}

		if !vol.Healthy {
			unhealthyCount++
		}
	}

	// Set bucket metrics for all buckets, including zero counts to ensure series continuity
	for _, bucket := range volumeSizeBuckets {
		count := bucketCounts[bucket.label]
		c.volumeSizeDist.WithLabelValues(c.arrayID, bucket.label).Set(float64(count))
	}

	c.volumeTotal.WithLabelValues(c.arrayID).Set(float64(totalCount))
	c.attachedTotal.WithLabelValues(c.arrayID).Set(float64(attachedCount))
	c.unhealthyTotal.WithLabelValues(c.arrayID).Set(float64(unhealthyCount))

	return nil
}

// Name returns the collector name.
func (c *VolumeCollector) Name() string { return "VolumeCollector" }
