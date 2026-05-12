package service

import (
	"context"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

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

type u4p104VolumeCreator struct {
	s               U4P104ServiceDeps
	pmaxClient      pmax.Pmax // standard client used for idempotency fallback (GetVolumesByIdentifier)
	pmaxClient104   pmax.Pmax // 10.4-capable client used for CreateVolume
	symmetrixID     string
	reqID           string
	params          map[string]string
	symmIDFoundInAZ bool
	apiVersion      int
}

type legacyVolumeCreator struct {
	s               *service
	pmaxClient      pmax.Pmax
	symmetrixID     string
	reqID           string
	params          map[string]string
	symmIDFoundInAZ bool
	apiVersion      int
}

// ---------------------------------------------------------------------------
// 10.4 volume creation constants
// ---------------------------------------------------------------------------

const (
	volumeStorageGroupActionAdd = "Add"
	createVolumesResponseSelect = "id,identifier,cap_cyl,storage_groups"
)

// ---------------------------------------------------------------------------
// 10.4 volume creation
// ---------------------------------------------------------------------------

func (c *u4p104VolumeCreator) Create(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	log := log.WithContext(ctx)
	var err error
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "create volume request cannot be nil")
	}

	params := req.GetParameters()
	accessibility := req.GetAccessibilityRequirements()

	volName := req.GetName()
	if volName == "" {
		return nil, status.Error(codes.InvalidArgument, "volume name is required")
	}

	// Block capability validation — match legacy behavior
	vcs := req.GetVolumeCapabilities()
	if vcs != nil && accTypeIsBlock(vcs) && !c.s.isBlockEnabled() {
		return nil, status.Error(codes.InvalidArgument, "Block Volume Capability is not supported")
	}

	storagePoolID := c.s.resolveParameter(params, c.symmetrixID, StoragePoolParam, "")
	if storagePoolID == "" {
		return nil, status.Error(codes.InvalidArgument, "A valid SRP parameter is required")
	}
	serviceLevel := c.s.resolveParameter(params, c.symmetrixID, ServiceLevelParam, "Optimized")
	if !isValidSLO(serviceLevel) {
		log.Error("An invalid Service Level parameter was specified")
		return nil, status.Errorf(codes.InvalidArgument, "An invalid Service Level parameter was specified")
	}
	storageGroupName := c.s.resolveParameter(params, c.symmetrixID, StorageGroupParam, "")
	applicationPrefix := c.s.resolveParameter(params, c.symmetrixID, ApplicationPrefixParam, "")
	hostLimitName := c.s.resolveParameter(params, c.symmetrixID, HostLimitNameParam, "")
	hostIOLimitMBSec := c.s.resolveParameter(params, c.symmetrixID, HostIOLimitMBSecParam, "")
	hostIOLimitIOSec := c.s.resolveParameter(params, c.symmetrixID, HostIOLimitIOSecParam, "")
	dynamicDistribution := c.s.resolveParameter(params, c.symmetrixID, DynamicDistributionParam, "")
	namespace := c.s.resolveParameter(params, "", CSIPVCNamespace, "")

	hostIOLimitInfo, err := buildHostIOLimitInfo(hostIOLimitMBSec, hostIOLimitIOSec, dynamicDistribution)
	if err != nil {
		return nil, err
	}

	requiredCylinders, err := computeRequiredCylinders(req.GetCapacityRange())
	if err != nil {
		return nil, err
	}

	clusterPrefix := c.s.getClusterPrefix()
	volumeIdentifier := buildVolumeIdentifier(clusterPrefix, volName, namespace)
	storageGroupName = resolveStorageGroupName(storageGroupName, clusterPrefix, applicationPrefix, serviceLevel, storagePoolID, hostLimitName)

	var (
		snapshotID         string
		srcDevID           string
		contentSourceValue string
	)
	contentSource := req.GetVolumeContentSource()
	if contentSource != nil {
		switch src := contentSource.GetType().(type) {
		case *csi.VolumeContentSource_Snapshot:
			snapshotID, srcDevID, contentSourceValue, err = c.handleSnapshotSource(ctx, contentSource)
			if err != nil {
				return nil, err
			}
		case *csi.VolumeContentSource_Volume:
			srcDevID, contentSourceValue, err = c.handleCloneSource(ctx, src.Volume.GetVolumeId())
			if err != nil {
				return nil, err
			}
		default:
			return nil, status.Error(codes.InvalidArgument, "VolumeContentSource is missing volume and snapshot source")
		}
		// check snapshot is licensed
		if licnErr := c.s.isSnapshotLicensed(ctx, c.symmetrixID, c.pmaxClient); licnErr != nil {
			log.WithContext(ctx).Errorf("Snapshot license check failed on array %s: %v", c.symmetrixID, licnErr)
			return nil, status.Error(codes.Internal, licnErr.Error())
		}
	}

	// Dynamic SG: select the best existing SG or propose a new one based on volume counts.
	// In 10.4 API, we don't need to explicitly create the SG - the API will create it
	// automatically if it doesn't exist using the provided attributes.
	if c.s.isDynamicSGEnabled() {
		dynamicSGName, _, err := c.s.getDynamicSG(ctx, c.symmetrixID, storageGroupName)
		if err != nil {
			log.Errorf("Failed to get dynamic SG for array %s: %v", c.symmetrixID, err)
			return nil, status.Errorf(codes.Internal, "failed to get dynamic storage group: %s", err.Error())
		}
		log.Infof("Dynamic SG enabled: selected storage group %s (base: %s) for array %s", dynamicSGName, storageGroupName, c.symmetrixID)
		storageGroupName = dynamicSGName
	}

	log.Infof("Attempting 10.4 CreateVolume for volume %s (identifier: %s) on array %s, %d cylinders",
		volName, volumeIdentifier, c.symmetrixID, requiredCylinders)

	// Single 10.4 API call: creates the volume, adds it to the SG (creating the SG if needed),
	// sets the identifier, and validates SRP capacity — all atomically.
	// Native idempotency: supplying volume.identifier alongside create_new causes the array to
	// return 200 and either the existing volume object (if re-added to SG) or just the SG object
	// (if fully idempotent: volume already exists and is already in the SG).
	// This replaces the legacy pre-flight sequence of:
	//   GetStoragePoolList + GetStoragePool + GetStorageGroup/CreateStorageGroup +
	//   GetVolumeIDList + GetVolumeByID + CreateVolumeInStorageGroupS  (5-7 calls → 1 call)
	var gpmaxReq *types.CreateVolumesRequest
	if snapshotID != "" {
		gpmaxReq = buildCreateVolumeFromSnapshotRequest(requiredCylinders, snapshotID, storagePoolID, serviceLevel, storageGroupName, volumeIdentifier, hostIOLimitInfo, c.reqID)
	} else if srcDevID != "" {
		gpmaxReq = buildCloneVolumesRequest(requiredCylinders, srcDevID, storagePoolID, serviceLevel, storageGroupName, volumeIdentifier, hostIOLimitInfo, c.reqID)
	} else {
		gpmaxReq = buildCreateVolumesRequest(requiredCylinders, storagePoolID, serviceLevel, storageGroupName, volumeIdentifier, hostIOLimitInfo, c.reqID)
	}

	// CSI specific metada for authorization metadata headers (PV name, PVC name, PVC namespace)
	headerMetadata := addMetaData(params)

	gpmaxResp, err := c.pmaxClient104.CreateVolume(ctx, c.symmetrixID, *gpmaxReq, headerMetadata)
	if err != nil {
		log.Errorf("10.4 CreateVolume failed for volume %s on array %s: %v", volName, c.symmetrixID, err)
		return nil, classifyCreateVolumeError(err)
	}

	if gpmaxResp == nil || len(gpmaxResp.Results.Result) == 0 {
		log.Errorf("10.4 CreateVolume response is empty for volume %s on array %s", volName, c.symmetrixID)
		return nil, status.Error(codes.Internal, "create volume response is empty")
	}

	result := gpmaxResp.Results.Result[0]
	if result.Volume == nil {
		// Fully idempotent: volume already exists and is already a member of the SG.
		// The array returns only the storage_group object, not the volume object.

		if result.StorageGroup != nil && result.StorageGroup.ID != "" {
			storageGroupName = result.StorageGroup.ID
		}
		// Fall back to GetVolumesByIdentifier to obtain the device ID and size.
		log.Infof("10.4 CreateVolume: fully idempotent for volume %s on array %s, performing lookup", volumeIdentifier, c.symmetrixID)
		return c.handleIdempotentVolume(ctx, volumeIdentifier, storageGroupName, serviceLevel, storagePoolID, requiredCylinders, contentSourceValue, contentSource, accessibility)
	}

	devID := result.Volume.ID
	if devID == "" {
		log.Errorf("10.4 CreateVolume returned volume object with empty device ID for volume %s on array %s", volName, c.symmetrixID)
		return nil, status.Error(codes.Internal, "create volume response contains empty device ID")
	}
	log.Infof("10.4 CreateVolume succeeded for volume %s on array %s with device ID %s (status: %s)",
		volName, c.symmetrixID, devID, result.Status)

	// Use the array-returned identifier for VolumeId (may include a tenant prefix).
	if result.Volume.Identifier != "" {
		volumeIdentifier = result.Volume.Identifier
	}

	// The top-level storage_group object in the response always identifies the target SG,
	// regardless of how many SGs the volume belongs to.
	if result.StorageGroup != nil && result.StorageGroup.ID != "" {
		storageGroupName = result.StorageGroup.ID
	}

	// Build VolumeId in the same format as legacy: volumeIdentifier-symID-devID
	csiVolumeID := fmt.Sprintf("%s-%s-%s", volumeIdentifier, c.symmetrixID, devID)

	volumeContext := buildVolumeContext(c.s.getReplicationContextPrefix(), c.symmetrixID, serviceLevel, storagePoolID, storageGroupName, result.Volume.CapCyl, contentSourceValue)

	// Add zone labels to volume context when array was found via Availability Zone
	if c.symmIDFoundInAZ {
		for k, v := range c.s.getStorageArrayLabels(c.symmetrixID) {
			volumeContext[k] = v
		}
	}

	resp := &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId:      csiVolumeID,
			CapacityBytes: int64(requiredCylinders) * cylinderSizeInBytes,
			VolumeContext: volumeContext,
			ContentSource: contentSource,
		},
	}
	if accessibility != nil {
		resp.Volume.AccessibleTopology = accessibility.Preferred
	}
	return resp, nil
}

// classifyCreateVolumeError inspects the error returned by the 10.4 CreateVolume
// API and maps well-known error codes/messages to appropriate CSI gRPC status errors.
// Error codes from the PowerMax REST API:
//
//	0x020e0105 – size mismatch (idempotent volume or target < source)
//	0x020e0114 – source volume does not exist (clone)
//	0x020e0117 – snapshot not found (snapshot restore)
func classifyCreateVolumeError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()

	// Idempotent volume creation with size mismatch
	if strings.Contains(msg, "does not match existing volume size") {
		return status.Errorf(codes.AlreadyExists,
			"A volume with the same name exists but has a different size: %s", msg)
	}

	// Clone / snapshot restore: requested target smaller than source
	if strings.Contains(msg, "cannot be smaller than source volume size") {
		return status.Errorf(codes.InvalidArgument,
			"Requested capacity is smaller than the source")
	}

	// Clone source volume does not exist
	if strings.Contains(msg, "Source Volume") && strings.Contains(msg, "does not exist") {
		return status.Errorf(codes.InvalidArgument,
			"Volume content source couldn't be found in the array: %s", msg)
	}

	// Snapshot not found
	if strings.Contains(msg, "No Snapshot found") {
		return status.Errorf(codes.InvalidArgument,
			"Snapshot not found on the array: %s", msg)
	}

	// Default: wrap as Internal
	return status.Errorf(codes.Internal, "Failed to create volume: %s", msg)
}

// buildVolumeIdentifier builds the volume identifier in the same format as legacy:
// csi-<clusterPrefix>-<shortVolName>[-<namespace>]
func buildVolumeIdentifier(clusterPrefix, volumeName, namespace string) string {
	maxLength := MaxVolIdentifierLength - len(clusterPrefix) - len(clusterPrefix) - len(CsiVolumePrefix) - 1
	shortVolumeName := truncateString(volumeName, maxLength)
	if namespace == "" {
		return CsiVolumePrefix + clusterPrefix + "-" + shortVolumeName
	}
	return CsiVolumePrefix + clusterPrefix + "-" + shortVolumeName + "-" + namespace
}

func resolveStorageGroupName(currentSGName, clusterPrefix, applicationPrefix, serviceLevel, storagePoolID, hostLimitName string) string {
	if currentSGName != "" {
		return currentSGName
	}

	var storageGroupName string
	if applicationPrefix == "" {
		storageGroupName = fmt.Sprintf("%s-%s-%s-%s-SG", CSIPrefix, clusterPrefix, serviceLevel, storagePoolID)
	} else {
		storageGroupName = fmt.Sprintf("%s-%s-%s-%s-%s-SG", CSIPrefix, clusterPrefix, applicationPrefix, serviceLevel, storagePoolID)
	}
	if hostLimitName != "" {
		storageGroupName = fmt.Sprintf("%s-%s", storageGroupName, hostLimitName)
	}
	return storageGroupName
}

func buildManageVolumeStorageGroupAction(storageGroupName, storagePoolID, serviceLevel string, hostIOLimitInfo *types.HostIOLimitInfo) *types.ManageVolumeStorageGroupAction {
	return &types.ManageVolumeStorageGroupAction{
		Action: volumeStorageGroupActionAdd,
		StorageGroup: types.VolumeStorageGroupParam{
			ID: storageGroupName,
			SRP: &types.VolumeSrpParam{
				ID: storagePoolID,
			},
			ServiceLevel: &types.VolumeServiceLevelParam{
				ID: serviceLevel,
			},
			HostIOLimitInfo: hostIOLimitInfo,
		},
	}
}

func buildCreateVolumesRequest(requiredCylinders int, storagePoolID, serviceLevel, storageGroupName, volumeIdentifier string, hostIOLimitInfo *types.HostIOLimitInfo, reqID string) *types.CreateVolumesRequest {
	return &types.CreateVolumesRequest{
		Volumes: []types.VolumeRequestParam{
			{
				// volume.identifier alongside create_new enables native 10.4 idempotency:
				// the array sets the identifier on creation, and uses it to recognise duplicate
				// requests instead of failing. ManageIdentifier action is NOT used — combining
				// volume.identifier with manage_identifier causes a 500 error.
				Volume: &types.ExistingVolumeRequestParam{Identifier: volumeIdentifier},
				CreateNew: &types.CreateVolumeParam{
					CreateNewFromAttributes: &types.CreateNewFromAttributes{
						CapacityUnit: "CYL",
						VolumeSize:   float64(requiredCylinders),
					},
					PrecheckSrpCapacity: &types.ValidationSrpAction{
						SRP: types.VolumeSrpParam{ID: storagePoolID},
					},
				},
				Actions: &types.VolumeRequestParamActions{
					ManageVolumeStorageGroup: buildManageVolumeStorageGroupAction(storageGroupName, storagePoolID, serviceLevel, hostIOLimitInfo),
				},
				RequestID:      reqID,
				ResponseSelect: createVolumesResponseSelect,
			},
		},
		ExecutionOption: types.ExecutionOptionSynchronous,
	}
}

// buildCreateVolumeFromSnapshotRequest builds the 10.4 CreateVolumes request using
// create_new_from_snapshot with new_volume_attributes for explicit size control.
// It uses the same SG management and identifier-based idempotency as the attributes path.
func buildCreateVolumeFromSnapshotRequest(requiredCylinders int, snapshotID, storagePoolID, serviceLevel, storageGroupName, volumeIdentifier string, hostIOLimitInfo *types.HostIOLimitInfo, reqID string) *types.CreateVolumesRequest {
	return &types.CreateVolumesRequest{
		Volumes: []types.VolumeRequestParam{
			{
				Volume: &types.ExistingVolumeRequestParam{Identifier: volumeIdentifier},
				CreateNew: &types.CreateVolumeParam{
					CreateNewFromSnapshot: &types.CreateNewFromSnapshot{
						Snapshot: types.SnapshotRequestParam{ID: snapshotID},
						NewVolumeAttributes: &types.CreateNewFromAttributes{
							CapacityUnit: "CYL",
							VolumeSize:   float64(requiredCylinders),
						},
					},
					PrecheckSrpCapacity: &types.ValidationSrpAction{
						SRP: types.VolumeSrpParam{ID: storagePoolID},
					},
				},
				Actions: &types.VolumeRequestParamActions{
					ManageVolumeStorageGroup: buildManageVolumeStorageGroupAction(storageGroupName, storagePoolID, serviceLevel, hostIOLimitInfo),
				},
				RequestID:      reqID,
				ResponseSelect: createVolumesResponseSelect,
			},
		},
		ExecutionOption: types.ExecutionOptionSynchronous,
	}
}

// buildCloneVolumesRequest builds a 10.4 CreateVolumes request that creates a new
// volume and copies data from srcDevID.  The request uses create_new_from_attributes
// with an explicit size so the target volume can be equal to or larger than the
// source.  Data is copied via the manage_replication / local / CopyFrom action
// with establish_terminate.
func buildCloneVolumesRequest(requiredCylinders int, srcDevID, storagePoolID, serviceLevel, storageGroupName, volumeIdentifier string, hostIOLimitInfo *types.HostIOLimitInfo, reqID string) *types.CreateVolumesRequest {
	estTerminate := true
	return &types.CreateVolumesRequest{
		Volumes: []types.VolumeRequestParam{
			{
				Volume: &types.ExistingVolumeRequestParam{Identifier: volumeIdentifier},
				CreateNew: &types.CreateVolumeParam{
					CreateNewFromAttributes: &types.CreateNewFromAttributes{
						CapacityUnit: "CYL",
						VolumeSize:   float64(requiredCylinders),
					},
					PrecheckSrpCapacity: &types.ValidationSrpAction{
						SRP: types.VolumeSrpParam{ID: storagePoolID},
					},
				},
				Actions: &types.VolumeRequestParamActions{
					ManageVolumeStorageGroup: buildManageVolumeStorageGroupAction(storageGroupName, storagePoolID, serviceLevel, hostIOLimitInfo),
					ManageReplication: &types.ManageReplicationAction{
						Local: &types.LocalReplicationAction{
							Action: "CopyFrom",
							Volume: types.ExistingVolumeRequestParam{
								ID: srcDevID,
							},
							EstablishTerminate: &estTerminate,
						},
					},
				},
				RequestID:      reqID,
				ResponseSelect: createVolumesResponseSelect,
			},
		},
		ExecutionOption: types.ExecutionOptionSynchronous,
	}
}

func buildHostIOLimitInfo(hostIOLimitMBSec, hostIOLimitIOSec, dynamicDistribution string) (*types.HostIOLimitInfo, error) {
	if hostIOLimitMBSec == "" && hostIOLimitIOSec == "" && dynamicDistribution == "" {
		return nil, nil
	}

	hostIOLimitInfo := &types.HostIOLimitInfo{
		DynamicDistribution: dynamicDistribution,
	}

	if hostIOLimitMBSec != "" {
		mbSec, err := strconv.Atoi(hostIOLimitMBSec)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid %s value: %s", HostIOLimitMBSecParam, hostIOLimitMBSec)
		}
		if mbSec < 0 {
			return nil, status.Errorf(codes.InvalidArgument, "%s must be non-negative", HostIOLimitMBSecParam)
		}
		hostIOLimitInfo.HostIOLimitMBSec = mbSec
	}

	if hostIOLimitIOSec != "" {
		ioSec, err := strconv.Atoi(hostIOLimitIOSec)
		if err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid %s value: %s", HostIOLimitIOSecParam, hostIOLimitIOSec)
		}
		if ioSec < 0 {
			return nil, status.Errorf(codes.InvalidArgument, "%s must be non-negative", HostIOLimitIOSecParam)
		}
		hostIOLimitInfo.HostIOLimitIOSec = ioSec
	}

	return hostIOLimitInfo, nil
}

func buildVolumeContext(replicationContextPrefix, symmetrixID, serviceLevel, storagePoolID, storageGroupName string, capCyl float64, contentSource string) map[string]string {
	return map[string]string{
		ServiceLevelParam: serviceLevel,
		StoragePoolParam:  storagePoolID,
		path.Join(replicationContextPrefix, SymmetrixIDParam): symmetrixID,
		CapacityGB:     fmt.Sprintf("%.2f", capCyl),
		ContentSource:  contentSource,
		StorageGroup:   storageGroupName,
		"CreationTime": time.Now().Format("20060102150405"),
	}
}

// handleSnapshotSource parses, validates, and performs size-check for snapshot restore.
// Returns:
//   - snapshotID: the snap_id from VolumeSnapshotSource (for create_new_from_snapshot.snapshot.id)
//   - srcDevID: the source volume device ID (for size validation)
//   - contentSource: the parsed snapshot name (stored in VolumeContext[ContentSource])
func (c *u4p104VolumeCreator) handleSnapshotSource(ctx context.Context, cs *csi.VolumeContentSource) (snapshotID, srcDevID, contentSource string, err error) {
	if ctx.Err() != nil {
		return "", "", "", status.FromContextError(ctx.Err()).Err()
	}

	if cs == nil || cs.GetSnapshot() == nil {
		return "", "", "", status.Error(codes.InvalidArgument, "snapshot source is required in VolumeContentSource")
	}

	srcSnapID := cs.GetSnapshot().GetSnapshotId()
	if srcSnapID == "" {
		return "", "", "", status.Error(codes.InvalidArgument, "snapshot ID is required in VolumeContentSource")
	}

	// Parse the CSI snapshot ID: format is <snapName>-<symID>-<devID>
	parsedSnapID, symID, devID, _, _, parseErr := c.s.parseCsiID(srcSnapID)
	if parseErr != nil {
		log.WithContext(ctx).Errorf("Snapshot identifier not in supported format: %s", srcSnapID)
		return "", "", "", status.Error(codes.InvalidArgument, "Snapshot identifier not in supported format")
	}

	// Validate the snapshot belongs to the target array
	if symID != c.symmetrixID {
		log.WithContext(ctx).WithFields(map[string]interface{}{
			"snapshot_array": symID,
			"target_array":   c.symmetrixID,
		}).Error("Snapshot is on different PowerMax array")
		return "", "", "", status.Error(codes.InvalidArgument, "The volume content source is in different PowerMax array")
	}

	// Fetch snapshot info to obtain snap_id for 10.4 API
	snapInfo, snapErr := c.pmaxClient.GetSnapshotInfo(ctx, c.symmetrixID, devID, parsedSnapID)
	if snapErr != nil {
		log.WithContext(ctx).WithFields(map[string]interface{}{
			"snapshot_name": parsedSnapID,
			"source_device": devID,
			"array":         c.symmetrixID,
		}).Errorf("Failed to get snapshot info: %v", snapErr)
		return "", "", "", status.Errorf(codes.InvalidArgument, "Snapshot %s not found on source volume %s: %s", parsedSnapID, devID, snapErr.Error())
	}

	if snapInfo == nil || len(snapInfo.VolumeSnapshotSource) == 0 {
		log.WithContext(ctx).Errorf("Snapshot %s has no source generations on volume %s", parsedSnapID, devID)
		return "", "", "", status.Errorf(codes.InvalidArgument, "Snapshot %s has no source generations on volume %s", parsedSnapID, devID)
	}

	// Extract snap_id from the first generation for the 10.4 API's create_new_from_snapshot.snapshot.id field
	snapID := snapInfo.VolumeSnapshotSource[0].SnapID
	if snapID == 0 {
		log.WithContext(ctx).Errorf("Snapshot %s has invalid snap_id on volume %s", parsedSnapID, devID)
		return "", "", "", status.Errorf(codes.Internal, "Snapshot %s has invalid snap_id on volume %s", parsedSnapID, devID)
	}

	// Return snap_id as string for the 10.4 API, devID for tracking, and parsedSnapID for VolumeContext
	return fmt.Sprintf("%d", snapID), devID, parsedSnapID, nil
}

// handleCloneSource parses, validates, and performs size-check for volume clone.
// Returns:
//   - srcDevID: the source volume device ID
//   - contentSource: the full CSI volume ID (stored in VolumeContext[ContentSource])
func (c *u4p104VolumeCreator) handleCloneSource(ctx context.Context, srcVolID string) (srcDevID string, contentSource string, err error) {
	if ctx.Err() != nil {
		return "", "", status.FromContextError(ctx.Err()).Err()
	}

	if srcVolID == "" {
		return "", "", status.Error(codes.InvalidArgument, "Source volume ID is required for cloning")
	}

	_, srcSymID, parsedDevID, _, _, parseErr := c.s.parseCsiID(srcVolID)
	if parseErr != nil {
		log.WithContext(ctx).Errorf("Could not parse source CSI VolumeId: %s", srcVolID)
		return "", "", status.Error(codes.InvalidArgument, "Source volume identifier not in supported format")
	}

	if srcSymID != c.symmetrixID {
		log.WithContext(ctx).WithFields(map[string]interface{}{
			"source_array": srcSymID,
			"target_array": c.symmetrixID,
		}).Error("Source volume is on different PowerMax array")
		return "", "", status.Error(codes.InvalidArgument, "Source volume must be on the same PowerMax array for cloning")
	}

	return parsedDevID, srcVolID, nil
}

// computeRequiredCylinders converts a CSI CapacityRange into a cylinder count.
// This is pure local math — no API calls. SRP capacity is validated server-side
// by precheck_srp_capacity in the 10.4 CreateVolume request.
func computeRequiredCylinders(cr *csi.CapacityRange) (int, error) {
	var minSizeBytes, maxSizeBytes int64
	if cr != nil {
		minSizeBytes = cr.GetRequiredBytes()
		maxSizeBytes = cr.GetLimitBytes()
	}
	if minSizeBytes < 0 || maxSizeBytes < 0 {
		return 0, status.Errorf(codes.OutOfRange,
			"bad capacity: requested volume size bytes %d and limit size bytes %d must not be negative",
			minSizeBytes, maxSizeBytes)
	}
	if minSizeBytes == 0 {
		minSizeBytes = DefaultVolumeSizeBytes
	}
	if minSizeBytes < MinVolumeSizeBytes {
		minSizeBytes = MinVolumeSizeBytes
	}
	numOfCylinders := int(minSizeBytes / cylinderSizeInBytes)
	if minSizeBytes%cylinderSizeInBytes > 0 {
		numOfCylinders++
	}
	sizeInBytes := int64(numOfCylinders) * cylinderSizeInBytes
	if maxSizeBytes > 0 && sizeInBytes > maxSizeBytes {
		return 0, status.Errorf(codes.OutOfRange,
			"bad capacity: size in bytes %d exceeds limit size bytes %d", sizeInBytes, maxSizeBytes)
	}
	return numOfCylinders, nil
}

// handleIdempotentVolume is called when the 10.4 CreateVolume API returns 200 but no volume
// object (fully idempotent: volume already exists and is already in the SG). It looks up the
// volume by identifier to obtain the device ID needed to build the CSI VolumeId.
// SG membership and size validation are omitted: the array already enforces both (a size
// mismatch returns 500 before reaching this path, and the SG was confirmed by the 200 response).
func (c *u4p104VolumeCreator) handleIdempotentVolume(ctx context.Context, volumeIdentifier, storageGroupName, serviceLevel, storagePoolID string, requiredCylinders int, volContent string, contentSource *csi.VolumeContentSource, accessibility *csi.TopologyRequirement) (*csi.CreateVolumeResponse, error) {
	log := log.WithContext(ctx)
	volumeList, err := c.pmaxClient.GetVolumesByIdentifier(ctx, c.symmetrixID, volumeIdentifier)
	if err != nil {
		log.Errorf("10.4 idempotency lookup failed for volume %s on array %s: %v", volumeIdentifier, c.symmetrixID, err)
		return nil, status.Errorf(codes.Internal, "idempotency check failed: %s", err.Error())
	}

	for _, eachVol := range volumeList.Volumes {
		log.Infof("10.4 idempotent volume detected %s on array %s, returning success", eachVol.ID, c.symmetrixID)

		csiVolumeID := fmt.Sprintf("%s-%s-%s", eachVol.Identifier, c.symmetrixID, eachVol.ID)
		volumeContext := buildVolumeContext(c.s.getReplicationContextPrefix(), c.symmetrixID, serviceLevel, storagePoolID, storageGroupName, eachVol.CapCyl, volContent)

		// Add zone labels to volume context when array was found via Availability Zone
		if c.symmIDFoundInAZ {
			for k, v := range c.s.getStorageArrayLabels(c.symmetrixID) {
				volumeContext[k] = v
			}
		}

		resp := &csi.CreateVolumeResponse{
			Volume: &csi.Volume{
				VolumeId:      csiVolumeID,
				CapacityBytes: int64(requiredCylinders) * cylinderSizeInBytes,
				VolumeContext: volumeContext,
				ContentSource: contentSource,
			},
		}
		if accessibility != nil {
			resp.Volume.AccessibleTopology = accessibility.Preferred
		}
		return resp, nil
	}

	return nil, status.Errorf(codes.Internal,
		"volume with identifier %s reported as existing but not found on array %s", volumeIdentifier, c.symmetrixID)
}

// ---------------------------------------------------------------------------
// Legacy volume creation (pre-10.4)
// ---------------------------------------------------------------------------

func (c *legacyVolumeCreator) Create(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	log := log.WithContext(ctx)

	reqID := c.reqID
	params := c.params
	symmetrixID := c.symmetrixID
	pmaxClient := c.pmaxClient
	symmIDFoundInAZ := c.symmIDFoundInAZ
	version := c.apiVersion

	accessibility := req.GetAccessibilityRequirements()

	thick := params[ThickVolumesParam]
	applicationPrefix := c.s.resolveParameter(params, symmetrixID, ApplicationPrefixParam, "")

	// Storage (resource) Pool. Validate it against exist Pools
	storagePoolID := c.s.resolveParameter(params, symmetrixID, StoragePoolParam, "")
	err := c.s.validateStoragePoolID(ctx, symmetrixID, storagePoolID, pmaxClient)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Errorf(codes.InvalidArgument, "%s", err.Error())
	}

	// SLO is optional
	serviceLevel := c.s.resolveParameter(params, symmetrixID, ServiceLevelParam, "Optimized")
	found := false
	for _, val := range validSLO {
		if serviceLevel == val {
			found = true
			break
		}
	}
	if !found {
		return nil, status.Errorf(codes.InvalidArgument, "An invalid Service Level parameter was specified")
	}

	storageGroupName := c.s.resolveParameter(params, symmetrixID, StorageGroupParam, "")
	hostLimitName := c.s.resolveParameter(params, symmetrixID, HostLimitNameParam, "")
	hostMBsec := c.s.resolveParameter(params, symmetrixID, HostIOLimitMBSecParam, "")
	hostIOsec := c.s.resolveParameter(params, symmetrixID, HostIOLimitIOSecParam, "")
	hostDynDistribution := c.s.resolveParameter(params, symmetrixID, DynamicDistributionParam, "")
	namespace := c.s.resolveParameter(params, "", CSIPVCNamespace, "")

	// Dynamic SG check is available only from 10.1
	if c.s.opts.dynamicSGEnabled && version < 101 {
		log.Errorf("Dynamic SG is enabled, but not supported for array %s with version %d. Minimum expected array version is 10.1", symmetrixID, version)
		return nil, status.Errorf(codes.Internal, "Dynamic SG is enabled, but not supported for array %s with version %d. Minimum expected array version is 10.1", symmetrixID, version)
	}

	// File related params
	useNFS := false
	nasServer := ""
	allowRoot := ""
	if params[NASServerName] != "" {
		nasServer = params[NASServerName]
	}
	if params[AllowRootParam] != "" {
		allowRoot = params[AllowRootParam]
	}

	// Validate volume capabilities
	vcs := req.GetVolumeCapabilities()
	if vcs != nil {
		isBlock := accTypeIsBlock(vcs)
		if isBlock && !c.s.opts.EnableBlock {
			return nil, status.Error(codes.InvalidArgument, "Block Volume Capability is not supported")
		}
		useNFS = accTypeIsNFS(vcs)
		if isBlock && useNFS {
			return nil, status.Errorf(codes.InvalidArgument, "NFS with Block is not supported")
		}
	}

	// Remote Replication based paramsMes
	var replicationEnabled string
	var remoteSymID string
	var localRDFGrpNo string
	var remoteRDFGrpNo string
	var remoteServiceLevel string
	var remoteSRPID string
	var repMode string
	var bias string

	if params[path.Join(c.s.opts.ReplicationPrefix, RepEnabledParam)] == "true" {
		if c.s.opts.IsVsphereEnabled {
			return nil, status.Errorf(codes.Unavailable, "Replication on a vSphere volume is not supported")
		}
		if useNFS {
			return nil, status.Errorf(codes.Unavailable, "Replication on a NFS volume is not supported")
		}
		replicationEnabled = params[path.Join(c.s.opts.ReplicationPrefix, RepEnabledParam)]
		// remote symmetrix ID and rdf group name are mandatory params when replication is enabled
		remoteSymID = params[path.Join(c.s.opts.ReplicationPrefix, RemoteSymIDParam)]
		// check if storage class contains SRDG details
		if params[path.Join(c.s.opts.ReplicationPrefix, LocalRDFGroupParam)] != "" {
			localRDFGrpNo = params[path.Join(c.s.opts.ReplicationPrefix, LocalRDFGroupParam)]
		}
		if params[path.Join(c.s.opts.ReplicationPrefix, RemoteRDFGroupParam)] != "" {
			remoteRDFGrpNo = params[path.Join(c.s.opts.ReplicationPrefix, RemoteRDFGroupParam)]
		}
		repMode = params[path.Join(c.s.opts.ReplicationPrefix, ReplicationModeParam)]

		if symmIDFoundInAZ && repMode == Metro {
			return nil, status.Errorf(codes.InvalidArgument, "The use of Availability Zones with Metro volumes is not supported")
		}

		remoteServiceLevel = params[path.Join(c.s.opts.ReplicationPrefix, RemoteServiceLevelParam)]
		remoteSRPID = params[path.Join(c.s.opts.ReplicationPrefix, RemoteSRPParam)]
		bias = params[path.Join(c.s.opts.ReplicationPrefix, BiasParam)]

		// Get Local and remote RDFg Numbers from a rest call
		// Create RDFg for a namespace if it doens't exist?
		// Create RDFg when the volume gets added first time for a replication sssn
		if localRDFGrpNo == "" && remoteRDFGrpNo == "" {
			localRDFGrpNo, remoteRDFGrpNo, err = c.s.GetOrCreateRDFGroup(ctx, symmetrixID, remoteSymID, repMode, namespace, pmaxClient)
			if err != nil {
				return nil, status.Errorf(codes.NotFound, "Received error get/create RDFG, err: %s", err.Error())
			}
			if localRDFGrpNo == "" || remoteRDFGrpNo == "" {
				return nil, status.Errorf(codes.Unavailable, "Can not fetch RDF Group for volume creation get/create RDFG")
			}
			log.Debugf("RDF group for given array pair and RDF mode: local(%s), remote(%s)", localRDFGrpNo, remoteRDFGrpNo)
		}
		if repMode == Metro {
			return c.s.createMetroVolume(ctx, req, reqID, storagePoolID, symmetrixID, storageGroupName, serviceLevel, thick, remoteSymID, localRDFGrpNo, remoteRDFGrpNo, remoteServiceLevel, remoteSRPID, namespace, applicationPrefix, bias, hostLimitName, hostMBsec, hostIOsec, hostDynDistribution)
		}
		if repMode != Async && repMode != Sync {
			log.Errorf("Unsupported Replication Mode: (%s)", repMode)
			return nil, status.Errorf(codes.InvalidArgument, "Unsupported Replication Mode: (%s)", repMode)
		}
	}

	// Get the required capacity
	cr := req.GetCapacityRange()
	requiredCylinders, err := c.s.validateVolSize(ctx, cr, symmetrixID, storagePoolID, pmaxClient)
	if err != nil {
		return nil, err
	}

	var srcVolID, srcSnapID string
	var symID, SrcDevID, snapID string
	var srcVol *types.Volume
	var volContent string
	// When content source is specified, the size of the new volume
	// is determined based on the size of the source volume in the
	// snapshot. The size of the new volume to be created should be
	// greater than or equal to the size of snapshot source
	contentSource := req.GetVolumeContentSource()
	if contentSource != nil {
		if useNFS {
			return nil, status.Errorf(codes.Unavailable, "Cloning on a NFS volume is not supported")
		}
		switch req.GetVolumeContentSource().GetType().(type) {
		case *csi.VolumeContentSource_Volume:
			srcVolID = req.GetVolumeContentSource().GetVolume().GetVolumeId()
			if srcVolID != "" {
				_, symID, SrcDevID, _, _, err = c.s.parseCsiID(srcVolID)
				if err != nil {
					// We couldn't comprehend the identifier.
					log.Error("Could not parse CSI VolumeId: " + srcVolID)
					return nil, status.Error(codes.InvalidArgument, "Source volume identifier not in supported format")
				}
				volContent = srcVolID
			}
			break
		case *csi.VolumeContentSource_Snapshot:
			srcSnapID = req.GetVolumeContentSource().GetSnapshot().GetSnapshotId()
			if srcSnapID != "" {
				snapID, symID, SrcDevID, _, _, err = c.s.parseCsiID(srcSnapID)
				if err != nil {
					// We couldn't comprehend the identifier.
					log.Error("Snapshot identifier not in supported format: " + srcSnapID)
					return nil, status.Error(codes.InvalidArgument, "Snapshot identifier not in supported format")
				}
				volContent = snapID
			}
			break
		default:
			return nil, status.Error(codes.InvalidArgument, "VolumeContentSource is missing volume and snapshot source")
		}
		// check snapshot is licensed
		if err := c.s.IsSnapshotLicensed(ctx, symID, pmaxClient); err != nil {
			log.Error("Error - " + err.Error())
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if SrcDevID != "" && symID != "" {
		if symID != symmetrixID {
			log.Error("The volume content source is in different PowerMax array")
			return nil, status.Errorf(codes.InvalidArgument, "The volume content source is in different PowerMax array")
		}
		srcVol, err = pmaxClient.GetVolumeByID(ctx, symmetrixID, SrcDevID)
		if err != nil {
			log.Error("Volume content source volume couldn't be found in the array: " + err.Error())
			return nil, status.Errorf(codes.InvalidArgument, "Volume content source volume couldn't be found in the array: %s", err.Error())
		}
		// reset the volume size to match with source
		if requiredCylinders < srcVol.CapacityCYL {
			log.Error("Capacity specified is smaller than the source")
			return nil, status.Error(codes.InvalidArgument, "Requested capacity is smaller than the source")
		}
	}

	// Get the volume name
	volumeName := req.GetName()
	if volumeName == "" {
		log.Error("Name cannot be empty")
		return nil, status.Error(codes.InvalidArgument,
			"Name cannot be empty")
	}

	// Get the Volume prefix from environment
	volumePrefix := c.s.getClusterPrefix()
	maxLength := MaxVolIdentifierLength - len(volumePrefix) - len(c.s.getClusterPrefix()) - len(CsiVolumePrefix) - 1
	// First get the short volume name
	shortVolumeName := truncateString(volumeName, maxLength)
	// Form the volume identifier using short volume name and namespace
	var namespaceSuffix string
	if namespace != "" {
		namespaceSuffix = "-" + namespace
	}
	volumeIdentifier := fmt.Sprintf("%s%s-%s%s", CsiVolumePrefix, c.s.getClusterPrefix(), shortVolumeName, namespaceSuffix)

	if useNFS {
		// calculate size in MiB
		reqSizeInMiB := (cr.GetRequiredBytes() + MiBSizeInBytes - 1) / MiBSizeInBytes
		return file.CreateFileSystem(ctx, reqID, accessibility, params, symmetrixID, storagePoolID, serviceLevel, nasServer, volumeIdentifier, allowRoot, reqSizeInMiB, pmaxClient)
	}
	// Storage Group is required to be derived from the parameters (such as service level and storage resource pool which are supplied in parameters)
	// Storage Group Name can optionally be supplied in the parameters (for testing) to over-ride the default.
	if storageGroupName == "" {
		if applicationPrefix == "" {
			storageGroupName = fmt.Sprintf("%s-%s-%s-%s-SG", CSIPrefix, c.s.getClusterPrefix(),
				serviceLevel, storagePoolID)
		} else {
			storageGroupName = fmt.Sprintf("%s-%s-%s-%s-%s-SG", CSIPrefix, c.s.getClusterPrefix(),
				applicationPrefix, serviceLevel, storagePoolID)
		}
		if hostLimitName != "" {
			storageGroupName = fmt.Sprintf("%s-%s", storageGroupName, hostLimitName)
		}
	}

	var dynamicSGName string
	var needCreation bool
	if c.s.opts.dynamicSGEnabled {
		if dynamicSGName, needCreation, err = getDynamicSG(ctx, symmetrixID, storageGroupName, c.s); err != nil {
			log.Error("failed to get dynamic SG: " + err.Error())
			return nil, status.Error(codes.Internal, err.Error())
		}
		log.Infof("####### dynamic storage group name: %s, base SG name: %s, needCreation: %v", dynamicSGName, storageGroupName, needCreation)
		storageGroupName = dynamicSGName
	}

	// localProtectionGroupID refers to name of Storage Group which has protected local volumes
	// remoteProtectionGroupID refers to name of Storage Group which has protected remote volumes
	var localProtectionGroupID string
	var remoteProtectionGroupID string
	if replicationEnabled == "true" {
		localProtectionGroupID = buildProtectionGroupID(namespace, localRDFGrpNo, repMode)
		remoteProtectionGroupID = buildProtectionGroupID(namespace, remoteRDFGrpNo, repMode)
	}

	// log all parameters used in CreateVolume call
	fields := map[string]interface{}{
		"SymmetrixID":                        symmetrixID,
		"SRP":                                storagePoolID,
		"Accessibility":                      accessibility,
		"ApplicationPrefix":                  applicationPrefix,
		"volumeIdentifier":                   volumeIdentifier,
		"requiredCylinders":                  requiredCylinders,
		"storageGroupName":                   storageGroupName,
		"CSIRequestID":                       reqID,
		"SourceVolume":                       srcVolID,
		"SourceSnapshot":                     srcSnapID,
		"ReplicationEnabled":                 replicationEnabled,
		"RemoteSymID":                        remoteSymID,
		"LocalRDFGroup":                      localRDFGrpNo,
		"RemoteRDFGroup":                     remoteRDFGrpNo,
		"SRDFMode":                           repMode,
		"PVCNamespace":                       namespace,
		"LocalProtectionGroupID":             localProtectionGroupID,
		"RemoteProtectionGroupID":            remoteProtectionGroupID,
		HeaderPersistentVolumeName:           params[CSIPersistentVolumeName],
		HeaderPersistentVolumeClaimName:      params[CSIPersistentVolumeClaimName],
		HeaderPersistentVolumeClaimNamespace: params[CSIPVCNamespace],
		HostIOLimitMBSec:                     hostMBsec,
		HostIOLimitIOSec:                     hostIOsec,
		DynamicDistribution:                  hostDynDistribution,
	}
	log.WithFields(fields).Info("Executing CreateVolume with following fields")

	// isSGUnprotected is set to true only if SG has a replica, eg if the SG is new
	isSGUnprotected := false
	if replicationEnabled == "true" {
		sg, err := c.s.getOrCreateProtectedStorageGroup(ctx, symmetrixID, localProtectionGroupID, namespace, localRDFGrpNo, repMode, reqID, pmaxClient)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Error in getOrCreateProtectedStorageGroup: (%s)", err.Error())
		}
		if sg != nil && sg.Rdf == true {
			// Check the direction of SG
			// Creation of replicated volume is allowed in an SG of type R1
			err := c.s.VerifyProtectedGroupDirection(ctx, symmetrixID, localProtectionGroupID, localRDFGrpNo, pmaxClient)
			if err != nil {
				return nil, err
			}
		} else {
			isSGUnprotected = true
		}
	}

	// Check existence of the Storage Group and create if necessary.
	if !c.s.opts.dynamicSGEnabled {
		sg, err := pmaxClient.GetStorageGroup(ctx, symmetrixID, storageGroupName)
		if err != nil || sg == nil {
			log.Debug(fmt.Sprintf("Unable to find storage group: %s", storageGroupName))
			needCreation = true
		}
	}

	if needCreation {
		hostLimitsParam := &types.SetHostIOLimitsParam{
			HostIOLimitMBSec:    hostMBsec,
			HostIOLimitIOSec:    hostIOsec,
			DynamicDistribution: hostDynDistribution,
		}
		optionalPayload := make(map[string]interface{})
		optionalPayload[HostLimits] = hostLimitsParam
		if *hostLimitsParam == (types.SetHostIOLimitsParam{}) {
			optionalPayload = nil
		}
		_, err := pmaxClient.CreateStorageGroup(ctx, symmetrixID, storageGroupName, storagePoolID,
			serviceLevel, thick == "true", optionalPayload)
		if err != nil {
			log.Error("Error creating storage group: " + err.Error())
			return nil, status.Errorf(codes.Internal, "Error creating storage group: %s", err.Error())
		}
	}
	alreadyExists := false
	isLocalVolumePresent := false

	var vol *types.Volume
	var volumeList *types.Volumev1
	isV4 := c.s.isV4OrAbove(ctx, symmetrixID, pmaxClient)

	if version >= APIVersion103 && isV4 {
		log.Debug("API version is greater than or equal to 103. Using enhanced API")
		// Idempotency test. We will read the volume and check for:
		// 1. Existence of a volume with matching volume name
		// 2. Matching cylinderSize
		// 3. Is a member of the storage group
		// 4. Check if snapshot/volume target
		log.Debug("Calling GetVolumeIDList for idempotency test")
		volumeList, err = pmaxClient.GetVolumesByIdentifier(ctx, symmetrixID, volumeIdentifier)
		if err != nil {
			log.Error("Error getting the volumes for idempotence check: " + err.Error())
			return nil, status.Errorf(codes.Internal, "Error  getting the volumes for idempotence check: %s", err.Error())
		}

		// isLocalVolumePresent restrict CreateVolumeInProtectedSG call if the volume is present in local SG but not in remote SG
		// isLocalVolumePresent := false
		// Look up the volume(s), if any, returned for the idempotency check to see if there are any matches
		// We ignore any volume not in the desired storage group (even though they have the same name).
		for _, eachVol := range volumeList.Volumes {
			if len(eachVol.StorageGroups) < 1 {
				log.Error("Idempotence check: StorageGroupIDList is empty for (%s): " + eachVol.ID)
				return nil, status.Errorf(codes.Internal, "Idempotence check: StorageGroupIDList is empty for (%s)", eachVol.ID)
			}
			matchesStorageGroup := false
			for _, sgid := range eachVol.StorageGroups {
				if strings.Contains(sgid.StorageGroupID, storageGroupName) {
					matchesStorageGroup = true
					storageGroupName = sgid.StorageGroupID
				}
			}

			// with Authorization, a tenant prefix is applied to the volume identifier on the array
			// csi-CSM-pmax-69298b3d3d-namespace -> tn1-csi-CSM-pmax-69298b3d3d-namespace
			// since we don't know the tenant prefix, the volume identifier on the array is checked to contain the standard volume identifier
			if matchesStorageGroup && (eachVol.Identifier == volumeIdentifier || strings.Contains(eachVol.Identifier, volumeIdentifier)) {
				// A volume with the same name exists and has the same size
				if eachVol.CapCyl != float64(requiredCylinders) {
					log.Error("A volume with the same name exists but has a different size than required.")
					alreadyExists = true
					continue
				}
				var remoteVolumeID string
				if replicationEnabled == "true" {
					remoteVolumeID, _, err = c.s.GetRemoteVolumeID(ctx, symmetrixID, localRDFGrpNo, eachVol.ID, pmaxClient)
					if err != nil && !strings.Contains(err.Error(), "The device must be an RDF device") {
						return nil, status.Errorf(codes.Internal, "Failed to fetch rdf pair information for (%s) - Error (%s)", eachVol.ID, err.Error())
					}
					if remoteVolumeID == "" {
						// Missing corresponding Remote Volume Name for existing local volume
						// The SG is unprotected as Local volume and Local SG exists but missing corresponding SRDF info
						// If the SG was protected, there must exist a corresponding remote replica volume
						log.Debugf("Local Volume already exist, skipping creation (%s)", eachVol.ID)
						isLocalVolumePresent = true
						vol = &types.Volume{}
						vol.VolumeID = eachVol.ID
						vol.CapacityGB = eachVol.CapCyl
						vol.VolumeIdentifier = eachVol.Identifier
						var sgIDs []string
						for _, sg := range eachVol.StorageGroups {
							if sg.StorageGroupID != "" {
								sgIDs = append(sgIDs, sg.StorageGroupID)
							}
						}
						vol.StorageGroupIDList = sgIDs
						continue
					}
				}
				if volContent != "" {
					if replicationEnabled == "true" {
						if srcSnapID != "" {
							err = c.s.LinkSRDFVolToSnapshot(ctx, reqID, symID, srcVol.VolumeID, snapID, localProtectionGroupID, localRDFGrpNo, vol, bias, false, pmaxClient)
							if err != nil {
								return nil, err
							}
						} else if srcVolID != "" {
							isV4 := c.s.isV4OrAbove(ctx, symID, pmaxClient)
							if isV4 {
								err = c.s.LinkSRDFCloneVolume(ctx, reqID, symID, srcVol, vol, localProtectionGroupID, localRDFGrpNo, "false", pmaxClient)
								if err != nil {
									return nil, status.Errorf(codes.Internal, "Failed to create SRDF volume from volume (%s)", err.Error())
								}
							} else {
								tmpSnapID := fmt.Sprintf("%s%s-%d", TempSnap, c.s.getClusterPrefix(), time.Now().Nanosecond())
								err = c.s.LinkSRDFVolToVolume(ctx, reqID, symID, srcVol, vol, tmpSnapID, localProtectionGroupID, localRDFGrpNo, "false", false, pmaxClient)
								if err != nil {
									return nil, status.Errorf(codes.Internal, "Failed to create SRDF volume from volume (%s)", err.Error())
								}
							}
						}
					} else { // replication is not enabled
						if srcSnapID != "" {
							err = c.s.UnlinkTargets(ctx, symID, SrcDevID, pmaxClient)
							if err != nil {
								return nil, status.Errorf(codes.Internal, "Failed unlink existing target from snapshot (%s)", err.Error())
							}
							err = c.s.LinkVolumeToSnapshot(ctx, symID, srcVol.VolumeID, eachVol.ID, snapID, reqID, false, pmaxClient)
							if err != nil {
								return nil, status.Errorf(codes.Internal, "Failed to create volume from snapshot (%s)", err.Error())
							}
						} else if srcVolID != "" && eachVol.ID != "" {
							isV4 := c.s.isV4OrAbove(ctx, symID, pmaxClient)
							if isV4 {
								replicaRequest := types.ReplicationRequest{
									ReplicationPair: []types.ReplicationPair{
										{
											SourceVolumeName: srcVol.VolumeID,
											TargetVolumeName: eachVol.ID,
										},
									},
									Establish:          true,
									EstablishTerminate: true,
								}
								err = pmaxClient.CloneVolumeFromVolume(ctx, symID, replicaRequest)
								if err != nil {
									return nil, status.Errorf(codes.Internal, "Failed to create volume from volume (%s)", err.Error())
								}
							} else {
								tmpSnapID := fmt.Sprintf("%s%s-%d", TempSnap, c.s.getClusterPrefix(), time.Now().Nanosecond())
								err = c.s.LinkVolumeToVolume(ctx, symID, srcVol, eachVol.ID, tmpSnapID, reqID, false, pmaxClient)
								if err != nil {
									return nil, status.Errorf(codes.Internal, "Failed to create volume from volume (%s)", err.Error())
								}
							}
						}
					}
				}

				log.WithFields(fields).Info("Idempotent volume detected, returning success")
				eachVol.ID = fmt.Sprintf("%s-%s-%s", eachVol.Identifier, symmetrixID, eachVol.ID)
				volResp := c.s.buildCSIVolume(&eachVol)
				// Set the volume context
				attributes := map[string]string{
					ServiceLevelParam: serviceLevel,
					StoragePoolParam:  storagePoolID,
					path.Join(c.s.opts.ReplicationContextPrefix, SymmetrixIDParam): symmetrixID,
					CapacityGB:    fmt.Sprintf("%.2f", eachVol.CapCyl),
					ContentSource: volContent,
					StorageGroup:  storageGroupName,
					// Format the time output
					"CreationTime": time.Now().Format("20060102150405"),
				}
				if replicationEnabled == "true" {
					addReplicationParamsToVolumeAttributes(attributes, c.s.opts.ReplicationContextPrefix, remoteSymID, repMode, remoteVolumeID, localRDFGrpNo, remoteRDFGrpNo)
				}
				volResp.VolumeContext = attributes
				csiResp := &csi.CreateVolumeResponse{
					Volume: volResp,
				}
				volResp.ContentSource = contentSource
				if accessibility != nil {
					volResp.AccessibleTopology = accessibility.Preferred
				}
				return csiResp, nil
			}
		}
	} else {
		// Idempotency test. We will read the volume and check for:
		// 1. Existence of a volume with matching volume name
		// 2. Matching cylinderSize
		// 3. Is a member of the storage group
		// 4. Check if snapshot/volume target
		log.Debug("Calling GetVolumeIDList for idempotency test")
		// For now an exact match
		volumeIDList, err := pmaxClient.GetVolumeIDList(ctx, symmetrixID, volumeIdentifier, false)
		if err != nil {
			log.Error("Error looking up volume for idempotence check: " + err.Error())
			return nil, status.Errorf(codes.Internal, "Error looking up volume for idempotence check: %s", err.Error())
		}
		// isLocalVolumePresent restrict CreateVolumeInProtectedSG call if the volume is present in local SG but not in remote SG
		// isLocalVolumePresent := false
		// Look up the volume(s), if any, returned for the idempotency check to see if there are any matches
		// We ignore any volume not in the desired storage group (even though they have the same name).
		for _, volumeID := range volumeIDList {
			// Fetch the volume
			log.WithFields(fields).Info("Calling GetVolumeByID for idempotence check")
			vol, err = pmaxClient.GetVolumeByID(ctx, symmetrixID, volumeID)
			if err != nil {
				log.Error("Error fetching volume for idempotence check: " + err.Error())
				return nil, status.Errorf(codes.Internal, "Error fetching volume for idempotence check: %s", err.Error())
			}
			if len(vol.StorageGroupIDList) < 1 {
				log.Error("Idempotence check: StorageGroupIDList is empty for (%s): " + volumeID)
				return nil, status.Errorf(codes.Internal, "Idempotence check: StorageGroupIDList is empty for (%s)", volumeID)
			}
			matchesStorageGroup := false
			for _, sgid := range vol.StorageGroupIDList {
				if strings.Contains(sgid, storageGroupName) {
					matchesStorageGroup = true
					storageGroupName = sgid
				}
			}

			// with Authorization, a tenant prefix is applied to the volume identifier on the array
			// csi-CSM-pmax-69298b3d3d-namespace -> tn1-csi-CSM-pmax-69298b3d3d-namespace
			// since we don't know the tenant prefix, the volume identifier on the array is checked to contain the standard volume identifier
			if matchesStorageGroup && (vol.VolumeIdentifier == volumeIdentifier || strings.Contains(vol.VolumeIdentifier, volumeIdentifier)) {
				// A volume with the same name exists and has the same size
				if vol.CapacityCYL != requiredCylinders {
					log.Error("A volume with the same name exists but has a different size than required.")
					alreadyExists = true
					continue
				}
				var remoteVolumeID string
				if replicationEnabled == "true" {
					remoteVolumeID, _, err = c.s.GetRemoteVolumeID(ctx, symmetrixID, localRDFGrpNo, vol.VolumeID, pmaxClient)
					if err != nil && !strings.Contains(err.Error(), "The device must be an RDF device") {
						return nil, status.Errorf(codes.Internal, "Failed to fetch rdf pair information for (%s) - Error (%s)", vol.VolumeID, err.Error())
					}
					if remoteVolumeID == "" {
						// Missing corresponding Remote Volume Name for existing local volume
						// The SG is unprotected as Local volume and Local SG exists but missing corresponding SRDF info
						// If the SG was protected, there must exist a corresponding remote replica volume
						log.Debugf("Local Volume already exist, skipping creation (%s)", vol.VolumeID)
						isLocalVolumePresent = true
						continue
					}
				}
				if volContent != "" {
					if replicationEnabled == "true" {
						if srcSnapID != "" {
							err = c.s.LinkSRDFVolToSnapshot(ctx, reqID, symID, srcVol.VolumeID, snapID, localProtectionGroupID, localRDFGrpNo, vol, bias, false, pmaxClient)
							if err != nil {
								return nil, err
							}
						} else if srcVolID != "" {
							if isV4 {
								err = c.s.LinkSRDFCloneVolume(ctx, reqID, symID, srcVol, vol, localProtectionGroupID, localRDFGrpNo, "false", pmaxClient)
								if err != nil {
									return nil, status.Errorf(codes.Internal, "Failed to create SRDF volume from volume (%s)", err.Error())
								}
							} else {
								tmpSnapID := fmt.Sprintf("%s%s-%d", TempSnap, c.s.getClusterPrefix(), time.Now().Nanosecond())
								err = c.s.LinkSRDFVolToVolume(ctx, reqID, symID, srcVol, vol, tmpSnapID, localProtectionGroupID, localRDFGrpNo, "false", false, pmaxClient)
								if err != nil {
									return nil, status.Errorf(codes.Internal, "Failed to create SRDF volume from volume (%s)", err.Error())
								}
							}
						}
					} else { // replication is not enabled
						if srcSnapID != "" {
							err = c.s.UnlinkTargets(ctx, symID, SrcDevID, pmaxClient)
							if err != nil {
								return nil, status.Errorf(codes.Internal, "Failed unlink existing target from snapshot (%s)", err.Error())
							}
							err = c.s.LinkVolumeToSnapshot(ctx, symID, srcVol.VolumeID, vol.VolumeID, snapID, reqID, false, pmaxClient)
							if err != nil {
								return nil, status.Errorf(codes.Internal, "Failed to create volume from snapshot (%s)", err.Error())
							}
						} else if srcVolID != "" {
							isV4 := c.s.isV4OrAbove(ctx, symID, pmaxClient)
							if isV4 {
								replicaRequest := types.ReplicationRequest{
									ReplicationPair: []types.ReplicationPair{
										{
											SourceVolumeName: srcVol.VolumeID,
											TargetVolumeName: vol.VolumeID,
										},
									},
									Establish:          true,
									EstablishTerminate: true,
								}
								err = pmaxClient.CloneVolumeFromVolume(ctx, symID, replicaRequest)
								if err != nil {
									return nil, status.Errorf(codes.Internal, "Failed to create volume from volume (%s)", err.Error())
								}
							} else {
								tmpSnapID := fmt.Sprintf("%s%s-%d", TempSnap, c.s.getClusterPrefix(), time.Now().Nanosecond())
								err = c.s.LinkVolumeToVolume(ctx, symID, srcVol, vol.VolumeID, tmpSnapID, reqID, false, pmaxClient)
								if err != nil {
									return nil, status.Errorf(codes.Internal, "Failed to create volume from volume (%s)", err.Error())
								}
							}
						}
					}
				}

				log.WithFields(fields).Info("Idempotent volume detected, returning success")
				vol.VolumeID = fmt.Sprintf("%s-%s-%s", vol.VolumeIdentifier, symmetrixID, vol.VolumeID)
				volResp := c.s.getCSIVolume(vol)
				// Set the volume context
				attributes := map[string]string{
					ServiceLevelParam: serviceLevel,
					StoragePoolParam:  storagePoolID,
					path.Join(c.s.opts.ReplicationContextPrefix, SymmetrixIDParam): symmetrixID,
					CapacityGB:    fmt.Sprintf("%.2f", vol.CapacityGB),
					ContentSource: volContent,
					StorageGroup:  storageGroupName,
					// Format the time output
					"CreationTime": time.Now().Format("20060102150405"),
				}

				if symmIDFoundInAZ {
					c.s.addZoneLabelsToVolumeAttributes(attributes, symmetrixID)
				}

				if replicationEnabled == "true" {
					addReplicationParamsToVolumeAttributes(attributes, c.s.opts.ReplicationContextPrefix, remoteSymID, repMode, remoteVolumeID, localRDFGrpNo, remoteRDFGrpNo)
				}
				volResp.VolumeContext = attributes
				csiResp := &csi.CreateVolumeResponse{
					Volume: volResp,
				}
				volResp.ContentSource = contentSource
				if accessibility != nil {
					volResp.AccessibleTopology = accessibility.Preferred
				}
				return csiResp, nil
			}
		}
	}
	if alreadyExists {
		log.Error("A volume with the same name " + volumeName + "exists but has a different size than requested. Use a different name.")
		return nil, status.Errorf(codes.AlreadyExists, "A volume with the same name %s exists but has a different size than requested. Use a different name.", volumeName)
	}

	// CSI specific metada for authorization
	headerMetadata := addMetaData(params)

	// Let's create the volume
	if !isLocalVolumePresent {
		vol, err = pmaxClient.CreateVolumeInStorageGroupS(ctx, symmetrixID, storageGroupName, volumeIdentifier, requiredCylinders, nil, headerMetadata)
		if err != nil {
			log.Error(fmt.Sprintf("Could not create volume: %s: %s", volumeName, err.Error()))
			return nil, status.Errorf(codes.Internal, "Could not create volume: %s: %s", volumeName, err.Error())
		}
	}

	if replicationEnabled == "true" {
		log.Debugf("RDF: Found Rdf enabled")
		// remote storage group name is kept same as local storage group name
		// Check if volume is already added in SG, else add it
		protectedSGID := c.s.GetProtectedStorageGroupID(vol.StorageGroupIDList, localRDFGrpNo+"-"+repMode)
		if protectedSGID == "" {
			// Volume is not present in Protected Storage Group, Add
			err = c.s.addVolumesToProtectedStorageGroup(ctx, reqID, symmetrixID, localProtectionGroupID, remoteSymID, remoteProtectionGroupID, false, vol.VolumeID, pmaxClient)
			if err != nil {
				return nil, err
			}
		}
		if isSGUnprotected {
			// If the required SG is still unprotected, protect the local SG with RDF info
			// If valid RDF group is supplied this will create a remote SG, a RDF pair and add the vol in respective SG created
			// Remote storage group name is kept same as local storage group name
			err := c.s.ProtectStorageGroup(ctx, symmetrixID, remoteSymID, localProtectionGroupID, remoteProtectionGroupID, "", localRDFGrpNo, repMode, vol.VolumeID, reqID, false, pmaxClient)
			if err != nil {
				log.Errorf("Proceeding to remove volume from protected storage group as rollback")
				// Remove volume from protected storage group as a rollback
				// The device could be just a TDEV and can make RDF unmanageable due to slow u4p response
				_, er := pmaxClient.RemoveVolumesFromStorageGroup(ctx, symmetrixID, localProtectionGroupID, true, vol.VolumeID)
				if er != nil {
					log.Errorf("Error removing volume %s from protected SG %s with error: %s", vol.VolumeID, localProtectionGroupID, er.Error())
				}
				return nil, err
			}
		}
	}

	// If volume content source is specified, initiate no_copy to newly created volume
	if contentSource != nil {
		if srcVolID != "" {
			if replicationEnabled == "true" {
				isV4 := c.s.isV4OrAbove(ctx, symID, pmaxClient)
				if isV4 {
					err = c.s.LinkSRDFCloneVolume(ctx, reqID, symID, srcVol, vol, localProtectionGroupID, localRDFGrpNo, "false", pmaxClient)
					if err != nil {
						return nil, status.Errorf(codes.Internal, "Failed to create SRDF volume from volume (%s)", err.Error())
					}
				} else {
					tmpSnapID := fmt.Sprintf("%s%s-%d", TempSnap, c.s.getClusterPrefix(), time.Now().Nanosecond())
					err = c.s.LinkSRDFVolToVolume(ctx, reqID, symID, srcVol, vol, tmpSnapID, localProtectionGroupID, localRDFGrpNo, "false", false, pmaxClient)
					if err != nil {
						return nil, status.Errorf(codes.Internal, "Failed to create SRDF volume from volume (%s)", err.Error())
					}
				}
			} else {
				isV4 := c.s.isV4OrAbove(ctx, symID, pmaxClient)
				if isV4 {
					replicaRequest := types.ReplicationRequest{
						ReplicationPair: []types.ReplicationPair{
							{
								SourceVolumeName: srcVol.VolumeID,
								TargetVolumeName: vol.VolumeID,
							},
						},
						Establish:          true,
						EstablishTerminate: true,
					}
					err = pmaxClient.CloneVolumeFromVolume(ctx, symID, replicaRequest)
					if err != nil {
						return nil, status.Errorf(codes.Internal, "Failed to create volume from volume (%s)", err.Error())
					}
				} else {
					tmpSnapID := fmt.Sprintf("%s%s-%d", TempSnap, c.s.getClusterPrefix(), time.Now().Nanosecond())
					err = c.s.LinkVolumeToVolume(ctx, symID, srcVol, vol.VolumeID, tmpSnapID, reqID, false, pmaxClient)
					if err != nil {
						return nil, status.Errorf(codes.Internal, "Failed to create volume from volume (%s)", err.Error())
					}
				}
			}
		} else if srcSnapID != "" {
			if replicationEnabled == "true" {
				err = c.s.LinkSRDFVolToSnapshot(ctx, reqID, symID, srcVol.VolumeID, snapID, localProtectionGroupID, localRDFGrpNo, vol, bias, false, pmaxClient)
				if err != nil {
					return nil, err
				}
			} else {
				// Unlink all previous targets from this snapshot if the link is in defined state
				err = c.s.UnlinkTargets(ctx, symID, SrcDevID, pmaxClient)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "Failed unlink existing target from snapshot (%s)", err.Error())
				}
				err = c.s.LinkVolumeToSnapshot(ctx, symID, srcVol.VolumeID, vol.VolumeID, snapID, reqID, false, pmaxClient)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "Failed to create volume from snapshot (%s)", err.Error())
				}
			}
		}
	}

	// Formulate the return response
	volID := vol.VolumeID
	vol.VolumeID = fmt.Sprintf("%s-%s-%s", vol.VolumeIdentifier, symmetrixID, vol.VolumeID)
	volResp := c.s.getCSIVolume(vol)
	volResp.ContentSource = contentSource
	// Set the volume context
	attributes := map[string]string{
		ServiceLevelParam: serviceLevel,
		StoragePoolParam:  storagePoolID,
		path.Join(c.s.opts.ReplicationContextPrefix, SymmetrixIDParam): symmetrixID,
		CapacityGB:    fmt.Sprintf("%.2f", vol.CapacityGB),
		ContentSource: volContent,
		StorageGroup:  storageGroupName,
		// Format the time output
		"CreationTime": time.Now().Format("20060102150405"),
	}
	if replicationEnabled == "true" {
		remoteVolumeID, _, err := c.s.GetRemoteVolumeID(ctx, symmetrixID, localRDFGrpNo, volID, pmaxClient)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to fetch rdf pair information for (%s) - Error (%s)", vol.VolumeID, err.Error())
		}
		addReplicationParamsToVolumeAttributes(attributes, c.s.opts.ReplicationContextPrefix, remoteSymID, repMode, remoteVolumeID, localRDFGrpNo, remoteRDFGrpNo)
	}

	volResp.VolumeContext = attributes
	if accessibility != nil {
		volResp.AccessibleTopology = accessibility.Preferred
	}
	csiResp := &csi.CreateVolumeResponse{
		Volume: volResp,
	}
	fields[storageGroupName] = storageGroupName
	log.WithFields(fields).Infof("Created volume with ID: %s", volResp.VolumeId)
	return csiResp, nil
}
