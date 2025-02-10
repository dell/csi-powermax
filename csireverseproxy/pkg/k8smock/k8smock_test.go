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
	"reflect"
	"testing"

	"revproxy/v2/pkg/common"
	"revproxy/v2/pkg/k8sutils"
	"revproxy/v2/pkg/utils"

	log "github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestMain(m *testing.M) {
	status := 0
	if st := m.Run(); st > status {
		status = st
	}
	err := utils.RemoveTempFiles()
	if err != nil {
		log.Fatalf("Failed to cleanup temp files. (%s)", err.Error())
		status = 1
	}
	os.Exit(status)
}

func TestInit(t *testing.T) {
	k8sUtils := Init()
	fmt.Printf("mockUtils: %+v\n", k8sUtils)
}

func TestStartInformer(t *testing.T) {
	dummyEventHandler := func(ui k8sutils.UtilsInterface, secret *corev1.Secret) {}

	mockUtils := Init()
	mockUtils.StartInformer(dummyEventHandler)
	fmt.Printf("mockUtils: %+v\n", mockUtils)
}

func TestStopInformer(t *testing.T) {
	mockUtils.StopInformer()
}

func TestGetCertFileFromSecretName(t *testing.T) {
	k8sUtils := Init()

	tests := []struct {
		name          string
		mockUtils     *MockUtils
		secretName    string
		createSecret  bool
		expectSuccess bool
	}{
		{
			name:          "Valid secret name",
			mockUtils:     k8sUtils,
			secretName:    "test-cert-secret-name",
			createSecret:  true,
			expectSuccess: true,
		},
		{
			name:          "Non-existent secret",
			mockUtils:     k8sUtils,
			secretName:    "non-existent-secret",
			createSecret:  false,
			expectSuccess: false,
		},
		{
			name:          "Empty secret name",
			mockUtils:     k8sUtils,
			secretName:    "",
			createSecret:  true,
			expectSuccess: true,
		},
		{
			name:          "mock utils not initialized",
			mockUtils:     nil,
			secretName:    "",
			createSecret:  false,
			expectSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var secret *corev1.Secret
			var err error

			if tt.createSecret {
				secret, err = tt.mockUtils.CreateNewCertSecret(tt.secretName)
				if err != nil {
					t.Errorf("Failed to create cert secret. (%s)", err.Error())
					return
				}
			} else {
				secret = &corev1.Secret{} // Simulate non-existent secret
			}

			certFile, err := tt.mockUtils.GetCertFileFromSecretName(secret.Name)
			if tt.expectSuccess && err != nil {
				t.Errorf("Expected success but failed to get cert file. (%s)", err.Error())
			} else if !tt.expectSuccess && err == nil {
				t.Errorf("Expected failure but got cert file: %s", certFile)
			}
		})
	}
}

func TestGetCertFileFromSecret(t *testing.T) {
	k8sUtils := Init()

	tests := []struct {
		name          string
		mockUtils     *MockUtils
		secretName    string
		createSecret  bool
		expectSuccess bool
	}{
		{
			name:          "Valid secret name",
			mockUtils:     k8sUtils,
			secretName:    "test-cert-secret-name",
			createSecret:  true,
			expectSuccess: true,
		},
		{
			name:          "Non-existent secret",
			mockUtils:     k8sUtils,
			createSecret:  false,
			expectSuccess: false,
		},
		{
			name:          "mock utils not initialized",
			mockUtils:     nil,
			secretName:    "",
			createSecret:  false,
			expectSuccess: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var secret *corev1.Secret
			var err error

			if tt.createSecret {
				secret, err = tt.mockUtils.CreateNewCertSecret(tt.secretName)
				if err != nil {
					t.Errorf("Failed to create cert secret. (%s)", err.Error())
					return
				}
			} else {
				secret = nil
			}

			certFile, err := tt.mockUtils.GetCertFileFromSecret(secret)
			if tt.expectSuccess && err != nil {
				t.Errorf("Expected success but failed to get cert file. (%s)", err.Error())
			} else if !tt.expectSuccess && err == nil {
				t.Errorf("Expected failure but got cert file: %s", certFile)
			}
		})
	}
}

func TestGetCredentialsFromSecretName(t *testing.T) {
	mockUtils := Init()

	// Test case: mockUtils is nil
	var nilMockUtils *MockUtils
	_, err := nilMockUtils.GetCredentialsFromSecretName("test-secret")
	if err == nil || err.Error() != "k8sutils not initialized" {
		t.Errorf("expected error 'k8sutils not initialized', got %v", err)
	}

	// Test case: secret does not exist
	_, err = mockUtils.GetCredentialsFromSecretName("nonexistent-secret")
	if err == nil {
		t.Errorf("expected error for missing secret, got nil")
	}

	// Test case: secret exists
	secret, err := mockUtils.CreateNewCredentialSecret("test-secret")
	if err != nil {
		t.Errorf("unexpected error creating test secret: %v", err)

	}
	expectCred := &common.Credentials{
		UserName: "test-username",
		Password: "test-password",
	}

	cred, err := mockUtils.GetCredentialsFromSecretName(secret.Name)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(expectCred, cred) {
		t.Errorf("expected '%v', got %v", expectCred, cred)
	}

	// Test case: secret exists, username password speficied literally
	mockUtils.Username = []byte("test-username")
	mockUtils.Password = []byte("test-password")
	secret, err = mockUtils.CreateNewCredentialSecret("test-secret-2")
	if err != nil {
		t.Errorf("unexpected error creating test secret: %v", err)
	}

	expectCred = &common.Credentials{
		UserName: "test-username",
		Password: "test-password",
	}

	cred, err = mockUtils.GetCredentialsFromSecretName(secret.Name)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(expectCred, cred) {
		t.Errorf("expected '%v', got %v", expectCred, cred)
	}
}

func TestGetCredentialsFromSecret(t *testing.T) {
	mockUtils := Init()

	secret, err := mockUtils.CreateNewCredentialSecret("test-secret")
	if err != nil {
		t.Errorf("unexpected error creating test secret: %v", err)

	}
	// Test case: mockUtils is nil
	var nilMockUtils *MockUtils
	_, err = nilMockUtils.GetCredentialsFromSecret(secret)
	if err != nil && err.Error() != "k8sutils not initialized" {
		t.Errorf("expected error 'k8sutils not initialized', got %v", err)
	}

	// Test case: secret is nil
	_, err = mockUtils.GetCredentialsFromSecret(nil)
	if err != nil && err.Error() != "secret can't be nil" {
		t.Errorf("expected error for missing secret, got nil")
	}

	// Test case: secret exists
	expectCred := &common.Credentials{
		UserName: "test-username",
		Password: "test-password",
	}

	cred, err := mockUtils.GetCredentialsFromSecret(secret)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(expectCred, cred) {
		t.Errorf("expected '%v', got %v", expectCred, cred)
	}

	//Test case: secret exists but does not container username or password
	badSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "bad-secret",
			Namespace: common.DefaultNameSpace,
		},
		Data: make(map[string][]byte),
		Type: "Generic",
	}
	_, err = mockUtils.KubernetesClient.CoreV1().Secrets(common.DefaultNameSpace).Create(context.TODO(), badSecret, metav1.CreateOptions{})
	if err != nil {
		t.Errorf("unexpected error creating secret: %v", err)
	}
	_, err = mockUtils.GetCredentialsFromSecret(badSecret)
	if err != nil && err.Error() != "username not found in secret data" {
		t.Errorf("expected %v got %v", "username not found in secret data", err)
	}

}
