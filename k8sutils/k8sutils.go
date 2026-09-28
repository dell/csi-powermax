/*
Copyright © 2020-2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package k8sutils

import (
	"context"
	"fmt"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	metricsv1beta1api "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/kubernetes-csi/csi-lib-utils/leaderelection"

	"github.com/dell/csmlog"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// UtilsInterface - interface which provides helper methods related to k8s
type UtilsInterface interface {
	GetNodeLabels(string) (map[string]string, error)
	GetNodeIPs(string) string
	GetPVCForVolume(ctx context.Context, pvName string, volumeID string) (*corev1.PersistentVolumeClaim, error)
	GetClient() kubernetes.Interface
	GetMetricsClient() metricsv.Interface
	GetPodMetrics(ctx context.Context, namespace, podName string) (*metricsv1beta1api.PodMetrics, error)
}

// K8sUtils stores the configuration of the k8s client, k8s client and the informer
type K8sUtils struct {
	KubernetesClient *KubernetesClient
}

var k8sUtils *K8sUtils

// KubernetesClient - client connection
type KubernetesClient struct {
	ClientSet     kubernetes.Interface
	MetricsClient metricsv.Interface
}

// Init - Initializes the k8s client and creates the secret informer
func Init(kubeConfig string) (*K8sUtils, error) {
	if k8sUtils != nil {
		return k8sUtils, nil
	}
	kubeClient, err := CreateKubeClientSet(kubeConfig)
	if err != nil {
		csmlog.Errorf("failed to create kube client. error: %s", err.Error())
		return nil, err
	}

	// Initialize metrics client only if metrics is enabled
	var metricsClient metricsv.Interface
	metricsEnabled := strings.EqualFold(os.Getenv("X_CSI_METRICS_ENABLED"), "true")
	if metricsEnabled {
		config, err := getInClusterConfigFunc()
		if kubeConfig != "" {
			config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
		}
		if err == nil {
			metricsClient, err = newMetricsForConfigFunc(config)
			if err != nil {
				csmlog.Warnf("failed to create metrics client: %s (CPU/memory metrics will not be available)", err.Error())
				metricsClient = nil
			}
		} else {
			csmlog.Warnf("failed to get config for metrics client: %s", err.Error())
		}
	}

	k8sUtils = &K8sUtils{
		KubernetesClient: &KubernetesClient{
			ClientSet:     kubeClient,
			MetricsClient: metricsClient,
		},
	}
	return k8sUtils, nil
}

// set this function as a var so that it can be mocked
var (
	getInClusterConfigFunc  = rest.InClusterConfig
	newMetricsForConfigFunc = metricsv.NewForConfig
)

// CreateKubeClientSet - Returns kubeClient set
func CreateKubeClientSet(kubeConfig string) (*kubernetes.Clientset, error) {
	var clientSet *kubernetes.Clientset
	var config *rest.Config
	var err error
	if kubeConfig != "" {
		// use the current context in kubeConfig
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
		if err != nil {
			return nil, err
		}
	} else {
		config, err = getInClusterConfigFunc()
		if err != nil {
			return nil, err
		}
	}
	// create the clientSet
	clientSet, err = kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	return clientSet, nil
}

// CreateDynamicClient creates a Kubernetes dynamic client from the given
// kubeConfig path. Pass "" to use the in-cluster configuration.
// The dynamic client is used by the CRD-backed VolumeJournal.
func CreateDynamicClient(kubeConfig string) (dynamic.Interface, error) {
	var config *rest.Config
	var err error
	if kubeConfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeConfig)
		if err != nil {
			return nil, err
		}
	} else {
		config, err = getInClusterConfigFunc()
		if err != nil {
			return nil, err
		}
	}
	return dynamic.NewForConfig(config)
}

// LeaderElection ...
func LeaderElection(clientSet kubernetes.Interface, lockName string, namespace string, runFunc func(ctx context.Context)) error {
	le := leaderelection.NewLeaderElection(clientSet, lockName, runFunc)
	le.WithNamespace(namespace)

	return le.Run()
}

// GetNodeLabels returns back Node labels for the node name
func (c *K8sUtils) GetNodeLabels(nodeFullName string) (map[string]string, error) {
	// access the API to fetch node object
	node, err := c.KubernetesClient.ClientSet.CoreV1().Nodes().Get(context.TODO(), nodeFullName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	csmlog.Debugf("Node %s details\n", node)

	return node.Labels, nil
}

// GetClient returns the underlying kubernetes.Interface client.
func (c *K8sUtils) GetClient() kubernetes.Interface {
	if c.KubernetesClient != nil {
		return c.KubernetesClient.ClientSet
	}
	return nil
}

// GetMetricsClient returns the underlying metrics client.
func (c *K8sUtils) GetMetricsClient() metricsv.Interface {
	if c.KubernetesClient != nil {
		return c.KubernetesClient.MetricsClient
	}
	return nil
}

// GetNodeIPs returns cluster IP of the node object
func (c *K8sUtils) GetNodeIPs(nodeID string) string {
	// access the API to fetch node object
	nodeList, err := c.KubernetesClient.ClientSet.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
	if err != nil {
		return ""
	}
	for _, node := range nodeList.Items {
		if strings.Contains(node.Name, nodeID) {
			for _, addr := range node.Status.Addresses {
				if addr.Type == corev1.NodeInternalIP {
					return addr.Address
				}
			}
		}
	}
	return ""
}

// GetPVCForVolume retrieves the PVC bound to the given PV.
// It validates that the PV's CSI volume handle matches the provided volumeID.
func (c *K8sUtils) GetPVCForVolume(ctx context.Context, pvName string, volumeID string) (*corev1.PersistentVolumeClaim, error) {
	// Get PV
	pv, err := c.KubernetesClient.ClientSet.CoreV1().PersistentVolumes().Get(ctx, pvName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get PV %s: %w", pvName, err)
	}

	// Validate CSI volume handle
	if pv.Spec.CSI == nil || pv.Spec.CSI.VolumeHandle != volumeID {
		return nil, fmt.Errorf("PV %s CSI volume handle does not match volume ID %s", pvName, volumeID)
	}

	// Get PVC via ClaimRef
	if pv.Spec.ClaimRef == nil {
		return nil, fmt.Errorf("PV %s has no ClaimRef", pvName)
	}

	pvc, err := c.KubernetesClient.ClientSet.CoreV1().PersistentVolumeClaims(pv.Spec.ClaimRef.Namespace).Get(ctx, pv.Spec.ClaimRef.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get PVC %s/%s: %w", pv.Spec.ClaimRef.Namespace, pv.Spec.ClaimRef.Name, err)
	}

	return pvc, nil
}

// GetPodMetrics retrieves metrics for the current pod
func (c *K8sUtils) GetPodMetrics(ctx context.Context, namespace, podName string) (*metricsv1beta1api.PodMetrics, error) {
	if c.KubernetesClient == nil || c.KubernetesClient.MetricsClient == nil {
		return nil, fmt.Errorf("metrics client is uninitialized")
	}

	return c.KubernetesClient.MetricsClient.MetricsV1beta1().PodMetricses(namespace).Get(ctx, podName, metav1.GetOptions{})
}
