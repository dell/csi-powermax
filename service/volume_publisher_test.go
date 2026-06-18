package service

import (
	"context"
	"errors"
	"testing"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestU4P104VolumePublisher_MissingNodeID(t *testing.T) {
	p := &u4p104VolumePublisher{s: &service{}}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "node ID is required")
}

func TestU4P104VolumePublisher_MissingVolumeCapability(t *testing.T) {
	p := &u4p104VolumePublisher{s: &service{}}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "volume capability is required")
}

func TestU4P104VolumePublisher_MissingAccessMode(t *testing.T) {
	p := &u4p104VolumePublisher{s: &service{}}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
		},
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "access mode is required")
}

func TestU4P104VolumePublisher_UnknownAccessMode(t *testing.T) {
	p := &u4p104VolumePublisher{s: &service{}}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
			},
		},
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), errUnknownAccessMode)
}

func TestU4P104VolumePublisher_GetVolumeByIDError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockClient := mocks.NewMockPmaxClient(ctrl)
	// fsType is ext4 so no GetFileSystemByID call
	// GetVolumeByID fails
	mockClient.EXPECT().GetVolumeByID(gomock.Any(), "000120000001", "011AB").
		Return(nil, errors.New("Could not find device 011AB")).Times(1)
	p := &u4p104VolumePublisher{
		s:            &service{},
		legacyClient: mockClient,
		symID:        "000120000001",
		devID:        "011AB",
		volID:        "csi-ABC-pmax-testvol-000120000001-011AB",
		volumeName:   "csi-ABC-pmax-testvol",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Could not find")
}

func TestU4P104VolumePublisher_IsNodeNVMeError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockClient := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	// Clear stale node cache entries from prior tests
	nodeCache.Delete("000120000001:worker-1")

	mockClient.EXPECT().GetVolumeByID(gomock.Any(), "000120000001", "011AB").
		Return(&types.Volume{
			VolumeID:         "011AB",
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     "60000970000120000001533030314142",
		}, nil).Times(1)

	// IsNodeNVMe calls - NVMe MV and host lookups both fail
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), "000120000001", gomock.Any()).
		Return(nil, errors.New("not found")).AnyTimes()
	mockClient.EXPECT().GetHostByID(gomock.Any(), "000120000001", gomock.Any()).
		Return(nil, errors.New("not found")).AnyTimes()

	// Set NVMe transport so IsNodeNVMe actually returns an error when host is not found
	svc := &service{
		opts: Opts{
			TransportProtocol: NvmeTCPTransportProtocol,
		},
	}
	p := &u4p104VolumePublisher{
		s:            svc,
		legacyClient: mockClient,
		symID:        "000120000001",
		devID:        "011AB",
		volID:        volID,
		volumeName:   "csi-ABC-pmax-testvol",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to fetch host id from array")
}

func TestU4P104VolumePublisher_NoEffectiveWWN(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockClient := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	// Clear stale node cache entries from prior tests
	nodeCache.Delete("000120000001:worker-1")

	mockClient.EXPECT().GetVolumeByID(gomock.Any(), "000120000001", "011AB").
		Return(&types.Volume{
			VolumeID:         "011AB",
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     "", // empty WWN
		}, nil).Times(1)

	// IsNodeNVMe → false (default transport protocol is not NVMe)
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), "000120000001", gomock.Any()).
		Return(nil, errors.New("not found")).AnyTimes()
	// IsNodeISCSI → iSCSI host found
	mockClient.EXPECT().GetHostByID(gomock.Any(), "000120000001", gomock.Any()).
		Return(&types.Host{HostID: "csi-node--worker-1", HostType: "iSCSI"}, nil).AnyTimes()

	svc := &service{
		opts: Opts{
			PortGroups: []string{"csi-pg-1"},
		},
	}
	p := &u4p104VolumePublisher{
		s:            svc,
		legacyClient: mockClient,
		symID:        "000120000001",
		devID:        "011AB",
		volID:        volID,
		volumeName:   "csi-ABC-pmax-testvol",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has no effective WWN")
}

func TestU4P104VolumePublisher_GetHostByIDError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockClient := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	symID := "000120000001"
	// Clear stale node cache entries from prior tests
	nodeCache.Delete(symID + ":worker-1")

	mockClient.EXPECT().GetVolumeByID(gomock.Any(), symID, "011AB").
		Return(&types.Volume{
			VolumeID:         "011AB",
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     "60000970000120000001533030314142",
		}, nil).Times(1)

	// IsNodeNVMe → false (default transport)
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), symID, gomock.Any()).
		Return(nil, errors.New("not found")).AnyTimes()
	// IsNodeISCSI → iSCSI host found (default transport checks FC host, then iSCSI host)
	// All GetHostByID calls during IsNodeISCSI succeed with iSCSI host
	// but the final GetHostByID for the derived hostID (for PG selection) fails
	hostCall := mockClient.EXPECT().GetHostByID(gomock.Any(), symID, gomock.Any()).
		Return(&types.Host{HostID: "csi-node--worker-1", HostType: "iSCSI"}, nil).AnyTimes()
	// Override: the specific call for derived hostID during host lookup fails
	// We use a counter to simulate: first N calls succeed (IsNodeISCSI), last call fails
	callCount := 0
	hostCall.DoAndReturn(func(_ context.Context, _ string, hostID string) (*types.Host, error) {
		callCount++
		// The last call is the explicit GetHostByID for PG selection
		// IsNodeISCSI with default FC transport checks: fcHost, iscsiHost = 2 calls
		// Then the Publish method makes 1 more call for PG selection
		if callCount > 2 {
			return nil, errors.New("host not found on array")
		}
		return &types.Host{HostID: hostID, HostType: "iSCSI"}, nil
	})

	svc := &service{
		opts: Opts{
			PortGroups: []string{"csi-pg-1"},
		},
	}
	p := &u4p104VolumePublisher{
		s:            svc,
		legacyClient: mockClient,
		symID:        symID,
		devID:        "011AB",
		volID:        volID,
		volumeName:   "csi-ABC-pmax-testvol",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to fetch host details")
}

func TestU4P104VolumePublisher_SelectOrCreatePortGroupError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockClient := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	symID := "000120000001"
	// Clear stale node cache entries from prior tests
	nodeCache.Delete("000120000001:worker-1")

	mockClient.EXPECT().GetVolumeByID(gomock.Any(), symID, "011AB").
		Return(&types.Volume{
			VolumeID:         "011AB",
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     "60000970000120000001533030314142",
		}, nil).Times(1)

	// IsNodeNVMe → false
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), symID, gomock.Any()).
		Return(nil, errors.New("not found")).AnyTimes()
	// IsNodeISCSI → iSCSI host found
	mockClient.EXPECT().GetHostByID(gomock.Any(), symID, gomock.Any()).
		Return(&types.Host{HostID: "csi-node--worker-1", HostType: "iSCSI"}, nil).AnyTimes()

	// No port groups configured → SelectPortGroup will fail
	svc := &service{
		opts: Opts{
			PortGroups: []string{},
		},
	}
	p := &u4p104VolumePublisher{
		s:            svc,
		legacyClient: mockClient,
		symID:        symID,
		devID:        "011AB",
		volID:        volID,
		volumeName:   "csi-ABC-pmax-testvol",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to select/create port group")
}

func TestU4P104VolumePublisher_PublishMaskingViewsAPIError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	// Clear stale node cache entries from prior tests
	nodeCache.Delete("000120000001:worker-1")

	mockClient.EXPECT().GetVolumeByID(gomock.Any(), "000120000001", "011AB").
		Return(&types.Volume{
			VolumeID:         "011AB",
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     "60000970000120000001533030314142",
		}, nil).Times(1)

	// IsNodeNVMe → false, no error
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), "000120000001", gomock.Any()).
		Return(nil, errors.New("not found")).AnyTimes()
	// IsNodeISCSI → iSCSI host found
	mockClient.EXPECT().GetHostByID(gomock.Any(), "000120000001", gomock.Any()).
		Return(&types.Host{HostID: "csi-node--worker-1", HostType: "iSCSI"}, nil).AnyTimes()

	// SelectOrCreatePortGroup for iSCSI uses SelectPortGroup
	svc := &service{
		opts: Opts{
			PortGroups: []string{"csi-pg-1"},
		},
	}

	// PublishMaskingViews API call fails
	mockClient104.EXPECT().PublishMaskingViews(gomock.Any(), "000120000001", gomock.Any()).
		Return(nil, errors.New("array connection timeout")).Times(1)

	p := &u4p104VolumePublisher{
		s:            svc,
		legacyClient: mockClient,
		client104:    mockClient104,
		symID:        "000120000001",
		devID:        "011AB",
		volID:        volID,
		volumeName:   "csi-ABC-pmax-testvol",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "PublishMaskingViews failed")
}

func TestU4P104VolumePublisher_PublishMaskingViewsPartialFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	// Clear stale node cache entries from prior tests
	nodeCache.Delete("000120000001:worker-1")

	mockClient.EXPECT().GetVolumeByID(gomock.Any(), "000120000001", "011AB").
		Return(&types.Volume{
			VolumeID:         "011AB",
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     "60000970000120000001533030314142",
		}, nil).Times(1)

	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), "000120000001", gomock.Any()).
		Return(nil, errors.New("not found")).AnyTimes()
	mockClient.EXPECT().GetHostByID(gomock.Any(), "000120000001", gomock.Any()).
		Return(&types.Host{HostID: "csi-node--worker-1", HostType: "iSCSI"}, nil).AnyTimes()

	svc := &service{
		opts: Opts{
			PortGroups: []string{"csi-pg-1"},
		},
	}

	// PublishMaskingViews returns partial failure
	mockClient104.EXPECT().PublishMaskingViews(gomock.Any(), "000120000001", gomock.Any()).
		Return(&types.PublishMaskingViewResponse{
			Summary: types.PublishSummary{Total: 1, Failed: 1},
			Results: types.PublishResultsBlock{
				Result: []types.PublishResult{
					{Status: "failed", ResourceID: "csi-mv--worker-1"},
				},
			},
		}, nil).Times(1)

	p := &u4p104VolumePublisher{
		s:            svc,
		legacyClient: mockClient,
		client104:    mockClient104,
		symID:        "000120000001",
		devID:        "011AB",
		volID:        volID,
		volumeName:   "csi-ABC-pmax-testvol",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "PublishMaskingViews failed for masking view")
}

func TestU4P104VolumePublisher_PublishSuccess(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Start the lock manager required by updatePublishContext
	LockRequestHandler()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	devID := "011AB"
	symID := "000120000001"
	effectiveWWN := "60000970000120000001533030314142"
	tgtMaskingViewID := "csi-mv--worker-1"
	portIdentifier := "iqn.1994-05.com.redhat:target1"
	// Clear stale node cache entries from prior tests
	nodeCache.Delete("000120000001:worker-1")

	mockClient.EXPECT().GetVolumeByID(gomock.Any(), symID, devID).
		Return(&types.Volume{
			VolumeID:         devID,
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     effectiveWWN,
		}, nil).Times(1)

	// IsNodeNVMe → false (default transport protocol)
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), symID, gomock.Any()).
		Return(nil, errors.New("not found")).AnyTimes()
	// IsNodeISCSI → iSCSI host
	mockClient.EXPECT().GetHostByID(gomock.Any(), symID, gomock.Any()).
		Return(&types.Host{HostID: "csi-node--worker-1", HostType: "iSCSI"}, nil).AnyTimes()

	svc := &service{
		opts: Opts{
			PortGroups: []string{"csi-pg-1"},
		},
	}
	getPmaxCache(symID)

	// PublishMaskingViews succeeds
	mockClient104.EXPECT().PublishMaskingViews(gomock.Any(), symID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, param *types.PublishMaskingViewsParam) (*types.PublishMaskingViewResponse, error) {
			// Verify the request structure
			assert.Len(t, param.MaskingViews, 1)
			mv := param.MaskingViews[0]
			assert.Equal(t, tgtMaskingViewID, mv.ID)
			assert.NotNil(t, mv.StorageGroup)
			assert.NotNil(t, mv.Host)
			assert.NotNil(t, mv.PortGroup)
			assert.Equal(t, "csi-pg-1", mv.PortGroup.ID)
			// Verify volume is in the request
			assert.NotNil(t, mv.StorageGroup.Actions)
			assert.NotNil(t, mv.StorageGroup.Actions.AddVolumesToStorageGroupAction)
			assert.Len(t, mv.StorageGroup.Actions.AddVolumesToStorageGroupAction.Volumes, 1)
			assert.Len(t, mv.StorageGroup.Actions.AddVolumesToStorageGroupAction.Volumes[0].ExistingVolumes, 1)
			assert.Equal(t, devID, mv.StorageGroup.Actions.AddVolumesToStorageGroupAction.Volumes[0].ExistingVolumes[0].ID)
			return &types.PublishMaskingViewResponse{
				Summary: types.PublishSummary{Total: 1, Succeeded: 1},
			}, nil
		}).Times(1)

	// GetMaskingViewConnections for updatePublishContext
	mockClient.EXPECT().GetMaskingViewConnections(gomock.Any(), symID, tgtMaskingViewID, devID).
		Return([]*types.MaskingViewConnection{
			{VolumeID: devID, HostLUNAddress: "0001", DirectorPort: "SE-1E:4"},
			{VolumeID: devID, HostLUNAddress: "0001", DirectorPort: "SE-2E:4"},
		}, nil).Times(1)

	// GetPort for port identifiers
	mockClient.EXPECT().GetPort(gomock.Any(), symID, "SE-1E", "4").
		Return(&types.Port{
			SymmetrixPort: types.SymmetrixPortType{
				Identifier: portIdentifier,
			},
		}, nil).Times(1)
	mockClient.EXPECT().GetPort(gomock.Any(), symID, "SE-2E", "4").
		Return(&types.Port{
			SymmetrixPort: types.SymmetrixPortType{
				Identifier: portIdentifier,
			},
		}, nil).Times(1)

	p := &u4p104VolumePublisher{
		s:            svc,
		legacyClient: mockClient,
		client104:    mockClient104,
		symID:        symID,
		devID:        devID,
		volID:        volID,
		volumeName:   "csi-ABC-pmax-testvol",
		reqID:        "req-1",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, effectiveWWN, resp.PublishContext[PublishContextDeviceWWN])
	assert.Equal(t, "0001", resp.PublishContext[PublishContextLUNAddress])
	assert.NotEmpty(t, resp.PublishContext[PortIdentifiers+"_1"])
}

func TestU4P104VolumePublisher_PublishSuccessWithConnectionFetchFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	// Start the lock manager required by updatePublishContext
	LockRequestHandler()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	devID := "011AB"
	symID := "000120000001"
	effectiveWWN := "60000970000120000001533030314142"
	tgtMaskingViewID := "csi-mv--worker-1"
	portIdentifier := "iqn.1994-05.com.redhat:target1"
	// Clear stale node cache entries from prior tests
	nodeCache.Delete("000120000001:worker-1")

	mockClient.EXPECT().GetVolumeByID(gomock.Any(), symID, devID).
		Return(&types.Volume{
			VolumeID:         devID,
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     effectiveWWN,
		}, nil).Times(1)

	// IsNodeNVMe → false
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), symID, gomock.Any()).
		Return(nil, errors.New("not found")).AnyTimes()
	// IsNodeISCSI → iSCSI host
	mockClient.EXPECT().GetHostByID(gomock.Any(), symID, gomock.Any()).
		Return(&types.Host{HostID: "csi-node--worker-1", HostType: "iSCSI"}, nil).AnyTimes()

	svc := &service{
		opts: Opts{
			PortGroups: []string{"csi-pg-1"},
		},
	}
	getPmaxCache(symID)

	// PublishMaskingViews succeeds
	mockClient104.EXPECT().PublishMaskingViews(gomock.Any(), symID, gomock.Any()).
		Return(&types.PublishMaskingViewResponse{
			Summary: types.PublishSummary{Total: 1, Succeeded: 1},
		}, nil).Times(1)

	// First GetMaskingViewConnections (in Publish) fails
	// Second GetMaskingViewConnections (retry in updatePublishContext) succeeds
	gomock.InOrder(
		mockClient.EXPECT().GetMaskingViewConnections(gomock.Any(), symID, tgtMaskingViewID, devID).
			Return(nil, errors.New("connection timeout")).Times(1),
		mockClient.EXPECT().GetMaskingViewConnections(gomock.Any(), symID, tgtMaskingViewID, devID).
			Return([]*types.MaskingViewConnection{
				{VolumeID: devID, HostLUNAddress: "0002", DirectorPort: "SE-1E:4"},
				{VolumeID: devID, HostLUNAddress: "0002", DirectorPort: "SE-2E:4"},
			}, nil).Times(1),
	)

	// GetPort for port identifiers
	mockClient.EXPECT().GetPort(gomock.Any(), symID, "SE-1E", "4").
		Return(&types.Port{
			SymmetrixPort: types.SymmetrixPortType{
				Identifier: portIdentifier,
			},
		}, nil).Times(1)
	mockClient.EXPECT().GetPort(gomock.Any(), symID, "SE-2E", "4").
		Return(&types.Port{
			SymmetrixPort: types.SymmetrixPortType{
				Identifier: portIdentifier,
			},
		}, nil).Times(1)

	p := &u4p104VolumePublisher{
		s:            svc,
		legacyClient: mockClient,
		client104:    mockClient104,
		symID:        symID,
		devID:        devID,
		volID:        volID,
		volumeName:   "csi-ABC-pmax-testvol",
		reqID:        "req-2",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, effectiveWWN, resp.PublishContext[PublishContextDeviceWWN])
	assert.Equal(t, "0002", resp.PublishContext[PublishContextLUNAddress])
	assert.NotEmpty(t, resp.PublishContext[PortIdentifiers+"_1"])
}

// TestU4P104VolumePublisher_ExistingMV_UsesExistingPortGroup is the regression
// test for CSME-250: when a MaskingView already exists the driver must reuse the
// existing PortGroupID from the MV instead of calling SelectOrCreatePortGroup.
// In the failing scenario two port groups had identical ports but different IDs;
// PowerMax rejected the request because it interpreted the different ID as an
// attempt to modify the MV's port group, which is not allowed.
func TestU4P104VolumePublisher_ExistingMV_UsesExistingPortGroup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	LockRequestHandler()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	devID := "011AB"
	symID := "000120000001"
	effectiveWWN := "60000970000120000001533030314142"
	tgtMaskingViewID := "csi-mv--worker-1"
	existingPGID := "csi-CSM-OR-1C-3-OR-1C-7-OR-2C-6-OR-2C-2-PG"
	portIdentifier := "iqn.1994-05.com.redhat:target1"

	nodeCache.Delete(symID + ":worker-1")

	mockClient.EXPECT().GetVolumeByID(gomock.Any(), symID, devID).
		Return(&types.Volume{
			VolumeID:         devID,
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     effectiveWWN,
		}, nil).Times(1)

	// GetMaskingViewByID is called in two contexts:
	//   1. IsNodeNVMe detection: looks up NVMe-format MV (not equal to tgtMaskingViewID) → not found
	//   2. Port-group branch: looks up tgtMaskingViewID → found with existingPGID
	// Use a single DoAndReturn dispatcher to avoid gomock matcher ordering conflicts.
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), symID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, mvID string) (*types.MaskingView, error) {
			if mvID == tgtMaskingViewID {
				return &types.MaskingView{
					MaskingViewID:  tgtMaskingViewID,
					PortGroupID:    existingPGID,
					HostID:         "csi-node--worker-1",
					StorageGroupID: "csi-no-srp-sg--worker-1",
				}, nil
			}
			return nil, errors.New("not found")
		}).AnyTimes()

	// IsNodeISCSI resolution — GetHostByID called only for transport detection
	mockClient.EXPECT().GetHostByID(gomock.Any(), symID, gomock.Any()).
		Return(&types.Host{HostID: "csi-node--worker-1", HostType: "iSCSI"}, nil).AnyTimes()

	svc := &service{
		opts: Opts{
			// PortGroups configured with a *different* PG that has the same ports.
			// If the bug were present the driver would pick this instead of existingPGID
			// and the request would fail with a 500 from the array.
			PortGroups: []string{"PG_GBE01103_ESX_CLU"},
		},
	}
	getPmaxCache(symID)

	// PublishMaskingViews must receive the existing PG ID, not PG_GBE01103_ESX_CLU
	mockClient104.EXPECT().PublishMaskingViews(gomock.Any(), symID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, param *types.PublishMaskingViewsParam) (*types.PublishMaskingViewResponse, error) {
			assert.Len(t, param.MaskingViews, 1)
			mv := param.MaskingViews[0]
			assert.NotNil(t, mv.PortGroup)
			assert.Equal(t, existingPGID, mv.PortGroup.ID,
				"must reuse existing MV PortGroup, not select a new one")
			return &types.PublishMaskingViewResponse{
				Summary: types.PublishSummary{Total: 1, Succeeded: 1},
			}, nil
		}).Times(1)

	mockClient.EXPECT().GetMaskingViewConnections(gomock.Any(), symID, tgtMaskingViewID, devID).
		Return([]*types.MaskingViewConnection{
			{VolumeID: devID, HostLUNAddress: "0003", DirectorPort: "SE-1E:4"},
			{VolumeID: devID, HostLUNAddress: "0003", DirectorPort: "SE-2E:4"},
		}, nil).Times(1)

	mockClient.EXPECT().GetPort(gomock.Any(), symID, "SE-1E", "4").
		Return(&types.Port{
			SymmetrixPort: types.SymmetrixPortType{Identifier: portIdentifier},
		}, nil).Times(1)
	mockClient.EXPECT().GetPort(gomock.Any(), symID, "SE-2E", "4").
		Return(&types.Port{
			SymmetrixPort: types.SymmetrixPortType{Identifier: portIdentifier},
		}, nil).Times(1)

	p := &u4p104VolumePublisher{
		s:            svc,
		legacyClient: mockClient,
		client104:    mockClient104,
		symID:        symID,
		devID:        devID,
		volID:        volID,
		volumeName:   "csi-ABC-pmax-testvol",
		reqID:        "req-csme250",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, effectiveWWN, resp.PublishContext[PublishContextDeviceWWN])
}

// TestU4P104VolumePublisher_ExistingMV_SkipsHostByIDForPortGroup verifies that
// when a MaskingView already exists, GetHostByID is not called for port group
// selection (only called by the IsNodeNVMe/IsNodeISCSI transport detection path).
// If it were called we would be doing an unnecessary API round-trip.
func TestU4P104VolumePublisher_ExistingMV_SkipsHostByIDForPortGroup(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	LockRequestHandler()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	devID := "011AB"
	symID := "000120000001"
	effectiveWWN := "60000970000120000001533030314142"
	tgtMaskingViewID := "csi-mv--worker-1"
	existingPGID := "existing-fc-pg"

	nodeCache.Delete(symID + ":worker-1")

	// volID must match VolumeIdentifier for GetVolumeByID service-layer validation
	mockClient.EXPECT().GetVolumeByID(gomock.Any(), symID, devID).
		Return(&types.Volume{
			VolumeID:         devID,
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     effectiveWWN,
		}, nil).Times(1)

	// GetMaskingViewByID: NVMe detection → not found; tgtMV → exists
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), symID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, mvID string) (*types.MaskingView, error) {
			if mvID == tgtMaskingViewID {
				return &types.MaskingView{
					MaskingViewID:  tgtMaskingViewID,
					PortGroupID:    existingPGID,
					HostID:         "csi-node--worker-1",
					StorageGroupID: "csi-no-srp-sg--worker-1",
				}, nil
			}
			return nil, errors.New("not found")
		}).AnyTimes()

	// GetHostByID may be called for IsNodeISCSI transport detection only.
	// It must NOT be called a second time for port group selection.
	// We use MaxTimes(2) to capture the two transport-detection calls and assert
	// no additional call is made.
	mockClient.EXPECT().GetHostByID(gomock.Any(), symID, gomock.Any()).
		Return(&types.Host{HostID: "csi-node--worker-1", HostType: "Fibre"}, nil).MaxTimes(2)

	svc := &service{opts: Opts{PortGroups: []string{"should-not-be-used-pg"}}}
	getPmaxCache(symID)

	mockClient104.EXPECT().PublishMaskingViews(gomock.Any(), symID, gomock.Any()).
		Return(&types.PublishMaskingViewResponse{
			Summary: types.PublishSummary{Total: 1, Succeeded: 1},
		}, nil).Times(1)

	mockClient.EXPECT().GetMaskingViewConnections(gomock.Any(), symID, tgtMaskingViewID, devID).
		Return([]*types.MaskingViewConnection{
			{VolumeID: devID, HostLUNAddress: "0004", DirectorPort: "FA-1D:4"},
			{VolumeID: devID, HostLUNAddress: "0004", DirectorPort: "FA-2D:4"},
		}, nil).Times(1)

	mockClient.EXPECT().GetPort(gomock.Any(), symID, "FA-1D", "4").
		Return(&types.Port{
			SymmetrixPort: types.SymmetrixPortType{Identifier: "50:00:09:72:00:00:00:01"},
		}, nil).Times(1)
	mockClient.EXPECT().GetPort(gomock.Any(), symID, "FA-2D", "4").
		Return(&types.Port{
			SymmetrixPort: types.SymmetrixPortType{Identifier: "50:00:09:72:00:00:00:02"},
		}, nil).Times(1)

	p := &u4p104VolumePublisher{
		s:            svc,
		legacyClient: mockClient,
		client104:    mockClient104,
		symID:        symID,
		devID:        devID,
		volID:        volID,
		reqID:        "req-skip-hostbyid",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

// TestU4P104VolumePublisher_ExistingMV_PublishMaskingViewsFails covers the
// error path where the MaskingView already exists (so we correctly reuse its
// PortGroupID) but PublishMaskingViews still returns an error (e.g. network
// failure, array busy).
func TestU4P104VolumePublisher_ExistingMV_PublishMaskingViewsFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	devID := "011AB"
	symID := "000120000001"
	tgtMaskingViewID := "csi-mv--worker-1"
	existingPGID := "csi-CSM-OR-1C-3-PG"

	nodeCache.Delete(symID + ":worker-1")

	mockClient.EXPECT().GetVolumeByID(gomock.Any(), symID, devID).
		Return(&types.Volume{
			VolumeID:         devID,
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     "60000970000120000001533030314142",
		}, nil).Times(1)

	// GetMaskingViewByID: NVMe detection → not found; tgtMV → exists
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), symID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, mvID string) (*types.MaskingView, error) {
			if mvID == tgtMaskingViewID {
				return &types.MaskingView{
					MaskingViewID:  tgtMaskingViewID,
					PortGroupID:    existingPGID,
					HostID:         "csi-node--worker-1",
					StorageGroupID: "csi-no-srp-sg--worker-1",
				}, nil
			}
			return nil, errors.New("not found")
		}).AnyTimes()

	// Transport detection calls
	mockClient.EXPECT().GetHostByID(gomock.Any(), symID, gomock.Any()).
		Return(&types.Host{HostID: "csi-node--worker-1", HostType: "iSCSI"}, nil).AnyTimes()

	svc := &service{opts: Opts{PortGroups: []string{"csi-pg-1"}}}

	// PublishMaskingViews fails even though we sent the correct PG
	mockClient104.EXPECT().PublishMaskingViews(gomock.Any(), symID, gomock.Any()).
		Return(nil, errors.New("array internal error")).Times(1)

	p := &u4p104VolumePublisher{
		s:            svc,
		legacyClient: mockClient,
		client104:    mockClient104,
		symID:        symID,
		devID:        devID,
		volID:        volID,
		reqID:        "req-existing-mv-fail",
	}
	resp, err := p.Publish(context.Background(), &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	})
	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "PublishMaskingViews failed")
}

// mockMVSetup is a helper that wires GetVolumeByID and GetMaskingViewByID for the
// "MV already exists" tests so each test only needs to configure what is unique.
func mockMVSetup(mockClient *mocks.MockPmaxClient, symID, devID, tgtMaskingViewID string, mv *types.MaskingView) {
	mockClient.EXPECT().GetVolumeByID(gomock.Any(), symID, devID).
		Return(&types.Volume{
			VolumeID:         devID,
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     "60000970000120000001533030314142",
		}, nil).Times(1)

	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), symID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, mvID string) (*types.MaskingView, error) {
			if mvID == tgtMaskingViewID {
				return mv, nil
			}
			return nil, errors.New("not found")
		}).AnyTimes()

	mockClient.EXPECT().GetHostByID(gomock.Any(), symID, gomock.Any()).
		Return(&types.Host{HostID: "csi-node--worker-1", HostType: "iSCSI"}, nil).AnyTimes()
}

// publishReqWorker1 builds a standard single-node-writer request for worker-1.
func publishReqWorker1() *csi.ControllerPublishVolumeRequest {
	return &csi.ControllerPublishVolumeRequest{
		NodeId: "worker-1",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{FsType: "ext4"},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	}
}

// TestU4P104VolumePublisher_ExistingMV_ConflictingHost verifies that when the
// existing MaskingView's HostID does not match the hostID derived from nodeID
// the driver returns codes.Internal with a message mirroring the legacy error
// from storage_group_svc.go:addVolumesToSGMV, and does NOT call PublishMaskingViews.
func TestU4P104VolumePublisher_ExistingMV_ConflictingHost(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	devID := "011AB"
	symID := "000120000001"
	tgtMaskingViewID := "csi-mv--worker-1"
	// MV on array was created for a different host (e.g. after a node rename)
	conflictingHostID := "csi-node--old-worker-name"
	computedSGID := "csi-no-srp-sg--worker-1"

	nodeCache.Delete(symID + ":worker-1")

	mockMVSetup(mockClient, symID, devID, tgtMaskingViewID, &types.MaskingView{
		MaskingViewID:  tgtMaskingViewID,
		PortGroupID:    "csi-pg-1",
		HostID:         conflictingHostID, // diverges from what driver computes
		StorageGroupID: computedSGID,      // SG matches — only host conflicts
	})

	// PublishMaskingViews must NOT be called when conflict is detected
	// (gomock will fail the test if it is called, since no EXPECT is set)

	svc := &service{opts: Opts{PortGroups: []string{"csi-pg-1"}}}

	p := &u4p104VolumePublisher{
		s: svc, legacyClient: mockClient, client104: mockClient104,
		symID: symID, devID: devID, volID: volID, reqID: "req-conflict-host",
	}
	resp, err := p.Publish(context.Background(), publishReqWorker1())

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	// Error message must match the legacy format exactly
	assert.Contains(t, err.Error(), tgtMaskingViewID)
	assert.Contains(t, err.Error(), "conflicting SG")
	assert.Contains(t, err.Error(), "Host")
}

// TestU4P104VolumePublisher_ExistingMV_ConflictingSG verifies that when the
// existing MaskingView's StorageGroupID does not match the SG derived from
// nodeID the driver returns codes.Internal and does NOT call PublishMaskingViews.
func TestU4P104VolumePublisher_ExistingMV_ConflictingSG(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	devID := "011AB"
	symID := "000120000001"
	tgtMaskingViewID := "csi-mv--worker-1"
	computedHostID := "csi-node--worker-1"
	// MV on array carries a different SG (e.g. cluster prefix was changed)
	conflictingSGID := "csi-no-srp-sg--old-prefix-worker-1"

	nodeCache.Delete(symID + ":worker-1")

	mockMVSetup(mockClient, symID, devID, tgtMaskingViewID, &types.MaskingView{
		MaskingViewID:  tgtMaskingViewID,
		PortGroupID:    "csi-pg-1",
		HostID:         computedHostID,  // host matches
		StorageGroupID: conflictingSGID, // SG diverges
	})

	svc := &service{opts: Opts{PortGroups: []string{"csi-pg-1"}}}

	p := &u4p104VolumePublisher{
		s: svc, legacyClient: mockClient, client104: mockClient104,
		symID: symID, devID: devID, volID: volID, reqID: "req-conflict-sg",
	}
	resp, err := p.Publish(context.Background(), publishReqWorker1())

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Contains(t, err.Error(), tgtMaskingViewID)
	assert.Contains(t, err.Error(), "conflicting SG")
}

// TestU4P104VolumePublisher_ExistingMV_ConflictingHostAndSG verifies the error
// message format when both Host and SG diverge from the array's ground truth,
// and confirms it mirrors the legacy error from storage_group_svc.go line 610-614.
func TestU4P104VolumePublisher_ExistingMV_ConflictingHostAndSG(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	devID := "011AB"
	symID := "000120000001"
	tgtMaskingViewID := "csi-mv--worker-1"

	nodeCache.Delete(symID + ":worker-1")

	mockMVSetup(mockClient, symID, devID, tgtMaskingViewID, &types.MaskingView{
		MaskingViewID:  tgtMaskingViewID,
		PortGroupID:    "csi-pg-1",
		HostID:         "csi-node--renamed-host",
		StorageGroupID: "csi-no-srp-sg--renamed-host",
	})

	svc := &service{opts: Opts{PortGroups: []string{"csi-pg-1"}}}

	p := &u4p104VolumePublisher{
		s: svc, legacyClient: mockClient, client104: mockClient104,
		symID: symID, devID: devID, volID: volID, reqID: "req-conflict-both",
	}
	resp, err := p.Publish(context.Background(), publishReqWorker1())

	assert.Nil(t, resp)
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	// Verify the error matches legacy format: "Existing masking view X with conflicting SG Y or Host Z"
	assert.Contains(t, err.Error(), "Existing masking view")
	assert.Contains(t, err.Error(), tgtMaskingViewID)
	assert.Contains(t, err.Error(), "conflicting SG")
}

// TestU4P104VolumePublisher_ExistingMV_VsphereHostGroupIDMatch verifies that
// when an MV was created for a vSphere host group (HostID is empty, HostGroupID
// is set), the validation succeeds and publish proceeds normally.
// This mirrors the legacy EqualFold check on tgtMaskingView.HostGroupID at
// storage_group_svc.go:595.
func TestU4P104VolumePublisher_ExistingMV_VsphereHostGroupIDMatch(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	LockRequestHandler()

	mockClient := mocks.NewMockPmaxClient(ctrl)
	mockClient104 := mocks.NewMockPmaxClient(ctrl)

	volID := "csi-ABC-pmax-testvol-000120000001-011AB"
	devID := "011AB"
	symID := "000120000001"
	effectiveWWN := "60000970000120000001533030314142"
	// For vSphere, GetVSphereFCHostSGAndMVIDFromNodeID returns opts.VSphereHostName
	vsphereHostName := "vsphere-host-group-1"
	tgtMaskingViewID := "csi-mv-ABC-VSPHERE"
	tgtStorageGroupID := "csi-no-srp-sg-ABC-VSPHERE"
	existingPGID := "csi-vsphere-pg"

	nodeCache.Delete(symID + ":worker-1")

	mockClient.EXPECT().GetVolumeByID(gomock.Any(), symID, devID).
		Return(&types.Volume{
			VolumeID:         devID,
			VolumeIdentifier: "csi-ABC-pmax-testvol",
			EffectiveWWN:     effectiveWWN,
		}, nil).Times(1)

	// IsNodeISCSI with IsVsphereEnabled=true calls getHostForVsphere which calls
	// GetHostByID(VSphereHostName). On success it returns (false, nil) — no iSCSI.
	mockClient.EXPECT().GetHostByID(gomock.Any(), symID, vsphereHostName).
		Return(&types.Host{HostID: vsphereHostName}, nil).AnyTimes()

	// GetMaskingViewByID: tgtMV exists with HostGroupID set (not HostID)
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), symID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, mvID string) (*types.MaskingView, error) {
			if mvID == tgtMaskingViewID {
				return &types.MaskingView{
					MaskingViewID:  tgtMaskingViewID,
					PortGroupID:    existingPGID,
					HostID:         "",              // vSphere MVs use HostGroupID
					HostGroupID:    vsphereHostName, // must match computed hostID
					StorageGroupID: tgtStorageGroupID,
				}, nil
			}
			return nil, errors.New("not found")
		}).AnyTimes()

	svc := &service{
		opts: Opts{
			IsVsphereEnabled: true,
			VSphereHostName:  vsphereHostName,
			VSpherePortGroup: existingPGID,
			ClusterPrefix:    "ABC",
		},
	}
	getPmaxCache(symID)

	mockClient104.EXPECT().PublishMaskingViews(gomock.Any(), symID, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, param *types.PublishMaskingViewsParam) (*types.PublishMaskingViewResponse, error) {
			assert.Equal(t, existingPGID, param.MaskingViews[0].PortGroup.ID)
			assert.Equal(t, vsphereHostName, param.MaskingViews[0].Host.ID)
			return &types.PublishMaskingViewResponse{
				Summary: types.PublishSummary{Total: 1, Succeeded: 1},
			}, nil
		}).Times(1)

	mockClient.EXPECT().GetMaskingViewConnections(gomock.Any(), symID, tgtMaskingViewID, devID).
		Return([]*types.MaskingViewConnection{
			{VolumeID: devID, HostLUNAddress: "0001", DirectorPort: "FA-1D:4"},
			{VolumeID: devID, HostLUNAddress: "0001", DirectorPort: "FA-2D:4"},
		}, nil).Times(1)

	mockClient.EXPECT().GetPort(gomock.Any(), symID, "FA-1D", "4").
		Return(&types.Port{SymmetrixPort: types.SymmetrixPortType{Identifier: "50:00:09:72:00:00:00:01"}}, nil).Times(1)
	mockClient.EXPECT().GetPort(gomock.Any(), symID, "FA-2D", "4").
		Return(&types.Port{SymmetrixPort: types.SymmetrixPortType{Identifier: "50:00:09:72:00:00:00:02"}}, nil).Times(1)

	p := &u4p104VolumePublisher{
		s: svc, legacyClient: mockClient, client104: mockClient104,
		symID: symID, devID: devID, volID: volID, reqID: "req-vsphere-hgid",
	}
	resp, err := p.Publish(context.Background(), publishReqWorker1())

	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, effectiveWWN, resp.PublishContext[PublishContextDeviceWWN])
}

func TestBuildPublishMaskingViewsParam(t *testing.T) {
	param := buildPublishMaskingViewsParam("csi-mv--worker-1", "csi-sg-1", "011AB", "csi-node--worker-1", "csi-pg-1")

	assert.NotNil(t, param)
	assert.Len(t, param.MaskingViews, 1)

	mv := param.MaskingViews[0]
	assert.Equal(t, "csi-mv--worker-1", mv.ID)

	assert.NotNil(t, mv.StorageGroup)
	assert.Equal(t, "csi-sg-1", mv.StorageGroup.ID)
	assert.NotNil(t, mv.StorageGroup.Actions)
	assert.NotNil(t, mv.StorageGroup.Actions.AddVolumesToStorageGroupAction)
	assert.Len(t, mv.StorageGroup.Actions.AddVolumesToStorageGroupAction.Volumes, 1)
	assert.Len(t, mv.StorageGroup.Actions.AddVolumesToStorageGroupAction.Volumes[0].ExistingVolumes, 1)
	assert.Equal(t, "011AB", mv.StorageGroup.Actions.AddVolumesToStorageGroupAction.Volumes[0].ExistingVolumes[0].ID)

	assert.NotNil(t, mv.Host)
	assert.Equal(t, "csi-node--worker-1", mv.Host.ID)

	assert.NotNil(t, mv.PortGroup)
	assert.Equal(t, "csi-pg-1", mv.PortGroup.ID)
}
