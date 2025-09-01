/*
 *
 * Copyright © 2021-2024 Dell Inc. or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

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
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
)

const kubeconfigFileDir = "../../test-config/tmp"

func InitMockK8sUtils(namespace, certDirectory string, inCluster bool, resyncPeriod time.Duration, kubeClient *KubernetesClient) (*K8sUtils, error) {
	kubeClient.Clientset = fake.NewSimpleClientset()

	informerFactory := informers.NewSharedInformerFactoryWithOptions(kubeClient.Clientset, resyncPeriod, informers.WithNamespace(namespace))
	secretInformer := informerFactory.Core().V1().Secrets()

	k8sUtils = &K8sUtils{
		KubernetesClient: *kubeClient,
		InformerFactory:  informerFactory,
		SecretInformer:   secretInformer,
		Namespace:        namespace,
		CertDirectory:    certDirectory,
		stopCh:           make(chan struct{}),
		TimeNowFunc: func() int64 { // Mock the timestamp
			return 1700000000000000000 // Fixed timestamp for testing
		},
	}
	return k8sUtils, nil
}

func TestInitMethods(t *testing.T) {
	client := &KubernetesClient{}
	client.InitMethods()

	if client.NewForConfigFunc == nil {
		t.Errorf("NewForConfigFunc not set")
	}

	if client.RestForConfigFunc == nil {
		t.Errorf("RestForConfigFunc not set")
	}

	if client.GetPathFromEnvFunc == nil {
		t.Errorf("GetPathFromEnvFunc not set")
	}
}

func TestInit(t *testing.T) {
	// Create temp kubeconfig
	kubeDir := filepath.Join(kubeconfigFileDir, ".kube")
	kubeFile := filepath.Join(kubeDir, "config")
	err := os.MkdirAll(kubeDir, 0o700)
	if err != nil {
		t.Errorf("Failed to create temp kubeconfig directory. (%s)", err.Error())
	}
	err = createTempKubeconfig(kubeFile)
	if err != nil {
		t.Errorf("Failed to create temp kubeconfig file. (%s)", err.Error())
		return
	}
	defer func() {
		fmt.Printf("removing temp kubeconfig directory %s", kubeDir)
		_ = os.RemoveAll(kubeDir)
	}()

	tests := []struct {
		name        string
		client      KubernetesClient
		utils       *K8sUtils
		inCluster   bool
		createError error
		expectError bool
	}{
		{
			name:        "Test utils not nil",
			client:      KubernetesClient{}, // Does not matter for this test
			inCluster:   true,
			utils:       &K8sUtils{},
			createError: nil,
			expectError: false,
		},
		{
			name:        "Successful in cluster",
			inCluster:   true,
			utils:       nil,
			createError: nil,
			expectError: false,
			client: KubernetesClient{
				Clientset:         fake.NewSimpleClientset(),
				RestForConfigFunc: func() (*rest.Config, error) { return &rest.Config{}, nil },
				NewForConfigFunc: func(config *rest.Config) (kubernetes.Interface, error) {
					return fake.NewSimpleClientset(), nil
				},
				GetPathFromEnvFunc: func() string { return kubeconfigFileDir },
			},
		},
		{
			name:  "Successful out of cluster",
			utils: nil,
			client: KubernetesClient{
				Clientset:         fake.NewSimpleClientset(),
				RestForConfigFunc: func() (*rest.Config, error) { return &rest.Config{}, nil },
				NewForConfigFunc: func(config *rest.Config) (kubernetes.Interface, error) {
					return fake.NewSimpleClientset(), nil
				},
				GetPathFromEnvFunc: func() string { return kubeconfigFileDir },
			},
			inCluster:   false,
			createError: nil,
			expectError: false,
		},
		{
			name:  "Failure in cluster fail rest.InClusterConfig",
			utils: nil,
			client: KubernetesClient{
				Clientset:         fake.NewSimpleClientset(),
				RestForConfigFunc: func() (*rest.Config, error) { return &rest.Config{}, errors.Errorf("rest.InConfig failed") },
				NewForConfigFunc: func(config *rest.Config) (kubernetes.Interface, error) {
					return fake.NewSimpleClientset(), nil
				},
				GetPathFromEnvFunc: func() string { return kubeconfigFileDir },
			},
			inCluster:   true,
			createError: nil,
			expectError: true,
		},
		{
			name:  "Failure in cluster fail kubernetes.newForConfig",
			utils: nil,
			client: KubernetesClient{
				Clientset:         fake.NewSimpleClientset(),
				RestForConfigFunc: func() (*rest.Config, error) { return &rest.Config{}, nil },
				NewForConfigFunc: func(config *rest.Config) (kubernetes.Interface, error) {
					return fake.NewSimpleClientset(), errors.Errorf("kubernetes.NewForConfig failed")
				},
				GetPathFromEnvFunc: func() string { return kubeconfigFileDir },
			},
			inCluster:   true,
			createError: nil,
			expectError: true,
		},
		{
			name:  "Failure out of cluster fail kubernetes.newForConfig",
			utils: nil,
			client: KubernetesClient{
				Clientset:         fake.NewSimpleClientset(),
				RestForConfigFunc: func() (*rest.Config, error) { return &rest.Config{}, nil },
				NewForConfigFunc: func(config *rest.Config) (kubernetes.Interface, error) {
					return fake.NewSimpleClientset(), errors.Errorf("kubernetes.NewForConfig failed")
				},
				GetPathFromEnvFunc: func() string { return kubeconfigFileDir },
			},
			inCluster:   false,
			createError: nil,
			expectError: true,
		},
		{
			name:  "Failure out of cluster no kubeconfig",
			utils: nil,
			client: KubernetesClient{
				Clientset:         fake.NewSimpleClientset(),
				RestForConfigFunc: func() (*rest.Config, error) { return &rest.Config{}, nil },
				NewForConfigFunc: func(config *rest.Config) (kubernetes.Interface, error) {
					return fake.NewSimpleClientset(), errors.Errorf("kubeconfig not found")
				},
				GetPathFromEnvFunc: func() string { return "" },
			},
			inCluster:   false,
			createError: nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k8sUtils = tt.utils // Resets the k8sUtils global for all except the first one
			_, err := Init(common.DefaultNameSpace, "/tmp/certs", tt.inCluster, time.Minute, &tt.client)

			if tt.expectError {
				if err == nil {
					t.Errorf("expected an error but got none")
				}
				if k8sUtils != nil {
					t.Errorf("expected k8sUtils to be nil but got %v", k8sUtils)
				}
			} else {
				if err != nil {
					t.Errorf("did not expect an error but got %v", err)
				}
				if k8sUtils == nil {
					t.Errorf("expected k8sUtils to be non-nil but got nil")
				}
			}
		})
	}
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

func TestGetCredentialsFromSecret(t *testing.T) {
	mockUtils, _ := InitMockK8sUtils(common.DefaultNameSpace, "/tmp/certs", false, time.Second, &KubernetesClient{})

	tests := []struct {
		name     string
		utils    *K8sUtils
		secret   *corev1.Secret
		wantCred *common.Credentials
		wantErr  bool
	}{
		{
			name:  "GetSecret - valid secret",
			utils: mockUtils,
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
			wantCred: &common.Credentials{UserName: "test-username", Password: "test-password"},
			wantErr:  false,
		},
		{
			name:    "GetSecret-Invalid secret",
			utils:   mockUtils,
			wantErr: true,
		},
		{
			name:  "GetSecret-No username secret",
			utils: mockUtils,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name: "test-secret",
				},
				Data: map[string][]byte{
					"cert": []byte("test-cert"),
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cred, err := tt.utils.GetCredentialsFromSecret(tt.secret)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetCredentialsFromSecret error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if !reflect.DeepEqual(cred, tt.wantCred) {
				t.Errorf("GetCredentialsFromSecret = want %v, got %v", tt.wantCred, cred)
			}
		})
	}
}

func TestGetCredentialsFromSecretName(t *testing.T) {
	mockUtils, _ := InitMockK8sUtils(common.DefaultNameSpace, "/tmp/certs", false, time.Second, &KubernetesClient{})
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-secret",
		},
		Data: map[string][]byte{
			"cert":     []byte("test-cert"),
			"username": []byte("test-username"),
			"password": []byte("test-password"),
		},
	}

	secretNoUserName := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-secret-no-username",
		},
		Data: map[string][]byte{
			"cert": []byte("test-cert"),
		},
	}
	_, err := mockUtils.KubernetesClient.Clientset.CoreV1().Secrets(common.DefaultNameSpace).Create(context.TODO(), secret, metav1.CreateOptions{})
	if err != nil {
		t.Errorf("failed to create mock secret. error: %s", err.Error())
	}

	_, err = mockUtils.KubernetesClient.Clientset.CoreV1().Secrets(common.DefaultNameSpace).Create(context.TODO(), secretNoUserName, metav1.CreateOptions{})
	if err != nil {
		t.Errorf("failed to create mock secret. error: %s", err.Error())
	}

	tests := []struct {
		name       string
		utils      *K8sUtils
		secretName string
		wantCred   *common.Credentials
		wantErr    bool
	}{
		{
			name:       "GetSecret-Vvalid secret",
			utils:      mockUtils,
			secretName: "test-secret",
			wantCred:   &common.Credentials{UserName: "test-username", Password: "test-password"},
			wantErr:    false,
		},
		{
			name:       "GetSecret-Invalid secret",
			utils:      mockUtils,
			secretName: "invalid-secret",
			wantErr:    true,
		},
		{
			name:       "GetSecret-No username secret",
			utils:      mockUtils,
			secretName: "test-secret-no-username",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cred, err := tt.utils.GetCredentialsFromSecretName(tt.secretName)

			// Create the secret

			if (err != nil) != tt.wantErr {
				t.Errorf("GetCredentialsFromSecretName error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if !reflect.DeepEqual(cred, tt.wantCred) {
				t.Errorf("GetCredentialsFromSecretName = want %v, got %v", tt.wantCred, cred)
			}
		})
	}
}

func TestGetCertFileFromSecret(t *testing.T) {
	mockUtils, _ := InitMockK8sUtils(common.DefaultNameSpace, "/tmp/certs", false, time.Second, &KubernetesClient{})
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-secret",
		},
		Data: map[string][]byte{
			"cert":     []byte("test-cert"),
			"username": []byte("test-username"),
			"password": []byte("test-password"),
		},
	}
	_, err := mockUtils.KubernetesClient.Clientset.CoreV1().Secrets(common.DefaultNameSpace).Create(context.TODO(), secret, metav1.CreateOptions{})
	if err != nil {
		t.Errorf("failed to create mock secret. error: %s", err.Error())
	}

	tests := []struct {
		name     string
		utils    *K8sUtils
		secret   *corev1.Secret
		wantCert string
		wantErr  bool
		credName string
	}{
		{
			name:     "GetCertFileFromSecret-Valid secret",
			utils:    mockUtils,
			secret:   secret,
			wantCert: "/tmp/certs/test-secret-proxy-1700000000000000000.pem",
			wantErr:  false,
		},
		{
			name:    "GetCertFileFromSecret-Invalid secret",
			utils:   mockUtils,
			secret:  nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up any necessary test fixtures
			err := os.MkdirAll("/tmp/certs", 0o700)
			if err != nil {
				t.Errorf("Error creating certs directory: %v", err)
			}
			// Call the relevant methods of the K8sUtils struct
			cert, err := tt.utils.GetCertFileFromSecret(tt.secret)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetCertFileFromSecret error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if cert != tt.wantCert {
				t.Errorf("GetCertFileFromSecret = want %v, got %v", tt.wantCert, cert)
			}
			os.RemoveAll("/tmp/certs")
		})
	}
}

func TestGetCertFileFromSecretName(t *testing.T) {
	mockUtils, _ := InitMockK8sUtils(common.DefaultNameSpace, "/tmp/certs", false, time.Second, &KubernetesClient{})
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-secret",
		},
		Data: map[string][]byte{
			"cert":     []byte("test-cert"),
			"username": []byte("test-username"),
			"password": []byte("test-password"),
		},
	}
	_, err := mockUtils.KubernetesClient.Clientset.CoreV1().Secrets(common.DefaultNameSpace).Create(context.TODO(), secret, metav1.CreateOptions{})
	if err != nil {
		t.Errorf("failed to create mock secret. error: %s", err.Error())
	}

	tests := []struct {
		name       string
		utils      *K8sUtils
		secretName string
		wantCert   string
		wantErr    bool
		credName   string
	}{
		{
			name:       "GetCertFileFromSecretName-Valid secret",
			utils:      mockUtils,
			secretName: "test-secret",
			wantCert:   "/tmp/certs/test-secret-proxy-1700000000000000000.pem",
			wantErr:    false,
		},
		{
			name:       "GetCertFileFromSecretName-Invalid secret",
			utils:      mockUtils,
			secretName: "invalid-secret",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up any necessary test fixtures
			err := os.MkdirAll("/tmp/certs", 0o700)
			if err != nil {
				t.Errorf("Error creating certs directory: %v", err)
			}

			// Call the relevant methods of the K8sUtils struct
			cert, err := tt.utils.GetCertFileFromSecretName(tt.secretName)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetCertFileFromSecretName error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			if cert != tt.wantCert {
				t.Errorf("GetCertFileFromSecretName = want cert %v, got %v", tt.wantCert, cert)
			}

			// Cleanup
			os.RemoveAll("/tmp/certs")
		})
	}
}

// TestStartInformer checks if the StartInformer function properly triggers the callback.
func TestStartInformer(t *testing.T) {
	// Create a fake Kubernetes clientset
	clientset := fake.NewSimpleClientset()

	// Create informer factory and a shared stop channel
	informerFactory := informers.NewSharedInformerFactory(clientset, 0)
	secretInformer := informerFactory.Core().V1().Secrets()

	// Create K8sUtils struct with mocked informer
	utils := &K8sUtils{
		SecretInformer:  secretInformer,
		InformerFactory: informerFactory,
		stopCh:          make(chan struct{}), // Stop channel for cleanup
	}

	// Use WaitGroup to ensure the callback is invoked
	var wg sync.WaitGroup
	wg.Add(2) // Expect two invocations (AddFunc and UpdateFunc)

	// Callback function to check if it is invoked
	callback := func(_ UtilsInterface, secret *corev1.Secret) {
		fmt.Printf("Callback invoked for secret: %s\n", secret.Name)
		wg.Done()
	}

	// Start the informer in a separate goroutine
	go func() {
		utils.StartInformer(callback)
	}()

	// Wait for informer to start
	time.Sleep(100 * time.Millisecond)

	// Create a secret (this should trigger AddFunc)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-secret",
			Namespace:       "default",
			ResourceVersion: "1",
		},
	}
	_, err := clientset.CoreV1().Secrets("default").Create(context.TODO(), secret, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create secret: %v", err)
	}

	// Update the secret (this should trigger UpdateFunc)
	secret.ResourceVersion = "2"
	_, err = clientset.CoreV1().Secrets("default").Update(context.TODO(), secret, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("Failed to update secret: %v", err)
	}

	// Update the secret with nothing changed (this should trigger UpdateFunc)
	secret.ResourceVersion = "2"
	_, err = clientset.CoreV1().Secrets("default").Update(context.TODO(), secret, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("Failed to update secret: %v", err)
	}

	// Wait for the callback to be triggered
	wg.Wait()

	// Cleanup: Stop the informer
	close(utils.stopCh)
}

func TestStopInformer(t *testing.T) {
	mockUtils, err := InitMockK8sUtils(common.DefaultNameSpace, "/tmp/certs", false, time.Second, &KubernetesClient{})
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

// creatTempKubeconfig creates a temporary, fake kubeconfig in the current directory
// using the given file path.
func createTempKubeconfig(kubeFilePath string) error {
	kubeconfig := `clusters:
- cluster:
    server: https://some.hostname.or.ip:6443
  name: fake-cluster
contexts:
- context:
    cluster: fake-cluster
    user: admin
  name: admin
current-context: admin
preferences: {}
users:
- name: admin`

	// filename := filepath.Join(dir, filepath.Base(path))
	err := os.WriteFile(kubeFilePath, []byte(kubeconfig), 0o600)
	return err
}
