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
	"reflect"
	"strings"

	"github.com/golang/mock/gomock"
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

// Added a new mocking capability to help enable mocking this dynamically

// MockUtilsInterface is a mock of UtilsInterface interface
type MockUtilsInterface struct {
	ctrl     *gomock.Controller
	recorder *MockUtilsInterfaceMockRecorder
}

// MockUtilsInterfaceMockRecorder is the mock recorder for MockUtilsInterface
type MockUtilsInterfaceMockRecorder struct {
	mock *MockUtilsInterface
}

// NewMockUtilsInterface creates a new mock instance
func NewMockUtilsInterface(ctrl *gomock.Controller) *MockUtilsInterface {
	mock := &MockUtilsInterface{ctrl: ctrl}
	mock.recorder = &MockUtilsInterfaceMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use
func (m *MockUtilsInterface) EXPECT() *MockUtilsInterfaceMockRecorder {
	return m.recorder
}

// GetNodeLabels mocks base method
func (m *MockUtilsInterface) GetNodeLabels(arg0 string) (map[string]string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetNodeLabels", arg0)
	ret0, _ := ret[0].(map[string]string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetNodeLabels indicates an expected call of GetNodeLabels
func (mr *MockUtilsInterfaceMockRecorder) GetNodeLabels(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetNodeLabels", reflect.TypeOf((*MockUtilsInterface)(nil).GetNodeLabels), arg0)
}

// GetNodeIPs mocks base method
func (m *MockUtilsInterface) GetNodeIPs(arg0 string) string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetNodeIPs", arg0)
	ret0, _ := ret[0].(string)
	return ret0
}

// GetNodeIPs indicates an expected call of GetNodeIPs
func (mr *MockUtilsInterfaceMockRecorder) GetNodeIPs(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetNodeIPs", reflect.TypeOf((*MockUtilsInterface)(nil).GetNodeIPs), arg0)
}
