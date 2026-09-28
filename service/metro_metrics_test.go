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
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// registerFreshMetrics creates a new metrics set and registers it into a fresh
// registry. Each test call produces an isolated set of Prometheus metrics with
// no dependency on other tests or the package-level sync.Once.
func registerFreshMetrics(t *testing.T) (*prometheus.Registry, *metroMetrics) {
	t.Helper()
	registry := prometheus.NewRegistry()
	m := newMetroMetrics()
	registry.MustRegister(
		m.deferredOpsTotal,
		m.queueDepth,
		m.degradedVolumes,
		m.reconcileTotal,
		m.siteFailures,
		m.reconcileDuration,
	)
	return registry, m
}

func TestRegisterMetroMetrics_NoPanic(t *testing.T) {
	registry, m := registerFreshMetrics(t)

	// Initialize counter vecs so they appear in Gather output.
	m.deferredOpsTotal.WithLabelValues("test").Add(0)
	m.reconcileTotal.WithLabelValues("test").Add(0)
	m.siteFailures.WithLabelValues("test", "test").Add(0)
	m.queueDepth.WithLabelValues("array-A", "array-B").Set(0)
	m.degradedVolumes.WithLabelValues("array-A", "array-B").Set(0)
	m.reconcileDuration.WithLabelValues("array-A", "array-B", "success").Observe(1.0)

	families, err := registry.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	expectedNames := map[string]bool{
		"dell_csi_powermax_metro_deferred_ops_total":         false,
		"dell_csi_powermax_metro_deferred_ops_queue_depth":   false,
		"dell_csi_powermax_metro_degraded_volumes":           false,
		"dell_csi_powermax_metro_reconciliation_total":       false,
		"dell_csi_powermax_metro_site_failures_total":        false,
		"dell_csi_powermax_metro_reconcile_duration_seconds": false,
	}

	for _, f := range families {
		if _, ok := expectedNames[f.GetName()]; ok {
			expectedNames[f.GetName()] = true
		}
	}

	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected metric %s not found in registry", name)
		}
	}
}

func TestMetroDeferredOpsTotal_Increment(t *testing.T) {
	_, m := registerFreshMetrics(t)

	m.deferredOpsTotal.WithLabelValues("DeviceCleanup").Inc()
	m.deferredOpsTotal.WithLabelValues("DeviceCleanup").Inc()
	m.deferredOpsTotal.WithLabelValues("MetroPairing").Inc()

	var mtr dto.Metric
	if err := m.deferredOpsTotal.WithLabelValues("DeviceCleanup").Write(&mtr); err != nil {
		t.Fatalf("failed to write metric: %v", err)
	}
	if got := mtr.GetCounter().GetValue(); got != 2 {
		t.Errorf("expected DeviceCleanup count 2, got %v", got)
	}
}

func TestMetroDeferredOpsQueueDepth_SetGet(t *testing.T) {
	_, m := registerFreshMetrics(t)

	m.queueDepth.WithLabelValues("array-A", "array-B").Set(42)
	var mtr dto.Metric
	if err := m.queueDepth.WithLabelValues("array-A", "array-B").Write(&mtr); err != nil {
		t.Fatalf("failed to write metric: %v", err)
	}
	if got := mtr.GetGauge().GetValue(); got != 42 {
		t.Errorf("expected queue depth 42, got %v", got)
	}
}
