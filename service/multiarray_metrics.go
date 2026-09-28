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

// Package service implements the observable per-array Prometheus metrics.
// The five metrics are registered against the driver's existing
// DriverMetricsRegistry() (see metrics.go) so they are exposed on the
// driver's existing metrics endpoint; no new server or scrape target is
// introduced. All updates are nil-guarded so that when metrics are
// disabled (metricsEnabled() == false) the selection and capacity-poll hot
// paths incur no cost beyond a nil check.
package service

import (
	"errors"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// zoneLabelKey is the well-known Kubernetes topology zone label used to group
// PowerMax arrays into zones.
const zoneLabelKey = "topology.kubernetes.io/zone"

// Failover / skip reason vocabulary fixed by the telemetry requirement.
// These exact strings appear in operational logs.
const (
	reasonArrayUnreachable  = "array_unreachable"
	reasonCapacityExhausted = "capacity_exhausted"
)

// multiArrayMetrics holds the five FR-3 Prometheus collectors. A nil
// *multiArrayMetrics is a valid no-op receiver, used when metrics are
// disabled.
type multiArrayMetrics struct {
	provisioningTotal   *prometheus.CounterVec   // {zone, array}
	capacityUtilization *prometheus.GaugeVec     // {zone, array}
	arrayAvailable      *prometheus.GaugeVec     // {zone, array}
	selectionLatency    *prometheus.HistogramVec // {zone}
	multiArrayZones     prometheus.Gauge         // driver-wide
}

// registerOrExisting registers c with reg, returning the previously
// registered collector of the same type if registration reports an
// AlreadyRegisteredError. This mirrors the idempotent-registration pattern in
// service/collectors/registry_helpers.go and lets newMultiArrayMetrics be
// called safely more than once against the singleton registry (e.g. across
// tests).
func registerOrExisting[T prometheus.Collector](reg prometheus.Registerer, c T) T {
	if err := reg.Register(c); err != nil {
		var alreadyRegistered prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegistered) {
			if existing, ok := alreadyRegistered.ExistingCollector.(T); ok {
				return existing
			}
		}
	}
	return c
}

// newMultiArrayMetrics constructs and registers the five FR-3 metrics against
// reg.
func newMultiArrayMetrics(reg prometheus.Registerer) *multiArrayMetrics {
	return &multiArrayMetrics{
		provisioningTotal: registerOrExisting(reg, prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dell_csi_provisioning_total",
			Help: "Count of provisioning operations per array per zone.",
		}, []string{"zone", "array"})),
		capacityUtilization: registerOrExisting(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "dell_csi_array_capacity_utilization",
			Help: "Current free-physical-capacity utilization percentage per array per zone.",
		}, []string{"zone", "array"})),
		arrayAvailable: registerOrExisting(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "dell_csi_array_available",
			Help: "Array availability (1=available, 0=unavailable) per array per zone.",
		}, []string{"zone", "array"})),
		selectionLatency: registerOrExisting(reg, prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "dell_csi_array_selection_latency_seconds",
			Help:    "Wall-clock duration of capacity-based array selection per zone (excludes Unisphere API calls).",
			Buckets: prometheus.DefBuckets,
		}, []string{"zone"})),
		multiArrayZones: registerOrExisting(reg, prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "dell_csi_multiarray_zones_total",
			Help: "Number of zones currently configured with more than one array.",
		})),
	}
}

func (m *multiArrayMetrics) observeSelectionLatency(zone string, d time.Duration) {
	if m == nil {
		return
	}
	m.selectionLatency.WithLabelValues(zone).Observe(d.Seconds())
}

func (m *multiArrayMetrics) incProvisioning(zone, array string) {
	if m == nil {
		return
	}
	m.provisioningTotal.WithLabelValues(zone, array).Inc()
}

func (m *multiArrayMetrics) setArrayCapacity(zone, array string, utilizationPercent float64) {
	if m == nil {
		return
	}
	m.capacityUtilization.WithLabelValues(zone, array).Set(utilizationPercent)
}

func (m *multiArrayMetrics) setArrayAvailable(zone, array string, available bool) {
	if m == nil {
		return
	}
	v := 0.0
	if available {
		v = 1.0
	}
	m.arrayAvailable.WithLabelValues(zone, array).Set(v)
}

func (m *multiArrayMetrics) setMultiArrayZones(n int) {
	if m == nil {
		return
	}
	m.multiArrayZones.Set(float64(n))
}

// zoneForArray returns the zone label value configured for arrayID, or the
// empty string if the array is not configured or has no zone label.
func (s *service) zoneForArray(arrayID string) string {
	cfg, ok := s.opts.StorageArrays[arrayID]
	if !ok {
		return ""
	}
	if z, ok := cfg.Labels[zoneLabelKey]; ok {
		if zs, ok := z.(string); ok {
			return zs
		}
	}
	return ""
}

// countMultiArrayZones returns the number of distinct zones that have more
// than one configured array (adoption gauge).
func countMultiArrayZones(storageArrays map[string]StorageArrayConfig) int {
	perZone := make(map[string]int)
	for _, cfg := range storageArrays {
		if z, ok := cfg.Labels[zoneLabelKey]; ok {
			if zs, ok := z.(string); ok && zs != "" {
				perZone[zs]++
			}
		}
	}
	multi := 0
	for _, c := range perZone {
		if c > 1 {
			multi++
		}
	}
	return multi
}

// syncCapacityMetricsFromCache refreshes the per-array capacity-utilization
// and availability gauges from the current capacity cache state. Called from
// the capacity poller goroutine after each poll cycle. No-op when metrics are
// disabled or the cache is not initialized.
func (s *service) syncCapacityMetricsFromCache(arrayIDs []string) {
	if s.multiArrayMetrics == nil || s.capCache == nil {
		return
	}
	for _, arrayID := range arrayIDs {
		utilizationPercent, available, _, known := s.capCache.snapshot(arrayID)
		if !known {
			continue
		}
		zone := s.zoneForArray(arrayID)
		s.multiArrayMetrics.setArrayCapacity(zone, arrayID, utilizationPercent)
		s.multiArrayMetrics.setArrayAvailable(zone, arrayID, available)
	}
}
