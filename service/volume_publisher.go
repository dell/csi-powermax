/*
 Copyright © 2021-2026 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/dell/csmlog"

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
	nodeID := req.GetNodeId()
	if nodeID == "" {
		csmlog.WithContext(ctx).Error("node ID is required")
		return nil, status.Error(codes.InvalidArgument, "node ID is required")
	}

	vc := req.GetVolumeCapability()
	if vc == nil {
		csmlog.WithContext(ctx).Error("volume capability is required")
		return nil, status.Error(codes.InvalidArgument, "volume capability is required")
	}
	am := vc.GetAccessMode()
	if am == nil {
		csmlog.WithContext(ctx).Error("access mode is required")
		return nil, status.Error(codes.InvalidArgument, "access mode is required")
	}
	if am.Mode == csi.VolumeCapability_AccessMode_UNKNOWN {
		csmlog.WithContext(ctx).Error(errUnknownAccessMode)
		return nil, status.Error(codes.InvalidArgument, errUnknownAccessMode)
	}

	// Fetch the volume details from array (need EffectiveWWN for publish context)
	symID, devID, vol, err := p.s.GetVolumeByID(ctx, p.volID, p.legacyClient)
	if err != nil {
		csmlog.WithContext(ctx).Error("GetVolumeByID Error: " + err.Error())
		return nil, err
	}

	fields := map[string]interface{}{
		"SymmetrixID":  symID,
		"VolumeId":     p.volID,
		"NodeId":       nodeID,
		"AccessMode":   am.Mode,
		"CSIRequestID": p.reqID,
	}
	csmlog.WithFields(fields).Info("Executing 10.4 ControllerPublishVolume with following fields")

	// Determine host type (NVMe, iSCSI, FC) — check node cache first
	isNVMETCP := false
	isISCSI := false
	nodeInCache := false
	cacheID := symID + ":" + nodeID
	// A BFS-adopted host carries an administrator-chosen name, so neither the
	// suffix-based protocol detection below nor the CSI naming convention applies.
	// A failed lookup must not be read as "not adopted" — that would silently
	// derive a host name that does not exist on the array.
	adoptedID, err := p.s.getAdoptedHostIDFromNodeLabels(ctx, symID, nodeID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if adoptedID != "" {
		csmlog.WithContext(ctx).Debugf("10.4 Node %s has adopted host %s on array %s, protocol is FC", nodeID, adoptedID, symID)
		isISCSI = false
		isNVMETCP = false
	} else if tempHostID, ok := nodeCache.Load(cacheID); ok {
		csmlog.WithContext(ctx).Debugf("10.4 Loaded nodeID: %s, hostID: %s from node cache", nodeID, tempHostID.(string))
		nodeInCache = true
		if !strings.Contains(tempHostID.(string), "-FC") {
			isISCSI = true
		}
		if strings.Contains(tempHostID.(string), "-NVMETCP") {
			isISCSI = false
			isNVMETCP = true
		}
	} else {
		isNVMETCP, err = p.s.IsNodeNVMe(ctx, symID, nodeID, p.legacyClient, adoptedID)
		if err != nil {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		if !isNVMETCP {
			isISCSI, err = p.s.IsNodeISCSI(ctx, symID, nodeID, p.legacyClient, adoptedID)
			if err != nil {
				return nil, status.Error(codes.NotFound, err.Error())
			}
		}
	}

	hostID, tgtStorageGroupID, tgtMaskingViewID := p.s.GetNVMETCPHostSGAndMVIDFromNodeID(nodeID)
	if !isNVMETCP {
		hostID, tgtStorageGroupID, tgtMaskingViewID = p.s.GetHostSGAndMVIDFromNodeID(nodeID, isISCSI)
	}
	if adoptedID != "" {
		// The CSI storage group and masking view keep the standard CSI naming; only
		// the host is the pre-existing BFS object.
		hostID = adoptedID
	}

	if !nodeInCache {
		val, loaded := nodeCache.LoadOrStore(cacheID, hostID)
		if !loaded {
			csmlog.WithContext(ctx).Debugf("10.4 Added nodeID: %s, hostID: %s to node cache", nodeID, hostID)
		} else {
			csmlog.WithContext(ctx).Debugf("10.4 Another goroutine added hostID: %s for node: %s to node cache", val.(string), nodeID)
			if hostID != val.(string) {
				csmlog.WithContext(ctx).Warnf("10.4 Mismatch between calculated hostID: %s and cached hostID: %s from node cache", hostID, val.(string))
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
			csmlog.Error(errormsg)
			return nil, status.Error(codes.Internal, errormsg)
		}
		// All entities are consistent — reuse the existing PortGroup to avoid
		// modification errors (the original CSME-250 fix).
		portGroupID = existingMV.PortGroupID
		csmlog.Infof("10.4 ControllerPublishVolume: Using existing PortGroup %s from MaskingView %s", portGroupID, tgtMaskingViewID)
	} else {
		// MaskingView doesn't exist, select or create a port group
		csmlog.Debugf("10.4 ControllerPublishVolume: MaskingView %s not found, will select/create port group", tgtMaskingViewID)

		// Get host details for port group selection
		host, err := p.legacyClient.GetHostByID(ctx, symID, hostID)
		if err != nil {
			csmlog.Errorf("10.4 ControllerPublishVolume: Failed to fetch host details for %s on %s: %s", hostID, symID, err.Error())
			return nil, status.Errorf(codes.NotFound, "Failed to fetch host details for %s on %s: %s", hostID, symID, err.Error())
		}

		// Select or create the port group
		portGroupID, err = p.s.SelectOrCreatePortGroup(ctx, symID, host, p.legacyClient)
		if err != nil {
			csmlog.Errorf("10.4 ControllerPublishVolume: Failed to select/create port group for host %s on %s: %s", hostID, symID, err.Error())
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

	csmlog.WithContext(ctx).Infof("10.4 PublishMaskingViews request for volume %s on array %s, MV: %s, SG: %s, Host: %s, PG: %s",
		devID, symID, tgtMaskingViewID, tgtStorageGroupID, hostID, portGroupID)

	publishResp, err := p.client104.PublishMaskingViews(ctx, symID, publishParam)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("10.4 PublishMaskingViews failed for volume %s on array %s: %v", devID, symID, err)
		return nil, status.Errorf(codes.Internal, "10.4 PublishMaskingViews failed: %s", err.Error())
	}

	if publishResp != nil && publishResp.Summary.Failed > 0 {
		csmlog.WithContext(ctx).Errorf("10.4 PublishMaskingViews reported failures for volume %s on array %s: %+v", devID, symID, publishResp.Results)
		return nil, status.Errorf(codes.Internal, "10.4 PublishMaskingViews failed for masking view %s", tgtMaskingViewID)
	}

	// Fetch MV connections for the device after successful publish
	lockNum, err := RequestLock(getMVLockKey(symID, tgtMaskingViewID), p.reqID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire lock: %v", err)
	}
	connections, connErr := p.legacyClient.GetMaskingViewConnections(ctx, symID, tgtMaskingViewID, devID)
	ReleaseLock(getMVLockKey(symID, tgtMaskingViewID), p.reqID, lockNum)
	if connErr != nil {
		csmlog.WithContext(ctx).Warnf("10.4 ControllerPublishVolume: initial connection fetch failed for %s on %s: %v, will retry in updatePublishContext", devID, symID, connErr)
		connections = nil
	} else {
		csmlog.WithContext(ctx).Infof("10.4 ControllerPublishVolume: fetched %d connections for volume %s on MV %s", len(connections), devID, tgtMaskingViewID)
	}

	// Build publish context from MV connections (LUN address and port identifiers)
	return p.s.updatePublishContext(ctx, publishContext, symID, tgtMaskingViewID, devID, p.reqID, connections, p.legacyClient, true)
}

// ---------------------------------------------------------------------------
// Legacy volume publishing (pre-10.4)
// ---------------------------------------------------------------------------

func (p *legacyVolumePublisher) Publish(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error) {
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
		csmlog.WithContext(ctx).Infof("VolumeContext:")
		for key, value := range volumeContext {
			csmlog.WithContext(ctx).Infof("    [%s]=%s", key, value)
		}
	}

	nodeID := req.GetNodeId()
	if nodeID == "" {
		csmlog.WithContext(ctx).Error("node ID is required")
		return nil, status.Error(codes.InvalidArgument,
			"node ID is required")
	}

	vc := req.GetVolumeCapability()
	if vc == nil {
		csmlog.WithContext(ctx).Error("volume capability is required")
		return nil, status.Error(codes.InvalidArgument,
			"volume capability is required")
	}
	am := vc.GetAccessMode()
	if am == nil {
		csmlog.WithContext(ctx).Error("access mode is required")
		return nil, status.Error(codes.InvalidArgument,
			"access mode is required")
	}

	if am.Mode == csi.VolumeCapability_AccessMode_UNKNOWN {
		csmlog.WithContext(ctx).Error(errUnknownAccessMode)
		return nil, status.Error(codes.InvalidArgument, errUnknownAccessMode)
	}
	isNFS := accTypeIsNFS([]*csi.VolumeCapability{vc})
	if isNFS {
		// incoming request for file system volume
		return file.CreateNFSExport(ctx, reqID, symID, devID, am, volumeContext, pmaxClient)
	}

	if vc.GetMount().GetFsType() == "" {
		// can happen when doing static provisioning, check for filesystem existence
		csmlog.WithContext(ctx).Debug("fsType empty...checking for file system existence")
		_, err := pmaxClient.GetFileSystemByID(ctx, symID, devID)
		if err == nil {
			// we found fs, proceed to CreateNFSExport
			return nil, status.Errorf(codes.Unavailable, "static provisioning on a file system is not supported.")
		}
	}

	// Fetch the volume details from array.
	// For Metro (non-uniform) volumes, the "local" side parsed from the CSI ID
	// may be unreachable or no longer hold the volume (e.g. preferred site
	// failure). In that case fall back to the remote side so the rest of the
	// publish flow can still execute against the surviving array.
	localVolumeMissing := false
	symID, devID, vol, err := p.s.GetVolumeByID(ctx, volID, pmaxClient)
	if err != nil {
		code := status.Code(err)
		if remoteSymID != "" && remoteVolumeID != "" &&
			(code == codes.NotFound || code == codes.FailedPrecondition) {
			csmlog.WithContext(ctx).Warnf("ControllerPublishVolume: local volume lookup failed for Metro volume %s (Array: %s, Volume: %s): %s; attempting remote array %s/%s",
				volID, p.symID, p.devID, err.Error(), remoteSymID, remoteVolumeID)
			remoteVol, rerr := pmaxClient.GetVolumeByID(ctx, remoteSymID, remoteVolumeID)
			if rerr != nil {
				csmlog.WithContext(ctx).Errorf("ControllerPublishVolume: remote volume lookup also failed (Array: %s, Volume: %s): %s",
					remoteSymID, remoteVolumeID, rerr.Error())
				return nil, err
			}
			// Restore parsed local IDs (GetVolumeByID zeroes them on error)
			// and keep them as-is so logging / SG / MV naming derived from the
			// node side is unchanged. We will refuse to publish on the local
			// array below by forcing skipLocalPublish=true.
			symID = p.symID
			devID = p.devID
			vol = remoteVol
			localVolumeMissing = true
			csmlog.WithContext(ctx).Infof("ControllerPublishVolume: recovered Metro volume %s via remote array %s/%s; local publish will be skipped",
				volID, remoteSymID, remoteVolumeID)
		} else {
			csmlog.WithContext(ctx).Error("GetVolumeByID Error: " + err.Error())
			return nil, err
		}
	}

	// log all parameters used in ControllerPublishVolume call
	fields := map[string]interface{}{
		"SymmetrixID":        symID,
		"VolumeId":           volID,
		"NodeId":             nodeID,
		"AccessMode":         am.Mode,
		"CSIRequestID":       reqID,
		"IsVsphereVolume":    p.s.opts.IsVsphereEnabled,
		"LocalVolumeMissing": localVolumeMissing,
	}
	csmlog.WithFields(fields).Info("Executing ControllerPublishVolume with following fields")
	// flag createNFSExport()
	isNVMETCP := false
	isISCSI := false
	// Check if node ID is present in cache
	nodeInCache := false
	cacheID := symID + ":" + nodeID
	// A BFS-adopted host carries an administrator-chosen name, so neither the
	// suffix-based protocol detection below nor the CSI naming convention applies.
	// A failed lookup must not be read as "not adopted" — that would silently
	// derive a host name that does not exist on the array.
	adoptedID, err := p.s.getAdoptedHostIDFromNodeLabels(ctx, symID, nodeID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if adoptedID != "" {
		csmlog.WithContext(ctx).Debugf("REQ ID: %s Node %s has adopted host %s on array %s, protocol is FC", reqID, nodeID, adoptedID, symID)
		isISCSI = false
		isNVMETCP = false
	} else if tempHostID, ok := nodeCache.Load(cacheID); ok {
		csmlog.WithContext(ctx).Debugf("REQ ID: %s Loaded nodeID: %s, hostID: %s from node cache",
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
		csmlog.WithContext(ctx).Debugf("REQ ID: %s nodeID: %s not present in node cache", reqID, nodeID)
		// For Metro volumes in a non-uniform setup, the node's host may exist on
		// the remote array instead of the local one. Try local first, then fall
		// back to the remote array so we can still determine the protocol and
		// allow the per-array skip logic below to skip the array where the host
		// is not present.
		csmlog.WithContext(ctx).Infof("REQ ID: %s detecting host protocol for node %s on local array %s", reqID, nodeID, symID)
		isNVMETCP, err = p.s.IsNodeNVMe(ctx, symID, nodeID, pmaxClient, adoptedID)
		localProtocolErr := err
		if err == nil && !isNVMETCP {
			isISCSI, err = p.s.IsNodeISCSI(ctx, symID, nodeID, pmaxClient, adoptedID)
			localProtocolErr = err
		}
		if localProtocolErr != nil && remoteSymID != "" {
			csmlog.WithContext(ctx).Infof("REQ ID: %s host for node %s not found on local array %s (%s); trying remote array %s (non-uniform Metro)",
				reqID, nodeID, symID, localProtocolErr.Error(), remoteSymID)
			// Adopted hosts are per-array, so look up for the remote array too
			remoteAdoptedID, _ := p.s.getAdoptedHostIDFromNodeLabels(ctx, remoteSymID, nodeID)
			isNVMETCP, err = p.s.IsNodeNVMe(ctx, remoteSymID, nodeID, pmaxClient, remoteAdoptedID)
			if err == nil && !isNVMETCP {
				isISCSI, err = p.s.IsNodeISCSI(ctx, remoteSymID, nodeID, pmaxClient, remoteAdoptedID)
			}
			if err != nil {
				csmlog.WithContext(ctx).Errorf("REQ ID: %s host for node %s not found on either local array %s or remote array %s",
					reqID, nodeID, symID, remoteSymID)
				return nil, status.Error(codes.NotFound, err.Error())
			}
			csmlog.WithContext(ctx).Infof("REQ ID: %s host for node %s located on remote array %s (isNVMETCP=%v, isISCSI=%v)",
				reqID, nodeID, remoteSymID, isNVMETCP, isISCSI)
		} else if localProtocolErr != nil {
			return nil, status.Error(codes.NotFound, localProtocolErr.Error())
		} else {
			csmlog.WithContext(ctx).Infof("REQ ID: %s host for node %s located on local array %s (isNVMETCP=%v, isISCSI=%v)",
				reqID, nodeID, symID, isNVMETCP, isISCSI)
		}
	}

	hostID, tgtStorageGroupID, tgtMaskingViewID := p.s.GetNVMETCPHostSGAndMVIDFromNodeID(nodeID)

	if !isNVMETCP {
		// Update the values, if NVME is false
		hostID, tgtStorageGroupID, tgtMaskingViewID = p.s.GetHostSGAndMVIDFromNodeID(nodeID, isISCSI)
	}
	if adoptedID != "" {
		// The CSI storage group and masking view keep the standard CSI naming; only
		// the host is the pre-existing BFS object.
		hostID = adoptedID
	}

	if !nodeInCache {
		// Update the map
		val, ok := nodeCache.LoadOrStore(cacheID, hostID)
		if !ok {
			csmlog.WithContext(ctx).Debugf("REQ ID: %s Added nodeID: %s, hostID: %s to node cache", reqID, nodeID, hostID)
		} else {
			csmlog.WithContext(ctx).Debugf("REQ ID: %s Some other goroutine added hostID: %s for node: %s to node cache",
				reqID, val.(string), nodeID)
			if hostID != val.(string) {
				csmlog.WithContext(ctx).Warnf("REQ ID: %s Mismatch between calculated value: %s and latest value: %s from node cache",
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

	// Non-uniform Metro: check if node has a host on the local array.
	// If not, skip local publish — the volume will only be published on the remote array.
	// Also skip the local publish when the local volume itself could not be
	// retrieved (preferred-site failure scenario).
	skipLocalPublish := false
	if localVolumeMissing {
		csmlog.WithContext(ctx).Infof("ControllerPublishVolume: local volume %s not available on array %s, skipping local publish; will publish on remote array %s",
			devID, symID, remoteSymID)
		skipLocalPublish = true
	}
	if !skipLocalPublish && remoteSymID != "" {
		csmlog.WithContext(ctx).Infof("ControllerPublishVolume: checking host %s presence on local array %s for node %s (Metro volume, remoteSymID=%s)",
			hostID, symID, nodeID, remoteSymID)
		skip, _ := p.s.shouldSkipRemotePublish(ctx, pmaxClient, symID, hostID)
		if skip {
			csmlog.WithContext(ctx).Infof("ControllerPublishVolume: Node %s has no host %s on local array %s (non-uniform Metro), skipping local publish; will publish on remote array %s",
				nodeID, hostID, symID, remoteSymID)
			skipLocalPublish = true
		}
	}

	var ctrlPubRes *csi.ControllerPublishVolumeResponse
	if !skipLocalPublish {
		csmlog.WithContext(ctx).Infof("ControllerPublishVolume: publishing volume %s on local array %s (MV: %s, SG: %s, Host: %s)",
			devID, symID, tgtMaskingViewID, tgtStorageGroupID, hostID)
		var ctrlPubErr error
		ctrlPubRes, ctrlPubErr = p.s.publishVolume(ctx, publishContext, tgtStorageGroupID, hostID, symID, symID, tgtMaskingViewID, devID, reqID, volumeName, am, pmaxClient, true)
		if ctrlPubErr != nil {
			return nil, ctrlPubErr
		}
	}

	if remoteSymID != "" && remoteVolumeID != "" {
		// Check if node has a host on the remote array before attempting remote publish
		csmlog.WithContext(ctx).Infof("ControllerPublishVolume: checking host %s presence on remote array %s for node %s",
			hostID, remoteSymID, nodeID)
		skip, _ := p.s.shouldSkipRemotePublish(ctx, pmaxClient, remoteSymID, hostID)
		if skip {
			csmlog.WithContext(ctx).Infof("ControllerPublishVolume: Node %s has no host %s on remote array %s (non-uniform Metro), skipping remote publish",
				nodeID, hostID, remoteSymID)
			if skipLocalPublish {
				// Neither array has the host — surface the failure rather than silently
				// returning success with no masking view.
				csmlog.WithContext(ctx).Errorf("ControllerPublishVolume: Node %s has no host %s on either local array %s or remote array %s; no masking view will be created",
					nodeID, hostID, symID, remoteSymID)
				return nil, status.Errorf(codes.NotFound,
					"node %s has no host registered on either array %s or %s; cannot publish volume %s",
					nodeID, symID, remoteSymID, devID)
			}
			return ctrlPubRes, nil
		}
		csmlog.WithContext(ctx).Infof("ControllerPublishVolume: publishing volume %s on remote array %s (MV: %s, SG: %s, Host: %s, remoteDevID: %s)",
			devID, remoteSymID, tgtMaskingViewID, tgtStorageGroupID, hostID, remoteVolumeID)
		remoteVol, err := pmaxClient.GetVolumeByID(ctx, remoteSymID, remoteVolumeID)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "PublishVolume: Could not retrieve remote volume: (%s)", err.Error())
		}
		if !localVolumeMissing && strings.Compare(remoteVol.EffectiveWWN, vol.EffectiveWWN) != 0 {
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
		csmlog.WithContext(ctx).Debugf("remote-vol: %#v, error: %#v", remoteVol, err)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "PublishVolume: Could not retrieve remote volume: (%s)", err.Error())
		}
		publishContext[PublishContextDeviceWWN] = remoteVol.EffectiveWWN
		return p.s.publishVolume(ctx, publishContext, tgtStorageGroupID, hostID, symID, remoteSymID, tgtMaskingViewID, remoteVolumeID, reqID, volumeName, am, pmaxClient, skipLocalPublish)
	}
	return ctrlPubRes, nil
}
