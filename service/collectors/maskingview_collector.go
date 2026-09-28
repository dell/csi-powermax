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

// MaskingViewInfo holds masking view data from Unisphere.
type MaskingViewInfo struct {
	MaskingViewID    string
	StorageGroupID   string
	PortGroupID      string
	InitiatorGroupID string
	Connected        bool
	VolumeCount      int            // Number of PV-validated volumes in this masking view
	ProtocolCounts   map[string]int // Volume count per protocol (FC, ISCSI, NVMETCP, etc.)
}

// MaskingViewClient is the minimal interface for gopowermax masking view calls.
type MaskingViewClient interface {
	GetMaskingViews(ctx context.Context) ([]MaskingViewInfo, error)
}

// MaskingViewCollector collects PowerMax masking view metrics.
type MaskingViewCollector struct {
	client     MaskingViewClient
	arrayID    string
	totalGauge *prometheus.GaugeVec
	healthy    *prometheus.GaugeVec
	info       *prometheus.GaugeVec
}

// NewMaskingViewCollector creates a new MaskingViewCollector.
func NewMaskingViewCollector(client MaskingViewClient, reg prometheus.Registerer, arrayID string) *MaskingViewCollector {
	// Register metrics using helper function that handles AlreadyRegisteredError
	totalGauge := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_maskingview_total",
		Help: "Total masking views on the array.",
	}, []string{"array_id"}))

	healthy := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_maskingview_healthy",
		Help: "Masking view health (1=healthy, 0=unhealthy).",
	}, []string{"array_id", "masking_view"}))

	info := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_maskingview_info",
		Help: "Masking view topology information.",
	}, []string{"array_id", "masking_view", "storage_group", "port_group", "initiator_group"}))

	return &MaskingViewCollector{
		client:     client,
		arrayID:    arrayID,
		totalGauge: totalGauge,
		healthy:    healthy,
		info:       info,
	}
}

// Collect fetches masking view metrics.
func (c *MaskingViewCollector) Collect(ctx context.Context) error {
	views, err := c.client.GetMaskingViews(ctx)
	if err != nil {
		return fmt.Errorf("MaskingViewCollector: failed to get Masking Views: %w", err)
	}

	// Track total masking views
	totalViews := 0

	for _, mv := range views {
		healthyVal := 0.0
		if mv.Connected {
			healthyVal = 1.0
		}

		// Emit metrics per masking view (not per protocol)
		c.healthy.WithLabelValues(c.arrayID, mv.MaskingViewID).Set(healthyVal)
		c.info.WithLabelValues(c.arrayID, mv.MaskingViewID, mv.StorageGroupID, mv.PortGroupID, mv.InitiatorGroupID).Set(1.0)
		totalViews++
	}

	// Set total masking view count
	c.totalGauge.WithLabelValues(c.arrayID).Set(float64(totalViews))

	return nil
}

// Name returns the collector name.
func (c *MaskingViewCollector) Name() string { return "MaskingViewCollector" }
