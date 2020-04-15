/*
 Copyright © 2020 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	// "errors"
	// "fmt"

	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	types "github.com/dell/gopowermax/types/v90"
	"github.com/golang/protobuf/ptypes"
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
	MaxVolumeSizeBytes = 1099512545280
	// GiB is 1 Gibibyte in bytes
	GiB                             = 1073741824
	removeModeOnlyMe                = "ONLY_ME"
	errNoMultiMap                   = "volume not enabled for mapping to multiple hosts"
	errUnknownAccessType            = "unknown access type is not Block or Mount"
	errUnknownAccessMode            = "access mode cannot be UNKNOWN"
	errNoMultiNodeWriter            = "multi-node with writer(s) only supported for block access type"
	TRUE                            = "TRUE"
	FALSE                           = "FALSE"
	StoragePoolCacheDuration        = 4 * time.Hour
	MaxVolIdentifierLength          = 64
	MaxPortGroupIdentifierLength    = 64
	MaxClusterPrefixLength          = 3
	NumOfVolIDAttributes            = 4
	CSIPrefix                       = "csi"
	DeletionPrefix                  = "_DEL"
	SymmetricIDLength               = 12
	DeviceIDLength                  = 5
	CsiHostPrefix                   = "csi-node-"
	CsiMVPrefix                     = "csi-mv-"
	CsiNoSrpSGPrefix                = "csi-no-srp-sg-"
	CsiVolumePrefix                 = "csi-"
	PublishContextDeviceWWN         = "DEVICE_WWN"
	PublishContextLUNAddress        = "LUN_ADDRESS"
	PortIdentifiers                 = "PORT_IDENTIFIERS"
	FCSuffix                        = "-FC"
	PGSuffix                        = "PG"
	notFound                        = "not found"       // error message from s.GetVolumeByID when volume not found
	cannotBeFound                   = "cannot be found" // error message from pmax when volume not found
	ignoredViaAWhitelist            = "ignored via a whitelist"
	failedToValidateVolumeNameAndID = "Failed to validate combination of Volume Name and Volume ID"
	errDeviceInStorageGrp           = "device is a member of a storage group"
	IscsiTransportProtocol          = "ISCSI"
	FcTransportProtocol             = "FC"
	MaxSnapIdentifierLength         = 32
	SnapDelPrefix                   = "DEL"
	delSrcTag                       = "DS"
	SnapLock                        = "snapshot_lock"
)

// Keys for parameters to CreateVolume
const (
	SymmetrixIDParam  = "SYMID"
	ServiceLevelParam = "ServiceLevel"
	ContentSource     = "VolumeContentSource"
	StoragePoolParam  = "SRP"
	// If storage_group is set, this over-rides the generation of the Storage Group from SLO/SRP
	StorageGroupParam      = "StorageGroup"
	ThickVolumesParam      = "ThickVolumes" // "true" or "false" or "" (defaults thin)
	ApplicationPrefixParam = "ApplicationPrefix"
	CapacityGB             = "CapacityGB"
	uCode5978              = 5978
	uCodeELMSR             = 221
)

//Pair - structure which holds a pair
type Pair struct {
	first, second interface{}
}

var nodeCache sync.Map

// Information cached about a pmax
type pmaxCachedInformation struct {
	// Existence of a StoragePool discovered at indicated time
	knownStoragePools map[string]time.Time
	// Pair of a map containing dirPortKey to portIdentifier mapping
	// and a timestamp indicating when this map was created
	portIdentifiers *Pair
	uCodeVersion    *Pair
}

// Initializes a pmaxCachedInformation type
func (p *pmaxCachedInformation) initialize() {
	p.knownStoragePools = make(map[string]time.Time)
	p.portIdentifiers = nil
	p.uCodeVersion = nil
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

	// Quickly fail if there is too much load
	controllerPendingState pendingState
)

func (s *service) GetPortIdentifier(symID string, dirPortKey string) (string, error) {
	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()
	portIdentifier := ""
	cache := getPmaxCache(symID)
	cacheExpired := false
	if cache.portIdentifiers != nil {
		portTimeStamp := cache.portIdentifiers.second.(time.Time)
		if time.Now().Sub(portTimeStamp) < s.storagePoolCacheDuration {
			// We have a valid cache entry
			dirPortKeys := cache.portIdentifiers.first.(map[string]string)
			portIdentifier, ok := dirPortKeys[dirPortKey]
			if ok {
				log.Debug("Cache hit for :" + dirPortKey)
				return portIdentifier, nil
			}
		} else {
			cacheExpired = true
		}
	}
	// Example dirPortKeys: FA-1D:4 (FC), SE-1E:24 (ISCSI)
	dirPortDetails := strings.Split(dirPortKey, ":")
	dirID := dirPortDetails[0]
	portNumber := dirPortDetails[1]
	// Fetch the port details
	port, err := s.adminClient.GetPort(symID, dirID, portNumber)
	if err != nil {
		log.Errorf("Couldn't get port details for %s. Error: %s", dirPortKey, err.Error())
		return "", err
	}
	log.Debugf("Symmetrix ID: %s, DirPortKey: %s, Port type: %s",
		symID, dirPortKey, port.SymmetrixPort.Type)
	if strings.Contains(port.SymmetrixPort.Type, "FibreChannel") {
		// Add "0x" to the FC Port WWN as that is used by gofsutils to differentiate between FC and ISCSI
		portIdentifier = "0x"
	}
	portIdentifier += port.SymmetrixPort.Identifier
	if cache.portIdentifiers != nil {
		if cacheExpired {
			// Invalidate all the values in cache for this symmetrix
			log.Debug("Cache expired. Rebuilding port identifier cache with entry for : " + dirPortKey)
			dirPortKeys := make(map[string]string)
			dirPortKeys[dirPortKey] = portIdentifier
			cache.portIdentifiers = &Pair{
				first:  dirPortKeys,
				second: time.Now(),
			}
		} else {
			// We are adding a new port identifier to the cache
			log.Debug("Adding value to port identifier cache for :" + dirPortKey)
			dirPortKeys := cache.portIdentifiers.first.(map[string]string)
			dirPortKeys[dirPortKey] = portIdentifier
		}
	} else {
		// Build the cache
		dirPortKeys := make(map[string]string)
		dirPortKeys[dirPortKey] = portIdentifier
		cache.portIdentifiers = &Pair{
			first:  dirPortKeys,
			second: time.Now(),
		}
		log.Debug("Port identifier cache created with entry for :" + dirPortKey)
	}
	return portIdentifier, nil
}

func (s *service) CreateVolume(
	ctx context.Context,
	req *csi.CreateVolumeRequest) (
	*csi.CreateVolumeResponse, error) {

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
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

	// Get the required capacity
	cr := req.GetCapacityRange()
	requiredCylinders, err := s.validateVolSize(cr, symmetrixID, storagePoolID)
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
		switch req.GetVolumeContentSource().GetType().(type) {
		case *csi.VolumeContentSource_Volume:
			srcVolID = req.GetVolumeContentSource().GetVolume().GetVolumeId()
			if srcVolID != "" {
				_, symID, SrcDevID, err = s.parseCsiID(srcVolID)
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
				snapID, symID, SrcDevID, err = s.parseCsiID(srcSnapID)
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
		if err := s.IsSnapshotLicensed(symID); err != nil {
			log.Error("Error - " + err.Error())
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if SrcDevID != "" && symID != "" {
		if symID != symmetrixID {
			log.Error("The volume content source is in different PowerMax array")
			return nil, status.Errorf(codes.Internal, "The volume content source is in different PowerMax array")
		}
		srcVol, err = s.adminClient.GetVolumeByID(symmetrixID, SrcDevID)
		if err != nil {
			log.Error("Volume content source volume couldn't be found in the array: " + err.Error())
			return nil, status.Errorf(codes.Internal, "Volume content source volume couldn't be found in the array: %s", err.Error())
		}
		// reset the volume size to match with source
		if requiredCylinders < srcVol.CapacityCYL {
			log.Error("Capacity specified is smaller than the source")
			return nil, status.Error(codes.InvalidArgument, "Requested capacity is smaller than the source")
		}
	}

	// Validate volume capabilities
	vcs := req.GetVolumeCapabilities()
	if vcs != nil {
		isBlock := accTypeIsBlock(vcs)
		if isBlock && !s.opts.EnableBlock {
			return nil, status.Error(codes.InvalidArgument, "Block Volume Capability is not supported")
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
		"SourceVolume":      srcVolID,
		"SourceSnapshot":    srcSnapID,
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

	// Idempotency test. We w	ill read the volume and check for:
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
				CapacityGB:        fmt.Sprintf("%.2f", vol.CapacityGB),
				ContentSource:     volContent,
				//Format the time output
				"CreationTime": time.Now().Format("20060102150405"),
			}
			volResp.VolumeContext = attributes
			csiResp := &csi.CreateVolumeResponse{
				Volume: volResp,
			}
			volResp.ContentSource = contentSource
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
	// If volume content source is specified, initiate no_copy to newly created volume
	if contentSource != nil {
		if srcVolID != "" {
			//Build the temporary snapshot identifier
			snapID := fmt.Sprintf("%s%s-%d", TempSnap, s.getClusterPrefix(), time.Now().Nanosecond())
			err = s.LinkVolumeToVolume(symID, srcVol, vol.VolumeID, snapID, reqID)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "Failed to create volume from volume (%s)", err.Error())
			}
		} else if srcSnapID != "" {
			err = s.LinkVolumeToSnapshot(symID, srcVol.VolumeID, vol.VolumeID, snapID, reqID)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "Failed to create volume from snapshot (%s)", err.Error())
			}
		}
	}
	// Formulate the return response
	vol.VolumeID = fmt.Sprintf("%s-%s-%s", volumeIdentifier, symmetrixID, vol.VolumeID)
	volResp := s.getCSIVolume(vol)
	volResp.ContentSource = contentSource
	//Set the volume context
	attributes := map[string]string{
		ServiceLevelParam: serviceLevel,
		StoragePoolParam:  storagePoolID,
		CapacityGB:        fmt.Sprintf("%.2f", vol.CapacityGB),
		ContentSource:     volContent,
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
func (s *service) validateVolSize(cr *csi.CapacityRange, symmetrixID, storagePoolID string) (int, error) {

	var minSizeBytes, maxSizeBytes int64
	minSizeBytes = cr.GetRequiredBytes()
	maxSizeBytes = cr.GetLimitBytes()

	if minSizeBytes < 0 || maxSizeBytes < 0 {
		return 0, status.Errorf(
			codes.OutOfRange,
			"bad capacity: requested volume size bytes %d and limit size bytes: %d must not be negative", minSizeBytes, maxSizeBytes)
	}

	if minSizeBytes == 0 {
		minSizeBytes = DefaultVolumeSizeBytes
	}

	var maxAvailBytes int64 = MaxVolumeSizeBytes
	if symmetrixID != "" && storagePoolID != "" {
		// Normal path
		srpCap, err := s.getStoragePoolCapacities(symmetrixID, storagePoolID)
		if err != nil {
			return 0, err
		}
		totalSrpCapInGB := srpCap.UsableTotInTB * 1024.0
		usedSrpCapInGB := srpCap.UsableUsedInTB * 1024.0
		remainingCapInGB := totalSrpCapInGB - usedSrpCapInGB
		// maxAvailBytes is the remaining capacity in bytes
		maxAvailBytes = int64(remainingCapInGB) * 1024 * 1024 * 1024
		log.Infof("totalSrcCapInGB %f usedSrpCapInGB %f remainingCapInGB %f maxAvailBytes %d",
			totalSrpCapInGB, usedSrpCapInGB, remainingCapInGB, maxAvailBytes)
	}

	if maxSizeBytes == 0 {
		maxSizeBytes = maxAvailBytes
	}

	if maxSizeBytes > maxAvailBytes {
		return 0, status.Errorf(
			codes.OutOfRange,
			"bad capacity: requested maximum size (%d bytes) is greater than the maximum available capacity (%d bytes)", maxSizeBytes, maxAvailBytes)
	}
	if minSizeBytes > maxAvailBytes {
		return 0, status.Errorf(
			codes.OutOfRange,
			"bad capacity: requested minimum size (%d bytes) is greater than the maximum available capacity (%d bytes)", minSizeBytes, maxAvailBytes)
	}
	if minSizeBytes < MinVolumeSizeBytes {
		return 0, status.Errorf(
			codes.OutOfRange,
			"bad capacity: requested minimum size (%d bytes) is less than the minimum volume size (%d bytes)", minSizeBytes, MinVolumeSizeBytes)
	}
	if maxSizeBytes < minSizeBytes {
		return 0, status.Errorf(
			codes.OutOfRange,
			"bad capacity: requested maximum size (%d bytes) is less than the requested minimum size (%d bytes)", maxSizeBytes, minSizeBytes)
	}
	minNumberOfCylinders := int(minSizeBytes / cylinderSizeInBytes)
	var numOfCylinders int
	if minSizeBytes%cylinderSizeInBytes > 0 {
		numOfCylinders = minNumberOfCylinders + 1
	} else {
		numOfCylinders = minNumberOfCylinders
	}
	sizeInBytes := int64(numOfCylinders * cylinderSizeInBytes)
	if sizeInBytes > maxSizeBytes {
		return 0, status.Errorf(
			codes.OutOfRange,
			"bad capacity: size in bytes %d exceeds limit size bytes %d", sizeInBytes, maxSizeBytes)
	}
	return numOfCylinders, nil
}

// Validates Storage Pool IDs, keeps a cache. The cache entries are only  valid for
// the StoragePoolCacheDuration period, after that they are rechecked.
func (s *service) validateStoragePoolID(symmetrixID string, storagePoolID string) error {
	if storagePoolID == "" {
		return fmt.Errorf("A valid SRP parameter is required")
	}
	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()

	cache := getPmaxCache(symmetrixID)

	if !cache.knownStoragePools[storagePoolID].IsZero() {
		storagePoolTimeStamp := cache.knownStoragePools[storagePoolID]
		if time.Now().Sub(storagePoolTimeStamp) < s.storagePoolCacheDuration {
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

// isPostElmSR - checks if the array is running ucode higher than the
// ELM SR ucode version. ELM SR is the release name for uCode version - 5978.221.221
func (s *service) isPostElmSR(symmetrixID string) (bool, error) {
	cacheMiss := false
	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()
	cache := getPmaxCache(symmetrixID)
	uCodeVersion := ""
	if cache.uCodeVersion != nil {
		uCodeTimeStamp := cache.uCodeVersion.second.(time.Time)
		if time.Now().Sub(uCodeTimeStamp) < s.storagePoolCacheDuration {
			// We have a valid cache entry
			uCodeVersion = cache.uCodeVersion.first.(string)
		}
	}
	// If entry not found in cache
	if uCodeVersion == "" {
		cacheMiss = true
		symmetrix, err := s.adminClient.GetSymmetrixByID(symmetrixID)
		if err != nil {
			return false, err
		}
		uCodeVersion = symmetrix.Ucode
		// Update the cache
		cache.uCodeVersion = &Pair{
			first:  uCodeVersion,
			second: time.Now(),
		}
	}
	var majorVersion, minorVersion, patchversion int
	_, err := fmt.Sscanf(uCodeVersion, "%d.%d.%d", &majorVersion, &minorVersion, &patchversion)
	if err != nil {
		log.Errorf("Failed to parse the uCode version string: %s", uCodeVersion)
		return false, err
	}
	if cacheMiss {
		log.Infof("Cache miss: Ucode details for %s - Major version: %d, Minor version: %d, Patch: %d",
			symmetrixID, majorVersion, minorVersion, patchversion)
	}
	if majorVersion >= uCode5978 && minorVersion > uCodeELMSR {
		return true, nil
	}
	return false, nil
}

// Only used for testing
func (s *service) setStoragePoolCacheDuration(duration time.Duration) {
	s.storagePoolCacheDuration = duration
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

func splitFibreChannelInitiatorID(initiatorID string) (string, string, string, error) {
	initElements := strings.Split(initiatorID, ":")
	initiator := ""
	dir := ""
	dirPort := ""
	if len(initElements) == 3 {
		dir = initElements[0]
		dirPort = initElements[0] + ":" + initElements[1]
		initiator = initElements[2]
	} else {
		return "", "", "", fmt.Errorf("Failed to parse the initiator ID - %s", initiatorID)
	}
	return dir, dirPort, initiator, nil
}

// Create a CSI VolumeId from component parts.
func (s *service) createCSIVolumeID(volumePrefix, volumeName, symID, devID string) string {
	//return fmt.Sprintf("%s-%s-%s-%s", volumePrefix, volumeName, symID, devID)
	return fmt.Sprintf("%s%s-%s-%s-%s", CsiVolumePrefix, s.getClusterPrefix(), volumeName, symID, devID)
}

// parseCsiID returns the VolumeName, Array ID, and Device ID given the CSI ID.
// The last 19 characters of the CSI volume ID are special:
//      A dash '-', followed by 12 digits of array serial number, followed by a dash, followed by 5 digits of array device id.
//	That's 19 characters total on the right end.
// Also an error returned if mal-formatted.
func (s *service) parseCsiID(csiID string) (
	volName string, arrayID string, devID string, err error) {
	if csiID == "" {
		err = fmt.Errorf("A Volume ID is required for the request")
		return
	}
	// get the Device ID and Array ID
	idComponents := strings.Split(csiID, "-")
	// Protect against mal-formed component
	numOfIDComponents := len(idComponents)
	if numOfIDComponents < 3 {
		// Not well formed
		err = fmt.Errorf("The CSI ID %s is not formed correctly", csiID)
		return
	}
	// Device ID is the last token
	devID = idComponents[numOfIDComponents-1]
	// Array ID is the second to last token
	arrayID = idComponents[numOfIDComponents-2]

	// The two here is for two dashes - one at front of array ID and one between the Array ID and Device ID
	lengthOfTrailer := len(devID) + len(arrayID) + 2
	length := len(csiID)
	if length <= lengthOfTrailer+2 {
		// Not well formed...
		err = fmt.Errorf("The CSI ID %s is not formed correctly", csiID)
		return
	}
	// calculate the volume name, which is everything before the array ID
	volName = csiID[0 : length-lengthOfTrailer]
	return
}

func (s *service) DeleteVolume(
	ctx context.Context,
	req *csi.DeleteVolumeRequest) (
	*csi.DeleteVolumeResponse, error) {

	id := req.GetVolumeId()
	volName, symID, devID, err := s.parseCsiID(id)
	if err != nil {
		// We couldn't comprehend the identifier.
		log.Info("Could not parse CSI VolumeId: " + id)
		return &csi.DeleteVolumeResponse{}, nil
	}
	var volumeID volumeIDType = volumeIDType(id)
	if err := volumeID.checkAndUpdatePendingState(&controllerPendingState); err != nil {
		return nil, err
	}
	defer volumeID.clearPending(&controllerPendingState)

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	if err := s.requireProbe(ctx); err != nil {
		log.Error("Failed to probe with erro: " + err.Error())
		return nil, err
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
	log.Debugf("vol: %#v, error: %#v\n", vol, err)
	if err != nil {
		if strings.Contains(err.Error(), cannotBeFound) || strings.Contains(err.Error(), ignoredViaAWhitelist) {
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

	// Verify if volume is snapshot source
	isSnapSrc, err := s.IsSnapshotSource(symID, devID)
	if err != nil {
		log.Error("Failed to determine volume as a snapshot source: Error - ", err.Error())
		return nil, status.Errorf(codes.Internal, err.Error())
	}
	if isSnapSrc {
		//Execute soft delete i.e. return DeleteVolume success to the CO/k8s
		//after appending the volumeID with 'DS' tag. While appending the tag, ensure
		//that length of volume name shouldn't exceed MaxVolIdentifierLength

		var newVolName string
		//truncLen is number of characters to truncate if new volume name with DS tag exceeds
		//MaxVolIdentifierLength
		truncLen := len(volName) + len(delSrcTag) - MaxVolIdentifierLength
		if truncLen > 0 {
			//Truncate volName to fit in '-' and 'DS' tag
			volName = volName[:len(volName)-truncLen]
		}
		newVolName = fmt.Sprintf("%s-%s", volName, delSrcTag)
		vol, err = s.adminClient.RenameVolume(symID, devID, newVolName)
		if err != nil || vol == nil {
			log.Error(fmt.Sprintf("DeleteVolume: Could not rename volume %s", id))
			return nil, status.Errorf(codes.Internal, "Failed to rename volume")
		}
		log.Infof("Soft deletion of source volume (%s) is successful", volName)
		return &csi.DeleteVolumeResponse{}, nil
	}
	err = s.MarkVolumeForDeletion(symID, vol)
	if err != nil {
		log.Error("RequestSoftVolDelete failed with error - ", err.Error())
		return nil, status.Errorf(codes.Internal, "Failed marking volume for deletion with error (%s)", err.Error())
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func (s *service) ControllerPublishVolume(
	ctx context.Context,
	req *csi.ControllerPublishVolumeRequest) (
	*csi.ControllerPublishVolumeResponse, error) {

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	volumeContext := req.GetVolumeContext()
	if volumeContext != nil {
		log.Infof("VolumeContext:")
		for key, value := range volumeContext {
			log.Infof("    [%s]=%s", key, value)
		}
	}

	volID := req.GetVolumeId()
	if volID == "" {
		log.Error("volume ID is required")
		return nil, status.Error(codes.InvalidArgument,
			"volume ID is required")
	}
	var volumeID volumeIDType = volumeIDType(volID)
	if err := volumeID.checkAndUpdatePendingState(&controllerPendingState); err != nil {
		return nil, err
	}
	defer volumeID.clearPending(&controllerPendingState)

	if err := s.requireProbe(ctx); err != nil {
		log.Error("Failed to probe with error: " + err.Error())
		return nil, err
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
	isISCSI := false
	// Check if node ID is present in cache
	nodeInCache := false
	cacheID := symID + ":" + nodeID
	tempHostID, ok := nodeCache.Load(cacheID)
	if ok {
		log.Debugf("REQ ID: %s Loaded nodeID: %s, hostID: %s from node cache\n",
			reqID, nodeID, tempHostID.(string))
		nodeInCache = true
		if !strings.Contains(tempHostID.(string), "-FC") {
			isISCSI = true
		}
	} else {
		log.Debugf("REQ ID: %s nodeID: %s not present in node cache\n", reqID, nodeID)
		isISCSI, err = s.IsNodeISCSI(symID, nodeID)
		if err != nil {
			return nil, status.Error(codes.NotFound, err.Error())
		}
	}
	hostID, tgtStorageGroupID, tgtMaskingViewID := s.GetHostSGAndMVIDFromNodeID(nodeID, isISCSI)
	if !nodeInCache {
		// Update the map
		val, ok := nodeCache.LoadOrStore(cacheID, hostID)
		if !ok {
			log.Debugf("REQ ID: %s Added nodeID: %s, hostID: %s to node cache\n", reqID, nodeID, hostID)
		} else {
			log.Debugf("REQ ID: %s Some other goroutine added hostID: %s for node: %s to node cache\n",
				reqID, val.(string), nodeID)
			if hostID != val.(string) {
				log.Warningf("REQ ID: %s Mismatch between calculated value: %s and latest value: %s from node cache\n",
					reqID, val.(string), hostID)
			}
		}
	}
	lockNum := RequestLock(tgtStorageGroupID, reqID)
	defer ReleaseLock(tgtStorageGroupID, reqID, lockNum)

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
			// make sure we got at least one masking view
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
					return s.updatePublishContext(publishContext, symID, tgtMaskingViewID, devID)
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
				return s.updatePublishContext(publishContext, symID, tgtMaskingViewID, devID)
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
		// We need to create a Masking view
		// First fetch the host details
		host, err := s.adminClient.GetHostByID(symID, hostID)
		retry := false
		if err != nil {
			// Retry once more after a gap of 2 seconds
			retry = true
			time.Sleep(2 * time.Second)
		}
		if retry {
			host, err = s.adminClient.GetHostByID(symID, hostID)
		}
		if err != nil {
			errormsg := fmt.Sprintf(
				"ControllerPublishVolume: Failed to fetch host details for host %s on %s with error - %s", hostID, symID, err.Error())
			log.Error(errormsg)
			return nil, status.Error(codes.NotFound, errormsg)
		}
		//Fetch or create a port Group
		portGroupID, err := s.SelectOrCreatePortGroup(symID, host)
		if err != nil {
			errormsg := fmt.Sprintf(
				"ControllerPublishVolume: Failed to select/create PG for host %s on %s with error - %s", hostID, symID, err.Error())
			log.Error(errormsg)
			return nil, status.Error(codes.Internal, errormsg)
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

	return s.updatePublishContext(publishContext, symID, tgtMaskingViewID, devID)
}

// Adds the LUN_ADDRESS and SCSI target information to the PublishContext by looking at MaskingView connections.
// The return arguments are suitable for directly passing back to the grpc called.
// This routine is careful to throw errors if it cannot come up with a valid context, because having ControllerPublish
// succeed but without valid context will only cause the NodeStage or NodePublish to fail.
func (s *service) updatePublishContext(publishContext map[string]string, symID, tgtMaskingViewID, deviceID string) (*csi.ControllerPublishVolumeResponse, error) {
	// We ignore errors here; adding the HostLunAddress to the context is optional (could be multiple.)
	// Adding target information is also optional as NodePublish may succeed without target information
	var connections []*types.MaskingViewConnection
	var err error
	connections, err = s.adminClient.GetMaskingViewConnections(symID, tgtMaskingViewID, deviceID)
	if err != nil {
		log.Error("Could not get MV Connections: " + err.Error())
		return nil, status.Errorf(codes.Internal, "PublishContext: Could not get MV Connections: %s", tgtMaskingViewID)
	}
	lunid := ""
	dirPorts := make([]string, 0)
	for _, conn := range connections {
		if deviceID != conn.VolumeID {
			log.Debug("MV Connection: VolumeID != deviceID")
			continue
		}
		if lunid == "" {
			lunid = conn.HostLUNAddress
			dirPorts = appendIfMissing(dirPorts, conn.DirectorPort)

		} else if lunid != conn.HostLUNAddress {
			log.Infof("MV Connection: Multiple HostLUNAddress values")
			return nil, status.Error(codes.Internal, "PublishContext: MV Connection has multiple HostLUNAddress values")
		} else {
			// Add each port per entry for the volume in masking view connections
			dirPorts = appendIfMissing(dirPorts, conn.DirectorPort)
		}
	}
	portIdentifiers := ""
	for _, dirPortKey := range dirPorts {
		portIdentifier, err := s.GetPortIdentifier(symID, dirPortKey)
		if err != nil {
			continue
		}
		portIdentifiers += portIdentifier + ","
	}
	if portIdentifiers == "" {
		log.Errorf("Failed to fetch port details for any director ports which are part of masking view: %s", tgtMaskingViewID)
		return nil, status.Errorf(codes.Internal,
			"PublishContext: Failed to fetch port details for any director ports which are part of masking view: %s", tgtMaskingViewID)
	}
	log.Debugf("Port identifiers in publish context: %s", portIdentifiers)
	publishContext[PortIdentifiers] = portIdentifiers

	if lunid == "" {
		return nil, status.Error(codes.Internal, "PublishContext: Could not determine HostLUNAddress")
	}
	publishContext[PublishContextLUNAddress] = lunid
	return &csi.ControllerPublishVolumeResponse{PublishContext: publishContext}, nil
}

// IsNodeISCSI - Takes a sym id, node id as input and based on the transport protocol setting
// and the existence of the host on array, it returns a bool to indicate if the Host
// on array is ISCSI or not
func (s *service) IsNodeISCSI(symID, nodeID string) (bool, error) {
	fcHostID, _, fcMaskingViewID := s.GetFCHostSGAndMVIDFromNodeID(nodeID)
	iSCSIHostID, _, iSCSIMaskingViewID := s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	if s.opts.TransportProtocol == FcTransportProtocol || s.opts.TransportProtocol == "" {
		log.Debug("Preferred transport protocol is set to FC")
		_, fcmverr := s.adminClient.GetMaskingViewByID(symID, fcMaskingViewID)
		if fcmverr == nil {
			return false, nil
		}
		// Check if ISCSI MV exists
		_, iscsimverr := s.adminClient.GetMaskingViewByID(symID, iSCSIMaskingViewID)
		if iscsimverr == nil {
			return true, nil
		}
		// Check if FC Host exists
		fcHost, fcHostErr := s.adminClient.GetHostByID(symID, fcHostID)
		if fcHostErr == nil {
			if fcHost.HostType == "Fibre" {
				return false, nil
			}
		}
		// Check if ISCSI Host exists
		iscsiHost, iscsiHostErr := s.adminClient.GetHostByID(symID, iSCSIHostID)
		if iscsiHostErr == nil {
			if iscsiHost.HostType == "iSCSI" {
				return true, nil
			}
		}
	} else if s.opts.TransportProtocol == IscsiTransportProtocol {
		log.Debug("Preferred transport protocol is set to ISCSI")
		// Check if ISCSI MV exists
		_, iscsimverr := s.adminClient.GetMaskingViewByID(symID, iSCSIMaskingViewID)
		if iscsimverr == nil {
			return true, nil
		}
		// Check if FC MV exists
		_, fcmverr := s.adminClient.GetMaskingViewByID(symID, fcMaskingViewID)
		if fcmverr == nil {
			return false, nil
		}
		// Check if ISCSI Host exists
		iscsiHost, iscsiHostErr := s.adminClient.GetHostByID(symID, iSCSIHostID)
		if iscsiHostErr == nil {
			if iscsiHost.HostType == "iSCSI" {
				return true, nil
			}
		}
		// Check if FC Host exists
		fcHost, fcHostErr := s.adminClient.GetHostByID(symID, fcHostID)
		if fcHostErr == nil {
			if fcHost.HostType == "Fibre" {
				return false, nil
			}
		}
	}
	return false, fmt.Errorf("Failed to fetch host id from array for node: %s", nodeID)
}

// GetVolumeByID - Takes a CSI volume ID and checks for its existence on array
// along with matching with the volume identifier. Returns back the volume name
// on array, device ID, volume structure
func (s *service) GetVolumeByID(volID string) (string, string, *types.Volume, error) {
	// parse the volume and get the array serial and volume ID
	volName, symID, devID, err := s.parseCsiID(volID)
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
			failedToValidateVolumeNameAndID)
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

// GetHostSGAndMVIDFromNodeID - Gets the Host ID, SG ID, MV ID given a node ID and
// a boolean which indicates if node is FC or ISCSI
func (s *service) GetHostSGAndMVIDFromNodeID(nodeID string, isISCSI bool) (string, string, string) {
	if isISCSI {
		return s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	}
	return s.GetFCHostSGAndMVIDFromNodeID(nodeID)
}

// GetISCSIHostSGAndMVIDFromNodeID - Forms HostID, StorageGroupID, MaskingViewID
// using the NodeID and returns them
func (s *service) GetISCSIHostSGAndMVIDFromNodeID(nodeID string) (string, string, string) {
	hostID := CsiHostPrefix + s.getClusterPrefix() + "-" + nodeID
	storageGroupID := CsiNoSrpSGPrefix + s.getClusterPrefix() + "-" + nodeID
	maskingViewID := CsiMVPrefix + s.getClusterPrefix() + "-" + nodeID
	return hostID, storageGroupID, maskingViewID
}

// GetFCHostSGAndMVIDFromNodeID - Forms fibrechannel HostID, StorageGroupID, MaskingViewID
// These are the same as iSCSI except for "_FC" is added as a suffix.
func (s *service) GetFCHostSGAndMVIDFromNodeID(nodeID string) (string, string, string) {
	hostID, storageGroupID, maskingViewID := s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	return hostID + FCSuffix, storageGroupID + FCSuffix, maskingViewID + FCSuffix
}

func (s *service) ControllerUnpublishVolume(
	ctx context.Context,
	req *csi.ControllerUnpublishVolumeRequest) (
	*csi.ControllerUnpublishVolumeResponse, error) {

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	volID := req.GetVolumeId()
	if volID == "" {
		log.Error("GetVolumeId : Volume ID is required ")
		return nil, status.Error(codes.InvalidArgument,
			"Volume ID is required")
	}
	var volumeID volumeIDType = volumeIDType(volID)
	if err := volumeID.checkAndUpdatePendingState(&controllerPendingState); err != nil {
		return nil, err
	}
	defer volumeID.clearPending(&controllerPendingState)

	nodeID := req.GetNodeId()
	if nodeID == "" {
		log.Error("Node ID is required")
		return nil, status.Error(codes.InvalidArgument,
			"Node ID is required")
	}

	if err := s.requireProbe(ctx); err != nil {
		log.Error("Failed to probe with error: " + err.Error())
		return nil, err
	}

	//Fetch the volume details from array
	symID, devID, vol, err := s.GetVolumeByID(volID)
	if err != nil {
		// CSI sanity test will call this idempotently and expects pass
		if strings.Contains(err.Error(), notFound) || strings.Contains(err.Error(), failedToValidateVolumeNameAndID) {
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
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

	// Determine if the volume is in a FC or ISCSI MV
	_, tgtFCStorageGroupID, tgtFCMaskingViewID := s.GetFCHostSGAndMVIDFromNodeID(nodeID)
	_, tgtISCSIStorageGroupID, tgtISCSIMaskingViewID := s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	isISCSI := false
	// Check if volume is part of the Storage group
	currentSGIDs := vol.StorageGroupIDList
	volumeInStorageGroup := false
	for _, storageGroupID := range currentSGIDs {
		if storageGroupID == tgtFCStorageGroupID {
			volumeInStorageGroup = true
			break
		} else if storageGroupID == tgtISCSIStorageGroupID {
			volumeInStorageGroup = true
			isISCSI = true
			break
		}
	}

	if !volumeInStorageGroup {
		log.Debug("volume already unpublished")
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}
	var tgtStorageGroupID, tgtMaskingViewID string
	if !isISCSI {
		tgtStorageGroupID = tgtFCStorageGroupID
		tgtMaskingViewID = tgtFCMaskingViewID
	} else {
		tgtStorageGroupID = tgtISCSIStorageGroupID
		tgtMaskingViewID = tgtISCSIMaskingViewID
	}
	lockNum := RequestLock(tgtStorageGroupID, reqID)
	defer ReleaseLock(tgtStorageGroupID, reqID, lockNum)

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
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	if err := s.requireProbe(ctx); err != nil {
		log.Error("Failed to probe with error: " + err.Error())
		return nil, err
	}

	volID := req.GetVolumeId()
	// parse the volume and get the array serial and volume ID
	volName, symID, devID, err := s.parseCsiID(volID)
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
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
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
		log.Infof("Supported capabilities - Error(%s)\n", reason)
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

	// Get storage pool capacities
	srpCap, err := s.getStoragePoolCapacities(symmetrixID, storagePoolID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not retrieve StoragePool %s. Error(%s)", storagePoolID, err.Error())
	}

	totalSrpCapInGB := srpCap.UsableTotInTB * 1024
	usedSrpCapInGB := srpCap.UsableUsedInTB * 1024
	remainingCapInGB := totalSrpCapInGB - usedSrpCapInGB
	remainingCapInBytes := remainingCapInGB * 1024 * 1024 * 1024

	return &csi.GetCapacityResponse{
		AvailableCapacity: int64(remainingCapInBytes),
	}, nil
}

// Return the storage pool capacities of types.SrpCap
func (s *service) getStoragePoolCapacities(symmetrixID, storagePoolID string) (*types.SrpCap, error) {
	// Get storage pool info
	srp, err := s.adminClient.GetStoragePool(symmetrixID, storagePoolID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not retrieve StoragePool %s. Error(%s)", storagePoolID, err.Error())
	}
	log.Infof("StoragePoolCapacities: %#v", srp.SrpCap)
	return srp.SrpCap, nil
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
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_CREATE_DELETE_SNAPSHOT,
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
	controllerPendingState.maxPending = 6
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

// SelectPortGroup - Selects a Port Group randomly from the list of supplied port groups
func (s *service) SelectPortGroup() (string, error) {
	if len(s.opts.PortGroups) == 0 {
		return "", fmt.Errorf("No port groups have been supplied")
	}

	// select a random port group
	n := rand.Int() % len(s.opts.PortGroups)
	pg := s.opts.PortGroups[n]
	return pg, nil
}

// SelectOrCreatePortGroup - Selects or Creates a PG given a symId and host
// and the host type
func (s *service) SelectOrCreatePortGroup(symID string, host *types.Host) (string, error) {
	if host == nil {
		return "", fmt.Errorf("SelectOrCreatePortGroup: host can't be nil")
	}
	if host.HostType == "Fibre" {
		return s.SelectOrCreateFCPGForHost(symID, host)
	}
	return s.SelectPortGroup()
}

// SelectOrCreateFCPGForHost - Selects or creates a Fibre Channel PG given a symid and host
func (s *service) SelectOrCreateFCPGForHost(symID string, host *types.Host) (string, error) {
	if host == nil {
		return "", fmt.Errorf("SelectOrCreateFCPGForHost: host can't be nil")
	}
	validPortGroupID := ""
	hostID := host.HostID
	var portListFromHost []string
	var isValidHost bool
	if host.HostType == "Fibre" {
		for _, initiator := range host.Initiators {
			initList, err := s.adminClient.GetInitiatorList(symID, initiator, false, false)
			if err != nil {
				log.Errorf("Failed to get details for initiator - %s", initiator)
				continue
			} else {
				for _, initiatorID := range initList.InitiatorIDs {
					_, dirPort, _, err := splitFibreChannelInitiatorID(initiatorID)
					if err != nil {
						continue
					}
					portListFromHost = appendIfMissing(portListFromHost, dirPort)
					isValidHost = true
				}
			}
		}
	}
	if !isValidHost {
		return "", fmt.Errorf("Failed to find a valid initiator for hostID %s from %s", hostID, symID)
	}
	fcPortGroupList, err := s.adminClient.GetPortGroupList(symID, "fibre")
	if err != nil {
		return "", fmt.Errorf("Failed to fetch Fibre channel port groups for array: %s", symID)
	}
	filteredPGList := make([]string, 0)
	for _, portGroupID := range fcPortGroupList.PortGroupIDs {
		pgPrefix := "csi-" + s.opts.ClusterPrefix
		if strings.Contains(portGroupID, pgPrefix) {
			filteredPGList = append(filteredPGList, portGroupID)
		}
	}
	for _, portGroupID := range filteredPGList {
		portGroup, err := s.adminClient.GetPortGroupByID(symID, portGroupID)
		if err != nil {
			log.Error("Failed to fetch port group details")
			continue
		} else {
			var portList []string
			if portGroup.PortGroupType == "Fibre" {
				for _, portKey := range portGroup.SymmetrixPortKey {
					portList = append(portList, portKey.PortID)
				}
				if stringSlicesEqual(portList, portListFromHost) {
					validPortGroupID = portGroupID
					log.Debug(fmt.Sprintf("Found valid port group %s on the array %s",
						portGroupID, symID))
					break
				}
			}
		}
	}
	if validPortGroupID == "" {
		log.Warning("No port group found on the array. Attempting to create one")
		// Create a PG
		dirNames := ""
		portKeys := make([]types.PortKey, 0)
		for _, dirPortKey := range portListFromHost {
			dirPortDetails := strings.Split(dirPortKey, ":")
			portKey := types.PortKey{
				DirectorID: dirPortDetails[0],
				PortID:     dirPortDetails[1],
			}
			portKeys = append(portKeys, portKey)
			dirNames += dirPortDetails[0] + "-" + dirPortDetails[1] + "-"
		}
		constComponentLength := len(CSIPrefix) + len(s.opts.ClusterPrefix) + len(PGSuffix) + 2 //for the "-"
		MaxDirNameLength := MaxPortGroupIdentifierLength - constComponentLength
		if len(dirNames) > MaxDirNameLength {
			dirNames = truncateString(dirNames, MaxDirNameLength)
		}
		portGroupName := CSIPrefix + "-" + s.opts.ClusterPrefix + "-" + dirNames + PGSuffix
		_, err = s.adminClient.CreatePortGroup(symID, portGroupName, portKeys)
		if err != nil {
			return "", fmt.Errorf("Failed to create PortGroup. Error - %s", err.Error())
		}
		validPortGroupID = portGroupName
	}
	return validPortGroupID, nil
}

// CreateSnapshot creates a snapshot.
// If Parameters["VolumeIDList"] has a comma separated list of additional volumes, they will be
// snapshotted in a consistency group with the primary volume in CreateSnapshotRequest.SourceVolumeId.
func (s *service) CreateSnapshot(
	ctx context.Context,
	req *csi.CreateSnapshotRequest) (
	*csi.CreateSnapshotResponse, error) {
	// Requires probe
	if err := s.requireProbe(ctx); err != nil {
		return nil, err
	}

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	// Get the snapshot name
	snapName := req.GetName()
	if snapName == "" {
		log.Error("Snapshot name cannot be empty")
		return nil, status.Error(codes.InvalidArgument,
			"Snapshot name cannot be empty")
	}

	// Get the snapshot prefix from environment
	maxLength := MaxSnapIdentifierLength - len(s.getClusterPrefix()) - len(CsiVolumePrefix) - 5
	//First get the short snap name
	shortSnapName := truncateString(snapName, maxLength)
	//Form the snapshot identifier using short snap name
	snapID := fmt.Sprintf("%s%s-%s", CsiVolumePrefix, s.getClusterPrefix(), shortSnapName)

	// Validate snapshot volume
	volID := req.GetSourceVolumeId()
	if volID == "" {
		return nil, status.Errorf(codes.InvalidArgument,
			"Source volume ID is required for creating snapshot")
	}
	_, symID, devID, err := s.parseCsiID(volID)
	if err != nil {
		// We couldn't comprehend the identifier.
		log.Error("Could not parse CSI VolumeId: " + volID)
		return nil, status.Error(codes.InvalidArgument,
			"Could not parse CSI VolumeId")
	}

	// check snapshot is licensed
	if err := s.IsSnapshotLicensed(symID); err != nil {
		log.Error("Error - " + err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	vol, err := s.adminClient.GetVolumeByID(symID, devID)
	if err != nil {
		log.Error("Could not find device: " + devID)
		return nil, status.Error(codes.InvalidArgument,
			"Could not find source volume on the array")
	}

	// Is it an idempotent request?
	snapInfo, err := s.adminClient.GetSnapshotInfo(symID, devID, snapID)
	if err == nil && snapInfo.VolumeSnapshotSource != nil {
		snapID = fmt.Sprintf("%s-%s-%s", snapID, symID, devID)
		snapshot := &csi.Snapshot{
			SnapshotId:     snapID,
			SourceVolumeId: volID, ReadyToUse: true,
			CreationTime: ptypes.TimestampNow()}
		resp := &csi.CreateSnapshotResponse{Snapshot: snapshot}
		return resp, nil
	}

	// log all parameters used in CreateSnapshot call
	fields := map[string]interface{}{
		"CSIRequestID": reqID,
		"SymmetrixID":  symID,
		"SnapshotID":   snapID,
		"DeviceID":     devID,
	}
	log.WithFields(fields).Info("Executing CreateSnapshot with following fields")

	// Create snapshot
	lockNumber := RequestLock(SnapLock, reqID)
	snap, err := s.CreateSnapshotFromVolume(symID, vol, snapID, 0, reqID)
	ReleaseLock(SnapLock, reqID, lockNumber)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Failed to create snapshot: %s", err.Error())
	}

	snapID = fmt.Sprintf("%s-%s-%s", snap.SnapshotName, symID, devID)
	// populate response structure
	snapshot := &csi.Snapshot{
		SnapshotId:     snapID,
		SourceVolumeId: volID, ReadyToUse: true,
		CreationTime: ptypes.TimestampNow()}
	resp := &csi.CreateSnapshotResponse{Snapshot: snapshot}

	log.Debugf("Created snapshot: SnapshotId %s SourceVolumeId %s CreationTime %s",
		snapshot.SnapshotId, snapshot.SourceVolumeId, ptypes.TimestampString(snapshot.CreationTime))
	return resp, nil
}

// DeleteSnapshot deletes a snapshot
func (s *service) DeleteSnapshot(
	ctx context.Context,
	req *csi.DeleteSnapshotRequest) (
	*csi.DeleteSnapshotResponse, error) {
	// Requires probe
	if err := s.requireProbe(ctx); err != nil {
		return nil, err
	}

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	// Validate snapshot volume
	id := req.GetSnapshotId()
	if id == "" {
		return nil, status.Errorf(codes.InvalidArgument, "Snapshot ID to be deleted is required")
	}
	snapID, symID, devID, err := s.parseCsiID(id)
	if err != nil {
		// We couldn't comprehend the identifier.
		log.Error("Could not parse CSI snapshot identifier: " + id)
		return nil, status.Error(codes.InvalidArgument, "Snapshot name is not in supported format")
	}

	// check snapshot is licensed
	if err := s.IsSnapshotLicensed(symID); err != nil {
		log.Error("Error - " + err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Idempotency check
	snapInfo, err := s.adminClient.GetSnapshotInfo(symID, devID, snapID)
	if err != nil {
		//Unisphere returns "does not exist"
		//when the snapshot is not found for Elm-SR ucode(9.0)
		if strings.Contains(err.Error(), "does not exist") {
			return &csi.DeleteSnapshotResponse{}, nil
		}
		// Snapshot to be deleted couldn't be found in the system.
		log.Errorf("GetSnapshotInfo() failed with error (%s) for snapshot (%s)", err.Error(), snapID)
		return nil, status.Errorf(codes.Internal,
			"GetSnapshotInfo() failed with error (%s) for snapshot (%s)", err.Error(), snapID)
	}
	//Unisphere return success and an empty object
	//when the snapshot is not found for Foxtail ucode(9.1)
	if snapInfo.VolumeSnapshotSource == nil {
		return &csi.DeleteSnapshotResponse{}, nil
	}

	// log all parameters used in DeleteSnapshot call
	fields := map[string]interface{}{
		"SymmetrixID":    symID,
		"SnapshotName":   snapID,
		"SourceDeviceID": devID,
		"CSIRequestID":   reqID,
	}
	log.WithFields(fields).Info("Executing DeleteSnapshot with following fields")

	lockHandle := fmt.Sprintf("%s%s", devID, symID)
	lockNum := RequestLock(lockHandle, reqID)
	defer ReleaseLock(lockHandle, reqID, lockNum)
	// mark the snapshot for deletion by changing the snapshot name to mark for deletion
	newSnapID, err := s.MarkSnapshotForDeletion(symID, snapID, devID)
	if err != nil {
		log.Errorf("Failed to rename snapshot (%s) error (%s)", snapID, err.Error())
		return nil, status.Errorf(codes.Internal, "Failed to rename snapshot - Error(%s)", err.Error())
	}
	err = s.UnlinkAndTerminate(symID, devID, newSnapID)
	if err != nil {
		//Push the undeleted snapshot for cleanup
		var cleanReq snapCleanupRequest
		cleanReq.snapshotID = newSnapID
		cleanReq.symmetrixID = symID
		cleanReq.volumeID = devID
		cleanReq.requestID = reqID
		snapCleaner.requestCleanup(&cleanReq)
		log.Errorf("Returning success though it failed to terminate snapshot (%s) with error (%s)",
			snapID, err.Error())
		return &csi.DeleteSnapshotResponse{}, nil
	}
	log.Debugf("Deleted snapshot for SnapshotId (%s) and SourceVolumeId (%s)", snapID, devID)
	return &csi.DeleteSnapshotResponse{}, nil
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

func (s *service) ControllerExpandVolume(ctx context.Context, req *csi.ControllerExpandVolumeRequest) (*csi.ControllerExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

//MarkVolumeForDeletion renames the volume with deletion prefix and sends a
//request to deletion_worker queue
func (s *service) MarkVolumeForDeletion(symID string, vol *types.Volume) error {
	if vol == nil {
		return fmt.Errorf("MarkVolumeForDeletion: Null volume object")
	}
	oldVolName := vol.VolumeIdentifier
	// Rename the volume to mark it for deletion
	newVolName := fmt.Sprintf("%s%s", DeletionPrefix, oldVolName)
	if len(newVolName) > MaxVolIdentifierLength {
		newVolName = newVolName[:MaxVolIdentifierLength]
	}
	vol, err := s.adminClient.RenameVolume(symID, vol.VolumeID, newVolName)
	if err != nil || vol == nil {
		return fmt.Errorf("MarkVolumeForDeletion: Failed to rename volume %s", oldVolName)
	}

	// Fetch the uCode version details
	isPostElmSR, err := s.isPostElmSR(symID)
	if err != nil {
		return fmt.Errorf("Failed to get symmetrix uCode version details")
	}

	// Create a delete worker request for this volume
	var delReq deletionWorkerRequest
	delReq.volumeID = vol.VolumeID
	delReq.volumeName = vol.VolumeIdentifier
	delReq.symmetrixID = symID
	delReq.volumeSizeInCylinders = int64(vol.CapacityCYL)
	delReq.skipDeallocate = isPostElmSR
	delWorker.requestDeletion(&delReq)
	log.Infof("Request dispatched to delete worker thread for %s/%s", vol.VolumeID, symID)
	return nil
}
