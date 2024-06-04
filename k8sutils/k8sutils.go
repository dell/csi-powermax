/*
Copyright © 2020 Dell Inc. or its subsidiaries. All Rights Reserved.

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

	v12 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	log "github.com/sirupsen/logrus"

	"github.com/kubernetes-csi/csi-lib-utils/leaderelection"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type leaderElection interface {
	Run() error
	WithNamespace(namespace string)
}

// UtilsInterface - interface which provides helper methods related to k8s
type UtilsInterface interface {
	GetNodeLabels(string) (map[string]string, error)
	GetNodeIPs(string) string
}

// K8sUtils stores the configuration of the k8s client, k8s client and the informer
type K8sUtils struct {
	KubernetesClient *KubernetesClient
}

var k8sUtils *K8sUtils

// KubernetesClient - client connection
type KubernetesClient struct {
	ClientSet *kubernetes.Clientset
}

// Init - Initializes the k8s client and creates the secret informer
func Init(kubeConfig string) (*K8sUtils, error) {
	if k8sUtils != nil {
		return k8sUtils, nil
	}
	var kubeClient *kubernetes.Clientset
	kubeClient, err := CreateKubeClientSet(kubeConfig)
	if err != nil {
		log.Errorf("failed to create kube client. error: %s", err.Error())
		return nil, err
	}
	k8sUtils := &K8sUtils{
		KubernetesClient: &KubernetesClient{
			ClientSet: kubeClient,
		},
	}
	return k8sUtils, nil
}

// CreateKubeClientSet - Returns kubeClient set
func CreateKubeClientSet(kubeConfig string) (*kubernetes.Clientset, error) {
	var clientSet *kubernetes.Clientset
	if kubeConfig != "" {
		// use the current context in kubeConfig
		config, err := clientcmd.BuildConfigFromFlags("", kubeConfig)
		if err != nil {
			return nil, err
		}
		// create the clientSet
		clientSet, err = kubernetes.NewForConfig(config)
		if err != nil {
			return nil, err
		}
	} else {
		config, err := rest.InClusterConfig()
		if err != nil {
			return nil, err
		}
		// creates the clientSet
		clientSet, err = kubernetes.NewForConfig(config)
		if err != nil {
			return nil, err
		}
	}
	return clientSet, nil
}

// LeaderElection ...
func LeaderElection(clientSet *kubernetes.Clientset, lockName string, namespace string, runFunc func(ctx context.Context)) {
	le := leaderelection.NewLeaderElection(clientSet, lockName, runFunc)
	le.WithNamespace(namespace)
	if err := le.Run(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "failed to initialize leader election: %v", err)
		os.Exit(1)
	}
}

// GetNodeLabels returns back Node labels for the node name
func (c *K8sUtils) GetNodeLabels(nodeFullName string) (map[string]string, error) {
	// access the API to fetch node object
	node, err := c.KubernetesClient.ClientSet.CoreV1().Nodes().Get(context.TODO(), nodeFullName, v1.GetOptions{})
	if err != nil {
		return nil, err
	}
	log.Debugf("Node %s details\n", node)

	return node.Labels, nil
}

// GetNodeIPs returns cluster IP of the node object
func (c *K8sUtils) GetNodeIPs(nodeID string) string {
	// access the API to fetch node object
	nodeList, err := c.KubernetesClient.ClientSet.CoreV1().Nodes().List(context.TODO(), v1.ListOptions{})
	if err != nil {
		return ""
	}
	for _, node := range nodeList.Items {
		if strings.Contains(node.Name, nodeID) {
			for _, addr := range node.Status.Addresses {
				if addr.Type == v12.NodeInternalIP {
					return addr.Address
				}
			}
		}
	}
	return ""
}
