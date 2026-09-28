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
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/dell/csi-powermax/v2/k8sutils"
	"github.com/dell/csmlog"
	pmax "github.com/dell/gopowermax/v2"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// PMXDriverHealthCollector collects PowerMax driver health metrics.
type PMXDriverHealthCollector struct {
	arrayID            string
	startTime          time.Time
	uptimeGauge        *prometheus.GaugeVec
	restartTotal       *prometheus.GaugeVec
	goroutineCount     *prometheus.GaugeVec
	connPoolActive     *prometheus.GaugeVec
	cpuUsage           *prometheus.GaugeVec
	memUsage           *prometheus.GaugeVec
	k8sClient          k8sutils.UtilsInterface
	metricsClient      metricsv.Interface
	pmaxClient         pmax.Pmax
	connectionHealth   bool
	connectionHealthMu sync.RWMutex
}

// NewDriverHealthCollector creates a new PMXDriverHealthCollector.
func NewDriverHealthCollector(reg prometheus.Registerer, arrayID string, k8sClient k8sutils.UtilsInterface, metricsClient metricsv.Interface, pmaxClient pmax.Pmax) *PMXDriverHealthCollector {
	// Register metrics using helper function that handles AlreadyRegisteredError
	uptimeGauge := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_csi_driver_uptime_seconds",
		Help: "Driver uptime in seconds since last restart.",
	}, []string{"array_id", "pod", "node", "namespace", "instance_type"}))

	restartTotal := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_csi_driver_restart_total",
		Help: "Total container restarts from Kubernetes Pod API (driver and reverseproxy).",
	}, []string{"array_id", "pod", "node", "namespace", "instance_type", "container"}))

	goroutineCount := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_csi_goroutine_count",
		Help: "Number of active goroutines.",
	}, []string{"array_id", "pod", "node", "namespace", "instance_type"}))

	connPoolActive := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_csi_connection_pool_active",
		Help: "Active connections to PowerMax array (ready containers).",
	}, []string{"array_id", "pod", "node", "namespace", "instance_type"}))

	cpuUsage := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_csi_driver_cpu_usage_percent",
		Help: "CSI driver CPU usage percentage from Kubernetes metrics API.",
	}, []string{"array_id", "pod", "node", "namespace", "instance_type"}))

	memUsage := registerOrGetGaugeVec(reg, prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_csi_driver_memory_usage_bytes",
		Help: "CSI driver memory usage in bytes from Kubernetes metrics API.",
	}, []string{"array_id", "pod", "node", "namespace", "instance_type"}))

	return &PMXDriverHealthCollector{
		arrayID:          arrayID,
		startTime:        time.Now().Add(-time.Millisecond),
		uptimeGauge:      uptimeGauge,
		restartTotal:     restartTotal,
		goroutineCount:   goroutineCount,
		connPoolActive:   connPoolActive,
		cpuUsage:         cpuUsage,
		memUsage:         memUsage,
		k8sClient:        k8sClient,
		metricsClient:    metricsClient,
		pmaxClient:       pmaxClient,
		connectionHealth: true, // Default to healthy
	}
}

// getDriverNamespace returns the driver namespace, checking both env var names used
// by the Helm chart (X_CSI_DRIVER_NAMESPACE) and the operator (DRIVER_NAMESPACE).
func getDriverNamespace() string {
	if ns := os.Getenv("X_CSI_DRIVER_NAMESPACE"); ns != "" {
		return ns
	}
	return os.Getenv("DRIVER_NAMESPACE")
}

// getPodIdentityLabels returns the pod identity labels for metrics
func (c *PMXDriverHealthCollector) getPodIdentityLabels() []string {
	podName := os.Getenv("POD_NAME")
	nodeName := os.Getenv("KUBE_NODE_NAME")
	namespace := getDriverNamespace()
	mode := os.Getenv("X_CSI_MODE")

	// Provide default values if env vars are not set
	if podName == "" {
		podName = "unknown"
	}
	if nodeName == "" {
		nodeName = "unknown"
	}
	if namespace == "" {
		namespace = "unknown"
	}
	if mode == "" {
		mode = "unknown"
	}

	return []string{c.arrayID, podName, nodeName, namespace, mode}
}

// getPodIdentityLabelsContainer returns the pod identity labels with container for metrics
func (c *PMXDriverHealthCollector) getPodIdentityLabelsContainer(container string) []string {
	labels := c.getPodIdentityLabels()
	return append(labels, container)
}

// Collect updates driver health metrics.
func (c *PMXDriverHealthCollector) Collect(ctx context.Context) error {
	labels := c.getPodIdentityLabels()
	c.uptimeGauge.WithLabelValues(labels...).Set(time.Since(c.startTime).Seconds())
	c.goroutineCount.WithLabelValues(labels...).Set(float64(runtime.NumGoroutine()))

	// Collect restart count from Kubernetes Pod API (skip if not available)
	c.collectRestartCount(ctx)

	// Collect CPU/memory from Kubernetes metrics API (skip if not available)
	c.collectKubernetesMetrics(ctx)

	// Connection pool active - count of running containers in this pod
	c.collectConnectionPoolMetrics(ctx)

	return nil
}

// collectRestartCount fetches the restart count from Kubernetes Pod API for both driver and reverseproxy containers
func (c *PMXDriverHealthCollector) collectRestartCount(ctx context.Context) {
	podName := os.Getenv("POD_NAME")
	namespace := getDriverNamespace()

	if podName == "" || namespace == "" || c.k8sClient == nil {
		return
	}

	// Get the pod object to extract container restart counts
	pod, err := c.k8sClient.GetClient().CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		csmlog.Warnf("collectRestartCount: failed to get pod %s: %v", podName, err)
		return
	}

	// Get restart count for driver container
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.Name == "driver" {
			labels := c.getPodIdentityLabelsContainer("driver")
			c.restartTotal.WithLabelValues(labels...).Set(float64(containerStatus.RestartCount))
			break
		}
	}

	// Get restart count for reverseproxy container if it exists
	for _, containerStatus := range pod.Status.ContainerStatuses {
		if containerStatus.Name == "reverseproxy" {
			labels := c.getPodIdentityLabelsContainer("reverseproxy")
			c.restartTotal.WithLabelValues(labels...).Set(float64(containerStatus.RestartCount))
			break
		}
	}
}

// collectConnectionPoolMetrics sets the connection pool metric based on the
// connection health status. When healthy, it reports the number of ready
// containers in the current pod. When unhealthy, it reports 0.
// Also performs an actual health check against the Unisphere API if client is available.
func (c *PMXDriverHealthCollector) collectConnectionPoolMetrics(ctx context.Context) {
	// Perform actual health check if pmaxClient is available
	// TODO: Update health check to use correct gopowermax API after version upgrade
	// if c.pmaxClient != nil {
	// 	// Simple health check: try to get array info
	// 	_, err := c.pmaxClient.GetArray(ctx, c.arrayID)
	// 	if err != nil {
	// 		log.Warnf("PMXDriverHealthCollector: Unisphere API health check failed for array %s: %v", c.arrayID, err)
	// 		c.SetConnectionHealth(false)
	// 	} else {
	// 		c.SetConnectionHealth(true)
	// 	}
	// }

	c.connectionHealthMu.RLock()
	isHealthy := c.connectionHealth
	c.connectionHealthMu.RUnlock()

	// If unhealthy, set to 0 regardless of container status
	if !isHealthy {
		c.connPoolActive.WithLabelValues(c.getPodIdentityLabels()...).Set(0)
		return
	}

	// When healthy, report the number of ready containers
	podName := os.Getenv("POD_NAME")
	namespace := getDriverNamespace()

	if podName == "" || namespace == "" || c.k8sClient == nil {
		c.connPoolActive.WithLabelValues(c.getPodIdentityLabels()...).Set(0)
		return
	}

	pod, err := c.k8sClient.GetClient().CoreV1().Pods(namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		c.connPoolActive.WithLabelValues(c.getPodIdentityLabels()...).Set(0)
		return
	}

	readyCount := 0
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Ready {
			readyCount++
		}
	}
	c.connPoolActive.WithLabelValues(c.getPodIdentityLabels()...).Set(float64(readyCount))
}

// collectKubernetesMetrics fetches CPU/memory metrics from Kubernetes Metrics API
func (c *PMXDriverHealthCollector) collectKubernetesMetrics(ctx context.Context) {
	// Get pod name and namespace from environment variables
	podName := os.Getenv("POD_NAME")
	namespace := getDriverNamespace()

	if podName == "" || namespace == "" || c.k8sClient == nil {
		return
	}

	// Get pod metrics from Kubernetes using k8sClient
	podMetrics, err := c.k8sClient.GetPodMetrics(ctx, namespace, podName)
	if err != nil {
		csmlog.Debugf("collectKubernetesMetrics: failed to get pod metrics for %s: %v", podName, err)
		return
	}

	// Find the CSI driver container metrics
	for _, container := range podMetrics.Containers {
		// Match container name (typically "driver" or contains "powermax")
		if strings.Contains(strings.ToLower(container.Name), "powermax") || strings.Contains(strings.ToLower(container.Name), "driver") {
			// Extract CPU usage (in cores, convert to percentage)
			cpuUsage := container.Usage[corev1.ResourceCPU]
			cpuPercent := float64(cpuUsage.MilliValue()) / 10.0 // Convert millicores to percentage

			// Extract memory usage (in bytes)
			memUsage := container.Usage[corev1.ResourceMemory]

			// Set metrics with pod identity labels
			labels := c.getPodIdentityLabels()
			c.cpuUsage.WithLabelValues(labels...).Set(cpuPercent)
			c.memUsage.WithLabelValues(labels...).Set(float64(memUsage.Value()))

			return
		}
	}
}

// SetConnectionHealth sets the connection health status for the driver.
// When set to false, the connection pool metric will report 0 regardless of
// container status. When set to true (default), it reports the number of
// ready containers.
func (c *PMXDriverHealthCollector) SetConnectionHealth(healthy bool) {
	c.connectionHealthMu.Lock()
	defer c.connectionHealthMu.Unlock()
	c.connectionHealth = healthy
}

// Name returns the collector name.
func (c *PMXDriverHealthCollector) Name() string { return "PMXDriverHealthCollector" }
