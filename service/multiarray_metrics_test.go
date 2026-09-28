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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

// gatherValue returns the value of the metric named name with exactly the
// provided labels. For histograms it returns the sample count. The second
// return is false when no such series exists yet.
func gatherValue(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	mfs, err := reg.Gather()
	assert.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			got := map[string]string{}
			for _, l := range m.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			if !labelsEqual(got, labels) {
				continue
			}
			switch {
			case m.Gauge != nil:
				return m.GetGauge().GetValue(), true
			case m.Counter != nil:
				return m.GetCounter().GetValue(), true
			case m.Histogram != nil:
				return float64(m.GetHistogram().GetSampleCount()), true
			}
		}
	}
	return 0, false
}

func labelsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range b {
		if a[k] != v {
			return false
		}
	}
	return true
}

func newTestMetrics() (*multiArrayMetrics, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	return newMultiArrayMetrics(reg), reg
}

// --- countMultiArrayZones --------------------------------------------------

func TestCountMultiArrayZones(t *testing.T) {
	zoneLabel := func(z string) StorageArrayConfig {
		return StorageArrayConfig{Labels: map[string]interface{}{zoneLabelKey: z}}
	}
	tests := []struct {
		name string
		in   map[string]StorageArrayConfig
		want int
	}{
		{"empty", map[string]StorageArrayConfig{}, 0},
		{"single array one zone", map[string]StorageArrayConfig{"a": zoneLabel("z1")}, 0},
		{"two arrays same zone", map[string]StorageArrayConfig{"a": zoneLabel("z1"), "b": zoneLabel("z1")}, 1},
		{"two zones one multi", map[string]StorageArrayConfig{
			"a": zoneLabel("z1"), "b": zoneLabel("z1"), "c": zoneLabel("z2"),
		}, 1},
		{"two multi zones", map[string]StorageArrayConfig{
			"a": zoneLabel("z1"), "b": zoneLabel("z1"), "c": zoneLabel("z2"), "d": zoneLabel("z2"),
		}, 2},
		{"unzoned arrays ignored", map[string]StorageArrayConfig{
			"a": {}, "b": {},
		}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, countMultiArrayZones(tt.in))
		})
	}
}

func TestZoneForArray(t *testing.T) {
	s := &service{}
	s.opts.StorageArrays = map[string]StorageArrayConfig{
		"array1": {Labels: map[string]interface{}{zoneLabelKey: "us-east-1a"}},
		"array2": {Labels: map[string]interface{}{"other": "x"}},
	}
	assert.Equal(t, "us-east-1a", s.zoneForArray("array1"))
	assert.Equal(t, "", s.zoneForArray("array2"))
	assert.Equal(t, "", s.zoneForArray("missing"))
}

// --- metric registration and updates ---------------------------------------

func TestNewMultiArrayMetrics_RegistersAllFive(t *testing.T) {
	m, reg := newTestMetrics()
	assert.NotNil(t, m)

	// Touch each metric so a series is emitted, then confirm presence.
	m.incProvisioning("z1", "a")
	m.setArrayCapacity("z1", "a", 55.5)
	m.setArrayAvailable("z1", "a", true)
	m.observeSelectionLatency("z1", 2*time.Millisecond)
	m.setMultiArrayZones(3)

	v, ok := gatherValue(t, reg, "dell_csi_provisioning_total", map[string]string{"zone": "z1", "array": "a"})
	assert.True(t, ok)
	assert.Equal(t, 1.0, v)

	v, ok = gatherValue(t, reg, "dell_csi_array_capacity_utilization", map[string]string{"zone": "z1", "array": "a"})
	assert.True(t, ok)
	assert.Equal(t, 55.5, v)

	v, ok = gatherValue(t, reg, "dell_csi_array_available", map[string]string{"zone": "z1", "array": "a"})
	assert.True(t, ok)
	assert.Equal(t, 1.0, v)

	v, ok = gatherValue(t, reg, "dell_csi_array_selection_latency_seconds", map[string]string{"zone": "z1"})
	assert.True(t, ok)
	assert.Equal(t, 1.0, v) // one observation

	v, ok = gatherValue(t, reg, "dell_csi_multiarray_zones_total", map[string]string{})
	assert.True(t, ok)
	assert.Equal(t, 3.0, v)
}

func TestNewMultiArrayMetrics_IdempotentRegistration(t *testing.T) {
	reg := prometheus.NewRegistry()
	m1 := newMultiArrayMetrics(reg)
	m2 := newMultiArrayMetrics(reg)
	// Both must be usable against the same registry without a duplicate
	// registration panic; updates accumulate on the shared series.
	m1.incProvisioning("z1", "a")
	m2.incProvisioning("z1", "a")
	v, ok := gatherValue(t, reg, "dell_csi_provisioning_total", map[string]string{"zone": "z1", "array": "a"})
	assert.True(t, ok)
	assert.Equal(t, 2.0, v)
}

func TestArrayAvailable_ZeroWhenUnavailable(t *testing.T) {
	m, reg := newTestMetrics()
	m.setArrayAvailable("z1", "a", false)
	v, ok := gatherValue(t, reg, "dell_csi_array_available", map[string]string{"zone": "z1", "array": "a"})
	assert.True(t, ok)
	assert.Equal(t, 0.0, v)
}

func TestMultiArrayMetrics_NilReceiverIsNoOp(t *testing.T) {
	var m *multiArrayMetrics
	assert.NotPanics(t, func() {
		m.incProvisioning("z", "a")
		m.setArrayCapacity("z", "a", 1)
		m.setArrayAvailable("z", "a", true)
		m.observeSelectionLatency("z", time.Second)
		m.setMultiArrayZones(1)
	})
}

// --- syncCapacityMetricsFromCache ------------------------------------------

func TestSyncCapacityMetricsFromCache(t *testing.T) {
	m, reg := newTestMetrics()
	s := &service{multiArrayMetrics: m, capCache: newCapacityCache()}
	s.opts.StorageArrays = map[string]StorageArrayConfig{
		"array1": {Labels: map[string]interface{}{zoneLabelKey: "z1"}},
		"array2": {Labels: map[string]interface{}{zoneLabelKey: "z1"}},
	}
	now := time.Now()
	s.capCache.recordPollSuccess("array1", 30, now)
	s.capCache.recordPollFailure("array2", time.Minute, now) // no prior success -> unavailable

	s.syncCapacityMetricsFromCache([]string{"array1", "array2"})

	util, ok := gatherValue(t, reg, "dell_csi_array_capacity_utilization", map[string]string{"zone": "z1", "array": "array1"})
	assert.True(t, ok)
	assert.Equal(t, 30.0, util)

	avail1, _ := gatherValue(t, reg, "dell_csi_array_available", map[string]string{"zone": "z1", "array": "array1"})
	assert.Equal(t, 1.0, avail1)
	avail2, _ := gatherValue(t, reg, "dell_csi_array_available", map[string]string{"zone": "z1", "array": "array2"})
	assert.Equal(t, 0.0, avail2)
}

func TestSyncCapacityMetricsFromCache_NoMetricsIsNoOp(t *testing.T) {
	s := &service{capCache: newCapacityCache()}
	assert.NotPanics(t, func() { s.syncCapacityMetricsFromCache([]string{"array1"}) })
}

// --- selectArray metric + reason integration -------------------------------

func TestSelectArray_ObservesLatencyAndLogsSelection(t *testing.T) {
	m, reg := newTestMetrics()
	s := &service{multiArrayMetrics: m, capCache: newCapacityCache(), capacityThresholdFull: 100}
	now := time.Now()
	s.capCache.recordPollSuccess("array1", 80, now)
	s.capCache.recordPollSuccess("array2", 20, now)

	selected, err := s.selectArray("z1", []string{"array1", "array2"})
	assert.NoError(t, err)
	assert.Equal(t, "array2", selected) // lowest utilization

	cnt, ok := gatherValue(t, reg, "dell_csi_array_selection_latency_seconds", map[string]string{"zone": "z1"})
	assert.True(t, ok)
	assert.Equal(t, 1.0, cnt)
}

func TestSelectArray_AllUnavailableObservesLatency(t *testing.T) {
	m, reg := newTestMetrics()
	s := &service{multiArrayMetrics: m, capCache: newCapacityCache(), capacityThresholdFull: 100}
	// Mark both arrays unavailable.
	s.capCache.invalidate("array1")
	s.capCache.invalidate("array2")

	_, err := s.selectArray("z1", []string{"array1", "array2"})
	assert.Error(t, err)

	cnt, ok := gatherValue(t, reg, "dell_csi_array_selection_latency_seconds", map[string]string{"zone": "z1"})
	assert.True(t, ok)
	assert.Equal(t, 1.0, cnt)
}

func TestSelectArray_CapacityExhaustedSkipClassified(t *testing.T) {
	// Not a metric assertion (skips are logged), but exercises the
	// capacity_exhausted classification branch: an available array at/above
	// threshold that is not selected.
	m, _ := newTestMetrics()
	s := &service{multiArrayMetrics: m, capCache: newCapacityCache(), capacityThresholdFull: 90}
	now := time.Now()
	s.capCache.recordPollSuccess("full", 95, now)
	s.capCache.recordPollSuccess("ok", 10, now)

	selected, err := s.selectArray("z1", []string{"full", "ok"})
	assert.NoError(t, err)
	assert.Equal(t, "ok", selected)
}
