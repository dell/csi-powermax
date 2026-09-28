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
	"strconv"
	"time"

	"github.com/dell/csmlog"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	csiaddonsreplication "github.com/csi-addons/spec/lib/go/replication"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// CSIAddonsReplicationServer implements the CSI-Addons Replication
// Controller service for the PowerMax driver. All RPCs operate at the
// VolumeGroup (PowerMax SRDF-protected Storage Group) granularity. The
// server intentionally bypasses the legacy CSM Replication helpers in
// service/replication.go (Failover, Suspend, Resume, ...) and instead
// drives SRDF directly through the gopowermax SDK. This keeps the new
// CSI-Addons feature flag (X_CSI_CSIADDONS_REPLICATION_ENABLED)
// completely isolated from existing replication behaviour.
//
// Scope:
//   - SRDF/S (synchronous) and SRDF/A (asynchronous) are supported.
//   - SRDF/Metro is explicitly rejected with an InvalidArgument error
//     carrying the canonical message "SRDF Metro is not supported by
//     CSI-Addons".
//   - Only VolumeGroup-scoped RPCs are implemented. Volume-scoped
//     calls (deprecated VolumeId / ReplicationSource_VolumeSource)
//     return InvalidArgument.
type CSIAddonsReplicationServer struct {
	csiaddonsreplication.UnimplementedControllerServer
	service *service
}

// NewCSIAddonsReplicationServer constructs a CSIAddonsReplicationServer.
func NewCSIAddonsReplicationServer(svc *service) *CSIAddonsReplicationServer {
	return &CSIAddonsReplicationServer{service: svc}
}

// RegisterCSIAddonsReplicationServer registers the CSI-Addons replication
// server with the given gRPC server.
func RegisterCSIAddonsReplicationServer(server *grpc.Server, srv *CSIAddonsReplicationServer) {
	csiaddonsreplication.RegisterControllerServer(server, srv)
}

// resolveVGFromSource returns the encoded volume group id from a CSI-Addons
// ReplicationSource. Volume-level sources are explicitly rejected
// because the PowerMax CSI-Addons integration operates at the Storage
// Group / VolumeGroup granularity.
func (s *CSIAddonsReplicationServer) resolveVGFromSource(src *csiaddonsreplication.ReplicationSource) (string, error) {
	if src == nil {
		return "", status.Error(codes.InvalidArgument, "replication_source is required")
	}
	if vg := src.GetVolumegroup(); vg != nil && vg.GetVolumeGroupId() != "" {
		return vg.GetVolumeGroupId(), nil
	}
	if v := src.GetVolume(); v != nil && v.GetVolumeId() != "" {
		return "", status.Error(codes.InvalidArgument, "volume-level replication is not supported; use volume group replication")
	}
	return "", status.Error(codes.InvalidArgument, "replication_source.volumegroup is required")
}

// EnableVolumeReplication establishes SRDF protection on the PowerMax
// Storage Group identified by the volume group id in req. The operation
// is idempotent: if the SG is already SRDF-protected with a matching
// mode the call succeeds without issuing any change.
func (s *CSIAddonsReplicationServer) EnableVolumeReplication(ctx context.Context,
	req *csiaddonsreplication.EnableVolumeReplicationRequest,
) (*csiaddonsreplication.EnableVolumeReplicationResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons EnableVolumeReplication called: %+v", req)

	vgID, err := s.resolveVGFromSource(req.GetReplicationSource())
	if err != nil {
		return nil, err
	}
	params, err := parseCSIAddonsReplicationParams(req.GetParameters())
	if err != nil {
		return nil, err
	}
	symID, sgName, rdfGrpNo, err := decodeVolumeGroupID(vgID)
	if err != nil {
		return nil, err
	}
	// The RDF group number must be encoded in the volume_group_id by
	// CreateVolumeGroup. Auto-discovery is only performed in
	// CreateVolumeGroup so that it happens exactly once.
	if rdfGrpNo == "" {
		return nil, status.Error(codes.InvalidArgument, "EnableVolumeReplication: rdfGroupNumber must be encoded in the volume_group_id (set by CreateVolumeGroup)")
	}

	pmaxClient, err := s.service.GetPowerMaxClient(symID, params.remoteSymID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "EnableVolumeReplication: failed to get PowerMax client: %v", err)
	}

	// Serialize SRDF / SG mutations for this storage group so concurrent
	// CSI-Addons (or CSM Replication) requests cannot race.
	reqID := csiAddonsReqID(ctx)
	lockHandle := csiAddonsSGLockHandle(sgName, symID)
	lockNum, err := RequestLock(lockHandle, reqID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire lock: %v", err)
	}
	defer ReleaseLock(lockHandle, reqID, lockNum)

	// protectStorageGroupCSIAddons is idempotent (returns success when the
	// SG is already SRDF-protected), validates the RDF group and clears any
	// stale remote SG before establishing SRDF protection.
	if perr := s.service.protectStorageGroupCSIAddons(
		ctx, symID, params.remoteSymID, sgName,
		rdfGrpNo, params.replicationMode, pmaxClient); perr != nil {
		return nil, perr
	}

	// After SRDF protection is established (or confirmed idempotent),
	// ensure that every R2 volume has its VolumeIdentifier set to match
	// the corresponding R1 volume. SRDF CreateSGReplica does not
	// propagate VolumeIdentifiers, causing post-failover CSI validation
	// errors (invalid CSI volume ID). The helper is idempotent:
	// already-correct identifiers are skipped.
	if rerr := s.service.syncRemoteVolumeIdentifiers(
		ctx, symID, params.remoteSymID, sgName,
		rdfGrpNo, pmaxClient); rerr != nil {
		return nil, rerr
	}

	return &csiaddonsreplication.EnableVolumeReplicationResponse{}, nil
}

// DisableVolumeReplication suspends the SRDF session on the target
// Storage Group. Per the CSI-Addons contract, this RPC must be
// idempotent -- a SG that is not protected or already suspended
// returns success.
func (s *CSIAddonsReplicationServer) DisableVolumeReplication(ctx context.Context,
	req *csiaddonsreplication.DisableVolumeReplicationRequest,
) (*csiaddonsreplication.DisableVolumeReplicationResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons DisableVolumeReplication called: %+v", req)

	vgID, err := s.resolveVGFromSource(req.GetReplicationSource())
	if err != nil {
		return nil, err
	}
	symID, sgName, rdfGrpNo, err := decodeVolumeGroupID(vgID)
	if err != nil {
		return nil, err
	}
	// Enforce the co-existence rule: CSI-Addons must not operate on storage
	// groups owned by CSM Replication or ordinary applications.
	if !isCSIAddonsManagedSG(sgName) {
		return nil, status.Errorf(codes.InvalidArgument, "DisableVolumeReplication: storage group %q is not managed by CSI-Addons; refusing to operate on it", sgName)
	}
	pmaxClient, err := s.service.GetPowerMaxClient(symID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "DisableVolumeReplication: failed to get PowerMax client: %v", err)
	}

	reqID := csiAddonsReqID(ctx)
	lockHandle := csiAddonsSGLockHandle(sgName, symID)
	lockNum, err := RequestLock(lockHandle, reqID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire lock: %v", err)
	}
	defer ReleaseLock(lockHandle, reqID, lockNum)

	psg, gerr := pmaxClient.GetProtectedStorageGroup(ctx, symID, sgName)
	if gerr != nil || psg == nil || !psg.Rdf {
		csmlog.WithContext(ctx).Infof("DisableVolumeReplication: SG %q on %q is not protected; idempotent success", sgName, symID)
		return &csiaddonsreplication.DisableVolumeReplicationResponse{}, nil
	}
	// Resolve the RDF group, refusing to guess when the SG spans more than
	// one RDF group.
	effectiveRdfGrp, rerr := resolveEffectiveRDFGroup(rdfGrpNo, psg)
	if rerr != nil {
		return nil, rerr
	}

	// Skip the suspend when SRDF state is already Suspended.
	if sgRDF, sErr := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, effectiveRdfGrp); sErr == nil && sgRDF != nil {
		state, _, _, _ := csiAddonsSRDFPersonality(sgRDF)
		if state == Suspended {
			csmlog.WithContext(ctx).Infof("DisableVolumeReplication: SG %q already Suspended; idempotent success", sgName)
			return &csiaddonsreplication.DisableVolumeReplicationResponse{}, nil
		}
	}
	if aerr := pmaxClient.ExecuteReplicationActionOnSG(
		ctx, symID, csiAddonsActionSuspend, sgName, effectiveRdfGrp, true /* force */, false, false); aerr != nil {
		return nil, status.Errorf(codes.Internal, "DisableVolumeReplication: failed to suspend SG %q: %v", sgName, aerr)
	}
	return &csiaddonsreplication.DisableVolumeReplicationResponse{}, nil
}

// PromoteVolume executes a SRDF Failover on the targeted Storage Group
// so that the local array becomes the active (R1) side. The semantics
// follow the CSI-Addons spec:
//   - force=false: planned failover. Followed by a Swap so that the
//     PowerMax personality is also swapped to keep the R1 label in
//     sync with reality.
//   - force=true: unplanned failover (no Swap; used when the original
//     R1 side is unreachable).
//
// Mixed personalities or mixed states are rejected with
// FailedPrecondition because PowerMax does not support SG-level SRDF
// actions in those situations.
func (s *CSIAddonsReplicationServer) PromoteVolume(ctx context.Context,
	req *csiaddonsreplication.PromoteVolumeRequest,
) (*csiaddonsreplication.PromoteVolumeResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons PromoteVolume called: force=%v %+v", req.GetForce(), req)

	pmaxClient, symID, sgName, rdfGrpNo, sgRDF, err := s.loadSRDFContext(ctx, req.GetReplicationSource())
	if err != nil {
		return nil, err
	}

	reqID := csiAddonsReqID(ctx)
	lockHandle := csiAddonsSGLockHandle(sgName, symID)
	lockNum, err := RequestLock(lockHandle, reqID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire lock: %v", err)
	}
	defer ReleaseLock(lockHandle, reqID, lockNum)

	state, isR1, mixedPersonalities, mixedStates := csiAddonsSRDFPersonality(sgRDF)
	if mixedPersonalities {
		return nil, status.Errorf(codes.FailedPrecondition, "PromoteVolume: SG %q has mixed SRDF personalities (%v); cannot operate at SG level", sgName, sgRDF.VolumeRdfTypes)
	}
	if mixedStates && !req.GetForce() {
		return nil, status.Errorf(codes.FailedPrecondition, "PromoteVolume: SG %q has mixed SRDF states (%v); use force=true for unplanned failover", sgName, sgRDF.States)
	}
	// Already primary -> idempotent.
	if isR1 && state != FailedOver {
		csmlog.WithContext(ctx).Infof("PromoteVolume: SG %q on %q is already R1 (state=%s); idempotent success", sgName, symID, state)
		return &csiaddonsreplication.PromoteVolumeResponse{}, nil
	}
	// R2 in FailedOver means this array received a prior failover and is
	// already the active (promoted) side. Re-issuing Failover would be a
	// no-op or trigger a second failover, so treat as idempotent success.
	if !isR1 && state == FailedOver {
		csmlog.WithContext(ctx).Infof("PromoteVolume: SG %q on %q is R2 and already failed over; idempotent success", sgName, symID)
		return &csiaddonsreplication.PromoteVolumeResponse{}, nil
	}
	// R1 in FailedOver means a prior DemoteVolume transferred control to
	// R2 via Failover. The resync loop (Swap → Resume) has not run yet.
	// Reclaim primary by issuing Failback, which restores the original
	// R1→R2 replication direction without a personality swap.
	if isR1 && state == FailedOver {
		csmlog.WithContext(ctx).Infof("PromoteVolume: SG %q on %q is R1 in FailedOver (previously demoted); issuing Failback to reclaim primary", sgName, symID)
		if fberr := pmaxClient.ExecuteReplicationActionOnSG(
			ctx, symID, csiAddonsActionFailback, sgName, rdfGrpNo,
			req.GetForce(), true /* exemptConsistency */, false /* bias */); fberr != nil {
			return nil, status.Errorf(codes.Internal, "PromoteVolume: failback on SG %q failed: %v", sgName, fberr)
		}
		return &csiaddonsreplication.PromoteVolumeResponse{}, nil
	}
	// exemptConsistency=true mirrors the CSM Replication failover path
	// (service/replication.go): PowerMax may otherwise reject the failover
	// when it cannot verify link consistency (e.g. the partner array is
	// unreachable), which is exactly the DR scenario this RPC must handle.
	if ferr := pmaxClient.ExecuteReplicationActionOnSG(
		ctx, symID, csiAddonsActionFailover, sgName, rdfGrpNo,
		req.GetForce() /* force */, true /* exemptConsistency */, false /* bias */); ferr != nil {
		return nil, status.Errorf(codes.Internal, "PromoteVolume: failover on SG %q failed: %v", sgName, ferr)
	}
	if !req.GetForce() {
		// Planned failover: swap so PowerMax R1/R2 labels match reality.
		if serr := pmaxClient.ExecuteReplicationActionOnSG(
			ctx, symID, csiAddonsActionSwap, sgName, rdfGrpNo, false, false, false); serr != nil {
			csmlog.WithContext(ctx).Warnf("PromoteVolume: swap on SG %q failed (continuing): %v", sgName, serr)
		}
	}
	return &csiaddonsreplication.PromoteVolumeResponse{}, nil
}

// DemoteVolume relinquishes the primary role on the local array by
// running a planned SRDF Failover with the local site as the source.
// When the SRDF link is already FailedOver toward the remote site the
// RPC is a no-op (idempotent).
func (s *CSIAddonsReplicationServer) DemoteVolume(ctx context.Context,
	req *csiaddonsreplication.DemoteVolumeRequest,
) (*csiaddonsreplication.DemoteVolumeResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons DemoteVolume called: %+v", req)

	pmaxClient, symID, sgName, rdfGrpNo, sgRDF, err := s.loadSRDFContext(ctx, req.GetReplicationSource())
	if err != nil {
		return nil, err
	}

	reqID := csiAddonsReqID(ctx)
	lockHandle := csiAddonsSGLockHandle(sgName, symID)
	lockNum, err := RequestLock(lockHandle, reqID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire lock: %v", err)
	}
	defer ReleaseLock(lockHandle, reqID, lockNum)

	state, isR1, mixedPersonalities, mixedStates := csiAddonsSRDFPersonality(sgRDF)
	if mixedPersonalities {
		return nil, status.Errorf(codes.FailedPrecondition, "DemoteVolume: SG %q has mixed SRDF personalities (%v); cannot operate at SG level", sgName, sgRDF.VolumeRdfTypes)
	}
	// DemoteVolume performs a planned failover, which PowerMax rejects on
	// a storage group whose devices are in mixed SRDF states. Reject it
	// up front with an actionable error rather than letting the array
	// fail the request with an opaque internal error.
	if mixedStates {
		return nil, status.Errorf(codes.FailedPrecondition, "DemoteVolume: SG %q has mixed SRDF states (%v); cannot perform a planned failover at SG level", sgName, sgRDF.States)
	}
	// If we are already R2 OR the link is already FailedOver, nothing
	// to do.
	if !isR1 || state == FailedOver {
		csmlog.WithContext(ctx).Infof("DemoteVolume: SG %q on %q already demoted (state=%s,isR1=%v); idempotent success",
			sgName, symID, state, isR1)
		return &csiaddonsreplication.DemoteVolumeResponse{}, nil
	}
	// exemptConsistency=true mirrors the CSM Replication failover path
	// (service/replication.go) so a planned demote is not blocked by a
	// transient consistency check.
	if ferr := pmaxClient.ExecuteReplicationActionOnSG(
		ctx, symID, csiAddonsActionFailover, sgName, rdfGrpNo,
		req.GetForce(), true /* exemptConsistency */, false); ferr != nil {
		return nil, status.Errorf(codes.Internal, "DemoteVolume: failover on SG %q failed: %v", sgName, ferr)
	}
	return &csiaddonsreplication.DemoteVolumeResponse{}, nil
}

// ResyncVolume re-establishes SRDF replication after a failover.
// The response Ready flag is true only when the SG is in a fully
// synchronised state (Consistent for SRDF/A or Synchronized for
// SRDF/S). Intermediate states return Ready=false so the orchestrator
// will retry.
func (s *CSIAddonsReplicationServer) ResyncVolume(ctx context.Context,
	req *csiaddonsreplication.ResyncVolumeRequest,
) (*csiaddonsreplication.ResyncVolumeResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons ResyncVolume called: %+v", req)

	pmaxClient, symID, sgName, rdfGrpNo, sgRDF, err := s.loadSRDFContext(ctx, req.GetReplicationSource())
	if err != nil {
		return nil, err
	}

	reqID := csiAddonsReqID(ctx)
	lockHandle := csiAddonsSGLockHandle(sgName, symID)
	lockNum, err := RequestLock(lockHandle, reqID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to acquire lock: %v", err)
	}
	defer ReleaseLock(lockHandle, reqID, lockNum)

	state, _, mixedPersonalities, _ := csiAddonsSRDFPersonality(sgRDF)
	if mixedPersonalities {
		return nil, status.Errorf(codes.FailedPrecondition, "ResyncVolume: SG %q has mixed SRDF personalities (%v); cannot operate at SG level", sgName, sgRDF.VolumeRdfTypes)
	}
	if state == Consistent || state == Synchronized || state == ActiveBias {
		return &csiaddonsreplication.ResyncVolumeResponse{Ready: true}, nil
	}

	// A sync that is already in progress must not trigger another SRDF
	// action: issuing Establish on an in-progress copy can restart the
	// cycle and force a full resync. Return Ready=false so the
	// orchestrator polls again.
	if state == SyncInProgress {
		csmlog.WithContext(ctx).Infof("ResyncVolume: SG %q on %q is %s; waiting for sync to complete", sgName, symID, state)
		return &csiaddonsreplication.ResyncVolumeResponse{Ready: false}, nil
	}

	// Map the current SRDF link state to the recovery action, mirroring
	// the CSI-Addons design table (section 4.7):
	//   Suspended / Partitioned -> Resume
	//   Split                   -> Establish
	//   FailedOver              -> Swap
	var action string
	switch state {
	case Suspended, Partitioned:
		action = csiAddonsActionResume
	case Split:
		action = csiAddonsActionEstablish
	case FailedOver:
		action = csiAddonsActionSwap
	default:
		// Unknown / transient state: do not issue a blind SRDF action
		// (which could restart a sync). Wait and let the orchestrator
		// retry.
		csmlog.WithContext(ctx).Warnf("ResyncVolume: SG %q on %q in unhandled state %q; waiting", sgName, symID, state)
		return &csiaddonsreplication.ResyncVolumeResponse{Ready: false}, nil
	}
	// Resume exempts consistency (matching the CSM Replication Resume
	// helper in service/replication.go). Establish and Swap do not,
	// because they must validate the link state before proceeding.
	exemptConsistency := action == csiAddonsActionResume
	if aerr := pmaxClient.ExecuteReplicationActionOnSG(
		ctx, symID, action, sgName, rdfGrpNo,
		req.GetForce(), exemptConsistency, false); aerr != nil {
		return nil, status.Errorf(codes.Internal, "ResyncVolume: %s on SG %q failed: %v", action, sgName, aerr)
	}
	return &csiaddonsreplication.ResyncVolumeResponse{Ready: false}, nil
}

// GetVolumeReplicationInfo queries the live SRDF state for the supplied
// volume group and translates it into the CSI-Addons Health enum.
func (s *CSIAddonsReplicationServer) GetVolumeReplicationInfo(ctx context.Context,
	req *csiaddonsreplication.GetVolumeReplicationInfoRequest,
) (*csiaddonsreplication.GetVolumeReplicationInfoResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons GetVolumeReplicationInfo called: %+v", req)

	_, _, sgName, _, sgRDF, err := s.loadSRDFContext(ctx, req.GetReplicationSource())
	if err != nil {
		return nil, err
	}
	state, _, mixedPersonalities, mixedStates := csiAddonsSRDFPersonality(sgRDF)
	statusCode := csiaddonsreplication.GetVolumeReplicationInfoResponse_UNKNOWN
	statusMessage := fmt.Sprintf("SG=%s %s", sgName, summarizeSRDFState(sgRDF))
	switch {
	case mixedPersonalities:
		// Actionable guidance: an SG with both R1 and R2 devices cannot be
		// driven at the SG level and needs an administrator to realign the
		// device roles.
		statusCode = csiaddonsreplication.GetVolumeReplicationInfoResponse_DEGRADED
		statusMessage = fmt.Sprintf("SG=%s has mixed SRDF personalities (%v); manual intervention required to align all devices to a single R1/R2 role (e.g. re-pair the divergent device) before replication operations can proceed", sgName, sgRDF.VolumeRdfTypes)
	case state == Consistent, state == Synchronized, state == ActiveBias:
		// ActiveBias is treated as a healthy/synchronized state, matching
		// the CSM Replication state handling.
		statusCode = csiaddonsreplication.GetVolumeReplicationInfoResponse_HEALTHY
	case mixedStates:
		statusCode = csiaddonsreplication.GetVolumeReplicationInfoResponse_DEGRADED
		statusMessage = fmt.Sprintf("SG=%s has mixed SRDF states (%v); wait for the link to settle, or suspend then resume the group to realign all devices", sgName, sgRDF.States)
	case state == SyncInProgress:
		statusCode = csiaddonsreplication.GetVolumeReplicationInfoResponse_DEGRADED
	case state == Suspended, state == Split, state == Partitioned, state == FailedOver:
		// "Partitioned" (link down) was previously unmapped and fell through
		// to UNKNOWN.
		statusCode = csiaddonsreplication.GetVolumeReplicationInfoResponse_DEGRADED
	case state == Invalid:
		statusCode = csiaddonsreplication.GetVolumeReplicationInfoResponse_ERROR
	}
	resp := &csiaddonsreplication.GetVolumeReplicationInfoResponse{
		Status:        statusCode,
		StatusMessage: statusMessage,
	}
	// PowerMax does not expose a per-SG last-sync timestamp. We report
	// time.Now() for states where the local data is consistent (in-sync
	// or failed-over). Transitional states (SyncInProgress, etc.) leave
	// the field nil.
	switch state {
	case Consistent, Synchronized, ActiveBias, FailedOver:
		resp.LastSyncTime = timestamppb.New(time.Now())
	}
	return resp, nil
}

// loadSRDFContext is the shared prologue used by every SRDF action RPC.
// It decodes the volume group id, opens a Unisphere client and fetches
// the current SRDF state for the target Storage Group.
func (s *CSIAddonsReplicationServer) loadSRDFContext(ctx context.Context,
	src *csiaddonsreplication.ReplicationSource,
) (pmax.Pmax, string, string, string, *types.StorageGroupRDFG, error) {
	vgID, err := s.resolveVGFromSource(src)
	if err != nil {
		return nil, "", "", "", nil, err
	}
	symID, sgName, rdfGrpNo, err := decodeVolumeGroupID(vgID)
	if err != nil {
		return nil, "", "", "", nil, err
	}
	if rdfGrpNo == "" {
		return nil, "", "", "", nil, status.Error(codes.InvalidArgument, "rdfGroupNumber must be encoded in the volume_group_id")
	}
	pmaxClient, err := s.service.GetPowerMaxClient(symID)
	if err != nil {
		return nil, "", "", "", nil, status.Errorf(codes.Internal, "failed to get PowerMax client: %v", err)
	}
	sgRDF, gerr := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, rdfGrpNo)
	if gerr != nil || sgRDF == nil {
		return nil, "", "", "", nil, status.Errorf(codes.NotFound, "SG %q on %q (rdfg=%s): %v", sgName, symID, rdfGrpNo, gerr)
	}
	return pmaxClient, symID, sgName, rdfGrpNo, sgRDF, nil
}

// GetReplicationDestinationInfo returns the remote (R2) volume group id and
// the per-volume source->destination device mappings for an SRDF-protected
// Storage Group. Only VolumeGroup sources are supported; volume-level
// sources are rejected, consistent with the rest of the PowerMax CSI-Addons
// surface.
//
// The remote Symmetrix ID and remote RDF group number are discovered from
// the SRDF device pairs (GetRDFDevicePairInfo) so that no request parameters
// are required -- the CSI-Addons GetReplicationDestinationInfo RPC does not
// carry VolumeReplicationClass parameters.
func (s *CSIAddonsReplicationServer) GetReplicationDestinationInfo(ctx context.Context,
	req *csiaddonsreplication.GetReplicationDestinationInfoRequest,
) (*csiaddonsreplication.GetReplicationDestinationInfoResponse, error) {
	csmlog.WithContext(ctx).Infof("CSI-Addons GetReplicationDestinationInfo called: %+v", req)

	vgID, err := s.resolveVGFromSource(req.GetReplicationSource())
	if err != nil {
		return nil, err
	}
	symID, sgName, rdfGrpNo, err := decodeVolumeGroupID(vgID)
	if err != nil {
		return nil, err
	}
	if rdfGrpNo == "" {
		return nil, status.Error(codes.InvalidArgument, "GetReplicationDestinationInfo: rdfGroupNumber must be encoded in the volume_group_id")
	}
	pmaxClient, err := s.service.GetPowerMaxClient(symID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "GetReplicationDestinationInfo: failed to get PowerMax client: %v", err)
	}

	devIDs, lerr := pmaxClient.GetVolumeIDListInStorageGroup(ctx, symID, sgName)
	if lerr != nil {
		return nil, status.Errorf(codes.NotFound, "GetReplicationDestinationInfo: failed to list volumes in SG %q on %q: %v", sgName, symID, lerr)
	}
	if len(devIDs) == 0 {
		return nil, status.Errorf(codes.FailedPrecondition, "GetReplicationDestinationInfo: SG %q on %q has no volumes", sgName, symID)
	}

	// Fetch VolumeIdentifiers for all volumes in the SG to construct full CSI volume handles.
	// The v1 enhanced volumes endpoint was introduced in Unisphere 10.1 (API version 101).
	// If the array is older, fall back to per-volume GetVolumeByID calls.
	identifiersByDevID := make(map[string]string)
	useBulk := false
	versionDetails, verr := s.service.getVersionCache().getOrFetchVersionDetails(ctx, symID, pmaxClient)
	if verr != nil {
		csmlog.WithContext(ctx).Infof("GetReplicationDestinationInfo: could not determine Unisphere version; using per-volume GetVolumeByID: %v", verr)
	} else if versionDetails.APIVersion != "" {
		apiVersion, aerr := strconv.Atoi(versionDetails.APIVersion)
		if aerr != nil {
			csmlog.WithContext(ctx).Infof("GetReplicationDestinationInfo: could not parse APIVersion %q; using per-volume GetVolumeByID: %v", versionDetails.APIVersion, aerr)
		} else if apiVersion >= APIVersion101 {
			useBulk = true
		} else {
			csmlog.WithContext(ctx).Infof("GetReplicationDestinationInfo: APIVersion %d is below %d; using per-volume GetVolumeByID", apiVersion, APIVersion101)
		}
	} else {
		csmlog.WithContext(ctx).Infof("GetReplicationDestinationInfo: no APIVersion available; using per-volume GetVolumeByID")
	}

	if useBulk {
		volPage, err := pmaxClient.GetVolumesIdentifiersInStorageGroup(ctx, symID, sgName)
		if err != nil {
			csmlog.WithContext(ctx).Infof("GetReplicationDestinationInfo: bulk fetch failed (%v); falling back to per-volume GetVolumeByID", err)
			useBulk = false
		} else {
			for _, v := range volPage.Volumes {
				identifiersByDevID[v.ID] = v.Identifier
			}
		}
	}

	volumeMappings := make(map[string]string, len(devIDs))
	remoteSymID := ""
	remoteRdfGrpNo := ""
	for _, devID := range devIDs {
		identifier, ok := identifiersByDevID[devID]
		if !ok {
			// Fallback to per-volume query if bulk failed or wasn't used
			vol, err := pmaxClient.GetVolumeByID(ctx, symID, devID)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "GetReplicationDestinationInfo: failed to get volume info for %q on %q: %v", devID, symID, err)
			}
			identifier = vol.VolumeIdentifier
		}

		pair, perr := pmaxClient.GetRDFDevicePairInfo(ctx, symID, rdfGrpNo, devID)
		if perr != nil || pair == nil {
			return nil, status.Errorf(codes.FailedPrecondition, "GetReplicationDestinationInfo: no RDF pair for device %q in SG %q: %v", devID, sgName, perr)
		}
		if remoteSymID == "" {
			remoteSymID = pair.RemoteSymmID
			remoteRdfGrpNo = fmt.Sprint(pair.RemoteRdfGroupNumber)
		}

		// PV volume handles must match exactly to allow RAMEN DR to manage replica PVs.
		// A CSI volume handle looks like: <VolumeIdentifier>-<symID>-<devID>
		// We format both source and destination IDs using this pattern. Since
		// EnableVolumeReplication synchronizes the remote VolumeIdentifier to match
		// the local one, we use the local identifier for both.
		src := fmt.Sprintf("%s-%s-%s", identifier, symID, devID)
		dst := fmt.Sprintf("%s-%s-%s", identifier, pair.RemoteSymmID, pair.RemoteVolumeName)
		volumeMappings[src] = dst
	}

	destVGID := encodeVolumeGroupID(remoteSymID, sgName, remoteRdfGrpNo)
	csmlog.WithContext(ctx).Infof("GetReplicationDestinationInfo: SG %q on %q -> remote VG %q (%d volume mappings)",
		sgName, symID, destVGID, len(volumeMappings))

	return &csiaddonsreplication.GetReplicationDestinationInfoResponse{
		ReplicationDestination: &csiaddonsreplication.ReplicationDestination{
			Type: &csiaddonsreplication.ReplicationDestination_Volumegroup{
				Volumegroup: &csiaddonsreplication.ReplicationDestination_VolumeGroupDestination{
					VolumeGroupId: destVGID,
					VolumeIds:     volumeMappings,
				},
			},
		},
	}, nil
}
