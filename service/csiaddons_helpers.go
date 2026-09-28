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

package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/dell/csmlog"
	csictx "github.com/dell/gocsi/context"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CSI-Addons parameter keys exposed on VolumeReplicationClass and
// VolumeGroupReplicationClass parameters. These names follow the
// "replication.storage.dell.com/" convention used across Dell CSI drivers
// (see csi-powerstore reference implementation) so that a single Ramen /
// OCM DR configuration can drive multiple storage backends.
const (
	// CSIAddonsParamRemoteSystem is the parameter key for the remote
	// PowerMax Symmetrix ID (R2 array). Required.
	CSIAddonsParamRemoteSystem = "replication.storage.dell.com/remoteSystem"

	// CSIAddonsParamReplicationMode is the parameter key for the SRDF
	// replication mode. Must be one of "SYNC" or "ASYNC". "METRO" is
	// explicitly rejected.
	CSIAddonsParamReplicationMode = "replication.storage.dell.com/replicationMode"

	// CSIAddonsParamVolumeGroupPrefix is the parameter key for the prefix
	// to use when constructing the protected Storage Group name. Defaults
	// to "csi-addons-vg" when not provided.
	CSIAddonsParamVolumeGroupPrefix = "replication.storage.dell.com/volumeGroupPrefix"

	// CSIAddonsParamRdfGroupNumber is the parameter key for the manual
	// SRDF RDF group number. When omitted the driver will discover or
	// auto-create the RDF group.
	CSIAddonsParamRdfGroupNumber = "replication.storage.dell.com/rdfGroupNumber"

	// CSIAddonsParamLocalArrayID is the parameter key for the source
	// (R1) Symmetrix ID. When absent the driver derives it from the
	// volume IDs in the request.
	CSIAddonsParamLocalArrayID = "replication.storage.dell.com/sourceArray"

	// CSIAddonsParamTargetArrayID is an alias for the remote system
	// parameter. Some Ramen configurations use this key.
	CSIAddonsParamTargetArrayID = "replication.storage.dell.com/targetArray"

	// CSIAddonsParamNamespace is the parameter key for the namespace
	// component used when constructing the storage group name.
	CSIAddonsParamNamespace = "replication.storage.dell.com/namespace"

	// CSIAddonsManagedByLabel marks the protected storage group as
	// managed by the CSI-Addons code path rather than CSM Replication.
	CSIAddonsManagedByLabel = "replication.storage.dell.com/managed-by"

	// CSIAddonsManagedByValue is the value paired with the managed-by
	// label.
	CSIAddonsManagedByValue = "csi-addons"

	// defaultVolumeGroupPrefix is used when the caller does not supply
	// CSIAddonsParamVolumeGroupPrefix.
	defaultVolumeGroupPrefix = "csi-addons-vg"

	// metroErrorMessage is the standard error returned when a caller
	// requests SRDF/Metro through the CSI-Addons API surface.
	metroErrorMessage = "SRDF Metro is not supported by CSI-Addons"

	// CSI-Addons local copies of the SRDF action verbs accepted by
	// gopowermax's ExecuteReplicationActionOnSG. These constants
	// intentionally duplicate the values defined in service/replication.go
	// so that the CSI-Addons code path has no compile-time dependency on
	// the existing CSM Replication helpers (per the design constraint
	// "do not reuse existing service/replication.go").
	csiAddonsActionSuspend   = "Suspend"
	csiAddonsActionResume    = "Resume"
	csiAddonsActionEstablish = "Establish"
	csiAddonsActionFailover  = "Failover"
	csiAddonsActionFailback  = "Failback"
	csiAddonsActionSwap      = "Swap"
)

// csiAddonsReplicationParams collects parameters extracted from the
// CSI-Addons request that are required to drive PowerMax SRDF operations.
type csiAddonsReplicationParams struct {
	// remoteSymID is the R2 Symmetrix ID.
	remoteSymID string
	// replicationMode is one of Async or Sync (PowerMax SRDF mode strings).
	replicationMode string
	// rdfGroupNumber is the explicit RDF group number; empty when the
	// driver should auto-discover/create one.
	rdfGroupNumber string
	// volumeGroupPrefix is the prefix used for the storage group name.
	volumeGroupPrefix string
	// namespace, when non-empty, is appended to the storage group name.
	namespace string
}

// parseCSIAddonsReplicationParams extracts and validates the standard
// CSI-Addons replication parameter set. The parameters map is the union
// of VolumeReplicationClass parameters and any per-request parameters
// merged in by the CSI-Addons controller. The function is intentionally
// strict so that misconfigurations are surfaced as InvalidArgument
// errors at the gRPC boundary rather than producing partial PowerMax
// changes.
//
// SRDF Metro is rejected with codes.InvalidArgument and a stable error
// message ("SRDF Metro is not supported by CSI-Addons") so that
// orchestrators can reliably detect this condition.
func parseCSIAddonsReplicationParams(params map[string]string) (*csiAddonsReplicationParams, error) {
	if params == nil {
		return nil, status.Error(codes.InvalidArgument, "request parameters are required")
	}

	p := &csiAddonsReplicationParams{
		volumeGroupPrefix: defaultVolumeGroupPrefix,
	}

	// remote system: required. Accept either the canonical key or the
	// targetArray alias.
	if v, ok := params[CSIAddonsParamRemoteSystem]; ok && v != "" {
		p.remoteSymID = v
	} else if v, ok := params[CSIAddonsParamTargetArrayID]; ok && v != "" {
		p.remoteSymID = v
	}
	if p.remoteSymID == "" {
		return nil, status.Errorf(codes.InvalidArgument,
			"missing required parameter %q", CSIAddonsParamRemoteSystem)
	}

	// replication mode: required, must be SYNC or ASYNC.
	mode := strings.ToUpper(strings.TrimSpace(params[CSIAddonsParamReplicationMode]))
	switch mode {
	case Sync, Async:
		p.replicationMode = mode
	case Metro:
		return nil, status.Error(codes.InvalidArgument, metroErrorMessage)
	case "":
		return nil, status.Errorf(codes.InvalidArgument,
			"missing required parameter %q", CSIAddonsParamReplicationMode)
	default:
		return nil, status.Errorf(codes.InvalidArgument,
			"unsupported replication mode %q; supported values: SYNC, ASYNC", mode)
	}

	// optional fields
	if v, ok := params[CSIAddonsParamRdfGroupNumber]; ok {
		p.rdfGroupNumber = strings.TrimSpace(v)
	}
	if v, ok := params[CSIAddonsParamVolumeGroupPrefix]; ok && strings.TrimSpace(v) != "" {
		p.volumeGroupPrefix = strings.TrimSpace(v)
	}
	if v, ok := params[CSIAddonsParamNamespace]; ok {
		p.namespace = strings.TrimSpace(v)
	}

	return p, nil
}

// buildCSIAddonsSGName generates a deterministic storage group name for a
// CSI-Addons managed volume group. Reusing the existing CsiRepSGPrefix
// keeps the on-array naming consistent with CSM Replication so that
// administrators only have to recognise a single Storage Group naming
// convention. The "addons" infix prevents collisions with CSM Replication
// SGs in mixed deployments.
//
// PowerMax limits storage group names to 64 characters. When the assembled
// name exceeds this limit, the variable parts (vgPrefix and namespace) are
// truncated using truncateString while the fixed structural components
// (prefix, rdfGroupNo, mode and separators) are kept intact. This follows
// the same pattern used for port group names in controller.go.
//
// Format: csi-rep-sg-addons-<vgPrefix>-<namespace>-<rdfGroupNo>-<mode>
func buildCSIAddonsSGName(vgPrefix, namespace, rdfGrpNo, mode string) string {
	cleanPrefix := strings.TrimSpace(vgPrefix)
	if cleanPrefix == "" {
		cleanPrefix = defaultVolumeGroupPrefix
	}
	cleanNS := strings.TrimSpace(namespace)
	if cleanNS == "" {
		cleanNS = "default"
	}

	// Fixed parts: "csi-rep-sg-addons-" (18) + "-" + "-" + rdfGrpNo + "-" + mode
	fixedLen := len(csiAddonsManagedSGPrefix) + len(rdfGrpNo) + len(mode) + 3 // 3 separating dashes
	maxVariableLen := MaxStorageGroupNameLength - fixedLen
	variablePart := cleanPrefix + "-" + cleanNS
	if len(variablePart) > maxVariableLen {
		variablePart = truncateString(variablePart, maxVariableLen)
	}
	return fmt.Sprintf("%s%s-%s-%s", csiAddonsManagedSGPrefix, variablePart, rdfGrpNo, mode)
}

// csiAddonsManagedSGPrefix is the on-array Storage Group name prefix used
// to identify CSI-Addons managed protected SGs. It reuses CsiRepSGPrefix
// (shared with CSM Replication) plus an "addons-" infix so the two code
// paths never collide and CSI-Addons SGs can be enumerated independently.
const csiAddonsManagedSGPrefix = CsiRepSGPrefix + "addons-"

// parseCSIAddonsSGName extracts the RDF group number and replication mode
// from a CSI-Addons managed Storage Group name produced by
// buildCSIAddonsSGName. The trailing two dash-separated components are
// always "<rdfGroupNo>-<mode>", so parsing is robust even when the volume
// group prefix or namespace themselves contain dashes. ok is false when
// the name is not a CSI-Addons managed SG.
func parseCSIAddonsSGName(sgName string) (rdfGrpNo, mode string, ok bool) {
	if !strings.HasPrefix(sgName, csiAddonsManagedSGPrefix) {
		return "", "", false
	}
	parts := strings.Split(sgName, "-")
	if len(parts) < 2 {
		return "", "", false
	}
	mode = parts[len(parts)-1]
	rdfGrpNo = parts[len(parts)-2]
	if rdfGrpNo == "" || mode == "" {
		return "", "", false
	}
	return rdfGrpNo, mode, true
}

// encodeVolumeGroupID encodes a PowerMax volume group identifier. The
// encoded id is stateless: every operation can reconstruct the array,
// storage group name and RDF group number directly from the id without
// hitting Unisphere first. Format:
//
//	<symID>:<sgName>:<rdfGroupNo>
func encodeVolumeGroupID(symID, sgName, rdfGrpNo string) string {
	return fmt.Sprintf("%s:%s:%s", symID, sgName, rdfGrpNo)
}

// decodeVolumeGroupID is the inverse of encodeVolumeGroupID. It returns
// codes.InvalidArgument if the id is malformed.
func decodeVolumeGroupID(vgID string) (symID, sgName, rdfGrpNo string, err error) {
	if vgID == "" {
		return "", "", "", status.Error(codes.InvalidArgument,
			"volume_group_id is required")
	}
	parts := strings.Split(vgID, ":")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" {
		return "", "", "", status.Errorf(codes.InvalidArgument,
			"invalid volume_group_id %q; expected <symID>:<sgName>:<rdfGroupNo>", vgID)
	}
	return parts[0], parts[1], parts[2], nil
}

// extractSymIDFromVolumeID returns the PowerMax Symmetrix ID for a CSI
// volume ID. It is a thin wrapper around service.parseCsiID so that the
// CSI-Addons code path does not need to depend on the full parse contract.
func (s *service) extractSymIDFromVolumeID(volumeID string) (string, error) {
	if volumeID == "" {
		return "", status.Error(codes.InvalidArgument, "volume ID is required")
	}
	_, symID, _, _, _, err := s.parseCsiID(volumeID)
	if err != nil {
		return "", status.Errorf(codes.InvalidArgument,
			"failed to parse volume id %q: %v", volumeID, err)
	}
	return symID, nil
}

// extractDevIDFromVolumeID returns the PowerMax device ID (5-char hex) for
// a CSI volume ID.
func (s *service) extractDevIDFromVolumeID(volumeID string) (string, error) {
	if volumeID == "" {
		return "", status.Error(codes.InvalidArgument, "volume ID is required")
	}
	_, _, devID, _, _, err := s.parseCsiID(volumeID)
	if err != nil {
		return "", status.Errorf(codes.InvalidArgument,
			"failed to parse volume id %q: %v", volumeID, err)
	}
	return devID, nil
}

// csiAddonsSRDFPersonality inspects a StorageGroupRDFG response and
// returns (link state, isR1, mixedPersonalities, mixedStates). It is a
// CSI-Addons-local copy of the equivalent helper in service/replication.go
// so that the new code path has no compile-time coupling to the existing
// CSM Replication helpers. Keeping the implementation in one place here
// also lets CSI-Addons unit tests cover this logic without pulling in
// the broader CSM Replication mocks.
func csiAddonsSRDFPersonality(psg *types.StorageGroupRDFG) (state string, isR1, mixedPersonalities, mixedStates bool) {
	if psg == nil {
		return "", false, false, false
	}
	// Scan ALL device personalities (not just the first element) so the
	// result is independent of the order Unisphere returns volumeRdfTypes
	// in. mixedPersonalities is true when more than one distinct personality
	// is present; isR1 is true only when every device is R1.
	personalities := make(map[string]struct{}, 2)
	for _, rdfType := range psg.VolumeRdfTypes {
		if rdfType == "" {
			continue
		}
		personalities[rdfType] = struct{}{}
	}
	mixedPersonalities = len(personalities) > 1
	if !mixedPersonalities {
		_, isR1 = personalities["R1"]
	}

	// Scan ALL device states symmetrically. state is the first non-empty
	// representative state; mixedStates is true when devices disagree.
	states := make(map[string]struct{}, 2)
	for _, rdfState := range psg.States {
		if rdfState == "" {
			continue
		}
		if state == "" {
			state = rdfState
		}
		states[rdfState] = struct{}{}
	}
	mixedStates = len(states) > 1
	return state, isR1, mixedPersonalities, mixedStates
}

// resolveEffectiveRDFGroup determines the RDF group number to operate on.
// When the caller supplied one (encoded in the volume_group_id) it is used
// verbatim. Otherwise the protected SG's RDF group is used, but only when it
// is unambiguous: an SG spanning more than one RDF group cannot be acted on
// at the SG level without an explicit group, so this returns a
// FailedPrecondition error instead of silently picking an arbitrary one.
func resolveEffectiveRDFGroup(rdfGrpNo string, psg *types.RDFStorageGroup) (string, error) {
	if rdfGrpNo != "" {
		return rdfGrpNo, nil
	}
	if psg == nil || len(psg.RDFGroups) == 0 {
		return "", status.Error(codes.FailedPrecondition,
			"cannot determine RDF group: none encoded in the volume_group_id and none reported by the storage group")
	}
	if len(psg.RDFGroups) > 1 {
		return "", status.Errorf(codes.FailedPrecondition,
			"storage group spans multiple RDF groups (%v); the rdfGroupNumber must be encoded in the volume_group_id", psg.RDFGroups)
	}
	return fmt.Sprint(psg.RDFGroups[0]), nil
}

// isCSIAddonsManagedSG reports whether the storage group name was created by
// the CSI-Addons code path (identified by the csi-rep-sg-addons- prefix).
// The co-existence rule requires CSI-Addons RPCs to refuse operating on
// storage groups owned by CSM Replication or by ordinary applications.
func isCSIAddonsManagedSG(sgName string) bool {
	return strings.HasPrefix(sgName, csiAddonsManagedSGPrefix)
}

// summarizeSRDFState returns a short human-readable string describing the
// current SRDF state of a storage group. Used in log lines and gRPC
// response messages.
func summarizeSRDFState(psg *types.StorageGroupRDFG) string {
	if psg == nil {
		return "unknown"
	}
	state, isR1, mixedPersonalities, mixedStates := csiAddonsSRDFPersonality(psg)
	personality := "R2"
	if isR1 {
		personality = "R1"
	}
	if mixedPersonalities {
		personality = "mixed"
	}
	if mixedStates {
		return fmt.Sprintf("personality=%s state=mixed states=%v", personality, psg.States)
	}
	if state == "" {
		state = "unknown"
	}
	return fmt.Sprintf("personality=%s state=%s", personality, state)
}

// csiAddonsReqID extracts the CSI request ID injected by gocsi from the
// context. It returns an empty string when no request ID is present;
// RequestLock treats an empty request ID as valid (it is used only for
// logging/diagnostics).
func csiAddonsReqID(ctx context.Context) string {
	if v, ok := ctx.Value(csictx.RequestIDKey).(string); ok {
		return v
	}
	return ""
}

// csiAddonsSGLockHandle builds the resource lock handle used to serialize
// SRDF / Storage Group mutations. It mirrors the handle used by the CSM
// Replication path (service/replication.go) -- "<sgName><symID>" -- so the
// two code paths cannot concurrently mutate the same Storage Group.
func csiAddonsSGLockHandle(sgName, symID string) string {
	return fmt.Sprintf("%s%s", sgName, symID)
}

// pendingRename holds the information needed to rename a single R2 volume.
type pendingRename struct {
	remoteDevID string // R2 device ID on the remote array
	identifier  string // desired VolumeIdentifier (copied from R1)
}

// syncRemoteVolumeIdentifiers ensures that each remote (R2) volume in the SRDF
// pair has the same VolumeIdentifier as the corresponding local (R1) volume.
// SRDF CreateSGReplica does not propagate the VolumeIdentifier to the R2 side,
// which causes the CSI driver's volume validation (volName != vol.VolumeIdentifier)
// to fail with an invalid CSI volume ID error during post-failover operations.
//
// The function is idempotent: volumes that already carry the correct identifier
// are skipped. Identifier lookup uses the v1 bulk endpoint
// (GetVolumesIdentifiersInStorageGroup) on Unisphere 10.1+ to reduce REST calls;
// older arrays or bulk failures fall back to per-volume GetVolumeByID. Renaming
// is performed with per-volume RenameVolume. Failures on individual volumes are
// logged and the loop continues so the remaining volumes are still renamed, but
// the function ultimately returns an error if any volume could not be renamed.
func (s *service) syncRemoteVolumeIdentifiers(ctx context.Context,
	localSymID, remoteSymID, sgName, rdfGrpNo string,
	pmaxClient pmax.Pmax,
) error {
	log := csmlog.WithContext(ctx)

	// ── Phase 0: decide whether the local array supports the v1 bulk GET endpoint.
	// The same API version applies to the enhanced volumes endpoint on the
	// remote array because the pair belongs to the same Unisphere version matrix.
	useBulkGet := false
	versionDetails, verr := s.getVersionCache().getOrFetchVersionDetails(ctx, localSymID, pmaxClient)
	if verr != nil {
		log.Infof("syncRemoteVolumeIdentifiers: could not determine Unisphere version; using per-volume GetVolumeByID: %v", verr)
	} else if versionDetails.APIVersion != "" {
		apiVersion, aerr := strconv.Atoi(versionDetails.APIVersion)
		if aerr != nil {
			log.Infof("syncRemoteVolumeIdentifiers: could not parse APIVersion %q; using per-volume GetVolumeByID: %v", versionDetails.APIVersion, aerr)
		} else if apiVersion >= APIVersion101 {
			useBulkGet = true
		} else {
			log.Infof("syncRemoteVolumeIdentifiers: APIVersion %d is below %d; using per-volume GetVolumeByID", apiVersion, APIVersion101)
		}
	} else {
		log.Infof("syncRemoteVolumeIdentifiers: no APIVersion available; using per-volume GetVolumeByID")
	}

	// ── Phase 1: gather local volume identifiers and device IDs ──────────
	localIdentifiersByDevID := make(map[string]string)
	var localDevIDs []string

	if useBulkGet {
		localVolPage, err := pmaxClient.GetVolumesIdentifiersInStorageGroup(ctx, localSymID, sgName)
		if err != nil {
			log.Infof("syncRemoteVolumeIdentifiers: bulk GET failed for local SG %q on %q (%v); falling back to per-volume GetVolumeByID", sgName, localSymID, err)
			useBulkGet = false
		} else {
			localDevIDs = make([]string, 0, len(localVolPage.Volumes))
			for _, v := range localVolPage.Volumes {
				localDevIDs = append(localDevIDs, v.ID)
				localIdentifiersByDevID[v.ID] = v.Identifier
			}
		}
	}

	if !useBulkGet {
		var err error
		localDevIDs, err = pmaxClient.GetVolumeIDListInStorageGroup(ctx, localSymID, sgName)
		if err != nil {
			return status.Errorf(codes.Internal,
				"syncRemoteVolumeIdentifiers: failed to list volumes in SG %q on %q: %v", sgName, localSymID, err)
		}
	}

	if len(localDevIDs) == 0 {
		log.Infof("syncRemoteVolumeIdentifiers: SG %q on %q has no volumes; nothing to rename", sgName, localSymID)
		return nil
	}

	// ── Phase 2: gather remote volume identifiers in bulk when possible ─────
	remoteIdentifiersByDevID := make(map[string]string)
	if useBulkGet {
		remoteVolPage, err := pmaxClient.GetVolumesIdentifiersInStorageGroup(ctx, remoteSymID, sgName)
		if err != nil {
			log.Infof("syncRemoteVolumeIdentifiers: bulk GET failed for remote SG %q on %q (%v); falling back to per-volume GetVolumeByID", sgName, remoteSymID, err)
		} else {
			for _, v := range remoteVolPage.Volumes {
				remoteIdentifiersByDevID[v.ID] = v.Identifier
			}
		}
	}

	// ── Phase 3: build the list of renames needed ───────────────────────
	var pending []pendingRename
	var gatherErr error
	for _, localDevID := range localDevIDs {
		localIdentifier, ok := localIdentifiersByDevID[localDevID]
		if !ok {
			localVol, err := pmaxClient.GetVolumeByID(ctx, localSymID, localDevID)
			if err != nil {
				log.Errorf("syncRemoteVolumeIdentifiers: failed to get local volume %s on %s: %v", localDevID, localSymID, err)
				gatherErr = err
				continue
			}
			localIdentifier = localVol.VolumeIdentifier
		}
		if localIdentifier == "" {
			log.Infof("syncRemoteVolumeIdentifiers: local volume %s on %s has no identifier; skipping", localDevID, localSymID)
			continue
		}

		pair, err := pmaxClient.GetRDFDevicePairInfo(ctx, localSymID, rdfGrpNo, localDevID)
		if err != nil {
			log.Errorf("syncRemoteVolumeIdentifiers: failed to get RDF pair for %s (rdfg %s) on %s: %v", localDevID, rdfGrpNo, localSymID, err)
			gatherErr = err
			continue
		}

		remoteDevID := pair.RemoteVolumeName
		remoteIdentifier, ok := remoteIdentifiersByDevID[remoteDevID]
		if !ok {
			remoteVol, err := pmaxClient.GetVolumeByID(ctx, remoteSymID, remoteDevID)
			if err != nil {
				log.Errorf("syncRemoteVolumeIdentifiers: failed to get remote volume %s on %s: %v", remoteDevID, remoteSymID, err)
				gatherErr = err
				continue
			}
			remoteIdentifier = remoteVol.VolumeIdentifier
		}

		if remoteIdentifier == localIdentifier {
			log.Infof("syncRemoteVolumeIdentifiers: remote volume %s on %s already has identifier %q; skipping",
				remoteDevID, remoteSymID, remoteIdentifier)
			continue
		}

		pending = append(pending, pendingRename{remoteDevID: remoteDevID, identifier: localIdentifier})
	}

	if len(pending) == 0 {
		if gatherErr != nil {
			return status.Errorf(codes.Internal,
				"syncRemoteVolumeIdentifiers: errors during gather phase: %v", gatherErr)
		}
		return nil
	}

	// Phase 4: rename each pending R2 volume individually.
	if renameErr := s.perVolumeRename(ctx, remoteSymID, pending, pmaxClient); renameErr != nil {
		gatherErr = renameErr
	}

	if gatherErr != nil {
		return status.Errorf(codes.Internal,
			"syncRemoteVolumeIdentifiers: one or more remote volumes could not be renamed: %v", gatherErr)
	}
	return nil
}

// perVolumeRename renames each pending remote volume individually using
// RenameVolume. All failures are logged and returned as a joined error.
func (s *service) perVolumeRename(ctx context.Context,
	symID string, renames []pendingRename, pmaxClient pmax.Pmax,
) error {
	log := csmlog.WithContext(ctx)
	var errs []error
	for _, r := range renames {
		if _, err := pmaxClient.RenameVolume(ctx, symID, r.remoteDevID, r.identifier); err != nil {
			log.Errorf("syncRemoteVolumeIdentifiers: failed to rename remote volume %s on %s to %q: %v",
				r.remoteDevID, symID, r.identifier, err)
			errs = append(errs, err)
			continue
		}
		log.Infof("syncRemoteVolumeIdentifiers: renamed remote volume %s on %s to %q",
			r.remoteDevID, symID, r.identifier)
	}
	return errors.Join(errs...)
}

// protectStorageGroupCSIAddons establishes SRDF protection on a local
// Storage Group using the gopowermax SDK. It reproduces the safety checks
// performed by service.ProtectStorageGroup without coupling the CSI-Addons
// path to the CSM Replication SRDF helpers:
//
//  1. Idempotency: if the SG is already SRDF-protected the call is a no-op.
//  2. RDF group validation: the RDF group must exist; an ASYNC RDF group
//     that already carries device pairings is rejected.
//  3. Stale remote SG cleanup: any empty remote SG with the same name is
//     removed (CreateSGReplica requires no pre-existing remote SG).
//  4. Protection: CreateSGReplica creates the remote SG, RDF pairs and
//     establishes the SRDF relationship.
//
// The caller is responsible for holding the SG lock.
func (s *service) protectStorageGroupCSIAddons(ctx context.Context,
	localSymID, remoteSymID, sgName, rdfGrpNo, rdfMode string,
	pmaxClient pmax.Pmax,
) error {
	// Idempotency: already protected -> nothing to do.
	if sg, err := pmaxClient.GetProtectedStorageGroup(ctx, localSymID, sgName); err == nil && sg != nil && sg.Rdf {
		csmlog.WithContext(ctx).Infof("protectStorageGroupCSIAddons: SG %q on %q already SRDF-protected; idempotent success", sgName, localSymID)
		return nil
	}

	// Validate the RDF group before attempting to protect.
	rdfg, err := pmaxClient.GetRDFGroupByID(ctx, localSymID, rdfGrpNo)
	if err != nil {
		return status.Errorf(codes.Internal,
			"could not get RDF group (%s) information on symID (%s): %v", rdfGrpNo, localSymID, err)
	}
	if rdfg.Async && rdfg.NumDevices > 0 {
		return status.Errorf(codes.FailedPrecondition,
			"RDF group (%s) cannot be used for ASYNC, as it already has volume pairing", rdfGrpNo)
	}

	// Clear any stale (empty) remote SG so CreateSGReplica can succeed.
	if err := s.verifyAndDeleteRemoteStorageGroup(ctx, remoteSymID, sgName, pmaxClient); err != nil {
		return status.Errorf(codes.Internal,
			"could not verify remote storage group (%s): %v", sgName, err)
	}

	if _, err := pmaxClient.CreateSGReplica(
		ctx, localSymID, remoteSymID, rdfMode, rdfGrpNo,
		sgName, sgName, "", false /* bias */); err != nil {
		return status.Errorf(codes.Internal,
			"could not create storage group replica for (%s): %v", sgName, err)
	}
	return nil
}
