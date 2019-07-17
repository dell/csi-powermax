package service

import (
	// "errors"
	// "fmt"

	"fmt"
	"math/rand"
	"strings"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	types "github.com/dell/csi-powermax/pmax/types/v90"
	log "github.com/sirupsen/logrus"
)

// constants
const (
	// KeyStoragePool is the key used to get the storagepool name from the
	// volume create parameters map
	KeyStoragePool         = "storagepool"
	cylinderSizeInBytes    = 1966080
	DefaultVolumeSizeBytes = 1073741824
	// MinVolumeSizeBytes - This is the minimum volume size in bytes. This is equal to
	// the number of bytes to create a volume which requires 1 cylinder less than
	// the number of bytes required for 50 MB
	MinVolumeSizeBytes = 51118080
	// MaxVolumeSizeBytes - This is the maximum volume size in bytes. This is equal to
	// the minimum number of bytes required to create a 1 TB volume on Powermax arrays
	MaxVolumeSizeBytes       = 1099512545280
	removeModeOnlyMe         = "ONLY_ME"
	errNoMultiMap            = "volume not enabled for mapping to multiple hosts"
	errUnknownAccessType     = "unknown access type is not Block or Mount"
	errUnknownAccessMode     = "access mode cannot be UNKNOWN"
	errNoMultiNodeWriter     = "multi-node with writer(s) only supported for block access type"
	TRUE                     = "TRUE"
	FALSE                    = "FALSE"
	StoragePoolCacheDuration = 4 * time.Hour
	MaxVolIdentifierLength   = 64
	MaxClusterPrefixLength   = 3
	NumOfVolIDAttributes     = 4
	CSIPrefix                = "csi"
	DeletionPrefix           = "_DEL"
	SymmetricIDLength        = 12
	DeviceIDLength           = 5
	CsiHostPrefix            = "csi-node-"
	CsiMVPrefix              = "csi-mv-"
	CsiNoSrpSGPrefix         = "csi-no-srp-sg-"
	CsiVolumePrefix          = "csi-"
	PublishContextDeviceWWN  = "DEVICE_WWN"
	PublishContextLUNAddress = "LUN_ADDRESS"
	cannotBeFound            = "cannot be found" // error message from pmax when volume not found
)

// Keys for parameters to CreateVolume
const (
	SymmetrixIDParam  = "SYMID"
	ServiceLevelParam = "ServiceLevel"
	StoragePoolParam  = "SRP"
	// If storage_group is set, this over-rides the generation of the Storage Group from SLO/SRP
	StorageGroupParam      = "StorageGroup"
	ThickVolumesParam      = "ThickVolumes" // "true" or "false" or "" (defaults thin)
	ApplicationPrefixParam = "ApplicationPrefix"
)

// Information cached about a pmax
type pmaxCachedInformation struct {
	// Existence of a StoragePool discovered at indicated time
	knownStoragePools map[string]time.Time
}

// Initializes a pmaxCachedInformation type
func (p *pmaxCachedInformation) initialize() {
	p.knownStoragePools = make(map[string]time.Time)
}

func getPmaxCache(symID string) *pmaxCachedInformation {
	if pmaxCache == nil {
		pmaxCache = make(map[string]*pmaxCachedInformation)
	}
	if pmaxCache[symID] != nil {
		return pmaxCache[symID]
	}
	info := &pmaxCachedInformation{}
	info.initialize()
	pmaxCache[symID] = info
	return info
}

var (
	validSLO = [...]string{"Diamond", "Platinum", "Gold", "Silver", "Bronze", "Optimized", "None"}

	// A map of the symmetrixID to the pmaxCachedInformation structure for that pmax
	pmaxCache map[string]*pmaxCachedInformation
)

func (s *service) CreateVolume(
	ctx context.Context,
	req *csi.CreateVolumeRequest) (
	*csi.CreateVolumeResponse, error) {

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		reqID = headers["csi.requestid"][0]
	}

	if err := s.requireProbe(ctx); err != nil {
		return nil, err
	}

	// Get the parameters
	params := req.GetParameters()
	params = mergeStringMaps(params, req.GetSecrets())
	symmetrixID := params[SymmetrixIDParam]
	if symmetrixID == "" {
		log.Error("A SYMID parameter is required")
		return nil, status.Errorf(codes.InvalidArgument, "A SYMID parameter is required")
	}
	thick := params[ThickVolumesParam]

	applicationPrefix := ""
	if params[ApplicationPrefixParam] != "" {
		applicationPrefix = params[ApplicationPrefixParam]
	}

	// Storage (resource) Pool. Validate it against exist Pools
	storagePoolID := params[StoragePoolParam]
	err := s.validateStoragePoolID(symmetrixID, storagePoolID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Errorf(codes.InvalidArgument, err.Error())
	}

	// SLO is optional
	serviceLevel := "Optimized"
	if params[ServiceLevelParam] != "" {
		serviceLevel = params[ServiceLevelParam]
		found := false
		for _, val := range validSLO {
			if serviceLevel == val {
				found = true
			}
		}
		if !found {
			log.Error("An invalid Service Level parameter was specified")
			return nil, status.Errorf(codes.InvalidArgument, "An invalid Service Level parameter was specified")
		}
	}
	storageGroupName := ""
	if params[StorageGroupParam] != "" {
		storageGroupName = params[StorageGroupParam]
	}

	// AccessibleTopology not currently supported
	accessibility := req.GetAccessibilityRequirements()
	if accessibility != nil {
		log.Error("Volume AccessibilityRequirements is not supported")
		return nil, status.Errorf(codes.InvalidArgument, "Volume AccessibilityRequirements is not supported")
	}
	// Volume content source is not supported until we do Snapshots or Clones
	contentSource := req.GetVolumeContentSource()
	if contentSource != nil {
		log.Error("Volume VolumeContentSource is not supported")
		return nil, status.Error(codes.InvalidArgument, "Volume VolumeContentSource is not supported")
	}

	// Get the required capacity
	cr := req.GetCapacityRange()
	requiredCylinders, err := validateVolSize(cr)
	if err != nil {
		return nil, err
	}

	// Get the volume name
	volumeName := req.GetName()
	if volumeName == "" {
		log.Error("Name cannot be empty")
		return nil, status.Error(codes.InvalidArgument,
			"Name cannot be empty")
	}

	// Get the Volume prefix from environment
	volumePrefix := s.getClusterPrefix()
	maxLength := MaxVolIdentifierLength - len(volumePrefix) - len(s.getClusterPrefix()) - len(CsiVolumePrefix) - 1
	//First get the short volume name
	shortVolumeName := truncateString(volumeName, maxLength)
	//Form the volume identifier using short volume name
	volumeIdentifier := fmt.Sprintf("%s%s-%s", CsiVolumePrefix, s.getClusterPrefix(), shortVolumeName)

	// Storage Group is required to be derived from the parameters (such as service level and storage resource pool which are supplied in parameters)
	// Storage Group Name can optionally be supplied in the parameters (for testing) to over-ride the default.
	if storageGroupName == "" {
		if applicationPrefix == "" {
			storageGroupName = fmt.Sprintf("%s-%s-%s-%s-SG", CSIPrefix, s.getClusterPrefix(),
				serviceLevel, storagePoolID)
		} else {
			storageGroupName = fmt.Sprintf("%s-%s-%s-%s-%s-SG", CSIPrefix, s.getClusterPrefix(),
				applicationPrefix, serviceLevel, storagePoolID)
		}
	}

	// log all parameters used in CreateVolume call
	fields := map[string]interface{}{
		"SymmetrixID":       symmetrixID,
		"SRP":               storagePoolID,
		"Accessibility":     accessibility,
		"ApplicationPrefix": applicationPrefix,
		"volumeIdentifier":  volumeIdentifier,
		"requiredCylinders": requiredCylinders,
		"storageGroupName":  storageGroupName,
		"CSIRequestID":      reqID,
	}
	log.WithFields(fields).Info("Executing CreateVolume with following fields")

	// Check existence of the Storage Group and create if necessary.
	sg, err := s.adminClient.GetStorageGroup(symmetrixID, storageGroupName)
	if err != nil || sg == nil {
		log.Debug(fmt.Sprintf("Unable to find storage group: %s", storageGroupName))
		_, err := s.adminClient.CreateStorageGroup(symmetrixID, storageGroupName, storagePoolID,
			serviceLevel, thick == "true")
		if err != nil {
			log.Error("Error creating storage group: " + err.Error())
			return nil, status.Errorf(codes.Internal, "Error creating storage group: %s", err.Error())
		}
	}

	// Idempotency test. We will read the volume and check for:
	// 1. Existence of a volume with matching volume name
	// 2. Matching cylinderSize
	// 3. Is a member of the storage group
	log.Debug("Calling GetVolumeIDList for idempotency test")
	// For now an exact match
	volumeIDList, err := s.adminClient.GetVolumeIDList(symmetrixID, volumeIdentifier, false)
	if err != nil {
		log.Error("Error looking up volume for idempotence check: " + err.Error())
		return nil, status.Errorf(codes.Internal, "Error looking up volume for idempotence check: %s", err.Error())
	}
	alreadyExists := false
	// Look up the volume(s), if any, returned for the idempotency check to see if there are any matches
	// We ignore any volume not in the desired storage group (even though they have the same name).
	for _, volumeID := range volumeIDList {
		// Fetch the volume
		log.WithFields(fields).Info("Calling GetVolumeByID for idempotence check")
		vol, err := s.adminClient.GetVolumeByID(symmetrixID, volumeID)
		if err != nil {
			log.Error("Error fetching volume for idempotence check: " + err.Error())
			return nil, status.Errorf(codes.Internal, "Error fetching volume for idempotence check: %s", err.Error())
		}
		matchesStorageGroup := false
		for _, sgid := range vol.StorageGroupIDList {
			if sgid == storageGroupName {
				matchesStorageGroup = true
			}
		}
		if matchesStorageGroup && vol.VolumeIdentifier == volumeIdentifier {
			if vol.CapacityCYL != requiredCylinders {
				log.Error("A volume with the same name exists but has a different size than required.")
				alreadyExists = true
				continue
			}
			log.WithFields(fields).Info("Idempotent volume detected, returning success")
			vol.VolumeID = fmt.Sprintf("%s-%s-%s", volumeIdentifier, symmetrixID, vol.VolumeID)
			volResp := s.getCSIVolume(vol)
			//Set the volume context
			attributes := map[string]string{
				ServiceLevelParam: serviceLevel,
				StoragePoolParam:  storagePoolID,
				//Format the time output
				"CreationTime": time.Now().Format("20060102150405"),
			}
			volResp.VolumeContext = attributes
			csiResp := &csi.CreateVolumeResponse{
				Volume: volResp,
			}
			return csiResp, nil
		}
	}
	if alreadyExists {
		log.Error("A volume with the same name " + volumeName + "exists but has a different size than requested. Use a different name.")
		return nil, status.Errorf(codes.AlreadyExists, "A volume with the same name %s exists but has a different size than requested. Use a different name.", volumeName)
	}
	// Let's create the volume
	vol, err := s.adminClient.CreateVolumeInStorageGroup(symmetrixID, storageGroupName, volumeIdentifier, requiredCylinders)
	if err != nil {
		log.Error(fmt.Sprintf("Could not create volume: %s: %s", volumeName, err.Error()))
		return nil, status.Errorf(codes.Internal, "Could not create volume: %s: %s", volumeName, err.Error())
	}
	// Formulate the return response
	vol.VolumeID = fmt.Sprintf("%s-%s-%s", volumeIdentifier, symmetrixID, vol.VolumeID)
	volResp := s.getCSIVolume(vol)
	//Set the volume context
	attributes := map[string]string{
		ServiceLevelParam: serviceLevel,
		StoragePoolParam:  storagePoolID,
		//Format the time output
		"CreationTime": time.Now().Format("20060102150405"),
	}
	volResp.VolumeContext = attributes
	csiResp := &csi.CreateVolumeResponse{
		Volume: volResp,
	}

	return csiResp, nil
}

// validateVolSize uses the CapacityRange range params to determine what size
// volume to create, and returns an error if volume size would be greater than
// the given limit. Returned size is in number of cylinders
func validateVolSize(cr *csi.CapacityRange) (int, error) {

	minSize := cr.GetRequiredBytes()
	maxSize := cr.GetLimitBytes()

	if minSize < 0 || maxSize < 0 {
		return 0, status.Errorf(
			codes.OutOfRange,
			"bad capacity: volume size bytes %d and limit size bytes: %d must not be negative", minSize, maxSize)
	}

	if minSize == 0 {
		minSize = DefaultVolumeSizeBytes
	}
	if maxSize == 0 {
		maxSize = MaxVolumeSizeBytes
	}
	if maxSize < minSize {
		return 0, status.Errorf(
			codes.OutOfRange,
			"bad capacity: max size bytes %d can't be less than minimum size bytes %d", maxSize, minSize)
	}
	if minSize < MinVolumeSizeBytes {
		return 0, status.Errorf(
			codes.OutOfRange,
			"bad capacity: min size bytes %d can't be less than minimum volume size bytes %d", minSize, MinVolumeSizeBytes)
	}
	minNumberOfCylinders := int(minSize / cylinderSizeInBytes)
	var numOfCylinders int
	if minSize%cylinderSizeInBytes > 0 {
		numOfCylinders = minNumberOfCylinders + 1
	} else {
		numOfCylinders = minNumberOfCylinders
	}
	sizeInBytes := int64(numOfCylinders * cylinderSizeInBytes)
	if sizeInBytes > maxSize {
		return 0, status.Errorf(
			codes.OutOfRange,
			"bad capacity: size in bytes %d exceeds limit size bytes %d", sizeInBytes, maxSize)
	}
	return numOfCylinders, nil
}

// Validates Storage Pool IDs, keeps a cache. The cache entries are only  valid for
// the StoragePoolCacheDuration period, after that they are rechecked.
func (s *service) validateStoragePoolID(symmetrixID string, storagePoolID string) error {
	if storagePoolID == "" {
		return fmt.Errorf("A valid SRP parameter is required")
	}

	cache := getPmaxCache(symmetrixID)

	if !cache.knownStoragePools[storagePoolID].IsZero() {
		storagePoolTimeStamp := cache.knownStoragePools[storagePoolID]
		if time.Now().Sub(storagePoolTimeStamp) < StoragePoolCacheDuration {
			// We have a valid cache entry.
			return nil
		}
	}
	list, err := s.adminClient.GetStoragePoolList(symmetrixID)
	if err != nil {
		return err
	}
	for _, value := range list.StoragePoolIDs {
		if storagePoolID == value {
			// Make a cache entry and record the timestamp when created
			cache.knownStoragePools[value] = time.Now()
			return nil
		}
	}
	return status.Errorf(codes.InvalidArgument, "Storage Pool %s not found", storagePoolID)
}

func truncateString(str string, maxLength int) string {
	truncatedString := str
	newLength := 0
	if len(str) > maxLength {
		if maxLength%2 != 0 {
			newLength = len(str) - maxLength/2 - 1
		} else {
			newLength = len(str) - maxLength/2
		}
		truncatedString = str[0:maxLength/2] + str[newLength:]
	}
	return truncatedString
}

// Create a CSI VolumeId from component parts.
func (s *service) createCSIVolumeID(volumePrefix, volumeName, symID, devID string) string {
	//return fmt.Sprintf("%s-%s-%s-%s", volumePrefix, volumeName, symID, devID)
	return fmt.Sprintf("%s%s-%s-%s-%s", CsiVolumePrefix, s.getClusterPrefix(), volumeName, symID, devID)
}

// parseCSVolumeID returns the VolumeName, Array ID, and Device ID given the CSI ID.
// The last 19 characters of the CSI volume ID are special:
//      A dash '-', followed by 12 digits of array serial number, followed by a dash, followed by 5 digits of array device id.
//	That's 19 characters total on the right end.
// Also an error returned if mal-formatted.
func (s *service) parseCSIVolumeID(csiVolumeID string) (
	volName string, arrayID string, devID string, err error) {
	if csiVolumeID == "" {
		err = fmt.Errorf("A Volume ID is required for the request")
		return
	}
	// get the Device ID and Array ID
	idComponents := strings.Split(csiVolumeID, "-")
	// Protect against mal-formed component
	numOfIDComponents := len(idComponents)
	if numOfIDComponents < 3 {
		// Not well formed
		err = fmt.Errorf("The CSI Volume ID %s is not formed correctly", csiVolumeID)
		return
	}
	// Device ID is the last token
	devID = idComponents[numOfIDComponents-1]
	// Array ID is the second to last token
	arrayID = idComponents[numOfIDComponents-2]

	// The two here is for two dashes - one at front of array ID and one between the Array ID and Device ID
	lengthOfTrailer := len(devID) + len(arrayID) + 2
	length := len(csiVolumeID)
	if length <= lengthOfTrailer+2 {
		// Not well formed...
		err = fmt.Errorf("The CSI Volume ID %s is not formed correctly", csiVolumeID)
		return
	}
	// calculate the volume name, which is everything before the array ID
	volName = csiVolumeID[0 : length-lengthOfTrailer]
	return
}

func (s *service) DeleteVolume(
	ctx context.Context,
	req *csi.DeleteVolumeRequest) (
	*csi.DeleteVolumeResponse, error) {

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		reqID = headers["csi.requestid"][0]
	}

	if err := s.requireProbe(ctx); err != nil {
		log.Error("Failed to probe with erro: " + err.Error())
		return nil, err
	}

	id := req.GetVolumeId()
	volName, symID, devID, err := s.parseCSIVolumeID(id)
	if err != nil {
		// We couldn't comprehend the identifier.
		log.Info("Could not parse CSI VolumeId: " + id)
		return &csi.DeleteVolumeResponse{}, nil
	}
	// log all parameters used in CreateVolume call
	fields := map[string]interface{}{
		"SymmetrixID":  symID,
		"VolumeName":   volName,
		"DeviceID":     devID,
		"CSIRequestID": reqID,
	}
	log.WithFields(fields).Info("Executing DeleteVolume with following fields")

	vol, err := s.adminClient.GetVolumeByID(symID, devID)
	fmt.Printf("vol: %#v, error: %#v\n", vol, err)
	if err != nil {
		if strings.Contains(err.Error(), cannotBeFound) {
			// The volume is already deleted
			log.Info(fmt.Sprintf("DeleteVolume: Could not find volume: %s/%s so assume it's already deleted", symID, devID))
			return &csi.DeleteVolumeResponse{}, nil
		}
		return nil, status.Errorf(codes.Internal, "Could not retrieve volume: (%s)", err.Error())
	}

	if vol.VolumeIdentifier != volName {
		// This volume is aready deleted or marked for deletion,
		// or volume id is an old stale identifier not matching a volume.
		// Either way idempotence calls for doing nothing and returning ok.
		log.Info(fmt.Sprintf("DeleteVolume: VolumeIdentifier %s did not match volume name %s so assume it's already deleted",
			vol.VolumeIdentifier, volName))
		return &csi.DeleteVolumeResponse{}, nil
	}
	// find if volume is present in any masking view
	for _, sgid := range vol.StorageGroupIDList {
		sg, err := s.adminClient.GetStorageGroup(symID, sgid)
		if err != nil || sg == nil {
			log.Error(fmt.Sprintf("DeleteVolume: could not retrieve Storage Group %s/%s", symID, sgid))
			return nil, status.Errorf(codes.Internal, "Unable to find storage group: %s in %s", sgid, symID)
		}
		if sg.NumOfMaskingViews > 0 {
			log.Error(fmt.Sprintf("DeleteVolume: Volume %s is in use by Storage Group %s which has Masking Views",
				id, sgid))
			return nil, status.Errorf(codes.Internal, "Volume is in use")
		}
	}
	// Rename the volume before delete
	// If the name length goes beyond 64 characters, Unisphere can truncate
	// from the end. The objective is to retrieve volumes marked for deletion from
	// Symmetrix if the plugin abruptly shutdown
	newVolName := fmt.Sprintf("%s%s", DeletionPrefix, volName)
	if len(newVolName) > MaxVolIdentifierLength {
		newVolName = newVolName[:MaxVolIdentifierLength]
	}
	vol, err = s.adminClient.RenameVolume(symID, devID, newVolName)
	if err != nil || vol == nil {
		log.Error(fmt.Sprintf("DeleteVolume: Could not rename volume %s", id))
		return nil, status.Errorf(codes.Internal, "Failed to rename volume")
	}

	var delReq deletionWorkerRequest

	delReq.volumeID = vol.VolumeID
	delReq.volumeName = vol.VolumeIdentifier
	delReq.symmetrixID = symID
	delReq.volumeSizeInCylinders = int64(vol.CapacityCYL)
	delWorker.requestDeletion(&delReq)
	log.Printf("Request dispatched to delete worker thread for %s/%s", vol.VolumeID, vol.SSID)

	return &csi.DeleteVolumeResponse{}, nil
}

func (s *service) ControllerPublishVolume(
	ctx context.Context,
	req *csi.ControllerPublishVolumeRequest) (
	*csi.ControllerPublishVolumeResponse, error) {

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		reqID = headers["csi.requestid"][0]
	}

	volumeContext := req.GetVolumeContext()
	if volumeContext != nil {
		log.Printf("VolumeContext:")
		for key, value := range volumeContext {
			log.Printf("    [%s]=%s", key, value)
		}
	}

	if err := s.requireProbe(ctx); err != nil {
		log.Error("Failed to probe with error: " + err.Error())
		return nil, err
	}

	volID := req.GetVolumeId()
	if volID == "" {
		log.Error("volume ID is required")
		return nil, status.Error(codes.InvalidArgument,
			"volume ID is required")
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

	//Fetch the volume details from array
	symID, devID, vol, err := s.GetVolumeByID(volID)
	if err != nil {
		log.Error("GetVolumeByID Error: " + err.Error())
		return nil, err
	}

	// log all parameters used in ControllerPublishVolume call
	fields := map[string]interface{}{
		"SymmetrixID":  symID,
		"VolumeId":     volID,
		"NodeId":       nodeID,
		"AccessMode":   am.Mode,
		"CSIRequestID": reqID,
	}
	log.WithFields(fields).Info("Executing ControllerPublishVolume with following fields")

	// Get the hostid, sgid && mvid using node id
	hostID, tgtStorageGroupID, tgtMaskingViewID := s.GetHostSGAndMVIDFromNodeID(nodeID)
	AcquireLock(tgtStorageGroupID)
	defer ReleaseLock(tgtStorageGroupID)

	publishContext := map[string]string{
		PublishContextDeviceWWN: vol.WWN,
	}
	volumeAlreadyInSG := false
	currentSGIDs := vol.StorageGroupIDList
	for _, sgID := range currentSGIDs {
		if sgID == tgtStorageGroupID {
			volumeAlreadyInSG = true
		}
	}
	maskingViewIDs, storageGroups, err := s.GetMaskingViewAndSGDetails(symID, currentSGIDs)
	if err != nil {
		log.Error("GetMaskingViewAndSGDetails Error: " + err.Error())
		return nil, err
	}
	for _, storageGroup := range storageGroups {
		if storageGroup.StorageGroupID == tgtStorageGroupID {
			if storageGroup.SRP != "" {
				log.Error("Conflicting SG present")
				return nil, status.Error(codes.Internal, "Conflicting SG present")
			}
		}
	}
	maskingViewExists := false
	volumeInMaskingView := false
	// First check if our masking view is in the existsing list
	for _, mvID := range maskingViewIDs {
		if mvID == tgtMaskingViewID {
			maskingViewExists = true
			volumeInMaskingView = true
		}
	}
	switch am.Mode {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
		csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:
		// Check if the volume is already mapped to some host
		if vol.NumberOfFrontEndPaths > 0 {
			// make sure we go at least one masking view
			if len(maskingViewIDs) == 0 {
				// this is an error, we should have gotten at least one MV
				return nil, status.Error(codes.Internal, "No masking views found for volume")
			}
			if len(maskingViewIDs) > 1 {
				log.Error("Volume already part of multiple Masking views")
				return nil, status.Error(codes.Internal, "Volume already part of multiple Masking views")
			}
			if maskingViewIDs[0] == tgtMaskingViewID {
				// Do a double check for the SG as well
				if volumeAlreadyInSG {
					log.Debug("volume already mapped")
					s.addLUNIDToPublishContext(publishContext, symID, tgtMaskingViewID, devID)
					return &csi.ControllerPublishVolumeResponse{
						PublishContext: publishContext,
					}, nil
				}
				log.Error(fmt.Sprintf("ControllerPublishVolume: Masking view - %s has conflicting SG", tgtMaskingViewID))
				return nil, status.Errorf(codes.FailedPrecondition,
					"Volume in conflicting masking view")
			}
			log.Error(fmt.Sprintf("ControllerPublishVolume: Volume present in a different masking view - %s", maskingViewIDs[0]))
			return nil, status.Errorf(codes.FailedPrecondition,
				"volume already present in a different masking view")
		}
	}
	tgtMaskingView := &types.MaskingView{}
	// Fetch the masking view
	tgtMaskingView, err = s.adminClient.GetMaskingViewByID(symID, tgtMaskingViewID)
	if err != nil {
		log.Debug("Failed to fetch masking view from array")
	} else {
		maskingViewExists = true
	}
	if maskingViewExists {
		// We just need to confirm if all the other entities are in order
		if tgtMaskingView.HostID == hostID &&
			tgtMaskingView.StorageGroupID == tgtStorageGroupID {
			if volumeInMaskingView {
				log.Debug("volume already mapped")
				s.addLUNIDToPublishContext(publishContext, symID, tgtMaskingViewID, devID)
				return &csi.ControllerPublishVolumeResponse{
					PublishContext: publishContext,
				}, nil
			}
			//Add the volume to masking view
			err := s.adminClient.AddVolumesToStorageGroup(symID, tgtStorageGroupID, devID)
			if err != nil {
				log.Error(fmt.Sprintf("ControllerPublishVolume: Failed to add device - %s to storage group - %s", devID, tgtStorageGroupID))
				return nil, status.Error(codes.Internal, "Failed to add volume to storage group")
			}
		} else {
			errormsg := fmt.Sprintf(
				"ControllerPublishVolume: Existing masking view %s with conflicting SG %s or Host %s",
				tgtMaskingViewID, tgtStorageGroupID, hostID)
			log.Error(errormsg)
			return nil, status.Error(codes.Internal, errormsg)
		}
	} else {
		// We should create the Masking view and SG (if required)
		//Fetch the host from the array
		_, err := s.adminClient.GetHostByID(symID, hostID)
		if err != nil {
			log.Error(fmt.Sprintf("ControllerPublishVolume: Failed to get host - %s", hostID))
			return nil, status.Error(codes.NotFound, "Failed to fetch host details from the array")
		}
		// Fetch a port group
		portGroupID, err := s.SelectPortGroup()
		if err != nil {
			log.Error("Failed to get port group")
			return nil, status.Error(codes.Internal, "Failed to get port group")
		}
		if !volumeAlreadyInSG {
			// First check if our storage group exists
			tgtStorageGroup, err := s.adminClient.GetStorageGroup(symID, tgtStorageGroupID)
			if err == nil {
				// Check if this SG is not managed by FAST
				if tgtStorageGroup.SRP != "" {
					log.Error(fmt.Sprintf("ControllerPublishVolume: Storage group - %s exists with same name but with conflicting params", tgtStorageGroupID))
					return nil, status.Error(codes.Internal, "Storage group exists with same name but with conflicting params")
				}
			} else {
				// Attempt to create SG
				tgtStorageGroup, err = s.adminClient.CreateStorageGroup(symID, tgtStorageGroupID, "None", "", false)
				if err != nil {
					log.Error(fmt.Sprintf("ControllerPublishVolume: Failed to create storage group - %s", tgtStorageGroupID))
					return nil, status.Error(codes.Internal, "Failed to create storage group")
				}
			}
			// Add the volume to storage group
			err = s.adminClient.AddVolumesToStorageGroup(symID, tgtStorageGroupID, devID)
			if err != nil {
				log.Error(fmt.Sprintf("ControllerPublishVolume: Failed to add device - %s to storage group - %s", devID, tgtStorageGroupID))
				return nil, status.Error(codes.Internal, "Failed to add volume to storage group")
			}
		}
		_, err = s.adminClient.CreateMaskingView(symID, tgtMaskingViewID, tgtStorageGroupID, hostID, true, portGroupID)
		if err != nil {
			log.Error(fmt.Sprintf("ControllerPublishVolume: Failed to create masking view - %s", tgtMaskingViewID))
			return nil, status.Error(codes.Internal, "Failed to create masking view")
		}
	}
	s.addLUNIDToPublishContext(publishContext, symID, tgtMaskingViewID, devID)

	return &csi.ControllerPublishVolumeResponse{
		PublishContext: publishContext}, nil
}

// Adds the LUN_ADDRESS to the PublishContext by looking at MaskingView connections
func (s *service) addLUNIDToPublishContext(publishContext map[string]string, symID, tgtMaskingViewID, deviceID string) {
	// We ignore errors here; adding the HostLunAddress to the context is optional (could be multiple.)
	var connections []*types.MaskingViewConnection
	var err error
	connections, err = s.adminClient.GetMaskingViewConnections(symID, tgtMaskingViewID, deviceID)
	if err != nil {
		log.Error("Could not get MV Connections: " + err.Error())
		return
	}
	lunid := ""
	for _, conn := range connections {
		if deviceID != conn.VolumeID {
			log.Debug("MV Connection: VolumeID != deviceID")
			continue
		}
		if lunid == "" {
			lunid = conn.HostLUNAddress
		} else if lunid != conn.HostLUNAddress {
			log.Printf("MV Connection: Multiple HostLUNAddress values")
			return
		}
	}
	publishContext[PublishContextLUNAddress] = lunid
}

// GetVolumeByID - Takes a CSI volume ID and checks for its existence on array
// along with matching with the volume identifier. Returns back the volume name
// on array, device ID, symID
func (s *service) GetVolumeByID(volID string) (string, string, *types.Volume, error) {
	// parse the volume and get the array serial and volume ID
	volName, symID, devID, err := s.parseCSIVolumeID(volID)
	if err != nil {
		return "", "", nil, status.Errorf(codes.InvalidArgument,
			"volID: %s malformed. Error: %s", volID, err.Error())
	}

	vol, err := s.adminClient.GetVolumeByID(symID, devID)
	if err != nil {
		if strings.Contains(err.Error(), cannotBeFound) {
			return "", "", nil, status.Errorf(codes.NotFound,
				"Volume not found (Array: %s, Volume: %s)status %s",
				symID, devID, err.Error())
		}
		return "", "", nil, status.Errorf(codes.Internal,
			"failure checking volume (Array: %s, Volume: %s)status %s",
			symID, devID, err.Error())
	}
	if volName != vol.VolumeIdentifier {
		return "", "", nil, status.Errorf(codes.FailedPrecondition,
			"Failed to validate combination of Volume Name and Volume ID")
	}
	return symID, devID, vol, nil
}

// GetMaskingViewAndSGDetails - Takes a list of SGs and returns the list of associated masking views
// and individual storage group objects. The storage group objects are returned as
// they can avoid any extra queries (since they have been already queried in this function)
func (s *service) GetMaskingViewAndSGDetails(symID string, sgIDs []string) ([]string, []*types.StorageGroup, error) {
	maskingViewIDs := make([]string, 0)
	storageGroups := make([]*types.StorageGroup, 0)
	// Fetch each SG this device is part of
	for _, sgID := range sgIDs {
		sg, err := s.adminClient.GetStorageGroup(symID, sgID)
		if err != nil {
			return nil, nil, status.Error(codes.Internal, "Failed to fetch SG details")
		}
		storageGroups = append(storageGroups, sg)
		// Loop through the masking views and see if it is part of the masking view
		for _, mvID := range sg.MaskingView {
			maskingViewIDs = appendIfMissing(maskingViewIDs, mvID)
		}
	}
	return maskingViewIDs, storageGroups, nil
}

// AppendIfMissing - Appends a string to a slice if not already present
// in slice
func appendIfMissing(slice []string, str string) []string {
	for _, ele := range slice {
		if ele == str {
			return slice
		}
	}
	return append(slice, str)
}

// GetHostSGAndMVIDFromNodeID - Forms HostID, StorageGroupID, MaskingViewID
// using the NodeID and returns them
func (s *service) GetHostSGAndMVIDFromNodeID(nodeID string) (string, string, string) {
	hostID := CsiHostPrefix + s.getClusterPrefix() + "-" + nodeID
	storageGroupID := CsiNoSrpSGPrefix + s.getClusterPrefix() + "-" + nodeID
	maskingViewID := CsiMVPrefix + s.getClusterPrefix() + "-" + nodeID
	return hostID, storageGroupID, maskingViewID
}

func (s *service) ControllerUnpublishVolume(
	ctx context.Context,
	req *csi.ControllerUnpublishVolumeRequest) (
	*csi.ControllerUnpublishVolumeResponse, error) {

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		reqID = headers["csi.requestid"][0]
	}

	if err := s.requireProbe(ctx); err != nil {
		log.Error("Failed to probe with error: " + err.Error())
		return nil, err
	}

	volID := req.GetVolumeId()
	if volID == "" {
		log.Error("GetVolumeId : Volume ID is required ")
		return nil, status.Error(codes.InvalidArgument,
			"Volume ID is required")
	}

	nodeID := req.GetNodeId()
	if nodeID == "" {
		log.Error("Node ID is required")
		return nil, status.Error(codes.InvalidArgument,
			"Node ID is required")
	}

	//Fetch the volume details from array
	symID, devID, vol, err := s.GetVolumeByID(volID)
	if err != nil {
		log.Error("GetVolumeByID Error: " + err.Error())
		return nil, err
	}

	// log all parameters used in ControllerUnpublishVolume call
	fields := map[string]interface{}{
		"SymmetrixID":  symID,
		"VolumeId":     volID,
		"NodeId":       nodeID,
		"CSIRequestID": reqID,
	}
	log.WithFields(fields).Info("Executing ControllerUnpublishVolume with following fields")

	// Get the hostid, sgid && mvid using node id
	_, tgtStorageGroupID, tgtMaskingViewID := s.GetHostSGAndMVIDFromNodeID(nodeID)
	AcquireLock(tgtStorageGroupID)
	defer ReleaseLock(tgtStorageGroupID)

	// Check if volume is part of the Storage group
	currentSGIDs := vol.StorageGroupIDList
	volumeInStorageGroup := false
	for _, storageGroupID := range currentSGIDs {
		if storageGroupID == tgtStorageGroupID {
			volumeInStorageGroup = true
			break
		}
	}

	if !volumeInStorageGroup {
		log.Debug("volume already unpublished")
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}

	tempStorageGroupList := []string{tgtStorageGroupID}
	maskingViewIDs, storageGroups, err := s.GetMaskingViewAndSGDetails(symID, tempStorageGroupList)
	if err != nil {
		return nil, err
	}
	// Check if volume is in masking view
	volumeInMaskingView := false
	for _, maskingViewID := range maskingViewIDs {
		if tgtMaskingViewID == maskingViewID {
			volumeInMaskingView = true
			break
		}
	}
	if !volumeInMaskingView {
		log.Debug("volume already unpublished")
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}
	maskingViewDeleted := false
	//First check if this is the only volume in the SG
	if storageGroups[0].NumOfVolumes == 1 {
		// We need to delete the MV first
		err = s.adminClient.DeleteMaskingView(symID, tgtMaskingViewID)
		if err != nil {
			log.Error(fmt.Sprintf("Failed to delete masking view (Array: %s, MaskingView: %s) status %s",
				symID, tgtMaskingViewID, err.Error()))
			return nil, status.Errorf(codes.Internal,
				"Failed to delete masking view (Array: %s, MaskingView: %s) status %s",
				symID, tgtMaskingViewID, err.Error())
		}
		maskingViewDeleted = true
	}
	// Remove the volume from SG
	_, err = s.adminClient.RemoveVolumesFromStorageGroup(symID, tgtStorageGroupID, devID)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to remove volume from SG (Volume: %s, SG: %s) status %s",
			devID, tgtStorageGroupID, err.Error()))
		return nil, status.Errorf(codes.Internal,
			"Failed to remove volume from SG (Volume: %s, SG: %s) status %s",
			devID, tgtStorageGroupID, err.Error())
	}
	// If SG was deleted, then delete the SG as well
	if maskingViewDeleted {
		err = s.adminClient.DeleteStorageGroup(symID, tgtStorageGroupID)
		if err != nil {
			// We can just log a warning and continue
			log.Warning(fmt.Printf("Failed to delete storage group %s", tgtStorageGroupID))
		}
	}

	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (s *service) ValidateVolumeCapabilities(
	ctx context.Context,
	req *csi.ValidateVolumeCapabilitiesRequest) (
	*csi.ValidateVolumeCapabilitiesResponse, error) {

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		reqID = headers["csi.requestid"][0]
	}

	if err := s.requireProbe(ctx); err != nil {
		log.Error("Failed to probe with error: " + err.Error())
		return nil, err
	}

	volID := req.GetVolumeId()
	// parse the volume and get the array serial and volume ID
	volName, symID, devID, err := s.parseCSIVolumeID(volID)
	if err != nil {
		log.Error(fmt.Sprintf("volID: %s malformed. Error: %s", volID, err.Error()))
		return nil, status.Errorf(codes.InvalidArgument,
			"volID: %s malformed. Error: %s", volID, err.Error())
	}

	// log all parameters used in ValidateVolumeCapabilities call
	fields := map[string]interface{}{
		"SymmetrixID":  symID,
		"VolumeId":     volID,
		"CSIRequestID": reqID,
	}
	log.WithFields(fields).Info("Executing ValidateVolumeCapabilities with following fields")

	vol, err := s.adminClient.GetVolumeByID(symID, devID)
	if err != nil {
		log.Error(fmt.Sprintf("failure checking volume (Array: %s, Volume: %s)status for capabilities: %s",
			symID, devID, err.Error()))
		return nil, status.Errorf(codes.NotFound,
			"failure checking volume (Array: %s, Volume: %s)status for capabilities: %s",
			symID, devID, err.Error())
	}

	if volName != vol.VolumeIdentifier {
		log.Error("Failed to validate combination of Volume Name and Volume ID")
		return nil, status.Errorf(codes.NotFound,
			"Failed to validate combination of Volume Name and Volume ID")
	}

	attributes := req.GetVolumeContext()
	validContext, reasonContext := s.valVolumeContext(attributes, vol, symID)
	if validContext != true {
		log.Error(fmt.Sprintf("Failure checking volume context (Array: %s, Volume: %s): %s",
			symID, devID, reasonContext))
		return nil, status.Errorf(codes.Internal,
			"Failure checking volume context (Array: %s, Volume: %s): %s",
			symID, devID, reasonContext)
	}

	vcs := req.GetVolumeCapabilities()
	supported, reason := valVolumeCaps(vcs, vol)

	resp := &csi.ValidateVolumeCapabilitiesResponse{}
	if supported {
		// The optional fields volume_context and parameters are not passed.
		confirmed := &csi.ValidateVolumeCapabilitiesResponse_Confirmed{}
		confirmed.VolumeCapabilities = vcs
		confirmed.VolumeContext = attributes
		resp.Confirmed = confirmed
	} else {
		resp.Message = reason
	}

	return resp, nil
}

func accTypeIsBlock(vcs []*csi.VolumeCapability) bool {
	for _, vc := range vcs {
		if at := vc.GetBlock(); at != nil {
			return true
		}
	}
	return false
}

func checkValidAccessTypes(vcs []*csi.VolumeCapability) bool {
	for _, vc := range vcs {
		if vc == nil {
			continue
		}
		atblock := vc.GetBlock()
		if atblock != nil {
			continue
		}
		atmount := vc.GetMount()
		if atmount != nil {
			continue
		}
		// Unknown access type, we should reject it.
		return false
	}
	return true
}

func (s *service) valVolumeContext(attributes map[string]string, vol *types.Volume, symID string) (bool, string) {

	foundSP := false
	foundSL := false
	sp := attributes[StoragePoolParam]
	sl := attributes[ServiceLevelParam]

	// set the right initial values
	if sl == "" {
		foundSL = true
	}
	if sp == "" {
		foundSP = true
	}
	// for each storage group the volume is in, check if the service level and/or pool match
	if len(vol.StorageGroupIDList) == 0 {
		return false, "Unable to find any associated Storage Groups"
	}
	for _, sgID := range vol.StorageGroupIDList {
		sg, err := s.adminClient.GetStorageGroup(symID, sgID)
		if err == nil {
			if (sl != "") && (sl == sg.SLO) {
				foundSL = true
			}
			if (sp != "") && (sp == sg.SRP) {
				foundSP = true
			}
		}
		if foundSP == true && foundSL == true {
			return true, ""
		}
	}
	msg := fmt.Sprintf("Unable to validate context (SRP=%v, SLO=%v)", foundSP, foundSL)
	return false, msg
}

func valVolumeCaps(
	vcs []*csi.VolumeCapability, vol *types.Volume) (bool, string) {

	var (
		supported = true
		isBlock   = accTypeIsBlock(vcs)
		reason    string
	)
	// Check that all access types are valid
	if !checkValidAccessTypes(vcs) {
		return false, errUnknownAccessType
	}

	for _, vc := range vcs {
		am := vc.GetAccessMode()
		if am == nil {
			continue
		}
		switch am.Mode {
		case csi.VolumeCapability_AccessMode_UNKNOWN:
			supported = false
			reason = errUnknownAccessMode
			break
		case csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER:
			break
		case csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY:
			break
		case csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY:
			break
		case csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER:
			fallthrough
		case csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER:
			if !isBlock {
				supported = false
				reason = errNoMultiNodeWriter
			}
			break
		default:
			// This is to guard against new access modes not understood
			supported = false
			reason = errUnknownAccessMode
		}
	}

	return supported, reason
}

func (s *service) ListVolumes(
	ctx context.Context,
	req *csi.ListVolumesRequest) (
	*csi.ListVolumesResponse, error) {

	return nil, status.Error(codes.Unimplemented, "")
}

func (s *service) ListSnapshots(
	ctx context.Context,
	req *csi.ListSnapshotsRequest) (
	*csi.ListSnapshotsResponse, error) {

	return nil, status.Error(codes.Unimplemented, "")

}

func (s *service) GetCapacity(
	ctx context.Context,
	req *csi.GetCapacityRequest) (
	*csi.GetCapacityResponse, error) {

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		reqID = headers["csi.requestid"][0]
	}

	if err := s.requireProbe(ctx); err != nil {
		log.Error("Failed to probe with error: " + err.Error())
		return nil, err
	}

	// Optionally validate the volume capability
	vcs := req.GetVolumeCapabilities()
	if vcs != nil {
		supported, reason := valVolumeCaps(vcs, nil)
		if !supported {
			log.Error("GetVolumeCapabilities failed with error: " + reason)
			return nil, status.Errorf(codes.InvalidArgument, reason)
		}
		fmt.Printf("Supported capabilities - Error(%s)\n", reason)
	}

	params := req.GetParameters()
	if len(params) <= 0 {
		log.Error("GetCapacity: Required StoragePool and SymID in parameters")
		return nil, status.Errorf(codes.InvalidArgument, "GetCapacity: Required StoragePool and SymID in parameters")
	}
	symmetrixID := params[SymmetrixIDParam]
	if symmetrixID == "" {
		log.Error("A SYMID parameter is required")
		return nil, status.Errorf(codes.InvalidArgument, "A SYMID parameter is required")
	}

	// Storage (resource) Pool. Validate it against exist Pools
	storagePoolID := params[StoragePoolParam]
	err := s.validateStoragePoolID(symmetrixID, storagePoolID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Errorf(codes.InvalidArgument, err.Error())
	}

	// log all parameters used in GetCapacity call
	fields := map[string]interface{}{
		"SymmetrixID":  symmetrixID,
		"SRP":          storagePoolID,
		"CSIRequestID": reqID,
	}
	log.WithFields(fields).Info("Executing ValidateVolumeCapabilities with following fields")

	// Get storage pool info
	srp, err := s.adminClient.GetStoragePool(symmetrixID, storagePoolID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not retrieve StoragePool %s. Error(%s)", storagePoolID, err.Error())
	}

	totalSrpCapInGB := srp.SrpCap.UsableTotInTB * 1024
	usedSrpCapInGB := srp.SrpCap.UsableUsedInTB * 1024
	remainingCapInGB := totalSrpCapInGB - usedSrpCapInGB
	remainingCapInBytes := remainingCapInGB * 1024 * 1024 * 1024

	return &csi.GetCapacityResponse{
		AvailableCapacity: int64(remainingCapInBytes),
	}, nil
}

func (s *service) ControllerGetCapabilities(
	ctx context.Context,
	req *csi.ControllerGetCapabilitiesRequest) (
	*csi.ControllerGetCapabilitiesResponse, error) {

	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: []*csi.ControllerServiceCapability{
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_VOLUME,
					},
				},
			},
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_PUBLISH_UNPUBLISH_VOLUME,
					},
				},
			},
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_GET_CAPACITY,
					},
				},
			},
		},
	}, nil
}

func (s *service) controllerProbe(ctx context.Context) error {

	log.Debug("Entering controllerProbe")
	defer log.Debug("Exiting controllerProbe")

	// Check that we have the details needed to login to the Gateway
	if s.opts.Endpoint == "" {
		return status.Error(codes.FailedPrecondition,
			"missing Unisphere endpoint")
	}
	if s.opts.User == "" {
		return status.Error(codes.FailedPrecondition,
			"missing Unisphere user")
	}
	if s.opts.Password == "" {
		return status.Error(codes.FailedPrecondition,
			"missing Unisphere password")
	}

	log.Debugf("Portgroups length is: %d", len(s.opts.PortGroups))
	for _, p := range s.opts.PortGroups {
		log.Debugf("Portgroups include: %s", p)
	}
	if len(s.opts.AllowedArrays) == 0 {
		log.Debug("Allowing access to all Arrays known to Unisphere")
	} else {
		log.Debugf("Restricting access to the following Arrays: %v", s.opts.AllowedArrays)
	}

	err := s.createPowerMaxClient()
	if err != nil {
		return err
	}

	return nil
}

func (s *service) requireProbe(ctx context.Context) error {
	if s.adminClient == nil {
		if !s.opts.AutoProbe {
			return status.Error(codes.FailedPrecondition,
				"Controller Service has not been probed")
		}
		log.Debug("probing controller service automatically")
		if err := s.controllerProbe(ctx); err != nil {
			return status.Errorf(codes.FailedPrecondition,
				"failed to probe/init plugin: %s", err.Error())
		}
	}
	return nil
}

func (s *service) SelectPortGroup() (string, error) {
	if len(s.opts.PortGroups) == 0 {
		return "", fmt.Errorf("No port groups have been supplied")
	}

	// select a random port group
	n := rand.Int() % len(s.opts.PortGroups)
	pg := s.opts.PortGroups[n]
	return pg, nil
}

// CreateSnapshot creates a snapshot.
// If Parameters["VolumeIDList"] has a comma separated list of additional volumes, they will be
// snapshotted in a consistency group with the primary volume in CreateSnapshotRequest.SourceVolumeId.
func (s *service) CreateSnapshot(
	ctx context.Context,
	req *csi.CreateSnapshotRequest) (
	*csi.CreateSnapshotResponse, error) {

	return nil, status.Error(codes.Unimplemented, "")
}

func (s *service) DeleteSnapshot(
	ctx context.Context,
	req *csi.DeleteSnapshotRequest) (
	*csi.DeleteSnapshotResponse, error) {

	return nil, status.Error(codes.Unimplemented, "")

}

// mergeStringMaps copies the additional key/value pairs into the base string map.
func mergeStringMaps(base map[string]string, additional map[string]string) map[string]string {
	result := make(map[string]string)
	if base != nil {
		for k, v := range base {
			result[k] = v
		}
	}
	if additional != nil {
		for k, v := range additional {
			result[k] = v
		}
	}
	return result
}
