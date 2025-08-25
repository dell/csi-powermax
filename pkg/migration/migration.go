/*
 *
 * Copyright © 2021-2024 Dell Inc. or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package migration

import (
	"context"
	"fmt"
	"strings"

	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// NodePairs contains -  0002D (local): 00031 (remote)
var NodePairs map[string]string

// SGToRemoteVols contains -  csi-ABC-local-sg : [00031, ...]
var SGToRemoteVols map[string][]string

// localSGVolumeList contains - csi-ABC-local-sg: [0002D, ...]
var localSGVolumeList map[string]*types.VolumeIterator

// CacheReset is flag for cache
var CacheReset bool

const (
	// CsiNoSrpSGPrefix to be used as filter
	CsiNoSrpSGPrefix = "csi-no-srp-sg-"
	// CsiVolumePrefix to be used as filter
	CsiVolumePrefix = "csi-"
	// Synchronized to be used for migration state check
	Synchronized = "Synchronized"
	// HostLimits to be used in SG payload
	HostLimits = "hostLimits"
)

// ResetCache resets all the maps being used with migration
func ResetCache() {
	if !CacheReset {
		NodePairs = make(map[string]string)
		SGToRemoteVols = make(map[string][]string)
		localSGVolumeList = make(map[string]*types.VolumeIterator)
		CacheReset = true
		log.Debugf("Migration cache reset done.")
	}
}

// ListContains return true if x is found in a[]
func ListContains(a []string, x string) bool {
	for _, n := range a {
		if x == n {
			return true
		}
	}
	return false
}

func getOrCreateSGMigration(ctx context.Context, symID, remoteSymID, storageGroupID string, pmaxClient pmax.Pmax) (*types.MigrationSession, error) {
	migrationSG, err := pmaxClient.GetStorageGroupMigrationByID(ctx, symID, storageGroupID)
	if err != nil {
		if strings.Contains(err.Error(), "is not in a migration") || migrationSG == nil {
			// not found
			migrationSG, err = pmaxClient.CreateSGMigration(ctx, symID, remoteSymID, storageGroupID)
			if err != nil {
				return nil, err
			}
		} else {
			// generic error, return
			return nil, err
		}
	}
	log.Debugf("Migration session for sg %s: session: %v", storageGroupID, migrationSG)
	return migrationSG, nil
}

// StorageGroupMigration creates migration session for "no-srp" masking view storage groups
// 1. Remove volumes from default SRP SG
// 2. Create migration session for no-srp SG
// 3. Create default SRP storage group on remote array, and maintain a list of volumes to be added.
var StorageGroupMigration = func(ctx context.Context, symID, remoteSymID, clusterPrefix string, pmaxClient pmax.Pmax) (bool, error) {
	// Before running no-srp-sg migrate call, remove the volumes from srp SG
	// for all the SG for this cluster on local sym ID
	localSgList, err := pmaxClient.GetStorageGroupIDList(ctx, symID, CsiVolumePrefix+clusterPrefix, true)
	if err != nil {
		return false, err
	}

	for _, localSGID := range localSgList.StorageGroupIDs {
		volumeIDList, err := pmaxClient.GetVolumesInStorageGroupIterator(ctx, symID, localSGID)
		if err != nil {
			log.Error("Error getting  storage group volume list: " + err.Error())
			return false, status.Errorf(codes.Internal, "Error getting storage group volume list: %s", err.Error())
		}
		// check the no of volumes in SG localSGID, if exist continue, else ignore
		if volumeIDList.Count > 0 {
			// add entry to cache
			localSGVolumeList[localSGID] = volumeIDList
			// Remove the volumes from the srp SG
			for _, vol := range volumeIDList.ResultList.VolumeList {
				_, err := pmaxClient.RemoveVolumesFromStorageGroup(ctx, symID, localSGID, true, vol.VolumeIDs)
				if err != nil {
					log.Errorf("Error removing volumes %s from protected SG %s with error: %s", vol.VolumeIDs, localSGID, err.Error())
				}
			}
		}
	}

	// for no-srp-sg-<CLUSTER> create migration session
	localSgNoSrpList, err := pmaxClient.GetStorageGroupIDList(ctx, symID, CsiNoSrpSGPrefix+clusterPrefix, true)
	if err != nil {
		return false, err
	}
	for _, storageGroupID := range localSgNoSrpList.StorageGroupIDs {
		sg, err := pmaxClient.GetStorageGroup(ctx, symID, storageGroupID)
		if err != nil {
			log.Errorf("Failed to fetch details of SG(%s) - Error (%s)", storageGroupID, err.Error())
			return false, status.Errorf(codes.Internal, "Failed to fetch details of SG(%s) - Error (%s)", storageGroupID, err.Error())
		}
		if sg.NumOfVolumes > 0 {
			// send migrate call to remote side for SG having volumes
			migrationSG, err := getOrCreateSGMigration(ctx, symID, remoteSymID, storageGroupID, pmaxClient)
			if err != nil {
				log.Errorf("Failed to create array migration session for target array (%s) - Error (%s)", remoteSymID, err.Error())
				return false, status.Errorf(codes.Internal, "Failed to create array migration session for target array(%s) - Error (%s)", remoteSymID, err.Error())
			}
			for _, pair := range migrationSG.DevicePairs {
				NodePairs[pair.SrcVolumeName] = pair.TgtVolumeName
			}
		}
	}

	// for all the SG for this cluster on remote sym ID
	remoteSgList, err := pmaxClient.GetStorageGroupIDList(ctx, remoteSymID, CsiVolumePrefix+clusterPrefix, true)
	if err != nil {
		return false, err
	}

	// Creating SRP storage groups on target array
	for _, localSGID := range localSgList.StorageGroupIDs {
		// check if SG is present on remote side or not
		output := ListContains(remoteSgList.StorageGroupIDs, localSGID)
		if !output {
			// get local SG Params
			sg, err := pmaxClient.GetStorageGroup(ctx, symID, localSGID)
			// create SG on remote SG
			hostLimitsParam := &types.SetHostIOLimitsParam{
				HostIOLimitMBSec:    sg.HostIOLimit.HostIOLimitMBSec,
				HostIOLimitIOSec:    sg.HostIOLimit.HostIOLimitIOSec,
				DynamicDistribution: sg.HostIOLimit.DynamicDistribution,
			}
			optionalPayload := make(map[string]interface{})
			optionalPayload[HostLimits] = hostLimitsParam
			if *hostLimitsParam == (types.SetHostIOLimitsParam{}) {
				optionalPayload = nil
			}
			_, err = pmaxClient.CreateStorageGroup(ctx, remoteSymID, localSGID, sg.SRP,
				sg.SLO, true, optionalPayload)
			if err != nil {
				log.Error("Error creating storage group on remote array: " + err.Error())
				return false, status.Errorf(codes.Internal, "Error creating storage group on remote array: %s", err.Error())
			}
		}

		var remoteVolIDs []string
		// check if localSGID had any volumes to be migrated
		if _, ok := localSGVolumeList[localSGID]; ok {
			for _, vol := range localSGVolumeList[localSGID].ResultList.VolumeList {
				if _, ok := NodePairs[vol.VolumeIDs]; ok {
					remoteVolIDs = append(remoteVolIDs, NodePairs[vol.VolumeIDs])
				}
			}
			SGToRemoteVols[localSGID] = remoteVolIDs
		}
	}
	return true, nil
}

// StorageGroupCommit does a "commit" on all the migration session SG
// Returns true if not sessions found, all migration completed
var StorageGroupCommit = func(ctx context.Context, symID, action string, pmaxClient pmax.Pmax) (bool, error) {
	// for all the SG in saved local SG
	mgSGList, err := pmaxClient.GetStorageGroupMigration(ctx, symID)
	if err != nil {
		return false, err
	}
	if len(mgSGList.MigratingNameList) == 0 {
		log.Debug("No storage group in migration, all migration completed!")
		return true, nil
	}
	for _, id := range mgSGList.MigratingNameList {
		mgSG, err := pmaxClient.GetStorageGroupMigrationByID(ctx, symID, id)
		if err != nil {
			return false, status.Errorf(codes.Internal, "error getting Migration session for SG %s on sym %s: %s", id, symID, err.Error())
		}
		if mgSG.State == Synchronized {
			err := pmaxClient.ModifyMigrationSession(ctx, symID, action, mgSG.StorageGroup)
			if err != nil {
				return false, status.Errorf(codes.Internal, "error modifying Migration session for SG %s on sym %s: %s", id, symID, err.Error())
			}
		} else {
			return false, status.Errorf(codes.Internal, "Waiting for SG to be in SYNC state, current state of SG %s is %s", id, mgSG.State)
		}
	}
	return true, nil
}

// AddVolumesToRemoteSG adds remote volumes to default SRP SG on remote array
var AddVolumesToRemoteSG = func(ctx context.Context, remoteSymID string, pmaxClient pmax.Pmax) (bool, error) {
	// add all the volumes in SGtoRemoteVols
	log.Debugf("SGToRemoteVols: %v", SGToRemoteVols)
	if len(SGToRemoteVols) == 0 {
		// This could be due to driver restart, which will make the objects nil, send warning to do manual addition
		log.Debugf("Add remote volumes to default SG manually using U4P")
		return false, nil
	}
	for storageGroup, remoteVols := range SGToRemoteVols {
		if len(remoteVols) > 0 {
			err := pmaxClient.AddVolumesToStorageGroupS(ctx, remoteSymID, storageGroup, true, remoteVols...)
			if err != nil {
				log.Error(fmt.Sprintf("Could not add volume in SG on R2: %s: %s", remoteSymID, err.Error()))
				return false, status.Errorf(codes.Internal, "Could not add volume in SG on R2: %s: %s", remoteSymID, err.Error())
			}
		}
	}
	return true, nil
}

// GetOrCreateMigrationEnvironment creates migration environment between array pair
var GetOrCreateMigrationEnvironment = func(ctx context.Context, localSymID, remoteSymID string, pmaxClient pmax.Pmax) (*types.MigrationEnv, error) {
	var migrationEnv *types.MigrationEnv
	migrationEnv, err := pmaxClient.GetMigrationEnvironment(ctx, localSymID, remoteSymID)
	if err != nil || migrationEnv == nil {
		migrationEnv, err = pmaxClient.CreateMigrationEnvironment(ctx, localSymID, remoteSymID)
		if err != nil {
			return nil, err
		}
	}
	return migrationEnv, err
}
