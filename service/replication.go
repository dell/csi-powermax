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
	"strings"

	pmax "github.com/dell/gopowermax"

	"github.com/dell/gopowermax/types/v90"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Supported actions
const (
	Establish = "Establish"
	Resume    = "Resume"
	Suspend   = "Suspend"
	FailOver  = "Failover"
	Swap      = "Swap"
	FailBack  = "Failback"
	Reprotect = "Reprotect"
)

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
	rdfg, err := pmaxClient.GetRDFGroup(ctx, symID, rdfGrpNo)
	if err != nil {
		log.Errorf("Could not get rdf group (%s) information on symID (%s)", symID, rdfGrpNo)
		return status.Errorf(codes.Internal, "Could not get rdf group (%s) information on symID (%s). Error (%s)", symID, rdfGrpNo, err.Error())
	}
	log.Debugf("RDF: for vol(%s) rdf group info: (%v+)", localVolID, rdfg)
	if rdfg.Async && rdfg.NumDevices > 0 {
		return status.Errorf(codes.Internal, "RDF group (%s) cannot be used for ASYNC, as it already has volume pairing", rdfGrpNo)
	}
	log.Debugf("RDF: rdfg has 0 device ! vol(%s)", localVolID)
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
func (s *service) GetRemoteVolumeID(ctx context.Context, symID, rdfGrpNo, localVolID string, pmaxClient pmax.Pmax) (string, error) {
	rdfPair, err := s.GetRDFDevicePairInfo(ctx, symID, rdfGrpNo, localVolID, pmaxClient)
	if err != nil {
		return "", err
	}
	return rdfPair.RemoteVolumeName, nil
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
	if compLength < 6 || !strings.Contains(storageGroupID, CsiRepSGPrefix) {
		err = fmt.Errorf("The protected SGID %s is not formed correctly", storageGroupID)
		return
	}
	namespace = strings.Join(sgComponents[3:compLength-2], "-")
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
	unplanned, withoutSwap bool) (bool, *types.StorageGroupRDFG, error) {
	psg, err := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, rdfGrpNo)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error())
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.Internal, errorMsg)
	}
	state, isR1, mixedPersonalities, mixedStates := getStateAndSRDFPersonality(psg)
	if mixedPersonalities {
		errorMsg := fmt.Sprintf("SG Name: %s, state: %s - mixed SRDF personalities. can't perform SRDF operations at SG level",
			sgName, state)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, errorMsg)
	}
	if mixedStates && !unplanned {
		errorMsg := fmt.Sprintf("SG Name: %s, states: %v - mixed SRDF states. can't perform planned SRDF operations at SG level",
			sgName, psg.States)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, errorMsg)
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
				} else {
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
			}
			if (isR1 && !toLocal) || (isR1 && toLocal) {
				// 1. Planned failover without Swap
				// 2. the intended failover site is R1
				// We should fail because target site is already R1 & we can't failover to it
				errorMsg := "Can't perform planned failover without Swap to the target site as it is already R1"
				log.Error(errorMsg)
				return false, nil, status.Errorf(codes.FailedPrecondition, errorMsg)
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
				} else {
					// We don't know what to do here
					errorMsg := fmt.Sprintf("SG name: %s,state: %s. driver unable to determine next SRDF action",
						sgName, state)
					log.Error(errorMsg)
					return false, nil, status.Errorf(codes.Internal, errorMsg)
				}
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
							return false, nil, status.Errorf(codes.Internal, errorMsg)
						}
					} else {
						//return error
						errorMsg := fmt.Sprintf("SG name: %s, state: %s, can't perform planned failover with Swap in this state",
							sgName, state)
						log.Error(errorMsg)
						if state == Suspended || state == Invalid {
							return false, nil, status.Errorf(codes.Aborted, errorMsg)
						}
						return false, nil, status.Errorf(codes.FailedPrecondition, errorMsg)
					}
				}
				err := pmaxClient.ExecuteReplicationActionOnSG(ctx, symID, Swap, sgName, rdfGrpNo, unplanned, true, false)
				if err != nil {
					errorMsg := fmt.Sprintf("Fail over: Failed to modify SG (%s) - Error (%s)", sgName, err.Error())
					log.Error(errorMsg)
					return false, nil, status.Errorf(codes.Internal, errorMsg)
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
	toLocal bool) (bool, *types.StorageGroupRDFG, error) {
	psg, err := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, rdfGrpNo)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error())
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.Internal, errorMsg)
	}
	state, isR1, mixedPersonalities, mixedStates := getStateAndSRDFPersonality(psg)
	if mixedPersonalities {
		errorMsg := fmt.Sprintf("SG Name: %s, state: %s - mixed SRDF personalities. can't perform SRDF operations at SG level",
			sgName, state)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, errorMsg)
	}
	if mixedStates {
		errorMsg := fmt.Sprintf("SG Name: %s, states: %v - mixed SRDF states. can't perform planned SRDF operations at SG level",
			sgName, psg.States)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, errorMsg)
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
		return true, psg, status.Errorf(codes.FailedPrecondition, errorMsg)
	}
	return false, nil, nil
}

func (s *service) Reprotect(ctx context.Context, symID, sgName, rdfGrpNo string, pmaxClient pmax.Pmax,
	toLocal bool) (bool, *types.StorageGroupRDFG, error) {
	psg, err := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, rdfGrpNo)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error())
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.Internal, errorMsg)
	}
	state, isR1, mixedPersonalities, mixedStates := getStateAndSRDFPersonality(psg)
	if mixedPersonalities {
		errorMsg := fmt.Sprintf("SG Name: %s, state: %s - mixed SRDF personalities. can't perform SRDF operations at SG level",
			sgName, state)
		log.Error(errorMsg)
		return true, psg, status.Errorf(codes.FailedPrecondition, errorMsg)
	}
	if mixedStates {
		errorMsg := fmt.Sprintf("SG Name: %s, states: %v - mixed SRDF states. can't perform planned SRDF operations at SG level",
			sgName, psg.States)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, errorMsg)
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
			return true, psg, status.Errorf(codes.FailedPrecondition, errorMsg)
		}
	}
	return false, nil, nil
}

func (s *service) Swap(ctx context.Context, symID, sgName, rdfGrpNo string, pmaxClient pmax.Pmax,
	toLocal bool) (bool, *types.StorageGroupRDFG, error) {
	psg, err := pmaxClient.GetStorageGroupRDFInfo(ctx, symID, sgName, rdfGrpNo)
	if err != nil {
		errorMsg := fmt.Sprintf("Failed to fetch replication state for SG (%s) - Error (%s)", sgName, err.Error())
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.Internal, errorMsg)
	}
	state, isR1, mixedPersonalities, mixedStates := getStateAndSRDFPersonality(psg)
	if mixedPersonalities {
		errorMsg := fmt.Sprintf("SG Name: %s, state: %s - mixed SRDF personalities. can't perform SRDF operations at SG level",
			sgName, state)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, errorMsg)
	}
	if mixedStates {
		errorMsg := fmt.Sprintf("SG Name: %s, states: %v - mixed SRDF states. can't perform planned SRDF operations at SG level",
			sgName, psg.States)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, errorMsg)
	}
	if state == Consistent || state == Synchronized {
		errorMsg := fmt.Sprintf("SG Name: %s, states: %v - Incorrect SRDF state to perform a Swap",
			sgName, psg.States)
		log.Error(errorMsg)
		return false, nil, status.Errorf(codes.FailedPrecondition, errorMsg)
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
			return false, nil, status.Errorf(codes.Internal, errorMsg)
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

//Suspend validates current state of replication & executes 'Suspend' on storage group replication link
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
		if state == Consistent {
			log.Infof("SG (%s) is already in desired state: (%s)", sgName, state)
			return true, nil
		}
	case Establish:
		if state == Consistent {
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

func buildProtectionGroupID(namespace, localRdfGrpNo, repMode string) string {
	protectionGrpID := CsiRepSGPrefix + namespace + "-" + localRdfGrpNo + "-" + repMode
	return protectionGrpID
}
