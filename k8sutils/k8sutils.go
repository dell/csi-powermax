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
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/kubernetes-csi/csi-lib-utils/leaderelection"

	"github.com/dell/csmlog"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var log = csmlog.GetLogger()

// UtilsInterface - interface which provides helper methods related to k8s
type UtilsInterface interface {
	GetNodeLabels(string) (map[string]string, error)
	GetNodeIPs(string) string
	GetPVCForVolume(ctx context.Context, pvName string, volumeID string) (*corev1.PersistentVolumeClaim, error)
	GetClient() kubernetes.Interface
}

// K8sUtils stores the configuration of the k8s client, k8s client and the informer
type K8sUtils struct {
	KubernetesClient *KubernetesClient
}

var k8sUtils *K8sUtils

// KubernetesClient - client connection
type KubernetesClient struct {
	ClientSet kubernetes.Interface
}

// Init - Initializes the k8s client and creates the secret informer
func Init(kubeConfig string) (*K8sUtils, error) {
	if k8sUtils != nil {
		return k8sUtils, nil
	}
	kubeClient, err := CreateKubeClientSet(kubeConfig)
	if err != nil {
		log.Errorf("failed to create kube client. error: %s", err.Error())
		return nil, err
	}
	k8sUtils = &K8sUtils{
		KubernetesClient: &KubernetesClient{
			ClientSet: kubeClient,
		},
	}
	return k8sUtils, nil
}

// set this function as a var so that it can be mocked
var getInClusterConfigFunc = rest.InClusterConfig

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
	log.Debugf("Node %s details\n", node)

	return node.Labels, nil
}

// GetClient returns the underlying kubernetes.Interface client.
func (c *K8sUtils) GetClient() kubernetes.Interface {
	if c.KubernetesClient != nil {
		return c.KubernetesClient.ClientSet
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
