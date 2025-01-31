/*
 Copyright © 2024 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"reflect"
	"testing"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetes "k8s.io/client-go/kubernetes/fake"
)

func TestMockUtils(t *testing.T) {

	mockutils := Init()

	mockutils.CertDirectory = "/tmp"
	mockutils.KubernetesClient = kubernetes.NewSimpleClientset()
	mockutils.TimeNowFunc = func() int64 { // Mock the timestamp
		return 1700000000000000000 // Fixed timestamp for testing
	}

	testCases := []struct {
		description string
		mockUtils   *MockUtils
		expected    interface{}
	}{
		{
			description: "Test GetCertFileFromSecret method",
			mockUtils:   mockutils,
			expected:    "/tmp/test-secret-proxy-1700000000000000000.pem",
		},
		{
			description: "Test GetCertFileFromSecretName method",
			mockUtils:   mockutils,
			expected:    "/tmp/test-secret-proxy-1700000000000000000.pem",
		},
		{
			description: "Test GetCredentialsFromSecret method",
			mockUtils:   mockutils,
			expected:    &common.Credentials{UserName: "test-username", Password: "test-password"},
		},
		{
			description: "Test GetCredentialsFromSecretName method",
			mockUtils:   mockutils,
			expected:    &common.Credentials{UserName: "test-username", Password: "test-password"},
		},
		{
			description: "Test CreateNewCertSecret method",
			mockUtils:   Init(),
			expected: &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "dummy-secret-name", Namespace: common.DefaultNameSpace}, Data: map[string][]byte{
				"cert": []byte("This is a dummy cert file")}, Type: "Generic"},
		},
		{
			description: "Test CreateNewCredentialSecret method",
			mockUtils:   Init(),
			expected:    &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "dummy-secret-name", Namespace: common.DefaultNameSpace}, Data: map[string][]byte{"username": []byte("test-username"), "password": []byte("test-password")}, Type: "Generic"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			switch tc.description {
			case "Test GetCertFileFromSecret method":
				secret := &corev1.Secret{
					Data: map[string][]byte{
						"cert": []byte("mock-cert-data"),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-secret",
					},
				}
				result, err := tc.mockUtils.GetCertFileFromSecret(secret)
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if result != tc.expected {
					t.Errorf("Expected %v, got %v", tc.expected, result)
				}
			case "Test GetCertFileFromSecretName method":
				secret := &corev1.Secret{
					Data: map[string][]byte{
						"cert": []byte("mock-cert-data"),
					},
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-secret",
					},
				}
				tc.mockUtils.KubernetesClient.CoreV1().Secrets(common.DefaultNameSpace).Create(context.TODO(), secret,
					metav1.CreateOptions{})
				result, err := tc.mockUtils.GetCertFileFromSecretName("test-secret")
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if result != tc.expected {
					t.Errorf("Expected %v, got %v", tc.expected, result)
				}
			case "Test GetCredentialsFromSecret method":
				secret := &corev1.Secret{Data: map[string][]byte{"username": []byte("test-username"), "password": []byte("test-password")}}
				result, err := tc.mockUtils.GetCredentialsFromSecret(secret)
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !reflect.DeepEqual(result, tc.expected) {
					t.Errorf("Expected %v, got %v", tc.expected, result)
				}
			case "Test GetCredentialsFromSecretName method":
				tc.mockUtils.KubernetesClient.CoreV1().Secrets(common.DefaultNameSpace).Create(context.TODO(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "dummy-secret-name", Namespace: common.DefaultNameSpace}, Data: map[string][]byte{"username": []byte("test-username"), "password": []byte("test-password")}}, metav1.CreateOptions{})
				result, err := tc.mockUtils.GetCredentialsFromSecretName("dummy-secret-name")
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !reflect.DeepEqual(result, tc.expected) {
					t.Errorf("Expected %v, got %v", tc.expected, result)
				}
			case "Test CreateNewCertSecret method":
				result, err := tc.mockUtils.CreateNewCertSecret("dummy-secret-name")
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !reflect.DeepEqual(result, tc.expected) {
					t.Errorf("Expected %v, got %v", tc.expected, result)
				}
			case "Test CreateNewCredentialSecret method":
				result, err := tc.mockUtils.CreateNewCredentialSecret("dummy-secret-name")
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if !reflect.DeepEqual(result, tc.expected) {
					t.Errorf("Expected %v, got %v", tc.expected, result)
				}
			}
		})
	}
}
