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
	"reflect"
	"strings"

	"github.com/golang/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	kubernetesFake "k8s.io/client-go/kubernetes/fake"
	metricsv1beta1api "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

var mockUtils *MockUtils

// MockUtils - mock kubernetes utils
type MockUtils struct {
	KubernetesClient *kubernetesFake.Clientset
}

// Init - initializes the mock k8s utils
func Init() *MockUtils {
	if mockUtils != nil {
		return mockUtils
	}
	kubernetesClient := kubernetesFake.NewSimpleClientset()
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

// GetPVCForVolume is mock implementation for GetPVCForVolume
func (m *MockUtils) GetPVCForVolume(_ context.Context, _ string, _ string) (*corev1.PersistentVolumeClaim, error) {
	return nil, nil
}

// GetClient is mock implementation for GetClient
func (m *MockUtils) GetClient() kubernetes.Interface {
	return m.KubernetesClient
}

// GetMetricsClient is mock implementation for GetMetricsClient
func (m *MockUtils) GetMetricsClient() metricsv.Interface {
	return nil
}

// GetPodMetrics is mock implementation for GetPodMetrics
func (m *MockUtils) GetPodMetrics(_ context.Context, _ string, _ string) (*metricsv1beta1api.PodMetrics, error) {
	return nil, nil
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

// GetPVCForVolume mocks base method
func (m *MockUtilsInterface) GetPVCForVolume(ctx context.Context, pvName string, volumeID string) (*corev1.PersistentVolumeClaim, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPVCForVolume", ctx, pvName, volumeID)
	ret0, _ := ret[0].(*corev1.PersistentVolumeClaim)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetPVCForVolume indicates an expected call of GetPVCForVolume
func (mr *MockUtilsInterfaceMockRecorder) GetPVCForVolume(ctx, pvName, volumeID interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPVCForVolume", reflect.TypeOf((*MockUtilsInterface)(nil).GetPVCForVolume), ctx, pvName, volumeID)
}

// GetClient mocks base method
func (m *MockUtilsInterface) GetClient() kubernetes.Interface {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetClient")
	ret0, _ := ret[0].(kubernetes.Interface)
	return ret0
}

// GetClient indicates an expected call of GetClient
func (mr *MockUtilsInterfaceMockRecorder) GetClient() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetClient", reflect.TypeOf((*MockUtilsInterface)(nil).GetClient))
}

// GetMetricsClient mocks base method
func (m *MockUtilsInterface) GetMetricsClient() metricsv.Interface {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetMetricsClient")
	ret0, _ := ret[0].(metricsv.Interface)
	return ret0
}

// GetMetricsClient indicates an expected call of GetMetricsClient
func (mr *MockUtilsInterfaceMockRecorder) GetMetricsClient() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetMetricsClient", reflect.TypeOf((*MockUtilsInterface)(nil).GetMetricsClient))
}

// GetPodMetrics mocks base method
func (m *MockUtilsInterface) GetPodMetrics(ctx context.Context, namespace, podName string) (*metricsv1beta1api.PodMetrics, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "GetPodMetrics", ctx, namespace, podName)
	ret0, _ := ret[0].(*metricsv1beta1api.PodMetrics)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// GetPodMetrics indicates an expected call of GetPodMetrics
func (mr *MockUtilsInterfaceMockRecorder) GetPodMetrics(ctx, namespace, podName interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "GetPodMetrics", reflect.TypeOf((*MockUtilsInterface)(nil).GetPodMetrics), ctx, namespace, podName)
}
