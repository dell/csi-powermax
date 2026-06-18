package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/dell/csi-powermax/v2/pkg/file"

	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ---------------------------------------------------------------------------
// Struct definitions
// ---------------------------------------------------------------------------

type u4p104VolumePublisher struct {
	s              *service
	legacyClient   pmax.Pmax // standard client for lookups (GetVolumeByID, MV connections, etc.)
	client104      pmax.Pmax // 10.4-capable client for PublishMaskingViews
	symID          string
	devID          string
	volID          string
	volumeName     string
	remoteSymID    string
	remoteVolumeID string
	reqID          string
}

type legacyVolumePublisher struct {
	s              *service
	pmaxClient     pmax.Pmax
	symID          string
	devID          string
	volID          string
	volumeName     string
	remoteSymID    string
	remoteVolumeID string
	reqID          string
}

// ---------------------------------------------------------------------------
// 10.4 volume publishing
// ---------------------------------------------------------------------------

// buildPublishMaskingViewsParam constructs the request payload for the 10.4
// PublishMaskingViews API call, binding a volume to a masking view, storage
// group, host, and port group.
func buildPublishMaskingViewsParam(tgtMaskingViewID, tgtStorageGroupID, devID, hostID, portGroupID string) *types.PublishMaskingViewsParam {
	return &types.PublishMaskingViewsParam{
		MaskingViews: []types.MaskingViewPublishParam{
			{
				ID: tgtMaskingViewID,
				StorageGroup: &types.StorageGroupPublishParam{
					ID: tgtStorageGroupID,
					Actions: &types.StorageGroupPublishActions{
						AddVolumesToStorageGroupAction: &types.AddVolumesToStorageGroupAction{
							Volumes: []types.VolumePublishParam{
								{
									ExistingVolumes: []types.ExistingVolumeParam{
										{ID: devID},
									},
								},
							},
						},
					},
				},
				Host: &types.HostPublishParam{
					ID: hostID,
				},
				PortGroup: &types.PortGroupPublishParam{
					ID: portGroupID,
				},
			},
		},
	}
}

// 10.4 publish — uses the single PublishMaskingViews API to atomically ensure
// the volume is in the correct storage group, the masking view exists with the
// right host and port group, and is connected. This replaces the legacy multi-step
// sequence of GetStorageGroup/CreateStorageGroup + AddVolumesToStorageGroup +
// GetHost + SelectOrCreatePortGroup + CreateMaskingView.
func (p *u4p104VolumePublisher) Publish(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	log := log.WithContext(ctx)

	nodeID := req.GetNodeId()
	if nodeID == "" {
		log.Error("node ID is required")
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}

	vc := req.GetVolumeCapability()
	if vc == nil {
		log.Error("volume capability is required")
		return nil, status.Error(codes.InvalidArgument, "volume capability is required")
	}
	am := vc.GetAccessMode()
	if am == nil {
		log.Error("access mode is required")
		return nil, status.Error(codes.InvalidArgument, "access mode is required")
	}
	if am.Mode == csi.VolumeCapability_AccessMode_UNKNOWN {
		log.Error(errUnknownAccessMode)
		return nil, status.Error(codes.InvalidArgument, errUnknownAccessMode)
	}

	// Fetch the volume details from array (need EffectiveWWN for publish context)
	symID, devID, vol, err := p.s.GetVolumeByID(ctx, p.volID, p.legacyClient)
	if err != nil {
		log.Error("GetVolumeByID Error: " + err.Error())
		return nil, err
	}

	fields := map[string]interface{}{
		"SymmetrixID":  symID,
		"VolumeId":     p.volID,
		"NodeId":       nodeID,
		"AccessMode":   am.Mode,
		"CSIRequestID": p.reqID,
	}
	log.WithFields(fields).Info("Executing 10.4 ControllerPublishVolume with following fields")

	// Determine host type (NVMe, iSCSI, FC) — check node cache first
	isNVMETCP := false
	isISCSI := false
	nodeInCache := false
	cacheID := symID + ":" + nodeID
	if tempHostID, ok := nodeCache.Load(cacheID); ok {
		log.Debugf("10.4 Loaded nodeID: %s, hostID: %s from node cache", nodeID, tempHostID.(string))
		nodeInCache = true
		if !strings.Contains(tempHostID.(string), "-FC") {
			isISCSI = true
		}
		if strings.Contains(tempHostID.(string), "-NVMETCP") {
			isISCSI = false
			isNVMETCP = true
		}
	} else {
		isNVMETCP, err = p.s.IsNodeNVMe(ctx, symID, nodeID, p.legacyClient)
		if err != nil {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		if !isNVMETCP {
			isISCSI, err = p.s.IsNodeISCSI(ctx, symID, nodeID, p.legacyClient)
			if err != nil {
				return nil, status.Error(codes.NotFound, err.Error())
			}
		}
	}

	hostID, tgtStorageGroupID, tgtMaskingViewID := p.s.GetNVMETCPHostSGAndMVIDFromNodeID(nodeID)
	if !isNVMETCP {
		hostID, tgtStorageGroupID, tgtMaskingViewID = p.s.GetHostSGAndMVIDFromNodeID(nodeID, isISCSI)
	}

	if !nodeInCache {
		val, loaded := nodeCache.LoadOrStore(cacheID, hostID)
		if !loaded {
			log.Debugf("10.4 Added nodeID: %s, hostID: %s to node cache", nodeID, hostID)
		} else {
			log.Debugf("10.4 Another goroutine added hostID: %s for node: %s to node cache", val.(string), nodeID)
			if hostID != val.(string) {
				log.Warnf("10.4 Mismatch between calculated hostID: %s and cached hostID: %s from node cache", hostID, val.(string))
			}
		}
	}

	// Check if the MaskingView already exists
	existingMV, mvErr := p.legacyClient.GetMaskingViewByID(ctx, symID, tgtMaskingViewID)
	var portGroupID string
	if mvErr == nil && existingMV != nil {
		// MaskingView exists — validate Host and SG match before proceeding.
		// This mirrors the legacy check in storage_group_svc.go:addVolumesToSGMV.
		// If they diverge (e.g. node rename, transport flip, prefix change) the
		// atomic PublishMaskingViews call would fail anyway; returning a clear error
		// here avoids that unnecessary round-trip and matches legacy behaviour.
		// vSphere MVs carry the host via HostGroupID instead of HostID, so check both.
		hostMatch := strings.EqualFold(existingMV.HostID, hostID) || strings.EqualFold(existingMV.HostGroupID, hostID)
		sgMatch := strings.EqualFold(existingMV.StorageGroupID, tgtStorageGroupID)
		if !hostMatch || !sgMatch {
			errormsg := fmt.Sprintf("10.4 ControllerPublishVolume: Existing masking view %s with conflicting SG %s or Host %s",
				tgtMaskingViewID, tgtStorageGroupID, hostID)
			log.Error(errormsg)
			return nil, status.Error(codes.Internal, errormsg)
		}
		// All entities are consistent — reuse the existing PortGroup to avoid
		// modification errors (the original CSME-250 fix).
		portGroupID = existingMV.PortGroupID
		log.Infof("10.4 ControllerPublishVolume: Using existing PortGroup %s from MaskingView %s", portGroupID, tgtMaskingViewID)
	} else {
		// MaskingView doesn't exist, select or create a port group
		log.Debugf("10.4 ControllerPublishVolume: MaskingView %s not found, will select/create port group", tgtMaskingViewID)

		// Get host details for port group selection
		host, err := p.legacyClient.GetHostByID(ctx, symID, hostID)
		if err != nil {
			log.Errorf("10.4 ControllerPublishVolume: Failed to fetch host details for %s on %s: %s", hostID, symID, err.Error())
			return nil, status.Errorf(codes.NotFound, "Failed to fetch host details for %s on %s: %s", hostID, symID, err.Error())
		}

		// Select or create the port group
		portGroupID, err = p.s.SelectOrCreatePortGroup(ctx, symID, host, p.legacyClient)
		if err != nil {
			log.Errorf("10.4 ControllerPublishVolume: Failed to select/create port group for host %s on %s: %s", hostID, symID, err.Error())
			return nil, status.Errorf(codes.Internal, "Failed to select/create port group for host %s on %s: %s", hostID, symID, err.Error())
		}
	}

	publishContext := make(map[string]string)
	if len(vol.EffectiveWWN) > 0 {
		publishContext[PublishContextDeviceWWN] = vol.EffectiveWWN
	} else {
		return nil, status.Errorf(codes.Internal,
			"PublishVolume: Volume %s has no effective WWN, Unisphere may not be synchronized with array or synchronization may be in progress", p.volID)
	}

	publishParam := buildPublishMaskingViewsParam(tgtMaskingViewID, tgtStorageGroupID, devID, hostID, portGroupID)

	log.Infof("10.4 PublishMaskingViews request for volume %s on array %s, MV: %s, SG: %s, Host: %s, PG: %s",
		devID, symID, tgtMaskingViewID, tgtStorageGroupID, hostID, portGroupID)

	publishResp, err := p.client104.PublishMaskingViews(ctx, symID, publishParam)
	if err != nil {
		log.Errorf("10.4 PublishMaskingViews failed for volume %s on array %s: %v", devID, symID, err)
		return nil, status.Errorf(codes.Internal, "10.4 PublishMaskingViews failed: %s", err.Error())
	}

	if publishResp != nil && publishResp.Summary.Failed > 0 {
		log.Errorf("10.4 PublishMaskingViews reported failures for volume %s on array %s: %+v", devID, symID, publishResp.Results)
		return nil, status.Errorf(codes.Internal, "10.4 PublishMaskingViews failed for masking view %s", tgtMaskingViewID)
	}

	// Fetch MV connections for the device after successful publish
	lockNum := RequestLock(getMVLockKey(symID, tgtMaskingViewID), p.reqID)
	connections, connErr := p.legacyClient.GetMaskingViewConnections(ctx, symID, tgtMaskingViewID, devID)
	ReleaseLock(getMVLockKey(symID, tgtMaskingViewID), p.reqID, lockNum)
	if connErr != nil {
		log.Warnf("10.4 ControllerPublishVolume: initial connection fetch failed for %s on %s: %v, will retry in updatePublishContext", devID, symID, connErr)
		connections = nil
	} else {
		log.Infof("10.4 ControllerPublishVolume: fetched %d connections for volume %s on MV %s", len(connections), devID, tgtMaskingViewID)
	}

	// Build publish context from MV connections (LUN address and port identifiers)
	return p.s.updatePublishContext(ctx, publishContext, symID, tgtMaskingViewID, devID, p.reqID, connections, p.legacyClient, true)
}

// ---------------------------------------------------------------------------
// Legacy volume publishing (pre-10.4)
// ---------------------------------------------------------------------------

func (p *legacyVolumePublisher) Publish(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
	log := log.WithContext(ctx)

	reqID := p.reqID
	symID := p.symID
	devID := p.devID
	volID := p.volID
	volumeName := p.volumeName
	remoteSymID := p.remoteSymID
	remoteVolumeID := p.remoteVolumeID
	pmaxClient := p.pmaxClient

	volumeContext := req.GetVolumeContext()
	if volumeContext != nil {
		log.Infof("VolumeContext:")
		for key, value := range volumeContext {
			log.Infof("    [%s]=%s", key, value)
		}
	}

	nodeID := req.GetNodeId()
	if nodeID == "" {
		log.Error("node ID is required")
		return nil, status.Error(codes.InvalidArgument,
			"node ID is required")
	}

	vc := req.GetVolumeCapability()
	if vc == nil {
		log.Error("volume capability is required")
		return nil, status.Error(codes.InvalidArgument,
			"volume capability is required")
	}
	am := vc.GetAccessMode()
	if am == nil {
		log.Error("access mode is required")
		return nil, status.Error(codes.InvalidArgument,
			"access mode is required")
	}

	if am.Mode == csi.VolumeCapability_AccessMode_UNKNOWN {
		log.Error(errUnknownAccessMode)
		return nil, status.Error(codes.InvalidArgument, errUnknownAccessMode)
	}
	isNFS := accTypeIsNFS([]*csi.VolumeCapability{vc})
	if isNFS {
		// incoming request for file system volume
		return file.CreateNFSExport(ctx, reqID, symID, devID, am, volumeContext, pmaxClient)
	}

	if vc.GetMount().GetFsType() == "" {
		// can happen when doing static provisioning, check for filesystem existence
		log.Debug("fsType empty...checking for file system existence")
		_, err := pmaxClient.GetFileSystemByID(ctx, symID, devID)
		if err == nil {
			// we found fs, proceed to CreateNFSExport
			return nil, status.Errorf(codes.Unavailable, "static provisioning on a file system is not supported.")
		}
	}

	// Fetch the volume details from array
	symID, devID, vol, err := p.s.GetVolumeByID(ctx, volID, pmaxClient)
	if err != nil {
		log.Error("GetVolumeByID Error: " + err.Error())
		return nil, err
	}

	// log all parameters used in ControllerPublishVolume call
	fields := map[string]interface{}{
		"SymmetrixID":     symID,
		"VolumeId":        volID,
		"NodeId":          nodeID,
		"AccessMode":      am.Mode,
		"CSIRequestID":    reqID,
		"IsVsphereVolume": p.s.opts.IsVsphereEnabled,
	}
	log.WithFields(fields).Info("Executing ControllerPublishVolume with following fields")
	// flag createNFSExport()
	isNVMETCP := false
	isISCSI := false
	// Check if node ID is present in cache
	nodeInCache := false
	cacheID := symID + ":" + nodeID
	tempHostID, ok := nodeCache.Load(cacheID)
	if ok {
		log.Debugf("REQ ID: %s Loaded nodeID: %s, hostID: %s from node cache",
			reqID, nodeID, tempHostID.(string))
		nodeInCache = true
		if !strings.Contains(tempHostID.(string), "-FC") {
			isISCSI = true
		}
		if strings.Contains(tempHostID.(string), "-NVMETCP") {
			isISCSI = false
			isNVMETCP = true
		}
	} else {
		log.Debugf("REQ ID: %s nodeID: %s not present in node cache", reqID, nodeID)
		isNVMETCP, err = p.s.IsNodeNVMe(ctx, symID, nodeID, pmaxClient)
		if err != nil {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		if !isNVMETCP {
			isISCSI, err = p.s.IsNodeISCSI(ctx, symID, nodeID, pmaxClient)
			if err != nil {
				return nil, status.Error(codes.NotFound, err.Error())
			}
		}
	}

	hostID, tgtStorageGroupID, tgtMaskingViewID := p.s.GetNVMETCPHostSGAndMVIDFromNodeID(nodeID)

	if !isNVMETCP {
		// Update the values, if NVME is false
		hostID, tgtStorageGroupID, tgtMaskingViewID = p.s.GetHostSGAndMVIDFromNodeID(nodeID, isISCSI)
	}

	if !nodeInCache {
		// Update the map
		val, ok := nodeCache.LoadOrStore(cacheID, hostID)
		if !ok {
			log.Debugf("REQ ID: %s Added nodeID: %s, hostID: %s to node cache", reqID, nodeID, hostID)
		} else {
			log.Debugf("REQ ID: %s Some other goroutine added hostID: %s for node: %s to node cache",
				reqID, val.(string), nodeID)
			if hostID != val.(string) {
				log.Warnf("REQ ID: %s Mismatch between calculated value: %s and latest value: %s from node cache",
					reqID, val.(string), hostID)
			}
		}
	}

	publishContext := make(map[string]string)
	if len(vol.EffectiveWWN) > 0 {
		publishContext[PublishContextDeviceWWN] = vol.EffectiveWWN
	} else {
		return nil, status.Errorf(codes.Internal, "PublishVolume: Volume %s has no effective WWN, Unisphere may not be synchronized with array or synchronization may be in progress", volID)
	}

	ctrlPubRes, ctrlPubErr := p.s.publishVolume(ctx, publishContext, tgtStorageGroupID, hostID, symID, symID, tgtMaskingViewID, devID, reqID, volumeName, am, pmaxClient, true)
	if ctrlPubErr != nil {
		return nil, ctrlPubErr
	}

	if remoteSymID != "" && remoteVolumeID != "" {
		remoteVol, err := pmaxClient.GetVolumeByID(ctx, remoteSymID, remoteVolumeID)
		if strings.Compare(remoteVol.EffectiveWWN, vol.EffectiveWWN) != 0 {
			// Refresh the symmetrix
			err := pmaxClient.RefreshSymmetrix(ctx, symID)
			if err != nil {
				if !strings.Contains(err.Error(), "Too Many Requests") {
					return nil, status.Errorf(codes.Internal, "PublishVolume: Could not refresh symmetrix: (%s)", err.Error())
				}
				return nil, status.Errorf(codes.Internal, "symmetrix sync in progress, waiting for cache to update")
			}
			// wait till the remote volume has an effective wwn
			return nil, status.Errorf(codes.Internal, "PublishVolume: Could not publish remote volume: (%s)", "remote volume does not have effective wwn, waiting for it to SYNC")
		}
		log.Debugf("remote-vol: %#v, error: %#v", remoteVol, err)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "PublishVolume: Could not retrieve remote volume: (%s)", err.Error())
		}
		publishContext[RemotePublishContextDeviceWWN] = remoteVol.EffectiveWWN
		return p.s.publishVolume(ctx, publishContext, tgtStorageGroupID, hostID, symID, remoteSymID, tgtMaskingViewID, remoteVolumeID, reqID, volumeName, am, pmaxClient, false)
	}
	return ctrlPubRes, ctrlPubErr
}
