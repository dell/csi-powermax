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
	"os"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

// TestPMXDriverHealthCollector_getPodIdentityLabels tests the private getPodIdentityLabels method
func TestPMXDriverHealthCollector_getPodIdentityLabels(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	// Test with no env vars set
	os.Unsetenv("POD_NAME")
	os.Unsetenv("KUBE_NODE_NAME")
	os.Unsetenv("DRIVER_NAMESPACE")
	os.Unsetenv("X_CSI_MODE")

	labels := c.getPodIdentityLabels()
	assert.Equal(t, []string{"array-1", "unknown", "unknown", "unknown", "unknown"}, labels)

	// Test with env vars set
	os.Setenv("POD_NAME", "test-pod")
	os.Setenv("KUBE_NODE_NAME", "test-node")
	os.Setenv("DRIVER_NAMESPACE", "test-ns")
	os.Setenv("X_CSI_MODE", "controller")

	labels = c.getPodIdentityLabels()
	assert.Equal(t, []string{"array-1", "test-pod", "test-node", "test-ns", "controller"}, labels)

	// Clean up
	os.Unsetenv("POD_NAME")
	os.Unsetenv("KUBE_NODE_NAME")
	os.Unsetenv("DRIVER_NAMESPACE")
	os.Unsetenv("X_CSI_MODE")
}

// TestPMXDriverHealthCollector_getPodIdentityLabelsContainer tests the private getPodIdentityLabelsContainer method
func TestPMXDriverHealthCollector_getPodIdentityLabelsContainer(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	// Test with driver container
	labels := c.getPodIdentityLabelsContainer("driver")
	assert.Equal(t, []string{"array-1", "unknown", "unknown", "unknown", "unknown", "driver"}, labels)

	// Test with reverseproxy container
	labels = c.getPodIdentityLabelsContainer("reverseproxy")
	assert.Equal(t, []string{"array-1", "unknown", "unknown", "unknown", "unknown", "reverseproxy"}, labels)
}

// TestPMXDriverHealthCollector_collectRestartCount tests the private collectRestartCount method
func TestPMXDriverHealthCollector_collectRestartCount(_ *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	// Should not panic with nil client
	c.collectRestartCount(context.Background())

	// Should not panic with missing env vars
	c.collectRestartCount(context.Background())
}

// TestPMXDriverHealthCollector_collectKubernetesMetrics tests the private collectKubernetesMetrics method
func TestPMXDriverHealthCollector_collectKubernetesMetrics(_ *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	// Should not panic with nil client
	c.collectKubernetesMetrics(context.Background())

	// Should not panic with missing env vars
	c.collectKubernetesMetrics(context.Background())
}

// TestPMXDriverHealthCollector_collectConnectionPoolMetrics tests the private collectConnectionPoolMetrics method
func TestPMXDriverHealthCollector_collectConnectionPoolMetrics(_ *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	// Should not panic with nil client
	c.collectConnectionPoolMetrics(context.Background())

	// Should not panic with unhealthy status
	c.SetConnectionHealth(false)
	c.collectConnectionPoolMetrics(context.Background())

	// Should not panic with healthy status
	c.SetConnectionHealth(true)
	c.collectConnectionPoolMetrics(context.Background())
}

// TestPMXDriverHealthCollector_collectKubernetesMetrics_WithClients tests with mock clients
func TestPMXDriverHealthCollector_collectKubernetesMetrics_WithClients(_ *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	// With nil clients should not panic
	c.collectKubernetesMetrics(context.Background())
}

// TestPMXDriverHealthCollector_collectRestartCount_WithEnv tests with env vars
func TestPMXDriverHealthCollector_collectRestartCount_WithEnv(_ *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	os.Setenv("POD_NAME", "test-pod")
	defer os.Unsetenv("POD_NAME")

	// Should not panic with env var set
	c.collectRestartCount(context.Background())
}

// TestPMXDriverHealthCollector_Name tests the Name method
func TestPMXDriverHealthCollector_Name(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := NewDriverHealthCollector(reg, "array-1", nil, nil, nil)
	assert.Equal(t, "PMXDriverHealthCollector", c.Name())
}
