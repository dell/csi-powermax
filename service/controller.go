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
	"errors"
	"fmt"
	"math/rand"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	pmax "github.com/dell/gopowermax"

	"github.com/dell/csi-powermax/pkg/symmetrix"

	csiext "github.com/dell/dell-csi-extensions/replication"

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
	PortIdentifierKeyCount          = "PORT_IDENTIFIER_KEYS"
	MaxPortIdentifierLength         = 128
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
	StorageGroup                    = "StorageGroup"
	Async                           = "ASYNC"
	Sync                            = "SYNC"
	Metro                           = "METRO"
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
	// These params will be in replication enabled storage class
	RepEnabledParam              = "isReplicationEnabled"
	LocalRDFGroupParam           = "RdfGroup"
	RemoteRDFGroupParam          = "RemoteRDFGroup"
	RemoteSymIDParam             = "RemoteSYMID"
	ReplicationModeParam         = "RdfMode"
	CSIPVCNamespace              = "csi.storage.k8s.io/pvc/namespace"
	CSIPersistentVolumeName      = "csi.storage.k8s.io/pv/name"
	CSIPersistentVolumeClaimName = "csi.storage.k8s.io/pvc/name"
	// These map to the above fields in the form of HTTP header names.
	HeaderPersistentVolumeName           = "x-csi-pv-name"
	HeaderPersistentVolumeClaimName      = "x-csi-pv-claimname"
	HeaderPersistentVolumeClaimNamespace = "x-csi-pv-namespace"
	RemoteServiceLevelParam              = "RemoteServiceLevel"
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
	snapshotPendingState   pendingState

	// Retry delay for retrying GetMaskingViewConnections
	getMVConnectionsDelay = 30 * time.Second
)

func (s *service) GetPortIdentifier(symID string, dirPortKey string, pmaxClient pmax.Pmax) (string, error) {
	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()
	portIdentifier := ""
	cache := getPmaxCache(symID)
	cacheExpired := false
	if cache == nil {
		return "", fmt.Errorf("Internal error - cache pointer is empty")
	}
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
	port, err := pmaxClient.GetPort(symID, dirID, portNumber)
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

	params := req.GetParameters()
	params = mergeStringMaps(params, req.GetSecrets())
	symmetrixID := params[SymmetrixIDParam]
	if symmetrixID == "" {
		log.Error("A SYMID parameter is required")
		return nil, status.Errorf(codes.InvalidArgument, "A SYMID parameter is required")
	}
	pmaxClient, err := symmetrix.GetPowerMaxClient(symmetrixID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Get the parameters
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	thick := params[ThickVolumesParam]

	applicationPrefix := ""
	if params[ApplicationPrefixParam] != "" {
		applicationPrefix = params[ApplicationPrefixParam]
	}

	// Storage (resource) Pool. Validate it against exist Pools
	storagePoolID := params[StoragePoolParam]
	err = s.validateStoragePoolID(symmetrixID, storagePoolID, pmaxClient)
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

	// Remote Replication based params
	var replicationEnabled string
	var remoteSymID string
	var localRDFGrpNo string
	var repMode string
	var namespace string
	repEnabledParam := s.opts.ReplicationPrefix + "/" + RepEnabledParam
	if params[repEnabledParam] == "true" {
		replicationEnabled = params[repEnabledParam]
		// remote symmetrix ID and rdf group name are mandatory params when replication is enabled
		remoteSymID = params[RemoteSymIDParam]
		localRDFGrpNo = params[LocalRDFGroupParam]
		repMode = params[ReplicationModeParam]
		namespace = params[CSIPVCNamespace]
		if repMode != Async {
			log.Errorf("Unsupported Replication Mode: (%s)" + repMode)
			return nil, status.Errorf(codes.InvalidArgument, "Unsupported Replication Mode: (%s)", repMode)
		}
	}

	accessibility := req.GetAccessibilityRequirements()

	// Get the required capacity
	cr := req.GetCapacityRange()
	requiredCylinders, err := s.validateVolSize(cr, symmetrixID, storagePoolID, pmaxClient)
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
		if replicationEnabled == "true" {
			log.Error("VolumeContentSource:Clone Volume is not supported with replication")
			return nil, status.Errorf(codes.InvalidArgument, "VolumeContentSource:Clone Volume is not supported with replication")
		}
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
		if err := s.IsSnapshotLicensed(symID, pmaxClient); err != nil {
			log.Error("Error - " + err.Error())
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if SrcDevID != "" && symID != "" {
		if symID != symmetrixID {
			log.Error("The volume content source is in different PowerMax array")
			return nil, status.Errorf(codes.Internal, "The volume content source is in different PowerMax array")
		}
		srcVol, err = pmaxClient.GetVolumeByID(symmetrixID, SrcDevID)
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
	// localProtectionGroupID refers to name of Storage Group which has protected local volumes
	var localProtectionGroupID string
	if replicationEnabled == "true" {
		localProtectionGroupID = s.buildProtectionGroupID(namespace, localRDFGrpNo, repMode)
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
		"RemoteSymmID":                       remoteSymID,
		"LocalRDFGroup":                      localRDFGrpNo,
		"SRDFMode":                           repMode,
		"PVCNamespace":                       namespace,
		"LocalProtectionGroupID":             localProtectionGroupID,
		HeaderPersistentVolumeName:           params[CSIPersistentVolumeName],
		HeaderPersistentVolumeClaimName:      params[CSIPersistentVolumeClaimName],
		HeaderPersistentVolumeClaimNamespace: params[CSIPVCNamespace],
	}
	log.WithFields(fields).Info("Executing CreateVolume with following fields")

	// isSGUnprotected is set to true only if SG has a replica, eg if the SG is new
	isSGUnprotected := false
	if replicationEnabled == "true" {
		sg, err := s.getOrCreateProtectedStorageGroup(symmetrixID, localProtectionGroupID, namespace, localRDFGrpNo, repMode, reqID, pmaxClient)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Error in getOrCreateProtectedStorageGroup: (%s)", err.Error())
		}
		if sg != nil && sg.Rdf == true {
			// Check the direction of SG
			// Creation of replicated volume is allowed in an SG of type R1
			err := s.VerifyProtectedGroupDirection(symmetrixID, localProtectionGroupID, localRDFGrpNo, pmaxClient)
			if err != nil {
				return nil, err
			}
		} else {
			isSGUnprotected = true
		}
	}
	// Check existence of the Storage Group and create if necessary.
	sg, err := pmaxClient.GetStorageGroup(symmetrixID, storageGroupName)
	if err != nil || sg == nil {
		log.Debug(fmt.Sprintf("Unable to find storage group: %s", storageGroupName))
		_, err := pmaxClient.CreateStorageGroup(symmetrixID, storageGroupName, storagePoolID,
			serviceLevel, thick == "true")
		if err != nil {
			log.Error("Error creating storage group: " + err.Error())
			return nil, status.Errorf(codes.Internal, "Error creating storage group: %s", err.Error())
		}
	}
	var vol *types.Volume
	// Idempotency test. We will read the volume and check for:
	// 1. Existence of a volume with matching volume name
	// 2. Matching cylinderSize
	// 3. Is a member of the storage group
	log.Debug("Calling GetVolumeIDList for idempotency test")
	// For now an exact match
	volumeIDList, err := pmaxClient.GetVolumeIDList(symmetrixID, volumeIdentifier, false)
	if err != nil {
		log.Error("Error looking up volume for idempotence check: " + err.Error())
		return nil, status.Errorf(codes.Internal, "Error looking up volume for idempotence check: %s", err.Error())
	}
	alreadyExists := false
	// isLocalVolumePresent restrict CreateVolumeInProtectedSG call if the volume is present in local SG but not in remote SG
	isLocalVolumePresent := false
	// Look up the volume(s), if any, returned for the idempotency check to see if there are any matches
	// We ignore any volume not in the desired storage group (even though they have the same name).
	for _, volumeID := range volumeIDList {
		// Fetch the volume
		log.WithFields(fields).Info("Calling GetVolumeByID for idempotence check")
		vol, err = pmaxClient.GetVolumeByID(symmetrixID, volumeID)
		if err != nil {
			log.Error("Error fetching volume for idempotence check: " + err.Error())
			return nil, status.Errorf(codes.Internal, "Error fetching volume for idempotence check: %s", err.Error())
		}
		if len(vol.StorageGroupIDList) < 1 {
			log.Error("Idempotence check: StorageGroupIDList is empty for (%s): " + volumeID)
			return nil, status.Errorf(codes.Internal, "Idempotence check: StorageGroupIDList is empty for (%s): "+volumeID)
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
			if replicationEnabled == "true" {
				remoteVolumeID, _ := s.GetRemoteVolumeID(symmetrixID, localRDFGrpNo, vol.VolumeID, pmaxClient)
				if remoteVolumeID == "" {
					// Missing corresponding Remote Volume Name for existing local volume
					// The SG is unprotected as Local volume and Local SG exists but missing corresponding SRDF info
					// If the SG was protected, there must exist a corresponding remote replica volume
					log.Debugf("Local Volume already exist, skipping creation (%s)", vol.VolumeID)
					isLocalVolumePresent = true
					continue
				}
			}
			log.WithFields(fields).Info("Idempotent volume detected, returning success")
			vol.VolumeID = fmt.Sprintf("%s-%s-%s", volumeIdentifier, symmetrixID, vol.VolumeID)
			volResp := s.getCSIVolume(vol)
			//Set the volume context
			attributes := map[string]string{
				ServiceLevelParam: serviceLevel,
				StoragePoolParam:  storagePoolID,
				path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam): symmetrixID,
				CapacityGB:    fmt.Sprintf("%.2f", vol.CapacityGB),
				ContentSource: volContent,
				StorageGroup:  storageGroupName,
				//Format the time output
				"CreationTime": time.Now().Format("20060102150405"),
			}
			if replicationEnabled == "true" {
				addReplicationParamsToVolumeAttributes(attributes, s.opts.ReplicationContextPrefix, remoteSymID, repMode)
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
	if alreadyExists {
		log.Error("A volume with the same name " + volumeName + "exists but has a different size than requested. Use a different name.")
		return nil, status.Errorf(codes.AlreadyExists, "A volume with the same name %s exists but has a different size than requested. Use a different name.", volumeName)
	}

	//CSI specific metada for authorization
	var headerMetadata = addMetaData(params)

	// Let's create the volume
	if !isLocalVolumePresent {
		vol, err = pmaxClient.CreateVolumeInStorageGroupS(symmetrixID, storageGroupName, volumeIdentifier, requiredCylinders, headerMetadata)
		if err != nil {
			log.Error(fmt.Sprintf("Could not create volume: %s: %s", volumeName, err.Error()))
			return nil, status.Errorf(codes.Internal, "Could not create volume: %s: %s", volumeName, err.Error())
		}
	}

	if replicationEnabled == "true" {
		log.Debugf("RDF: Found Rdf enabled")
		// remote storage group name is kept same as local storage group name
		// Check if volume is already added in SG, else add it
		protectedSGID := s.GetProtectedStorageGroupID(vol.StorageGroupIDList, localRDFGrpNo+"-"+repMode)
		if protectedSGID == "" {
			// Volume is not present in Protected Storage Group, Add
			err = pmaxClient.AddVolumesToProtectedStorageGroup(symmetrixID, localProtectionGroupID, remoteSymID, localProtectionGroupID, true, vol.VolumeID)
			if err != nil {
				log.Error(fmt.Sprintf("Could not add volume in protected SG: %s: %s", volumeName, err.Error()))
				return nil, status.Errorf(codes.Internal, "Could not add volume in protected SG: %s: %s", volumeName, err.Error())
			}
		}
		if isSGUnprotected {
			// If the required SG is still unprotected, protect the local SG with RDF info
			// If valid RDF group is supplied this will create a remote SG, a RDF pair and add the vol in respective SG created
			// Remote storage group name is kept same as local storage group name
			err := s.ProtectStorageGroup(symmetrixID, remoteSymID, localProtectionGroupID, localProtectionGroupID, "", localRDFGrpNo, repMode, vol.VolumeID, reqID, pmaxClient)
			if err != nil {
				return nil, err
			}
		}
	}
	// If volume content source is specified, initiate no_copy to newly created volume
	if contentSource != nil {
		if srcVolID != "" {
			//Build the temporary snapshot identifier
			snapID := fmt.Sprintf("%s%s-%d", TempSnap, s.getClusterPrefix(), time.Now().Nanosecond())
			err = s.LinkVolumeToVolume(symID, srcVol, vol.VolumeID, snapID, reqID, pmaxClient)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "Failed to create volume from volume (%s)", err.Error())
			}
		} else if srcSnapID != "" {
			//Unlink all previous targets from this snapshot if the link is in defined state
			err = s.UnlinkTargets(symID, SrcDevID, pmaxClient)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "Failed unlink existing target from snapshot (%s)", err.Error())
			}
			err = s.LinkVolumeToSnapshot(symID, srcVol.VolumeID, vol.VolumeID, snapID, reqID, pmaxClient)
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
		path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam): symmetrixID,
		CapacityGB:    fmt.Sprintf("%.2f", vol.CapacityGB),
		ContentSource: volContent,
		StorageGroup:  storageGroupName,
		//Format the time output
		"CreationTime": time.Now().Format("20060102150405"),
	}
	if replicationEnabled == "true" {
		addReplicationParamsToVolumeAttributes(attributes, s.opts.ReplicationContextPrefix, remoteSymID, repMode)
	}
	volResp.VolumeContext = attributes
	if accessibility != nil {
		volResp.AccessibleTopology = accessibility.Preferred
	}
	csiResp := &csi.CreateVolumeResponse{
		Volume: volResp,
	}
	log.WithFields(fields).Infof("Created volume with ID: %s", volResp.VolumeId)
	return csiResp, nil
}

func addReplicationParamsToVolumeAttributes(attributes map[string]string, prefix, remoteSymID, repMode string) {
	attributes[path.Join(prefix, RemoteSymIDParam)] = remoteSymID
	attributes[path.Join(prefix, ReplicationModeParam)] = repMode
}

func (s *service) getOrCreateProtectedStorageGroup(symID, localProtectionGroupID, namespace, localRDFGrpNo, repMode, reqID string, pmaxClient pmax.Pmax) (*types.RDFStorageGroup, error) {
	var lockHandle string
	if repMode == Async {
		lockHandle = fmt.Sprintf("%s%s", localRDFGrpNo, symID)
	} else {
		//Mode is SYNC
		lockHandle = fmt.Sprintf("%s%s", localProtectionGroupID, symID)
	}
	lockNum := RequestLock(lockHandle, reqID)
	defer ReleaseLock(lockHandle, reqID, lockNum)
	sg, err := pmaxClient.GetProtectedStorageGroup(symID, localProtectionGroupID)
	if err != nil || sg == nil {
		// Verify the creation of new protected storage group is valid
		err = s.verifyProtectionGroupID(symID, localProtectionGroupID, namespace, localRDFGrpNo, repMode, pmaxClient)
		if err != nil {
			log.Errorf("VerifyProtectionGroupID failed:(%s)", err.Error())
			return nil, status.Errorf(codes.Internal, "VerifyProtectionGroupID failed:(%s)", err.Error())
		}
		// this SG is valid, new and will need protection if working in replication mode
		// Create protected SG
		_, err := pmaxClient.CreateStorageGroup(symID, localProtectionGroupID, "None", "", false)
		if err != nil {
			log.Errorf("Error creating protected storage group (%s): (%s)", localProtectionGroupID, err.Error())
			return nil, status.Errorf(codes.Internal, "Error creating protected storage group (%s): (%s)", localProtectionGroupID, err.Error())
		}
	}
	return sg, nil
}

func (s *service) buildProtectionGroupID(namespace, localRdfGrpNo, repMode string) string {
	protectionGrpID := CsiNoSrpSGPrefix + namespace + "-" + localRdfGrpNo + "-" + repMode
	return protectionGrpID
}

// verifyProtectionGroupID verify's the ProtectionGroupID's uniqueness w.r.t the srdf mode
// For sync mode, one srdf group can have rdf pairing from many namespaces
// For async mode, one srdf group can only have rdf pairing from one namespace
// All rdf volume pairs of a namespace should be of one rdf mode
// In async rdf mode there should be One to One correspondence between namespace and srdf group
func (s *service) verifyProtectionGroupID(symID, storageGroupName, namespace, localRdfGrpNo, repMode string, pmaxClient pmax.Pmax) error {
	sgList, err := pmaxClient.GetStorageGroupIDList(symID)
	if err != nil {
		return err
	}
	for _, value := range sgList.StorageGroupIDs {
		// Is it trying to create a new SG for with same namespace in multiple rdf groups?
		if strings.Contains(value, storageGroupName+"-"+namespace) {
			return fmt.Errorf("RDF pairs for Namespace (%s) are already in protection group (%s)", namespace, value)
		}
		// Is it trying to create more than one SG in async mode for one rdf group
		if repMode == Async &&
			strings.Contains(value, localRdfGrpNo+"-"+Async) || strings.Contains(value, localRdfGrpNo+"-"+Sync) {
			return fmt.Errorf("RDF group (%s) is already a part of ReplicationGroup (%s) in Sync/Async mode", localRdfGrpNo, value)
		}
		// Is it trying to create a SG with a rdf group which is already used in Async mode
		if repMode == Sync && strings.Contains(value, localRdfGrpNo+"-"+Async) {
			return fmt.Errorf("RDF group (%s) is already part of another Async mode ReplicationGroup (%s)", localRdfGrpNo, value)
		}
	}
	return nil
}

// validateVolSize uses the CapacityRange range params to determine what size
// volume to create, and returns an error if volume size would be greater than
// the given limit. Returned size is in number of cylinders
func (s *service) validateVolSize(cr *csi.CapacityRange, symmetrixID, storagePoolID string, pmaxClient pmax.Pmax) (int, error) {

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
		srpCap, err := s.getStoragePoolCapacities(symmetrixID, storagePoolID, pmaxClient)
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
func (s *service) validateStoragePoolID(symmetrixID string, storagePoolID string, pmaxClient pmax.Pmax) error {
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
	list, err := pmaxClient.GetStoragePoolList(symmetrixID)
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
func (s *service) isPostElmSR(symmetrixID string, pmaxClient pmax.Pmax) (bool, error) {
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
		symmetrix, err := pmaxClient.GetSymmetrixByID(symmetrixID)
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
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
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

	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		log.Error("Failed to probe with erro: " + err.Error())
		return nil, err
	}

	// log all parameters used in DeleteVolume call
	fields := map[string]interface{}{
		"SymmetrixID":  symID,
		"VolumeName":   volName,
		"DeviceID":     devID,
		"CSIRequestID": reqID,
	}
	log.WithFields(fields).Info("Executing DeleteVolume with following fields")

	vol, err := pmaxClient.GetVolumeByID(symID, devID)
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
		sg, err := pmaxClient.GetStorageGroup(symID, sgid)
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
	isSnapSrc, err := s.IsSnapshotSource(symID, devID, pmaxClient)
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
		vol, err = pmaxClient.RenameVolume(symID, devID, newVolName)
		if err != nil || vol == nil {
			log.Error(fmt.Sprintf("DeleteVolume: Could not rename volume %s", id))
			return nil, status.Errorf(codes.Internal, "Failed to rename volume")
		}
		log.Infof("Soft deletion of source volume (%s) is successful", volName)
		return &csi.DeleteVolumeResponse{}, nil
	}
	err = s.MarkVolumeForDeletion(symID, vol, pmaxClient)
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
	_, symID, _, err := s.parseCsiID(volID)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", volID)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", volID)
	}
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	var volumeID volumeIDType = volumeIDType(volID)
	if err := volumeID.checkAndUpdatePendingState(&controllerPendingState); err != nil {
		return nil, err
	}
	defer volumeID.clearPending(&controllerPendingState)

	if err := s.requireProbe(ctx, pmaxClient); err != nil {
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
	symID, devID, vol, err := s.GetVolumeByID(volID, pmaxClient)
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
		isISCSI, err = s.IsNodeISCSI(symID, nodeID, pmaxClient)
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

	waitChan, lockChan, err := s.sgSvc.requestAddVolumeToSGMV(tgtStorageGroupID, tgtMaskingViewID, hostID, reqID, symID, symID, devID, am)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	log.Infof("reqID %s devID %s waitChan %v lockChan %v", reqID, devID, waitChan, lockChan)

	var connections []*types.MaskingViewConnection
	for done := false; !done; {
		select {
		case response := <-waitChan:
			err = response.err
			connections = response.connections
			if err != nil {
				log.Infof("Received error %s on waitChan %v reqID %s", err, waitChan, reqID)
			}
			close(waitChan)
			done = true
		case <-lockChan:
			// We own the lock, and should process the service
			s.sgSvc.runAddVolumesToSGMV(symID, tgtStorageGroupID)
		}
	}

	// Return error if that was the result
	if err != nil {
		return nil, err
	}

	publishContext := map[string]string{
		PublishContextDeviceWWN: vol.WWN,
	}

	return s.updatePublishContext(publishContext, symID, tgtMaskingViewID, devID, reqID, connections, pmaxClient)
}

// Adds the LUN_ADDRESS and SCSI target information to the PublishContext by looking at MaskingView connections.
// The connections may be optionally passed in (returned by the batching code for batched requests) or
// will be read (and retried) if necessary. GetMaskingViewConnections is an expensive call.
// The return arguments are suitable for directly passing back to the grpc called.
// This routine is careful to throw errors if it cannot come up with a valid context, because having ControllerPublish
// succeed but without valid context will only cause the NodeStage or NodePublish to fail.
func (s *service) updatePublishContext(publishContext map[string]string, symID, tgtMaskingViewID, deviceID, reqID string,
	connections []*types.MaskingViewConnection, pmaxClient pmax.Pmax) (*csi.ControllerPublishVolumeResponse, error) {

	// If we got connections already from runAddVolumesToSGMV, see if there are any for our deviceID.
	err := errors.New("no connections")
	if len(connections) > 0 {
		count := 0
		for _, conn := range connections {
			if deviceID == conn.VolumeID {
				count++
			}
		}
		log.Infof("PublishContext found %d incoming connections for %s %s %s", count, symID, tgtMaskingViewID, deviceID)
		// If at least two connections passed from the batching code, then we're go to go without refetching the connections.
		if count >= 2 {
			err = nil
		} else {
			time.Sleep(getMVConnectionsDelay)
		}
	}

	// Getting the connections may take some time, thus the retry loop. K8S will retry if this fails.
	for retry := 0; retry < 2 && err != nil; retry++ {
		if retry > 0 {
			time.Sleep(getMVConnectionsDelay)
			log.Infof("GetMaskingViewConnections retry %d %s %s %s err: %s", retry, symID, tgtMaskingViewID, deviceID, err)
		}
		lockNum := RequestLock(getMVLockKey(symID, tgtMaskingViewID), reqID)
		connections, err = pmaxClient.GetMaskingViewConnections(symID, tgtMaskingViewID, deviceID)
		ReleaseLock(getMVLockKey(symID, tgtMaskingViewID), reqID, lockNum)
	}
	if err != nil {
		log.Error("Could not get MV Connections: " + err.Error())
		return nil, status.Errorf(codes.Internal, "PublishContext: Could not get MV Connections: %s", tgtMaskingViewID)
	}

	// Process the connections
	lunid := ""
	dirPorts := make([]string, 0)
	for _, conn := range connections {
		if deviceID != conn.VolumeID {
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
	if lunid == "" {
		return nil, status.Error(codes.Internal, "PublishContext: No matching connections for deviceID")
	}

	portIdentifiers := ""

	for _, dirPortKey := range dirPorts {
		portIdentifier, err := s.GetPortIdentifier(symID, dirPortKey, pmaxClient)
		if err != nil {
			log.Errorf("PublishContext: Failed to fetch port details %s %s", symID, dirPortKey)
			continue
		}
		portIdentifiers += portIdentifier + ","
	}
	if portIdentifiers == "" {
		log.Errorf("PublishContext: Failed to fetch port details for %s %s which are part of masking view: %s", symID, deviceID, tgtMaskingViewID)
		return nil, status.Errorf(codes.Internal,
			"PublishContext: Failed to fetch port details for any director ports which are part of masking view: %s", tgtMaskingViewID)
	}
	// Each context key holds upto 128 characters of portIdentifiers. If one Identifier is more than
	// 128, it gets overlayed to next key.
	keyCount := int(len(portIdentifiers)/MaxPortIdentifierLength) + 1
	for i := 1; i <= keyCount; i++ {
		portIdentifierKey := fmt.Sprintf("%s_%d", PortIdentifiers, i)
		start := (i - 1) * MaxPortIdentifierLength
		end := i * MaxPortIdentifierLength
		if end > len(portIdentifiers) {
			end = len(portIdentifiers)
		}
		publishContext[portIdentifierKey] = portIdentifiers[start:end]
	}
	log.Debugf("Port identifiers in publish context: %s", portIdentifiers)
	publishContext[PortIdentifierKeyCount] = strconv.Itoa(keyCount)
	publishContext[PublishContextLUNAddress] = lunid
	return &csi.ControllerPublishVolumeResponse{PublishContext: publishContext}, nil
}

func getMVLockKey(symID, tgtMaskingViewID string) string {
	return symID + ":" + tgtMaskingViewID
}

// IsNodeISCSI - Takes a sym id, node id as input and based on the transport protocol setting
// and the existence of the host on array, it returns a bool to indicate if the Host
// on array is ISCSI or not
func (s *service) IsNodeISCSI(symID, nodeID string, pmaxClient pmax.Pmax) (bool, error) {
	fcHostID, _, fcMaskingViewID := s.GetFCHostSGAndMVIDFromNodeID(nodeID)
	iSCSIHostID, _, iSCSIMaskingViewID := s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	if s.opts.TransportProtocol == FcTransportProtocol || s.opts.TransportProtocol == "" {
		log.Debug("Preferred transport protocol is set to FC")
		_, fcmverr := pmaxClient.GetMaskingViewByID(symID, fcMaskingViewID)
		if fcmverr == nil {
			return false, nil
		}
		// Check if ISCSI MV exists
		_, iscsimverr := pmaxClient.GetMaskingViewByID(symID, iSCSIMaskingViewID)
		if iscsimverr == nil {
			return true, nil
		}
		// Check if FC Host exists
		fcHost, fcHostErr := pmaxClient.GetHostByID(symID, fcHostID)
		if fcHostErr == nil {
			if fcHost.HostType == "Fibre" {
				return false, nil
			}
		}
		// Check if ISCSI Host exists
		iscsiHost, iscsiHostErr := pmaxClient.GetHostByID(symID, iSCSIHostID)
		if iscsiHostErr == nil {
			if iscsiHost.HostType == "iSCSI" {
				return true, nil
			}
		}
	} else if s.opts.TransportProtocol == IscsiTransportProtocol {
		log.Debug("Preferred transport protocol is set to ISCSI")
		// Check if ISCSI MV exists
		_, iscsimverr := pmaxClient.GetMaskingViewByID(symID, iSCSIMaskingViewID)
		if iscsimverr == nil {
			return true, nil
		}
		// Check if FC MV exists
		_, fcmverr := pmaxClient.GetMaskingViewByID(symID, fcMaskingViewID)
		if fcmverr == nil {
			return false, nil
		}
		// Check if ISCSI Host exists
		iscsiHost, iscsiHostErr := pmaxClient.GetHostByID(symID, iSCSIHostID)
		if iscsiHostErr == nil {
			if iscsiHost.HostType == "iSCSI" {
				return true, nil
			}
		}
		// Check if FC Host exists
		fcHost, fcHostErr := pmaxClient.GetHostByID(symID, fcHostID)
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
func (s *service) GetVolumeByID(volID string, pmaxClient pmax.Pmax) (string, string, *types.Volume, error) {
	// parse the volume and get the array serial and volume ID
	volName, symID, devID, err := s.parseCsiID(volID)
	if err != nil {
		return "", "", nil, status.Errorf(codes.InvalidArgument,
			"volID: %s malformed. Error: %s", volID, err.Error())
	}

	vol, err := pmaxClient.GetVolumeByID(symID, devID)
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
func (s *service) GetMaskingViewAndSGDetails(symID string, sgIDs []string, pmaxClient pmax.Pmax) ([]string, []*types.StorageGroup, error) {
	maskingViewIDs := make([]string, 0)
	storageGroups := make([]*types.StorageGroup, 0)
	// Fetch each SG this device is part of
	for _, sgID := range sgIDs {
		sg, err := pmaxClient.GetStorageGroup(symID, sgID)
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

func (s *service) GetHostIDFromTemplate(nodeID string) string {
	if s.opts.NodeNameTemplate != "" {
		hostID, err := s.buildHostIDFromTemplate(nodeID)
		if err == nil {
			return hostID
		}
		log.Infof("%s. Using default naming for host ", err.Error())
	}
	return CsiHostPrefix + s.getClusterPrefix() + "-" + nodeID
}

func (s *service) buildHostIDFromTemplate(nodeID string) (
	hostID string, err error) {
	// get the Device ID and Array ID
	tmpltComponents := strings.Split(s.opts.NodeNameTemplate, "%")
	// Protect against mal-formed component
	numOfComponents := len(tmpltComponents)

	if numOfComponents < 3 {
		// Not well formed
		err = fmt.Errorf("The node name template %s is not formed correctly", s.opts.NodeNameTemplate)
		return "", err
	}

	nodePrefix := tmpltComponents[0]
	nodeSuffix := tmpltComponents[2]
	hostID = nodePrefix + nodeID + nodeSuffix

	// Check if the hostname has any invalid characters
	if !isValidHostID(hostID) {
		err = fmt.Errorf("Character in hostID (%s) is not acceptable", hostID)
		return "", err
	}

	return hostID, nil
}

// GetISCSIHostSGAndMVIDFromNodeID - Forms HostID, StorageGroupID, MaskingViewID
// using the NodeID and returns them
func (s *service) GetISCSIHostSGAndMVIDFromNodeID(nodeID string) (string, string, string) {
	hostID := s.GetHostIDFromTemplate(nodeID)
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
	_, symID, _, err := s.parseCsiID(volID)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", volID)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", volID)
	}
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
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

	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		log.Error("Failed to probe with error: " + err.Error())
		return nil, err
	}

	//Fetch the volume details from array
	symID, devID, vol, err := s.GetVolumeByID(volID, pmaxClient)
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

	waitChan, lockChan, err := s.sgSvc.requestRemoveVolumeFromSGMV(tgtStorageGroupID, tgtMaskingViewID, reqID, symID, symID, devID)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	log.Infof("reqID %s devID %s waitChan %v lockChan %v", reqID, devID, waitChan, lockChan)

	for done := false; !done; {
		select {
		case response := <-waitChan:
			err = response.err
			if err != nil {
				log.Infof("Received error %s on waitChan %v reqID %s", err, waitChan, reqID)
			}
			close(waitChan)
			done = true
		case <-lockChan:
			// We own the lock, and should process the service
			s.sgSvc.runRemoveVolumesFromSGMV(symID, tgtStorageGroupID)
		}
	}

	// Return error if that was the result
	if err != nil {
		return nil, err
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

	volID := req.GetVolumeId()
	// parse the volume and get the array serial and volume ID
	volName, symID, devID, err := s.parseCsiID(volID)
	if err != nil {
		log.Error(fmt.Sprintf("volID: %s malformed. Error: %s", volID, err.Error()))
		return nil, status.Errorf(codes.InvalidArgument,
			"volID: %s malformed. Error: %s", volID, err.Error())
	}
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		log.Error("Failed to probe with error: " + err.Error())
		return nil, err
	}

	// log all parameters used in ValidateVolumeCapabilities call
	fields := map[string]interface{}{
		"SymmetrixID":  symID,
		"VolumeId":     volID,
		"CSIRequestID": reqID,
	}
	log.WithFields(fields).Info("Executing ValidateVolumeCapabilities with following fields")

	vol, err := pmaxClient.GetVolumeByID(symID, devID)
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
	validContext, reasonContext := s.valVolumeContext(attributes, vol, symID, pmaxClient)
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

func (s *service) valVolumeContext(attributes map[string]string, vol *types.Volume, symID string, pmaxClient pmax.Pmax) (bool, string) {

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
		sg, err := pmaxClient.GetStorageGroup(symID, sgID)
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
	pmaxClient, err := symmetrix.GetPowerMaxClient(symmetrixID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if err := s.requireProbe(ctx, pmaxClient); err != nil {
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

	// Storage (resource) Pool. Validate it against exist Pools
	storagePoolID := params[StoragePoolParam]
	err = s.validateStoragePoolID(symmetrixID, storagePoolID, pmaxClient)
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
	srpCap, err := s.getStoragePoolCapacities(symmetrixID, storagePoolID, pmaxClient)
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
func (s *service) getStoragePoolCapacities(symmetrixID, storagePoolID string, pmaxClient pmax.Pmax) (*types.SrpCap, error) {
	// Get storage pool info
	srp, err := pmaxClient.GetStoragePool(symmetrixID, storagePoolID)
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
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_CLONE_VOLUME,
					},
				},
			},
			{
				Type: &csi.ControllerServiceCapability_Rpc{
					Rpc: &csi.ControllerServiceCapability_RPC{
						Type: csi.ControllerServiceCapability_RPC_EXPAND_VOLUME,
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
	if !s.opts.UseProxy && s.opts.Endpoint == "" {
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

	err := s.createPowerMaxClients()
	if err != nil {
		return err
	}
	return nil
}

func (s *service) requireProbe(ctx context.Context, pmaxClient pmax.Pmax) error {
	// If we're using the proxy, the throttling is in the proxy in front of U4V.
	// so we can handle a large number of pending requests.
	// Otherwise a small number since there's no protection for U4V.
	if s.opts.UseProxy || s.opts.IsReverseProxyEnabled {
		controllerPendingState.maxPending = 50
		snapshotPendingState.maxPending = 50
	} else {
		controllerPendingState.maxPending = s.opts.GrpcMaxThreads
		snapshotPendingState.maxPending = s.opts.GrpcMaxThreads
	}
	if pmaxClient == nil {
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
	n := rand.Int() % len(s.opts.PortGroups) // #nosec G404
	pg := s.opts.PortGroups[n]
	return pg, nil
}

// SelectOrCreatePortGroup - Selects or Creates a PG given a symId and host
// and the host type
func (s *service) SelectOrCreatePortGroup(symID string, host *types.Host, pmaxClient pmax.Pmax) (string, error) {
	if host == nil {
		return "", fmt.Errorf("SelectOrCreatePortGroup: host can't be nil")
	}
	if host.HostType == "Fibre" {
		return s.SelectOrCreateFCPGForHost(symID, host, pmaxClient)
	}
	return s.SelectPortGroup()
}

// SelectOrCreateFCPGForHost - Selects or creates a Fibre Channel PG given a symid and host
func (s *service) SelectOrCreateFCPGForHost(symID string, host *types.Host, pmaxClient pmax.Pmax) (string, error) {
	if host == nil {
		return "", fmt.Errorf("SelectOrCreateFCPGForHost: host can't be nil")
	}
	validPortGroupID := ""
	hostID := host.HostID
	var portListFromHost []string
	var isValidHost bool
	if host.HostType == "Fibre" {
		for _, initiator := range host.Initiators {
			initList, err := pmaxClient.GetInitiatorList(symID, initiator, false, false)
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
	fcPortGroupList, err := pmaxClient.GetPortGroupList(symID, "fibre")
	if err != nil {
		return "", fmt.Errorf("Failed to fetch Fibre channel port groups for array: %s", symID)
	}
	log.Debugf("List of Fibre Channel Port Groups fetched from array: %v", fcPortGroupList)
	filteredPGList := make([]string, 0)
	for _, portGroupID := range fcPortGroupList.PortGroupIDs {
		pgPrefix := "csi-" + s.opts.ClusterPrefix
		if strings.Contains(portGroupID, pgPrefix) {
			filteredPGList = append(filteredPGList, portGroupID)
		}
	}
	for _, portGroupID := range filteredPGList {
		portGroup, err := pmaxClient.GetPortGroupByID(symID, portGroupID)
		if err != nil {
			log.Error("Failed to fetch port group details")
			continue
		} else {
			var portList []string
			if portGroup.PortGroupType == "Fibre" {
				for _, portKey := range portGroup.SymmetrixPortKey {
					dirPort := fmt.Sprintf("%s:%s", portKey.DirectorID, portKey.PortID)
					portList = append(portList, dirPort)
				}
				sort.Strings(portList)
				sort.Strings(portListFromHost)
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
		_, err = pmaxClient.CreatePortGroup(symID, portGroupName, portKeys)
		if err != nil {
			return "", fmt.Errorf("Failed to create PortGroup - %s. Error - %s", portGroupName, err.Error())
		}
		log.Debugf("Successfully created Port Group for Fibre Channel with name: %s", portGroupName)
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
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	// check snapshot is licensed
	if err := s.IsSnapshotLicensed(symID, pmaxClient); err != nil {
		log.Error("Error - " + err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	vol, err := pmaxClient.GetVolumeByID(symID, devID)
	if err != nil {
		log.Error("Could not find device: " + devID)
		return nil, status.Error(codes.InvalidArgument,
			"Could not find source volume on the array")
	}

	if strings.Contains(vol.Type, "RDF") {
		log.Error("Snapshot on RDF enabled volume is not supported")
		return nil, status.Errorf(codes.InvalidArgument, "Snapshot on RDF enabled volume is not supported")
	}

	// Is it an idempotent request?
	snapInfo, err := pmaxClient.GetSnapshotInfo(symID, devID, snapID)
	if err == nil && snapInfo.VolumeSnapshotSource != nil {
		snapID = fmt.Sprintf("%s-%s-%s", snapID, symID, devID)
		snapshot := &csi.Snapshot{
			SnapshotId:     snapID,
			SourceVolumeId: volID, ReadyToUse: true,
			CreationTime: ptypes.TimestampNow()}
		resp := &csi.CreateSnapshotResponse{Snapshot: snapshot}
		return resp, nil
	}
	symDevID := fmt.Sprintf("%s-%s", symID, devID)
	var stateID = volumeIDType(symDevID)
	if err := stateID.checkAndUpdatePendingState(&snapshotPendingState); err != nil {
		return nil, err
	}
	defer stateID.clearPending(&snapshotPendingState)

	// log all parameters used in CreateSnapshot call
	fields := map[string]interface{}{
		"CSIRequestID": reqID,
		"SymmetrixID":  symID,
		"SnapshotID":   snapID,
		"DeviceID":     devID,
	}
	log.WithFields(fields).Info("Executing CreateSnapshot with following fields")

	// Create snapshot
	snap, err := s.CreateSnapshotFromVolume(symID, vol, snapID, 0, reqID, pmaxClient)
	if err != nil {
		if strings.Contains(err.Error(), "The maximum number of sessions has been exceeded for the specified Source device") {
			return nil, status.Errorf(codes.FailedPrecondition, "Failed to create snapshot: %s", err.Error())
		}
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
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	// check snapshot is licensed
	if err := s.IsSnapshotLicensed(symID, pmaxClient); err != nil {
		log.Error("Error - " + err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Idempotency check
	snapInfo, err := pmaxClient.GetSnapshotInfo(symID, devID, snapID)
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

	symDevID := fmt.Sprintf("%s-%s", symID, devID)
	var stateID = volumeIDType(symDevID)
	if err := stateID.checkAndUpdatePendingState(&snapshotPendingState); err != nil {
		return nil, err
	}
	defer stateID.clearPending(&snapshotPendingState)

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
	err = s.UnlinkAndTerminate(symID, devID, snapID, pmaxClient)
	if err != nil {
		log.Error("Error - " + err.Error())
		return nil, status.Error(codes.Internal, err.Error())
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

//ControllerExpandVolume expands a CSI volume on the Pmax array
func (s *service) ControllerExpandVolume(
	ctx context.Context, req *csi.ControllerExpandVolumeRequest) (
	*csi.ControllerExpandVolumeResponse, error) {
	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	id := req.GetVolumeId()
	_, symID, _, err := s.parseCsiID(id)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	symID, devID, vol, err := s.GetVolumeByID(id, pmaxClient)
	if err != nil {
		log.Errorf("GetVolumeByID failed with (%s) for devID (%s)", err.Error(), devID)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	volName := vol.VolumeIdentifier

	// Check if ExpandVolume request has CapacityRange set
	if req.CapacityRange == nil {
		err = fmt.Errorf("Invalid argument - CapacityRange not set")
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Get the required capacity in cylinders
	requestedSize, err := s.validateVolSize(req.CapacityRange, "", "", pmaxClient)
	if err != nil {
		log.Errorf("Failed to validate volume size (%s). Error(%s)", devID, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	// log all parameters used in ExpandVolume call
	fields := map[string]interface{}{
		"RequestID":     reqID,
		"SymmetrixID":   symID,
		"VolumeName":    volName,
		"DeviceID":      devID,
		"RequestedSize": requestedSize,
	}
	log.WithFields(fields).Info("Executing ExpandVolume with following fields")

	allocatedSize := vol.CapacityCYL

	if requestedSize < allocatedSize {
		log.Errorf("Attempting to shrink size of volume (%s) from (%d) CYL to (%d) CYL",
			volName, allocatedSize, requestedSize)
		return nil, status.Error(codes.InvalidArgument,
			"Attempting to shrink the volume size - unsupported operation")
	}
	if requestedSize == allocatedSize {
		log.Infof("Idempotent call detected for volume (%s) with requested size (%d) CYL and allocated size (%d) CYL",
			volName, requestedSize, allocatedSize)
		return &csi.ControllerExpandVolumeResponse{CapacityBytes: int64(allocatedSize) * cylinderSizeInBytes,
			NodeExpansionRequired: true}, nil
	}

	//Expand the volume
	vol, err = pmaxClient.ExpandVolume(symID, devID, requestedSize)
	if err != nil {
		log.Errorf("Failed to execute ExpandVolume() with error (%s)", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	//return the response with NodeExpansionRequired = true, so that CO could call
	// NodeExpandVolume subsequently
	csiResp := &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         int64(vol.CapacityCYL) * cylinderSizeInBytes,
		NodeExpansionRequired: true,
	}
	return csiResp, nil
}

//MarkVolumeForDeletion renames the volume with deletion prefix and sends a
//request to deletion_worker queue
func (s *service) MarkVolumeForDeletion(symID string, vol *types.Volume, pmaxClient pmax.Pmax) error {
	if vol == nil {
		return fmt.Errorf("MarkVolumeForDeletion: Null volume object")
	}
	oldVolName := vol.VolumeIdentifier
	// Rename the volume to mark it for deletion
	newVolName := fmt.Sprintf("%s%s", DeletionPrefix, oldVolName)
	if len(newVolName) > MaxVolIdentifierLength {
		newVolName = newVolName[:MaxVolIdentifierLength]
	}
	vol, err := pmaxClient.RenameVolume(symID, vol.VolumeID, newVolName)
	if err != nil || vol == nil {
		return fmt.Errorf("MarkVolumeForDeletion: Failed to rename volume %s", oldVolName)
	}
	/*
		// Fetch the uCode version details
		isPostElmSR, err := s.isPostElmSR(symID)
		if err != nil {
			return fmt.Errorf("Failed to get symmetrix uCode version details")
		}
	*/
	err = s.deletionWorker.QueueDeviceForDeletion(vol.VolumeID, vol.VolumeIdentifier, symID)
	if err != nil {
		return err
	}
	log.Infof("Request dispatched to delete worker thread for %s/%s", vol.VolumeID, symID)
	return nil
}

// GetProtectedStorageGroupID returns selected protected SG based on filter and a list of SG
func (s *service) GetProtectedStorageGroupID(storageGroupIDList []string, filter string) string {
	var protectionGroupID string
	for _, sg := range storageGroupIDList {
		if strings.Contains(sg, csiPrefix) && strings.Contains(sg, filter) {
			protectionGroupID = sg
			break
		}
	}
	return protectionGroupID
}

func (s *service) CreateStorageProtectionGroup(ctx context.Context, req *csiext.CreateStorageProtectionGroupRequest) (*csiext.CreateStorageProtectionGroupResponse, error) {
	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}
	id := req.GetVolumeHandle()
	_, symID, _, err := s.parseCsiID(id)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	symID, devID, vol, err := s.GetVolumeByID(id, pmaxClient)
	if err != nil {
		log.Errorf("GetVolumeByID failed with (%s) for devID (%s)", err.Error(), devID)
		return nil, err
	}
	// Get the parameters
	params := req.GetParameters()
	localRDFGroup := params[LocalRDFGroupParam]
	remoteRDFGroup := params[RemoteRDFGroupParam]
	remoteSymID := params[RemoteSymIDParam]
	repMode := params[ReplicationModeParam]
	remoteVolumeID, err := s.GetRemoteVolumeID(symID, localRDFGroup, devID, pmaxClient)
	if err != nil {
		log.Errorf("GetRemoteVolumeID failed with (%s) for devID (%s)", err.Error(), devID)
		return nil, err
	}
	// log all parameters used in CreateStorageProtectionGroup call
	fields := map[string]interface{}{
		"RequestID":         reqID,
		"SymmetrixID":       symID,
		"VolumeName":        vol.VolumeIdentifier,
		"DeviceID":          devID,
		"RemoteDeviceID":    remoteVolumeID,
		"LocalSRDFG":        localRDFGroup,
		"RemoteSymmetrixID": remoteSymID,
		"ReplicationMode":   repMode,
	}
	log.WithFields(fields).Info("Executing CreateStorageProtectionGroup with following fields")

	// localProtectionGroupID refers to local protected storage group having local volume
	localProtectionGroupID := s.GetProtectedStorageGroupID(vol.StorageGroupIDList, localRDFGroup+"-"+repMode)
	if localProtectionGroupID == "" {
		errorMsg := fmt.Sprintf("CreateStorageProtectionGroup failed with (%s) for devID (%s)", "Failed to find protected local storage group", devID)
		log.Error(errorMsg)
		return nil, status.Error(codes.InvalidArgument, errorMsg)
	}
	remoteVol, err := pmaxClient.GetVolumeByID(remoteSymID, remoteVolumeID)
	if err != nil {
		if strings.Contains(err.Error(), cannotBeFound) {
			return nil, status.Errorf(codes.NotFound,
				"Volume not found (Array: %s, Volume: %s)status %s",
				remoteSymID, remoteVolumeID, err.Error())
		}
		return nil, status.Errorf(codes.Internal,
			"failure checking volume (Array: %s, Volume: %s)status %s",
			remoteSymID, remoteVolumeID, err.Error())
	}
	// remoteProtectionGroupID refers to remote protected storage group having remote volume
	remoteProtectionGroupID := s.GetProtectedStorageGroupID(remoteVol.StorageGroupIDList, localRDFGroup+"-"+repMode)
	if remoteProtectionGroupID == "" {
		errorMsg := fmt.Sprintf("CreateStorageProtectionGroup failed with (%s) for devID (%s)", "Failed to find protected remote storage group", remoteVolumeID)
		log.Error(errorMsg)
		return nil, status.Error(codes.InvalidArgument, errorMsg)
	}

	localParams := map[string]string{
		path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam):    symID,
		path.Join(s.opts.ReplicationContextPrefix, LocalRDFGroupParam):  localRDFGroup,
		path.Join(s.opts.ReplicationContextPrefix, RemoteSymIDParam):    remoteSymID,
		path.Join(s.opts.ReplicationContextPrefix, RemoteRDFGroupParam): remoteRDFGroup,
		path.Join(s.opts.ReplicationContextPrefix, "ReplicationMode"):   repMode,
	}
	remoteParams := map[string]string{
		path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam):    remoteSymID,
		path.Join(s.opts.ReplicationContextPrefix, LocalRDFGroupParam):  remoteRDFGroup,
		path.Join(s.opts.ReplicationContextPrefix, RemoteSymIDParam):    symID,
		path.Join(s.opts.ReplicationContextPrefix, RemoteRDFGroupParam): localRDFGroup,
		path.Join(s.opts.ReplicationContextPrefix, "ReplicationMode"):   repMode,
	}
	// found both SGs, return response
	csiExtResp := &csiext.CreateStorageProtectionGroupResponse{
		LocalProtectionGroupId:          localProtectionGroupID,
		RemoteProtectionGroupId:         remoteProtectionGroupID,
		LocalProtectionGroupAttributes:  localParams,
		RemoteProtectionGroupAttributes: remoteParams,
	}
	return csiExtResp, nil
}

func (s *service) CreateRemoteVolume(ctx context.Context, req *csiext.CreateRemoteVolumeRequest) (*csiext.CreateRemoteVolumeResponse, error) {
	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}
	id := req.GetVolumeHandle()
	_, symID, _, err := s.parseCsiID(id)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	symID, devID, vol, err := s.GetVolumeByID(id, pmaxClient)
	if err != nil {
		log.Errorf("GetVolumeByID failed with (%s) for devID (%s)", err.Error(), devID)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Get the parameters
	params := req.GetParameters()
	localRDFGroup := params[LocalRDFGroupParam]
	remoteSymID := params[RemoteSymIDParam]
	repMode := params[ReplicationModeParam]
	remoteServiceLevel := params[RemoteServiceLevelParam]
	remoteRDFGroup := params[RemoteRDFGroupParam]
	remoteVolumeID, err := s.GetRemoteVolumeID(symID, localRDFGroup, devID, pmaxClient)
	if err != nil {
		log.Errorf("GetRemoteVolumeID failed with (%s) for devID (%s)", err.Error(), devID)
		return nil, err
	}
	// log all parameters used in CreateRemoteVolume call
	fields := map[string]interface{}{
		"RequestID":         reqID,
		"SymmetrixID":       symID,
		"VolumeName":        vol.VolumeIdentifier,
		"DeviceID":          devID,
		"RemoteDeviceID":    remoteVolumeID,
		"LocalSRDFG":        localRDFGroup,
		"RemoteSymmetrixID": remoteSymID,
		"ReplicationMode":   repMode,
	}
	log.WithFields(fields).Info("Executing CreateRemoteVolume with following fields")

	remoteVol, err := pmaxClient.GetVolumeByID(remoteSymID, remoteVolumeID)
	if err != nil {
		if strings.Contains(err.Error(), cannotBeFound) {
			return nil, status.Errorf(codes.NotFound,
				"Volume not found (Array: %s, Volume: %s)status %s",
				remoteSymID, remoteVolumeID, err.Error())
		}
		return nil, status.Errorf(codes.Internal,
			"failure checking volume (Array: %s, Volume: %s)status %s",
			remoteSymID, remoteVolumeID, err.Error())
	}
	// Set the volume identifier on the remote volume to be same as local volume
	if remoteVol.VolumeIdentifier == "" {
		remoteVol, err = pmaxClient.RenameVolume(remoteSymID, remoteVol.VolumeID, vol.VolumeIdentifier)
		if err != nil || vol == nil {
			return nil, status.Errorf(codes.InvalidArgument, "RenameRemoteVolume: Failed to rename volume %s %s", remoteVolumeID, err.Error())
		}
	}
	remoteProtectionGroupID := s.GetProtectedStorageGroupID(remoteVol.StorageGroupIDList, localRDFGroup+"-"+repMode)
	if remoteProtectionGroupID == "" {
		errorMsg := fmt.Sprintf("CreateRemoteVolume failed with (%s) for devID (%s)", "Failed to find protected remote storage group", remoteVolumeID)
		log.Error(errorMsg)
		return nil, status.Error(codes.InvalidArgument, errorMsg)
	}
	// Set the volume context for the response
	volContext := map[string]string{
		CapacityGB:   fmt.Sprintf("%.2f", vol.CapacityGB),
		StorageGroup: remoteProtectionGroupID,
		path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam): remoteSymID,
		ServiceLevelParam:  remoteServiceLevel,
		LocalRDFGroupParam: remoteRDFGroup,
		path.Join(s.opts.ReplicationContextPrefix, RemoteSymIDParam): symID,
		RemoteRDFGroupParam: localRDFGroup,
		path.Join(s.opts.ReplicationContextPrefix, ReplicationModeParam): repMode,
	}

	csiExtResp := &csiext.CreateRemoteVolumeResponse{
		RemoteVolume: &csiext.Volume{
			CapacityBytes: int64(remoteVol.CapacityCYL * cylinderSizeInBytes),
			VolumeId:      fmt.Sprintf("%s-%s-%s", remoteVol.VolumeIdentifier, remoteSymID, remoteVol.VolumeID),
			VolumeContext: volContext,
		},
	}
	return csiExtResp, nil
}

func (s *service) DeleteStorageProtectionGroup(ctx context.Context, req *csiext.DeleteStorageProtectionGroupRequest) (*csiext.DeleteStorageProtectionGroupResponse, error) {
	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}
	protectionGroupID := req.GetProtectionGroupId()
	localParams := req.GetProtectionGroupAttributes()
	symID := localParams[path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam)]
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}
	sg, err := pmaxClient.GetProtectedStorageGroup(symID, protectionGroupID)
	if err != nil {
		if strings.Contains(err.Error(), cannotBeFound) || strings.Contains(err.Error(), ignoredViaAWhitelist) {
			// The protected storage group is already deleted
			log.Info(fmt.Sprintf("DeleteStorageProtectionGroup: Could not find protected SG: %s on SymID: %s so assume it's already deleted", protectionGroupID, symID))
			return &csiext.DeleteStorageProtectionGroupResponse{}, nil
		}
		log.Errorf("GetProtectedStorageGroup failed for (%s):(%s)", protectionGroupID, err.Error())
		return nil, status.Errorf(codes.Internal, "GetProtectedStorageGroup failed for (%s):(%s)", protectionGroupID, err.Error())
	}
	if sg.NumDevicesNonGk > 0 {
		log.Errorf("Can't delete protection group: (%s) as it is not empty", protectionGroupID)
		return nil, status.Errorf(codes.FailedPrecondition, "Can't delete protection group: (%s) as it is not empty", protectionGroupID)
	}
	// log all parameters used in DeleteStorageProtectionGroup call
	fields := map[string]interface{}{
		"RequestID":         reqID,
		"SymmetrixID":       symID,
		"ProtectionGroupID": protectionGroupID,
	}
	log.WithFields(fields).Info("Executing DeleteStorageGroup with following fields")

	err = pmaxClient.DeleteStorageGroup(symID, protectionGroupID)
	if err != nil {
		log.Errorf(" DeleteStorageGroup failed for (%s) with (%s)", protectionGroupID, err.Error())
		return nil, status.Errorf(codes.Internal, " DeleteStorageGroup failed for (%s) with (%s)", protectionGroupID, err.Error())
	}
	return &csiext.DeleteStorageProtectionGroupResponse{}, nil
}

func addMetaData(params map[string]string) map[string][]string {
	// CSI specific metadata header for authorization
	log.Debug("Creating meta data for HTTP header")
	var headerMetadata = make(map[string][]string)
	if _, ok := params[CSIPersistentVolumeName]; ok {
		headerMetadata[HeaderPersistentVolumeName] = []string{params[CSIPersistentVolumeName]}
	}

	if _, ok := params[CSIPersistentVolumeClaimName]; ok {
		headerMetadata[HeaderPersistentVolumeClaimName] = []string{params[CSIPersistentVolumeClaimName]}
	}

	if _, ok := params[CSIPVCNamespace]; ok {
		headerMetadata[HeaderPersistentVolumeClaimNamespace] = []string{params[CSIPVCNamespace]}
	}
	return headerMetadata
}

func (s *service) ControllerGetVolume(ctx context.Context, request *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func (s *service) ExecuteAction(ctx context.Context, request *csiext.ExecuteActionRequest) (*csiext.ExecuteActionResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}

func (s *service) GetStorageProtectionGroupStatus(ctx context.Context, request *csiext.GetStorageProtectionGroupStatusRequest) (*csiext.GetStorageProtectionGroupStatusResponse, error) {
	return nil, status.Error(codes.Unimplemented, "not implemented yet")
}
