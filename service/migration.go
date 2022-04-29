package service

import (
	"fmt"
	csiext "github.com/dell/dell-csi-extensions/migration"
	"github.com/dell/gopowermax/types/v90"
	log "github.com/sirupsen/logrus"
	"golang.org/x/net/context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"path"
	"time"
)

func (s *service) VolumeMigrate(ctx context.Context, req *csiext.VolumeMigrateRequest) (*csiext.VolumeMigrateResponse, error) {

	volID := req.GetVolumeHandle()

	_, symID, _, _, _, err := s.parseCsiID(volID)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", volID)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", volID)
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

	symID, devID, vol, err := s.GetVolumeByID(ctx, volID, pmaxClient)
	if err != nil {
		log.Errorf("GetVolumeByID failed with (%s) for devID (%s)", err.Error(), devID)
		return nil, err
	}
	// Get the parameters

	params := req.GetScParameters()

	applicationPrefix := ""
	if params[ApplicationPrefixParam] != "" {
		applicationPrefix = params[ApplicationPrefixParam]
	}

	storagePoolID := params[StoragePoolParam]
	err = s.validateStoragePoolID(ctx, symID, storagePoolID, pmaxClient)
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

	migrateType := req.GetType()
	var migrationFunc func(context.Context, map[string]string, string, string, string, string, string, *service, *types.Volume) error

	switch migrateType {
	case csiext.MigrateTypes_UNKNOWN_MIGRATE:
		return nil, status.Errorf(codes.Unknown, "Unknown Migration Type")
	case csiext.MigrateTypes_NON_REPL_TO_REPL:
		migrationFunc = nonReplToRepl
	case csiext.MigrateTypes_REPL_TO_NON_REPL:
		migrationFunc = replToNonRepl
	case csiext.MigrateTypes_VERSION_UPGRADE:
		migrationFunc = versionUpgrade

	}
	if err := migrationFunc(ctx, params, storageGroupName, applicationPrefix, serviceLevel, storagePoolID, symID, s, vol); err != nil {
		return nil, err
	}

	attributes := map[string]string{
		ServiceLevelParam: serviceLevel,
		StoragePoolParam:  storagePoolID,
		path.Join(s.opts.ReplicationContextPrefix, SymmetrixIDParam): symID,
		CapacityGB:   fmt.Sprintf("%.2f", vol.CapacityGB),
		StorageGroup: storageGroupName,
		//Format the time output
		"MigrationTime": time.Now().Format("20060102150405"),
	}
	volume := new(csiext.Volume)
	volume.VolumeId = volID
	volume.FsType = params[FsTypeParam]
	volume.VolumeContext = attributes
	csiVol := s.getCSIVolume(vol)
	volume.CapacityBytes = csiVol.CapacityBytes
	csiResp := &csiext.VolumeMigrateResponse{
		MigratedVolume: volume,
	}

	return csiResp, nil
}

func nonReplToRepl(ctx context.Context, params map[string]string, storageGroupName, applicationPrefix, serviceLevel, storagePoolID, symID string, s *service, vol *types.Volume) error {
	var replicationEnabled string
	var remoteSymID string
	var localRDFGrpNo string
	var remoteRDFGrpNo string
	var repMode string
	var namespace string

	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return status.Error(codes.InvalidArgument, err.Error())
	}

	if params[path.Join(s.opts.ReplicationPrefix, RepEnabledParam)] == "true" {
		replicationEnabled = params[path.Join(s.opts.ReplicationPrefix, RepEnabledParam)]
		// remote symmetrix ID and rdf group name are mandatory params when replication is enabled
		remoteSymID = params[path.Join(s.opts.ReplicationPrefix, RemoteSymIDParam)]
		localRDFGrpNo = params[path.Join(s.opts.ReplicationPrefix, LocalRDFGroupParam)]
		remoteRDFGrpNo = params[path.Join(s.opts.ReplicationPrefix, RemoteRDFGroupParam)]
		repMode = params[path.Join(s.opts.ReplicationPrefix, ReplicationModeParam)]
		namespace = params[CSIPVCNamespace]
		if repMode == Metro {
			log.Errorf("Unsupported Replication Mode: (%s)", repMode)
			return status.Errorf(codes.InvalidArgument, "Unsupported Replication Mode: (%s)", repMode)
		}
		if repMode != Async && repMode != Sync {
			log.Errorf("Unsupported Replication Mode: (%s)", repMode)
			return status.Errorf(codes.InvalidArgument, "Unsupported Replication Mode: (%s)", repMode)
		}
	}
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
	// remoteProtectionGroupID refers to name of Storage Group which has protected remote volumes
	var localProtectionGroupID string
	var remoteProtectionGroupID string
	if replicationEnabled == "true" {
		localProtectionGroupID = buildProtectionGroupID(namespace, localRDFGrpNo, repMode)
		remoteProtectionGroupID = buildProtectionGroupID(namespace, remoteRDFGrpNo, repMode)
	}
	if replicationEnabled == "true" {
		log.Debugf("RDF: Found Rdf enabled")
		// remote storage group name is kept same as local storage group name
		// Check if volume is already added in SG, else add it
		protectedSGID := s.GetProtectedStorageGroupID(vol.StorageGroupIDList, localRDFGrpNo+"-"+repMode)
		if protectedSGID == "" {
			// Volume is not present in Protected Storage Group, Add
			err := pmaxClient.AddVolumesToProtectedStorageGroup(ctx, symID, localProtectionGroupID, remoteSymID, remoteProtectionGroupID, true, vol.VolumeID)
			if err != nil {
				log.Error(fmt.Sprintf("Could not add volume to protected SG: %s: %s", localProtectionGroupID, err.Error()))
				return status.Errorf(codes.Internal, "Could add volume to protected SG: %s: %s", localProtectionGroupID, err.Error())
			}
		}
	}
	return nil
}

func replToNonRepl(ctx context.Context, params map[string]string, storageGroupName, applicationPrefix, serviceLevel, storagePoolID, symID string, s *service, vol *types.Volume) error {
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return status.Error(codes.InvalidArgument, err.Error())
	}

	sgID := ""
	if len(vol.StorageGroupIDList) > 0 {
		for _, storageGroupID := range vol.StorageGroupIDList {
			_, err := pmaxClient.GetStorageGroup(ctx, symID, storageGroupID)
			if err != nil {
				log.Error(fmt.Sprintf("Could not get Storage Group %s: %s", storageGroupID, err.Error()))
				// return status.Error(codes.Internal, err.Error())
			} else {
				sgID = storageGroupID
				break
			}
		}
	}

	ns, rdfNo, mode, err := GetRDFInfoFromSGID(sgID)
	if err != nil {
		log.Debugf("GetRDFInfoFromSGID failed for (%s) on symID (%s).", sgID, symID)
		return status.Error(codes.Internal, err.Error())
	}

	remoteSGID := buildProtectionGroupID(ns, rdfNo, mode)
	rdfInfo, err := pmaxClient.GetRDFGroup(ctx, symID, rdfNo)
	if err != nil {
		if err != nil {
			log.Errorf("GetRDFGroup failed for (%s) on symID (%s)", sgID, symID)
			return status.Error(codes.Internal, err.Error())
		}
	}
	_, err = pmaxClient.RemoveVolumesFromProtectedStorageGroup(ctx, symID, sgID, rdfInfo.RemoteSymmetrix, remoteSGID, true, vol.VolumeID)
	if err != nil {
		log.Error(fmt.Sprintf("Could not remove volume from protected SG: %s: %s", sgID, err.Error()))
		return status.Errorf(codes.Internal, "Could not remove volume from protected SG: %s: %s", sgID, err.Error())
	}

	return nil
}

func versionUpgrade(ctx context.Context, params map[string]string, storageGroupName, applicationPrefix, serviceLevel, storagePoolID, symID string, s *service, vol *types.Volume) error {
	return status.Error(codes.Unimplemented, "Unimplemented")
}
