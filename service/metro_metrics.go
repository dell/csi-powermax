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

package service

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

var (
	metroMetricsOnce sync.Once

	// MetroDeferredOpsTotal counts deferred operations by type.
	MetroDeferredOpsTotal *prometheus.CounterVec
	// MetroDeferredOpsQueueDepth is the current queue depth per Metro array pair.
	MetroDeferredOpsQueueDepth *prometheus.GaugeVec
	// MetroDegradedVolumes is the current count of degraded (non-replicated) volumes per Metro array pair.
	MetroDegradedVolumes *prometheus.GaugeVec
	// MetroReconciliationTotal counts reconciliation attempts by result.
	MetroReconciliationTotal *prometheus.CounterVec
	// MetroSiteFailures counts detected site failure events.
	MetroSiteFailures *prometheus.CounterVec
	// MetroReconcileDuration observes how long each reconciliation run takes per array pair and result.
	MetroReconcileDuration *prometheus.HistogramVec
)

// metroMetrics holds a complete set of Metro site-failure Prometheus metrics.
type metroMetrics struct {
	deferredOpsTotal  *prometheus.CounterVec
	queueDepth        *prometheus.GaugeVec
	degradedVolumes   *prometheus.GaugeVec
	reconcileTotal    *prometheus.CounterVec
	siteFailures      *prometheus.CounterVec
	reconcileDuration *prometheus.HistogramVec
}

// newMetroMetrics creates a fresh, unregistered set of Metro metrics.
// All metric names follow the dell_csi_* naming convention so they appear
// alongside other driver metrics in Prometheus, Grafana, and alert rules.
// Callers are responsible for registering the metrics in a Prometheus registry.
func newMetroMetrics() *metroMetrics {
	return &metroMetrics{
		deferredOpsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "dell_csi_powermax_metro_deferred_ops_total",
				Help: "Total number of deferred operations created during Metro site failures.",
			},
			[]string{"operation_type"},
		),
		queueDepth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "dell_csi_powermax_metro_deferred_ops_queue_depth",
				Help: "Current number of pending deferred operations in the queue, labelled by Metro array pair.",
			},
			[]string{"local_array", "remote_array"},
		),
		degradedVolumes: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: "dell_csi_powermax_metro_degraded_volumes",
				Help: "Current number of volumes in degraded (non-replicated) state, labelled by Metro array pair.",
			},
			[]string{"local_array", "remote_array"},
		),
		reconcileTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "dell_csi_powermax_metro_reconciliation_total",
				Help: "Total number of reconciliation replay attempts.",
			},
			[]string{"result"},
		),
		siteFailures: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "dell_csi_powermax_metro_site_failures_total",
				Help: "Total number of Metro site failure detections.",
			},
			[]string{"array_id", "role"},
		),
		reconcileDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "dell_csi_powermax_metro_reconcile_duration_seconds",
				Help:    "Duration in seconds of Metro reconciliation runs, labelled by array pair and result. Supports NFR-3 SLO verification.",
				Buckets: []float64{1, 5, 15, 30, 60, 120, 180, 240, 300},
			},
			[]string{"local_array", "remote_array", "result"},
		),
	}
}

// RegisterMetroMetrics registers Prometheus metrics for Metro site-failure
// handling with the driver's metrics registry. Safe to call multiple times;
// only the first call has effect (global package-level vars are set once).
func RegisterMetroMetrics(registry *prometheus.Registry) {
	metroMetricsOnce.Do(func() {
		m := newMetroMetrics()
		MetroDeferredOpsTotal = m.deferredOpsTotal
		MetroDeferredOpsQueueDepth = m.queueDepth
		MetroDegradedVolumes = m.degradedVolumes
		MetroReconciliationTotal = m.reconcileTotal
		MetroSiteFailures = m.siteFailures
		MetroReconcileDuration = m.reconcileDuration

		registry.MustRegister(
			MetroDeferredOpsTotal,
			MetroDeferredOpsQueueDepth,
			MetroDegradedVolumes,
			MetroReconciliationTotal,
			MetroSiteFailures,
			MetroReconcileDuration,
		)
	})
}
