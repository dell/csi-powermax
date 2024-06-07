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
	"strings"

	kubernetes "k8s.io/client-go/kubernetes/fake"
)

var mockUtils *MockUtils

// MockUtils - mock kubernetes utils
type MockUtils struct {
	KubernetesClient *kubernetes.Clientset
}

// Init - initializes the mock k8s utils
func Init() *MockUtils {
	if mockUtils != nil {
		return mockUtils
	}
	kubernetesClient := kubernetes.NewSimpleClientset()
	mockUtils = &MockUtils{
		KubernetesClient: kubernetesClient,
	}
	return mockUtils
}

// GetNodeLabels is mock implementation for GetNodeLabels
func (m *MockUtils) GetNodeLabels(_ string) (map[string]string, error) {
	// access the API to fetch node object
	return nil, nil
}

// GetNodeIPs is mock implementation for GetNodeIPs
func (m *MockUtils) GetNodeIPs(nodeID string) string {
	nodeElem := strings.Split(nodeID, "-")
	if len(nodeElem) < 2 {
		return ""
	}
	return nodeElem[1]
}
