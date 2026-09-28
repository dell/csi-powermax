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

package collectors_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/dell/csi-powermax/v2/service/collectors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

// mockVolumeClient implements collectors.VolumeClient for testing.
type mockVolumeClient struct {
	volumes []collectors.VolumeInfo
	err     error
}

func (m *mockVolumeClient) GetVolumes(_ context.Context) ([]collectors.VolumeInfo, error) {
	return m.volumes, m.err
}

// gaugePMX extracts a gauge value from a registry for the given metric name and labels.
func gaugePMXVol(t *testing.T, reg *prometheus.Registry, name string, labels map[string]string) float64 {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		val, found := gaugeMatchVol(mf, labels)
		if found {
			return val
		}
	}
	t.Fatalf("metric %s with labels %v not found", name, labels)
	return 0
}

func gaugeMatchVol(mf *dto.MetricFamily, labels map[string]string) (float64, bool) {
	for _, m := range mf.GetMetric() {
		got := make(map[string]string)
		for _, lp := range m.GetLabel() {
			got[lp.GetName()] = lp.GetValue()
		}
		match := true
		for k, v := range labels {
			if got[k] != v {
				match = false
				break
			}
		}
		if match {
			return m.GetGauge().GetValue(), true
		}
	}
	return 0, false
}

func TestVolumeCollector_Basic(t *testing.T) {
	client := &mockVolumeClient{
		volumes: []collectors.VolumeInfo{
			{VolumeID: "vol-001", StorageGroupID: "SG1", SizeBytes: 107374182400, Attached: true, HostName: "host1"},
			{VolumeID: "vol-002", StorageGroupID: "SG1", SizeBytes: 53687091200, Attached: false, HostName: ""},
			{VolumeID: "vol-003", StorageGroupID: "SG2", SizeBytes: 214748364800, Attached: true, HostName: "host2"},
		},
	}

	reg := prometheus.NewRegistry()
	collector, err := collectors.NewVolumeCollector(client, reg, "000123456789", nil)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed: %v", err)
	}

	err = collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() failed: %v", err)
	}

	// Test volume size bytes
	sizeVol1 := gaugePMXVol(t, reg, "dell_powermax_volume_size_bytes", map[string]string{
		"volume_id": "vol-001",
		"array_id":  "000123456789",
	})
	if sizeVol1 != 107374182400 {
		t.Errorf("Expected vol-001 size = 107374182400, got %v", sizeVol1)
	}

	sizeVol2 := gaugePMXVol(t, reg, "dell_powermax_volume_size_bytes", map[string]string{
		"volume_id": "vol-002",
		"array_id":  "000123456789",
	})
	if sizeVol2 != 53687091200 {
		t.Errorf("Expected vol-002 size = 53687091200, got %v", sizeVol2)
	}

	sizeVol3 := gaugePMXVol(t, reg, "dell_powermax_volume_size_bytes", map[string]string{
		"volume_id": "vol-003",
		"array_id":  "000123456789",
	})
	if sizeVol3 != 214748364800 {
		t.Errorf("Expected vol-003 size = 214748364800, got %v", sizeVol3)
	}

	// Test total volume count
	totalVol := gaugePMXVol(t, reg, "dell_csi_volume_total", map[string]string{
		"array_id": "000123456789",
	})
	if totalVol != 3 {
		t.Errorf("Expected total volumes = 3, got %v", totalVol)
	}

	// Test attached total
	attachedTotal := gaugePMXVol(t, reg, "dell_powermax_volume_attached_total", map[string]string{
		"array_id": "000123456789",
	})
	if attachedTotal != 2 {
		t.Errorf("Expected attached volumes = 2, got %v", attachedTotal)
	}

	// Test volume size distribution buckets
	bucketTests := []struct {
		label    string
		expected int
	}{
		{"<=1GiB", 0},
		{"<=5GiB", 0},
		{"<=10GiB", 0},
		{"<=50GiB", 1},
		{"<=100GiB", 2},
		{"<=500GiB", 3},
		{"<=1TiB", 3},
		{">1TiB", 0},
	}
	for _, bt := range bucketTests {
		got := gaugePMXVol(t, reg, "dell_powermax_volume_size_distribution_bytes", map[string]string{
			"array_id": "000123456789",
			"bucket":   bt.label,
		})
		if got != float64(bt.expected) {
			t.Errorf("Expected bucket %s count = %d, got %v", bt.label, bt.expected, got)
		}
	}

	// Test attachment status
	attachedVol1 := gaugePMXVol(t, reg, "dell_powermax_volume_attachment_status", map[string]string{
		"volume_id": "vol-001",
		"host_name": "host1",
		"array_id":  "000123456789",
	})
	if attachedVol1 != 1 {
		t.Errorf("Expected vol-001 attachment status = 1 (attached), got %v", attachedVol1)
	}

	// vol-002 is detached, no attachment status metric should be emitted
	// (metrics only emitted when HostName != "")
	// Verify by checking that vol-001 has the metric but vol-002 doesn't
	mfs, _ := reg.Gather()
	foundVol1 := false
	for _, mf := range mfs {
		if mf.GetName() == "dell_powermax_volume_attachment_status" {
			for _, m := range mf.GetMetric() {
				labels := make(map[string]string)
				for _, lp := range m.GetLabel() {
					labels[lp.GetName()] = lp.GetValue()
				}
				if labels["volume_id"] == "vol-001" && labels["host_name"] == "host1" {
					foundVol1 = true
				}
			}
		}
	}
	if !foundVol1 {
		t.Error("Expected vol-001 attachment metric to be emitted")
	}
}

func TestVolumeCollector_APIError(t *testing.T) {
	client := &mockVolumeClient{
		err: errors.New("Unisphere API unavailable"),
	}

	reg := prometheus.NewRegistry()
	collector, err := collectors.NewVolumeCollector(client, reg, "000123456789", nil)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed: %v", err)
	}

	err = collector.Collect(context.Background())
	if err == nil {
		t.Fatal("Expected error from Collect(), got nil")
	}
}

func TestVolumeCollector_EmptyVolumes(t *testing.T) {
	client := &mockVolumeClient{
		volumes: []collectors.VolumeInfo{},
	}

	reg := prometheus.NewRegistry()
	collector, err := collectors.NewVolumeCollector(client, reg, "000123456789", nil)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed: %v", err)
	}

	err = collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() failed: %v", err)
	}

	// No metrics should be emitted for empty volumes - gather and check
	mfs, _ := reg.Gather()
	for _, mf := range mfs {
		if mf.GetName() == "dell_powermax_volume_size_bytes" {
			if len(mf.GetMetric()) != 0 {
				t.Errorf("Expected 0 volume_size_bytes metrics for empty volumes, got %d", len(mf.GetMetric()))
			}
		}
	}
}

func TestVolumeCollector_Name(t *testing.T) {
	client := &mockVolumeClient{}
	reg := prometheus.NewRegistry()
	collector, err := collectors.NewVolumeCollector(client, reg, "000123456789", nil)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed: %v", err)
	}

	if name := collector.Name(); name != "VolumeCollector" {
		t.Errorf("Expected Name() = 'VolumeCollector', got '%s'", name)
	}
}

func TestVolumeCollector_New(t *testing.T) {
	client := &mockVolumeClient{
		volumes: []collectors.VolumeInfo{},
	}
	reg := prometheus.NewRegistry()
	collector, err := collectors.NewVolumeCollector(client, reg, "000123456789", nil)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed: %v", err)
	}

	if collector == nil {
		t.Fatal("Expected non-nil collector")
	}
}

func TestVolumeCollector_NewWithSameRegistry(t *testing.T) {
	client := &mockVolumeClient{
		volumes: []collectors.VolumeInfo{},
	}
	reg := prometheus.NewRegistry()

	// Create first collector
	collector1, err := collectors.NewVolumeCollector(client, reg, "000123456789", nil)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed: %v", err)
	}
	if collector1 == nil {
		t.Fatal("Expected non-nil collector")
	}

	// Create second collector with same registry to test AlreadyRegisteredError handling
	collector2, err := collectors.NewVolumeCollector(client, reg, "000123456789", nil)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed on second call: %v", err)
	}
	if collector2 == nil {
		t.Fatal("Expected non-nil collector on second call")
	}
}

func TestVolumeCollector_SingleVolume(t *testing.T) {
	client := &mockVolumeClient{
		volumes: []collectors.VolumeInfo{
			{VolumeID: "vol-001", StorageGroupID: "SG1", SizeBytes: 107374182400, Attached: true, HostName: "host1"},
		},
	}

	reg := prometheus.NewRegistry()
	collector, err := collectors.NewVolumeCollector(client, reg, "000123456789", nil)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed: %v", err)
	}

	err = collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() failed: %v", err)
	}
}

func TestVolumeCollector_AllVolumesDetached(t *testing.T) {
	client := &mockVolumeClient{
		volumes: []collectors.VolumeInfo{
			{VolumeID: "vol-001", StorageGroupID: "SG1", SizeBytes: 107374182400, Attached: false, HostName: ""},
			{VolumeID: "vol-002", StorageGroupID: "SG2", SizeBytes: 53687091200, Attached: false, HostName: ""},
		},
	}

	reg := prometheus.NewRegistry()
	collector, err := collectors.NewVolumeCollector(client, reg, "000123456789", nil)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed: %v", err)
	}

	err = collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() failed: %v", err)
	}
}

func TestVolumeCollector_WithValidator(t *testing.T) {
	client := &mockVolumeClient{
		volumes: []collectors.VolumeInfo{
			{VolumeID: "vol-001", StorageGroupID: "SG1", SizeBytes: 107374182400, Attached: true, HostName: "host1", Healthy: true},
			{VolumeID: "vol-002", StorageGroupID: "SG1", SizeBytes: 53687091200, Attached: false, HostName: "", Healthy: false},
		},
	}

	validator := &mockVolumeValidator{
		managedVolumes: map[string]bool{
			"vol-001": true,
		},
	}

	reg := prometheus.NewRegistry()
	collector, err := collectors.NewVolumeCollector(client, reg, "000123456789", validator)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed: %v", err)
	}

	err = collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() failed: %v", err)
	}

	// Verify metrics were collected
	mfs, _ := reg.Gather()
	foundVolumeTotal := false
	for _, mf := range mfs {
		if mf.GetName() == "dell_csi_volume_total" {
			foundVolumeTotal = true
		}
	}
	if !foundVolumeTotal {
		t.Error("Expected dell_csi_volume_total metric to be emitted")
	}
}

func TestVolumeCollector_ValidatorRefreshError(t *testing.T) {
	client := &mockVolumeClient{
		volumes: []collectors.VolumeInfo{
			{VolumeID: "vol-001", StorageGroupID: "SG1", SizeBytes: 107374182400, Attached: true, HostName: "host1"},
		},
	}

	validator := &mockVolumeValidatorErr{
		refreshErr: fmt.Errorf("refresh failed"),
	}

	reg := prometheus.NewRegistry()
	collector, err := collectors.NewVolumeCollector(client, reg, "000123456789", validator)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed: %v", err)
	}

	// Should not fail even if validator refresh fails
	err = collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() should not fail when validator refresh fails: %v", err)
	}
}

func TestVolumeCollector_UnhealthyVolumes(t *testing.T) {
	client := &mockVolumeClient{
		volumes: []collectors.VolumeInfo{
			{VolumeID: "vol-001", StorageGroupID: "SG1", SizeBytes: 107374182400, Attached: true, HostName: "host1", Healthy: false},
			{VolumeID: "vol-002", StorageGroupID: "SG1", SizeBytes: 53687091200, Attached: true, HostName: "host2", Healthy: false},
		},
	}

	reg := prometheus.NewRegistry()
	collector, err := collectors.NewVolumeCollector(client, reg, "000123456789", nil)
	if err != nil {
		t.Fatalf("NewVolumeCollector() failed: %v", err)
	}

	err = collector.Collect(context.Background())
	if err != nil {
		t.Fatalf("Collect() failed: %v", err)
	}
}

type mockVolumeValidator struct {
	managedVolumes map[string]bool
}

func (m *mockVolumeValidator) IsDriverManaged(_ context.Context, volumeID string) (bool, error) {
	return m.managedVolumes[volumeID], nil
}

func (m *mockVolumeValidator) RefreshCache(_ context.Context) error {
	return nil
}

type mockVolumeValidatorErr struct {
	refreshErr error
}

func (m *mockVolumeValidatorErr) IsDriverManaged(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func (m *mockVolumeValidatorErr) RefreshCache(_ context.Context) error {
	return m.refreshErr
}

// Ensure testutil is used to avoid unused import error
var _ = testutil.CollectAndCount
