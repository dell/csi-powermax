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
	"context"
	"fmt"
	"strconv"
	"strings"

	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Supported actions
const (
	Establish     = "Establish"
	Resume        = "Resume"
	Suspend       = "Suspend"
	FailOver      = "Failover"
	Swap          = "Swap"
	FailBack      = "Failback"
	Reprotect     = "Reprotect"
	QueryRemSymID = "remote_symmetrix_id"
	QueryAsync    = "Asynchronous"
	QuerySync     = "Synchronous"
	QueryMetro    = "Active"
)

func getQueryMode(mode string) string {
	switch mode {
	case Async:
		return QueryAsync
	case Sync:
		return QuerySync
	case Metro:
		return QueryMetro
	default:
		return ""
	}
}

// BuildRdfLabel builds an RDF Label using Local and Remote RDFg
// Format: "csi-<clusterPrefix>-<mode> for sync, metro. e.g. csi-ABC-<M,S>"
// <clusterPrefix><Namespace> for async. e.g. ABCnamespc
// There is a limit of 10 char of RDF label
func buildRdfLabel(mode, namespace, clusterPrefix string) string {
	label := fmt.Sprintf("%s%s-%s", csiPrefix, clusterPrefix, string(mode[0]))
	if mode == Async {
		label = fmt.Sprintf("%s%s", clusterPrefix, namespace)
	}
	return label
}

// LocalRDFPortsNotAdded checks if the RDF ports are already added to the create RDF payload
// and return accordingly.
func LocalRDFPortsNotAdded(createRDFPayload *types.RDFGroupCreate, localSymID string, dir string, port int) bool {
	result := false
	localPorts := createRDFPayload.LocalPorts
	if len(localPorts) < 1 {
		return true
	}
	for _, prtDetails := range localPorts {
		if prtDetails.SymmID != localSymID && prtDetails.DirID != dir && prtDetails.PortNum != port {
			result = true
		} else {
			result = false
		}
	}
	return result
}

// GetOrCreateRDFGroup get or creates an RDF group automatically.
// 0. Return if there is already a RDFG pair exist
// 1. Get the Next Free RDF group number for Local and Remote
// 2. Get all RDF Directors which are 'ONLINE' from Local Sym
// 3. Get all 'ONLINE' RDF Ports for each obtained 'ONLINE' Director
// 4. SAN SCAN from each 'ONLINE' RDFDir:Port to see which ports on the Remote site are available
// 5. Choose all Ports associated with the remoteSite Provided
// 6. Use all the details above to create a RDFg
func (s *service) GetOrCreateRDFGroup(ctx context.Context, localSymID string, remoteSymID string, repMode string, namespace string, pmaxClient pmax.Pmax) (string, string, error) {
	createRDFgPayload := new(types.RDFGroupCreate)
	proceedWithCreate := false
	rdfLabel := ""

	// check if there is already a RDFG exist for symmetrix pair and the mode
	rdfLabel = buildRdfLabel(repMode, namespace, s.opts.ClusterPrefix)
	if len(rdfLabel) > 10 {
		return "", "", fmt.Errorf("rdfLabel: %s for mode: %s exceeds 10 char limit, rename the namespace within 7 char or use pre-existing RDFG via storage class", rdfLabel, repMode)
	}
	rDFGList, err := pmaxClient.GetRDFGroupList(ctx, localSymID, types.QueryParams{
		QueryRemSymID: remoteSymID,
	})
	if err != nil {
		log.Errorf("Failed to fetch RDF pre existing group, Error (%s)", err.Error())
		return "", "", err
	}
	for _, rDFGID := range rDFGList.RDFGroupIDs {
		if strings.Compare(rdfLabel, rDFGID.Label) == 0 {
			log.Debugf("found pre existing label for given array pair and RDF mode: %+v", rDFGID)
			rDFG, err := pmaxClient.GetRDFGroupByID(ctx, localSymID, strconv.Itoa(rDFGID.RDFGNumber))
			if err != nil {
				log.Errorf("Failed to fetch RDF pre existing group, Error (%s)", err.Error())
				return "", "", err
			}
			log.Debugf("found pre-existing RDF group with label: %s", rDFGID.Label)
			return strconv.Itoa(rDFG.RdfgNumber), strconv.Itoa(rDFG.RemoteRdfgNumber), nil
		}
	}

	// Create new RDFG pair, no pre-existing pair for symIDs and rep mode
	nextFreeRDFG, err := pmaxClient.GetFreeLocalAndRemoteRDFg(ctx, localSymID, "")
	if err != nil {
		log.Error(fmt.Sprintf("Failed to fetch free RDF groups, Error (%s)", err.Error()))
		return "", "", err
	}
	localRDFG := nextFreeRDFG.LocalRdfGroup[0]
	// Create new RDFG pair, no pre-existing pair for symIDs and rep mode
	nextFreeRDFG, err = pmaxClient.GetFreeLocalAndRemoteRDFg(ctx, remoteSymID, "")
	if err != nil {
		log.Error(fmt.Sprintf("Failed to fetch free RDF groups, Error (%s)", err.Error()))
		return "", "", err
	}
	remoteRDFG := nextFreeRDFG.LocalRdfGroup[0]
	log.Infof("Fetched Local RDFg:(%d), remote RDFg:(%d)", localRDFG, remoteRDFG)

	// We are only bothered about ONLINE RDF dirs, so get only those
	onlineDirList, err := pmaxClient.GetLocalOnlineRDFDirs(ctx, localSymID)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to fetch local ONLINE RDF Directors, Error (%s)", err.Error()))
		return "", "", err
	}

	// For each of the onlineDirs Obtained, get the ONLINE ports
	for _, dirs := range onlineDirList.RdfDirs {
		onlinePortList, err := pmaxClient.GetLocalOnlineRDFPorts(ctx, dirs, localSymID)
		if err != nil {
			log.Errorf("Unable to get Port list for Online RDF Director:%s err: %s", dirs, err.Error())
			// If the Dir is online we have to get the port list otherwise something gone wrong
			return "", "", err
		}
		for _, ports := range onlinePortList.RdfPorts {
			// SAN Scan per ONLINE DIR:PORT is quite time-consuming. Check for timeouts.
			// Scan time also increases if multiple sites are zoned over the same RDF Port
			onlinePortInfo, err := pmaxClient.GetRemoteRDFPortOnSAN(ctx, localSymID, dirs, ports)
			if err != nil {
				log.Errorf("Unable to get Remote Port on SAN for Local RDF port:(%s:%s), err: %s", dirs, ports, err.Error())
				// RDF Dir:Ports were online, yet we didn't get any Remote ports connected on SAN. something is wrong! Exit
				return "", "", err
			}
			// Start Building the Req structure if the SAN SCAN reports a hit on the SID of the remoteSymm
			for _, remArray := range onlinePortInfo.RemotePorts {
				log.Debugf("rem array ports: %+v", remArray)
				if remArray.SymmID == remoteSymID {
					log.Infof("remote array matched symm we provided:%s", remoteSymID)
					// WHen there is a match on the SID, it means from the given local RDFDir:Port
					// combo has been zoned to the remote site. So Get the Local Dir:Port and
					// build the Local RDF list , if its not already present.
					// Build the Array one by one.
					ports, _ := strconv.Atoi(ports)
					LocalRDFDirPortInfo, err := pmaxClient.GetLocalRDFPortDetails(ctx, localSymID, dirs, ports)
					if err != nil {
						log.Errorf("Unable to get Remote Port on SAN for Local RDF port:(%s:%d), err: %s", dirs, ports, err)
						return "", "", err
					}
					log.Infof("checking if dir:%s,port:%d is already added to rdfpayload", dirs, ports)
					if LocalRDFPortsNotAdded(createRDFgPayload, localSymID, dirs, ports) {
						log.Debugf("appending dir:%s,port:%d ", dirs, ports)
						createRDFgPayload.LocalPorts = append(createRDFgPayload.LocalPorts, *LocalRDFDirPortInfo)
					}
					// Add Remote Ports
					createRDFgPayload.RemotePorts = append(createRDFgPayload.RemotePorts, remArray)
				}
			}
		}
		proceedWithCreate = true
	}
	if proceedWithCreate {
		// Add the remaining parameters to the create Call and fire it
		createRDFgPayload.Label = rdfLabel
		createRDFgPayload.LocalRDFNum = localRDFG
		createRDFgPayload.RemoteRDFNum = remoteRDFG

		// Fire the call
		err = pmaxClient.ExecuteCreateRDFGroup(ctx, localSymID, createRDFgPayload)
		if err != nil {
			log.Errorf("Unable to Create RDF Group %s", err.Error())
			return "", "", err
		}
		// successfully created SRDF groups
		return strconv.Itoa(localRDFG), strconv.Itoa(remoteRDFG), nil
	}
	return "", "", err
}

// GetRDFDevicePairInfo returns the RDF informtaion of a volume
func (s *service) GetRDFDevicePairInfo(ctx context.Context, symID, rdfGrpNo, localVolID string, pmaxClient pmax.Pmax) (*types.RDFDevicePair, error) {
	rdfPair, err := pmaxClient.GetRDFDevicePairInfo(ctx, symID, rdfGrpNo, localVolID)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to fetch rdf pair information for (%s) - Error (%s)", localVolID, err.Error()))
		return nil, err
	}
	return rdfPair, nil
}

// ProtectStorageGroup protects a local SG based on the given RDF Information
// This will create a remote storage group, RDF pairs and add the volumes in their respective SG
func (s *service) ProtectStorageGroup(ctx context.Context, symID, remoteSymID, storageGroupName, remoteStorageGroupName, remoteServiceLevel, rdfGrpNo, rdfMode, localVolID, reqID string, bias bool, pmaxClient pmax.Pmax) error {
	lockHandle := fmt.Sprintf("%s%s", storageGroupName, symID)
	lockNum := RequestLock(lockHandle, reqID)
	defer ReleaseLock(lockHandle, reqID, lockNum)
	sg, err := pmaxClient.GetProtectedStorageGroup(ctx, symID, storageGroupName)
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
	rdfg, err := pmaxClient.GetRDFGroupByID(ctx, symID, rdfGrpNo)
	if err != nil {
		log.Errorf("Could not get rdf group (%s) information on symID (%s)", rdfGrpNo, symID)
		return status.Errorf(codes.Internal, "Could not get rdf group (%s) information on symID (%s). Error (%s)", rdfGrpNo, symID, err.Error())
	}
	if rdfg.Async && rdfg.NumDevices > 0 {
		return status.Errorf(codes.Internal, "RDF group (%s) cannot be used for ASYNC, as it already has volume pairing", rdfGrpNo)
	}
	log.Debugf("RDF: rdfg has %d devices ! for vol(%s)", rdfg.NumDevices, localVolID)
	err = s.verifyAndDeleteRemoteStorageGroup(ctx, remoteSymID, remoteStorageGroupName, pmaxClient)
	if err != nil {
		log.Error(fmt.Sprintf("Could not verify remote storage group (%s)", storageGroupName))
		return status.Errorf(codes.Internal, "Could not verify remote storage group (%s) - Error (%s)", storageGroupName, err.Error())
	}
	cr, err := pmaxClient.CreateSGReplica(ctx, symID, remoteSymID, rdfMode, rdfGrpNo, storageGroupName, remoteStorageGroupName, remoteServiceLevel, bias)
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
func (s *service) verifyAndDeleteRemoteStorageGroup(ctx context.Context, remoteSymID, remoteStorageGroupName string, pmaxClient pmax.Pmax) error {
	sg, err := pmaxClient.GetStorageGroup(ctx, remoteSymID, remoteStorageGroupName)
	if err != nil || sg == nil {
		log.Debug("Can not found Remote storage group, proceed")
		return nil
	}
	if sg.NumOfVolumes > 0 {
		log.Errorf("Remote Storage Group (%s) has devices, can not protect SG", remoteStorageGroupName)
		return fmt.Errorf("Remote Storage Group (%s) has devices, can not protect SG", remoteStorageGroupName)
	}
	// remote SG is present with no volumes, proceed to delete
	err = pmaxClient.DeleteStorageGroup(ctx, remoteSymID, remoteStorageGroupName)
	if err != nil {
		log.Errorf("Delete Remote Storage Group (%s) failed (%s)", remoteStorageGroupName, err.Error())
		return fmt.Errorf("Delete Remote Storage Group (%s) failed (%s)", remoteStorageGroupName, err.Error())
	}
	return nil
}

// GetRemoteVolumeID returns a remote volume ID for give local volume
func (s *service) GetRemoteVolumeID(ctx context.Context, symID, rdfGrpNo, localVolID string, pmaxClient pmax.Pmax) (string, string, error) {
	rdfPair, err := s.GetRDFDevicePairInfo(ctx, symID, rdfGrpNo, localVolID, pmaxClient)
	if err != nil {
		return "", "", err
	}
	return rdfPair.RemoteVolumeName, strconv.Itoa(rdfPair.RemoteRdfGroupNumber), nil
}

// VerifyProtectedGroupDirection returns the direction of protected SG
func (s *service) VerifyProtectedGroupDirection(ctx context.Context, symID, localProtectionGroupID, localRdfGrpNo string, pmaxClient pmax.Pmax) error {
	sgRDFInfo, err := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, localProtectionGroupID, localRdfGrpNo)
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
	if compLength < 5 || !strings.Contains(storageGroupID, CsiRepSGPrefix) {
		err = fmt.Errorf("The protected SGID %s is not formed correctly", storageGroupID)
		return
	}
	if compLength > 5 {
		// this is ASYNC/SYNC rep sg, it will have namespace
		namespace = strings.Join(sgComponents[3:compLength-2], "-")
	}
	rDFGno = sgComponents[compLength-2]
	repMode = sgComponents[compLength-1]
	return
}

func isValidStateForFailOver(state string) bool {
	if state == Consistent || state == Synchronized {
		return true
	}
	return false
}

func getStateAndSRDFPersonality(psg *types.StorageGroupRDFG) (string, bool, bool, bool) {
	localPersonality := ""
	mixedPersonalities := false
	for _, rdfType := range psg.VolumeRdfTypes {
		if localPersonality == "" {
			localPersonality = rdfType
		} else {
			if localPersonality != rdfType {
				mixedPersonalities = true
				break
			}
		}
	}
	state := ""
	mixedStates := false
	for _, rdfState := range psg.States {
		if state == "" {
			state = rdfState
		} else {
			if state != rdfState {
				mixedStates = true
				break
			}
		}
	}
	isR1 := false
	if localPersonality == "R1" {
		isR1 = true
	}
	return state, isR1, mixedPersonalities, mixedStates
}

// Failover validates current state of replication & executes 'Failover' action on storage group replication link
func (s *service) Failover(ctx context.Context, symID, sgName, rdfGrpNo string, pmaxClient pmax.Pmax, toLocal,
	unplanned, withoutSwap bool,
) (bool, *types.StorageGroupRDFG, error) {
	psg, err := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, rdfGrpNo)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error())
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.Internal, "%s", errorMsg)
	}
	state, isR1, mixedPersonalities, mixedStates := getStateAndSRDFPersonality(psg)
	if mixedPersonalities {
		errorMsg := fmt.Sprintf("SG Name: %s, state: %s - mixed SRDF personalities. can't perform SRDF operations at SG level",
			sgName, state)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
	}
	if mixedStates && !unplanned {
		errorMsg := fmt.Sprintf("SG Name: %s, states: %v - mixed SRDF states. can't perform planned SRDF operations at SG level",
			sgName, psg.States)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
	}
	if !unplanned {
		if withoutSwap {
			if (isR1 && !toLocal) || (!isR1 && toLocal) {
				// simplest case
				if state == Consistent {
					// Perform the failover
					err := pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, FailOver, sgName, rdfGrpNo, false, true, false)
					if err != nil {
						log.Errorf("Fail over: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
						return false, nil, status.Errorf(codes.Internal, "Fail over: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
					}
					log.Infof("Action (%s) with Swap set to (%v), Unplanned (%v), successful on SG(%s)",
						FailOver, !withoutSwap, unplanned, sgName)
					return false, nil, nil
				} else if state == FailedOver {
					// Idempotent operation, return success
					log.Warningf("SG Name: %s, state: %s already in the desired state", sgName, state)
					return true, psg, nil
				}
				// We try a best effort failover & if it doesn't succeed, then return a failed precondition
				// TODO: Revisit this
				err := pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, FailOver, sgName, rdfGrpNo, unplanned, true, false)
				if err != nil {
					log.Errorf("Fail over: Failed to modify SG (%s) - Error (%s)",
						sgName, err.Error())
					return false, nil, status.Errorf(codes.FailedPrecondition,
						"Unable to perform Fail over for SG Name: %s, state: %s. An attempt failed with", sgName, err.Error())
				}
				log.Infof("Action (%s) with Swap set to (%v), Unplanned (%v), successful on SG(%s)",
					FailOver, !withoutSwap, unplanned, sgName)
				return false, nil, nil
			}
			if (isR1 && !toLocal) || (isR1 && toLocal) {
				// 1. Planned failover without Swap
				// 2. the intended failover site is R1
				// We should fail because target site is already R1 & we can't failover to it
				errorMsg := "Can't perform planned failover without Swap to the target site as it is already R1"
				log.Error(errorMsg)
				return false, nil, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
			}
		} else {
			if (toLocal && isR1) || (!toLocal && !isR1) {
				// failover -Swap to local Sym Or failover -Swap to remote Sym
				// In both these cases, we are already in the desired state
				// There is the odd chance that because of user error, we may get a failover -Swap targeted
				// at a site which is already R1. Since the driver is stateless, we have no way to differentiate
				// For now, we just check the state of replication & return success if in desired state
				// We just check the state of the replication
				if state == Consistent {
					// Already reprotected at site
					// log a warning but don't throw an error
					log.Warningf("SG name: %s, state: %s, volumes already protected at the desired site. Nothing to do here",
						sgName, state)
					return true, psg, nil
				} else if state == Suspended {
					// idempotent call
					log.Warningf("SG name: %s, state: %s, idempotent operation. Nothing to do here", sgName, state)
					return true, psg, nil
				}
				// We don't know what to do here
				errorMsg := fmt.Sprintf("SG name: %s,state: %s. driver unable to determine next SRDF action",
					sgName, state)
				log.Error(errorMsg)
				return false, nil, status.Errorf(codes.Internal, "%s", errorMsg)
			}
			if (toLocal && !isR1) || (!toLocal && isR1) {
				skipFailover := false
				if state == FailedOver {
					skipFailover = true
				}
				if !skipFailover {
					if isValidStateForFailOver(state) {
						// Perform the failover
						err := pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, FailOver, sgName, rdfGrpNo, unplanned, true, false)
						if err != nil {
							errorMsg := fmt.Sprintf("Fail over: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
							log.Error(errorMsg)
							return false, nil, status.Errorf(codes.Internal, "%s", errorMsg)
						}
					} else {
						// return error
						errorMsg := fmt.Sprintf("SG name: %s, state: %s, can't perform planned failover with Swap in this state",
							sgName, state)
						log.Error(errorMsg)
						if state == Suspended || state == Invalid {
							return false, nil, status.Errorf(codes.Aborted, "%s", errorMsg)
						}
						return false, nil, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
					}
				}
				err := pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, Swap, sgName, rdfGrpNo, unplanned, true, false)
				if err != nil {
					errorMsg := fmt.Sprintf("Fail over: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
					log.Error(errorMsg)
					return false, nil, status.Errorf(codes.Internal, "%s", errorMsg)
				}
				log.Infof("Action (%s) with Swap set to (%v), Unplanned (%v), successful on SG(%s)",
					FailOver, !withoutSwap, unplanned, sgName)
				return false, nil, nil
			}
		}
	} else {
		if (isR1 && toLocal) || (!isR1 && !toLocal) {
			// Nothing to do here
			log.Warningf("SG name: %s, state: %s, idempotent operation. Nothing to do here", sgName, state)
			return true, psg, nil
		}
		err = pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, FailOver, sgName, rdfGrpNo, true, true, false)
		if err != nil {
			log.Errorf("Fail over: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
			return false, nil, status.Errorf(codes.Internal, "Fail over: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
		}
		log.Infof("Action (%s) Unplanned (%v), successful on SG(%s)",
			FailOver, unplanned, sgName)
		return false, nil, nil
	}
	return false, nil, nil
}

// Failback validates current state of replication & executes 'Failback' on storage group replication link
func (s *service) Failback(ctx context.Context, symID, sgName, rdfGrpNo string, pmaxClient pmax.Pmax,
	toLocal bool,
) (bool, *types.StorageGroupRDFG, error) {
	psg, err := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, rdfGrpNo)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error())
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.Internal, "%s", errorMsg)
	}
	state, isR1, mixedPersonalities, mixedStates := getStateAndSRDFPersonality(psg)
	if mixedPersonalities {
		errorMsg := fmt.Sprintf("SG Name: %s, state: %s - mixed SRDF personalities. can't perform SRDF operations at SG level",
			sgName, state)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
	}
	if mixedStates {
		errorMsg := fmt.Sprintf("SG Name: %s, states: %v - mixed SRDF states. can't perform planned SRDF operations at SG level",
			sgName, psg.States)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
	}
	if (toLocal && isR1) || (!toLocal && !isR1) {
		// We are trying to do
		// 1. Failback to local sym and it is R1
		// 2. Failback to Remote sym and it is R1
		// Both these scenarios are valid
		if state == Consistent || state == Synchronized {
			// Already in desired state
			log.Warningf("SG name: %s, state: %s, idempotent operation. Nothing to do here", sgName, state)
			return true, psg, nil
		}
		if state != FailedOver {
			// Log a warning and do a best effort failback
			log.Warningf("SG name: %s, state: %s, incorrect state for performing failback as it may fail", sgName, state)
		}
		err := pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, FailBack, sgName, rdfGrpNo, false, true, false)
		if err != nil {
			log.Errorf("Fail back: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
			if state != FailedOver {
				// Return a failed precondition as we were not in a right state to perform fail over anyways
				return false, nil, status.Errorf(codes.FailedPrecondition,
					"Incorrect state to perform failback for SG name: %s, state: %s. Failed with error: %s",
					sgName, state, err.Error())
			}
			return false, nil, status.Errorf(codes.Internal, "Fail back: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
		}
		log.Infof("Action (%s) successful on SG(%s)", FailBack, sgName)
	} else if (!toLocal && isR1) || (toLocal && !isR1) {
		// We are trying to do
		// 1. Failback to remote sym when the remote device is R2
		// 2. Failback to local sym when the local device is R2
		// Both these scenarios are invalid as failback can only be done to R1
		errorMsg := "Can't perform planned failback to the target site as it is R2"
		log.Error(errorMsg)
		return true, psg, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
	}
	return false, nil, nil
}

func (s *service) Reprotect(ctx context.Context, symID, sgName, rdfGrpNo string, pmaxClient pmax.Pmax,
	toLocal bool,
) (bool, *types.StorageGroupRDFG, error) {
	psg, err := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, rdfGrpNo)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error())
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.Internal, "%s", errorMsg)
	}
	state, isR1, mixedPersonalities, mixedStates := getStateAndSRDFPersonality(psg)
	if mixedPersonalities {
		errorMsg := fmt.Sprintf("SG Name: %s, state: %s - mixed SRDF personalities. can't perform SRDF operations at SG level",
			sgName, state)
		log.Error(errorMsg)
		return true, psg, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
	}
	if mixedStates {
		errorMsg := fmt.Sprintf("SG Name: %s, states: %v - mixed SRDF states. can't perform planned SRDF operations at SG level",
			sgName, psg.States)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
	}
	if (toLocal && isR1) || (!toLocal && !isR1) {
		// We want to reprotect at
		// 1. Local sym and it is R1
		// 2. Remote sym and it is R1
		// These are the valid states for running a reprotect
		if state == Consistent || state == Synchronized {
			// Nothing to do, idempotent call
			log.Warningf("SG name: %s, state: %s, idempotent operation. Nothing to do here", sgName, state)
			return true, psg, nil
		}
		if state == Suspended || state == Split {
			log.Infof("SG name: %s, state: %s, Attempting to resume replication", sgName, state)
		} else {
			log.Warningf("SG name: %s, state: %s, incorrect state for performing Reprotect as it may fail",
				sgName, state)
		}
		err := pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, Resume, sgName, rdfGrpNo,
			true, false, false)
		if err != nil {
			log.Errorf("Resume: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
			// Lets proceed and try with an incremental establish and check if that works
			err1 := pmaxClient.ExecuteReplicationActionOnSG(ctx, symID,
				Establish, sgName, rdfGrpNo, true, false, false)
			if err1 != nil {
				log.Errorf("Establish: Failed to modify SG (%s) - Error (%s)", sgName, err1.Error())
				return false, nil, status.Errorf(codes.Internal, "Both Resume & Establish failed for SG %s with errors - %s & %s",
					sgName, err.Error(), err1.Error())
			}
		}
		log.Infof("Reprotect successful on SG(%s)", sgName)
	} else {
		if (toLocal && !isR1) || (!toLocal && isR1) {
			errorMsg := fmt.Sprintf("SG Name: %s, state: %s - Can't reprotect volumes at site which is R2",
				sgName, state)
			log.Error(errorMsg)
			return true, psg, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
		}
	}
	return false, nil, nil
}

func (s *service) Swap(ctx context.Context, symID, sgName, rdfGrpNo string, pmaxClient pmax.Pmax,
	toLocal bool,
) (bool, *types.StorageGroupRDFG, error) {
	psg, err := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, rdfGrpNo)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error())
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.Internal, "%s", errorMsg)
	}
	state, isR1, mixedPersonalities, mixedStates := getStateAndSRDFPersonality(psg)
	if mixedPersonalities {
		errorMsg := fmt.Sprintf("SG Name: %s, state: %s - mixed SRDF personalities. can't perform SRDF operations at SG level",
			sgName, state)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
	}
	if mixedStates {
		errorMsg := fmt.Sprintf("SG Name: %s, states: %v - mixed SRDF states. can't perform planned SRDF operations at SG level",
			sgName, psg.States)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
	}
	if state == Consistent || state == Synchronized {
		errorMsg := fmt.Sprintf("SG Name: %s, states: %v - Incorrect SRDF state to perform a Swap",
			sgName, psg.States)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, "%s", errorMsg)
	}
	if (toLocal && !isR1) || (!toLocal && isR1) {
		// 1. Swap if the local site is R2
		// 2. Swap if the local site is R1
		// Both are valid scenarios
		err = pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, Swap, sgName, rdfGrpNo, false, false, false)
		if err != nil {
			log.Error(fmt.Sprintf("Swap: Failed to modify SG (%s) - Error (%s)", sgName, err.Error()))
			return false, nil, status.Errorf(codes.Internal, "Swap: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
		}
	} else {
		if (toLocal && isR1) || (!toLocal && !isR1) {
			// Swap & R1
			// if state is suspended, then it is an idempotent call
			if state == Suspended {
				log.Warningf("SG name: %s, state: %s, idempotent operation. Nothing to do here", sgName, state)
				return true, psg, nil
			}
			// Any other state, we don't know what to do
			errorMsg := fmt.Sprintf("SG name: %s,state: %s. driver unable to determine next SRDF action",
				sgName, state)
			log.Error(errorMsg)
			return false, nil, status.Errorf(codes.Internal, "%s", errorMsg)
		}
	}
	return false, nil, nil
}

func suspend(ctx context.Context, symID, sgName, rdfGrpNo string, pmaxClient pmax.Pmax) error {
	inDesiredState, err := validateRDFState(ctx, symID, Suspend, sgName, rdfGrpNo, pmaxClient)
	if err != nil {
		return err
	}
	if !inDesiredState {
		err := pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, Suspend, sgName, rdfGrpNo, false, false, false)
		if err != nil {
			log.Error(fmt.Sprintf("Suspend: Failed to modify SG (%s) - Error (%s)", sgName, err.Error()))
			return status.Errorf(codes.Internal, "Suspend: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
		}
		log.Debugf("Action (%s) successful on SG(%s)", Suspend, sgName)
	}
	return nil
}

// Suspend validates current state of replication & executes 'Suspend' on storage group replication link
func (s *service) Suspend(ctx context.Context, symID, sgName, rdfGrpNo string, pmaxClient pmax.Pmax) error {
	return suspend(ctx, symID, sgName, rdfGrpNo, pmaxClient)
}

func establish(ctx context.Context, symID, sgName, rdfGrpNo string, bias bool, pmaxClient pmax.Pmax) error {
	inDesiredState, err := validateRDFState(ctx, symID, Establish, sgName, rdfGrpNo, pmaxClient)
	if err != nil {
		return err
	}
	if !inDesiredState {
		err = pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, Establish, sgName, rdfGrpNo, false, false, bias)
		if err != nil {
			log.Error(fmt.Sprintf("Establish: Failed to modify SG (%s) - Error (%s)", sgName, err.Error()))
			return status.Errorf(codes.Internal, "Establish: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
		}
		log.Debugf("Action (%s) successful on SG(%s)", Establish, sgName)
	}
	return nil
}

// Establish validates current state of replication & executes 'Establish' on storage group replication link
func (s *service) Establish(ctx context.Context, symID, sgName, rdfGrpNo string, bias bool, pmaxClient pmax.Pmax) error {
	return establish(ctx, symID, sgName, rdfGrpNo, bias, pmaxClient)
}

// Resume validates current state of replication & executes 'Resume' on storage group replication link
func (s *service) Resume(ctx context.Context, symID, sgName, rdfGrpNo string, pmaxClient pmax.Pmax) error {
	inDesiredState, err := validateRDFState(ctx, symID, Resume, sgName, rdfGrpNo, pmaxClient)
	if err != nil {
		return err
	}
	if !inDesiredState {
		err := pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, Resume, sgName, rdfGrpNo, false, true, false)
		if err != nil {
			log.Error(fmt.Sprintf("Resume: Failed to modify SG (%s) - Error (%s)", sgName, err.Error()))
			return status.Errorf(codes.Internal, "Resume: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
		}
		log.Debugf("Action (%s) successful on SG(%s)", Resume, sgName)
	}
	return nil
}

// ValidateRDFState checks if the given action is permissible on the protected storage group based on its current state
func validateRDFState(ctx context.Context, symID, action, sgName, rdfGrpNo string, pmaxClient pmax.Pmax) (bool, error) {
	// validate appropriateness of current link state to the action
	psg, err := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, rdfGrpNo)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error()))
		return false, status.Errorf(codes.Internal, "Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error())
	}
	state := psg.States[0]
	switch action {
	case Resume:
		if state == Consistent || state == Synchronized || state == ActiveBias {
			log.Infof("SG (%s) is already in desired state: (%s)", sgName, state)
			return true, nil
		}
	case Establish:
		if state == Consistent || state == Synchronized || state == ActiveBias {
			log.Infof("SG (%s) is already in desired state: (%s)", sgName, state)
			return true, nil
		}
	case Suspend:
		if state == Suspended {
			log.Infof("SG (%s) is already in desired state: (%s)", sgName, state)
			return true, nil
		}
	case FailOver:
		if state == FailedOver {
			log.Infof("SG (%s) is already in desired state: (%s)", sgName, state)
			return true, nil
		}
	case FailBack:
		if state == Consistent {
			log.Infof("SG (%s) is already in desired state: (%s)", sgName, state)
			return true, nil
		}
	}
	return false, nil
}

func (s *service) addVolumesToProtectedStorageGroup(ctx context.Context, reqID, symID, localProtectionGroupID, remoteSymID, remoteProtectionGroupID string, force bool, volID string, pmaxClient pmax.Pmax) error {
	lockHandle := fmt.Sprintf("%s%s", localProtectionGroupID, symID)
	lockNum := RequestLock(lockHandle, reqID)
	defer ReleaseLock(lockHandle, reqID, lockNum)
	err := pmaxClient.AddVolumesToProtectedStorageGroup(ctx, symID, localProtectionGroupID, remoteSymID, remoteProtectionGroupID, force, volID)
	if err != nil {
		log.Error(fmt.Sprintf("Could not add volume in protected SG: %s: %s", volID, err.Error()))
		return status.Errorf(codes.Internal, "Could not add volume in protected SG: %s: %s", volID, err.Error())
	}
	log.Debugf("volume (%s) added to protected SG (%s)", volID, localProtectionGroupID)
	return nil
}

func buildProtectionGroupID(namespace, localRdfGrpNo, repMode string) string {
	var protectionGrpID string
	if repMode == Metro {
		protectionGrpID = CsiRepSGPrefix + localRdfGrpNo + "-" + repMode
	} else {
		protectionGrpID = CsiRepSGPrefix + namespace + "-" + localRdfGrpNo + "-" + repMode
	}
	return protectionGrpID
}
