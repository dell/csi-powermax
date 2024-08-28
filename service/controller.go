/*
 Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

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

	"github.com/dell/csi-powermax/v2/pkg/file"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	pmax "github.com/dell/gopowermax/v2"

	csiext "github.com/dell/dell-csi-extensions/replication"

	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/container-storage-interface/spec/lib/go/csi"
	types "github.com/dell/gopowermax/v2/types/v100"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// constants
const (
	cylinderSizeInBytes    = 1966080
	MiBSizeInBytes         = 1048576
	DefaultVolumeSizeBytes = 1073741824
	// MinVolumeSizeBytes - This is the minimum volume size in bytes. This is equal to
	// the number of bytes to create a volume which requires 1 cylinder less than
	// the number of bytes required for 50 MB
	MinVolumeSizeBytes = 51118080
	// MaxVolumeSizeBytes - This is the maximum volume size in bytes. This is equal to
	// the minimum number of bytes required to create a 65 TB volume on PowerMax arrays
	MaxVolumeSizeBytes              = 70368745881600
	errUnknownAccessType            = "unknown access type is not Block or Mount"
	errUnknownAccessMode            = "access mode cannot be UNKNOWN"
	errNoMultiNodeWriter            = "multi-node with writer(s) only supported for block access type"
	StoragePoolCacheDuration        = 4 * time.Hour
	MaxVolIdentifierLength          = 64
	MaxPortGroupIdentifierLength    = 64
	MaxClusterPrefixLength          = 3
	CSIPrefix                       = "csi"
	DeletionPrefix                  = "_DEL"
	CsiHostPrefix                   = "csi-node-"
	CsiMVPrefix                     = "csi-mv-"
	CsiNoSrpSGPrefix                = "csi-no-srp-sg-"
	CsiVolumePrefix                 = "csi-"
	CsiRepSGPrefix                  = "csi-rep-sg-"
	PublishContextDeviceWWN         = "DEVICE_WWN"
	RemotePublishContextDeviceWWN   = "REMOTE_DEVICE_WWN"
	PublishContextLUNAddress        = "LUN_ADDRESS"
	RemotePublishContextLUNAddress  = "REMOTE_LUN_ADDRESS"
	PortIdentifiers                 = "PORT_IDENTIFIERS"
	RemotePortIdentifiers           = "REMOTE_PORT_IDENTIFIERS"
	PortIdentifierKeyCount          = "PORT_IDENTIFIER_KEYS"
	RemotePortIdentifierKeyCount    = "REMOTE_PORT_IDENTIFIER_KEYS"
	MaxPortIdentifierLength         = 128
	FCSuffix                        = "-FC"
	NVMETCPSuffix                   = "-NVMETCP"
	PGSuffix                        = "PG"
	notFound                        = "not found"      // error message from s.GetVolumeByID when volume not found
	cannotBeFound                   = "Could not find" // error message from pmax when volume not found
	failedToValidateVolumeNameAndID = "Failed to validate combination of Volume Name and Volume ID"
	IscsiTransportProtocol          = "ISCSI"
	FcTransportProtocol             = "FC"
	NvmeTCPTransportProtocol        = "NVMETCP"
	MaxSnapIdentifierLength         = 32
	SnapDelPrefix                   = "DEL"
	delSrcTag                       = "DS"
	StorageGroup                    = "StorageGroup"
	Async                           = "ASYNC"
	Sync                            = "SYNC"
	Metro                           = "METRO"
	ActiveBias                      = "ActiveBias"
	Consistent                      = "Consistent"
	Synchronized                    = "Synchronized"
	FailedOver                      = "Failed Over"
	Suspended                       = "Suspended"
	Invalid                         = "Invalid"
	Split                           = "Split"
	SyncInProgress                  = "SyncInProg"
	Vsphere                         = "VSPHERE"
	MigrationActionCommit           = "Commit"
	HostLimitName                   = "HostLimitName"
	HostLimits                      = "hostLimits"
	HostIOLimitMBSec                = "HostIOLimitMBSec"
	HostIOLimitIOSec                = "HostIOLimitIOSec"
	DynamicDistribution             = "DynamicDistribution"
	NFS                             = "nfs"
	NASServerName                   = "nasServer"
	fileSystemID                    = "file_system_id"
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
	RemoteVolumeIDParam          = "RemoteVolumeID"
	ReplicationModeParam         = "RdfMode"
	CSIPVCNamespace              = "csi.storage.k8s.io/pvc/namespace"
	CSIPersistentVolumeName      = "csi.storage.k8s.io/pv/name"
	CSIPersistentVolumeClaimName = "csi.storage.k8s.io/pvc/name"
	// These map to the above fields in the form of HTTP header names.
	HeaderPersistentVolumeName           = "x-csi-pv-name"
	HeaderPersistentVolumeClaimName      = "x-csi-pv-claimname"
	HeaderPersistentVolumeClaimNamespace = "x-csi-pv-namespace"
	RemoteServiceLevelParam              = "RemoteServiceLevel"
	RemoteSRPParam                       = "RemoteSRP"
	BiasParam                            = "Bias"
	FsTypeParam                          = "csi.storage.k8s.io/fstype"
	AllowRootParam                       = "allowRoot"
	NVMETCPHostType                      = "NVMe/TCP"
)

// Pair - structure which holds a pair
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

func (s *service) GetPortIdentifier(ctx context.Context, symID string, dirPortKey string, pmaxClient pmax.Pmax) (string, error) {
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
	port, err := pmaxClient.GetPort(ctx, symID, dirID, portNumber)
	if err != nil {
		log.Errorf("Couldn't get port details for %s. Error: %s", dirPortKey, err.Error())
		return "", err
	}
	log.Debugf("Symmetrix ID: %s, DirPortKey: %s, Port type: %s",
		symID, dirPortKey, port.SymmetrixPort.Type)
	if !strings.Contains(port.SymmetrixPort.Identifier, "iqn") && !strings.Contains(port.SymmetrixPort.Identifier, "nqn") {
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

// GetPowerMaxClient returns a valid client based on arrays
func (s *service) GetPowerMaxClient(primarySymID string, arrayIDs ...string) (pmax.Pmax, error) {
	if len(arrayIDs) > 0 && arrayIDs[0] != "" {
		return symmetrix.GetPowerMaxClient(primarySymID, arrayIDs...)
	}
	return symmetrix.GetPowerMaxClient(primarySymID)
}

func (s *service) CreateVolume(
	ctx context.Context,
	req *csi.CreateVolumeRequest) (
	*csi.CreateVolumeResponse, error,
) {
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
	pmaxClient, err := s.GetPowerMaxClient(symmetrixID)
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
	err = s.validateStoragePoolID(ctx, symmetrixID, storagePoolID, pmaxClient)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Errorf(codes.InvalidArgument, "%s", err.Error())
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

	// get HostIOLimits for the storage group
	hostLimitName := ""
	hostMBsec := ""
	hostIOsec := ""
	hostDynDistribution := ""
	if params[HostLimitName] != "" {
		hostLimitName = params[HostLimitName]
	}
	if params[HostIOLimitMBSec] != "" {
		hostMBsec = params[HostIOLimitMBSec]
	}
	if params[HostIOLimitIOSec] != "" {
		hostIOsec = params[HostIOLimitIOSec]
	}
	if params[DynamicDistribution] != "" {
		hostDynDistribution = params[DynamicDistribution]
	}

	// Get the namespace
	namespace := ""
	if params[CSIPVCNamespace] != "" {
		namespace = params[CSIPVCNamespace]
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
		if isBlock && !s.opts.EnableBlock {
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

	if params[path.Join(s.opts.ReplicationPrefix, RepEnabledParam)] == "true" {
		if s.opts.IsVsphereEnabled {
			return nil, status.Errorf(codes.Unavailable, "Replication on a vSphere volume is not supported")
		}
		if useNFS {
			return nil, status.Errorf(codes.Unavailable, "Replication on a NFS volume is not supported")
		}
		replicationEnabled = params[path.Join(s.opts.ReplicationPrefix, RepEnabledParam)]
		// remote symmetrix ID and rdf group name are mandatory params when replication is enabled
		remoteSymID = params[path.Join(s.opts.ReplicationPrefix, RemoteSymIDParam)]
		// check if storage class contains SRDG details
		if params[path.Join(s.opts.ReplicationPrefix, LocalRDFGroupParam)] != "" {
			localRDFGrpNo = params[path.Join(s.opts.ReplicationPrefix, LocalRDFGroupParam)]
		}
		if params[path.Join(s.opts.ReplicationPrefix, RemoteRDFGroupParam)] != "" {
			remoteRDFGrpNo = params[path.Join(s.opts.ReplicationPrefix, RemoteRDFGroupParam)]
		}
		repMode = params[path.Join(s.opts.ReplicationPrefix, ReplicationModeParam)]
		remoteServiceLevel = params[path.Join(s.opts.ReplicationPrefix, RemoteServiceLevelParam)]
		remoteSRPID = params[path.Join(s.opts.ReplicationPrefix, RemoteSRPParam)]
		bias = params[path.Join(s.opts.ReplicationPrefix, BiasParam)]

		// Get Local and remote RDFg Numbers from a rest call
		// Create RDFg for a namespace if it doens't exist?
		// Create RDFg when the volume gets added first time for a replication sssn
		if localRDFGrpNo == "" && remoteRDFGrpNo == "" {
			localRDFGrpNo, remoteRDFGrpNo, err = s.GetOrCreateRDFGroup(ctx, symmetrixID, remoteSymID, repMode, namespace, pmaxClient)
			if err != nil {
				return nil, status.Errorf(codes.NotFound, "Received error get/create RDFG, err: %s", err.Error())
			}
			if localRDFGrpNo == "" || remoteRDFGrpNo == "" {
				return nil, status.Errorf(codes.Unavailable, "Can not fetch RDF Group for volume creation get/create RDFG")
			}
			log.Debugf("RDF group for given array pair and RDF mode: local(%s), remote(%s)", localRDFGrpNo, remoteRDFGrpNo)
		}
		if repMode == Metro {
			return s.createMetroVolume(ctx, req, reqID, storagePoolID, symmetrixID, storageGroupName, serviceLevel, thick, remoteSymID, localRDFGrpNo, remoteRDFGrpNo, remoteServiceLevel, remoteSRPID, namespace, applicationPrefix, bias, hostLimitName, hostMBsec, hostIOsec, hostDynDistribution)
		}
		if repMode != Async && repMode != Sync {
			log.Errorf("Unsupported Replication Mode: (%s)", repMode)
			return nil, status.Errorf(codes.InvalidArgument, "Unsupported Replication Mode: (%s)", repMode)
		}
	}

	accessibility := req.GetAccessibilityRequirements()

	// Get the required capacity
	cr := req.GetCapacityRange()
	requiredCylinders, err := s.validateVolSize(ctx, cr, symmetrixID, storagePoolID, pmaxClient)
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
				_, symID, SrcDevID, _, _, err = s.parseCsiID(srcVolID)
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
				snapID, symID, SrcDevID, _, _, err = s.parseCsiID(srcSnapID)
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
		if err := s.IsSnapshotLicensed(ctx, symID, pmaxClient); err != nil {
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
	volumePrefix := s.getClusterPrefix()
	maxLength := MaxVolIdentifierLength - len(volumePrefix) - len(s.getClusterPrefix()) - len(CsiVolumePrefix) - 1
	// First get the short volume name
	shortVolumeName := truncateString(volumeName, maxLength)
	// Form the volume identifier using short volume name and namespace
	var namespaceSuffix string
	if namespace != "" {
		namespaceSuffix = "-" + namespace
	}
	volumeIdentifier := fmt.Sprintf("%s%s-%s%s", CsiVolumePrefix, s.getClusterPrefix(), shortVolumeName, namespaceSuffix)

	if useNFS {
		// calculate size in MiB
		reqSizeInMiB := cr.GetRequiredBytes() / MiBSizeInBytes
		return file.CreateFileSystem(ctx, reqID, accessibility, params, symmetrixID, storagePoolID, serviceLevel, nasServer, volumeIdentifier, allowRoot, reqSizeInMiB, pmaxClient)
	}
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
		if hostLimitName != "" {
			storageGroupName = fmt.Sprintf("%s-%s", storageGroupName, hostLimitName)
		}
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
		sg, err := s.getOrCreateProtectedStorageGroup(ctx, symmetrixID, localProtectionGroupID, namespace, localRDFGrpNo, repMode, reqID, pmaxClient)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Error in getOrCreateProtectedStorageGroup: (%s)", err.Error())
		}
		if sg != nil && sg.Rdf == true {
			// Check the direction of SG
			// Creation of replicated volume is allowed in an SG of type R1
			err := s.VerifyProtectedGroupDirection(ctx, symmetrixID, localProtectionGroupID, localRDFGrpNo, pmaxClient)
			if err != nil {
				return nil, err
			}
		} else {
			isSGUnprotected = true
		}
	}
	// Check existence of the Storage Group and create if necessary.
	sg, err := pmaxClient.GetStorageGroup(ctx, symmetrixID, storageGroupName)
	if err != nil || sg == nil {
		log.Debug(fmt.Sprintf("Unable to find storage group: %s", storageGroupName))
		hostLimitsParam := &types.SetHostIOLimitsParam{
			HostIOLimitMBSec:    hostIOsec,
			HostIOLimitIOSec:    hostMBsec,
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
	var vol *types.Volume
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
	alreadyExists := false
	// isLocalVolumePresent restrict CreateVolumeInProtectedSG call if the volume is present in local SG but not in remote SG
	isLocalVolumePresent := false
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
			if sgid == storageGroupName {
				matchesStorageGroup = true
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
				remoteVolumeID, _, err = s.GetRemoteVolumeID(ctx, symmetrixID, localRDFGrpNo, vol.VolumeID, pmaxClient)
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
						err = s.LinkSRDFVolToSnapshot(ctx, reqID, symID, srcVol.VolumeID, snapID, localProtectionGroupID, localRDFGrpNo, vol, bias, false, pmaxClient)
						if err != nil {
							return nil, err
						}
					} else if srcVolID != "" {
						// Build the temporary snapshot identifier
						tmpSnapID := fmt.Sprintf("%s%s-%d", TempSnap, s.getClusterPrefix(), time.Now().Nanosecond())
						err = s.LinkSRDFVolToVolume(ctx, reqID, symID, srcVol, vol, tmpSnapID, localProtectionGroupID, localRDFGrpNo, "false", false, pmaxClient)
						if err != nil {
							return nil, status.Errorf(codes.Internal, "Failed to create SRDF volume from volume (%s)", err.Error())
						}
					}
				} else { // replication is not enabled
					if srcSnapID != "" {
						err = s.UnlinkTargets(ctx, symID, SrcDevID, pmaxClient)
						if err != nil {
							return nil, status.Errorf(codes.Internal, "Failed unlink existing target from snapshot (%s)", err.Error())
						}
						err = s.LinkVolumeToSnapshot(ctx, symID, srcVol.VolumeID, vol.VolumeID, snapID, reqID, false, pmaxClient)
						if err != nil {
							return nil, status.Errorf(codes.Internal, "Failed to create volume from snapshot (%s)", err.Error())
						}
					} else if srcVolID != "" {
						tmpSnapID := fmt.Sprintf("%s%s-%d", TempSnap, s.getClusterPrefix(), time.Now().Nanosecond())
						err = s.LinkVolumeToVolume(ctx, symID, srcVol, vol.VolumeID, tmpSnapID, reqID, false, pmaxClient)
						if err != nil {
							return nil, status.Errorf(codes.Internal, "Failed to create volume from volume (%s)", err.Error())
						}
					}
				}
			}

			log.WithFields(fields).Info("Idempotent volume detected, returning success")
			vol.VolumeID = fmt.Sprintf("%s-%s-%s", vol.VolumeIdentifier, symmetrixID, vol.VolumeID)
			volResp := s.getCSIVolume(vol)
			// Set the volume context
			attributes := map[string]string{
				ServiceLevelParam: serviceLevel,
				StoragePoolParam:  storagePoolID,
				path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam): symmetrixID,
				CapacityGB:    fmt.Sprintf("%.2f", vol.CapacityGB),
				ContentSource: volContent,
				StorageGroup:  storageGroupName,
				// Format the time output
				"CreationTime": time.Now().Format("20060102150405"),
			}
			if replicationEnabled == "true" {
				addReplicationParamsToVolumeAttributes(attributes, s.opts.ReplicationContextPrefix, remoteSymID, repMode, remoteVolumeID, localRDFGrpNo, remoteRDFGrpNo)
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
		protectedSGID := s.GetProtectedStorageGroupID(vol.StorageGroupIDList, localRDFGrpNo+"-"+repMode)
		if protectedSGID == "" {
			// Volume is not present in Protected Storage Group, Add
			err = s.addVolumesToProtectedStorageGroup(ctx, reqID, symmetrixID, localProtectionGroupID, remoteSymID, remoteProtectionGroupID, false, vol.VolumeID, pmaxClient)
			if err != nil {
				return nil, err
			}
		}
		if isSGUnprotected {
			// If the required SG is still unprotected, protect the local SG with RDF info
			// If valid RDF group is supplied this will create a remote SG, a RDF pair and add the vol in respective SG created
			// Remote storage group name is kept same as local storage group name
			err := s.ProtectStorageGroup(ctx, symmetrixID, remoteSymID, localProtectionGroupID, remoteProtectionGroupID, "", localRDFGrpNo, repMode, vol.VolumeID, reqID, false, pmaxClient)
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
			// Build the temporary snapshot identifier
			snapID := fmt.Sprintf("%s%s-%d", TempSnap, s.getClusterPrefix(), time.Now().Nanosecond())
			if replicationEnabled == "true" {
				err = s.LinkSRDFVolToVolume(ctx, reqID, symID, srcVol, vol, snapID, localProtectionGroupID, localRDFGrpNo, "false", false, pmaxClient)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "Failed to create SRDF volume from volume (%s)", err.Error())
				}
			} else {
				err = s.LinkVolumeToVolume(ctx, symID, srcVol, vol.VolumeID, snapID, reqID, false, pmaxClient)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "Failed to create volume from volume (%s)", err.Error())
				}
			}
		} else if srcSnapID != "" {
			if replicationEnabled == "true" {
				err = s.LinkSRDFVolToSnapshot(ctx, reqID, symID, srcVol.VolumeID, snapID, localProtectionGroupID, localRDFGrpNo, vol, bias, false, pmaxClient)
				if err != nil {
					return nil, err
				}
			} else {
				// Unlink all previous targets from this snapshot if the link is in defined state
				err = s.UnlinkTargets(ctx, symID, SrcDevID, pmaxClient)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "Failed unlink existing target from snapshot (%s)", err.Error())
				}
				err = s.LinkVolumeToSnapshot(ctx, symID, srcVol.VolumeID, vol.VolumeID, snapID, reqID, false, pmaxClient)
				if err != nil {
					return nil, status.Errorf(codes.Internal, "Failed to create volume from snapshot (%s)", err.Error())
				}
			}
		}
	}

	// Formulate the return response
	volID := vol.VolumeID
	vol.VolumeID = fmt.Sprintf("%s-%s-%s", vol.VolumeIdentifier, symmetrixID, vol.VolumeID)
	volResp := s.getCSIVolume(vol)
	volResp.ContentSource = contentSource
	// Set the volume context
	attributes := map[string]string{
		ServiceLevelParam: serviceLevel,
		StoragePoolParam:  storagePoolID,
		path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam): symmetrixID,
		CapacityGB:    fmt.Sprintf("%.2f", vol.CapacityGB),
		ContentSource: volContent,
		StorageGroup:  storageGroupName,
		// Format the time output
		"CreationTime": time.Now().Format("20060102150405"),
	}
	if replicationEnabled == "true" {
		remoteVolumeID, _, err := s.GetRemoteVolumeID(ctx, symmetrixID, localRDFGrpNo, volID, pmaxClient)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to fetch rdf pair information for (%s) - Error (%s)", vol.VolumeID, err.Error())
		}
		addReplicationParamsToVolumeAttributes(attributes, s.opts.ReplicationContextPrefix, remoteSymID, repMode, remoteVolumeID, localRDFGrpNo, remoteRDFGrpNo)
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

func (s *service) createMetroVolume(ctx context.Context, req *csi.CreateVolumeRequest, reqID, storagePoolID, symID, storageGroupName, serviceLevel, thick, remoteSymID, localRDFGrpNo, remoteRDFGrpNo, remoteServiceLevel, remoteSRPID, namespace, applicationPrefix, bias, hostLimitName, hostMBsec, hostIOsec, hostDynDist string) (*csi.CreateVolumeResponse, error) {
	repMode := Metro
	accessibility := req.GetAccessibilityRequirements()
	pmaxClient, err := s.GetPowerMaxClient(symID, remoteSymID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Get the required capacity
	cr := req.GetCapacityRange()

	// validate size on R1
	requiredCylinders, err := s.validateVolSize(ctx, cr, symID, storagePoolID, pmaxClient)
	if err != nil {
		return nil, err
	}

	// validate size on R2
	_, err = s.validateVolSize(ctx, cr, remoteSymID, remoteSRPID, pmaxClient)
	if err != nil {
		return nil, err
	}

	// params from snapshot request
	var srcVolID, srcSnapID string
	var symmID, SrcDevID, snapID string
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
				_, symmID, SrcDevID, _, _, err = s.parseCsiID(srcVolID)
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
				snapID, symmID, SrcDevID, _, _, err = s.parseCsiID(srcSnapID)
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
		if err := s.IsSnapshotLicensed(ctx, symmID, pmaxClient); err != nil {
			log.Error("Error - " + err.Error())
			return nil, status.Error(codes.Internal, err.Error())
		}
	}

	if SrcDevID != "" && symmID != "" {
		if symmID != symID {
			log.Error("The volume content source is in different PowerMax array")
			return nil, status.Errorf(codes.InvalidArgument, "The volume content source is in different PowerMax array")
		}
		srcVol, err = pmaxClient.GetVolumeByID(ctx, symID, SrcDevID)
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
	// First get the short volume name
	shortVolumeName := truncateString(volumeName, maxLength)
	// Form the volume identifier using short volume name and namespace
	var namespaceSuffix string
	if namespace != "" {
		namespaceSuffix = "-" + namespace
	}
	volumeIdentifier := fmt.Sprintf("%s%s-%s%s", CsiVolumePrefix, s.getClusterPrefix(), shortVolumeName, namespaceSuffix)
	// Storage Group is required to be derived from the parameters (such as service level and storage resource pool which are supplied in parameters)
	// Storage Group Name can optionally be supplied in the parameters (for testing) to over-ride the default.
	var remoteStorageGroupName string
	if storageGroupName == "" {
		if applicationPrefix == "" {
			storageGroupName = fmt.Sprintf("%s-%s-%s-%s-SG", CSIPrefix, s.getClusterPrefix(),
				serviceLevel, storagePoolID)
			remoteStorageGroupName = fmt.Sprintf("%s-%s-%s-%s-SG", CSIPrefix, s.getClusterPrefix(),
				remoteServiceLevel, remoteSRPID)
		} else {
			storageGroupName = fmt.Sprintf("%s-%s-%s-%s-%s-SG", CSIPrefix, s.getClusterPrefix(),
				applicationPrefix, serviceLevel, storagePoolID)
			remoteStorageGroupName = fmt.Sprintf("%s-%s-%s-%s-%s-SG", CSIPrefix, s.getClusterPrefix(),
				applicationPrefix, remoteServiceLevel, remoteSRPID)
		}
		if hostLimitName != "" {
			storageGroupName = fmt.Sprintf("%s-%s", storageGroupName, hostLimitName)
			remoteStorageGroupName = fmt.Sprintf("%s-%s", remoteStorageGroupName, hostLimitName)

		}
	}
	// localProtectionGroupID refers to name of Storage Group which has protected local volumes
	// remoteProtectionGroupID refers to name of Storage Group which has protected remote volumes
	localProtectionGroupID := buildProtectionGroupID(namespace, localRDFGrpNo, repMode)
	remoteProtectionGroupID := buildProtectionGroupID(namespace, remoteRDFGrpNo, repMode)

	// log all parameters used in CreateVolume call
	fields := map[string]interface{}{
		"SymmetrixID":                        symID,
		"SRP":                                storagePoolID,
		"Accessibility":                      accessibility,
		"ApplicationPrefix":                  applicationPrefix,
		"volumeIdentifier":                   volumeIdentifier,
		"requiredCylinders":                  requiredCylinders,
		"storageGroupName":                   storageGroupName,
		"CSIRequestID":                       reqID,
		"SourceSnapshot":                     srcSnapID,
		"ReplicationEnabled":                 "true",
		"RemoteSymmID":                       remoteSymID,
		"LocalRDFGroup":                      localRDFGrpNo,
		"RemoteRDFGroup":                     remoteRDFGrpNo,
		"SRDFMode":                           repMode,
		"LocalProtectionGroupID":             localProtectionGroupID,
		"RemoteProtectionGroupID":            remoteProtectionGroupID,
		HeaderPersistentVolumeClaimNamespace: namespace,
	}
	log.WithFields(fields).Info("Executing CreateVolume with following fields")

	// isSGUnprotected is set to true only if SG has a replica, eg if the SG is new
	isSGUnprotected := false
	psg, err := s.getOrCreateProtectedStorageGroup(ctx, symID, localProtectionGroupID, namespace, localRDFGrpNo, repMode, reqID, pmaxClient)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Error in getOrCreateProtectedStorageGroup: (%s)", err.Error())
	}
	if psg != nil && psg.Rdf == true {
		// Check the direction of SG
		// Creation of replicated volume is allowed in an SG of type R1
		err := s.VerifyProtectedGroupDirection(ctx, symID, localProtectionGroupID, localRDFGrpNo, pmaxClient)
		if err != nil {
			return nil, err
		}
	} else {
		isSGUnprotected = true
	}

	// Check existence of the Storage Group and create if necessary on R1.
	sgOnR1, err := pmaxClient.GetStorageGroup(ctx, symID, storageGroupName)
	if err != nil || sgOnR1 == nil {
		log.Debug(fmt.Sprintf("Unable to find storage group: %s", storageGroupName))
		hostLimitsParam := &types.SetHostIOLimitsParam{
			HostIOLimitMBSec:    hostMBsec,
			HostIOLimitIOSec:    hostIOsec,
			DynamicDistribution: hostDynDist,
		}
		optionalPayload := make(map[string]interface{})
		optionalPayload[HostLimits] = hostLimitsParam
		if *hostLimitsParam == (types.SetHostIOLimitsParam{}) {
			optionalPayload = nil
		}
		_, err := pmaxClient.CreateStorageGroup(ctx, symID, storageGroupName, storagePoolID,
			serviceLevel, thick == "true", optionalPayload)
		if err != nil {
			log.Error("Error creating storage group on R1: " + err.Error())
			return nil, status.Errorf(codes.Internal, "Error creating storage group: %s", err.Error())
		}
	}
	// Check existence of Storage Group and create if necessary on R2.
	sgOnR2, err := pmaxClient.GetStorageGroup(ctx, remoteSymID, remoteStorageGroupName)
	if err != nil || sgOnR2 == nil {
		log.Debug(fmt.Sprintf("Unable to find storage group: %s", remoteStorageGroupName))
		hostLimitsParam := &types.SetHostIOLimitsParam{
			HostIOLimitMBSec:    hostMBsec,
			HostIOLimitIOSec:    hostIOsec,
			DynamicDistribution: hostDynDist,
		}
		optionalPayload := make(map[string]interface{})
		optionalPayload[HostLimits] = hostLimitsParam
		if *hostLimitsParam == (types.SetHostIOLimitsParam{}) {
			optionalPayload = nil
		}
		_, err := pmaxClient.CreateStorageGroup(ctx, remoteSymID, remoteStorageGroupName, remoteSRPID,
			remoteServiceLevel, thick == "true", optionalPayload)
		if err != nil {
			log.Error("Error creating storage group on R2: " + err.Error())
			return nil, status.Errorf(codes.Internal, "Error creating storage group: %s", err.Error())
		}
	}

	var vol *types.Volume
	// Idempotency test. We will read the volume and check for:
	// 1. Existence of a volume with matching volume name
	// 2. Matching cylinderSize
	// 3. Is a member of the storage group
	// 4. Is a snapshot/volume target
	log.Debug("Calling GetVolumeIDList for idempotency test")
	// For now an exact match
	volumeIDList, err := pmaxClient.GetVolumeIDList(ctx, symID, volumeIdentifier, false)
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
		vol, err = pmaxClient.GetVolumeByID(ctx, symID, volumeID)
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
			var remoteVolumeID string
			remoteVolumeID, _, err = s.GetRemoteVolumeID(ctx, symID, localRDFGrpNo, vol.VolumeID, pmaxClient)
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
			// Check if remote volume is present in default SG on R2
			remoteVol, err := pmaxClient.GetVolumeByID(ctx, remoteSymID, remoteVolumeID)
			if err != nil {
				log.Error("Error fetching remote volume for idempotence check: " + err.Error())
				return nil, status.Errorf(codes.Internal, "Error fetching remote volume for idempotence check: %s", err.Error())
			}
			if len(remoteVol.StorageGroupIDList) < 2 {
				// remote volume is not added to default storage group on R2 and local volume is present
				isLocalVolumePresent = true
				continue
			}

			// Check for snapshot target
			if volContent != "" {
				if srcSnapID != "" {
					err = s.LinkSRDFVolToSnapshot(ctx, reqID, symID, srcVol.VolumeID, snapID, localProtectionGroupID, localRDFGrpNo, vol, bias, true, pmaxClient)
					if err != nil {
						return nil, err
					}
				} else if srcVolID != "" {
					// Build the temporary snapshot identifier
					snapID := fmt.Sprintf("%s%s-%d", TempSnap, s.getClusterPrefix(), time.Now().Nanosecond())
					err = s.LinkSRDFVolToVolume(ctx, reqID, symID, srcVol, vol, snapID, localProtectionGroupID, localRDFGrpNo, bias, true, pmaxClient)
					if err != nil {
						return nil, err
					}
				}
			}

			log.WithFields(fields).Info("Idempotent volume detected, returning success")
			vol.VolumeID = fmt.Sprintf("%s-%s:%s-%s:%s", volumeIdentifier, symID, remoteSymID, vol.VolumeID, remoteVolumeID)
			volResp := s.getCSIVolume(vol)
			volResp.ContentSource = contentSource
			// Set the volume context
			attributes := map[string]string{
				ServiceLevelParam: serviceLevel,
				StoragePoolParam:  storagePoolID,
				path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam): symID,
				CapacityGB:    fmt.Sprintf("%.2f", vol.CapacityGB),
				ContentSource: volContent,
				StorageGroup:  storageGroupName,
				// Format the time output
				"CreationTime": time.Now().Format("20060102150405"),
			}
			addReplicationParamsToVolumeAttributes(attributes, s.opts.ReplicationContextPrefix, remoteSymID, repMode, remoteVolumeID, localRDFGrpNo, remoteRDFGrpNo)
			volResp.VolumeContext = attributes
			csiResp := &csi.CreateVolumeResponse{
				Volume: volResp,
			}

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

	// CSI specific metadata for authorization
	headerMetadata := addMetaData(req.GetParameters())

	// Let's create the volume
	if !isLocalVolumePresent {
		vol, err = pmaxClient.CreateVolumeInStorageGroupS(ctx, symID, storageGroupName, volumeIdentifier, requiredCylinders, nil, headerMetadata)
		if err != nil {
			log.Error(fmt.Sprintf("Could not create volume: %s: %s", volumeName, err.Error()))
			return nil, status.Errorf(codes.Internal, "Could not create volume: %s: %s", volumeName, err.Error())
		}
	}

	log.Debugf("RDF: Found Rdf enabled")
	// remote storage group name is kept same as local storage group name
	// Check if volume is already added in SG, else add it
	protectedSGID := s.GetProtectedStorageGroupID(vol.StorageGroupIDList, localRDFGrpNo+"-"+repMode)
	if protectedSGID == "" {
		// Volume is not present in Protected Storage Group, Add
		err = s.addVolumesToProtectedStorageGroup(ctx, reqID, symID, localProtectionGroupID, remoteSymID, remoteProtectionGroupID, false, vol.VolumeID, pmaxClient)
		if err != nil {
			return nil, err
		}
	}
	if isSGUnprotected {
		// If the required SG is still unprotected, protect the local SG with RDF info
		// If valid RDF group is supplied this will create a remote SG, a RDF pair and add the vol in respective SG created
		err := s.ProtectStorageGroup(ctx, symID, remoteSymID, localProtectionGroupID, remoteProtectionGroupID, "", localRDFGrpNo, repMode, vol.VolumeID, reqID, bias == "true", pmaxClient)
		if err != nil {
			log.Errorf("Proceeding to remove volume from protected storage group as rollback")
			// Remove volume from protected storage group as a rollback
			// The device could be just a TDEV and can make RDF unmanageable due to slow u4p response
			_, er := pmaxClient.RemoveVolumesFromStorageGroup(ctx, symID, localProtectionGroupID, true, vol.VolumeID)
			if er != nil {
				log.Errorf("Error removing volume %s from protected SG %s with error: %s", vol.VolumeID, localProtectionGroupID, er.Error())
			}
			return nil, err
		}
	}

	remoteVolumeID, _, err := s.GetRemoteVolumeID(ctx, symID, localRDFGrpNo, vol.VolumeID, pmaxClient)
	if err != nil {
		log.Errorf("Failed to fetch remote volume details: %s", err.Error())
		return nil, err
	}
	// RESET SRP of Protected SG on remote array
	r2PSG, err := pmaxClient.GetStorageGroup(ctx, remoteSymID, remoteProtectionGroupID)
	if err != nil {
		log.Errorf("Failed to fetch remote PSG details: %s", err.Error())
		return nil, status.Errorf(codes.Internal, "Failed to fetch remote PSG details %s", err.Error())
	}
	if r2PSG.SRP != "" && r2PSG.SRP != "NONE" {
		resetSRPPayload := &types.UpdateStorageGroupPayload{
			EditStorageGroupActionParam: types.EditStorageGroupActionParam{
				EditStorageGroupSRPParam: &types.EditStorageGroupSRPParam{
					SRPID: "NONE",
				},
			},
			ExecutionOption: types.ExecutionOptionSynchronous,
		}
		err = pmaxClient.UpdateStorageGroupS(ctx, remoteSymID, remoteProtectionGroupID, resetSRPPayload)
		if err != nil {
			log.Errorf("Failed to Update Remote SG SRP to NONE: %s", err.Error())
			return nil, status.Errorf(codes.Internal, "Failed to Update Remote SG SRP to NONE: %s", err.Error())
		}
	}
	// Add this volume to remote Storage group with service levels
	err = pmaxClient.AddVolumesToStorageGroupS(ctx, remoteSymID, remoteStorageGroupName, true, remoteVolumeID)
	if err != nil {
		log.Error(fmt.Sprintf("Could not add volume in SG on R2: %s: %s", remoteVolumeID, err.Error()))
		return nil, status.Errorf(codes.Internal, "Could not add volume in SG on R2: %s: %s", remoteVolumeID, err.Error())
	}

	// If volume content source is specified, initiate no_copy to newly created volume
	if contentSource != nil {
		if srcSnapID != "" {
			err = s.LinkSRDFVolToSnapshot(ctx, reqID, symID, srcVol.VolumeID, snapID, localProtectionGroupID, localRDFGrpNo, vol, bias, true, pmaxClient)
			if err != nil {
				return nil, err
			}
		} else if srcVolID != "" {
			// Build the temporary snapshot identifier
			snapID := fmt.Sprintf("%s%s-%d", TempSnap, s.getClusterPrefix(), time.Now().Nanosecond())
			err = s.LinkSRDFVolToVolume(ctx, reqID, symID, srcVol, vol, snapID, localProtectionGroupID, localRDFGrpNo, bias, true, pmaxClient)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "Failed to create SRDF volume from volume (%s)", err.Error())
			}
		}
	}

	// Formulate the return response
	vol.VolumeID = fmt.Sprintf("%s-%s:%s-%s:%s", volumeIdentifier, symID, remoteSymID, vol.VolumeID, remoteVolumeID)
	volResp := s.getCSIVolume(vol)
	volResp.ContentSource = contentSource
	// Set the volume context
	attributes := map[string]string{
		ServiceLevelParam: serviceLevel,
		StoragePoolParam:  storagePoolID,
		path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam): symID,
		CapacityGB:    fmt.Sprintf("%.2f", vol.CapacityGB),
		ContentSource: volContent,
		StorageGroup:  storageGroupName,
		// Format the time output
		"CreationTime": time.Now().Format("20060102150405"),
	}
	addReplicationParamsToVolumeAttributes(attributes, s.opts.ReplicationContextPrefix, remoteSymID, repMode, remoteVolumeID, localRDFGrpNo, remoteRDFGrpNo)
	attributes[path.Join(s.opts.ReplicationContextPrefix, RemoteVolumeIDParam)] = remoteVolumeID

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

func (s *service) LinkSRDFVolToVolume(ctx context.Context, reqID, symID string, vol, tgtVol *types.Volume, snapID, localProtectionGroupID, localRDFGrpNo string, bias string, isCopy bool, pmaxClient pmax.Pmax) error {
	// Create a snapshot from the Source
	// Set max 1 hr lifetime for the temporary snapshot
	log.Debugf("Creating snapshot %s on %s and linking it to %s", snapID, vol.VolumeID, tgtVol.VolumeID)
	var TTL int64 = 1
	snapInfo, err := s.CreateSnapshotFromVolume(ctx, symID, vol, snapID, TTL, reqID, pmaxClient)
	if err != nil {
		if strings.Contains(err.Error(), "The maximum number of sessions has been exceeded for the specified Source device") {
			return status.Errorf(codes.FailedPrecondition, "Failed to create snapshot: %s", err.Error())
		}
		return err
	}
	// Link the Target to the created snapshot
	err = s.LinkSRDFVolToSnapshot(ctx, reqID, symID, vol.VolumeID, snapID, localProtectionGroupID, localRDFGrpNo, tgtVol, bias, isCopy, pmaxClient)
	if err != nil {
		return err
	}
	// Push the temporary snapshot created for cleanup
	var cleanReq snapCleanupRequest
	cleanReq.snapshotID = snapInfo.SnapshotName
	cleanReq.symmetrixID = symID
	cleanReq.volumeID = vol.VolumeID
	cleanReq.requestID = reqID
	snapCleaner.requestCleanup(&cleanReq)
	return nil
}

func (s *service) LinkSRDFVolToSnapshot(ctx context.Context, reqID, symID, srcVolID, snapID, localProtectionGroupID, localRDFGrpNo string, tgtVol *types.Volume, bias string, isCopy bool, pmaxClient pmax.Pmax) error {
	// Take lock on SG
	var lockHandle string
	lockHandle = fmt.Sprintf("%s%s", localProtectionGroupID, symID)
	lockNum := RequestLock(lockHandle, reqID)
	defer ReleaseLock(lockHandle, reqID, lockNum)
	bbias, _ := strconv.ParseBool(bias)
	if !tgtVol.SnapTarget {
		// Unlink all previous targets from this snapshot if the link is in defined state
		err := s.UnlinkTargets(ctx, symID, srcVolID, pmaxClient)
		if err != nil {
			return status.Errorf(codes.Internal, "Failed unlink existing target from snapshot (%s)", err.Error())
		}
		// before linking snapshot, we need to suspend the protected storage group
		err = suspend(ctx, symID, localProtectionGroupID, localRDFGrpNo, pmaxClient)
		if err != nil {
			log.Errorf("suspend failed for (%s) err: %s", localProtectionGroupID, err.Error())
			return status.Errorf(codes.Internal, "Failed to create volume from snapshot (%s)", err.Error())
		}
		err = s.LinkVolumeToSnapshot(ctx, symID, srcVolID, tgtVol.VolumeID, snapID, reqID, isCopy, pmaxClient)
		if err != nil {
			return status.Errorf(codes.Internal, "Failed to create volume from snapshot (%s)", err.Error())
		}
	}
	err := establish(ctx, symID, localProtectionGroupID, localRDFGrpNo, bbias, pmaxClient)
	if err != nil {
		log.Errorf("establish failed for (%s) err: %s", localProtectionGroupID, err.Error())
		return status.Errorf(codes.Internal, "Failed to create volume from snapshot (%s)", err.Error())
	}
	return nil
}

func addReplicationParamsToVolumeAttributes(attributes map[string]string, prefix, remoteSymID, repMode, remoteVolID, localRDFGrpNo, remoteRDFGrpNo string) {
	attributes[path.Join(prefix, RemoteSymIDParam)] = remoteSymID
	attributes[path.Join(prefix, ReplicationModeParam)] = repMode
	attributes[path.Join(prefix, RemoteVolumeIDParam)] = remoteVolID
	attributes[path.Join(prefix, LocalRDFGroupParam)] = localRDFGrpNo
	attributes[path.Join(prefix, RemoteRDFGroupParam)] = remoteRDFGrpNo
}

func (s *service) getOrCreateProtectedStorageGroup(ctx context.Context, symID, localProtectionGroupID, _, localRDFGrpNo, repMode, reqID string, pmaxClient pmax.Pmax) (*types.RDFStorageGroup, error) {
	var lockHandle string
	lockHandle = fmt.Sprintf("%s%s", localProtectionGroupID, symID)
	lockNum := RequestLock(lockHandle, reqID)
	defer ReleaseLock(lockHandle, reqID, lockNum)
	sg, err := pmaxClient.GetProtectedStorageGroup(ctx, symID, localProtectionGroupID)
	if err != nil || sg == nil {
		// Verify the creation of new protected storage group is valid
		err = s.verifyProtectionGroupID(ctx, symID, localRDFGrpNo, repMode, pmaxClient)
		if err != nil {
			log.Errorf("VerifyProtectionGroupID failed:(%s)", err.Error())
			return nil, status.Errorf(codes.Internal, "VerifyProtectionGroupID failed:(%s)", err.Error())
		}
		// this SG is valid, new and will need protection if working in replication mode
		// Create protected SG
		_, err := pmaxClient.CreateStorageGroup(ctx, symID, localProtectionGroupID, "None", "", false, nil)
		if err != nil {
			log.Errorf("Error creating protected storage group (%s): (%s)", localProtectionGroupID, err.Error())
			return nil, status.Errorf(codes.Internal, "Error creating protected storage group (%s): (%s)", localProtectionGroupID, err.Error())
		}
	}
	return sg, nil
}

// verifyProtectionGroupID verifies the ProtectionGroupID's uniqueness w.r.t the srdf mode
// For metro mode, one srdf group can have rdf pairing from many namespace
// For sync mode, one srdf group can have rdf pairing from many namespaces
// For async mode, one srdf group can only have rdf pairing from one namespace
// In async rdf mode there should be One to One correspondence between namespace and srdf group
func (s *service) verifyProtectionGroupID(ctx context.Context, symID, localRdfGrpNo, repMode string, pmaxClient pmax.Pmax) error {
	sgList, err := pmaxClient.GetStorageGroupIDList(ctx, symID, "", false)
	if err != nil {
		return err
	}
	for _, value := range sgList.StorageGroupIDs {
		// Is it trying to create more than one SG in async mode for one rdf group
		if (repMode == Async) &&
			(strings.Contains(value, "-"+localRdfGrpNo+"-"+Async) || strings.Contains(value, "-"+localRdfGrpNo+"-"+Sync) || strings.Contains(value, "-"+localRdfGrpNo+"-"+Metro)) {
			return fmt.Errorf("RDF group (%s) is already a part of ReplicationGroup (%s) in Sync/Async/Metro mode", localRdfGrpNo, value)
		}

		// Is it trying to create a SG with a rdf group which is already used in Async/Metro mode
		if repMode == Sync && (strings.Contains(value, "-"+localRdfGrpNo+"-"+Async) || strings.Contains(value, "-"+localRdfGrpNo+"-"+Metro)) {
			return fmt.Errorf("RDF group (%s) is already part of another Async/Metro mode ReplicationGroup (%s)", localRdfGrpNo, value)
		}

		// Is it trying to create a SG with a rdf group which is already used in Async/Sync mode
		if repMode == Metro && (strings.Contains(value, "-"+localRdfGrpNo+"-"+Async) || strings.Contains(value, "-"+localRdfGrpNo+"-"+Sync)) {
			return fmt.Errorf("RDF group (%s) is already part of another Async/Sync mode ReplicationGroup (%s)", localRdfGrpNo, value)
		}
	}
	return nil
}

// validateVolSize uses the CapacityRange range params to determine what size
// volume to create, and returns an error if volume size would be greater than
// the given limit. Returned size is in number of cylinders
func (s *service) validateVolSize(ctx context.Context, cr *csi.CapacityRange, symmetrixID, storagePoolID string, pmaxClient pmax.Pmax) (int, error) {
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
		srp, fba, ckd, err := s.getStoragePoolCapacities(ctx, symmetrixID, storagePoolID, pmaxClient)
		if err != nil {
			return 0, err
		}
		if srp != nil {
			totalSrpCapInGB := srp.UsableTotInTB * 1024.0
			usedSrpCapInGB := srp.UsableUsedInTB * 1024.0
			remainingCapInGB := totalSrpCapInGB - usedSrpCapInGB
			// maxAvailBytes is the remaining capacity in bytes
			maxAvailBytes = int64(remainingCapInGB) * 1024 * 1024 * 1024
			log.Infof("totalSrcCapInGB %f usedSrpCapInGB %f remainingCapInGB %f maxAvailBytes %d",
				totalSrpCapInGB, usedSrpCapInGB, remainingCapInGB, maxAvailBytes)
		} else if (fba != nil) || (ckd != nil) {
			var totalCkdCapInGB float64
			var usedCkdCapInGB float64
			var totalFbaCapInGB float64
			var usedFbaCapInGB float64
			if ckd != nil {
				totalCkdCapInGB = ckd.Provisioned.UsableTotInTB * 1024.0
				usedCkdCapInGB = ckd.Provisioned.UsableUsedInTB * 1024.0
			}

			if fba != nil {
				totalFbaCapInGB = fba.Provisioned.UsableTotInTB * 1024.0
				usedFbaCapInGB = fba.Provisioned.UsableUsedInTB * 1024.0
			}
			totalSrpCapInGB := totalFbaCapInGB + totalCkdCapInGB
			usedSrpCapInGB := usedFbaCapInGB + usedCkdCapInGB
			remainingCapInGB := totalSrpCapInGB - usedSrpCapInGB
			// maxAvailBytes is the remaining capacity in bytes
			maxAvailBytes = int64(remainingCapInGB) * 1024 * 1024 * 1024
			log.Infof("totalSrcCapInGB %f usedSrpCapInGB %f remainingCapInGB %f maxAvailBytes %d",
				totalSrpCapInGB, usedSrpCapInGB, remainingCapInGB, maxAvailBytes)
		}
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
		log.Warningf("bad capacity: requested size (%d bytes) is less than the minimum volume size (%d bytes) supported by PowerMax..", minSizeBytes, MinVolumeSizeBytes)
		log.Warning("Proceeding with minimum volume size supported by PowerMax Array ......")
		minSizeBytes = MinVolumeSizeBytes
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
func (s *service) validateStoragePoolID(ctx context.Context, symmetrixID string, storagePoolID string, pmaxClient pmax.Pmax) error {
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
	list, err := pmaxClient.GetStoragePoolList(ctx, symmetrixID)
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
func (s *service) createCSIVolumeID(_, volumeName, symID, devID string) string {
	// return fmt.Sprintf("%s-%s-%s-%s", volumePrefix, volumeName, symID, devID)
	return fmt.Sprintf("%s%s-%s-%s-%s", CsiVolumePrefix, s.getClusterPrefix(), volumeName, symID, devID)
}

// parseCsiID returns the VolumeName, Array ID, and Device ID given the CSI ID.
// The last 19 characters of the CSI volume ID are special:
//
//	     A dash '-', followed by 12 digits of array serial number, followed by a dash, followed by 5 digits of array device id.
//		That's 19 characters total on the right end.
//
// Also an error returned if mal-formatted.
func (s *service) parseCsiID(csiID string) (
	volName string, arrayID string, devID string, remoteSymID, remoteVolID string, err error,
) {
	if csiID == "" {
		err = fmt.Errorf("A Volume ID is required for the request")
		return
	}
	// get the Device ID and Array ID
	idComponents := strings.Split(csiID, "-")
	// Protect against mal-formed component
	numOfIDComponents := len(idComponents)
	if numOfIDComponents < 3 {
		// Not well-formed
		err = fmt.Errorf("The CSI ID %s is not formed correctly", csiID)
		return
	}
	// check for file system and non FS, format:
	// fS:     csi-ABC-pmax-448c258b72-ns1-nsx-000120000549-649112ce-742b-b93a-abcd-026048200208
	// non-fs: csi-ABC-pmax-260602731A-ns1-nsx-000120000548-011AB
	lastElem := idComponents[numOfIDComponents-1]
	scndLastElem := idComponents[numOfIDComponents-2]
	// Device ID is the last token
	devID = lastElem
	// Array ID is the second to last token
	arrayID = scndLastElem
	if len(lastElem) > 5 && len(scndLastElem) < 12 {
		// devID is fsID
		devID = strings.Join(idComponents[numOfIDComponents-5:], "-")
		arrayID = idComponents[numOfIDComponents-6]
	}
	// The two here is for two dashes - one at front of array ID and one between the Array ID and Device ID
	lengthOfTrailer := len(devID) + len(arrayID) + 2
	length := len(csiID)
	if length <= lengthOfTrailer+2 {
		// Not well formed...
		err = fmt.Errorf("The CSI ID %s is not formed correctly", csiID)
		return
	}
	volName = csiID[0 : length-lengthOfTrailer]

	// Check if the symID and devID has remoteInfo
	if strings.Contains(arrayID, ":") {
		arrays := strings.Split(arrayID, ":")
		arrayID = arrays[0]
		remoteSymID = arrays[1]
		vols := strings.Split(devID, ":")
		devID = vols[0]
		remoteVolID = vols[1]
	}
	return
}

func (s *service) DeleteVolume(
	ctx context.Context,
	req *csi.DeleteVolumeRequest) (
	*csi.DeleteVolumeResponse, error,
) {
	id := req.GetVolumeId()
	volName, symID, devID, remoteSymID, remDevID, err := s.parseCsiID(id)
	if err != nil {
		// We couldn't comprehend the identifier.
		log.Info("Could not parse CSI VolumeId: " + id)
		return &csi.DeleteVolumeResponse{}, status.Error(codes.InvalidArgument, "Could not parse CSI VolumeId")
	}
	pmaxClient, err := s.GetPowerMaxClient(symID, remoteSymID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	volumeID := volumeIDType(id)
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
	// Check if devID is a non-fileSystem
	_, err = pmaxClient.GetVolumeByID(ctx, symID, devID)
	if err != nil {
		log.Debugf("Error:(%s) fetching volume with ID %s", err.Error(), devID)
		log.Debugf("checking for fileSystem...")
		err := s.deleteFileSystem(ctx, reqID, symID, volName, devID, id, pmaxClient)
		if err != nil {
			return nil, err
		}
		return &csi.DeleteVolumeResponse{}, nil
	}

	// delete non-FS volumes
	err = s.deleteVolume(ctx, reqID, symID, volName, devID, id, pmaxClient)
	if err != nil {
		return nil, err
	}
	// Delete scenario for SRDF METRO volumes
	if remoteSymID != "" {
		// set volume identifier on remote volume residing on remote SymID
		_, err := pmaxClient.RenameVolume(ctx, remoteSymID, remDevID, volName)
		if err != nil {
			if strings.Contains(err.Error(), cannotBeFound) {
				// The remote volume is already deleted
				log.Infof("DeleteVolume: Could not find volume: %s/%s so assume it's already deleted", symID, devID)
				return &csi.DeleteVolumeResponse{}, nil
			}
			return nil, status.Errorf(codes.InvalidArgument, "RenameRemoteVolume: Failed to rename volume %s %s", remDevID, err.Error())
		}
		err = s.deleteVolume(ctx, reqID, remoteSymID, volName, remDevID, id, pmaxClient)
		if err != nil {
			return nil, err
		}
	}
	return &csi.DeleteVolumeResponse{}, nil
}

func (s *service) deleteVolume(ctx context.Context, reqID, symID, volName, devID, id string, pmaxClient pmax.Pmax) error {
	// log all parameters used in DeleteVolume call
	fields := map[string]interface{}{
		"SymmetrixID":  symID,
		"VolumeName":   volName,
		"DeviceID":     devID,
		"CSIRequestID": reqID,
	}
	log.WithFields(fields).Info("Executing DeleteVolume with following fields")

	vol, err := pmaxClient.GetVolumeByID(ctx, symID, devID)
	log.Debugf("vol: %#v, error: %#v", vol, err)

	if err != nil {
		if strings.Contains(err.Error(), cannotBeFound) {
			// The volume is already deleted
			log.Info(fmt.Sprintf("DeleteVolume: Could not find volume: %s/%s so assume it's already deleted", symID, devID))
			return nil
		}
		return status.Errorf(codes.Internal, "Could not retrieve volume: (%s)", err.Error())
	}

	if vol.VolumeIdentifier != volName {
		// This volume is already deleted or marked for deletion,
		// or volume id is an old stale identifier not matching a volume.
		// Either way idempotence calls for doing nothing and returning ok.
		log.Info(fmt.Sprintf("DeleteVolume: VolumeIdentifier %s did not match volume name %s so assume it's already deleted",
			vol.VolumeIdentifier, volName))
		return nil
	}

	// find if volume is present in any masking view
	for _, sgid := range vol.StorageGroupIDList {
		sg, err := pmaxClient.GetStorageGroup(ctx, symID, sgid)
		if err != nil || sg == nil {
			log.Error(fmt.Sprintf("DeleteVolume: could not retrieve Storage Group %s/%s", symID, sgid))
			return status.Errorf(codes.Internal, "Unable to find storage group: %s in %s", sgid, symID)
		}
		if sg.NumOfMaskingViews > 0 {
			log.Error(fmt.Sprintf("DeleteVolume: Volume %s is in use by Storage Group %s which has Masking Views",
				id, sgid))
			return status.Errorf(codes.FailedPrecondition, "Volume is in use")
		}
	}

	// Verify if volume is snapshot source
	if vol.SnapSource {
		// Execute soft delete i.e. return DeleteVolume success to the CO/k8s
		// after appending the volumeID with 'DS' tag. While appending the tag, ensure
		// that length of volume name shouldn't exceed MaxVolIdentifierLength

		var newVolName string
		// truncLen is number of characters to truncate if new volume name with DS tag exceeds
		// MaxVolIdentifierLength
		truncLen := len(volName) + len(delSrcTag) - MaxVolIdentifierLength
		if truncLen > 0 {
			// Truncate volName to fit in '-' and 'DS' tag
			volName = volName[:len(volName)-truncLen]
		}
		newVolName = fmt.Sprintf("%s-%s", volName, delSrcTag)
		vol, err = pmaxClient.RenameVolume(ctx, symID, devID, newVolName)
		if err != nil || vol == nil {
			log.Error(fmt.Sprintf("DeleteVolume: Could not rename volume %s", id))
			return status.Errorf(codes.Internal, "Failed to rename volume")
		}
		log.Infof("Soft deletion of source volume (%s) is successful", volName)
		return nil
	}
	err = s.MarkVolumeForDeletion(ctx, symID, vol, pmaxClient)
	if err != nil {
		log.Error("RequestSoftVolDelete failed with error - ", err.Error())
		return status.Errorf(codes.Internal, "Failed marking volume for deletion with error (%s)", err.Error())
	}
	return nil
}

func (s *service) deleteFileSystem(ctx context.Context, reqID, symID, fsName, fsID, _ string, pmaxClient pmax.Pmax) error {
	// log all parameters used in DeleteVolume call
	fields := map[string]interface{}{
		"SymmetrixID":    symID,
		"fileSystemName": fsName,
		"fileSystemID":   fsID,
		"CSIRequestID":   reqID,
	}
	log.WithFields(fields).Info("Executing Delete File System with following fields")

	fileSystem, err := pmaxClient.GetFileSystemByID(ctx, symID, fsID)
	log.Debugf("fileSysetm: %#v, error: %#v", fileSystem, err)

	if err != nil {
		if strings.Contains(err.Error(), cannotBeFound) {
			// The volume is already deleted
			log.Info(fmt.Sprintf("DeleteVolume: Could not find file system: %s/%s so assume it's already deleted", symID, fsID))
			return nil
		}
		return status.Errorf(codes.Internal, "Could not retrieve fileSystem: (%s)", err.Error())
	}

	if fileSystem.Name != fsName {
		// This volume is already deleted or marked for deletion,
		// or volume id is an old stale identifier not matching a volume.
		// Either way idempotence calls for doing nothing and returning ok.
		log.Info(fmt.Sprintf("DeleteVolume: FileSystem name %s did not match fileSystem name %s so assume it's already deleted",
			fileSystem.Name, fsName))
		return nil
	}

	// find if file system has any NFS export
	query := types.QueryParams{fileSystemID: fsID}
	nfsExportList, err := pmaxClient.GetNFSExportList(ctx, symID, query)
	if nfsExportList.Count > 0 {
		nfsExportItem := nfsExportList.ResultList.NFSExportList[0]
		log.Errorf("DeleteVolume: file system has NFS export ID:%s/name:%s on symID: %s", nfsExportItem.ID, nfsExportItem.ID, symID)
		return status.Errorf(codes.Internal, "file system has NFS export ID:%s/name:%s on symID: %s", nfsExportItem.ID, nfsExportItem.ID, symID)
	}

	// Delete the file System as there is no NFS export
	err = file.DeleteFileSystem(ctx, symID, fsID, pmaxClient)
	if err != nil {
		log.Error("DeleteFileSystem failed with error - ", err.Error())
		return status.Errorf(codes.Internal, "Failed deletion of File System with error (%s)", err.Error())
	}
	return nil
}

func (s *service) ControllerPublishVolume(
	ctx context.Context,
	req *csi.ControllerPublishVolumeRequest) (
	*csi.ControllerPublishVolumeResponse, error,
) {
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
	_, symID, devID, remoteSymID, remoteVolumeID, err := s.parseCsiID(volID)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", volID)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", volID)
	}
	pmaxClient, err := s.GetPowerMaxClient(symID, remoteSymID)
	if err != nil {
		log.Error(err.Error())

		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	volumeID := volumeIDType(volID)
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
	symID, devID, vol, err := s.GetVolumeByID(ctx, volID, pmaxClient)
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
		"IsVsphereVolume": s.opts.IsVsphereEnabled,
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
		isNVMETCP, err = s.IsNodeNVMe(ctx, symID, nodeID, pmaxClient)
		if err != nil {
			return nil, status.Error(codes.NotFound, err.Error())
		}
		if !isNVMETCP {
			isISCSI, err = s.IsNodeISCSI(ctx, symID, nodeID, pmaxClient)
			if err != nil {
				return nil, status.Error(codes.NotFound, err.Error())
			}
		}
	}

	hostID, tgtStorageGroupID, tgtMaskingViewID := s.GetNVMETCPHostSGAndMVIDFromNodeID(nodeID)

	if !isNVMETCP {
		// Update the values, if NVME is false
		hostID, tgtStorageGroupID, tgtMaskingViewID = s.GetHostSGAndMVIDFromNodeID(nodeID, isISCSI)
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
				log.Warningf("REQ ID: %s Mismatch between calculated value: %s and latest value: %s from node cache",
					reqID, val.(string), hostID)
			}
		}
	}

	publishContext := map[string]string{
		PublishContextDeviceWWN: vol.EffectiveWWN,
	}

	ctrlPubRes, ctrlPubErr := s.publishVolume(ctx, publishContext, tgtStorageGroupID, hostID, symID, symID, tgtMaskingViewID, devID, reqID, am, pmaxClient, true)
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
		return s.publishVolume(ctx, publishContext, tgtStorageGroupID, hostID, symID, remoteSymID, tgtMaskingViewID, remoteVolumeID, reqID, am, pmaxClient, false)
	}
	return ctrlPubRes, ctrlPubErr
}

func (s *service) publishVolume(ctx context.Context, publishContext map[string]string, tgtStorageGroupID, hostID, clientSymID, symID, tgtMaskingViewID, deviceID, reqID string, accessMode *csi.VolumeCapability_AccessMode, pmaxClient pmax.Pmax, isLocal bool) (*csi.ControllerPublishVolumeResponse, error) {
	waitChan, lockChan, err := s.sgSvc.requestAddVolumeToSGMV(ctx, tgtStorageGroupID, tgtMaskingViewID, hostID, reqID, clientSymID, symID, deviceID, accessMode)
	if err != nil {
		log.Error(err)
		return nil, err
	}
	log.Infof("reqID %s devID %s waitChan %v lockChan %v", reqID, deviceID, waitChan, lockChan)

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
			s.sgSvc.runAddVolumesToSGMV(ctx, symID, tgtStorageGroupID, pmaxClient)
		}
	}

	// Return error if that was the result
	if err != nil {
		return nil, err
	}

	return s.updatePublishContext(ctx, publishContext, symID, tgtMaskingViewID, deviceID, reqID, connections, pmaxClient, isLocal)
}

// Adds the LUN_ADDRESS and SCSI target information to the PublishContext by looking at MaskingView connections.
// The connections may be optionally passed in (returned by the batching code for batched requests) or
// will be read (and retried) if necessary. GetMaskingViewConnections is an expensive call.
// The return arguments are suitable for directly passing back to the grpc called.
// This routine is careful to throw errors if it cannot come up with a valid context, because having ControllerPublish
// succeed but without valid context will only cause the NodeStage or NodePublish to fail.
func (s *service) updatePublishContext(ctx context.Context, publishContext map[string]string, symID, tgtMaskingViewID, deviceID, reqID string,
	connections []*types.MaskingViewConnection, pmaxClient pmax.Pmax, isLocal bool,
) (*csi.ControllerPublishVolumeResponse, error) {
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
		connections, err = pmaxClient.GetMaskingViewConnections(ctx, symID, tgtMaskingViewID, deviceID)
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

		} else if lunid != conn.HostLUNAddress && !s.opts.IsVsphereEnabled {
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
		portIdentifier, err := s.GetPortIdentifier(ctx, symID, dirPortKey, pmaxClient)
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
	keyCount := len(portIdentifiers)/MaxPortIdentifierLength + 1
	for i := 1; i <= keyCount; i++ {
		var portIdentifierKey string
		if isLocal {
			portIdentifierKey = fmt.Sprintf("%s_%d", PortIdentifiers, i)
		} else {
			portIdentifierKey = fmt.Sprintf("%s_%d", RemotePortIdentifiers, i)
		}
		start := (i - 1) * MaxPortIdentifierLength
		end := i * MaxPortIdentifierLength
		if end > len(portIdentifiers) {
			end = len(portIdentifiers)
		}
		publishContext[portIdentifierKey] = portIdentifiers[start:end]
	}
	log.Debugf("Port identifiers in publish context: %s", portIdentifiers)
	if isLocal {
		publishContext[PortIdentifierKeyCount] = strconv.Itoa(keyCount)
		publishContext[PublishContextLUNAddress] = lunid
	} else {
		publishContext[RemotePortIdentifierKeyCount] = strconv.Itoa(keyCount)
		publishContext[RemotePublishContextLUNAddress] = lunid
	}
	return &csi.ControllerPublishVolumeResponse{PublishContext: publishContext}, nil
}

func getMVLockKey(symID, tgtMaskingViewID string) string {
	return symID + ":" + tgtMaskingViewID
}

// IsNodeNVMe - Takes a sym id, node id as input and based on the transport protocol setting
// and the existence of the host on array, it returns a bool to indicate if the Host
// on array is NVMe or not
func (s *service) IsNodeNVMe(ctx context.Context, symID, nodeID string, pmaxClient pmax.Pmax) (bool, error) {
	nvmeTCPHostID, _, nvmeTCPMaskingViewID := s.GetNVMETCPHostSGAndMVIDFromNodeID(nodeID)
	if s.opts.TransportProtocol == NvmeTCPTransportProtocol {
		log.Debug("Preferred transport protocol is set to NVME/TCP")
		// Check if NVME MV exists
		_, nvmeMvErr := pmaxClient.GetMaskingViewByID(ctx, symID, nvmeTCPMaskingViewID)
		if nvmeMvErr == nil {
			return true, nil
		}
		// Check if NVMe Host exists
		nvmetcpHost, nvmetcpHostErr := pmaxClient.GetHostByID(ctx, symID, nvmeTCPHostID)
		if nvmetcpHostErr == nil {
			if nvmetcpHost.HostType == NVMETCPHostType {
				return true, nil
			}
		}
		return false, fmt.Errorf("Failed to fetch host id from array for node: %s", nodeID)
	}
	return false, nil
}

// IsNodeISCSI - Takes a sym id, node id as input and based on the transport protocol setting
// and the existence of the host on array, it returns a bool to indicate if the Host
// on array is ISCSI or not
func (s *service) IsNodeISCSI(ctx context.Context, symID, nodeID string, pmaxClient pmax.Pmax) (bool, error) {
	if s.opts.IsVsphereEnabled {
		err := s.getHostForVsphere(ctx, symID, pmaxClient)
		if err == nil {
			return false, nil
		}
		return false, fmt.Errorf("Failed to fetch host/host group from array for node %s err: %s", nodeID, err.Error())
	}

	fcHostID, _, fcMaskingViewID := s.GetFCHostSGAndMVIDFromNodeID(nodeID)
	iSCSIHostID, _, iSCSIMaskingViewID := s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	if s.opts.TransportProtocol == FcTransportProtocol || s.opts.TransportProtocol == "" {
		log.Debug("Preferred transport protocol is set to FC")
		_, fcmverr := pmaxClient.GetMaskingViewByID(ctx, symID, fcMaskingViewID)
		if fcmverr == nil {
			return false, nil
		}
		// Check if ISCSI MV exists
		_, iscsimverr := pmaxClient.GetMaskingViewByID(ctx, symID, iSCSIMaskingViewID)
		if iscsimverr == nil {
			return true, nil
		}
		// Check if FC Host exists
		fcHost, fcHostErr := pmaxClient.GetHostByID(ctx, symID, fcHostID)
		if fcHostErr == nil {
			if fcHost.HostType == "Fibre" {
				return false, nil
			}
		}
		// Check if ISCSI Host exists
		iscsiHost, iscsiHostErr := pmaxClient.GetHostByID(ctx, symID, iSCSIHostID)
		if iscsiHostErr == nil {
			if iscsiHost.HostType == "iSCSI" {
				return true, nil
			}
		}

	} else if s.opts.TransportProtocol == IscsiTransportProtocol {
		log.Debug("Preferred transport protocol is set to ISCSI")
		// Check if ISCSI MV exists
		_, iscsimverr := pmaxClient.GetMaskingViewByID(ctx, symID, iSCSIMaskingViewID)
		if iscsimverr == nil {
			return true, nil
		}

		// Check if ISCSI Host exists
		iscsiHost, iscsiHostErr := pmaxClient.GetHostByID(ctx, symID, iSCSIHostID)
		if iscsiHostErr == nil {
			if iscsiHost.HostType == "iSCSI" {
				return true, nil
			}
		}
		// Check if FC Host exists
		fcHost, fcHostErr := pmaxClient.GetHostByID(ctx, symID, fcHostID)
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
func (s *service) GetVolumeByID(ctx context.Context,
	volID string, pmaxClient pmax.Pmax,
) (string, string, *types.Volume, error) {
	// parse the volume and get the array serial and volume ID
	volName, symID, devID, _, _, err := s.parseCsiID(volID)
	if err != nil {
		return "", "", nil, status.Errorf(codes.InvalidArgument,
			"volID: %s malformed. Error: %s", volID, err.Error())
	}

	vol, err := pmaxClient.GetVolumeByID(ctx, symID, devID)
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
func (s *service) GetMaskingViewAndSGDetails(ctx context.Context, symID string, sgIDs []string, pmaxClient pmax.Pmax) ([]string, []*types.StorageGroup, error) {
	maskingViewIDs := make([]string, 0)
	storageGroups := make([]*types.StorageGroup, 0)
	// Fetch each SG this device is part of
	for _, sgID := range sgIDs {
		sg, err := pmaxClient.GetStorageGroup(ctx, symID, sgID)
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
	if s.opts.IsVsphereEnabled {
		return s.GetVSphereFCHostSGAndMVIDFromNodeID()
	}
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
	hostID string, err error,
) {
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

func (s *service) GetVSphereFCHostSGAndMVIDFromNodeID() (string, string, string) {
	storageGroupID := CsiNoSrpSGPrefix + s.getClusterPrefix() + "-" + Vsphere
	maskingViewID := CsiMVPrefix + s.getClusterPrefix() + "-" + Vsphere
	return s.opts.VSphereHostName, storageGroupID, maskingViewID
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

// GetNVMETCPHostSGAndMVIDFromNodeID - Forms fibrechannel HostID, StorageGroupID, MaskingViewID
// These are the same as iSCSI except for "_FC" is added as a suffix.
func (s *service) GetNVMETCPHostSGAndMVIDFromNodeID(nodeID string) (string, string, string) {
	hostID, storageGroupID, maskingViewID := s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	return hostID + NVMETCPSuffix, storageGroupID + NVMETCPSuffix, maskingViewID + NVMETCPSuffix
}

func (s *service) ControllerUnpublishVolume(
	ctx context.Context,
	req *csi.ControllerUnpublishVolumeRequest) (
	*csi.ControllerUnpublishVolumeResponse, error,
) {
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
	_, symID, devID, remoteSymID, remoteVolID, err := s.parseCsiID(volID)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", volID)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", volID)
	}
	pmaxClient, err := s.GetPowerMaxClient(symID, remoteSymID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	volumeID := volumeIDType(volID)
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

	// Check if devID is a file system
	_, err = pmaxClient.GetVolumeByID(ctx, symID, devID)
	if err != nil {
		log.Debugf("Error:(%s) fetching volume with ID %s", err.Error(), devID)
		log.Debugf("continuing with fileSystem...")
		// found file system
		return file.DeleteNFSExport(ctx, reqID, symID, devID, pmaxClient)
	}

	// Fetch the volume details from array
	symID, devID, vol, err := s.GetVolumeByID(ctx, volID, pmaxClient)
	if err != nil {
		// CSI sanity test will call this idempotently and expects pass
		if strings.Contains(err.Error(), notFound) || strings.Contains(err.Error(), failedToValidateVolumeNameAndID) {
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
		log.Error("GetVolumeByID Error: " + err.Error())
		return nil, err
	}
	err = s.unpublishVolume(ctx, reqID, vol, nodeID, symID, symID, devID)
	// Return error if that was the result
	if err != nil {
		return nil, err
	}

	// Fetch the volume details from secondary array
	if remoteSymID != "" {
		remVol, err := pmaxClient.GetVolumeByID(ctx, remoteSymID, remoteVolID)
		if err != nil {
			// CSI sanity test will call this idempotently and expects pass
			if strings.Contains(err.Error(), notFound) || strings.Contains(err.Error(), failedToValidateVolumeNameAndID) {
				return &csi.ControllerUnpublishVolumeResponse{}, nil
			}
			log.Error("GetVolumeByID Error: " + err.Error())
			return nil, err
		}
		err = s.unpublishVolume(ctx, reqID, remVol, nodeID, remoteSymID, remoteSymID, remVol.VolumeID)
		// Return error if that was the result
		if err != nil {
			return nil, err
		}
	}
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

func (s *service) unpublishVolume(ctx context.Context, reqID string, vol *types.Volume, nodeID, clientSymID, symID, devID string) error {
	// log all parameters used in ControllerUnpublishVolume call
	fields := map[string]interface{}{
		"SymmetrixID":  symID,
		"VolumeId":     vol.VolumeID,
		"NodeId":       nodeID,
		"CSIRequestID": reqID,
	}
	log.WithFields(fields).Info("Executing ControllerUnpublishVolume with following fields")

	// Determine if the volume is in a FC or ISCSI MV
	_, tgtFCStorageGroupID, tgtFCMaskingViewID := s.GetFCHostSGAndMVIDFromNodeID(nodeID)
	_, tgtISCSIStorageGroupID, tgtISCSIMaskingViewID := s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	_, tgtNVMeTCPStorageGroupID, tgtNVMeTCPMaskingViewID := s.GetNVMETCPHostSGAndMVIDFromNodeID(nodeID)
	isISCSI := false
	isNVMeTCP := false
	if s.opts.IsVsphereEnabled {
		_, tgtFCStorageGroupID, tgtFCMaskingViewID = s.GetVSphereFCHostSGAndMVIDFromNodeID()
	}
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
		} else if storageGroupID == tgtNVMeTCPStorageGroupID {
			volumeInStorageGroup = true
			isNVMeTCP = true
			break
		}
	}

	if !volumeInStorageGroup {
		log.Debug("volume already unpublished")
		return nil
	}
	var tgtStorageGroupID, tgtMaskingViewID string
	if isISCSI {
		tgtStorageGroupID = tgtISCSIStorageGroupID
		tgtMaskingViewID = tgtISCSIMaskingViewID
	} else if isNVMeTCP {
		tgtStorageGroupID = tgtNVMeTCPStorageGroupID
		tgtMaskingViewID = tgtNVMeTCPMaskingViewID
	} else {
		tgtStorageGroupID = tgtFCStorageGroupID
		tgtMaskingViewID = tgtFCMaskingViewID
	}
	waitChan, lockChan, err := s.sgSvc.requestRemoveVolumeFromSGMV(ctx, tgtStorageGroupID, tgtMaskingViewID, reqID, clientSymID, symID, devID)
	if err != nil {
		log.Error(err)
		return err
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
			s.sgSvc.runRemoveVolumesFromSGMV(ctx, symID, tgtStorageGroupID)
		}
	}
	return err
}

func (s *service) ValidateVolumeCapabilities(
	ctx context.Context,
	req *csi.ValidateVolumeCapabilitiesRequest) (
	*csi.ValidateVolumeCapabilitiesResponse, error,
) {
	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	volID := req.GetVolumeId()
	// parse the volume and get the array serial and volume ID
	volName, symID, devID, _, _, err := s.parseCsiID(volID)
	if err != nil {
		log.Error(fmt.Sprintf("volID: %s malformed. Error: %s", volID, err.Error()))
		return nil, status.Errorf(codes.InvalidArgument,
			"volID: %s malformed. Error: %s", volID, err.Error())
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
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

	vol, err := pmaxClient.GetVolumeByID(ctx, symID, devID)
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
	validContext, reasonContext := s.valVolumeContext(ctx, attributes, vol, symID, pmaxClient)
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

func accTypeIsNFS(vcs []*csi.VolumeCapability) bool {
	for _, vc := range vcs {
		if vc.GetMount().GetFsType() == NFS {
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

func (s *service) valVolumeContext(ctx context.Context, attributes map[string]string, vol *types.Volume, symID string, pmaxClient pmax.Pmax) (bool, string) {
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
		sg, err := pmaxClient.GetStorageGroup(ctx, symID, sgID)
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
	vcs []*csi.VolumeCapability, _ *types.Volume,
) (bool, string) {
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
		case csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER:
			break
		case csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER:
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
	_ context.Context,
	_ *csi.ListVolumesRequest) (
	*csi.ListVolumesResponse, error,
) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (s *service) ListSnapshots(
	_ context.Context,
	_ *csi.ListSnapshotsRequest) (
	*csi.ListSnapshotsResponse, error,
) {
	return nil, status.Error(codes.Unimplemented, "")
}

func (s *service) GetCapacity(
	ctx context.Context,
	req *csi.GetCapacityRequest) (
	*csi.GetCapacityResponse, error,
) {
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
	pmaxClient, err := s.GetPowerMaxClient(symmetrixID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		log.Error("Failed to probe with error: " + err.Error())
		return nil, err
	}

	// Storage (resource) Pool. Validate it against exist Pools
	storagePoolID := params[StoragePoolParam]
	err = s.validateStoragePoolID(ctx, symmetrixID, storagePoolID, pmaxClient)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Errorf(codes.InvalidArgument, "%s", err.Error())
	}

	// log all parameters used in GetCapacity call
	fields := map[string]interface{}{
		"SymmetrixID":  symmetrixID,
		"SRP":          storagePoolID,
		"CSIRequestID": reqID,
	}
	log.WithFields(fields).Info("Executing ValidateVolumeCapabilities with following fields")

	// Get storage pool capacities
	srpCap, fba, ckd, err := s.getStoragePoolCapacities(ctx, symmetrixID, storagePoolID, pmaxClient)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "Could not retrieve StoragePool %s. Error(%s)", storagePoolID, err.Error())
	}
	var totalSrpCapInGB float64
	var usedSrpCapInGB float64
	if srpCap != nil {
		totalSrpCapInGB = srpCap.UsableTotInTB * 1024
		usedSrpCapInGB = srpCap.UsableUsedInTB * 1024
	} else if (fba != nil) || (ckd != nil) {
		var totalCkdCapInGB float64
		var usedCkdCapInGB float64
		var totalFbaCapInGB float64
		var usedFbaCapInGB float64
		if ckd != nil {
			totalCkdCapInGB = ckd.Provisioned.UsableTotInTB * 1024.0
			usedCkdCapInGB = ckd.Provisioned.UsableUsedInTB * 1024.0
		}

		if fba != nil {
			totalFbaCapInGB = fba.Provisioned.UsableTotInTB * 1024.0
			usedFbaCapInGB = fba.Provisioned.UsableUsedInTB * 1024.0
		}
		totalSrpCapInGB = totalFbaCapInGB + totalCkdCapInGB
		usedSrpCapInGB = usedFbaCapInGB + usedCkdCapInGB
	}
	remainingCapInGB := totalSrpCapInGB - usedSrpCapInGB
	remainingCapInBytes := remainingCapInGB * 1024 * 1024 * 1024

	return &csi.GetCapacityResponse{
		AvailableCapacity: int64(remainingCapInBytes),
		MaximumVolumeSize: wrapperspb.Int64(MaxVolumeSizeBytes),
	}, nil
}

// Return the storage pool capacities of types.SrpCap
func (s *service) getStoragePoolCapacities(ctx context.Context, symmetrixID, storagePoolID string, pmaxClient pmax.Pmax) (*types.SrpCap, *types.FbaCap, *types.CkdCap, error) {
	// Get storage pool info
	srp, err := pmaxClient.GetStoragePool(ctx, symmetrixID, storagePoolID)
	if err != nil {
		return nil, nil, nil, status.Errorf(codes.Internal, "Could not retrieve StoragePool %s. Error(%s)", storagePoolID, err.Error())
	}
	if srp.SrpCap != nil {
		log.Infof("StoragePoolCapacities: %#v", srp.SrpCap)
		return srp.SrpCap, nil, nil, nil
	}
	if (srp.FbaCap != nil) || (srp.CkdCap != nil) {
		log.Infof("StoragePoolCapacities(Fba/Ckd) : %#v StoragePoolCapacities(Fba/Ckd) : %#v ", srp.FbaCap, srp.CkdCap)
		return nil, srp.FbaCap, srp.CkdCap, nil
	}
	return nil, nil, nil, status.Errorf(codes.Internal, "Could not retrieve StoragePool %s. Error(%s)", storagePoolID, err.Error())
}

func (s *service) ControllerGetCapabilities(
	_ context.Context,
	_ *csi.ControllerGetCapabilitiesRequest) (
	*csi.ControllerGetCapabilitiesResponse, error,
) {
	capabilities := []*csi.ControllerServiceCapability{
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
		{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: csi.ControllerServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
				},
			},
		},
	}

	healthMonitorCapabilities := []*csi.ControllerServiceCapability{
		{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: csi.ControllerServiceCapability_RPC_VOLUME_CONDITION,
				},
			},
		},
		{
			Type: &csi.ControllerServiceCapability_Rpc{
				Rpc: &csi.ControllerServiceCapability_RPC{
					Type: csi.ControllerServiceCapability_RPC_GET_VOLUME,
				},
			},
		},
	}

	if s.opts.IsHealthMonitorEnabled {
		capabilities = append(capabilities, healthMonitorCapabilities...)
	}

	return &csi.ControllerGetCapabilitiesResponse{
		Capabilities: capabilities,
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

	err := s.createPowerMaxClients(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (s *service) requireProbe(ctx context.Context, pmaxClient pmax.Pmax) error {
	// If we're using the proxy, the throttling is in the proxy in front of U4V.
	// so we can handle a large number of pending requests.
	// Otherwise, a small number since there's no protection for U4V.
	if s.opts.UseProxy {
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
func (s *service) SelectOrCreatePortGroup(ctx context.Context, symID string, host *types.Host, pmaxClient pmax.Pmax) (string, error) {
	if host == nil {
		return "", fmt.Errorf("SelectOrCreatePortGroup: host can't be nil")
	}
	if s.opts.IsVsphereEnabled {
		return s.opts.VSpherePortGroup, nil
	}
	if host.HostType == "Fibre" {
		return s.SelectOrCreateFCPGForHost(ctx, symID, host, pmaxClient)
	}
	return s.SelectPortGroup()
}

// SelectOrCreateFCPGForHost - Selects or creates a Fibre Channel PG given a symid and host
func (s *service) SelectOrCreateFCPGForHost(ctx context.Context, symID string, host *types.Host, pmaxClient pmax.Pmax) (string, error) {
	if host == nil {
		return "", fmt.Errorf("SelectOrCreateFCPGForHost: host can't be nil")
	}
	validPortGroupID := ""
	hostID := host.HostID
	var portListFromHost []string
	var isValidHost bool
	if host.HostType == "Fibre" {
		for _, initiator := range host.Initiators {
			initList, err := pmaxClient.GetInitiatorList(ctx, symID, initiator, false, false)
			if err != nil {
				log.Errorf("Failed to get details for initiator - %s", initiator)
				continue
			}
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
	if !isValidHost {
		return "", fmt.Errorf("Failed to find a valid initiator for hostID %s from %s", hostID, symID)
	}
	fcPortGroupList, err := pmaxClient.GetPortGroupList(ctx, symID, "fibre")
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
		portGroup, err := pmaxClient.GetPortGroupByID(ctx, symID, portGroupID)
		if err != nil {
			log.Error("Failed to fetch port group details")
			continue
		}
		var portList []string
		if (portGroup.PortGroupType == "Fibre") || (portGroup.PortGroupType == "SCSI_FC") {
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
		constComponentLength := len(CSIPrefix) + len(s.opts.ClusterPrefix) + len(PGSuffix) + 2 // for the "-"
		MaxDirNameLength := MaxPortGroupIdentifierLength - constComponentLength
		if len(dirNames) > MaxDirNameLength {
			dirNames = truncateString(dirNames, MaxDirNameLength)
		}
		portGroupName := CSIPrefix + "-" + s.opts.ClusterPrefix + "-" + dirNames + PGSuffix
		_, err = pmaxClient.CreatePortGroup(ctx, symID, portGroupName, portKeys, "SCSI_FC")
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
	*csi.CreateSnapshotResponse, error,
) {
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
	// First get the short snap name
	shortSnapName := truncateString(snapName, maxLength)
	// Form the snapshot identifier using short snap name
	snapID := fmt.Sprintf("%s%s-%s", CsiVolumePrefix, s.getClusterPrefix(), shortSnapName)

	// Validate snapshot volume
	volID := req.GetSourceVolumeId()
	if volID == "" {
		return nil, status.Errorf(codes.InvalidArgument,
			"Source volume ID is required for creating snapshot")
	}
	_, localSymID, localDevID, remoteSymID, remoteDevID, err := s.parseCsiID(volID)
	if err != nil {
		// We couldn't comprehend the identifier.
		log.Error("Could not parse CSI VolumeId: " + volID)
		return nil, status.Error(codes.InvalidArgument,
			"Could not parse CSI VolumeId")
	}
	symID, devID := localSymID, localDevID
	if snapSymID, ok := req.Parameters[SymmetrixIDParam]; ok {
		if snapSymID == remoteSymID {
			symID = remoteSymID
			devID = remoteDevID
		} else if snapSymID != localSymID {
			return nil, status.Error(codes.InvalidArgument, "Symmetrix ID in snapclass parameters doesn't match the volume's symmetrix id")
		}
	}
	log.Infof("Creating the snapshot for volume with id, %s, on array %s", devID, symID)

	pmaxClient, err := s.GetPowerMaxClient(localSymID, remoteSymID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Check if the volume is not fileSystem
	_, err = pmaxClient.GetFileSystemByID(ctx, symID, volID)
	if err == nil {
		// there is a file system
		return nil, status.Errorf(codes.Unavailable, "snapshot on a NFS volume is not supported")
	}
	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	// check snapshot is licensed
	if err := s.IsSnapshotLicensed(ctx, symID, pmaxClient); err != nil {
		log.Error("Error - " + err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	vol, err := pmaxClient.GetVolumeByID(ctx, symID, devID)
	if err != nil {
		log.Error("Could not find device: " + devID)
		return nil, status.Error(codes.InvalidArgument,
			"Could not find source volume on the array")
	}

	// Is it an idempotent request?
	snapInfo, err := pmaxClient.GetSnapshotInfo(ctx, symID, devID, snapID)
	if err == nil && snapInfo.VolumeSnapshotSource != nil {
		snapID = fmt.Sprintf("%s-%s-%s", snapID, symID, devID)
		snapshot := &csi.Snapshot{
			SnapshotId:     snapID,
			SourceVolumeId: volID, ReadyToUse: true,
			CreationTime: timestamppb.Now(),
		}
		resp := &csi.CreateSnapshotResponse{Snapshot: snapshot}
		return resp, nil
	}
	symDevID := fmt.Sprintf("%s-%s", symID, devID)
	stateID := volumeIDType(symDevID)
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
	snap, err := s.CreateSnapshotFromVolume(ctx, symID, vol, snapID, 0, reqID, pmaxClient)
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
		SourceVolumeId: volID,
		ReadyToUse:     true,
		CreationTime:   timestamppb.Now(),
	}
	resp := &csi.CreateSnapshotResponse{Snapshot: snapshot}

	log.Debugf("Created snapshot: SnapshotId %s SourceVolumeId %s CreationTime %s",
		snapshot.SnapshotId, snapshot.SourceVolumeId, snapshot.CreationTime.AsTime().Format(time.RFC3339Nano))
	return resp, nil
}

// DeleteSnapshot deletes a snapshot
func (s *service) DeleteSnapshot(
	ctx context.Context,
	req *csi.DeleteSnapshotRequest) (
	*csi.DeleteSnapshotResponse, error,
) {
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
	snapID, symID, devID, _, _, err := s.parseCsiID(id)
	if err != nil {
		// We couldn't comprehend the identifier.
		log.Error("Could not parse CSI snapshot identifier: " + id)
		return nil, status.Error(codes.InvalidArgument, "Snapshot name is not in supported format")
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	// check snapshot is licensed
	if err := s.IsSnapshotLicensed(ctx, symID, pmaxClient); err != nil {
		log.Error("Error - " + err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Idempotency check
	snapInfo, err := pmaxClient.GetSnapshotInfo(ctx, symID, devID, snapID)
	if err != nil {
		// Unisphere returns "not found"
		// when the snapshot is not found for Juniper ucode(10.0)
		if strings.Contains(err.Error(), "not found") {
			return &csi.DeleteSnapshotResponse{}, nil
		}
		// Snapshot to be deleted couldn't be found in the system.
		log.Errorf("GetSnapshotInfo() failed with error (%s) for snapshot (%s)", err.Error(), snapID)
		return nil, status.Errorf(codes.Internal,
			"GetSnapshotInfo() failed with error (%s) for snapshot (%s)", err.Error(), snapID)
	}
	// Unisphere return success and an empty object
	// when the snapshot is not found for Foxtail ucode(9.1)
	if snapInfo.VolumeSnapshotSource == nil {
		return &csi.DeleteSnapshotResponse{}, nil
	}

	symDevID := fmt.Sprintf("%s-%s", symID, devID)
	stateID := volumeIDType(symDevID)
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
	err = s.UnlinkAndTerminate(ctx, symID, devID, snapID, pmaxClient)
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

// ControllerExpandVolume expands a CSI volume on the Pmax array
func (s *service) ControllerExpandVolume(
	ctx context.Context, req *csi.ControllerExpandVolumeRequest) (
	*csi.ControllerExpandVolumeResponse, error,
) {
	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	id := req.GetVolumeId()
	_, symID, devID, _, _, err := s.parseCsiID(id)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	// Check if ExpandVolume request has CapacityRange set
	if req.CapacityRange == nil {
		err = fmt.Errorf("Invalid argument - CapacityRange not set")
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	if accTypeIsNFS([]*csi.VolumeCapability{req.VolumeCapability}) {
		newSizeInMib := (req.CapacityRange.GetRequiredBytes()) / MiBSizeInBytes
		return file.ExpandFileSystem(ctx, reqID, symID, devID, newSizeInMib, pmaxClient)
	}

	symID, devID, vol, err := s.GetVolumeByID(ctx, id, pmaxClient)
	if err != nil {
		log.Errorf("GetVolumeByID failed with (%s) for devID (%s)", err.Error(), devID)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	volName := vol.VolumeIdentifier

	// Get the required capacity in cylinders
	requestedSize, err := s.validateVolSize(ctx, req.CapacityRange, "", "", pmaxClient)
	if err != nil {
		log.Errorf("Failed to validate volume size (%s). Error(%s)", devID, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	// Check if volume is replicated and has RDF info
	var rdfGNo int
	if len(vol.RDFGroupIDList) > 0 {
		rdfGNo = vol.RDFGroupIDList[0].RDFGroupNumber
	}
	// log all parameters used in ExpandVolume call
	fields := map[string]interface{}{
		"RequestID":     reqID,
		"SymmetrixID":   symID,
		"VolumeName":    volName,
		"DeviceID":      devID,
		"RequestedSize": requestedSize,
		"RDF group":     rdfGNo,
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
		return &csi.ControllerExpandVolumeResponse{
			CapacityBytes:         int64(allocatedSize) * cylinderSizeInBytes,
			NodeExpansionRequired: true,
		}, nil
	}

	// Expand the volume
	vol, err = pmaxClient.ExpandVolume(ctx, symID, devID, rdfGNo, requestedSize)
	if err != nil {
		log.Errorf("Failed to execute ExpandVolume() with error (%s)", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	// return the response with NodeExpansionRequired = true, so that CO could call
	// NodeExpandVolume subsequently
	csiResp := &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         int64(vol.CapacityCYL) * cylinderSizeInBytes,
		NodeExpansionRequired: true,
	}
	return csiResp, nil
}

// MarkVolumeForDeletion renames the volume with deletion prefix and sends a
// request to deletion_worker queue
func (s *service) MarkVolumeForDeletion(ctx context.Context, symID string, vol *types.Volume, pmaxClient pmax.Pmax) error {
	if vol == nil {
		return fmt.Errorf("MarkVolumeForDeletion: Null volume object")
	}
	oldVolName := vol.VolumeIdentifier
	// Rename the volume to mark it for deletion
	newVolName := fmt.Sprintf("%s%s", DeletionPrefix, oldVolName)
	if len(newVolName) > MaxVolIdentifierLength {
		newVolName = newVolName[:MaxVolIdentifierLength]
	}
	vol, err := pmaxClient.RenameVolume(ctx, symID, vol.VolumeID, newVolName)
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
	_, symID, _, _, _, err := s.parseCsiID(id)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	symID, devID, vol, err := s.GetVolumeByID(ctx, id, pmaxClient)
	if err != nil {
		log.Errorf("GetVolumeByID failed with (%s) for devID (%s)", err.Error(), devID)
		return nil, err
	}
	// Get the parameters
	params := req.GetParameters()
	localRDFGroup := params[path.Join(s.opts.ReplicationPrefix, LocalRDFGroupParam)]
	remoteSymID := params[path.Join(s.opts.ReplicationPrefix, RemoteSymIDParam)]
	repMode := params[path.Join(s.opts.ReplicationPrefix, ReplicationModeParam)]
	remoteRDFGroup := params[path.Join(s.opts.ReplicationPrefix, RemoteRDFGroupParam)]

	if len(localRDFGroup) < 1 {
		localRDFGroup = strconv.Itoa(vol.RDFGroupIDList[0].RDFGroupNumber)
	}
	remoteVolumeID, rmRDFG, err := s.GetRemoteVolumeID(ctx, symID, localRDFGroup, devID, pmaxClient)
	if err != nil {
		log.Errorf("GetRemoteVolumeID failed with (%s) for devID (%s)", err.Error(), devID)
		return nil, err
	}
	if len(remoteRDFGroup) < 1 {
		remoteRDFGroup = rmRDFG
	}

	// log all parameters used in CreateStorageProtectionGroup call
	fields := map[string]interface{}{
		"RequestID":         reqID,
		"SymmetrixID":       symID,
		"VolumeName":        vol.VolumeIdentifier,
		"DeviceID":          devID,
		"RemoteDeviceID":    remoteVolumeID,
		"LocalSRDFG":        localRDFGroup,
		"RemoteSRDFG":       remoteRDFGroup,
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
	remoteVol, err := pmaxClient.GetVolumeByID(ctx, remoteSymID, remoteVolumeID)
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
	remoteProtectionGroupID := s.GetProtectedStorageGroupID(remoteVol.StorageGroupIDList, remoteRDFGroup+"-"+repMode)
	if remoteProtectionGroupID == "" {
		errorMsg := fmt.Sprintf("CreateStorageProtectionGroup failed with (%s) for devID (%s)", "Failed to find protected remote storage group", remoteVolumeID)
		log.Error(errorMsg)
		return nil, status.Error(codes.InvalidArgument, errorMsg)
	}
	localParams := map[string]string{
		path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam):     symID,
		path.Join(s.opts.ReplicationContextPrefix, LocalRDFGroupParam):   localRDFGroup,
		path.Join(s.opts.ReplicationContextPrefix, RemoteSymIDParam):     remoteSymID,
		path.Join(s.opts.ReplicationContextPrefix, RemoteRDFGroupParam):  remoteRDFGroup,
		path.Join(s.opts.ReplicationContextPrefix, ReplicationModeParam): repMode,
	}
	remoteParams := map[string]string{
		path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam):     remoteSymID,
		path.Join(s.opts.ReplicationContextPrefix, LocalRDFGroupParam):   remoteRDFGroup,
		path.Join(s.opts.ReplicationContextPrefix, RemoteSymIDParam):     symID,
		path.Join(s.opts.ReplicationContextPrefix, RemoteRDFGroupParam):  localRDFGroup,
		path.Join(s.opts.ReplicationContextPrefix, ReplicationModeParam): repMode,
	}
	pgStatus, err := s.getStorageProtectionGroupStatus(ctx, localProtectionGroupID, reqID, localParams)
	if err != nil {
		// Ignore the error for now, status should get updated in a future monitoring call
		log.Warning(fmt.Sprintf("failed to get status for SG: %s, error: %s. continuing",
			localProtectionGroupID, err.Error()))
	}
	// found both SGs, return response
	csiExtResp := &csiext.CreateStorageProtectionGroupResponse{
		LocalProtectionGroupId:          localProtectionGroupID,
		RemoteProtectionGroupId:         remoteProtectionGroupID,
		LocalProtectionGroupAttributes:  localParams,
		RemoteProtectionGroupAttributes: remoteParams,
		Status:                          pgStatus,
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
	_, symID, _, _, _, err := s.parseCsiID(id)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	symID, devID, vol, err := s.GetVolumeByID(ctx, id, pmaxClient)
	if err != nil {
		log.Errorf("GetVolumeByID failed with (%s) for devID (%s)", err.Error(), devID)
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Get the parameters
	params := req.GetParameters()
	localRDFGroup := params[path.Join(s.opts.ReplicationPrefix, LocalRDFGroupParam)]
	remoteSymID := params[path.Join(s.opts.ReplicationPrefix, RemoteSymIDParam)]
	repMode := params[path.Join(s.opts.ReplicationPrefix, ReplicationModeParam)]
	remoteServiceLevel := params[path.Join(s.opts.ReplicationPrefix, RemoteServiceLevelParam)]
	remoteSRPID := params[path.Join(s.opts.ReplicationPrefix, RemoteSRPParam)]
	remoteRDFGroup := params[path.Join(s.opts.ReplicationPrefix, RemoteRDFGroupParam)]

	// get HostIOLimits for the storage group
	hostLimitName := ""
	hostMBsec := ""
	hostIOsec := ""
	hostDynDistribution := ""
	if params[HostLimitName] != "" {
		hostLimitName = params[HostLimitName]
	}
	if params[HostIOLimitMBSec] != "" {
		hostMBsec = params[HostIOLimitMBSec]
	}
	if params[HostIOLimitIOSec] != "" {
		hostIOsec = params[HostIOLimitIOSec]
	}
	if params[DynamicDistribution] != "" {
		hostDynDistribution = params[DynamicDistribution]
	}

	applicationPrefix := ""
	if params[ApplicationPrefixParam] != "" {
		applicationPrefix = params[ApplicationPrefixParam]
	}
	thick := params[ThickVolumesParam]
	// Storage (resource) Pool. Validate it against exist Pools
	err = s.validateStoragePoolID(ctx, remoteSymID, remoteSRPID, pmaxClient)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Errorf(codes.InvalidArgument, "%s", err.Error())
	}

	// Validate Remote SLO
	found := false
	for _, val := range validSLO {
		if remoteServiceLevel == val {
			found = true
		}
	}
	if !found {
		log.Error("An invalid Remote Service Level parameter was specified")
		return nil, status.Errorf(codes.InvalidArgument, "An invalid Remote Service Level parameter was specified")
	}
	// check if localRDFGroup is present in req, else fetch it from volume context
	if len(localRDFGroup) < 1 {
		localRDFGroup = strconv.Itoa(vol.RDFGroupIDList[0].RDFGroupNumber)
	}
	remoteVolumeID, rmRDFG, err := s.GetRemoteVolumeID(ctx, symID, localRDFGroup, devID, pmaxClient)
	if err != nil {
		log.Errorf("GetRemoteVolumeID failed with (%s) for devID (%s)", err.Error(), devID)
		return nil, err
	}
	if len(remoteRDFGroup) < 1 {
		remoteRDFGroup = rmRDFG
	}

	// Check existence of Storage Group and create if necessary on R2.
	var remoteStorageGroupName string
	if applicationPrefix == "" {
		remoteStorageGroupName = fmt.Sprintf("%s-%s-%s-%s-SG", CSIPrefix, s.getClusterPrefix(),
			remoteServiceLevel, remoteSRPID)
	} else {
		remoteStorageGroupName = fmt.Sprintf("%s-%s-%s-%s-%s-SG", CSIPrefix, s.getClusterPrefix(),
			applicationPrefix, remoteServiceLevel, remoteSRPID)
	}
	if hostLimitName != "" {
		remoteStorageGroupName = fmt.Sprintf("%s-%s", remoteStorageGroupName, hostLimitName)
	}
	sg, err := pmaxClient.GetStorageGroup(ctx, remoteSymID, remoteStorageGroupName)
	if err != nil || sg == nil {
		log.Debug(fmt.Sprintf("Unable to find storage group: %s", remoteStorageGroupName))
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
		_, err := pmaxClient.CreateStorageGroup(ctx, remoteSymID, remoteStorageGroupName, remoteSRPID,
			remoteServiceLevel, thick == "true", optionalPayload)
		if err != nil {
			log.Errorf("Error: (%s) creating storage group on R2 (%s): ", err.Error(), remoteSymID)
			return nil, status.Errorf(codes.Internal, "Error creating storage group: %s", err.Error())
		}
	}

	// log all parameters used in CreateRemoteVolume call
	fields := map[string]interface{}{
		"RequestID":            reqID,
		"SymmetrixID":          symID,
		"VolumeName":           vol.VolumeIdentifier,
		"DeviceID":             devID,
		"RemoteDeviceID":       remoteVolumeID,
		"LocalSRDFG":           localRDFGroup,
		"RemoteSRDFG":          remoteRDFGroup,
		"RemoteSymmetrixID":    remoteSymID,
		"ReplicationMode":      repMode,
		"RemoteServiceLevel":   remoteServiceLevel,
		"RemoteSRPID":          remoteSRPID,
		"RemoteStorageGroupID": remoteStorageGroupName,
	}
	log.WithFields(fields).Info("Executing CreateRemoteVolume with following fields")

	remoteVol, err := pmaxClient.GetVolumeByID(ctx, remoteSymID, remoteVolumeID)
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
		remoteVol, err = pmaxClient.RenameVolume(ctx, remoteSymID, remoteVol.VolumeID, vol.VolumeIdentifier)
		errorMsg := ""
		if err != nil {
			errorMsg = err.Error()
		}
		if err != nil || vol == nil {
			return nil, status.Errorf(codes.InvalidArgument, "RenameRemoteVolume: Failed to rename volume %s %s", remoteVolumeID, errorMsg)
		}
	}

	remoteProtectionGroupID := s.GetProtectedStorageGroupID(remoteVol.StorageGroupIDList, remoteRDFGroup+"-"+repMode)
	if remoteProtectionGroupID == "" {
		errorMsg := fmt.Sprintf("CreateRemoteVolume failed with (%s) for devID (%s)", "Failed to find protected remote storage group", remoteVolumeID)
		log.Error(errorMsg)
		return nil, status.Error(codes.InvalidArgument, errorMsg)
	}

	// RESET SRP of Protected SG on remote array
	r2PSG, err := pmaxClient.GetStorageGroup(ctx, remoteSymID, remoteProtectionGroupID)
	if err != nil {
		log.Errorf("Failed to fetch remote PSG details: %s", err.Error())
		return nil, status.Errorf(codes.Internal, "Failed to fetch remote PSG details %s", err.Error())
	}
	if r2PSG.SRP != "" && r2PSG.SRP != "NONE" {
		resetSRPPayload := &types.UpdateStorageGroupPayload{
			EditStorageGroupActionParam: types.EditStorageGroupActionParam{
				EditStorageGroupSRPParam: &types.EditStorageGroupSRPParam{
					SRPID: "NONE",
				},
			},
			ExecutionOption: types.ExecutionOptionSynchronous,
		}
		err = pmaxClient.UpdateStorageGroupS(ctx, remoteSymID, remoteProtectionGroupID, resetSRPPayload)
		if err != nil {
			log.Errorf("Failed to Update Remote SG SRP to NONE: %s", err.Error())
			return nil, status.Errorf(codes.Internal, "Failed to Update Remote SG SRP to NONE: %s", err.Error())
		}
	}

	// Add the remote volume to remote default SG if not present
	if len(remoteVol.StorageGroupIDList) < 2 {
		err = pmaxClient.AddVolumesToStorageGroupS(ctx, remoteSymID, remoteStorageGroupName, true, remoteVolumeID)
		if err != nil {
			log.Error(fmt.Sprintf("Could not add volume in SG on R2: %s: %s", remoteVolumeID, err.Error()))
			return nil, status.Errorf(codes.Internal, "Could not add volume in SG on R2: %s: %s", remoteVolumeID, err.Error())
		}
	}

	// Set the volume context for the response
	volContext := map[string]string{
		CapacityGB:   fmt.Sprintf("%.2f", vol.CapacityGB),
		StorageGroup: remoteStorageGroupName,
		path.Join(s.opts.ReplicationContextPrefix, "LocalProtectionGroupID"): remoteProtectionGroupID,
		path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam):         remoteSymID,
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
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}
	sg, err := pmaxClient.GetProtectedStorageGroup(ctx, symID, protectionGroupID)
	if err != nil {
		if strings.Contains(err.Error(), cannotBeFound) {
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

	err = pmaxClient.DeleteStorageGroup(ctx, symID, protectionGroupID)
	if err != nil {
		log.Errorf(" DeleteStorageGroup failed for (%s) with (%s)", protectionGroupID, err.Error())
		return nil, status.Errorf(codes.Internal, " DeleteStorageGroup failed for (%s) with (%s)", protectionGroupID, err.Error())
	}
	return &csiext.DeleteStorageProtectionGroupResponse{}, nil
}

// DeleteLocalVolume deletes the backend volume on the storage array.
func (s *service) DeleteLocalVolume(ctx context.Context,
	req *csiext.DeleteLocalVolumeRequest,
) (*csiext.DeleteLocalVolumeResponse, error) {
	id := req.GetVolumeHandle()
	volName, symID, devID, _, _, err := s.parseCsiID(id)
	if err != nil {
		// We couldn't comprehend the identifier.
		log.Info("Could not parse CSI VolumeId: " + id)
		return &csiext.DeleteLocalVolumeResponse{}, status.Error(codes.InvalidArgument, "Could not parse CSI VolumeId")
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	volumeID := volumeIDType(id)
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
	err = s.deleteVolume(ctx, reqID, symID, volName, devID, id, pmaxClient)
	if err != nil {
		return nil, err
	}
	return &csiext.DeleteLocalVolumeResponse{}, nil
}

func addMetaData(params map[string]string) map[string][]string {
	// CSI specific metadata header for authorization
	log.Debug("Creating meta data for HTTP header")
	headerMetadata := make(map[string][]string)
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

func (s *service) ControllerGetVolume(ctx context.Context, req *csi.ControllerGetVolumeRequest) (*csi.ControllerGetVolumeResponse, error) {
	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}
	id := req.GetVolumeId()
	volName, symID, devID, _, _, err := s.parseCsiID(id)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}

	fields := map[string]interface{}{
		"RequestID":   reqID,
		"SymmetrixID": symID,
		"VolumeName":  volName,
		"DeviceID":    devID,
	}
	log.WithFields(fields).Info("Executing ControllerGetVolume with following fields")

	symID, devID, vol, err := s.GetVolumeByID(ctx, id, pmaxClient)
	if err != nil {
		log.Errorf("GetVolumeByID failed with (%s) for devID (%s)", err.Error(), devID)
		return &csi.ControllerGetVolumeResponse{
			Volume: nil,
			Status: &csi.ControllerGetVolumeResponse_VolumeStatus{
				VolumeCondition: &csi.VolumeCondition{
					Abnormal: true,
					Message:  fmt.Sprintf("error in getting volume (%s): (%s)", devID, err.Error()),
				},
			},
		}, nil
	}

	volResp := s.getCSIVolume(vol)
	if len(vol.StorageGroupIDList) < 2 {
		return &csi.ControllerGetVolumeResponse{
			Volume: volResp,
			Status: &csi.ControllerGetVolumeResponse_VolumeStatus{
				VolumeCondition: &csi.VolumeCondition{
					Abnormal: false,
					Message:  fmt.Sprintf("volume (%s) is not published", devID),
				},
			},
		}, nil
	}
	// fetch node ID's published
	var nodeIDs []string
	for _, sgID := range vol.StorageGroupIDList {
		if strings.Contains(sgID, "no-srp") {
			// fetch the SG details, and see if masking view is present
			sg, err := pmaxClient.GetStorageGroup(ctx, symID, sgID)
			if err != nil {
				log.Errorf("GetStorageGroup failed with (%s) for devID (%s) sgID (%s)", err.Error(), devID, sgID)
				return &csi.ControllerGetVolumeResponse{
					Volume: nil,
					Status: &csi.ControllerGetVolumeResponse_VolumeStatus{
						VolumeCondition: &csi.VolumeCondition{
							Abnormal: true,
							Message:  fmt.Sprintf("error in getting SG masking view details (%s): (%s)", devID, err.Error()),
						},
					},
				}, nil
			}
			for _, mv := range sg.MaskingView {
				hostID := getHostIDFromMaskingView(mv)
				nodeIDs = append(nodeIDs, hostID)
			}
		}
	}
	return &csi.ControllerGetVolumeResponse{
		Volume: volResp,
		Status: &csi.ControllerGetVolumeResponse_VolumeStatus{
			PublishedNodeIds: nodeIDs,
			VolumeCondition: &csi.VolumeCondition{
				Abnormal: false,
				Message:  "Volume is available",
			},
		},
	}, nil
}

// getHostIDFromMaskingView will return hostID from the given maskingview
// maskingview is CsiMVPrefix-ClusterPrefix-nodeID, eg. csi-mv-ABC-node1
func getHostIDFromMaskingView(maskingView string) string {
	subParts := strings.SplitAfterN(maskingView, "-", 4)
	return subParts[3]
}

func (s *service) ExecuteAction(ctx context.Context, req *csiext.ExecuteActionRequest) (*csiext.ExecuteActionResponse, error) {
	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}
	protectionGroupID := req.GetProtectionGroupId()
	action := req.GetAction().GetActionTypes()
	localParams := req.GetProtectionGroupAttributes()
	symID := localParams[path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam)]
	rDFGroup := localParams[path.Join(s.opts.ReplicationContextPrefix, LocalRDFGroupParam)]
	repMode := localParams[path.Join(s.opts.ReplicationContextPrefix, ReplicationModeParam)]
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// Requires probe
	if err := s.requireProbe(ctx, pmaxClient); err != nil {
		return nil, err
	}
	// log all parameters used in ExecuteAction call
	fields := map[string]interface{}{
		"RequestID":             reqID,
		"SymmetrixID":           symID,
		"ProtectedStorageGroup": protectionGroupID,
		"LocalSRDFG":            rDFGroup,
		"ReplicationMode":       repMode,
		"Action":                action,
	}
	log.WithFields(fields).Info("Executing ExecuteAction with following fields")
	actionType, toLocal := getActionString(action)
	var psg *types.StorageGroupRDFG
	idempotent := false
	if repMode == Async || repMode == Sync {
		switch actionType {
		case FailOver:
			withoutSwap := false
			force := false
			if strings.Contains(action.String(), "WITHOUT_SWAP") {
				withoutSwap = true
			}
			if strings.Contains(action.String(), "UNPLANNED") {
				force = true
			}
			idempotent, psg, err = s.Failover(ctx, symID, protectionGroupID, rDFGroup, pmaxClient, toLocal, force, withoutSwap)
			if err != nil {
				return nil, err
			}
		case FailBack:
			idempotent, psg, err = s.Failback(ctx, symID, protectionGroupID, rDFGroup, pmaxClient, toLocal)
			if err != nil {
				return nil, err
			}
		case Swap:
			idempotent, psg, err = s.Swap(ctx, symID, protectionGroupID, rDFGroup, pmaxClient, toLocal)
			if err != nil {
				return nil, err
			}
		case Reprotect:
			idempotent, psg, err = s.Reprotect(ctx, symID, protectionGroupID, rDFGroup, pmaxClient, toLocal)
			if err != nil {
				return nil, err
			}
		case csiext.ActionTypes_SUSPEND.String():
			err := s.Suspend(ctx, symID, protectionGroupID, rDFGroup, pmaxClient)
			if err != nil {
				return nil, err
			}
		case csiext.ActionTypes_RESUME.String():
			err := s.Resume(ctx, symID, protectionGroupID, rDFGroup, pmaxClient)
			if err != nil {
				return nil, err
			}
		case csiext.ActionTypes_ESTABLISH.String():
			err := s.Establish(ctx, symID, protectionGroupID, rDFGroup, false, pmaxClient)
			if err != nil {
				return nil, err
			}
		default:
			return nil, status.Errorf(codes.Unknown, "The requested action does not match with supported actions")
		}
	} else {
		return nil, fmt.Errorf("%s is not a valid srdf mode for action execution", repMode)
	}
	var pgStatus *csiext.StorageProtectionGroupStatus
	if idempotent && psg != nil {
		pgStatus = getPGStatusFromSGRDFInfo(psg)
	} else {
		// Get the updated status
		pgStatus, err = s.getStorageProtectionGroupStatus(ctx, protectionGroupID, reqID, localParams)
		if err != nil {
			// Ignore the error for now, status should get updated in a future monitoring call
			log.Warning(fmt.Sprintf("failed to get status for SG: %s, error: %s. continuing",
				protectionGroupID, err.Error()))
		}
	}
	resp := &csiext.ExecuteActionResponse{
		Success: true,
		ActionTypes: &csiext.ExecuteActionResponse_Action{
			Action: req.GetAction(),
		},
		Status: pgStatus,
	}
	return resp, nil
}

func getActionString(actionType csiext.ActionTypes) (string, bool) {
	action := ""
	toLocal := false
	if strings.Contains(actionType.String(), "FAILOVER") {
		action = FailOver
	} else if strings.Contains(actionType.String(), "FAILBACK") {
		action = FailBack
	} else if strings.Contains(actionType.String(), "SWAP") {
		action = Swap
	} else if strings.Contains(actionType.String(), "REPROTECT") {
		action = Reprotect
	} else {
		action = actionType.String()
	}
	if strings.Contains(actionType.String(), "LOCAL") {
		toLocal = true
	}
	return action, toLocal
}

func (s *service) getStorageProtectionGroupStatus(ctx context.Context, protectionGroupID,
	reqID string, params map[string]string,
) (*csiext.StorageProtectionGroupStatus, error) {
	symID := params[path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam)]
	pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	// log all parameters used in GetStorageProtectionGroupStatus call
	fields := map[string]interface{}{
		"RequestID":             reqID,
		"SymmetrixID":           symID,
		"ProtectedStorageGroup": protectionGroupID,
	}
	log.WithFields(fields).Info("Executing GetStorageProtectionGroupStatus with following fields")
	_, rDFGno, repMode, err := GetRDFInfoFromSGID(protectionGroupID)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%s", err.Error())
	}
	if repMode == Async || repMode == Sync {
		psg, err := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, protectionGroupID, rDFGno)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "Failed to Get RDF Info for protected SG (%s)", err.Error())
		}
		pgStatus := getPGStatusFromSGRDFInfo(psg)
		return pgStatus, nil
	}
	return nil, status.Errorf(codes.Internal, "expected error - invalid Replication mode")
}

func getPGStatusFromSGRDFInfo(psg *types.StorageGroupRDFG) *csiext.StorageProtectionGroupStatus {
	pgStatus := &csiext.StorageProtectionGroupStatus{}
	rdfState, isR1, mixedPersonalities, mixedStates := getStateAndSRDFPersonality(psg)
	if mixedPersonalities || mixedStates {
		log.Infof("Mixed state (%v) or Mixed RDF personalities (%v) found", mixedStates, mixedPersonalities)
		pgStatus.State = csiext.StorageProtectionGroupStatus_UNKNOWN
		pgStatus.IsSource = false
	} else {
		pgStatus.IsSource = isR1
		switch rdfState {
		case Consistent, Synchronized:
			pgStatus.State = csiext.StorageProtectionGroupStatus_SYNCHRONIZED
		case SyncInProgress:
			pgStatus.State = csiext.StorageProtectionGroupStatus_SYNC_IN_PROGRESS
		case Suspended:
			pgStatus.State = csiext.StorageProtectionGroupStatus_SUSPENDED
		case FailedOver:
			pgStatus.State = csiext.StorageProtectionGroupStatus_FAILEDOVER
		default:
			log.Infof("The status (%s) does not match with known actions", rdfState)
			pgStatus.State = csiext.StorageProtectionGroupStatus_UNKNOWN
		}
	}
	return pgStatus
}

func (s *service) GetStorageProtectionGroupStatus(ctx context.Context, req *csiext.GetStorageProtectionGroupStatusRequest) (*csiext.GetStorageProtectionGroupStatusResponse, error) {
	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}
	protectionGroupID := req.GetProtectionGroupId()
	params := req.GetProtectionGroupAttributes()
	pgStatus, err := s.getStorageProtectionGroupStatus(ctx, protectionGroupID, reqID, params)
	if err != nil {
		return nil, err
	}
	log.Infof("The current state for SG (%s) is (%s).", protectionGroupID, pgStatus.State.String())
	resp := &csiext.GetStorageProtectionGroupStatusResponse{
		Status: pgStatus,
	}
	return resp, err
}
