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

// SRPInfo holds Storage Resource Pool capacity data for metrics collection.
type SRPInfo struct {
	SRPID                        string
	UsableTotalTB                float64
	UsableUsedTB                 float64
	SubscribedTB                 float64
	SnapshotTB                   float64
	EffectiveUsedCapacityPercent float64
}

// SRPClient is the minimal interface for SRP data.
type SRPClient interface {
	GetSRPs(ctx context.Context) ([]SRPInfo, error)
}

// SRPCollector collects PowerMax SRP (Storage Resource Pool / thin pool) capacity metrics.
type SRPCollector struct {
	client      SRPClient
	arrayID     string
	srpCapacity *prometheus.GaugeVec
	srpUtil     *prometheus.GaugeVec
}

// NewSRPCollector creates and registers a new SRPCollector.
func NewSRPCollector(client SRPClient, reg prometheus.Registerer, arrayID string) *SRPCollector {
	// Register metrics using helper function that handles AlreadyRegisteredError
	srpCapacity := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_srp_capacity_bytes",
		Help: "SRP capacity in bytes (capacity_type: total, used, subscribed, snapshot).",
	}, []string{"array_id", "srp_id", "capacity_type"}))

	srpUtil := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_srp_utilization_ratio",
		Help: "SRP effective utilization ratio (0-1).",
	}, []string{"array_id", "srp_id"}))

	return &SRPCollector{
		client:      client,
		arrayID:     arrayID,
		srpCapacity: srpCapacity,
		srpUtil:     srpUtil,
	}
}

const tbToBytes = 1099511627776.0 // 1 TiB in bytes

// Collect fetches SRP capacity metrics from Unisphere.
func (c *SRPCollector) Collect(ctx context.Context) error {
	srps, err := c.client.GetSRPs(ctx)
	if err != nil {
		return fmt.Errorf("SRPCollector: failed to get SRPs: %w", err)
	}
	for _, srp := range srps {
		totalBytes := srp.UsableTotalTB * tbToBytes
		usedBytes := srp.UsableUsedTB * tbToBytes
		subscribedBytes := srp.SubscribedTB * tbToBytes
		snapshotBytes := srp.SnapshotTB * tbToBytes

		c.srpCapacity.WithLabelValues(c.arrayID, srp.SRPID, "total").Set(totalBytes)
		c.srpCapacity.WithLabelValues(c.arrayID, srp.SRPID, "used").Set(usedBytes)
		c.srpCapacity.WithLabelValues(c.arrayID, srp.SRPID, "subscribed").Set(subscribedBytes)
		c.srpCapacity.WithLabelValues(c.arrayID, srp.SRPID, "snapshot").Set(snapshotBytes)

		utilization := srp.EffectiveUsedCapacityPercent / 100.0
		c.srpUtil.WithLabelValues(c.arrayID, srp.SRPID).Set(utilization)
	}
	return nil
}

// Name returns the collector name.
func (c *SRPCollector) Name() string { return "SRPCollector" }
