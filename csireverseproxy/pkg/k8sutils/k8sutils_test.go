/*
 Copyright © 2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"reflect"
	"testing"
	"time"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes/fake"
)

func InitMockK8sUtils(namespace, certDirectory string, inCluster bool, resyncPeriod time.Duration) (*K8sUtils, error) {
	if k8sUtils != nil {
		return k8sUtils, nil
	}
	informerFactory := informers.NewSharedInformerFactoryWithOptions(fake.NewClientset(), resyncPeriod, informers.WithNamespace(namespace))

	secretInformer := informerFactory.Core().V1().Secrets()

	k8sUtils = &K8sUtils{
		KubernetesClient: &KubernetesClient{
			Clientset: fake.NewSimpleClientset(),
		},
		InformerFactory: informerFactory,
		SecretInformer:  secretInformer,
		Namespace:       namespace,
		CertDirectory:   certDirectory,
		stopCh:          make(chan struct{}),
	}

	k8sUtils.TimeNowFunc = func() int64 { // Mock the timestamp
		return 1700000000000000000 // Fixed timestamp for testing
	}
	return k8sUtils, nil
}

func TestGetKubeConfigPathFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		homeEnv  string
		xcsiEnv  string
		expected string
	}{
		{
			name:     "HOME is set",
			homeEnv:  "/home/user",
			xcsiEnv:  "/custom/path",
			expected: "/home/user",
		},
		{
			name:     "HOME is not set, X_CSI_KUBECONFIG_PATH is set",
			homeEnv:  "",
			xcsiEnv:  "/custom/path",
			expected: "/custom/path",
		},
		{
			name:     "Neither HOME nor X_CSI_KUBECONFIG_PATH is set",
			homeEnv:  "",
			xcsiEnv:  "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Setenv("HOME", tt.homeEnv)
			os.Setenv("X_CSI_KUBECONFIG_PATH", tt.xcsiEnv)

			result := getKubeConfigPathFromEnv()

			fmt.Println("Test Case:", tt.name)
			fmt.Println("Expected:", tt.expected, "Got:", result)
		})
	}
}

func TestGetSecretAndCredentials(t *testing.T) {
	k8sutils, _ := InitMockK8sUtils(common.DefaultNameSpace, "/tmp/certs", false, time.Second)

	tests := []struct {
		name     string
		utils    *K8sUtils
		secret   *corev1.Secret
		wantCert string
		wantCred *common.Credentials
		wantErr  bool
	}{
		{
			name:  "GetCertFileFromSecret - valid secret",
			utils: k8sutils,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-secret",
				},
				Data: map[string][]byte{
					"cert":     []byte("test-cert"),
					"username": []byte("test-username"),
					"password": []byte("test-password"),
				},
			},
			wantCert: "/tmp/certs/test-secret-proxy-1700000000000000000.pem",
			wantCred: &common.Credentials{UserName: "test-username", Password: "test-password"},
			wantErr:  false,
		},
		{
			name:    "GetCertFileFromSecret - invalid secret",
			utils:   k8sutils,
			wantErr: true,
		},
		{
			name:  "GetCredentialsFromSecret - valid secret",
			utils: k8sutils,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-secret",
				},
				Data: map[string][]byte{
					"username": []byte("test-username"),
					"password": []byte("test-password"),
				},
			},
			wantCred: &common.Credentials{
				UserName: "test-username",
				Password: "test-password",
			},
			wantErr:  false,
			wantCert: "/tmp/certs/test-secret-proxy-1700000000000000000.pem",
		},
		{
			name:    "GetCredentialsFromSecret - invalid secret",
			utils:   k8sutils,
			wantErr: true,
		},
		//Add more test cases if needed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up any necessary test fixtures
			os.Mkdir("/tmp/certs", 0o700)

			// Call the relevant methods of the K8sUtils struct
			cert, err := tt.utils.GetCertFileFromSecret(tt.secret)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetCertFileFromSecret() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if cert != tt.wantCert {
				t.Errorf("GetCertFileFromSecret() = %v, want %v", cert, tt.wantCert)
			}

			cred, err := tt.utils.GetCredentialsFromSecret(tt.secret)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetCredentialsFromSecret() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if !reflect.DeepEqual(cred, tt.wantCred) {
				t.Errorf("GetCredentialsFromSecret() = %v, want %v", cred, tt.wantCred)
			}

			// Perform any additional assertions or cleanup
		})
	}
}

func TestGetSecretAndCredentialsByName(t *testing.T) {
	k8sutils, _ := InitMockK8sUtils(common.DefaultNameSpace, "/tmp/certs", false, time.Second)

	tests := []struct {
		name       string
		utils      *K8sUtils
		secret     *corev1.Secret
		wantCert   string
		wantCred   *common.Credentials
		wantErr    bool
		secretName string
		credName   string
	}{
		{
			name:  "GetCertFileFromSecretName - valid secret",
			utils: k8sutils,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-secret",
				},
				Data: map[string][]byte{
					"cert":     []byte("test-cert"),
					"username": []byte("test-username"),
					"password": []byte("test-password"),
				},
			},
			secretName: "test-secret",
			wantCert:   "/tmp/certs/test-secret-proxy-1700000000000000000.pem",
			wantCred:   &common.Credentials{UserName: "test-username", Password: "test-password"},
			wantErr:    false,
		},
		{
			name:    "GetCertFileFromSecretName - invalid secret",
			utils:   k8sutils,
			wantErr: true,
		},
		{
			name:       "GetCredentialsFromSecretName - valid secret",
			credName:   "creds",
			secretName: "test-secret",
			utils:      k8sutils,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-secret",
				},
				Data: map[string][]byte{
					"username": []byte("test-username"),
					"password": []byte("test-password"),
				},
			},
			wantCred: &common.Credentials{
				UserName: "test-username",
				Password: "test-password",
			},
			wantErr:  false,
			wantCert: "/tmp/certs/test-secret-proxy-1700000000000000000.pem",
		},
		{
			name:    "GetCredentialsFromSecretName - invalid secret",
			utils:   k8sutils,
			wantErr: true,
		},
		//Add more test cases if needed
	}

	for _, tt := range tests {

		t.Run(tt.name, func(t *testing.T) {
			// Set up any necessary test fixtures
			os.Mkdir("/tmp/certs", 0o700)
			tt.utils.KubernetesClient.Clientset.CoreV1().Secrets(common.DefaultNameSpace).Create(context.TODO(), tt.secret, metav1.CreateOptions{})

			// Call the relevant methods of the K8sUtils struct
			cert, err := tt.utils.GetCertFileFromSecretName(tt.secretName)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetCertFileFromSecret() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if cert != tt.wantCert {
				t.Errorf("GetCertFileFromSecret() = %v, want %v", cert, tt.wantCert)
			}

			cred, err := tt.utils.GetCredentialsFromSecretName(tt.secretName)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetCredentialsFromSecret() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if !reflect.DeepEqual(cred, tt.wantCred) {
				t.Errorf("GetCredentialsFromSecret() = %v, want %v", cred, tt.wantCred)
			}
			// Perform any additional assertions or cleanup
		})
	}
}

func TestStartInformer(t *testing.T) {
	mockUtils, err := InitMockK8sUtils(common.DefaultNameSpace, "/tmp/certs", false, time.Second)
	if err != nil {
		fmt.Println("Error initializing mock k8s utils:", err)
		return
	}
	mockUtils.stopCh = make(chan struct{})

	callback := func(utils UtilsInterface, secret *corev1.Secret) {
		fmt.Println("Callback invoked for secret:", secret.Name)
	}

	err = mockUtils.StartInformer(callback)
	if err != nil {
		fmt.Println("Error starting informer:", err)
	} else {
		fmt.Println("Informer started successfully")
	}
}

func TestStartInformerV2(t *testing.T) {
	mockUtils, err := InitMockK8sUtils(common.DefaultNameSpace, "/tmp/certs", false, time.Second)
	if err != nil {
		fmt.Println("Error initializing mock k8s utils:", err)
		return
	}
	mockUtils.stopCh = make(chan struct{})

	callback := func(utils UtilsInterface, secret *corev1.Secret) {
		fmt.Println("Callback invoked for secret:", secret.Name)
	}

	// Simulating informer event handling
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "test-secret", ResourceVersion: "1"}}
	updatedSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "test-secret", ResourceVersion: "2"}}

	// Manually invoke AddFunc
	callback(mockUtils, secret)

	// Manually invoke UpdateFunc with a different resource version
	callback(mockUtils, updatedSecret)

	err = mockUtils.StartInformer(callback)
	if err != nil {
		fmt.Println("Error starting informer:", err)
	} else {
		fmt.Println("Informer started successfully")
	}
}

func TestStopInformer(t *testing.T) {
	mockUtils, err := InitMockK8sUtils(common.DefaultNameSpace, "/tmp/certs", false, time.Second)
	if err != nil {
		fmt.Println("Error initializing mock k8s utils:", err)
		return
	}
	mockUtils.stopCh = make(chan struct{})

	go func() {
		mockUtils.StopInformer()
		fmt.Println("Informer stopped successfully")
	}()

	_, isOpen := <-mockUtils.stopCh
	if !isOpen {
		fmt.Println("Stop channel successfully closed")
	} else {
		fmt.Println("Stop channel still open")
	}
}
