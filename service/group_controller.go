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
	"fmt"
	"strconv"
	"strings"
	"sync"

	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// vgsSnapshotPendingState prevents concurrent group snapshot operations on the same SG.
var vgsSnapshotPendingState = pendingState{
	maxPending:   50,
	pendingMutex: &sync.Mutex{},
}

// GroupControllerGetCapabilities returns the capabilities of the group controller service.
func (s *service) GroupControllerGetCapabilities(
	_ context.Context,
	_ *csi.GroupControllerGetCapabilitiesRequest,
) (*csi.GroupControllerGetCapabilitiesResponse, error) {
	return &csi.GroupControllerGetCapabilitiesResponse{
		Capabilities: []*csi.GroupControllerServiceCapability{
			{
				Type: &csi.GroupControllerServiceCapability_Rpc{
					Rpc: &csi.GroupControllerServiceCapability_RPC{
						Type: csi.GroupControllerServiceCapability_RPC_CREATE_DELETE_GET_VOLUME_GROUP_SNAPSHOT,
					},
				},
			},
		},
	}, nil
}

// CreateVolumeGroupSnapshot creates an atomic, crash-consistent group snapshot
// of multiple volumes on a PowerMax array using StorageGroup-level snapshot APIs.
func (s *service) CreateVolumeGroupSnapshot(
	ctx context.Context,
	req *csi.CreateVolumeGroupSnapshotRequest,
) (*csi.CreateVolumeGroupSnapshotResponse, error) {
	log := log.WithContext(ctx)

	// Validate name
	reqName := req.GetName()
	if reqName == "" {
		return nil, status.Error(codes.InvalidArgument, "required: Name")
	}

	// Build the snapshot name with cluster prefix
	snapName := buildGroupSnapName(s.getClusterPrefix(), reqName)
	if len(snapName) >= MaxSnapIdentifierLength {
		return nil, status.Errorf(codes.InvalidArgument,
			"snapshot name %q is %d characters, must be less than %d",
			snapName, len(snapName), MaxSnapIdentifierLength)
	}

	// Validate source volumes
	sourceVolumeIDs := req.GetSourceVolumeIds()
	if len(sourceVolumeIDs) == 0 {
		return nil, status.Error(codes.InvalidArgument, "required: SourceVolumeIds")
	}

	vols, commonSymID, err := s.parseAndValidateGroupSourceVolumes(sourceVolumeIDs)
	if err != nil {
		return nil, err
	}

	pmaxClient, err := s.getPmaxClient(commonSymID)
	if err != nil {
		return nil, err
	}
	if err := s.validateSnapshotLicense(ctx, commonSymID, pmaxClient); err != nil {
		return nil, err
	}

	volDetails, devIDToCSIVolID, sgName, err := s.getGroupVolumeDetails(ctx, pmaxClient, commonSymID, vols)
	if err != nil {
		return nil, err
	}

	groupSnapshotID := buildGroupSnapshotID(commonSymID, sgName, snapName)

	// Idempotency: if this snapshot already exists on all requested volumes,
	// return the existing group snapshot instead of creating a duplicate.
	existingResp, err := s.checkGroupSnapshotIdempotency(
		ctx, pmaxClient, commonSymID, snapName, groupSnapshotID, sgName, volDetails, devIDToCSIVolID)
	if err != nil {
		return nil, err
	}
	if existingResp != nil {
		log.Infof("CreateVolumeGroupSnapshot idempotent: groupSnapshotId=%s already exists, returning existing snapshot",
			groupSnapshotID)
		return existingResp, nil
	}

	// Pending state check per SG
	stateID := volumeIDType(fmt.Sprintf("%s-%s", commonSymID, sgName))
	if err := stateID.checkAndUpdatePendingState(&vgsSnapshotPendingState); err != nil {
		return nil, err
	}
	defer stateID.clearPending(&vgsSnapshotPendingState)

	// Create snapshots for multiple volumes in a single CreateSnapshot API call
	// This creates a consistency group snapshot of all volumes
	creationTime := timestamppb.Now()
	memberSnapshots, err := s.CreateMultiVolumeSnapshot(ctx, commonSymID, snapName, sgName, volDetails, devIDToCSIVolID, pmaxClient, creationTime)
	if err != nil {
		return nil, status.Errorf(codes.Internal,
			"failed to create multi-volume snapshot: %s", err.Error())
	}

	log.Infof("Created VolumeGroupSnapshot: groupSnapshotId=%s, members=%d, readyToUse=true",
		groupSnapshotID, len(memberSnapshots))

	return &csi.CreateVolumeGroupSnapshotResponse{
		GroupSnapshot: &csi.VolumeGroupSnapshot{
			GroupSnapshotId: groupSnapshotID,
			Snapshots:       memberSnapshots,
			CreationTime:    creationTime,
			ReadyToUse:      true, // Multi-volume snapshots are ready after creation
		},
	}, nil
}

// DeleteVolumeGroupSnapshot deletes a group snapshot and optionally cleans up temporary resources.
func (s *service) DeleteVolumeGroupSnapshot(
	ctx context.Context,
	req *csi.DeleteVolumeGroupSnapshotRequest,
) (*csi.DeleteVolumeGroupSnapshotResponse, error) {
	log := log.WithContext(ctx)

	groupSnapshotID := req.GetGroupSnapshotId()
	if groupSnapshotID == "" {
		return nil, status.Error(codes.InvalidArgument, "required: GroupSnapshotId")
	}

	symID, sgName, snapName, err := parseGroupSnapshotID(groupSnapshotID)
	if err != nil {
		// CSI spec v1.12: DeleteVolumeGroupSnapshot MUST be idempotent
		// Invalid or non-existent ID MUST return OK
		log.Infof("DeleteVolumeGroupSnapshot: invalid ID format %s, returning OK for idempotency", groupSnapshotID)
		return &csi.DeleteVolumeGroupSnapshotResponse{}, nil
	}

	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"failed to get PowerMax client for array %s: %s", symID, err.Error())
	}

	// Get the StorageGroup to determine which volumes are part of the group snapshot
	_, err = pmaxClient.GetStorageGroup(ctx, symID, sgName)
	if err != nil {
		// If SG not found, Snapshots associated with it would already be deleted.
		if types.IsNotFoundError(err) {
			log.Infof("StorageGroup %s not found, assuming group snapshot %s is already deleted", sgName, groupSnapshotID)
			return &csi.DeleteVolumeGroupSnapshotResponse{}, nil
		}

		return nil, status.Errorf(codes.Internal,
			"failed to get StorageGroup: %s", err.Error())
	}

	// Get the storage group snapshot information to find source volumes
	sgSnapIDs, err := pmaxClient.GetStorageGroupSnapshotSnapIDs(ctx, symID, sgName, snapName)
	if err != nil {
		if types.IsNotFoundError(err) {
			// Snapshot doesn't exist
			log.Infof("Storage group snapshot %s not found, assuming it's already deleted", snapName)
			return &csi.DeleteVolumeGroupSnapshotResponse{}, nil
		}

		// For non-NotFound errors, fail fast - these could be server errors, permissions, etc.
		return nil, status.Errorf(codes.Internal,
			"failed to get storage group snapshot IDs: %s", err.Error())
	}

	// If no snapshot IDs exist, snapshot doesn't exist
	if len(sgSnapIDs.SnapIDs) == 0 {
		log.Infof("No snapshot IDs found for %s, snapshot does not exist", snapName)
		return &csi.DeleteVolumeGroupSnapshotResponse{}, nil
	}

	// Get the storage group snapshot details using the first snap ID
	snapID := strconv.FormatInt(sgSnapIDs.SnapIDs[0], 10)
	sgSnapshot, err := pmaxClient.GetStorageGroupSnapshotSnap(ctx, symID, sgName, snapName, snapID)
	if err != nil {
		if types.IsNotFoundError(err) {
			// Snapshot details don't exist, assume snapshot doesn't exist
			log.Infof("Storage group snapshot details %s not found, assuming snapshot doesn't exist", snapName)
			return &csi.DeleteVolumeGroupSnapshotResponse{}, nil
		}
		// For non-NotFound errors, fail fast - these could be server errors, permissions, etc.
		return nil, status.Errorf(codes.Internal,
			"failed to get storage group snapshot details: %s", err.Error())
	}

	// Build source volumes list from the storage group snapshot
	sourceVolumes := []types.VolumeList{}
	if sgSnapshot != nil && sgSnapshot.SourceVolume != nil {
		for _, srcVol := range sgSnapshot.SourceVolume {
			sourceVolumes = append(sourceVolumes, types.VolumeList{Name: srcVol.Name})
		}
	}

	// CSI spec requires SP to check snapshot_ids and report mismatch if detectable.
	// Validate that every requested snapshot ID matches a volume we found in the SG.
	if snapshotIDs := req.GetSnapshotIds(); len(snapshotIDs) > 0 {
		expectedPrefix := fmt.Sprintf("%s-%s-", snapName, symID)
		foundDevIDs := make(map[string]bool, len(sourceVolumes))
		for _, sv := range sourceVolumes {
			foundDevIDs[sv.Name] = true
		}
		for _, sid := range snapshotIDs {
			if !strings.HasPrefix(sid, expectedPrefix) {
				return nil, status.Errorf(codes.InvalidArgument,
					"snapshot ID %q does not match group snapshot (expected prefix %q)", sid, expectedPrefix)
			}
			devID := strings.TrimPrefix(sid, expectedPrefix)
			if !foundDevIDs[devID] {
				log.Warnf("snapshot ID %s references device %s which was not found with snapshot %s", sid, devID, snapName)
			}
		}
	}

	// Use generation 0 for multi-volume snapshots
	generation := int64(0)

	err = pmaxClient.DeleteSnapshotS(ctx, symID, snapName, sourceVolumes, generation)
	if err != nil {
		// Treat not-found as success (idempotent)
		if types.IsNotFoundError(err) {
			log.Infof("Group snapshot %s already deleted (not found), returning success", groupSnapshotID)
		} else {
			return nil, status.Errorf(codes.Internal,
				"failed to delete multi-volume snapshot: %s", err.Error())
		}
	}

	log.Infof("Deleted VolumeGroupSnapshot: groupSnapshotId=%s", groupSnapshotID)
	return &csi.DeleteVolumeGroupSnapshotResponse{}, nil
}

// GetVolumeGroupSnapshot queries the current state of a group snapshot.
func (s *service) GetVolumeGroupSnapshot(
	ctx context.Context,
	req *csi.GetVolumeGroupSnapshotRequest,
) (*csi.GetVolumeGroupSnapshotResponse, error) {
	log := log.WithContext(ctx)

	groupSnapshotID := req.GetGroupSnapshotId()
	if groupSnapshotID == "" {
		return nil, status.Error(codes.InvalidArgument, "required: GroupSnapshotId")
	}

	symID, sgName, snapName, err := parseGroupSnapshotID(groupSnapshotID)
	if err != nil {
		// CSI spec v1.12: GetVolumeGroupSnapshot with non-existent ID MUST return NotFound
		return nil, status.Errorf(codes.NotFound,
			"group snapshot %s not found: %s", groupSnapshotID, err.Error())
	}

	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"failed to get PowerMax client for array %s: %s", symID, err.Error())
	}

	// Validate the StorageGroup exists
	_, err = pmaxClient.GetStorageGroup(ctx, symID, sgName)
	if err != nil {
		if types.IsNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound,
				"StorageGroup %s not found on array %s", sgName, symID)
		}
		return nil, status.Errorf(codes.Internal,
			"failed to get StorageGroup %s on array %s: %s", sgName, symID, err.Error())
	}

	// Get volume list from the StorageGroup to know which volumes should be in the snapshot
	volIDList, err := pmaxClient.GetVolumeIDListInStorageGroup(ctx, symID, sgName)
	if err != nil {
		if types.IsNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound,
				"StorageGroup %s not found on array %s", sgName, symID)
		}
		return nil, status.Errorf(codes.Internal,
			"failed to get volume list from StorageGroup %s on array %s: %s", sgName, symID, err.Error())
	}

	// Get the storage group snapshot information using the same approach as DeleteVolumeGroupSnapshot
	sgSnapshot, err := s.getStorageGroupSnapshotDetails(ctx, pmaxClient, symID, sgName, snapName)
	if err != nil {
		return nil, err
	}

	// Create a map of snapshot source volumes for quick lookup
	snapshotVolMap := make(map[string]types.SourceVolume, len(sgSnapshot.SourceVolume))
	for _, srcVol := range sgSnapshot.SourceVolume {
		snapshotVolMap[srcVol.Name] = srcVol
	}

	memberSnapshots := make([]*csi.Snapshot, 0, len(volIDList))
	groupReady := true
	creationTime := timestamppb.Now()

	// Process all volumes in the StorageGroup and check if they have the snapshot
	for _, devID := range volIDList {
		srcVol, hasSnapshot := snapshotVolMap[devID]
		if !hasSnapshot {
			groupReady = false
			continue
		}

		// Convert capacity from GB to bytes
		sizeBytes := int64(srcVol.CapacityGb * 1024 * 1024 * 1024)

		memberSnapshots = append(memberSnapshots, &csi.Snapshot{
			SnapshotId: buildMemberSnapshotID(snapName, symID, devID),
			// For volume group snapshots we only have the device ID at this stage.
			// Using the device ID keeps parity with CreateMultiVolumeSnapshot.
			SourceVolumeId: devID,
			SizeBytes:      sizeBytes,
			CreationTime:   creationTime,
			ReadyToUse:     true,
		})
	}

	if len(memberSnapshots) == 0 {
		return nil, status.Errorf(codes.NotFound,
			"group snapshot %s not found on array %s", snapName, symID)
	}

	log.Infof("GetVolumeGroupSnapshot: groupSnapshotId=%s, members=%d, readyToUse=%t",
		groupSnapshotID, len(memberSnapshots), groupReady)

	return &csi.GetVolumeGroupSnapshotResponse{
		GroupSnapshot: &csi.VolumeGroupSnapshot{
			GroupSnapshotId: groupSnapshotID,
			Snapshots:       memberSnapshots,
			CreationTime:    creationTime,
			ReadyToUse:      groupReady,
		},
	}, nil
}

// CreateMultiVolumeSnapshot creates snapshots for multiple volumes in a single API call
// This creates a consistency/storage group snapshot of all volumes using the CreateSnapshot API
func (s *service) CreateMultiVolumeSnapshot(ctx context.Context, symID, snapName, sgName string, volDetails []*types.Volume, devIDToCSIVolID map[string]string, pmaxClient pmax.Pmax, creationTime *timestamppb.Timestamp) ([]*csi.Snapshot, error) {
	log := log.WithContext(ctx)

	// Build the source list with all volumes
	sourceList := make([]types.VolumeList, 0, len(volDetails))
	for _, vol := range volDetails {
		sourceList = append(sourceList, types.VolumeList{Name: vol.VolumeID})
	}

	// Create snapshot for all volumes in a single call
	err := pmaxClient.CreateSnapshot(ctx, symID, snapName, sourceList, 0)
	if err != nil {
		return nil, fmt.Errorf("CreateGroupSnapshot failed with error: %s", err.Error())
	}

	// Get snapshot details to retrieve volume capacities
	sgSnapshot, err := s.getStorageGroupSnapshotDetails(ctx, pmaxClient, symID, sgName, snapName)
	if err != nil {
		return nil, err
	}

	// Build volume capacity map from snapshot response
	capacityMap := make(map[string]float64)
	for _, srcVol := range sgSnapshot.SourceVolume {
		capacityMap[srcVol.Name] = srcVol.CapacityGb
	}

	// Build member snapshot responses using capacities from snapshot response
	memberSnapshots := make([]*csi.Snapshot, 0, len(volDetails))
	for _, vol := range volDetails {
		volSnapID := buildMemberSnapshotID(snapName, symID, vol.VolumeID)
		srcVolID := devIDToCSIVolID[vol.VolumeID]
		if srcVolID == "" {
			srcVolID = vol.VolumeID
		}

		// Use capacity from snapshot response instead of volDetails
		capacityGb, exists := capacityMap[vol.VolumeID]
		if !exists {
			return nil, fmt.Errorf("volume %s not found in snapshot response", vol.VolumeID)
		}
		sizeBytes := int64(capacityGb * 1024 * 1024 * 1024)

		memberSnapshots = append(memberSnapshots, &csi.Snapshot{
			SnapshotId:     volSnapID,
			SourceVolumeId: srcVolID,
			SizeBytes:      sizeBytes,
			CreationTime:   creationTime,
			ReadyToUse:     true,
		})
	}

	log.Infof("Created group (multi-volume) snapshot: snapName=%s, volumes=%d", snapName, len(volDetails))
	return memberSnapshots, nil
}

// --- Helper functions ---

type groupVolInfo struct {
	csiVolID string
	volName  string
	symID    string
	devID    string
}

func (s *service) parseAndValidateGroupSourceVolumes(sourceVolumeIDs []string) ([]groupVolInfo, string, error) {
	vols := make([]groupVolInfo, 0, len(sourceVolumeIDs))
	var commonSymID string
	for _, srcVolID := range sourceVolumeIDs {
		volName, symID, devID, remoteSymID, remoteVolID, err := s.parseCsiID(srcVolID)
		if err != nil {
			return nil, "", status.Errorf(codes.InvalidArgument, "failed to parse volume ID: %s", srcVolID)
		}
		if remoteSymID != "" && remoteVolID != "" {
			return nil, "", status.Errorf(codes.InvalidArgument,
				"group snapshots are not supported on PowerMax metro volumes: %s", srcVolID)
		}
		if commonSymID == "" {
			commonSymID = symID
		} else if commonSymID != symID {
			return nil, "", status.Error(codes.InvalidArgument,
				"all source volumes must belong to the same PowerMax array")
		}
		vols = append(vols, groupVolInfo{csiVolID: srcVolID, volName: volName, symID: symID, devID: devID})
	}
	return vols, commonSymID, nil
}

func (s *service) getPmaxClient(symID string) (pmax.Pmax, error) {
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument,
			"failed to get PowerMax client for array %s: %s", symID, err.Error())
	}
	return pmaxClient, nil
}

func (s *service) getStorageGroupSnapshotDetails(ctx context.Context, pmaxClient pmax.Pmax, symID, sgName, snapName string) (*types.StorageGroupSnap, error) {
	// Get the storage group snapshot information
	sgSnapIDs, err := pmaxClient.GetStorageGroupSnapshotSnapIDs(ctx, symID, sgName, snapName)
	if err != nil {
		if types.IsNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound,
				"Group snapshot %s not found on array %s", snapName, symID)
		}
		return nil, status.Errorf(codes.Internal,
			"failed to get storage group snapshot IDs: %s", err.Error())
	}

	// If no snapshot IDs exist, snapshot doesn't exist
	if len(sgSnapIDs.SnapIDs) == 0 {
		return nil, status.Errorf(codes.NotFound,
			"group snapshot %s not found on array %s", snapName, symID)
	}

	// Get the storage group snapshot details using the first snap ID
	snapID := strconv.FormatInt(sgSnapIDs.SnapIDs[0], 10)
	sgSnapshot, err := pmaxClient.GetStorageGroupSnapshotSnap(ctx, symID, sgName, snapName, snapID)
	if err != nil {
		if types.IsNotFoundError(err) {
			return nil, status.Errorf(codes.NotFound,
				"Group snapshot details %s not found on array %s", snapName, symID)
		}
		return nil, status.Errorf(codes.Internal,
			"failed to get storage group snapshot details: %s", err.Error())
	}

	if sgSnapshot == nil {
		return nil, status.Errorf(codes.NotFound,
			"group snapshot %s not found on array %s", snapName, symID)
	}

	return sgSnapshot, nil
}

func (s *service) validateSnapshotLicense(ctx context.Context, symID string, pmaxClient pmax.Pmax) error {
	if err := s.IsSnapshotLicensed(ctx, symID, pmaxClient); err != nil {
		return status.Errorf(codes.FailedPrecondition,
			"Snapshot is not licensed on array %s: %s", symID, err.Error())
	}
	return nil
}

func (s *service) getGroupVolumeDetails(ctx context.Context, pmaxClient pmax.Pmax, symID string, vols []groupVolInfo) ([]*types.Volume, map[string]string, string, error) {
	if len(vols) == 0 {
		return nil, nil, "", status.Error(codes.InvalidArgument, "no volumes provided")
	}

	// Get SG name from first volume only
	firstVol, err := pmaxClient.GetVolumeByID(ctx, symID, vols[0].devID)
	if err != nil {
		if types.IsNotFoundError(err) {
			return nil, nil, "", status.Errorf(codes.NotFound,
				"volume %s not found on array %s", vols[0].devID, symID)
		}
		return nil, nil, "", status.Errorf(codes.Internal,
			"failed to get volume %s on array %s: %s", vols[0].devID, symID, err.Error())
	}

	sgName, err := resolveStorageGroup([]*types.Volume{firstVol}, s.getClusterPrefix())
	if err != nil {
		return nil, nil, "", err
	}

	// Get all volumes in the SG to validate all requested volumes are present
	sgVolIDs, err := pmaxClient.GetVolumeIDListInStorageGroup(ctx, symID, sgName)
	if err != nil {
		if types.IsNotFoundError(err) {
			return nil, nil, "", status.Errorf(codes.NotFound,
				"StorageGroup %s not found on array %s", sgName, symID)
		}
		return nil, nil, "", status.Errorf(codes.Internal,
			"failed to get volume list from StorageGroup %s on array %s: %s", sgName, symID, err.Error())
	}

	// Validate all requested volumes are in the SG
	sgVolSet := make(map[string]bool, len(sgVolIDs))
	for _, volID := range sgVolIDs {
		sgVolSet[volID] = true
	}

	for _, v := range vols {
		if !sgVolSet[v.devID] {
			return nil, nil, "", status.Errorf(codes.FailedPrecondition,
				"volume %s is not in storage group %s", v.devID, sgName)
		}
	}

	// Return minimal volDetails (we'll get capacities from snapshot response later)
	volDetails := make([]*types.Volume, 0, len(vols))
	devIDToCSIVolID := make(map[string]string, len(vols))
	for _, v := range vols {
		volDetails = append(volDetails, &types.Volume{VolumeID: v.devID})
		devIDToCSIVolID[v.devID] = v.csiVolID
	}

	return volDetails, devIDToCSIVolID, sgName, nil
}

// buildGroupSnapshotID constructs the opaque group snapshot ID.
// Format: {symID}/{sgName}/{snapName}
func buildGroupSnapshotID(symID, sgName, snapName string) string {
	return fmt.Sprintf("%s/%s/%s", symID, sgName, snapName)
}

// buildGroupSnapName returns a CSI-compliant name that keeps the csi-<clusterPrefix>-grp- prefix
// intact and appends a trimmed suffix derived from request Name after removing the "groupsnapshot-" prefix.
// The suffix is collapsed to a single hyphen when multiple appear consecutively and is truncated so the
// total length stays below MaxSnapIdentifierLength.
func buildGroupSnapName(clusterPrefix, reqName string) string {
	// Format: csi-<clusterPrefix>-grp-<shortSuffix>
	// NOTE: Some PowerMax endpoints enforce snapshot identifiers to be *strictly* less than
	// MaxSnapIdentifierLength (not <=). Reserve 1 character to guarantee that.
	prefix := fmt.Sprintf("%s%s-grp-", CsiVolumePrefix, clusterPrefix)
	maxTotalLen := MaxSnapIdentifierLength - 1
	maxSuffixLen := maxTotalLen - len(prefix)
	if maxSuffixLen <= 0 {
		return truncateString(prefix, maxTotalLen)
	}

	suffix := strings.TrimPrefix(reqName, "groupsnapshot-")
	suffix = strings.Trim(suffix, "-")
	if len(suffix) > maxSuffixLen {
		suffix = suffix[:maxSuffixLen]
		suffix = strings.TrimRight(suffix, "-")
	}
	return fmt.Sprintf("%s%s", prefix, suffix)
}

// parseGroupSnapshotID splits a group snapshot ID into its components.
func parseGroupSnapshotID(id string) (symID, sgName, snapName string, err error) {
	parts := strings.Split(id, "/")
	if len(parts) != 3 {
		return "", "", "", fmt.Errorf("expected format symID/sgName/snapName, got %q", id)
	}
	return parts[0], parts[1], parts[2], nil
}

// buildMemberSnapshotID constructs the per-member snapshot ID.
// Format: {snapName}-{symID}-{devID}
func buildMemberSnapshotID(snapName, symID, devID string) string {
	return fmt.Sprintf("%s-%s-%s", snapName, symID, devID)
}

// checkGroupSnapshotIdempotency checks whether the snapshot already exists on
// the backend volumes. It returns:
//   - (response, nil) if the snapshot exists on ALL volumes (idempotent success).
//   - (nil, error)    if it exists on SOME but not all (partial / conflict).
//   - (nil, nil)      if it exists on NONE (caller should proceed to create).
func (s *service) checkGroupSnapshotIdempotency(
	ctx context.Context,
	pmaxClient pmax.Pmax,
	symID, snapName, groupSnapshotID, sgName string,
	volDetails []*types.Volume,
	devIDToCSIVolID map[string]string,
) (*csi.CreateVolumeGroupSnapshotResponse, error) {
	// First get the storage group snapshot snap IDs
	sgSnapIDs, err := pmaxClient.GetStorageGroupSnapshotSnapIDs(ctx, symID, sgName, snapName)
	if err != nil {
		if types.IsNotFoundError(err) {
			// Snapshot doesn't exist, return nil so caller can create it
			return nil, nil
		}
		// For non-NotFound errors, we can't determine if snapshot exists
		// Return error to let caller handle the failure appropriately
		return nil, status.Errorf(codes.Internal,
			"failed to check group snapshot idempotency: %s", err.Error())
	}

	if sgSnapIDs == nil || len(sgSnapIDs.SnapIDs) == 0 {
		// No storage group snapshot found
		return nil, nil
	}

	// Get the storage group snapshot details using the first snap ID
	snapID := strconv.FormatInt(sgSnapIDs.SnapIDs[0], 10)
	sgSnapshot, err := pmaxClient.GetStorageGroupSnapshotSnap(ctx, symID, sgName, snapName, snapID)
	if err != nil {
		if types.IsNotFoundError(err) {
			// Snapshot details don't exist, return nil so caller can create it
			return nil, nil
		}
		// For non-NotFound errors, we can't determine if snapshot exists
		// Return error to let caller handle the failure appropriately
		return nil, status.Errorf(codes.Internal,
			"failed to check group snapshot idempotency: %s", err.Error())
	}
	if sgSnapshot == nil {
		// No storage group snapshot found
		return nil, nil
	}

	// Check if the storage group snapshot includes all volumes
	if len(sgSnapshot.SourceVolume) == 0 {
		// No source volumes found
		return nil, nil
	}

	if len(sgSnapshot.SourceVolume) < len(volDetails) {
		return nil, status.Errorf(codes.AlreadyExists,
			"group snapshot name %q already exists on %d of %d volumes in SG %s; "+
				"cannot create with different source volumes",
			snapName, len(sgSnapshot.SourceVolume), len(volDetails), sgName)
	}

	// All volumes already have this snapshot — rebuild the response.
	// Use volume capacities from the snapshot response instead of volDetails
	creationTime := timestamppb.Now()

	// Build volume capacity map from snapshot response
	capacityMap := make(map[string]float64)
	for _, srcVol := range sgSnapshot.SourceVolume {
		capacityMap[srcVol.Name] = srcVol.CapacityGb
	}

	memberSnapshots := make([]*csi.Snapshot, 0, len(volDetails))
	for _, vol := range volDetails {
		srcVolID := devIDToCSIVolID[vol.VolumeID]
		if srcVolID == "" {
			srcVolID = vol.VolumeID
		}

		// Use capacity from snapshot response
		capacityGb, exists := capacityMap[vol.VolumeID]
		if !exists {
			return nil, status.Errorf(codes.Internal,
				"volume %s not found in existing snapshot response", vol.VolumeID)
		}
		sizeBytes := int64(capacityGb * 1024 * 1024 * 1024)

		memberSnapshots = append(memberSnapshots, &csi.Snapshot{
			SnapshotId:     buildMemberSnapshotID(snapName, symID, vol.VolumeID),
			SourceVolumeId: srcVolID,
			SizeBytes:      sizeBytes,
			CreationTime:   creationTime,
			ReadyToUse:     true,
		})
	}

	return &csi.CreateVolumeGroupSnapshotResponse{
		GroupSnapshot: &csi.VolumeGroupSnapshot{
			GroupSnapshotId: groupSnapshotID,
			Snapshots:       memberSnapshots,
			CreationTime:    creationTime,
			ReadyToUse:      true,
		},
	}, nil
}

// resolveStorageGroup determines which StorageGroup to use for the group snapshot.
// Returns the SG name, whether it's a temporary SG that needs creation, and any error.
func resolveStorageGroup(volumes []*types.Volume, clusterPrefix string) (string, error) {
	csiSGPrefix := fmt.Sprintf("%s-%s-", CSIPrefix, clusterPrefix)

	var commonSG string
	for _, vol := range volumes {
		var candidate string
		for _, sg := range vol.StorageGroupIDList {
			if strings.HasPrefix(sg, csiSGPrefix) {
				candidate = sg
				break
			}
		}
		if candidate == "" {
			return "", status.Error(codes.FailedPrecondition,
				"volume must already belong to a CSI-managed storage group")
		}
		if commonSG == "" {
			commonSG = candidate
		} else if candidate != commonSG {
			return "", status.Error(codes.FailedPrecondition,
				"volumes have inconsistent StorageGroup membership; all must be in the same SG")
		}
	}
	if commonSG == "" {
		return "", status.Error(codes.FailedPrecondition,
			"volumes must belong to a CSI-managed storage group")
	}
	return commonSG, nil
}

// isSnapshotReady checks if any of the snapshot state strings contain "Established".
func isSnapshotReady(states []string) bool {
	for _, state := range states {
		if strings.Contains(state, "Established") {
			return true
		}
	}
	return false
}
