/*
 Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"path/filepath"
	"revproxy/v2/pkg/common"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"
	"k8s.io/client-go/tools/clientcmd"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	informerv1 "k8s.io/client-go/informers/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
)

// UtilsInterface - interface which provides helper methods related to k8s
type UtilsInterface interface {
	GetCertFileFromSecret(*corev1.Secret) (string, error)
	GetCertFileFromSecretName(string) (string, error)
	GetCredentialsFromSecret(*corev1.Secret) (*common.Credentials, error)
	GetCredentialsFromSecretName(string) (*common.Credentials, error)
	StartInformer(func(UtilsInterface, *corev1.Secret)) error
	StopInformer()
}

// K8sUtils stores the configuration of the k8s client, k8s client and the informer
type K8sUtils struct {
	KubernetesClient *KubernetesClient
	InformerFactory  informers.SharedInformerFactory
	SecretInformer   informerv1.SecretInformer
	Namespace        string
	CertDirectory    string
	stopCh           chan struct{}
}

var k8sUtils *K8sUtils

// KubernetesClient - client connection
type KubernetesClient struct {
	Clientset *kubernetes.Clientset
}

func getKubeConfigPathFromEnv() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	return os.Getenv("X_CSI_KUBECONFIG_PATH") // user specified path
}

// CreateKubeClient - Creates a k8s client - either InCluster or OutOfCluster
func (c *KubernetesClient) CreateKubeClient(inCluster bool) error {
	if inCluster {
		return c.CreateInClusterKubeClient()
	}
	return c.CreateOutOfClusterKubeClient()
}

// CreateInClusterKubeClient - creates the in-cluster config
func (c *KubernetesClient) CreateInClusterKubeClient() error {
	// creates the in-cluster config
	config, err := rest.InClusterConfig()
	if err != nil {
		return err
	}
	// creates the Clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	c.Clientset = clientset
	return nil
}

// CreateOutOfClusterKubeClient - Creates a Kubernetes Clientset
func (c *KubernetesClient) CreateOutOfClusterKubeClient() error {
	var kubeconfig string
	if path := getKubeConfigPathFromEnv(); path != "" {
		kubeconfig = filepath.Join(path, ".kube", "config")
	}
	if kubeconfig == "" {
		return fmt.Errorf("failed to get kube config path")
	}
	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return err
	}
	// create the Clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return err
	}
	c.Clientset = clientset
	return nil
}

// GetSecret - Given a namespace and secret name, returns a pointer to the secret object
func (c *KubernetesClient) GetSecret(namespace, secretName string) (*corev1.Secret, error) {
	secret, err := c.Clientset.CoreV1().Secrets(namespace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return secret, nil
}

// Init - Initializes the k8s client and creates the secret informer
func Init(namespace, certDirectory string, inCluster bool, resyncPeriod time.Duration) (*K8sUtils, error) {
	if k8sUtils != nil {
		return k8sUtils, nil
	}
	var kubeClient KubernetesClient
	err := kubeClient.CreateKubeClient(inCluster)
	if err != nil {
		log.Errorf("failed to create kube client. error: %s", err.Error())
		return nil, err
	}

	informerFactory := informers.NewSharedInformerFactoryWithOptions(kubeClient.Clientset, resyncPeriod, informers.WithNamespace(namespace))

	secretInformer := informerFactory.Core().V1().Secrets()

	k8sUtils = &K8sUtils{
		KubernetesClient: &kubeClient,
		InformerFactory:  informerFactory,
		SecretInformer:   secretInformer,
		Namespace:        namespace,
		CertDirectory:    certDirectory,
		stopCh:           make(chan struct{}),
	}
	return k8sUtils, nil
}

func (utils *K8sUtils) getCertFileFromSecret(certSecret *corev1.Secret) (string, error) {
	if certSecret == nil {
		return "", fmt.Errorf("cert secret can't be nil")
	}
	timestamp := strconv.FormatInt(time.Now().UnixNano(), 10)
	certFile := fmt.Sprintf("%s/%s-proxy-%s.pem", k8sUtils.CertDirectory, certSecret.Name, timestamp)
	err := utils.createFile(certFile, certSecret.Data["cert"])
	if err != nil {
		return "", err
	}
	return certFile, nil
}

// GetCertFileFromSecret - Given a k8s secret, creates a certificate file and returns the file name
func (utils *K8sUtils) GetCertFileFromSecret(secret *corev1.Secret) (string, error) {
	return utils.getCertFileFromSecret(secret)
}

// GetCertFileFromSecretName - Given a secret name, reads the secret and creates a corresponding
// cert file and returns the file name
func (utils *K8sUtils) GetCertFileFromSecretName(secretName string) (string, error) {
	certSecret, err := k8sUtils.KubernetesClient.GetSecret(k8sUtils.Namespace, secretName)
	if err != nil {
		return "", err
	}
	return utils.getCertFileFromSecret(certSecret)
}

func (utils *K8sUtils) getCredentialFromSecret(secret *corev1.Secret) (*common.Credentials, error) {
	if secret == nil {
		return nil, fmt.Errorf("secret can't be nil")
	}
	if _, ok := secret.Data["username"]; ok {
		return &common.Credentials{
			UserName: string(secret.Data["username"]),
			Password: string(secret.Data["password"]),
		}, nil
	}
	return nil, fmt.Errorf("username not found in secret data")
}

// GetCredentialsFromSecretName - Given a secret name, reads the secret and returns credentials
func (utils *K8sUtils) GetCredentialsFromSecretName(secretName string) (*common.Credentials, error) {
	secret, err := k8sUtils.KubernetesClient.GetSecret(k8sUtils.Namespace, secretName)
	if err != nil {
		return nil, err
	}
	return utils.getCredentialFromSecret(secret)
}

// GetCredentialsFromSecret - Given a secret object, returns credentials
func (utils *K8sUtils) GetCredentialsFromSecret(secret *corev1.Secret) (*common.Credentials, error) {
	return utils.getCredentialFromSecret(secret)
}

func (utils *K8sUtils) createFile(fileName string, data []byte) error {
	file, err := os.Create(fileName)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return file.Close()
}

// StartInformer -  starts the informer to listen for any events related to secrets
// in the namespace being watched
func (utils *K8sUtils) StartInformer(callback func(UtilsInterface, *corev1.Secret)) error {
	utils.SecretInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			callback(utils, obj.(*corev1.Secret))
		},
		UpdateFunc: func(old, new interface{}) {
			oldSecret := old.(*corev1.Secret)
			newSecret := new.(*corev1.Secret)
			if oldSecret.ResourceVersion == newSecret.ResourceVersion {
				return
			}
			callback(utils, newSecret)
		},
	})

	utils.InformerFactory.Start(utils.stopCh)
	return nil
}

// StopInformer - stops the informer
func (utils *K8sUtils) StopInformer() {
	close(utils.stopCh)
}
