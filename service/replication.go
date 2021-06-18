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
	"fmt"
	"strings"

	pmax "github.com/dell/gopowermax"

	"github.com/dell/gopowermax/types/v90"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// GetRDFDevicePairInfo returns the RDF informtaion of a volume
func (s *service) GetRDFDevicePairInfo(symID, rdfGrpNo, localVolID string, pmaxClient pmax.Pmax) (*types.RDFDevicePair, error) {
	rdfPair, err := pmaxClient.GetRDFDevicePairInfo(symID, rdfGrpNo, localVolID)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to fetch rdf pair information for (%s) - Error (%s)", localVolID, err.Error()))
		return nil, status.Errorf(codes.Internal, "Failed to fetch rdf pair information for (%s) - Error (%s)", localVolID, err.Error())
	}
	return rdfPair, nil
}

// ProtectStorageGroup protects a local SG based on the given RDF Information
// This will create a remote storage group, RDF pairs and add the volumes in their respective SG
func (s *service) ProtectStorageGroup(symID, remoteSymID, storageGroupName, remoteStorageGroupName, remoteServiceLevel, rdfGrpNo, rdfMode, localVolID, reqID string, pmaxClient pmax.Pmax) error {
	lockHandle := fmt.Sprintf("%s%s", storageGroupName, symID)
	lockNum := RequestLock(lockHandle, reqID)
	defer ReleaseLock(lockHandle, reqID, lockNum)
	sg, err := pmaxClient.GetProtectedStorageGroup(symID, storageGroupName)
	if err != nil {
		log.Errorf("ProtectStorageGroup: GetProtectedStorageGroup failed for (%s) SG: (%s)", symID, storageGroupName)
		return status.Errorf(codes.Internal, "ProtectStorageGroup: GetProtectedStorageGroup failed for (%s) SG: (%s). Error (%s)", symID, storageGroupName, err.Error())
	}
	if sg.Rdf == true {
		// SG is already protected, return
		// SG can be protected by any previous thread
		return nil
	}
	// Proceed to Protect the SG
	rdfg, err := pmaxClient.GetRDFGroup(symID, rdfGrpNo)
	if err != nil {
		log.Errorf("Could not get rdf group (%s) information on symID (%s)", symID, rdfGrpNo)
		return status.Errorf(codes.Internal, "Could not get rdf group (%s) information on symID (%s). Error (%s)", symID, rdfGrpNo, err.Error())
	}
	log.Debugf("RDF: for vol(%s) rdf group info: (%v+)", localVolID, rdfg)
	if rdfg.Async && rdfg.NumDevices > 0 {
		return status.Errorf(codes.Internal, "RDF group (%s) cannot be used for ASYNC, as it already has volume pairing", rdfGrpNo)
	}
	log.Debugf("RDF: rdfg has 0 device ! vol(%s)", localVolID)
	err = s.verifyAndDeleteRemoteStorageGroup(remoteSymID, remoteStorageGroupName, pmaxClient)
	if err != nil {
		log.Error(fmt.Sprintf("Could not verify remote storage group (%s)", storageGroupName))
		return status.Errorf(codes.Internal, "Could not verify remote storage group (%s) - Error (%s)", storageGroupName, err.Error())
	}
	cr, err := pmaxClient.CreateSGReplica(symID, remoteSymID, rdfMode, rdfGrpNo, storageGroupName, remoteStorageGroupName, remoteServiceLevel)
	if err != nil {
		log.Error(fmt.Sprintf("Could not create storage group replica for (%s)", storageGroupName))
		return status.Errorf(codes.Internal, "Could not create storage group replica for (%s) - Error (%s)", storageGroupName, err.Error())
	}
	log.Debugf("RDF: replica created: (%v+) for vol(%s)", cr, localVolID)
	return nil
}

// verifyAndDeleteRemoteStorageGroup verifies the existence of remote storage group.
// As CreateSGReplica needs that no storage group should be present on remote sym.
// So we delete the remote SG only if it has zero volumes.
func (s *service) verifyAndDeleteRemoteStorageGroup(remoteSymID, remoteStorageGroupName string, pmaxClient pmax.Pmax) error {
	sg, err := pmaxClient.GetStorageGroup(remoteSymID, remoteStorageGroupName)
	if err != nil || sg == nil {
		log.Debug("Can not found Remote storage group, proceed")
		return nil
	}
	if sg.NumOfVolumes > 0 {
		log.Errorf("Remote Storage Group (%s) has devices, can not protect SG", remoteStorageGroupName)
		return fmt.Errorf("Remote Storage Group (%s) has devices, can not protect SG", remoteStorageGroupName)
	}
	// remote SG is present with no volumes, proceed to delete
	err = pmaxClient.DeleteStorageGroup(remoteSymID, remoteStorageGroupName)
	if err != nil {
		log.Errorf("Delete Remote Storage Group (%s) failed (%s)", remoteStorageGroupName, err.Error())
		return fmt.Errorf("Delete Remote Storage Group (%s) failed (%s)", remoteStorageGroupName, err.Error())
	}
	return nil
}

// GetRemoteVolumeID returns a remote volume ID for give local volume
func (s *service) GetRemoteVolumeID(symID, rdfGrpNo, localVolID string, pmaxClient pmax.Pmax) (string, error) {
	rdfPair, err := s.GetRDFDevicePairInfo(symID, rdfGrpNo, localVolID, pmaxClient)
	if err != nil {
		return "", err
	}
	return rdfPair.RemoteVolumeName, nil
}

// VerifyProtectedGroupDirection returns the direction of protected SG
func (s *service) VerifyProtectedGroupDirection(symID, localProtectionGroupID, localRdfGrpNo string, pmaxClient pmax.Pmax) error {
	sgRDFInfo, err := pmaxClient.GetStorageGroupRDFInfo(symID, localProtectionGroupID, localRdfGrpNo)
	if err != nil {
		log.Errorf("GetStorageGroupRDFInfo failed:(%s)", err.Error())
		return status.Errorf(codes.Internal, "GetStorageGroupRDFInfo failed:(%s)", err.Error())
	}
	if sgRDFInfo.VolumeRdfTypes[0] == "R1" {
		return nil
	}
	log.Errorf("VerifyProtectedGroupDirection failed:(%s) does not contains R1 volumes", localProtectionGroupID)
	return status.Errorf(codes.Internal, "VerifyProtectedGroupDirection failed:(%s) does not contains R1 volumes", localProtectionGroupID)
}

// GetRDFInfoFromSGID returns namespace , RDFG number and replication mode
func GetRDFInfoFromSGID(storageGroupID string) (namespace string, rDFGno string, repMode string, err error) {
	sgComponents := strings.Split(storageGroupID, "-")

	compLength := len(sgComponents)
	if compLength < 7 && !strings.Contains(storageGroupID, CsiNoSrpSGPrefix) {
		err = fmt.Errorf("The protected SGID %s is not formed correctly", storageGroupID)
		return
	}
	namespace = strings.Join(sgComponents[4:compLength-2], "-")
	rDFGno = sgComponents[compLength-2]
	repMode = sgComponents[compLength-1]
	return
}

/*
// Failover validates current state of replication & executes failover action on storage group replication link
func (s *service) Failover(symID, sgName, rdfGrpNo string) error {
	// validate appropriateness of current link state to Failover
}

// Failback validates current state of replication & executes failback on storage group
// replication link
func (s *service) Failback(symID, sgName, rdfGrpNo string) error {
	// validate appropriateness of current link state to Failback
}
*/

// Suspend validates current state of replication & executes Suspend on storage group replication link
//func (s *service) Suspend(symID, sgName, rdfGrpNo string) error {
//	err := s.ValidateRDFState(symID, "Suspend", sgName, rdfGrpNo)
//	if err != nil {
//		return err
//	}
//	err = pmaxClient.ExecuteReplicationActionOnSG(symID, "Suspend", sgName, rdfGrpNo, false, true)
//	if err != nil {
//		log.Error(fmt.Sprintf("Suspend: Failed to modify SG (%s) - Error (%s)", sgName, err.Error()))
//		return status.Errorf(codes.Internal, "Suspend: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
//	}
//	return nil
//}

// Resume validates current state of replication & executes Resume on storage group replication link
//func (s *service) Resume(symID, sgName, rdfGrpNo string) error {
//	err := s.ValidateRDFState(symID, "Resume", sgName, rdfGrpNo)
//	if err != nil {
//		return err
//	}
//	err = pmaxClient.ExecuteReplicationActionOnSG(symID, "Resume", sgName, rdfGrpNo, false, true)
//	if err != nil {
//		log.Error(fmt.Sprintf("Resume: Failed to modify SG (%s) - Error (%s)", sgName, err.Error()))
//		return status.Errorf(codes.Internal, "Resume: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
//	}
//	return nil
//

// ValidateRDFState checks if the given action is permissible on the protected storage group based on its current state
//func (s *service) ValidateRDFState(symID, action, sgName, rdfGrpNo string) error {
//	// validate appropriateness of current link state to the action
//	_, err := pmaxClient.GetStorageGroupRDFInfo(symID, rdfGrpNo, sgName)
//	if err != nil {
//		log.Error(fmt.Sprintf("Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error()))
//		return status.Errorf(codes.Internal, "Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error())
//	}
//	//TODO: Validate current state to perform specific action
//	return nil
//}
