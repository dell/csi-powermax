package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

func TestU4P104VolumeCreator_DynamicSGEnabled_SelectsExistingSG(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	baseSGName := "csi--Optimized-SRP_1-SG"
	deviceID := "00DYN"
	volumeIdentifier := "csi--dyn-vol"

	// Mock CreateVolume
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, req types.CreateVolumesRequest, _ ...http.Header) (*types.CreateVolumesResponse, error) {
			// Verify the SG name is the one with lowest volume count
			assert.Equal(t, baseSGName, req.Volumes[0].Actions.ManageVolumeStorageGroup.StorageGroup.ID)
			return &types.CreateVolumesResponse{
				Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
				Results: types.CreateVolumesResults{
					Result: []types.CreateVolumeResponseItem{
						{
							Status: "success",
							Volume: &types.VolumeRefResponse{
								ID:         deviceID,
								Identifier: volumeIdentifier,
							},
							StorageGroup: &types.StorageGroupRefResponse{ID: baseSGName},
						},
					},
				},
			}, nil
		}).Times(1)

	// Use mock service deps with getDynamicSG returning the base SG name (existing SG)
	mockSvc := &mockU4P104ServiceDeps{
		dynamicSGEnabled: true,
		dynamicSGName:    baseSGName,
		dynamicSGCreated: false,
	}
	creator := &u4p104VolumeCreator{
		s:             mockSvc,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name: "dyn-vol",
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1073741824,
		},
		Parameters: map[string]string{
			StoragePoolParam:  "SRP_1",
			ServiceLevelParam: "Optimized",
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, baseSGName, resp.Volume.VolumeContext[StorageGroup])
}

func TestBuildHostIOLimitInfo_EmptyReturnsNil(t *testing.T) {
	hostIOLimitInfo, err := buildHostIOLimitInfo("", "", "")
	assert.NoError(t, err)
	assert.Nil(t, hostIOLimitInfo)
}

func TestBuildHostIOLimitInfo_ParsesValues(t *testing.T) {
	hostIOLimitInfo, err := buildHostIOLimitInfo("1000", "5000", "Always")
	assert.NoError(t, err)
	assert.NotNil(t, hostIOLimitInfo)
	assert.Equal(t, 1000, hostIOLimitInfo.HostIOLimitMBSec)
	assert.Equal(t, 5000, hostIOLimitInfo.HostIOLimitIOSec)
	assert.Equal(t, "Always", hostIOLimitInfo.DynamicDistribution)
}

func TestBuildHostIOLimitInfo_InvalidValues(t *testing.T) {
	_, err := buildHostIOLimitInfo("invalid", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), HostIOLimitMBSecParam)

	_, err = buildHostIOLimitInfo("", "invalid", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), HostIOLimitIOSecParam)

	_, err = buildHostIOLimitInfo("-1", "", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "non-negative")

	_, err = buildHostIOLimitInfo("", "-1", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "non-negative")
}

func TestU4P104VolumeCreator_ApplicationPrefixInSGName(t *testing.T) {
	// ApplicationPrefix is supported: it changes the SG name format to
	// csi-<cluster>-<appPrefix>-<serviceLevel>-<SRP>-SG (matching legacy lines 254-256).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	deviceID := "00APP"
	volumeIdentifier := "csi--app-vol"
	expectedSGName := "csi--myapp-Optimized-SRP_1-SG"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Status: "success",
						Volume: &types.VolumeRefResponse{
							ID:         deviceID,
							Identifier: volumeIdentifier,
						},
						StorageGroup: &types.StorageGroupRefResponse{ID: expectedSGName},
					},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "app-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters: map[string]string{
			StoragePoolParam:       "SRP_1",
			ServiceLevelParam:      "Optimized",
			ApplicationPrefixParam: "myapp",
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	// SG name in context must include the application prefix
	assert.Equal(t, expectedSGName, resp.Volume.VolumeContext[StorageGroup])
}

func TestU4P104VolumeCreator_HostLimitNameInSGName(t *testing.T) {
	// HostLimitName is a naming-only param: it appends a suffix to the SG name
	// (matching legacy controller.go lines 364-366).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	deviceID := "00HL1"
	volumeIdentifier := "csi--hl-vol"
	expectedSGName := "csi--Optimized-SRP_1-SG-gold"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Status: "success",
						Volume: &types.VolumeRefResponse{
							ID:         deviceID,
							Identifier: volumeIdentifier,
						},
						StorageGroup: &types.StorageGroupRefResponse{ID: expectedSGName},
					},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "hl-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters: map[string]string{
			StoragePoolParam:   "SRP_1",
			ServiceLevelParam:  "Optimized",
			HostLimitNameParam: "gold",
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	// SG name in context must include the host limit name suffix
	assert.Equal(t, expectedSGName, resp.Volume.VolumeContext[StorageGroup])
}

func TestU4P104VolumeCreator_HostIOLimitInfoInCreateRequest(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	deviceID := "00IOL"
	volumeIdentifier := "csi--io-vol"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, req types.CreateVolumesRequest, _ ...http.Header) (*types.CreateVolumesResponse, error) {
			if assert.Len(t, req.Volumes, 1) {
				actions := req.Volumes[0].Actions
				if assert.NotNil(t, actions) && assert.NotNil(t, actions.ManageVolumeStorageGroup) {
					hostIOLimitInfo := actions.ManageVolumeStorageGroup.StorageGroup.HostIOLimitInfo
					if assert.NotNil(t, hostIOLimitInfo) {
						assert.Equal(t, 1000, hostIOLimitInfo.HostIOLimitMBSec)
						assert.Equal(t, 5000, hostIOLimitInfo.HostIOLimitIOSec)
						assert.Equal(t, "Always", hostIOLimitInfo.DynamicDistribution)
					}
				}
			}

			return &types.CreateVolumesResponse{
				Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
				Results: types.CreateVolumesResults{
					Result: []types.CreateVolumeResponseItem{
						{
							Status: "success",
							Volume: &types.VolumeRefResponse{
								ID:         deviceID,
								Identifier: volumeIdentifier,
							},
						},
					},
				},
			}, nil
		}).Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "io-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters: map[string]string{
			StoragePoolParam:         "SRP_1",
			ServiceLevelParam:        "Optimized",
			HostIOLimitMBSecParam:    "1000",
			HostIOLimitIOSecParam:    "5000",
			DynamicDistributionParam: "Always",
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestU4P104VolumeCreator_StorageGroupParamOverride(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	deviceID := "00SG1"
	volumeIdentifier := "csi--sg-override-vol"
	expectedSGName := "custom-test-sg"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Status: "success",
						Volume: &types.VolumeRefResponse{
							ID:         deviceID,
							Identifier: volumeIdentifier,
						},
					},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "sg-override-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters: map[string]string{
			StoragePoolParam:       "SRP_1",
			ServiceLevelParam:      "Optimized",
			StorageGroupParam:      expectedSGName,
			ApplicationPrefixParam: "myapp",
			HostLimitNameParam:     "gold",
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, expectedSGName, resp.Volume.VolumeContext[StorageGroup])
}

func TestU4P104VolumeCreator_CreateSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"
	volumeName := "test-volume"
	storagePoolID := "SRP_1"
	serviceLevel := "Optimized"
	deviceID := "00ABC"
	// With empty clusterPrefix: volumeIdentifier = "csi--test-volume"
	volumeIdentifier := "csi--" + volumeName

	// Optimized path: single 10.4 CreateVolume call — no pre-flight GetStoragePoolList,
	// GetStoragePool, GetVolumeIDList, or GetVolumeByID calls.
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Status: "success",
						Volume: &types.VolumeRefResponse{
							ID:         deviceID,
							Identifier: volumeIdentifier,
						},
					},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: storagePoolID, ServiceLevelParam: serviceLevel},
	}

	req := &csi.CreateVolumeRequest{
		Name: volumeName,
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1073741824,
		},
		Parameters: map[string]string{
			StoragePoolParam:  storagePoolID,
			ServiceLevelParam: serviceLevel,
		},
	}

	resp, err := creator.Create(context.Background(), req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	// VolumeId format: volumeIdentifier-symID-devID (matches legacy createCSIVolumeID format)
	assert.Equal(t, volumeIdentifier+"-"+symmetrixID+"-"+deviceID, resp.Volume.VolumeId)
	assert.Equal(t, serviceLevel, resp.Volume.VolumeContext[ServiceLevelParam])
	assert.Equal(t, storagePoolID, resp.Volume.VolumeContext[StoragePoolParam])
	// With empty ReplicationContextPrefix on &service{}, key is path.Join("", SymmetrixIDParam) = SymmetrixIDParam
	assert.Equal(t, symmetrixID, resp.Volume.VolumeContext[SymmetrixIDParam])
}

func TestU4P104VolumeCreator_CreateIdempotent(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"
	volumeName := "existing-volume"
	storagePoolID := "SRP_1"
	serviceLevel := "Optimized"
	deviceID := "00DEF"
	// ceil(1073741824 / 1966080) = 547
	capacityCYL := 547
	// With empty clusterPrefix: volumeIdentifier = "csi--existing-volume"
	volumeIdentifier := "csi--" + volumeName
	storageGroupName := "csi--" + serviceLevel + "-" + storagePoolID + "-SG"

	// Fully idempotent: volume already exists and is already in the SG.
	// The array returns 200 with only the storage_group object (no volume object).
	// The driver falls back to GetVolumesByIdentifier to obtain device ID and size.
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: nil},
				},
			},
		}, nil).Times(1)
	mockClient.EXPECT().GetVolumesByIdentifier(gomock.Any(), symmetrixID, volumeIdentifier).
		Return(&types.Volumev1{
			Volumes: []types.VolumeEnhanced{
				{
					ID:         deviceID,
					Identifier: volumeIdentifier,
					CapCyl:     float64(capacityCYL),
					StorageGroups: []types.StorageGroupID{
						{StorageGroupID: storageGroupName},
					},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: storagePoolID, ServiceLevelParam: serviceLevel},
	}

	req := &csi.CreateVolumeRequest{
		Name: volumeName,
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1073741824,
		},
		Parameters: map[string]string{
			StoragePoolParam:  storagePoolID,
			ServiceLevelParam: serviceLevel,
		},
	}

	resp, err := creator.Create(context.Background(), req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	// VolumeId format: volumeIdentifier-symID-devID
	assert.Equal(t, volumeIdentifier+"-"+symmetrixID+"-"+deviceID, resp.Volume.VolumeId)
	// SG name resolved from GetVolumesByIdentifier response
	assert.Equal(t, storageGroupName, resp.Volume.VolumeContext[StorageGroup])
}

func TestU4P104VolumeCreator_IdempotentSizeMismatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"
	volumeName := "existing-volume"
	storagePoolID := "SRP_1"
	serviceLevel := "Optimized"

	// The array enforces size consistency: when volume.identifier matches an existing volume
	// but the requested size differs, the array returns a 500 error directly from CreateVolume.
	// classifyCreateVolumeError wraps this as AlreadyExists.
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(nil, errors.New("create volumes failed: 0x020e0105: defined volume size [1092]Cyls does not match existing volume size [546]Cyls")).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: storagePoolID, ServiceLevelParam: serviceLevel},
	}

	req := &csi.CreateVolumeRequest{
		Name: volumeName,
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 2147483648, // 2 GiB — mismatches existing 1 GiB volume
		},
		Parameters: map[string]string{
			StoragePoolParam:  storagePoolID,
			ServiceLevelParam: serviceLevel,
		},
	}

	resp, err := creator.Create(context.Background(), req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "A volume with the same name exists but has a different size")
	assert.Contains(t, err.Error(), "AlreadyExists")
}

func TestU4P104VolumeCreator_DuplicateIdentifiers(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"

	// The array returns 500 when multiple volumes share the same identifier.
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(nil, errors.New("Multiple volumes found with same identifier or id")).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	req := &csi.CreateVolumeRequest{
		Name:          "dup-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	resp, err := creator.Create(context.Background(), req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "Multiple volumes found")
}

func TestU4P104VolumeCreator_PartiallyIdempotent(t *testing.T) {
	// Volume existed but was removed from SG. Array re-adds it and returns the volume object.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"
	volumeName := "re-added-volume"
	storagePoolID := "SRP_1"
	serviceLevel := "Optimized"
	deviceID := "001FD"
	volumeIdentifier := "csi--" + volumeName
	storageGroupName := "csi--" + serviceLevel + "-" + storagePoolID + "-SG"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Status: "success",
						Volume: &types.VolumeRefResponse{
							ID:         deviceID,
							Identifier: volumeIdentifier,
							CapCyl:     546.0,
							StorageGroups: []types.StorageGroupID{
								{StorageGroupID: storageGroupName},
							},
						},
					},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: storagePoolID, ServiceLevelParam: serviceLevel},
	}

	req := &csi.CreateVolumeRequest{
		Name:          volumeName,
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: storagePoolID, ServiceLevelParam: serviceLevel},
	}

	resp, err := creator.Create(context.Background(), req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, volumeIdentifier+"-"+symmetrixID+"-"+deviceID, resp.Volume.VolumeId)
	// SG resolved from response
	assert.Equal(t, storageGroupName, resp.Volume.VolumeContext[StorageGroup])
}

func TestU4P104VolumeCreator_APIFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"
	volumeName := "test-volume"
	storagePoolID := "SRP_1"

	// Optimized path: single 10.4 call, no pre-flight mocks needed.
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(nil, errors.New("array connection failed")).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: storagePoolID},
	}

	req := &csi.CreateVolumeRequest{
		Name: volumeName,
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1073741824,
		},
		Parameters: map[string]string{StoragePoolParam: storagePoolID},
	}

	resp, err := creator.Create(context.Background(), req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "array connection failed")
}

func TestU4P104VolumeCreator_EmptyResponse(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"
	volumeName := "test-volume"
	storagePoolID := "SRP_1"

	// Optimized path: single 10.4 call, no pre-flight mocks needed.
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: storagePoolID},
	}

	req := &csi.CreateVolumeRequest{
		Name: volumeName,
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1073741824,
		},
		Parameters: map[string]string{StoragePoolParam: storagePoolID},
	}

	resp, err := creator.Create(context.Background(), req)

	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "create volume response is empty")
}

func TestU4P104VolumeCreator_VolumeContextValidation(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"
	volumeName := "test-volume"
	storagePoolID := "SRP_1"
	serviceLevel := "Optimized"
	deviceID := "00GHI"
	volumeIdentifier := "csi--" + volumeName

	// Optimized path: single 10.4 call, no pre-flight mocks needed.
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Status: "success",
						Volume: &types.VolumeRefResponse{
							ID:         deviceID,
							Identifier: volumeIdentifier,
						},
					},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: storagePoolID, ServiceLevelParam: serviceLevel},
	}

	req := &csi.CreateVolumeRequest{
		Name: volumeName,
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1073741824,
		},
		Parameters: map[string]string{
			StoragePoolParam:  storagePoolID,
			ServiceLevelParam: serviceLevel,
		},
	}

	resp, err := creator.Create(context.Background(), req)

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.Volume.VolumeContext)
	// Legacy context keys: ServiceLevelParam, StoragePoolParam, replication/symmetrixID,
	// CapacityGB, ContentSource, StorageGroup, CreationTime
	assert.Equal(t, serviceLevel, resp.Volume.VolumeContext[ServiceLevelParam])
	assert.Equal(t, storagePoolID, resp.Volume.VolumeContext[StoragePoolParam])
	// With empty ReplicationContextPrefix on &service{}, key is path.Join("", SymmetrixIDParam) = SymmetrixIDParam
	assert.Equal(t, symmetrixID, resp.Volume.VolumeContext[SymmetrixIDParam])
}

func TestU4P104VolumeCreator_NilRequest(t *testing.T) {
	creator := &u4p104VolumeCreator{s: &service{}}
	resp, err := creator.Create(context.Background(), nil)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be nil")
}

func TestU4P104VolumeCreator_EmptyVolumeName(t *testing.T) {
	creator := &u4p104VolumeCreator{s: &service{}}
	req := &csi.CreateVolumeRequest{
		Name:          "",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1"},
	}
	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "volume name is required")
}

func TestU4P104VolumeCreator_EmptySRP(t *testing.T) {
	creator := &u4p104VolumeCreator{s: &service{}}
	req := &csi.CreateVolumeRequest{
		Name:          "test-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{},
	}
	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SRP parameter is required")
}

func TestBuildCloneVolumesRequest(t *testing.T) {
	req := buildCloneVolumesRequest(547, "00SRC", "SRP_1", "Optimized", "csi-my-SG", "csi-my-clone", nil, "req-99")

	assert.Equal(t, types.ExecutionOptionSynchronous, req.ExecutionOption)
	assert.Len(t, req.Volumes, 1)

	vol := req.Volumes[0]
	assert.Equal(t, "req-99", vol.RequestID)
	// volume.identifier set for native idempotency
	assert.NotNil(t, vol.Volume)
	assert.Equal(t, "csi-my-clone", vol.Volume.Identifier)
	// create_new: must use create_new_from_attributes with explicit CYL size
	assert.NotNil(t, vol.CreateNew)
	assert.NotNil(t, vol.CreateNew.CreateNewFromAttributes)
	assert.Equal(t, "CYL", vol.CreateNew.CreateNewFromAttributes.CapacityUnit)
	assert.Equal(t, float64(547), vol.CreateNew.CreateNewFromAttributes.VolumeSize)
	// precheck_srp_capacity set
	assert.NotNil(t, vol.CreateNew.PrecheckSrpCapacity)
	assert.Equal(t, "SRP_1", vol.CreateNew.PrecheckSrpCapacity.SRP.ID)
	// actions: manage_volume_storage_group + manage_replication
	assert.NotNil(t, vol.Actions)
	assert.NotNil(t, vol.Actions.ManageVolumeStorageGroup)
	assert.Equal(t, "Add", vol.Actions.ManageVolumeStorageGroup.Action)
	assert.Equal(t, "csi-my-SG", vol.Actions.ManageVolumeStorageGroup.StorageGroup.ID)
	// manage_replication: local CopyFrom with establish_terminate
	assert.NotNil(t, vol.Actions.ManageReplication)
	assert.NotNil(t, vol.Actions.ManageReplication.Local)
	assert.Equal(t, "CopyFrom", vol.Actions.ManageReplication.Local.Action)
	assert.Equal(t, "00SRC", vol.Actions.ManageReplication.Local.Volume.ID)
	assert.NotNil(t, vol.Actions.ManageReplication.Local.EstablishTerminate)
	assert.True(t, *vol.Actions.ManageReplication.Local.EstablishTerminate)
	// manage_identifier must NOT be set
	assert.Nil(t, vol.Actions.ManageIdentifier)
	// response_select
	assert.Equal(t, "id,identifier,cap_cyl,storage_groups", vol.ResponseSelect)
}

func TestU4P104VolumeCreator_CloneSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)

	symmetrixID := "000197900049"
	srcDevID := "00SRC"
	srcCSIVolumeID := "csi--source-vol-" + symmetrixID + "-" + srcDevID
	targetDevID := "00TGT"
	volumeIdentifier := "csi--clone-vol"
	storagePoolID := "SRP_1"
	serviceLevel := "Optimized"

	// Mock: 10.4 CreateVolume — must receive clone request (create_new_from_attributes + CopyFrom)
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, req types.CreateVolumesRequest, _ ...http.Header) (*types.CreateVolumesResponse, error) {
			vol := req.Volumes[0]
			// Verify: create_new_from_attributes set with CYL size from the request (547 cyl = 1 GiB)
			assert.NotNil(t, vol.CreateNew.CreateNewFromAttributes)
			assert.Equal(t, "CYL", vol.CreateNew.CreateNewFromAttributes.CapacityUnit)
			assert.Equal(t, float64(547), vol.CreateNew.CreateNewFromAttributes.VolumeSize)
			// Verify: NO create_new_from_snapshot (this is a clone, not snapshot restore)
			assert.Nil(t, vol.CreateNew.CreateNewFromSnapshot)
			// Verify: manage_replication CopyFrom with establish_terminate
			assert.NotNil(t, vol.Actions.ManageReplication)
			assert.NotNil(t, vol.Actions.ManageReplication.Local)
			assert.Equal(t, "CopyFrom", vol.Actions.ManageReplication.Local.Action)
			assert.Equal(t, srcDevID, vol.Actions.ManageReplication.Local.Volume.ID)
			assert.NotNil(t, vol.Actions.ManageReplication.Local.EstablishTerminate)
			assert.True(t, *vol.Actions.ManageReplication.Local.EstablishTerminate)

			return &types.CreateVolumesResponse{
				Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
				Results: types.CreateVolumesResults{
					Result: []types.CreateVolumeResponseItem{
						{
							Status: "success",
							Volume: &types.VolumeRefResponse{
								ID:         targetDevID,
								Identifier: volumeIdentifier,
							},
						},
					},
				},
			}, nil
		}).Times(1)

	deps := &snapshotDeps{service: &service{}, licenseErr: nil}
	creator := &u4p104VolumeCreator{
		s:             deps,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: storagePoolID, ServiceLevelParam: serviceLevel},
	}

	req := &csi.CreateVolumeRequest{
		Name:          "clone-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: storagePoolID, ServiceLevelParam: serviceLevel},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: srcCSIVolumeID,
				},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, volumeIdentifier+"-"+symmetrixID+"-"+targetDevID, resp.Volume.VolumeId)
	// ContentSource in context should reference the source CSI volume ID
	assert.Equal(t, srcCSIVolumeID, resp.Volume.VolumeContext[ContentSource])
	// CSI ContentSource must be set
	assert.NotNil(t, resp.Volume.ContentSource)
}

func TestU4P104VolumeCreator_CloneEmptySourceID(t *testing.T) {
	creator := &u4p104VolumeCreator{s: &service{}}
	req := &csi.CreateVolumeRequest{
		Name:          "clone-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: "",
				},
			},
		},
	}
	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Source volume ID is required")
}

func TestU4P104VolumeCreator_CloneBadSourceFormat(t *testing.T) {
	creator := &u4p104VolumeCreator{s: &service{}}
	req := &csi.CreateVolumeRequest{
		Name:          "clone-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: "bad-format",
				},
			},
		},
	}
	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not in supported format")
}

func TestU4P104VolumeCreator_CloneCrossArrayFails(t *testing.T) {
	creator := &u4p104VolumeCreator{
		s:           &service{},
		symmetrixID: "000197900049",
	}
	// Source is on a different array
	req := &csi.CreateVolumeRequest{
		Name:          "clone-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: "csi--src-vol-000197900099-00SRC",
				},
			},
		},
	}
	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "same PowerMax array")
}

func TestU4P104VolumeCreator_CloneLargerCapacitySucceeds(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	srcDevID := "00SRC"
	targetDevID := "00TGT"
	volumeIdentifier := "csi--clone-vol"

	// CreateVolume should be called with the larger requested size (1094 cyl) from the request
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, req types.CreateVolumesRequest, _ ...http.Header) (*types.CreateVolumesResponse, error) {
			// Verify: create_new_from_attributes with 1093 cylinders (2 GiB)
			assert.NotNil(t, req.Volumes[0].CreateNew.CreateNewFromAttributes)
			assert.Equal(t, "CYL", req.Volumes[0].CreateNew.CreateNewFromAttributes.CapacityUnit)
			assert.Equal(t, float64(1093), req.Volumes[0].CreateNew.CreateNewFromAttributes.VolumeSize)
			// Verify: CopyFrom the source
			assert.NotNil(t, req.Volumes[0].Actions.ManageReplication)
			assert.Equal(t, "CopyFrom", req.Volumes[0].Actions.ManageReplication.Local.Action)
			assert.Equal(t, srcDevID, req.Volumes[0].Actions.ManageReplication.Local.Volume.ID)

			return &types.CreateVolumesResponse{
				Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
				Results: types.CreateVolumesResults{
					Result: []types.CreateVolumeResponseItem{
						{
							Status: "success",
							Volume: &types.VolumeRefResponse{
								ID:         targetDevID,
								Identifier: volumeIdentifier,
							},
						},
					},
				},
			}, nil
		}).Times(1)

	deps := &snapshotDeps{service: &service{}, licenseErr: nil}
	creator := &u4p104VolumeCreator{
		s:             deps,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	// Request 2 GiB (1094 cylinders) — larger than source
	req := &csi.CreateVolumeRequest{
		Name:          "clone-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 2147483648},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: "csi--src-vol-" + symmetrixID + "-00SRC",
				},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Contains(t, resp.Volume.VolumeId, targetDevID)
}

func TestU4P104VolumeCreator_IdempotentGetVolumesByIdentifierFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	// CreateVolume returns nil volume (fully idempotent), then GetVolumesByIdentifier fails.
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: nil},
				},
			},
		}, nil).Times(1)
	mockClient.EXPECT().GetVolumesByIdentifier(gomock.Any(), symmetrixID, gomock.Any()).
		Return(nil, errors.New("connection timeout")).Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	req := &csi.CreateVolumeRequest{
		Name:          "test-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "idempotency check failed")
}

func TestU4P104VolumeCreator_IdempotentEmptyVolumeList(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	// CreateVolume returns nil volume (fully idempotent), but GetVolumesByIdentifier returns empty.
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: nil},
				},
			},
		}, nil).Times(1)
	mockClient.EXPECT().GetVolumesByIdentifier(gomock.Any(), symmetrixID, gomock.Any()).
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{}}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	req := &csi.CreateVolumeRequest{
		Name:          "ghost-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "reported as existing but not found")
}

func TestU4P104VolumeCreator_IdempotentCapacityBytes(t *testing.T) {
	// Verify that CapacityBytes is correctly computed in the idempotent fallback path.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"
	deviceID := "00DEF"
	volumeIdentifier := "csi--cap-vol"
	// 1 GiB → ceil(1073741824 / 1966080) = 547 cylinders
	expectedCylinders := 547
	expectedCapacityBytes := int64(expectedCylinders) * 1966080

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: nil},
				},
			},
		}, nil).Times(1)
	mockClient.EXPECT().GetVolumesByIdentifier(gomock.Any(), symmetrixID, volumeIdentifier).
		Return(&types.Volumev1{
			Volumes: []types.VolumeEnhanced{
				{
					ID:         deviceID,
					Identifier: volumeIdentifier,
					CapCyl:     float64(expectedCylinders),
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	req := &csi.CreateVolumeRequest{
		Name:          "cap-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, expectedCapacityBytes, resp.Volume.CapacityBytes)
}

func TestU4P104VolumeCreator_NamespaceInIdentifier(t *testing.T) {
	// Verify namespace is appended to volumeIdentifier when CSIPVCNamespace is present.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"
	deviceID := "00XYZ"
	volumeName := "ns-vol"
	namespace := "prod-ns"
	// With empty clusterPrefix: "csi--ns-vol-prod-ns"
	expectedIdentifier := "csi--" + volumeName + "-" + namespace

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Status: "success",
						Volume: &types.VolumeRefResponse{
							ID:         deviceID,
							Identifier: expectedIdentifier,
						},
					},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	req := &csi.CreateVolumeRequest{
		Name:          volumeName,
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters: map[string]string{
			StoragePoolParam:  "SRP_1",
			ServiceLevelParam: "Optimized",
			CSIPVCNamespace:   namespace,
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Contains(t, resp.Volume.VolumeId, expectedIdentifier)
}

func TestU4P104VolumeCreator_EmptyDeviceID(t *testing.T) {
	// If the array returns a volume object with an empty device ID, the driver must
	// return an error rather than silently building a broken VolumeId.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Status: "success",
						Volume: &types.VolumeRefResponse{
							ID:         "", // empty — should trigger an error
							Identifier: "csi--empty-id-vol",
						},
					},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "empty-id-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "empty device ID")
}

func TestU4P104VolumeCreator_MultiSGResolvesTargetSG(t *testing.T) {
	// Volume already belongs to Bronze SG. Request adds it to Diamond SG.
	// The response volume.storage_groups lists both, but the top-level storage_group
	// object correctly identifies the target SG (Diamond). We must use that.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"
	deviceID := "001FA"
	volumeIdentifier := "csi--multi-sg-vol"
	targetSG := "csi--Diamond-SRP_1-SG"
	otherSG := "csi--Bronze-SRP_1-SG"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Volume: &types.VolumeRefResponse{
							ID:         deviceID,
							Identifier: volumeIdentifier,
							CapCyl:     546.0,
							StorageGroups: []types.StorageGroupID{
								{StorageGroupID: otherSG},
								{StorageGroupID: targetSG},
							},
						},
						StorageGroup: &types.StorageGroupRefResponse{
							ID:           targetSG,
							NumOfVolumes: 5,
						},
						Status: "success",
					},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "multi-sg-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Diamond"},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	// Must resolve to the target SG (Diamond), not the first SG in the list (Bronze)
	assert.Equal(t, targetSG, resp.Volume.VolumeContext[StorageGroup])
}

func TestU4P104VolumeCreator_TenantPrefixInIdentifier(t *testing.T) {
	// When the array applies a tenant prefix to the volume identifier,
	// the VolumeId must use the array-returned identifier, not the locally-built one.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockService := &service{}

	symmetrixID := "000197900049"
	deviceID := "00TPX"
	localIdentifier := "csi--tenant-vol"
	// Array returns identifier with tenant prefix
	arrayIdentifier := "tn1-csi--tenant-vol"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Status: "success",
						Volume: &types.VolumeRefResponse{
							ID:         deviceID,
							Identifier: arrayIdentifier,
							StorageGroups: []types.StorageGroupID{
								{StorageGroupID: "tn1-csi--Optimized-SRP_1-SG"},
							},
						},
						StorageGroup: &types.StorageGroupRefResponse{
							ID: "tn1-csi--Optimized-SRP_1-SG",
						},
					},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             mockService,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "tenant-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	// VolumeId must use array-returned identifier (with tenant prefix), not local one
	expectedVolumeID := arrayIdentifier + "-" + symmetrixID + "-" + deviceID
	assert.Equal(t, expectedVolumeID, resp.Volume.VolumeId)
	// Confirm the tenant prefix is present (not the local identifier without prefix)
	assert.True(t, len(resp.Volume.VolumeId) > len(localIdentifier+"-"+symmetrixID+"-"+deviceID),
		"VolumeId should be longer due to tenant prefix")
	// SG must come from the response too
	assert.Equal(t, "tn1-csi--Optimized-SRP_1-SG", resp.Volume.VolumeContext[StorageGroup])
}

func TestU4P104VolumeCreator_IdempotentSGFromCreateVolumeResponse(t *testing.T) {
	// Fully idempotent path: CreateVolume returns nil volume but a StorageGroup object.
	// The SG name must come from result.StorageGroup.ID in the CreateVolume response,
	// not the locally-constructed name (supports tenant-prefixed SG names).
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	deviceID := "00SGR"
	volumeIdentifier := "csi--sg-vol"
	// Array returns a tenant-prefixed SG name
	arraySGName := "tn1-csi--Optimized-SRP_1-SG"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Status: "success",
						Volume: nil,
						StorageGroup: &types.StorageGroupRefResponse{
							ID: arraySGName,
						},
					},
				},
			},
		}, nil).Times(1)
	mockClient.EXPECT().GetVolumesByIdentifier(gomock.Any(), symmetrixID, volumeIdentifier).
		Return(&types.Volumev1{
			Volumes: []types.VolumeEnhanced{
				{
					ID:         deviceID,
					Identifier: volumeIdentifier,
					CapCyl:     547,
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "sg-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	// SG must come from result.StorageGroup.ID, not the locally-built "csi--Optimized-SRP_1-SG"
	assert.Equal(t, arraySGName, resp.Volume.VolumeContext[StorageGroup])
}

func TestComputeRequiredCylinders(t *testing.T) {
	tests := []struct {
		name          string
		requiredBytes int64
		limitBytes    int64
		expectedCyl   int
		expectError   bool
		errorContains string
	}{
		{
			name: "nil CapacityRange uses default",
			// DefaultVolumeSizeBytes=1073741824 → ceil(1073741824/1966080) = 547
			expectedCyl: 547,
		},
		{
			name:          "1 GiB request",
			requiredBytes: 1073741824,
			expectedCyl:   547,
		},
		{
			name:          "exact cylinder boundary above minimum",
			requiredBytes: 51118080, // MinVolumeSizeBytes = 26 * 1966080
			expectedCyl:   26,
		},
		{
			name:          "partial cylinder above minimum rounds up",
			requiredBytes: 51118081,
			expectedCyl:   27,
		},
		{
			name: "zero bytes uses default",
			// DefaultVolumeSizeBytes=1073741824 → 547 cylinders
			expectedCyl: 547,
		},
		{
			name:          "below minimum uses minimum",
			requiredBytes: 1,
			// MinVolumeSizeBytes=51118080 → ceil(51118080/1966080) = 26
			expectedCyl: 26,
		},
		{
			name:          "negative required bytes",
			requiredBytes: -1,
			expectError:   true,
			errorContains: "must not be negative",
		},
		{
			name:          "negative limit bytes",
			requiredBytes: 1073741824,
			limitBytes:    -1,
			expectError:   true,
			errorContains: "must not be negative",
		},
		{
			name:          "exceeds limit",
			requiredBytes: 1073741824,
			limitBytes:    1073741824, // aligned size (547 cyl * 1966080) > 1073741824
			expectError:   true,
			errorContains: "exceeds limit",
		},
		{
			name:          "within limit",
			requiredBytes: 1073741824,
			limitBytes:    1075445760, // exactly 547 * 1966080
			expectedCyl:   547,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cr *csi.CapacityRange
			if tt.requiredBytes != 0 || tt.limitBytes != 0 {
				cr = &csi.CapacityRange{
					RequiredBytes: tt.requiredBytes,
					LimitBytes:    tt.limitBytes,
				}
			}
			cyl, err := computeRequiredCylinders(cr)
			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedCyl, cyl)
			}
		})
	}
}

func TestBuildCreateVolumesRequest(t *testing.T) {
	req := buildCreateVolumesRequest(547, "SRP_1", "Optimized", "csi-my-SG", "csi-my-vol", nil, "req-42")

	assert.Equal(t, "req-42", req.Volumes[0].RequestID)
	assert.Equal(t, types.ExecutionOptionSynchronous, req.ExecutionOption)
	assert.Len(t, req.Volumes, 1)

	vol := req.Volumes[0]
	// volume.identifier set for native idempotency
	assert.NotNil(t, vol.Volume)
	assert.Equal(t, "csi-my-vol", vol.Volume.Identifier)
	// create_new set
	assert.NotNil(t, vol.CreateNew)
	assert.NotNil(t, vol.CreateNew.CreateNewFromAttributes)
	assert.Equal(t, "CYL", vol.CreateNew.CreateNewFromAttributes.CapacityUnit)
	assert.Equal(t, float64(547), vol.CreateNew.CreateNewFromAttributes.VolumeSize)
	// precheck_srp_capacity set
	assert.NotNil(t, vol.CreateNew.PrecheckSrpCapacity)
	assert.Equal(t, "SRP_1", vol.CreateNew.PrecheckSrpCapacity.SRP.ID)
	// actions: manage_volume_storage_group set, manage_identifier NOT set
	assert.NotNil(t, vol.Actions)
	assert.NotNil(t, vol.Actions.ManageVolumeStorageGroup)
	assert.Equal(t, "Add", vol.Actions.ManageVolumeStorageGroup.Action)
	assert.Equal(t, "csi-my-SG", vol.Actions.ManageVolumeStorageGroup.StorageGroup.ID)
	assert.NotNil(t, vol.Actions.ManageVolumeStorageGroup.StorageGroup.SRP)
	assert.Equal(t, "SRP_1", vol.Actions.ManageVolumeStorageGroup.StorageGroup.SRP.ID)
	assert.NotNil(t, vol.Actions.ManageVolumeStorageGroup.StorageGroup.ServiceLevel)
	assert.Equal(t, "Optimized", vol.Actions.ManageVolumeStorageGroup.StorageGroup.ServiceLevel.ID)
	assert.Nil(t, vol.Actions.ManageVolumeStorageGroup.StorageGroup.HostIOLimitInfo)
	assert.Nil(t, vol.Actions.ManageIdentifier) // must NOT be set — causes 500
	// response_select at per-volume level
	assert.Equal(t, "id,identifier,cap_cyl,storage_groups", vol.ResponseSelect)
}

func TestBuildCreateVolumesRequest_WithHostIOLimitInfo(t *testing.T) {
	hostIOLimitInfo := &types.HostIOLimitInfo{
		HostIOLimitMBSec:    1000,
		HostIOLimitIOSec:    5000,
		DynamicDistribution: "Always",
	}
	req := buildCreateVolumesRequest(547, "SRP_1", "Optimized", "csi-my-SG", "csi-my-vol", hostIOLimitInfo, "req-42")

	assert.Len(t, req.Volumes, 1)
	vol := req.Volumes[0]
	assert.NotNil(t, vol.Actions)
	assert.NotNil(t, vol.Actions.ManageVolumeStorageGroup)
	assert.NotNil(t, vol.Actions.ManageVolumeStorageGroup.StorageGroup.HostIOLimitInfo)
	assert.Equal(t, 1000, vol.Actions.ManageVolumeStorageGroup.StorageGroup.HostIOLimitInfo.HostIOLimitMBSec)
	assert.Equal(t, 5000, vol.Actions.ManageVolumeStorageGroup.StorageGroup.HostIOLimitInfo.HostIOLimitIOSec)
	assert.Equal(t, "Always", vol.Actions.ManageVolumeStorageGroup.StorageGroup.HostIOLimitInfo.DynamicDistribution)
}

func TestBuildVolumeContext(t *testing.T) {
	ctx := buildVolumeContext("", "000197900049", "Optimized", "SRP_1", "csi-test-sg", 547, "")
	assert.Equal(t, "Optimized", ctx[ServiceLevelParam])
	assert.Equal(t, "SRP_1", ctx[StoragePoolParam])
	assert.Equal(t, "000197900049", ctx[SymmetrixIDParam])
	assert.Equal(t, "547.00", ctx[CapacityGB])
	assert.Equal(t, "", ctx[ContentSource])
	assert.Equal(t, "csi-test-sg", ctx[StorageGroup])
	assert.NotEmpty(t, ctx["CreationTime"])
}

func TestBuildVolumeContext_WithContentSource(t *testing.T) {
	csiSnapID := "snap1-000197900049-00ABC"
	ctx := buildVolumeContext("", "000197900049", "Optimized", "SRP_1", "csi-test-sg", 547, csiSnapID)
	assert.Equal(t, csiSnapID, ctx[ContentSource])
}

// ---------------------------------------------------------------------------
// Stage 8 — Snapshot restore unit tests
// ---------------------------------------------------------------------------

// snapshotDeps is a test-local U4P104ServiceDeps that controls snapshot license outcomes
// without requiring a real Unisphere connection.
type snapshotDeps struct {
	*service
	licenseErr error
}

func (d *snapshotDeps) isSnapshotLicensed(_ context.Context, _ string, _ pmax.Pmax) error {
	return d.licenseErr
}

func newSnapshotCreator(ctrl *gomock.Controller, symmetrixID string, licenseErr error) (*u4p104VolumeCreator, *mocks.MockPmaxClient) {
	mc := mocks.NewMockPmaxClient(ctrl)
	deps := &snapshotDeps{service: &service{}, licenseErr: licenseErr}
	return &u4p104VolumeCreator{
		s:             deps,
		pmaxClient:    mc,
		pmaxClient104: mc,
		symmetrixID:   symmetrixID,
	}, mc
}

// mockSnapshotID is the fake snap_id returned by GetSnapshotInfo in unit tests.
const mockSnapshotID = int64(100661523201)

// mockSnapshotInfo builds a VolumeSnapshot with a single source generation carrying
// mockSnapshotID, matching the 10.4 API expectation.
func mockSnapshotInfo(snapName string) *types.VolumeSnapshot {
	return &types.VolumeSnapshot{
		SnapshotName: snapName,
		VolumeSnapshotSource: []types.VolumeSnapshotSource{
			{SnapshotName: snapName, SnapID: mockSnapshotID, TimeStamp: "12:22:16 Wed, 25 Mar 2026 +0000"},
		},
	}
}

// csiSnapshotID builds a CSI snapshot ID in the format expected by parseCsiID:
// <snapName>-<symID>-<devID>
func csiSnapshotID(snapName, symID, devID string) string {
	return snapName + "-" + symID + "-" + devID
}

func TestU4P104VolumeCreator_CreateFromSnapshot_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	symmetrixID := "000197900049"
	srcDevID := "00SRC"
	snapName := "mysnap"
	snapCSIID := csiSnapshotID(snapName, symmetrixID, srcDevID)
	newDevID := "00NEW"
	storagePoolID := "SRP_1"
	serviceLevel := "Optimized"
	sgName := "csi--Optimized-SRP_1-SG"

	creator, mc := newSnapshotCreator(ctrl, symmetrixID, nil)

	// Pre-flight: GetSnapshotInfo to obtain snap_id for 10.4 API
	mc.EXPECT().GetSnapshotInfo(gomock.Any(), symmetrixID, srcDevID, snapName).
		Return(mockSnapshotInfo(snapName), nil).Times(1)

	// Single 10.4 create call — verify the request body
	mc.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, req types.CreateVolumesRequest, _ ...http.Header) (*types.CreateVolumesResponse, error) {
			vol := req.Volumes[0]
			// Verify: create_new_from_snapshot with correct snap_id from GetSnapshotInfo
			assert.NotNil(t, vol.CreateNew.CreateNewFromSnapshot)
			assert.Equal(t, fmt.Sprintf("%d", mockSnapshotID), vol.CreateNew.CreateNewFromSnapshot.Snapshot.ID)
			// Verify: new_volume_attributes set with CYL size matching the request
			assert.NotNil(t, vol.CreateNew.CreateNewFromSnapshot.NewVolumeAttributes)
			assert.Equal(t, "CYL", vol.CreateNew.CreateNewFromSnapshot.NewVolumeAttributes.CapacityUnit)
			assert.Equal(t, float64(547), vol.CreateNew.CreateNewFromSnapshot.NewVolumeAttributes.VolumeSize)
			// Verify: top-level create_new_from_attributes is NOT set (only nested inside snapshot)
			assert.Nil(t, vol.CreateNew.CreateNewFromAttributes)
			// Verify: manage_volume_storage_group present
			assert.NotNil(t, vol.Actions.ManageVolumeStorageGroup)
			assert.Equal(t, "Add", vol.Actions.ManageVolumeStorageGroup.Action)
			// Verify: NO manage_replication (snapshots don't use CopyFrom)
			assert.Nil(t, vol.Actions.ManageReplication)

			return &types.CreateVolumesResponse{
				Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
				Results: types.CreateVolumesResults{
					Result: []types.CreateVolumeResponseItem{
						{
							Status: "success",
							Volume: &types.VolumeRefResponse{
								ID:         newDevID,
								Identifier: "csi--snap-vol",
							},
							StorageGroup: &types.StorageGroupRefResponse{ID: sgName},
						},
					},
				},
			}, nil
		}).Times(1)

	req := &csi.CreateVolumeRequest{
		Name:          "snap-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: storagePoolID, ServiceLevelParam: serviceLevel},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapCSIID},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Contains(t, resp.Volume.VolumeId, newDevID)
	assert.Contains(t, resp.Volume.VolumeId, symmetrixID)
	// ContentSource must be set to the parsed snapshot name (legacy parity)
	assert.Equal(t, snapName, resp.Volume.VolumeContext[ContentSource])
	// StorageGroup must come from the response
	assert.Equal(t, sgName, resp.Volume.VolumeContext[StorageGroup])
}

func TestU4P104VolumeCreator_CreateFromSnapshot_IdempotentFallback(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	symmetrixID := "000197900049"
	srcDevID := "00SRC"
	snapName := "mysnap"
	snapCSIID := csiSnapshotID(snapName, symmetrixID, srcDevID)
	existingDevID := "00EXI"
	volumeIdentifier := "csi--snap-idem-vol"
	sgName := "csi--Optimized-SRP_1-SG"

	creator, mc := newSnapshotCreator(ctrl, symmetrixID, nil)

	// Pre-flight: GetSnapshotInfo to obtain snap_id
	mc.EXPECT().GetSnapshotInfo(gomock.Any(), symmetrixID, srcDevID, "mysnap").
		Return(mockSnapshotInfo("mysnap"), nil).Times(1)

	// CreateVolume returns nil volume (fully idempotent)
	mc.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: nil, StorageGroup: &types.StorageGroupRefResponse{ID: sgName}},
				},
			},
		}, nil).Times(1)

	// Idempotent fallback lookup
	mc.EXPECT().GetVolumesByIdentifier(gomock.Any(), symmetrixID, gomock.Any()).
		Return(&types.Volumev1{
			Volumes: []types.VolumeEnhanced{
				{ID: existingDevID, Identifier: volumeIdentifier, CapCyl: 547},
			},
		}, nil).Times(1)

	req := &csi.CreateVolumeRequest{
		Name:          "snap-idem-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapCSIID},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Contains(t, resp.Volume.VolumeId, existingDevID)
	// ContentSource preserved through idempotent path (parsed snapshot name, not full CSI ID)
	assert.Equal(t, snapName, resp.Volume.VolumeContext[ContentSource])
	// SG from CreateVolume response, not locally-computed
	assert.Equal(t, sgName, resp.Volume.VolumeContext[StorageGroup])
}

func TestU4P104VolumeCreator_CreateFromSnapshot_InvalidSnapshotID(t *testing.T) {
	creator := &u4p104VolumeCreator{s: &service{}, symmetrixID: "000197900049"}

	req := &csi.CreateVolumeRequest{
		Name:          "bad-snap-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: "bad"},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Snapshot identifier not in supported format")
}

func TestU4P104VolumeCreator_CreateFromSnapshot_SourceArrayMismatch(t *testing.T) {
	// Snapshot CSI ID references a different array than the creator's symmetrixID
	creator := &u4p104VolumeCreator{s: &service{}, symmetrixID: "000197900049"}

	otherArray := "000197900046"
	snapCSIID := csiSnapshotID("mysnap", otherArray, "00SRC")

	req := &csi.CreateVolumeRequest{
		Name:          "mismatch-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapCSIID},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "The volume content source is in different PowerMax array")
}

func TestU4P104VolumeCreator_CreateFromSnapshot_SnapshotLicenseFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	symmetrixID := "000197900049"
	snapCSIID := csiSnapshotID("mysnap", symmetrixID, "00SRC")

	// License check returns error
	creator, mc := newSnapshotCreator(ctrl, symmetrixID, errors.New("PowerMax array (000197900049) doesn't have Snapshot license"))

	// handleSnapshotSource calls GetSnapshotInfo before the license check
	mc.EXPECT().GetSnapshotInfo(gomock.Any(), symmetrixID, "00SRC", "mysnap").
		Return(mockSnapshotInfo("mysnap"), nil).Times(1)

	req := &csi.CreateVolumeRequest{
		Name:          "unlicensed-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapCSIID},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Snapshot license")
}

func TestU4P104VolumeCreator_CreateFromSnapshot_CreateAPIError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	symmetrixID := "000197900049"
	srcDevID := "00SRC"
	snapCSIID := csiSnapshotID("mysnap", symmetrixID, srcDevID)

	creator, mc := newSnapshotCreator(ctrl, symmetrixID, nil)

	mc.EXPECT().GetSnapshotInfo(gomock.Any(), symmetrixID, srcDevID, "mysnap").
		Return(mockSnapshotInfo("mysnap"), nil).Times(1)
	mc.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(nil, errors.New("backend error")).Times(1)

	req := &csi.CreateVolumeRequest{
		Name:          "api-err-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapCSIID},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "backend error")
}

func TestU4P104VolumeCreator_CreateFromSnapshot_EmptyCreateResponse(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	symmetrixID := "000197900049"
	srcDevID := "00SRC"
	snapCSIID := csiSnapshotID("mysnap", symmetrixID, srcDevID)

	creator, mc := newSnapshotCreator(ctrl, symmetrixID, nil)

	mc.EXPECT().GetSnapshotInfo(gomock.Any(), symmetrixID, srcDevID, "mysnap").
		Return(mockSnapshotInfo("mysnap"), nil).Times(1)
	mc.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{Results: types.CreateVolumesResults{}}, nil).Times(1)

	req := &csi.CreateVolumeRequest{
		Name:          "empty-resp-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapCSIID},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create volume response is empty")
}

func TestU4P104VolumeCreator_CreateFromSnapshot_SizeLargerSucceeds(t *testing.T) {
	// When requested size > snapshot source, the 10.4 API now supports explicit
	// size via new_volume_attributes, so the request should succeed.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	symmetrixID := "000197900049"
	srcDevID := "00SRC"
	snapName := "mysnap"
	snapCSIID := csiSnapshotID(snapName, symmetrixID, srcDevID)
	newDevID := "00LRG"
	volumeIdentifier := "csi--larger-vol"

	creator, mc := newSnapshotCreator(ctrl, symmetrixID, nil)

	// Pre-flight: GetSnapshotInfo to obtain snap_id
	mc.EXPECT().GetSnapshotInfo(gomock.Any(), symmetrixID, srcDevID, snapName).
		Return(mockSnapshotInfo(snapName), nil).Times(1)

	// CreateVolume should be called with larger size in new_volume_attributes (size from request)
	mc.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, req types.CreateVolumesRequest, _ ...http.Header) (*types.CreateVolumesResponse, error) {
			// Verify: new_volume_attributes set with 1093 cylinders
			assert.NotNil(t, req.Volumes[0].CreateNew.CreateNewFromSnapshot)
			assert.NotNil(t, req.Volumes[0].CreateNew.CreateNewFromSnapshot.NewVolumeAttributes)
			assert.Equal(t, "CYL", req.Volumes[0].CreateNew.CreateNewFromSnapshot.NewVolumeAttributes.CapacityUnit)
			assert.Equal(t, float64(1093), req.Volumes[0].CreateNew.CreateNewFromSnapshot.NewVolumeAttributes.VolumeSize)

			return &types.CreateVolumesResponse{
				Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
				Results: types.CreateVolumesResults{
					Result: []types.CreateVolumeResponseItem{
						{
							Status: "success",
							Volume: &types.VolumeRefResponse{
								ID:         newDevID,
								Identifier: volumeIdentifier,
							},
						},
					},
				},
			}, nil
		}).Times(1)

	req := &csi.CreateVolumeRequest{
		Name:          "larger-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 2 * 1073741824}, // 1094 cyl > 547
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapCSIID},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Contains(t, resp.Volume.VolumeId, newDevID)
}

func TestU4P104VolumeCreator_CreateFromSnapshot_GetSnapshotInfoFailure(t *testing.T) {
	// When GetSnapshotInfo fails, the driver should return an error indicating
	// the snapshot was not found on the source volume.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	symmetrixID := "000197900049"
	srcDevID := "00SRC"
	snapCSIID := csiSnapshotID("mysnap", symmetrixID, srcDevID)

	creator, mc := newSnapshotCreator(ctrl, symmetrixID, nil)

	mc.EXPECT().GetSnapshotInfo(gomock.Any(), symmetrixID, srcDevID, "mysnap").
		Return(nil, errors.New("snapshot not found")).Times(1)

	req := &csi.CreateVolumeRequest{
		Name:          "snap-info-err-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapCSIID},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Snapshot mysnap not found on source volume 00SRC")
}

// mockU4P104ServiceDeps is a mock implementation of U4P104ServiceDeps for testing
type mockU4P104ServiceDeps struct {
	dynamicSGEnabled bool
	clusterPrefix    string
	blockEnabled     bool
	arrayLabels      map[string]string
	// getDynamicSG mock return values
	dynamicSGName    string
	dynamicSGCreated bool
	dynamicSGErr     error
}

func (m *mockU4P104ServiceDeps) resolveParameter(params map[string]string, _, key, defaultVal string) string {
	if val, ok := params[key]; ok {
		return val
	}
	return defaultVal
}

func (m *mockU4P104ServiceDeps) isDynamicSGEnabled() bool {
	return m.dynamicSGEnabled
}

func (m *mockU4P104ServiceDeps) getReplicationPrefix() string {
	return ""
}

func (m *mockU4P104ServiceDeps) getReplicationContextPrefix() string {
	return ""
}

func (m *mockU4P104ServiceDeps) getClusterPrefix() string {
	return m.clusterPrefix
}

func (m *mockU4P104ServiceDeps) parseCsiID(_ string) (string, string, string, string, string, error) {
	return "", "", "", "", "", nil
}

func (m *mockU4P104ServiceDeps) isSnapshotLicensed(_ context.Context, _ string, _ pmax.Pmax) error {
	return nil
}

func (m *mockU4P104ServiceDeps) getDynamicSG(_ context.Context, _, baseSGName string) (string, bool, error) {
	if m.dynamicSGErr != nil {
		return "", false, m.dynamicSGErr
	}
	if m.dynamicSGName != "" {
		return m.dynamicSGName, m.dynamicSGCreated, nil
	}
	// Default: return baseSGName as existing SG
	return baseSGName, false, nil
}

func (m *mockU4P104ServiceDeps) getStorageArrayLabels(_ string) map[string]string {
	return m.arrayLabels
}

func (m *mockU4P104ServiceDeps) isBlockEnabled() bool {
	return m.blockEnabled
}

func TestU4P104VolumeCreator_DynamicSGEnabled_ProposesNewSG(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	baseSGName := "csi--Optimized-SRP_1-SG"
	newSGName := baseSGName + "--1"
	deviceID := "00NEW"
	volumeIdentifier := "csi--new-sg-vol"

	// Mock CreateVolume - 10.4 API will create the new SG automatically
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, req types.CreateVolumesRequest, _ ...http.Header) (*types.CreateVolumesResponse, error) {
			// Verify the new SG name is used
			assert.Equal(t, newSGName, req.Volumes[0].Actions.ManageVolumeStorageGroup.StorageGroup.ID)
			// Verify SRP and ServiceLevel are passed for SG creation
			assert.Equal(t, "SRP_1", req.Volumes[0].Actions.ManageVolumeStorageGroup.StorageGroup.SRP.ID)
			assert.Equal(t, "Optimized", req.Volumes[0].Actions.ManageVolumeStorageGroup.StorageGroup.ServiceLevel.ID)
			return &types.CreateVolumesResponse{
				Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
				Results: types.CreateVolumesResults{
					Result: []types.CreateVolumeResponseItem{
						{
							Status: "success",
							Volume: &types.VolumeRefResponse{
								ID:         deviceID,
								Identifier: volumeIdentifier,
							},
							StorageGroup: &types.StorageGroupRefResponse{ID: newSGName},
						},
					},
				},
			}, nil
		}).Times(1)

	// Use mock service deps with getDynamicSG returning the new SG name
	mockSvc := &mockU4P104ServiceDeps{
		dynamicSGEnabled: true,
		dynamicSGName:    newSGName,
		dynamicSGCreated: true,
	}
	creator := &u4p104VolumeCreator{
		s:             mockSvc,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name: "new-sg-vol",
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1073741824,
		},
		Parameters: map[string]string{
			StoragePoolParam:  "SRP_1",
			ServiceLevelParam: "Optimized",
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, newSGName, resp.Volume.VolumeContext[StorageGroup])
}

func TestU4P104VolumeCreator_DynamicSGEnabled_GetDynamicSGError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	// Use mock service deps with getDynamicSG returning an error
	mockSvc := &mockU4P104ServiceDeps{
		dynamicSGEnabled: true,
		dynamicSGErr:     errors.New("array unreachable"),
	}
	creator := &u4p104VolumeCreator{
		s:             mockSvc,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name: "fail-vol",
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1073741824,
		},
		Parameters: map[string]string{
			StoragePoolParam:  "SRP_1",
			ServiceLevelParam: "Optimized",
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to get dynamic storage group")
}

func TestU4P104VolumeCreator_DynamicSGEnabled_WithHostIOLimits(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	baseSGName := "csi--Optimized-SRP_1-SG"
	deviceID := "00IOL"
	volumeIdentifier := "csi--io-limit-vol"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, req types.CreateVolumesRequest, _ ...http.Header) (*types.CreateVolumesResponse, error) {
			// Verify host IO limits are passed
			hostIOLimitInfo := req.Volumes[0].Actions.ManageVolumeStorageGroup.StorageGroup.HostIOLimitInfo
			assert.NotNil(t, hostIOLimitInfo)
			assert.Equal(t, 1000, hostIOLimitInfo.HostIOLimitMBSec)
			assert.Equal(t, 5000, hostIOLimitInfo.HostIOLimitIOSec)
			assert.Equal(t, "Always", hostIOLimitInfo.DynamicDistribution)
			return &types.CreateVolumesResponse{
				Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
				Results: types.CreateVolumesResults{
					Result: []types.CreateVolumeResponseItem{
						{
							Status: "success",
							Volume: &types.VolumeRefResponse{
								ID:         deviceID,
								Identifier: volumeIdentifier,
							},
							StorageGroup: &types.StorageGroupRefResponse{ID: baseSGName},
						},
					},
				},
			}, nil
		}).Times(1)

	// Use mock service deps with getDynamicSG returning the base SG name
	mockSvc := &mockU4P104ServiceDeps{
		dynamicSGEnabled: true,
		dynamicSGName:    baseSGName,
		dynamicSGCreated: false,
	}
	creator := &u4p104VolumeCreator{
		s:             mockSvc,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name: "io-limit-vol",
		CapacityRange: &csi.CapacityRange{
			RequiredBytes: 1073741824,
		},
		Parameters: map[string]string{
			StoragePoolParam:         "SRP_1",
			ServiceLevelParam:        "Optimized",
			HostIOLimitMBSecParam:    "1000",
			HostIOLimitIOSecParam:    "5000",
			DynamicDistributionParam: "Always",
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestU4P104VolumeCreator_CreateFromSnapshot_ResponseParity(t *testing.T) {
	// Verify VolumeId format and VolumeContext keys/values match legacy conventions.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	symmetrixID := "000197900049"
	srcDevID := "00SRC"
	snapName := "mysnap"
	snapCSIID := csiSnapshotID(snapName, symmetrixID, srcDevID)
	newDevID := "00PAR"
	volIdentifier := "csi--parity-vol"
	sgName := "csi--Optimized-SRP_1-SG"

	creator, mc := newSnapshotCreator(ctrl, symmetrixID, nil)

	mc.EXPECT().GetSnapshotInfo(gomock.Any(), symmetrixID, srcDevID, snapName).
		Return(mockSnapshotInfo(snapName), nil).Times(1)

	mc.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{
						Status: "success",
						Volume: &types.VolumeRefResponse{
							ID:         newDevID,
							Identifier: volIdentifier,
						},
						StorageGroup: &types.StorageGroupRefResponse{ID: sgName},
					},
				},
			},
		}, nil).Times(1)

	req := &csi.CreateVolumeRequest{
		Name:          "parity-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapCSIID},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)

	// VolumeId format: <identifier>-<symID>-<devID>
	expectedVolumeID := volIdentifier + "-" + symmetrixID + "-" + newDevID
	assert.Equal(t, expectedVolumeID, resp.Volume.VolumeId)

	// VolumeContext required keys
	ctx := resp.Volume.VolumeContext
	assert.Equal(t, "Optimized", ctx[ServiceLevelParam])
	assert.Equal(t, "SRP_1", ctx[StoragePoolParam])
	assert.Equal(t, symmetrixID, ctx[SymmetrixIDParam])
	assert.Equal(t, sgName, ctx[StorageGroup])
	assert.Equal(t, snapName, ctx[ContentSource])
	assert.NotEmpty(t, ctx[CapacityGB])
	assert.NotEmpty(t, ctx["CreationTime"])
}

func TestBuildCreateVolumesRequestFromSnapshot(t *testing.T) {
	req := buildCreateVolumeFromSnapshotRequest(547, "mysnap", "SRP_1", "Optimized", "csi-my-SG", "csi-my-vol", nil, "req-snap-1")

	assert.Equal(t, types.ExecutionOptionSynchronous, req.ExecutionOption)
	assert.Len(t, req.Volumes, 1)

	vol := req.Volumes[0]
	assert.Equal(t, "req-snap-1", vol.RequestID)

	// volume.identifier set for idempotency
	assert.NotNil(t, vol.Volume)
	assert.Equal(t, "csi-my-vol", vol.Volume.Identifier)

	// create_new_from_snapshot set with new_volume_attributes; create_new_from_attributes NOT set at top level
	assert.NotNil(t, vol.CreateNew)
	assert.NotNil(t, vol.CreateNew.CreateNewFromSnapshot)
	assert.Equal(t, "mysnap", vol.CreateNew.CreateNewFromSnapshot.Snapshot.ID)
	assert.NotNil(t, vol.CreateNew.CreateNewFromSnapshot.NewVolumeAttributes)
	assert.Equal(t, "CYL", vol.CreateNew.CreateNewFromSnapshot.NewVolumeAttributes.CapacityUnit)
	assert.Equal(t, float64(547), vol.CreateNew.CreateNewFromSnapshot.NewVolumeAttributes.VolumeSize)
	assert.Nil(t, vol.CreateNew.CreateNewFromAttributes)

	// precheck_srp_capacity set
	assert.NotNil(t, vol.CreateNew.PrecheckSrpCapacity)
	assert.Equal(t, "SRP_1", vol.CreateNew.PrecheckSrpCapacity.SRP.ID)

	// manage_volume_storage_group set
	assert.NotNil(t, vol.Actions)
	assert.NotNil(t, vol.Actions.ManageVolumeStorageGroup)
	assert.Equal(t, "Add", vol.Actions.ManageVolumeStorageGroup.Action)
	assert.Equal(t, "csi-my-SG", vol.Actions.ManageVolumeStorageGroup.StorageGroup.ID)

	// manage_identifier NOT set (causes 500 when combined with volume.identifier)
	assert.Nil(t, vol.Actions.ManageIdentifier)

	// response_select at per-volume level
	assert.Equal(t, "id,identifier,cap_cyl,storage_groups", vol.ResponseSelect)
}

// ---------------------------------------------------------------------------
// Gap 1 — AccessibleTopology tests
// ---------------------------------------------------------------------------

func TestU4P104VolumeCreator_AccessibleTopologySet(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	deviceID := "00TOP"
	volumeIdentifier := "csi--topo-vol"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: &types.VolumeRefResponse{ID: deviceID, Identifier: volumeIdentifier}},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "topo-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		AccessibilityRequirements: &csi.TopologyRequirement{
			Preferred: []*csi.Topology{
				{Segments: map[string]string{"topology.kubernetes.io/zone": "zone-a"}},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.Volume.AccessibleTopology)
	assert.Len(t, resp.Volume.AccessibleTopology, 1)
	assert.Equal(t, "zone-a", resp.Volume.AccessibleTopology[0].Segments["topology.kubernetes.io/zone"])
}

func TestU4P104VolumeCreator_AccessibleTopologyNilWhenNoRequirements(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: &types.VolumeRefResponse{ID: "00NTR", Identifier: "csi--no-topo"}},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "no-topo",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		// No AccessibilityRequirements
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Nil(t, resp.Volume.AccessibleTopology)
}

func TestU4P104VolumeCreator_AccessibleTopologyOnIdempotentPath(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	volumeIdentifier := "csi--idem-topo"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: nil},
				},
			},
		}, nil).Times(1)
	mockClient.EXPECT().GetVolumesByIdentifier(gomock.Any(), symmetrixID, volumeIdentifier).
		Return(&types.Volumev1{
			Volumes: []types.VolumeEnhanced{
				{ID: "00IDE", Identifier: volumeIdentifier, CapCyl: 547},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "idem-topo",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		AccessibilityRequirements: &csi.TopologyRequirement{
			Preferred: []*csi.Topology{
				{Segments: map[string]string{"topology.kubernetes.io/zone": "zone-b"}},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.Volume.AccessibleTopology)
	assert.Equal(t, "zone-b", resp.Volume.AccessibleTopology[0].Segments["topology.kubernetes.io/zone"])
}

// ---------------------------------------------------------------------------
// Gap 5 — SLO validation tests
// ---------------------------------------------------------------------------

func TestU4P104VolumeCreator_InvalidSLORejected(t *testing.T) {
	creator := &u4p104VolumeCreator{s: &service{}}
	req := &csi.CreateVolumeRequest{
		Name:          "bad-slo-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Invalid"},
	}
	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "An invalid Service Level parameter was specified")
}

func TestU4P104VolumeCreator_ValidSLOAccepted(t *testing.T) {
	validSLOs := []string{"Diamond", "Platinum", "Gold", "Silver", "Bronze", "Optimized", "None"}
	for _, slo := range validSLOs {
		t.Run(slo, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockClient := mocks.NewMockPmaxClient(ctrl)
			symmetrixID := "000197900049"

			mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
				Return(&types.CreateVolumesResponse{
					Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
					Results: types.CreateVolumesResults{
						Result: []types.CreateVolumeResponseItem{
							{Status: "success", Volume: &types.VolumeRefResponse{ID: "00SLO", Identifier: "csi--slo-vol"}},
						},
					},
				}, nil).Times(1)

			creator := &u4p104VolumeCreator{
				s:             &service{},
				pmaxClient:    mockClient,
				pmaxClient104: mockClient,
				symmetrixID:   symmetrixID,
			}

			req := &csi.CreateVolumeRequest{
				Name:          "slo-vol",
				CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
				Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: slo},
			}

			resp, err := creator.Create(context.Background(), req)
			assert.NoError(t, err)
			assert.NotNil(t, resp)
		})
	}
}

func TestIsValidSLO(t *testing.T) {
	assert.True(t, isValidSLO("Diamond"))
	assert.True(t, isValidSLO("Optimized"))
	assert.True(t, isValidSLO("None"))
	assert.False(t, isValidSLO("Invalid"))
	assert.False(t, isValidSLO("diamond"))
	assert.False(t, isValidSLO(""))
}

// ---------------------------------------------------------------------------
// Gap 6 — Zone labels tests
// ---------------------------------------------------------------------------

func TestU4P104VolumeCreator_ZoneLabelsAddedWhenInAZ(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: &types.VolumeRefResponse{ID: "00ZL1", Identifier: "csi--zone-vol"}},
				},
			},
		}, nil).Times(1)

	svc := &service{}
	svc.opts.StorageArrays = map[string]StorageArrayConfig{
		symmetrixID: {Labels: map[string]interface{}{"topology.kubernetes.io/zone": "us-east-1a"}},
	}

	creator := &u4p104VolumeCreator{
		s:               svc,
		pmaxClient:      mockClient,
		pmaxClient104:   mockClient,
		symmetrixID:     symmetrixID,
		symmIDFoundInAZ: true,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "zone-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "us-east-1a", resp.Volume.VolumeContext["topology.kubernetes.io/zone"])
}

func TestU4P104VolumeCreator_NoZoneLabelsWhenNotInAZ(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: &types.VolumeRefResponse{ID: "00NZL", Identifier: "csi--no-zone"}},
				},
			},
		}, nil).Times(1)

	creator := &u4p104VolumeCreator{
		s:               &service{},
		pmaxClient:      mockClient,
		pmaxClient104:   mockClient,
		symmetrixID:     symmetrixID,
		symmIDFoundInAZ: false,
	}

	req := &csi.CreateVolumeRequest{
		Name:          "no-zone",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	_, hasZoneLabel := resp.Volume.VolumeContext["topology.kubernetes.io/zone"]
	assert.False(t, hasZoneLabel)
}

// ---------------------------------------------------------------------------
// Gap 7 — Block capability validation tests
// ---------------------------------------------------------------------------

func TestU4P104VolumeCreator_BlockCapabilityRejectedWhenDisabled(t *testing.T) {
	svc := &service{}
	svc.opts.EnableBlock = false

	creator := &u4p104VolumeCreator{s: svc}

	block := new(csi.VolumeCapability_BlockVolume)
	accessType := new(csi.VolumeCapability_Block)
	accessType.Block = block
	capability := &csi.VolumeCapability{
		AccessType: accessType,
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}

	req := &csi.CreateVolumeRequest{
		Name:               "block-vol",
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:         map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeCapabilities: []*csi.VolumeCapability{capability},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Block Volume Capability is not supported")
}

func TestU4P104VolumeCreator_BlockCapabilityAcceptedWhenEnabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: &types.VolumeRefResponse{ID: "00BLK", Identifier: "csi--block-vol"}},
				},
			},
		}, nil).Times(1)

	svc := &service{}
	svc.opts.EnableBlock = true

	creator := &u4p104VolumeCreator{
		s:             svc,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	block := new(csi.VolumeCapability_BlockVolume)
	accessType := new(csi.VolumeCapability_Block)
	accessType.Block = block
	capability := &csi.VolumeCapability{
		AccessType: accessType,
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}

	req := &csi.CreateVolumeRequest{
		Name:               "block-vol",
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:         map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeCapabilities: []*csi.VolumeCapability{capability},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestU4P104VolumeCreator_MountCapabilityAllowedWhenBlockDisabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(&types.CreateVolumesResponse{
			Summary: types.ResponseSummary{Total: 1, Succeeded: 1},
			Results: types.CreateVolumesResults{
				Result: []types.CreateVolumeResponseItem{
					{Status: "success", Volume: &types.VolumeRefResponse{ID: "00MNT", Identifier: "csi--mount-vol"}},
				},
			},
		}, nil).Times(1)

	svc := &service{}
	svc.opts.EnableBlock = false // block disabled, but mount should still work

	creator := &u4p104VolumeCreator{
		s:             svc,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
	}

	capability := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"}},
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}

	req := &csi.CreateVolumeRequest{
		Name:               "mount-vol",
		CapacityRange:      &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:         map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeCapabilities: []*csi.VolumeCapability{capability},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

// ---------------------------------------------------------------------------
// classifyCreateVolumeError tests
// ---------------------------------------------------------------------------

func TestClassifyCreateVolumeError_Nil(t *testing.T) {
	assert.Nil(t, classifyCreateVolumeError(nil))
}

func TestClassifyCreateVolumeError_SizeMismatch(t *testing.T) {
	err := fmt.Errorf("create volumes failed: 0x020e0105: defined volume size [2185]Cyls does not match existing volume size [4369]Cyls")
	result := classifyCreateVolumeError(err)
	assert.Error(t, result)
	assert.Contains(t, result.Error(), "A volume with the same name exists but has a different size")
	assert.Contains(t, result.Error(), "AlreadyExists")
}

func TestClassifyCreateVolumeError_TargetSmallerThanSource(t *testing.T) {
	err := fmt.Errorf("create volumes failed: 0x020e0105: Target volume size [2185]Cyls cannot be smaller than source volume size [4369]Cyls")
	result := classifyCreateVolumeError(err)
	assert.Error(t, result)
	assert.Contains(t, result.Error(), "Requested capacity is smaller than the source")
	assert.Contains(t, result.Error(), "InvalidArgument")
}

func TestClassifyCreateVolumeError_SourceVolumeNotFound(t *testing.T) {
	err := fmt.Errorf("create volumes failed: 0x020e0114: Source Volumewith identifier [csi-test-vol-1111] does not exist")
	result := classifyCreateVolumeError(err)
	assert.Error(t, result)
	assert.Contains(t, result.Error(), "Volume content source couldn't be found in the array")
	assert.Contains(t, result.Error(), "InvalidArgument")
}

func TestClassifyCreateVolumeError_SnapshotNotFound(t *testing.T) {
	err := fmt.Errorf("create volumes failed: 0x020e0117: No Snapshot found with id [101850091802]")
	result := classifyCreateVolumeError(err)
	assert.Error(t, result)
	assert.Contains(t, result.Error(), "Snapshot not found on the array")
	assert.Contains(t, result.Error(), "InvalidArgument")
}

func TestClassifyCreateVolumeError_GenericError(t *testing.T) {
	err := fmt.Errorf("some unknown API error")
	result := classifyCreateVolumeError(err)
	assert.Error(t, result)
	assert.Contains(t, result.Error(), "Failed to create volume")
	assert.Contains(t, result.Error(), "Internal")
}

// ---------------------------------------------------------------------------
// 10.4 CreateVolume error scenario integration tests
// ---------------------------------------------------------------------------

func TestU4P104VolumeCreator_IdempotentSizeMismatch_AlreadyExistsCode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	// Simulate API returning size mismatch error for idempotent volume
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(nil, fmt.Errorf("create volumes failed: 0x020e0105: defined volume size [547]Cyls does not match existing volume size [1093]Cyls")).
		Times(1)

	creator := &u4p104VolumeCreator{
		s:             &service{},
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	req := &csi.CreateVolumeRequest{
		Name:          "size-mismatch-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "A volume with the same name exists but has a different size")
	assert.Contains(t, err.Error(), "AlreadyExists")
}

func TestU4P104VolumeCreator_CloneSourceNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	// Simulate API returning source volume not found
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(nil, fmt.Errorf("create volumes failed: 0x020e0114: Source Volumewith identifier [csi--clone-vol] does not exist")).
		Times(1)

	deps := &snapshotDeps{service: &service{}, licenseErr: nil}
	creator := &u4p104VolumeCreator{
		s:             deps,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	req := &csi.CreateVolumeRequest{
		Name:          "clone-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: "csi--src-vol-" + symmetrixID + "-00SRC",
				},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Volume content source couldn't be found in the array")
}

func TestU4P104VolumeCreator_CloneSmallerCapacity(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"

	// Simulate API returning target smaller than source error
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(nil, fmt.Errorf("create volumes failed: 0x020e0105: Target volume size [547]Cyls cannot be smaller than source volume size [1093]Cyls")).
		Times(1)

	deps := &snapshotDeps{service: &service{}, licenseErr: nil}
	creator := &u4p104VolumeCreator{
		s:             deps,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	req := &csi.CreateVolumeRequest{
		Name:          "clone-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Volume{
				Volume: &csi.VolumeContentSource_VolumeSource{
					VolumeId: "csi--src-vol-" + symmetrixID + "-00SRC",
				},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Requested capacity is smaller than the source")
}

func TestU4P104VolumeCreator_SnapshotRestoreSmallerCapacity(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	symmetrixID := "000197900049"
	srcDevID := "00SRC"
	snapName := "mysnap"

	// GetSnapshotInfo succeeds
	mockClient.EXPECT().GetSnapshotInfo(gomock.Any(), symmetrixID, srcDevID, snapName).
		Return(mockSnapshotInfo(snapName), nil).Times(1)

	// Simulate API returning target smaller than source error
	mockClient.EXPECT().CreateVolume(gomock.Any(), symmetrixID, gomock.Any(), gomock.Any()).
		Return(nil, fmt.Errorf("create volumes failed: 0x020e0105: Target volume size [547]Cyls cannot be smaller than source volume size [1093]Cyls")).
		Times(1)

	deps := &snapshotDeps{service: &service{}, licenseErr: nil}
	creator := &u4p104VolumeCreator{
		s:             deps,
		pmaxClient:    mockClient,
		pmaxClient104: mockClient,
		symmetrixID:   symmetrixID,
		params:        map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
	}

	snapCSIID := csiSnapshotID(snapName, symmetrixID, srcDevID)
	req := &csi.CreateVolumeRequest{
		Name:          "snap-vol",
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1073741824},
		Parameters:    map[string]string{StoragePoolParam: "SRP_1", ServiceLevelParam: "Optimized"},
		VolumeContentSource: &csi.VolumeContentSource{
			Type: &csi.VolumeContentSource_Snapshot{
				Snapshot: &csi.VolumeContentSource_SnapshotSource{SnapshotId: snapCSIID},
			},
		},
	}

	resp, err := creator.Create(context.Background(), req)
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Requested capacity is smaller than the source")
}
