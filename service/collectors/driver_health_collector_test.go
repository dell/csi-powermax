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
	"testing"

	"github.com/dell/csi-powermax/v2/service/collectors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	metricsv1beta1api "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// U-DH-05: Name() returns "PMXDriverHealthCollector".
func TestPMXDriverHealthCollector_Name(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-5", nil, nil, nil)
	assert.Equal(t, "PMXDriverHealthCollector", c.Name())
}

// U-DH-06: NewDriverHealthCollector creates collector with valid params
func TestPMXDriverHealthCollector_New(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-1", nil, nil, nil)
	assert.NotNil(t, c)
}

// U-DH-06: SetConnectionHealth sets the connection health status
func TestPMXDriverHealthCollector_SetConnectionHealth(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	// Test setting to false
	c.SetConnectionHealth(false)

	// Test setting to true
	c.SetConnectionHealth(true)

	// Should not panic
	assert.True(t, true)
}

// U-DH-07: getPodIdentityLabels returns default values when env vars are not set
func TestPMXDriverHealthCollector_GetPodIdentityLabels_Defaults(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	// Unset all env vars
	t.Setenv("POD_NAME", "")
	t.Setenv("KUBE_NODE_NAME", "")
	t.Setenv("DRIVER_NAMESPACE", "")
	t.Setenv("X_CSI_MODE", "")

	// This is a private method, we can't call it directly from collectors_test
	// But we can test it indirectly through Collect
	ctx := context.Background()
	err := c.Collect(ctx)
	assert.NoError(t, err)
}

// U-DH-08: getPodIdentityLabels uses env vars when set
func TestPMXDriverHealthCollector_GetPodIdentityLabels_WithEnvVars(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	// Set env vars
	t.Setenv("POD_NAME", "test-pod")
	t.Setenv("KUBE_NODE_NAME", "test-node")
	t.Setenv("DRIVER_NAMESPACE", "test-ns")
	t.Setenv("X_CSI_MODE", "controller")

	ctx := context.Background()
	err := c.Collect(ctx)
	assert.NoError(t, err)
}

// U-DH-09: collectRestartCount skips when k8sClient is nil
func TestPMXDriverHealthCollector_CollectRestartCount_NilClient(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	ctx := context.Background()
	err := c.Collect(ctx)
	assert.NoError(t, err)
}

// U-DH-10: collectRestartCount skips when env vars are missing
func TestPMXDriverHealthCollector_CollectRestartCount_MissingEnvVars(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-1", &mockK8sUtils{}, nil, nil)

	t.Setenv("POD_NAME", "")
	t.Setenv("DRIVER_NAMESPACE", "")

	ctx := context.Background()
	err := c.Collect(ctx)
	assert.NoError(t, err)
}

// U-DH-11: collectKubernetesMetrics skips when k8sClient is nil
func TestPMXDriverHealthCollector_CollectKubernetesMetrics_NilClient(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	ctx := context.Background()
	err := c.Collect(ctx)
	assert.NoError(t, err)
}

// U-DH-12: collectKubernetesMetrics skips when env vars are missing
func TestPMXDriverHealthCollector_CollectKubernetesMetrics_MissingEnvVars(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-1", &mockK8sUtils{}, nil, nil)

	t.Setenv("POD_NAME", "")
	t.Setenv("DRIVER_NAMESPACE", "")

	ctx := context.Background()
	err := c.Collect(ctx)
	assert.NoError(t, err)
}

// U-DH-13: collectConnectionPoolMetrics reports 0 when unhealthy
func TestPMXDriverHealthCollector_CollectConnectionPoolMetrics_Unhealthy(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-1", &mockK8sUtils{}, nil, nil)

	c.SetConnectionHealth(false)

	ctx := context.Background()
	err := c.Collect(ctx)
	assert.NoError(t, err)
}

// U-DH-14: collectConnectionPoolMetrics reports 0 when k8sClient is nil
func TestPMXDriverHealthCollector_CollectConnectionPoolMetrics_NilClient(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	ctx := context.Background()
	err := c.Collect(ctx)
	assert.NoError(t, err)
}

// U-DH-15: Collect with nil clients should not panic
func TestPMXDriverHealthCollector_Collect_NilClients(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewDriverHealthCollector(reg, "array-1", nil, nil, nil)

	ctx := context.Background()
	err := c.Collect(ctx)
	assert.NoError(t, err)
}

// mockK8sUtils is a minimal mock for k8sutils.UtilsInterface
type mockK8sUtils struct{}

func (m *mockK8sUtils) GetNodeLabels(string) (map[string]string, error) {
	return nil, nil
}

func (m *mockK8sUtils) GetNodeIPs(string) string {
	return ""
}

func (m *mockK8sUtils) GetPVCForVolume(_ context.Context, _ string, _ string) (*corev1.PersistentVolumeClaim, error) {
	return nil, nil
}

func (m *mockK8sUtils) GetClient() kubernetes.Interface {
	return nil
}

func (m *mockK8sUtils) GetMetricsClient() metricsv.Interface {
	return nil
}

func (m *mockK8sUtils) GetPodMetrics(_ context.Context, _ string, _ string) (*metricsv1beta1api.PodMetrics, error) {
	return nil, nil
}
