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

package k8smock

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/k8sutils"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/utils"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	informerv1 "k8s.io/client-go/informers/core/v1"
	kubernetes "k8s.io/client-go/kubernetes/fake"
)

// Constants for the mock client
const (
	resyncPeriod = time.Minute * 30
)

var mockUtils *MockUtils

// MockUtils - mock kubernetes utils
type MockUtils struct {
	KubernetesClient *kubernetes.Clientset
	InformerFactory  informers.SharedInformerFactory
	SecretInformer   informerv1.SecretInformer
	Namespace        string
	CertDirectory    string
	stopCh           chan struct{}
	SecretCert       []byte
	Username         []byte
	Password         []byte
	TimeNowFunc      func() int64
}

// Init - initializes the mock k8s utils
func Init() *MockUtils {
	if mockUtils != nil {
		return mockUtils
	}
	kubernetesClient := kubernetes.NewSimpleClientset()

	informerFactory := informers.NewSharedInformerFactoryWithOptions(kubernetesClient, resyncPeriod, informers.WithNamespace(common.DefaultNameSpace))

	secretInformer := informerFactory.Core().V1().Secrets()
	certDirectory := utils.RootDir() + "/../" + common.DefaultCertDirName
	mockUtils = &MockUtils{
		KubernetesClient: kubernetesClient,
		InformerFactory:  informerFactory,
		SecretInformer:   secretInformer,
		Namespace:        common.DefaultNameSpace,
		CertDirectory:    certDirectory,
		TimeNowFunc:      time.Now().UnixNano,
	}
	return mockUtils
}

func (mockUtils *MockUtils) getCertFileFromSecret(certSecret *corev1.Secret) (string, error) {
	if certSecret == nil {
		return "", fmt.Errorf("cert secret can't be nil")
	}
	timestamp := strconv.FormatInt(mockUtils.TimeNowFunc(), 10)
	certFilePath := fmt.Sprintf("%s/%s-proxy-%s.pem", mockUtils.CertDirectory, certSecret.Name, timestamp)
	err := mockUtils.createFile(certFilePath, certSecret.Data["cert"])
	if err != nil {
		return "", err
	}
	return certFilePath, nil
}

// GetCertFileFromSecret - mock implementation for GetCertFileFromSecret
func (mockUtils *MockUtils) GetCertFileFromSecret(secret *corev1.Secret) (string, error) {
	if mockUtils == nil {
		return "", fmt.Errorf("k8sutils not initialized")
	}
	return mockUtils.getCertFileFromSecret(secret)
}

// GetCertFileFromSecretName - mock implementation for GetCertFileFromSecretName
func (mockUtils *MockUtils) GetCertFileFromSecretName(secretName string) (string, error) {
	if mockUtils == nil {
		return "", fmt.Errorf("k8sutils not initialized")
	}
	certSecret, err := mockUtils.KubernetesClient.CoreV1().Secrets(common.DefaultNameSpace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	return mockUtils.getCertFileFromSecret(certSecret)
}

func (mockUtils *MockUtils) createFile(fileName string, data []byte) error {
	fileName = filepath.Clean(fileName)
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

func (mockUtils *MockUtils) getCredentialFromSecret(secret *corev1.Secret) (*common.Credentials, error) {
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

func (mockUtils *MockUtils) updateSecret(secret *corev1.Secret) (*corev1.Secret, error) {
	if secret == nil {
		return nil, fmt.Errorf("secret can't be nil")
	}
	secret, err := mockUtils.KubernetesClient.CoreV1().Secrets(common.DefaultNameSpace).Update(context.TODO(), secret, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("error updating secret")
	}
	return secret, nil
}

// GetCredentialsFromSecretName - mock implementation for GetCredentialsFromSecretName
func (mockUtils *MockUtils) GetCredentialsFromSecretName(secretName string) (*common.Credentials, error) {
	if mockUtils == nil {
		return nil, fmt.Errorf("k8sutils not initialized")
	}
	secret, err := mockUtils.KubernetesClient.CoreV1().Secrets(common.DefaultNameSpace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return mockUtils.getCredentialFromSecret(secret)
}

// UpdateSecret - mock implementation for UpdateSecret
func (mockUtils *MockUtils) UpdateSecret(secret *corev1.Secret) (*corev1.Secret, error) {
	if mockUtils == nil {
		return nil, fmt.Errorf("k8sutils not initialized")
	}

	return mockUtils.updateSecret(secret)
}

// GetSecretFromSecretName - mock implementation for GetSecretFromSecretName
func (mockUtils *MockUtils) GetSecretFromSecretName(secretName string) (*corev1.Secret, error) {
	if mockUtils == nil {
		return nil, fmt.Errorf("k8sutils not initialized")
	}
	secret, err := mockUtils.KubernetesClient.CoreV1().Secrets(common.DefaultNameSpace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return secret, nil
}

// GetCredentialsFromSecret - mock implementation for GetCredentialsFromSecret
func (mockUtils *MockUtils) GetCredentialsFromSecret(secret *corev1.Secret) (*common.Credentials, error) {
	return mockUtils.getCredentialFromSecret(secret)
}

// StartInformer - mock implementation for StartInformer
func (mockUtils *MockUtils) StartInformer(callback func(k8sutils.UtilsInterface, *corev1.Secret)) {
}

// StopInformer - mock implementation for StopInformer
func (mockUtils *MockUtils) StopInformer() {
	return
}

// CreateNewCertSecret - creates a new mock secret for certs
func (mockUtils *MockUtils) CreateNewCertSecret(secretName string) (*corev1.Secret, error) {
	_, err := mockUtils.KubernetesClient.CoreV1().Secrets(common.DefaultNameSpace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return nil, err
	}

	data := map[string][]byte{
		"cert": []byte("This is a dummy cert file"),
	}
	if mockUtils.SecretCert != nil {
		data["cert"] = mockUtils.SecretCert
	}
	secretObj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: common.DefaultNameSpace,
		},
		Data: data,
		Type: "Generic",
	}
	certsecret, err := mockUtils.KubernetesClient.CoreV1().Secrets(common.DefaultNameSpace).Create(context.TODO(), secretObj, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, err
	}

	return certsecret, nil
}

// CreateNewCredentialSecret - creates a new mock secret for credentials
func (mockUtils *MockUtils) CreateNewCredentialSecret(secretName string) (*corev1.Secret, error) {
	_, err := mockUtils.KubernetesClient.CoreV1().Secrets(common.DefaultNameSpace).Get(context.TODO(), secretName, metav1.GetOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return nil, err
	}

	data := map[string][]byte{
		"username": []byte("test-username"),
		"password": []byte("test-password"),
	}
	if mockUtils.Username != nil {
		data["username"] = mockUtils.Username
	}
	if mockUtils.Password != nil {
		data["password"] = mockUtils.Password
	}
	secretObj := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: common.DefaultNameSpace,
		},
		Data: data,
		Type: "Generic",
	}

	secret, err := mockUtils.KubernetesClient.CoreV1().Secrets(common.DefaultNameSpace).Create(context.TODO(), secretObj, metav1.CreateOptions{})
	if err != nil && !errors.IsAlreadyExists(err) {
		return nil, err
	}

	return secret, nil
}
