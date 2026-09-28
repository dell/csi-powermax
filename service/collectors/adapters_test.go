/*
 Copyright © 2026 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package collectors

import (
	"context"
	"errors"
	"testing"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	v100 "github.com/dell/gopowermax/v2/types/v100"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPmaxSGAdapter_GetStorageGroups_Success tests successful storage group retrieval
func TestPmaxSGAdapter_GetStorageGroups_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStorageGroupIDList(gomock.Any(), "array-1", "", false).Return(&v100.StorageGroupIDList{
		StorageGroupIDs: []string{"sg-1", "sg-2"},
	}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{"vol-1"}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(&v100.Volume{
		VolumeID:           "vol-1",
		VolumeIdentifier:   "csivol-uuid-123-FC",
		CapacityGB:         1000,
		AllocatedPercent:   50,
		StorageGroupIDList: []string{"sg-1"},
	}, nil)

	client.EXPECT().GetStorageGroup(gomock.Any(), "array-1", "sg-1").Return(&v100.StorageGroup{
		StorageGroupID: "sg-1",
		CapacityGB:     1000,
		NumOfVolumes:   5,
	}, nil)

	validator := &mockValidator{managed: true}
	adapter := NewPmaxSGAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetStorageGroups(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1) // Only sg-1 has volumes
}

// TestPmaxSGAdapter_GetStorageGroups_NoVolumes tests storage groups with no volumes
func TestPmaxSGAdapter_GetStorageGroups_NoVolumes(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStorageGroupIDList(gomock.Any(), "array-1", "", false).Return(&v100.StorageGroupIDList{
		StorageGroupIDs: []string{"sg-1"},
	}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{}, nil)

	validator := &mockValidator{managed: true}
	adapter := NewPmaxSGAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetStorageGroups(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 0) // No storage groups with volumes
}

// TestPmaxSGAdapter_GetStorageGroups_ValidationSkip tests skipping validation
func TestPmaxSGAdapter_GetStorageGroups_ValidationSkip(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStorageGroupIDList(gomock.Any(), "array-1", "", false).Return(&v100.StorageGroupIDList{
		StorageGroupIDs: []string{"sg-1"},
	}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{"vol-1"}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(&v100.Volume{
		VolumeID:           "vol-1",
		VolumeIdentifier:   "csivol-uuid-123-FC",
		CapacityGB:         1000,
		AllocatedPercent:   50,
		StorageGroupIDList: []string{"sg-1"},
	}, nil)

	client.EXPECT().GetStorageGroup(gomock.Any(), "array-1", "sg-1").Return(&v100.StorageGroup{
		StorageGroupID: "sg-1",
		CapacityGB:     1000,
		NumOfVolumes:   5,
	}, nil)

	validator := &mockValidator{refreshError: true}
	adapter := NewPmaxSGAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetStorageGroups(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

// TestPmaxSGAdapter_GetStorageGroups_FallbackCapacity tests fallback to API capacity
func TestPmaxSGAdapter_GetStorageGroups_FallbackCapacity(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStorageGroupIDList(gomock.Any(), "array-1", "", false).Return(&v100.StorageGroupIDList{
		StorageGroupIDs: []string{"sg-1"},
	}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{"vol-1"}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(&v100.Volume{
		VolumeID:           "vol-1",
		VolumeIdentifier:   "csivol-uuid-123-FC",
		CapacityGB:         0,
		AllocatedPercent:   50,
		StorageGroupIDList: []string{"sg-1"},
	}, nil)

	client.EXPECT().GetStorageGroup(gomock.Any(), "array-1", "sg-1").Return(&v100.StorageGroup{
		StorageGroupID: "sg-1",
		CapacityGB:     2000,
		NumOfVolumes:   5,
	}, nil)

	validator := &mockValidator{managed: true}
	adapter := NewPmaxSGAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetStorageGroups(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, 2000.0, result[0].CapacityGB) // Fallback to API capacity
}

// TestPmaxSGAdapter_GetStorageGroups_GetStorageGroupError tests error when getting storage group details
func TestPmaxSGAdapter_GetStorageGroups_GetStorageGroupError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStorageGroupIDList(gomock.Any(), "array-1", "", false).Return(&v100.StorageGroupIDList{
		StorageGroupIDs: []string{"sg-1", "sg-2"},
	}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{"vol-1"}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(&v100.Volume{
		VolumeID:           "vol-1",
		VolumeIdentifier:   "csivol-uuid-123-FC",
		CapacityGB:         1000,
		AllocatedPercent:   50,
		StorageGroupIDList: []string{"sg-1", "sg-2"},
	}, nil)

	client.EXPECT().GetStorageGroup(gomock.Any(), "array-1", "sg-1").Return(&v100.StorageGroup{
		StorageGroupID: "sg-1",
		CapacityGB:     1000,
		NumOfVolumes:   5,
	}, nil)

	client.EXPECT().GetStorageGroup(gomock.Any(), "array-1", "sg-2").Return(nil, errors.New("storage group not found"))

	validator := &mockValidator{managed: true}
	adapter := NewPmaxSGAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetStorageGroups(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1) // Only sg-1 is returned, sg-2 is skipped due to error
}

// TestPmaxSGAdapter_GetStorageGroups_Error tests error handling
func TestPmaxSGAdapter_GetStorageGroups_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStorageGroupIDList(gomock.Any(), "array-1", "", false).Return(nil, errors.New("failed to get storage group list"))

	validator := &mockValidator{managed: true}
	adapter := NewPmaxSGAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	_, err := adapter.GetStorageGroups(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get storage group list")
}

// TestPmaxSRPAdapter_GetSRPs_Success tests successful SRP retrieval
func TestPmaxSRPAdapter_GetSRPs_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStoragePoolList(gomock.Any(), "array-1").Return(&v100.StoragePoolList{
		StoragePoolIDs: []string{"srp-1", "srp-2"},
	}, nil)

	client.EXPECT().GetStoragePool(gomock.Any(), "array-1", "srp-1").Return(&v100.StoragePool{
		StoragePoolID: "srp-1",
		SrpCap: &v100.SrpCap{
			UsableTotInTB:                1000.0,
			UsableUsedInTB:               500.0,
			SubTotInTB:                   800.0,
			SnapModInTB:                  100.0,
			EffectiveUsedCapacityPercent: 50,
		},
	}, nil)

	client.EXPECT().GetStoragePool(gomock.Any(), "array-1", "srp-2").Return(&v100.StoragePool{
		StoragePoolID: "srp-2",
		SrpCap: &v100.SrpCap{
			UsableTotInTB:                2000.0,
			UsableUsedInTB:               1000.0,
			SubTotInTB:                   1600.0,
			SnapModInTB:                  200.0,
			EffectiveUsedCapacityPercent: 50,
		},
	}, nil)

	adapter := &PmaxSRPAdapter{
		Client:  client,
		ArrayID: "array-1",
	}

	result, err := adapter.GetSRPs(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "srp-1", result[0].SRPID)
	assert.Equal(t, 1000.0, result[0].UsableTotalTB)
	assert.Equal(t, 500.0, result[0].UsableUsedTB)
}

// TestPmaxSRPAdapter_GetSRPs_FBACap tests SRP retrieval with FBA capacity
func TestPmaxSRPAdapter_GetSRPs_FBACap(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStoragePoolList(gomock.Any(), "array-1").Return(&v100.StoragePoolList{
		StoragePoolIDs: []string{"srp-1"},
	}, nil)

	client.EXPECT().GetStoragePool(gomock.Any(), "array-1", "srp-1").Return(&v100.StoragePool{
		StoragePoolID: "srp-1",
		FbaCap: &v100.FbaCap{
			Provisioned: &v100.Provisioned{
				UsableTotInTB:  1500.0,
				UsableUsedInTB: 750.0,
			},
			Effective: &v100.EffectiveCapacity{
				UsedTB:               750.0,
				EffectiveUsedPercent: 50,
			},
		},
		EffectiveUsedCapPerc: 50,
	}, nil)

	adapter := &PmaxSRPAdapter{
		Client:  client,
		ArrayID: "array-1",
	}

	result, err := adapter.GetSRPs(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, 1500.0, result[0].UsableTotalTB)
	assert.Equal(t, 750.0, result[0].UsableUsedTB)
}

// TestPmaxSRPAdapter_GetSRPs_CKDCap tests SRP retrieval with CKD capacity
func TestPmaxSRPAdapter_GetSRPs_CKDCap(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStoragePoolList(gomock.Any(), "array-1").Return(&v100.StoragePoolList{
		StoragePoolIDs: []string{"srp-1"},
	}, nil)

	client.EXPECT().GetStoragePool(gomock.Any(), "array-1", "srp-1").Return(&v100.StoragePool{
		StoragePoolID: "srp-1",
		CkdCap: &v100.CkdCap{
			Provisioned: &v100.Provisioned{
				UsableTotInTB:  1800.0,
				UsableUsedInTB: 900.0,
			},
		},
		EffectiveUsedCapPerc: 50,
	}, nil)

	adapter := &PmaxSRPAdapter{
		Client:  client,
		ArrayID: "array-1",
	}

	result, err := adapter.GetSRPs(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, 1800.0, result[0].UsableTotalTB)
	assert.Equal(t, 900.0, result[0].UsableUsedTB)
}

// TestPmaxSRPAdapter_GetSRPs_NoCap tests SRP retrieval with no capacity data
func TestPmaxSRPAdapter_GetSRPs_NoCap(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStoragePoolList(gomock.Any(), "array-1").Return(&v100.StoragePoolList{
		StoragePoolIDs: []string{"srp-1"},
	}, nil)

	client.EXPECT().GetStoragePool(gomock.Any(), "array-1", "srp-1").Return(&v100.StoragePool{
		StoragePoolID:        "srp-1",
		EffectiveUsedCapPerc: 75,
	}, nil)

	adapter := &PmaxSRPAdapter{
		Client:  client,
		ArrayID: "array-1",
	}

	result, err := adapter.GetSRPs(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, 75.0, result[0].EffectiveUsedCapacityPercent)
}

// TestPmaxSRPAdapter_GetSRPs_GetPoolError tests error when getting storage pool details
func TestPmaxSRPAdapter_GetSRPs_GetPoolError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStoragePoolList(gomock.Any(), "array-1").Return(&v100.StoragePoolList{
		StoragePoolIDs: []string{"srp-1", "srp-2"},
	}, nil)

	client.EXPECT().GetStoragePool(gomock.Any(), "array-1", "srp-1").Return(&v100.StoragePool{
		StoragePoolID: "srp-1",
		SrpCap: &v100.SrpCap{
			UsableTotInTB:                1000.0,
			UsableUsedInTB:               500.0,
			SubTotInTB:                   800.0,
			SnapModInTB:                  100.0,
			EffectiveUsedCapacityPercent: 50,
		},
	}, nil)

	client.EXPECT().GetStoragePool(gomock.Any(), "array-1", "srp-2").Return(nil, errors.New("storage pool not found"))

	adapter := &PmaxSRPAdapter{
		Client:  client,
		ArrayID: "array-1",
	}

	result, err := adapter.GetSRPs(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1) // Only srp-1 is returned, srp-2 is skipped due to error
}

// TestPmaxSRPAdapter_GetSRPs_Error tests error handling
func TestPmaxSRPAdapter_GetSRPs_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetStoragePoolList(gomock.Any(), "array-1").Return(nil, errors.New("failed to get storage pool list"))

	adapter := &PmaxSRPAdapter{
		Client:  client,
		ArrayID: "array-1",
	}

	_, err := adapter.GetSRPs(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get storage pool list")
}

// TestPmaxVolumeAdapter_GetVolumes_Success tests successful volume retrieval
func TestPmaxVolumeAdapter_GetVolumes_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{"mv-1", "mv-2"},
	}, nil)

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-1").Return(&v100.MaskingView{
		MaskingViewID:  "mv-1",
		StorageGroupID: "sg-1",
		HostID:         "host-1",
	}, nil)

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-2").Return(&v100.MaskingView{
		MaskingViewID:  "mv-2",
		StorageGroupID: "sg-2",
		HostID:         "host-2",
	}, nil)

	client.EXPECT().GetMaskingViewConnections(gomock.Any(), "array-1", "mv-1", "").Return([]*v100.MaskingViewConnection{}, nil)

	client.EXPECT().GetMaskingViewConnections(gomock.Any(), "array-1", "mv-2", "").Return([]*v100.MaskingViewConnection{}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{"vol-1", "vol-2"}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(&v100.Volume{
		VolumeID:              "vol-1",
		VolumeIdentifier:      "csivol-uuid-123-FC",
		CapacityGB:            1000,
		StorageGroupIDList:    []string{"sg-1"},
		Status:                "Ready",
		NumberOfFrontEndPaths: 1,
	}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-2").Return(&v100.Volume{
		VolumeID:              "vol-2",
		VolumeIdentifier:      "csivol-uuid-456-FC",
		CapacityGB:            2000,
		StorageGroupIDList:    []string{"sg-2"},
		Status:                "Ready",
		NumberOfFrontEndPaths: 1,
	}, nil)

	validator := &mockValidator{managed: true}
	adapter := NewPmaxVolumeAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetVolumes(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

// TestPmaxVolumeAdapter_GetVolumes_NoMaskingViews tests with no masking views
func TestPmaxVolumeAdapter_GetVolumes_NoMaskingViews(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{},
	}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{"vol-1"}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(&v100.Volume{
		VolumeID:              "vol-1",
		VolumeIdentifier:      "csivol-uuid-123-FC",
		CapacityGB:            1000,
		StorageGroupIDList:    []string{"sg-1"},
		Status:                "Ready",
		NumberOfFrontEndPaths: 0,
	}, nil)

	validator := &mockValidator{managed: true}
	adapter := NewPmaxVolumeAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetVolumes(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1)
	assert.False(t, result[0].Attached)
}

// TestPmaxVolumeAdapter_GetVolumes_ValidationSkip tests skipping validation
func TestPmaxVolumeAdapter_GetVolumes_ValidationSkip(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{"mv-1"},
	}, nil)

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-1").Return(&v100.MaskingView{
		MaskingViewID:  "mv-1",
		StorageGroupID: "sg-1",
		HostID:         "host-1",
	}, nil)

	client.EXPECT().GetMaskingViewConnections(gomock.Any(), "array-1", "mv-1", "").Return([]*v100.MaskingViewConnection{}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{"vol-1"}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(&v100.Volume{
		VolumeID:              "vol-1",
		VolumeIdentifier:      "csivol-uuid-123-FC",
		CapacityGB:            1000,
		StorageGroupIDList:    []string{"sg-1"},
		Status:                "Ready",
		NumberOfFrontEndPaths: 1,
	}, nil)

	validator := &mockValidator{refreshError: true}
	adapter := NewPmaxVolumeAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetVolumes(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

// TestPmaxVolumeAdapter_GetVolumes_NoIdentifier tests skipping volumes without identifier
func TestPmaxVolumeAdapter_GetVolumes_NoIdentifier(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{},
	}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{"vol-1", "vol-2"}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(&v100.Volume{
		VolumeID:              "vol-1",
		VolumeIdentifier:      "", // No identifier, should be skipped
		CapacityGB:            1000,
		StorageGroupIDList:    []string{"sg-1"},
		Status:                "Ready",
		NumberOfFrontEndPaths: 1,
	}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-2").Return(&v100.Volume{
		VolumeID:              "vol-2",
		VolumeIdentifier:      "csivol-uuid-456-FC",
		CapacityGB:            2000,
		StorageGroupIDList:    []string{"sg-2"},
		Status:                "Ready",
		NumberOfFrontEndPaths: 1,
	}, nil)

	validator := &mockValidator{managed: true}
	adapter := NewPmaxVolumeAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetVolumes(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1) // Only vol-2 is returned, vol-1 is skipped
}

// TestPmaxVolumeAdapter_GetVolumes_NotManaged tests validation returning false
func TestPmaxVolumeAdapter_GetVolumes_NotManaged(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{},
	}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{"vol-1"}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(&v100.Volume{
		VolumeID:              "vol-1",
		VolumeIdentifier:      "csivol-uuid-123-FC",
		CapacityGB:            1000,
		StorageGroupIDList:    []string{"sg-1"},
		Status:                "Ready",
		NumberOfFrontEndPaths: 1,
	}, nil)

	validator := &mockValidator{managed: false}
	adapter := NewPmaxVolumeAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetVolumes(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 0) // Volume is not managed, so skipped
}

// TestPmaxVolumeAdapter_GetVolumes_GetVolumeError tests error when getting volume details
func TestPmaxVolumeAdapter_GetVolumes_GetVolumeError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{},
	}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{"vol-1", "vol-2"}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(nil, errors.New("volume not found"))

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-2").Return(&v100.Volume{
		VolumeID:              "vol-2",
		VolumeIdentifier:      "csivol-uuid-456-FC",
		CapacityGB:            2000,
		StorageGroupIDList:    []string{"sg-2"},
		Status:                "Ready",
		NumberOfFrontEndPaths: 1,
	}, nil)

	validator := &mockValidator{managed: true}
	adapter := NewPmaxVolumeAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetVolumes(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1) // Only vol-2 is returned, vol-1 is skipped due to error
}

// TestPmaxVolumeAdapter_GetVolumes_GetMVError tests error when getting masking view details
func TestPmaxVolumeAdapter_GetVolumes_GetMVError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{"mv-1", "mv-2"},
	}, nil)

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-1").Return(nil, errors.New("masking view not found"))

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-2").Return(&v100.MaskingView{
		MaskingViewID:  "mv-2",
		StorageGroupID: "sg-2",
		HostID:         "host-2",
	}, nil)

	client.EXPECT().GetMaskingViewConnections(gomock.Any(), "array-1", "mv-2", "").Return([]*v100.MaskingViewConnection{}, nil)

	client.EXPECT().GetVolumeIDList(gomock.Any(), "array-1", "", false).Return([]string{"vol-2"}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-2").Return(&v100.Volume{
		VolumeID:              "vol-2",
		VolumeIdentifier:      "csivol-uuid-456-FC",
		CapacityGB:            2000,
		StorageGroupIDList:    []string{"sg-2"},
		Status:                "Ready",
		NumberOfFrontEndPaths: 1,
	}, nil)

	validator := &mockValidator{managed: true}
	adapter := NewPmaxVolumeAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	result, err := adapter.GetVolumes(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1) // Only vol-2 is returned, mv-1 is skipped due to error
}

// TestPmaxVolumeAdapter_GetVolumes_Error tests error handling
func TestPmaxVolumeAdapter_GetVolumes_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(nil, errors.New("failed to get masking view list"))

	validator := &mockValidator{managed: true}
	adapter := NewPmaxVolumeAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	_, err := adapter.GetVolumes(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get masking view list")
}

// TestPmaxMVAdapter_GetMaskingViews_Success tests successful masking view retrieval
func TestPmaxMVAdapter_GetMaskingViews_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{"mv-1", "mv-2"},
	}, nil)

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-1").Return(&v100.MaskingView{
		MaskingViewID:  "mv-1",
		StorageGroupID: "sg-1",
	}, nil)

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-2").Return(&v100.MaskingView{
		MaskingViewID:  "mv-2",
		StorageGroupID: "sg-2",
	}, nil)

	conn1 := &v100.MaskingViewConnection{
		VolumeID: "vol-1",
		LoggedIn: true,
		OnFabric: true,
	}

	conn2 := &v100.MaskingViewConnection{
		VolumeID: "vol-2",
		LoggedIn: true,
		OnFabric: true,
	}

	client.EXPECT().GetMaskingViewConnections(gomock.Any(), "array-1", "mv-1", "").Return([]*v100.MaskingViewConnection{conn1}, nil)

	client.EXPECT().GetMaskingViewConnections(gomock.Any(), "array-1", "mv-2", "").Return([]*v100.MaskingViewConnection{conn2}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(&v100.Volume{
		VolumeID:         "vol-1",
		VolumeIdentifier: "csivol-uuid-123-FC",
	}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-2").Return(&v100.Volume{
		VolumeID:         "vol-2",
		VolumeIdentifier: "csivol-uuid-456-FC",
	}, nil)

	adapter := &PmaxMVAdapter{
		Client:        client,
		ArrayID:       "array-1",
		ClusterPrefix: "",
		Validator:     nil,
	}

	result, err := adapter.GetMaskingViews(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

// TestPmaxMVAdapter_GetMaskingViews_NoConnections tests with no connections
func TestPmaxMVAdapter_GetMaskingViews_NoConnections(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{"mv-1"},
	}, nil)

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-1").Return(&v100.MaskingView{
		MaskingViewID:  "mv-1",
		StorageGroupID: "sg-1",
	}, nil)

	client.EXPECT().GetMaskingViewConnections(gomock.Any(), "array-1", "mv-1", "").Return([]*v100.MaskingViewConnection{}, nil)

	adapter := &PmaxMVAdapter{
		Client:        client,
		ArrayID:       "array-1",
		ClusterPrefix: "",
		Validator:     nil,
	}

	result, err := adapter.GetMaskingViews(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 0) // No volumes means no masking views returned
}

// TestPmaxMVAdapter_GetMaskingViews_ValidationSkip tests skipping validation
func TestPmaxMVAdapter_GetMaskingViews_ValidationSkip(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{"mv-1"},
	}, nil)

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-1").Return(&v100.MaskingView{
		MaskingViewID:  "mv-1",
		StorageGroupID: "sg-1",
	}, nil)

	conn1 := &v100.MaskingViewConnection{
		VolumeID: "vol-1",
		LoggedIn: true,
		OnFabric: true,
	}

	client.EXPECT().GetMaskingViewConnections(gomock.Any(), "array-1", "mv-1", "").Return([]*v100.MaskingViewConnection{conn1}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(&v100.Volume{
		VolumeID:         "vol-1",
		VolumeIdentifier: "csivol-uuid-123-FC",
	}, nil)

	validator := &mockValidator{refreshError: true, managed: true}
	adapter := &PmaxMVAdapter{
		Client:        client,
		ArrayID:       "array-1",
		ClusterPrefix: "",
		Validator:     validator,
	}

	result, err := adapter.GetMaskingViews(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

// TestPmaxMVAdapter_GetMaskingViews_GetMVError tests error when getting masking view details
func TestPmaxMVAdapter_GetMaskingViews_GetMVError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{"mv-1", "mv-2"},
	}, nil)

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-1").Return(nil, errors.New("masking view not found"))

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-2").Return(&v100.MaskingView{
		MaskingViewID:  "mv-2",
		StorageGroupID: "sg-2",
	}, nil)

	conn2 := &v100.MaskingViewConnection{
		VolumeID: "vol-2",
		LoggedIn: true,
		OnFabric: true,
	}

	client.EXPECT().GetMaskingViewConnections(gomock.Any(), "array-1", "mv-2", "").Return([]*v100.MaskingViewConnection{conn2}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-2").Return(&v100.Volume{
		VolumeID:         "vol-2",
		VolumeIdentifier: "csivol-uuid-456-FC",
	}, nil)

	adapter := &PmaxMVAdapter{
		Client:        client,
		ArrayID:       "array-1",
		ClusterPrefix: "",
		Validator:     nil,
	}

	result, err := adapter.GetMaskingViews(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 1) // Only mv-2 is returned, mv-1 is skipped due to error
}

// TestPmaxMVAdapter_GetMaskingViews_GetVolumeError tests error when getting volume details
func TestPmaxMVAdapter_GetMaskingViews_GetVolumeError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(&v100.MaskingViewList{
		MaskingViewIDs: []string{"mv-1"},
	}, nil)

	client.EXPECT().GetMaskingViewByID(gomock.Any(), "array-1", "mv-1").Return(&v100.MaskingView{
		MaskingViewID:  "mv-1",
		StorageGroupID: "sg-1",
	}, nil)

	conn1 := &v100.MaskingViewConnection{
		VolumeID: "vol-1",
		LoggedIn: true,
		OnFabric: true,
	}

	client.EXPECT().GetMaskingViewConnections(gomock.Any(), "array-1", "mv-1", "").Return([]*v100.MaskingViewConnection{conn1}, nil)

	client.EXPECT().GetVolumeByID(gomock.Any(), "array-1", "vol-1").Return(nil, errors.New("volume not found"))

	adapter := &PmaxMVAdapter{
		Client:        client,
		ArrayID:       "array-1",
		ClusterPrefix: "",
		Validator:     nil,
	}

	result, err := adapter.GetMaskingViews(context.Background())
	require.NoError(t, err)
	assert.Len(t, result, 0) // No volumes found, so no masking views returned
}

// TestPmaxMVAdapter_GetMaskingViews_Error tests error handling
func TestPmaxMVAdapter_GetMaskingViews_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)

	client.EXPECT().GetMaskingViewList(gomock.Any(), "array-1").Return(nil, errors.New("failed to get masking view list"))

	adapter := &PmaxMVAdapter{
		Client:        client,
		ArrayID:       "array-1",
		ClusterPrefix: "",
		Validator:     nil,
	}

	_, err := adapter.GetMaskingViews(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get masking view list")
}

// mockValidator is a minimal mock for VolumeValidator interface
type mockValidator struct {
	refreshError bool
	managed      bool
}

func (m *mockValidator) IsDriverManaged(_ context.Context, _ string) (bool, error) {
	return m.managed, nil
}

func (m *mockValidator) RefreshCache(_ context.Context) error {
	if m.refreshError {
		return errors.New("refresh error")
	}
	return nil
}

// TestNewPmaxSGAdapterWithRuntime tests the constructor
func TestNewPmaxSGAdapterWithRuntime(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)
	validator := &mockValidator{managed: true}

	adapter := NewPmaxSGAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	assert.NotNil(t, adapter)
	assert.Equal(t, "array-1", adapter.ArrayID)
	assert.Equal(t, "test-cluster", adapter.ClusterPrefix)
}

// TestNewPmaxVolumeAdapterWithRuntime tests the constructor
func TestNewPmaxVolumeAdapterWithRuntime(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := mocks.NewMockPmaxClient(ctrl)
	validator := &mockValidator{managed: true}

	adapter := NewPmaxVolumeAdapterWithRuntime(client, "array-1", "test-cluster", validator, nil)

	assert.NotNil(t, adapter)
	assert.Equal(t, "array-1", adapter.ArrayID)
	assert.Equal(t, "test-cluster", adapter.ClusterPrefix)
}

// TestExtractVolumeIDFromIdentifier tests the helper function for extracting volume ID
func TestExtractVolumeIDFromIdentifier(t *testing.T) {
	// Valid CSI volume identifier
	result := extractVolumeIDFromIdentifier("csivol-00123456-789-FC")
	assert.Equal(t, "00123456", result)

	// Empty identifier
	result = extractVolumeIDFromIdentifier("")
	assert.Equal(t, "", result)

	// Non-CSI identifier
	result = extractVolumeIDFromIdentifier("regular-volume-id")
	assert.Equal(t, "regular-volume-id", result)

	// CSI identifier with multiple parts
	result = extractVolumeIDFromIdentifier("csivol-uuid-123-FC-extra")
	assert.Equal(t, "uuid", result)

	// CSI identifier without enough parts
	result = extractVolumeIDFromIdentifier("csivol-uuid")
	assert.Equal(t, "uuid", result)
}

// TestExtractProtocolFromIdentifier tests the helper function for extracting protocol
func TestExtractProtocolFromIdentifier(t *testing.T) {
	// FC protocol
	result := extractProtocolFromIdentifier("csivol-uuid-123-FC")
	assert.Equal(t, "FC", result)

	// ISCSI protocol
	result = extractProtocolFromIdentifier("csivol-uuid-123-ISCSI")
	assert.Equal(t, "ISCSI", result)

	// NVMETCP protocol
	result = extractProtocolFromIdentifier("csivol-uuid-123-NVMETCP")
	assert.Equal(t, "NVMETCP", result)

	// NVMEFC protocol
	result = extractProtocolFromIdentifier("csivol-uuid-123-NVMEFC")
	assert.Equal(t, "NVMEFC", result)

	// Lowercase protocol
	result = extractProtocolFromIdentifier("csivol-uuid-123-fc")
	assert.Equal(t, "FC", result)

	// Empty identifier
	result = extractProtocolFromIdentifier("")
	assert.Equal(t, "unknown", result)

	// Non-CSI identifier
	result = extractProtocolFromIdentifier("regular-volume-id")
	assert.Equal(t, "unknown", result)

	// CSI identifier without enough parts
	result = extractProtocolFromIdentifier("csivol-uuid")
	assert.Equal(t, "unknown", result)

	// Custom protocol
	result = extractProtocolFromIdentifier("csivol-uuid-123-CUSTOM")
	assert.Equal(t, "CUSTOM", result)
}

// TestNewPmaxSGAdapterWithRuntime_Coverage tests constructor for coverage
func TestNewPmaxSGAdapterWithRuntime_Coverage(t *testing.T) {
	runtime := NewMetricsRuntime("array-1", RuntimeConfig{})
	adapter := NewPmaxSGAdapterWithRuntime(nil, "array-1", "csi-", nil, runtime)
	assert.NotNil(t, adapter)
}
