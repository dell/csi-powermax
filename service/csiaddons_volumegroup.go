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
	"sort"
	"strconv"

	"github.com/dell/csmlog"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/container-storage-interface/spec/lib/go/csi"
	volumegrouprpc "github.com/csi-addons/spec/lib/go/volumegroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CSIAddonsVolumeGroupServer implements the CSI-Addons VolumeGroup
// Controller service for the PowerMax driver. A CSI-Addons VolumeGroup
// maps 1:1 to a PowerMax SRDF-protected Storage Group, which is also
// the natural granularity for SRDF operations. The server is deliberately
// separate from the existing CSM Replication code path (see
// service/replication.go) so that the new feature can be enabled or
// disabled via X_CSI_CSIADDONS_REPLICATION_ENABLED without affecting
// existing deployments.
type CSIAddonsVolumeGroupServer struct {
	volumegrouprpc.UnimplementedControllerServer
	service *service
}

// NewCSIAddonsVolumeGroupServer constructs a CSIAddonsVolumeGroupServer.
func NewCSIAddonsVolumeGroupServer(svc *service) *CSIAddonsVolumeGroupServer {
	return &CSIAddonsVolumeGroupServer{service: svc}
}

// RegisterCSIAddonsVolumeGroupServer registers the CSI-Addons volume
// group server with the given gRPC server.
func RegisterCSIAddonsVolumeGroupServer(server *grpc.Server, srv *CSIAddonsVolumeGroupServer) {
	volumegrouprpc.RegisterControllerServer(server, srv)
}

// CreateVolumeGroup creates (or returns the existing) PowerMax Storage
// Group for the supplied volumes. SRDF protection is NOT established
// here; the CSI-Addons controller will call EnableVolumeReplication as
// a separate step to set up replication.
//
// Behavior:
//   - When the volumes belong to the configured source (R1) array the
//     driver creates the SG and adds the volumes.
//   - When the volumes belong to the configured target (R2) array (e.g.
//     a Ramen reconcile on the secondary cluster) the driver simply
//     returns the existing SG that SRDF has already auto-created. This
//     keeps the secondary-side reconcile idempotent.
//
// The RPC is fully idempotent: calling it repeatedly with the same
// (Name, VolumeIds, Parameters) returns the same VolumeGroup.
func (s *CSIAddonsVolumeGroupServer) CreateVolumeGroup(ctx context.Context,
	req *volumegrouprpc.CreateVolumeGroupRequest,
) (*volumegrouprpc.CreateVolumeGroupResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons CreateVolumeGroup called: name=%q volumeIDs=%v", req.GetName(), req.GetVolumeIds())

	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "CreateVolumeGroup: volume group name is required")
	}
	if len(req.GetVolumeIds()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "CreateVolumeGroup: at least one volume id is required")
	}

	params, err := parseCSIAddonsReplicationParams(req.GetParameters())
	if err != nil {
		return nil, err
	}

	// Derive the local Symmetrix ID from the first volume id. All volume
	// IDs must live on the same array for SRDF to make sense.
	localSymID, err := s.service.extractSymIDFromVolumeID(req.GetVolumeIds()[0])
	if err != nil {
		return nil, err
	}
	devIDs := make([]string, 0, len(req.GetVolumeIds()))
	for _, vid := range req.GetVolumeIds() {
		sym, derr := s.service.extractSymIDFromVolumeID(vid)
		if derr != nil {
			return nil, derr
		}
		if sym != localSymID && sym != params.remoteSymID {
			return nil, status.Errorf(codes.InvalidArgument, "CreateVolumeGroup: volume %q is on array %q, expected %q or remote %q", vid, sym, localSymID, params.remoteSymID)
		}
		dev, derr := s.service.extractDevIDFromVolumeID(vid)
		if derr != nil {
			return nil, derr
		}
		devIDs = append(devIDs, dev)
	}

	// If the volume IDs all belong to the remote (R2) array this call
	// is a secondary-side reconcile. Just return what is already on the
	// array.
	if localSymID == params.remoteSymID {
		csmlog.WithContext(ctx).Infof("CreateVolumeGroup: volumes are on remote (R2) array %s; returning existing SG", localSymID)
		return s.getExistingVolumeGroupResponse(ctx, localSymID, params)
	}

	pmaxClient, err := s.service.GetPowerMaxClient(localSymID, params.remoteSymID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "CreateVolumeGroup: failed to get PowerMax client: %v", err)
	}

	// Resolve the RDF group number. When an explicit number is provided
	// via the VolumeGroupReplicationClass parameter it is used directly.
	// Otherwise the driver auto-discovers (or creates) an RDF group for
	// the local/remote array pair and replication mode, reusing the same
	// logic as the existing CSM Replication path (service/replication.go).
	rdfGrpNo := params.rdfGroupNumber
	if rdfGrpNo == "" {
		localRDFGrpNo, _, rdferr := s.service.GetOrCreateRDFGroup(
			ctx, localSymID, params.remoteSymID, params.replicationMode, params.namespace, pmaxClient)
		if rdferr != nil {
			return nil, status.Errorf(codes.Internal, "CreateVolumeGroup: failed to get/create RDF group: %v", rdferr)
		}
		if localRDFGrpNo == "" {
			return nil, status.Error(codes.Unavailable, "CreateVolumeGroup: could not determine RDF group number for the given array pair and replication mode")
		}
		rdfGrpNo = localRDFGrpNo
		csmlog.WithContext(ctx).Infof("CreateVolumeGroup: auto-discovered RDF group %s for array pair %s -> %s (%s)",
			rdfGrpNo, localSymID, params.remoteSymID, params.replicationMode)
	}

	sgName := buildCSIAddonsSGName(params.volumeGroupPrefix, params.namespace, rdfGrpNo, params.replicationMode)

	// Serialize SG / SRDF mutations for this storage group.
	reqID := csiAddonsReqID(ctx)
	lockHandle := csiAddonsSGLockHandle(sgName, localSymID)
	lockNum, err := RequestLock(lockHandle, reqID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire lock: %v", err)
	}
	defer ReleaseLock(lockHandle, reqID, lockNum)

	// Step 1: ensure the local Storage Group exists. The SG is created
	// with SRP "None" and an empty service level so that it does NOT
	// carry a FAST policy. The volumes being added already belong to
	// their original FAST-enabled SG; a second FAST-enabled SG would
	// violate the PowerMax constraint "A device cannot belong to more
	// than one storage group in use by FAST". This matches the existing
	// CSM Replication path in controller.go (see CreateStorageGroup
	// with SRP "None").
	if _, gerr := pmaxClient.GetStorageGroup(ctx, localSymID, sgName); gerr != nil {
		if _, cerr := pmaxClient.CreateStorageGroup(
			ctx, localSymID, sgName, "None", "", false, nil); cerr != nil {
			return nil, status.Errorf(codes.Internal, "CreateVolumeGroup: failed to create storage group %q: %v", sgName, cerr)
		}
	}

	// Step 2: add only devices not already present in the SG. Unisphere
	// rejects the entire add request when any of the volumes are already
	// members (none of the remaining volumes get added either), so we must
	// filter them out for idempotency (e.g. a retry after partial add).
	// This mirrors the pre-check pattern used in storage_group_svc.go
	if len(devIDs) > 0 {
		existing, _ := pmaxClient.GetVolumeIDListInStorageGroup(ctx, localSymID, sgName)
		existSet := make(map[string]struct{}, len(existing))
		for _, id := range existing {
			existSet[id] = struct{}{}
		}
		var toAdd []string
		for _, id := range devIDs {
			if _, ok := existSet[id]; !ok {
				toAdd = append(toAdd, id)
			}
		}
		if len(toAdd) > 0 {
			if aerr := pmaxClient.AddVolumesToStorageGroup(ctx, localSymID, sgName, true, toAdd...); aerr != nil {
				return nil, status.Errorf(codes.Internal, "CreateVolumeGroup: failed to add volumes to storage group %q: %v", sgName, aerr)
			}
		} else {
			csmlog.WithContext(ctx).Infof("CreateVolumeGroup: all volumes already present in SG %q; skipping add", sgName)
		}
	}

	return s.buildVolumeGroupResponse(ctx, pmaxClient, localSymID, sgName, rdfGrpNo, params, req.GetVolumeIds()), nil
}

// getExistingVolumeGroupResponse returns the VolumeGroup response for a
// secondary-side reconcile, where SRDF has already created the R2 SG.
func (s *CSIAddonsVolumeGroupServer) getExistingVolumeGroupResponse(
	ctx context.Context, symID string, params *csiAddonsReplicationParams,
) (*volumegrouprpc.CreateVolumeGroupResponse, error) {
	pmaxClient, err := s.service.GetPowerMaxClient(symID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get PowerMax client: %v", err)
	}
	rdfGrpNo := params.rdfGroupNumber
	if rdfGrpNo == "" {
		return nil, status.Error(codes.InvalidArgument, "CreateVolumeGroup: rdfGroupNumber is required for secondary-side reconcile")
	}
	sgName := buildCSIAddonsSGName(params.volumeGroupPrefix, params.namespace, rdfGrpNo, params.replicationMode)
	return s.buildVolumeGroupResponse(ctx, pmaxClient, symID, sgName, rdfGrpNo, params, nil), nil
}

// buildVolumeGroupResponse looks up the storage group and returns the
// VolumeGroup proto populated with the current volume membership and
// replication context.
func (s *CSIAddonsVolumeGroupServer) buildVolumeGroupResponse(
	ctx context.Context, pmaxClient pmax.Pmax,
	symID, sgName, rdfGrpNo string,
	params *csiAddonsReplicationParams,
	preferredVolumeIDs []string,
) *volumegrouprpc.CreateVolumeGroupResponse {
	// Enrich the context from the storage group name when the caller did
	// not supply replication parameters (e.g. ModifyVolumeGroupMembership
	// and ControllerGetVolumeGroup operate purely off the volume_group_id).
	// The CSI-Addons SG name encodes the replication mode and RDF group, so
	// the VolumeGroupContext can be populated without an extra array call.
	mode := params.replicationMode
	if parsedRdf, parsedMode, ok := parseCSIAddonsSGName(sgName); ok {
		if mode == "" {
			mode = parsedMode
		}
		if rdfGrpNo == "" {
			rdfGrpNo = parsedRdf
		}
	}

	vgCtx := map[string]string{
		"storageGroup":          sgName,
		"sourceArrayID":         symID,
		"rdfGroupNumber":        rdfGrpNo,
		CSIAddonsManagedByLabel: CSIAddonsManagedByValue,
	}
	if mode != "" {
		vgCtx[CSIAddonsParamReplicationMode] = mode
	}
	if params.remoteSymID != "" {
		vgCtx[CSIAddonsParamRemoteSystem] = params.remoteSymID
	}
	vg := &volumegrouprpc.VolumeGroup{
		VolumeGroupId:      encodeVolumeGroupID(symID, sgName, rdfGrpNo),
		VolumeGroupContext: vgCtx,
	}

	// Best-effort enumeration of current members. Failures here are
	// non-fatal; we still want to return the volume group identifier.
	if devIDs, err := pmaxClient.GetVolumeIDListInStorageGroup(ctx, symID, sgName); err == nil {
		volumes := make([]*csi.Volume, 0, len(devIDs))
		for _, devID := range devIDs {
			volumes = append(volumes, &csi.Volume{
				VolumeId: fmt.Sprintf("%s-%s", symID, devID),
			})
		}
		vg.Volumes = volumes
	} else if len(preferredVolumeIDs) > 0 {
		// Fall back to echoing the requested volume IDs.
		volumes := make([]*csi.Volume, 0, len(preferredVolumeIDs))
		for _, vid := range preferredVolumeIDs {
			volumes = append(volumes, &csi.Volume{VolumeId: vid})
		}
		vg.Volumes = volumes
	}

	return &volumegrouprpc.CreateVolumeGroupResponse{VolumeGroup: vg}
}

// DeleteVolumeGroup tears down a CSI-Addons managed storage group.
//
// On the source (R1) array the driver suspends the SRDF session,
// breaks SRDF device pairs via RemoveVolumesFromProtectedStorageGroup,
// deletes the local SG and cleans up the orphaned remote SG. On the
// target (R2) array DeleteVolumeGroup is a no-op.
//
// The RPC is idempotent: if the SG does not exist, returns success.
func (s *CSIAddonsVolumeGroupServer) DeleteVolumeGroup(ctx context.Context,
	req *volumegrouprpc.DeleteVolumeGroupRequest,
) (*volumegrouprpc.DeleteVolumeGroupResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons DeleteVolumeGroup called: vgID=%q", req.GetVolumeGroupId())

	symID, sgName, rdfGrpNo, err := decodeVolumeGroupID(req.GetVolumeGroupId())
	if err != nil {
		return nil, err
	}

	pmaxClient, err := s.service.GetPowerMaxClient(symID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "DeleteVolumeGroup: failed to get PowerMax client: %v", err)
	}

	reqID := csiAddonsReqID(ctx)
	lockHandle := csiAddonsSGLockHandle(sgName, symID)
	lockNum, err := RequestLock(lockHandle, reqID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire lock: %v", err)
	}
	defer ReleaseLock(lockHandle, reqID, lockNum)

	// If the SG does not exist treat as success (idempotent delete).
	sg, gerr := pmaxClient.GetStorageGroup(ctx, symID, sgName)
	if gerr != nil || sg == nil {
		csmlog.WithContext(ctx).Infof("DeleteVolumeGroup: SG %q not found on %q; treating as success", sgName, symID)
		return &volumegrouprpc.DeleteVolumeGroupResponse{}, nil
	}

	// If the SG is SRDF-protected, tear down replication fully.
	psg, _ := pmaxClient.GetProtectedStorageGroup(ctx, symID, sgName)
	if psg != nil && psg.Rdf {
		return s.deleteProtectedVolumeGroup(ctx, pmaxClient, symID, sgName, rdfGrpNo, psg)
	}

	// Non-protected SG: remove volumes and delete.
	return s.deleteUnprotectedSG(ctx, pmaxClient, symID, sgName)
}

// deleteProtectedVolumeGroup handles full SRDF teardown: suspend,
// break device pairs, delete local SG and clean up the remote SG.
func (s *CSIAddonsVolumeGroupServer) deleteProtectedVolumeGroup(
	ctx context.Context, pmaxClient pmax.Pmax,
	symID, sgName, rdfGrpNo string, psg *types.RDFStorageGroup,
) (*volumegrouprpc.DeleteVolumeGroupResponse, error) {
	effectiveRdfGrp, rerr := resolveEffectiveRDFGroup(rdfGrpNo, psg)
	if rerr != nil {
		return nil, rerr
	}

	// R2 side is a no-op; the source-side delete handles cleanup.
	var state string
	isR1 := true
	if sgRDF, sErr := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, effectiveRdfGrp); sErr == nil && sgRDF != nil {
		st, r1, _, _ := csiAddonsSRDFPersonality(sgRDF)
		state = st
		isR1 = r1
	}
	if !isR1 {
		csmlog.WithContext(ctx).Infof("DeleteVolumeGroup: SG %q on %q is R2; no-op", sgName, symID)
		return &volumegrouprpc.DeleteVolumeGroupResponse{}, nil
	}

	// Discover remote array from the RDF group.
	rdfInfo, rErr := pmaxClient.GetRDFGroupByID(ctx, symID, effectiveRdfGrp)
	if rErr != nil {
		return nil, status.Errorf(codes.Internal, "DeleteVolumeGroup: failed to get RDF group %s info: %v", effectiveRdfGrp, rErr)
	}
	remoteSymID := rdfInfo.RemoteSymmetrix

	// Re-init client so it can operate on both arrays.
	pmaxClient, err := s.service.GetPowerMaxClient(symID, remoteSymID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "DeleteVolumeGroup: failed to get PowerMax client for both arrays: %v", err)
	}

	// Suspend the SRDF session before removing volumes. Skip if already
	// in an inactive state (Suspended, Split) to stay idempotent when
	// DisableVolumeReplication was called before DeleteVolumeGroup.
	if state != Suspended && state != Split {
		if serr := pmaxClient.ExecuteReplicationActionOnSG(
			ctx, symID, csiAddonsActionSuspend, sgName, effectiveRdfGrp, true, false, false); serr != nil {
			return nil, status.Errorf(codes.Internal, "DeleteVolumeGroup: failed to suspend SRDF on SG %q: %v", sgName, serr)
		}
	} else {
		csmlog.WithContext(ctx).Infof("DeleteVolumeGroup: SG %q already %s; skipping suspend", sgName, state)
	}

	// Remove volumes via the protected-SG API to break SRDF device pairs.
	if devIDs, lerr := pmaxClient.GetVolumeIDListInStorageGroup(ctx, symID, sgName); lerr == nil && len(devIDs) > 0 {
		if _, perr := pmaxClient.RemoveVolumesFromProtectedStorageGroup(
			ctx, symID, sgName, remoteSymID, sgName, true, devIDs...); perr != nil {
			return nil, status.Errorf(codes.Internal, "DeleteVolumeGroup: failed to remove protected volumes from %q: %v", sgName, perr)
		}
	}

	// Delete local SG.
	if derr := pmaxClient.DeleteStorageGroup(ctx, symID, sgName); derr != nil {
		return nil, status.Errorf(codes.Internal, "DeleteVolumeGroup: failed to delete local SG %q: %v", sgName, derr)
	}

	// Clean up the now-empty remote SG (best-effort).
	if derr := pmaxClient.DeleteStorageGroup(ctx, remoteSymID, sgName); derr != nil {
		csmlog.WithContext(ctx).Warnf("DeleteVolumeGroup: failed to delete remote SG %q on %q: %v (may already be removed)", sgName, remoteSymID, derr)
	}

	return &volumegrouprpc.DeleteVolumeGroupResponse{}, nil
}

// deleteUnprotectedSG removes volumes and deletes an SG that has no
// SRDF protection.
func (s *CSIAddonsVolumeGroupServer) deleteUnprotectedSG(
	ctx context.Context, pmaxClient pmax.Pmax, symID, sgName string,
) (*volumegrouprpc.DeleteVolumeGroupResponse, error) {
	if devIDs, lerr := pmaxClient.GetVolumeIDListInStorageGroup(ctx, symID, sgName); lerr == nil && len(devIDs) > 0 {
		if _, rerr := pmaxClient.RemoveVolumesFromStorageGroup(ctx, symID, sgName, true, devIDs...); rerr != nil {
			return nil, status.Errorf(codes.Internal, "DeleteVolumeGroup: failed to remove volumes from %q: %v", sgName, rerr)
		}
	}
	if derr := pmaxClient.DeleteStorageGroup(ctx, symID, sgName); derr != nil {
		return nil, status.Errorf(codes.Internal, "DeleteVolumeGroup: failed to delete SG %q: %v", sgName, derr)
	}
	return &volumegrouprpc.DeleteVolumeGroupResponse{}, nil
}

// ModifyVolumeGroupMembership adds and removes volumes from a CSI-Addons
// managed storage group. Membership changes are only honored on the
// source (R1) side; SRDF propagates the changes to R2 automatically.
//
// The set of volumes in the request is treated as the desired state:
// volumes that are present in the SG but not in the request are
// removed, and volumes that are in the request but not in the SG are
// added. This makes the RPC idempotent.
func (s *CSIAddonsVolumeGroupServer) ModifyVolumeGroupMembership(ctx context.Context,
	req *volumegrouprpc.ModifyVolumeGroupMembershipRequest,
) (*volumegrouprpc.ModifyVolumeGroupMembershipResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons ModifyVolumeGroupMembership called: vgID=%q desiredVolumes=%v",
		req.GetVolumeGroupId(), req.GetVolumeIds())

	symID, sgName, rdfGrpNo, err := decodeVolumeGroupID(req.GetVolumeGroupId())
	if err != nil {
		return nil, err
	}

	pmaxClient, err := s.service.GetPowerMaxClient(symID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ModifyVolumeGroupMembership: failed to get PowerMax client: %v", err)
	}

	reqID := csiAddonsReqID(ctx)
	lockHandle := csiAddonsSGLockHandle(sgName, symID)
	lockNum, err := RequestLock(lockHandle, reqID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire lock: %v", err)
	}
	defer ReleaseLock(lockHandle, reqID, lockNum)

	// Check if SG is SRDF-protected and resolve personality.
	var isProtected bool
	var remoteSymID string
	if psg, _ := pmaxClient.GetProtectedStorageGroup(ctx, symID, sgName); psg != nil && psg.Rdf && rdfGrpNo != "" {
		if sgRDF, sErr := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, rdfGrpNo); sErr == nil && sgRDF != nil {
			_, isR1, _, _ := csiAddonsSRDFPersonality(sgRDF)
			if !isR1 {
				return nil, status.Error(codes.FailedPrecondition, "ModifyVolumeGroupMembership: cannot modify membership on R2 side; perform changes on R1 (source) cluster")
			}
		}
		// Resolve remote array so volume removals can break SRDF pairs.
		if rdfInfo, rErr := pmaxClient.GetRDFGroupByID(ctx, symID, rdfGrpNo); rErr == nil {
			remoteSymID = rdfInfo.RemoteSymmetrix
			isProtected = true
			if c, cErr := s.service.GetPowerMaxClient(symID, remoteSymID); cErr == nil {
				pmaxClient = c
			}
		}
	}

	// Compute the desired set of device IDs from the request.
	desired := make(map[string]struct{}, len(req.GetVolumeIds()))
	for _, vid := range req.GetVolumeIds() {
		dev, derr := s.service.extractDevIDFromVolumeID(vid)
		if derr != nil {
			return nil, derr
		}
		desired[dev] = struct{}{}
	}

	current, lerr := pmaxClient.GetVolumeIDListInStorageGroup(ctx, symID, sgName)
	if lerr != nil {
		return nil, status.Errorf(codes.Internal, "ModifyVolumeGroupMembership: failed to list current members of %q: %v", sgName, lerr)
	}
	currentSet := make(map[string]struct{}, len(current))
	for _, devID := range current {
		currentSet[devID] = struct{}{}
	}

	var toAdd, toRemove []string
	for devID := range desired {
		if _, ok := currentSet[devID]; !ok {
			toAdd = append(toAdd, devID)
		}
	}
	for devID := range currentSet {
		if _, ok := desired[devID]; !ok {
			toRemove = append(toRemove, devID)
		}
	}

	if len(toAdd) > 0 {
		if isProtected {
			// Use protected-SG API so Unisphere creates SRDF pairs for the new volumes.
			if aerr := pmaxClient.AddVolumesToProtectedStorageGroup(
				ctx, symID, sgName, remoteSymID, sgName, true, toAdd...); aerr != nil {
				return nil, status.Errorf(codes.Internal, "ModifyVolumeGroupMembership: failed to add protected volumes %v to %q: %v", toAdd, sgName, aerr)
			}
		} else {
			if aerr := pmaxClient.AddVolumesToStorageGroup(ctx, symID, sgName, true, toAdd...); aerr != nil {
				return nil, status.Errorf(codes.Internal, "ModifyVolumeGroupMembership: failed to add %v to %q: %v", toAdd, sgName, aerr)
			}
		}
	}
	if len(toRemove) > 0 {
		if isProtected {
			// Use protected-SG API to break SRDF device pairs.
			if _, rerr := pmaxClient.RemoveVolumesFromProtectedStorageGroup(
				ctx, symID, sgName, remoteSymID, sgName, true, toRemove...); rerr != nil {
				return nil, status.Errorf(codes.Internal, "ModifyVolumeGroupMembership: failed to remove protected volumes %v from %q: %v", toRemove, sgName, rerr)
			}
		} else {
			if _, rerr := pmaxClient.RemoveVolumesFromStorageGroup(ctx, symID, sgName, true, toRemove...); rerr != nil {
				return nil, status.Errorf(codes.Internal, "ModifyVolumeGroupMembership: failed to remove %v from %q: %v", toRemove, sgName, rerr)
			}
		}
	}

	params := &csiAddonsReplicationParams{
		remoteSymID:       "",
		replicationMode:   "",
		volumeGroupPrefix: defaultVolumeGroupPrefix,
	}
	// Best-effort restore of parameters from the encoded id so that the
	// VolumeGroup response is meaningful.
	resp := s.buildVolumeGroupResponse(ctx, pmaxClient, symID, sgName, rdfGrpNo, params, req.GetVolumeIds())
	return &volumegrouprpc.ModifyVolumeGroupMembershipResponse{VolumeGroup: resp.VolumeGroup}, nil
}

// ControllerGetVolumeGroup returns the current PowerMax storage group
// state for the supplied CSI-Addons volume group id.
func (s *CSIAddonsVolumeGroupServer) ControllerGetVolumeGroup(ctx context.Context,
	req *volumegrouprpc.ControllerGetVolumeGroupRequest,
) (*volumegrouprpc.ControllerGetVolumeGroupResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons ControllerGetVolumeGroup called: vgID=%q", req.GetVolumeGroupId())

	symID, sgName, rdfGrpNo, err := decodeVolumeGroupID(req.GetVolumeGroupId())
	if err != nil {
		return nil, err
	}
	pmaxClient, err := s.service.GetPowerMaxClient(symID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ControllerGetVolumeGroup: failed to get PowerMax client: %v", err)
	}
	sg, gerr := pmaxClient.GetStorageGroup(ctx, symID, sgName)
	if gerr != nil || sg == nil {
		return nil, status.Errorf(codes.NotFound, "ControllerGetVolumeGroup: storage group %q not found on %q", sgName, symID)
	}
	params := &csiAddonsReplicationParams{
		volumeGroupPrefix: defaultVolumeGroupPrefix,
	}
	// Try to populate replication context fields from the protected SG.
	// Avoid picking an arbitrary RDF group when the SG spans more than one;
	// in that ambiguous case leave rdfGrpNo empty (it is also derivable from
	// the SG name in buildVolumeGroupResponse).
	if psg, perr := pmaxClient.GetProtectedStorageGroup(ctx, symID, sgName); perr == nil && psg != nil && psg.Rdf {
		if rdfGrpNo == "" {
			if eff, rerr := resolveEffectiveRDFGroup(rdfGrpNo, psg); rerr == nil {
				rdfGrpNo = eff
			}
		}
	}
	resp := s.buildVolumeGroupResponse(ctx, pmaxClient, symID, sgName, rdfGrpNo, params, nil)
	return &volumegrouprpc.ControllerGetVolumeGroupResponse{VolumeGroup: resp.VolumeGroup}, nil
}

// ListVolumeGroups enumerates the CSI-Addons managed protected Storage
// Groups across every managed PowerMax array. Only SGs created by the
// CSI-Addons code path (identified by the csi-rep-sg-addons- name prefix)
// are returned, so CSM Replication SGs and ordinary application SGs are
// not surfaced to the orchestrator.
func (s *CSIAddonsVolumeGroupServer) ListVolumeGroups(ctx context.Context,
	req *volumegrouprpc.ListVolumeGroupsRequest,
) (*volumegrouprpc.ListVolumeGroupsResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons ListVolumeGroups called: %+v", req)

	maxEntries := int(req.GetMaxEntries())
	if maxEntries < 0 {
		return nil, status.Error(codes.InvalidArgument, "ListVolumeGroups: max_entries cannot be negative")
	}

	entries := make([]*volumegrouprpc.ListVolumeGroupsResponse_Entry, 0)
	for _, symID := range s.service.opts.ManagedArrays {
		pmaxClient, err := s.service.GetPowerMaxClient(symID)
		if err != nil {
			csmlog.WithContext(ctx).Warnf("ListVolumeGroups: skipping array %q: %v", symID, err)
			continue
		}
		sgList, lerr := pmaxClient.GetStorageGroupIDList(ctx, symID, csiAddonsManagedSGPrefix, true)
		if lerr != nil || sgList == nil {
			csmlog.WithContext(ctx).Warnf("ListVolumeGroups: could not list storage groups on %q: %v", symID, lerr)
			continue
		}
		// Sort within the array so the overall ordering is deterministic,
		// which is required for stable pagination across calls.
		sgNames := append([]string(nil), sgList.StorageGroupIDs...)
		sort.Strings(sgNames)
		for _, sgName := range sgNames {
			rdfGrpNo, mode, ok := parseCSIAddonsSGName(sgName)
			if !ok {
				continue
			}
			vg := &volumegrouprpc.VolumeGroup{
				VolumeGroupId: encodeVolumeGroupID(symID, sgName, rdfGrpNo),
				VolumeGroupContext: map[string]string{
					"storageGroup":                sgName,
					"sourceArrayID":               symID,
					"rdfGroupNumber":              rdfGrpNo,
					CSIAddonsParamReplicationMode: mode,
					CSIAddonsManagedByLabel:       CSIAddonsManagedByValue,
				},
			}
			// Best-effort volume enumeration; failures are non-fatal.
			if devIDs, derr := pmaxClient.GetVolumeIDListInStorageGroup(ctx, symID, sgName); derr == nil {
				vols := make([]*csi.Volume, 0, len(devIDs))
				for _, devID := range devIDs {
					vols = append(vols, &csi.Volume{VolumeId: fmt.Sprintf("%s-%s", symID, devID)})
				}
				vg.Volumes = vols
			}
			entries = append(entries, &volumegrouprpc.ListVolumeGroupsResponse_Entry{VolumeGroup: vg})
		}
	}

	// Apply pagination (starting_token is an offset into the deterministic
	// list; next_token is the offset of the next page).
	start := 0
	if token := req.GetStartingToken(); token != "" {
		i, perr := strconv.Atoi(token)
		if perr != nil || i < 0 || i > len(entries) {
			return nil, status.Errorf(codes.Aborted,
				"ListVolumeGroups: invalid starting_token %q", token)
		}
		start = i
	}
	end := len(entries)
	nextToken := ""
	if maxEntries > 0 && start+maxEntries < len(entries) {
		end = start + maxEntries
		nextToken = strconv.Itoa(end)
	}
	return &volumegrouprpc.ListVolumeGroupsResponse{
		Entries:   entries[start:end],
		NextToken: nextToken,
	}, nil
}
