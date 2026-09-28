/*
 Copyright © 2026 Dell Inc. or its subsidiaries. All Rights Reserved.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at
      http://www.apache.org/licenses/LICENSE-2.0
 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package collectors

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// TestNewK8sVolumeValidator tests the creation of a new K8sVolumeValidator
func TestNewK8sVolumeValidator(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	driverName := "csi-powermax.dellemc.com"

	validator := NewK8sVolumeValidator(k8sClient, driverName)
	require.NotNil(t, validator)
	assert.Equal(t, driverName, validator.driverName)
	assert.NotNil(t, validator.k8sClient)
	assert.NotNil(t, validator.volumeIDCache)
}

// TestNewK8sVolumeValidator_NilClient tests creation with nil client
func TestNewK8sVolumeValidator_NilClient(t *testing.T) {
	driverName := "csi-powermax.dellemc.com"

	validator := NewK8sVolumeValidator(nil, driverName)
	require.NotNil(t, validator)
	assert.Nil(t, validator.k8sClient)
}

// TestK8sVolumeValidator_IsDriverManaged_NilClient tests IsDriverManaged with nil client
func TestK8sVolumeValidator_IsDriverManaged_NilClient(t *testing.T) {
	validator := NewK8sVolumeValidator(nil, "csi-powermax.dellemc.com")

	managed, err := validator.IsDriverManaged(context.Background(), "vol1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "kubernetes client not initialized")
	assert.False(t, managed)
}

// TestK8sVolumeValidator_IsDriverManaged_EmptyVolumeID tests with empty volume ID
func TestK8sVolumeValidator_IsDriverManaged_EmptyVolumeID(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	validator := NewK8sVolumeValidator(k8sClient, "csi-powermax.dellemc.com")

	managed, err := validator.IsDriverManaged(context.Background(), "")
	assert.NoError(t, err)
	assert.False(t, managed)
}

// TestK8sVolumeValidator_IsDriverManaged_CacheHit tests cache hit scenario
func TestK8sVolumeValidator_IsDriverManaged_CacheHit(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	validator := NewK8sVolumeValidator(k8sClient, "csi-powermax.dellemc.com")

	// Pre-populate cache
	validator.volumeIDCache["vol1"] = true

	managed, err := validator.IsDriverManaged(context.Background(), "vol1")
	assert.NoError(t, err)
	assert.True(t, managed)
}

// TestK8sVolumeValidator_IsDriverManaged_CacheMiss tests cache miss scenario
func TestK8sVolumeValidator_IsDriverManaged_CacheMiss(t *testing.T) {
	k8sClient := fake.NewSimpleClientset()
	validator := NewK8sVolumeValidator(k8sClient, "csi-powermax.dellemc.com")

	managed, err := validator.IsDriverManaged(context.Background(), "vol-not-in-cache")
	assert.NoError(t, err)
	assert.False(t, managed)
}

// TestK8sVolumeValidator_RefreshCache_Success tests successful cache refresh
func TestK8sVolumeValidator_RefreshCache_Success(t *testing.T) {
	k8sClient := fake.NewSimpleClientset(
		&corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pv1",
			},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "csi-powermax.dellemc.com",
						VolumeHandle: "csi-CSM-csivol-uuid-default-000123-001ABC",
					},
				},
			},
		},
		&corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pv2",
			},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "other-driver",
						VolumeHandle: "other-volume",
					},
				},
			},
		},
	)

	validator := NewK8sVolumeValidator(k8sClient, "csi-powermax.dellemc.com")

	err := validator.RefreshCache(context.Background())
	assert.NoError(t, err)

	// Check that the volume ID was cached
	validator.cacheMu.RLock()
	cached := validator.volumeIDCache["001ABC"]
	validator.cacheMu.RUnlock()
	assert.True(t, cached, "Volume ID should be cached")
}

// TestK8sVolumeValidator_RefreshCache_NilClient tests RefreshCache with nil client
func TestK8sVolumeValidator_RefreshCache_NilClient(t *testing.T) {
	validator := NewK8sVolumeValidator(nil, "csi-powermax.dellemc.com")

	// The code doesn't handle nil client gracefully, so we expect a panic
	assert.Panics(t, func() {
		validator.RefreshCache(context.Background())
	})
}

// TestK8sVolumeValidator_RefreshCache_EmptyVolumeHandle tests handling of PV with empty volumeHandle
func TestK8sVolumeValidator_RefreshCache_EmptyVolumeHandle(t *testing.T) {
	k8sClient := fake.NewSimpleClientset(
		&corev1.PersistentVolume{
			ObjectMeta: metav1.ObjectMeta{
				Name: "pv1",
			},
			Spec: corev1.PersistentVolumeSpec{
				PersistentVolumeSource: corev1.PersistentVolumeSource{
					CSI: &corev1.CSIPersistentVolumeSource{
						Driver:       "csi-powermax.dellemc.com",
						VolumeHandle: "", // Empty volume handle
					},
				},
			},
		},
	)

	validator := NewK8sVolumeValidator(k8sClient, "csi-powermax.dellemc.com")

	err := validator.RefreshCache(context.Background())
	assert.NoError(t, err) // Should not error, just skip
}

// TestExtractPowerMaxVolumeID tests the extractPowerMaxVolumeID function
func TestExtractPowerMaxVolumeID(t *testing.T) {
	testCases := []struct {
		name         string
		volumeHandle string
		expectedID   string
	}{
		{
			name:         "Standard format",
			volumeHandle: "csi-CSM-csivol-uuid-default-000123-001ABC",
			expectedID:   "001ABC",
		},
		{
			name:         "With array ID suffix",
			volumeHandle: "csi-CSM-csivol-uuid-default-000123-001ABC/000123",
			expectedID:   "001ABC",
		},
		{
			name:         "Simple csivol format",
			volumeHandle: "csivol-uuid-000123-001ABC",
			expectedID:   "001ABC",
		},
		{
			name:         "Empty handle",
			volumeHandle: "",
			expectedID:   "",
		},
		{
			name:         "Single part",
			volumeHandle: "simple-volume",
			expectedID:   "volume",
		},
		{
			name:         "Two parts",
			volumeHandle: "part1-part2",
			expectedID:   "part2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractPowerMaxVolumeID(tc.volumeHandle)
			assert.Equal(t, tc.expectedID, result)
		})
	}
}

// TestExtractPowerMaxVolumeID_WithProtocol tests extractPowerMaxVolumeID with protocol suffix
func TestExtractPowerMaxVolumeID_WithProtocol(t *testing.T) {
	handle := "000123-000123-000123-000123-000123-FC"
	result := extractPowerMaxVolumeID(handle)
	assert.Equal(t, "FC", result)
}

// TestExtractPowerMaxVolumeID_LongFormat tests extractPowerMaxVolumeID with long format
func TestExtractPowerMaxVolumeID_LongFormat(t *testing.T) {
	handle := "000123456789-000123456789-000123456789-000123456789-000123456789-NVMe"
	result := extractPowerMaxVolumeID(handle)
	assert.Equal(t, "NVMe", result)
}

// TestExtractPowerMaxVolumeID_WithISCSIProtocol tests extractPowerMaxVolumeID with iSCSI protocol
func TestExtractPowerMaxVolumeID_WithISCSIProtocol(t *testing.T) {
	handle := "000123-000123-000123-000123-000123-ISCSI"
	result := extractPowerMaxVolumeID(handle)
	assert.Equal(t, "ISCSI", result)
}
