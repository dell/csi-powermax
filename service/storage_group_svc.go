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
	"sync"
	"time"

	pmax "github.com/dell/gopowermax"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	types "github.com/dell/gopowermax/types/v90"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	storageGroupSvcQueueDepth = 200
)

var (
	maxAddGroupSize                      = 100
	maxRemoveGroupSize                   = 100
	enableBatchGetMaskingViewConnections = true
)

// addVolumeToSGMVReques requests a volume to be added to a SG / MV.
// Arguments:
// tgtStorageGroupID -- ID of the SG the volume is contained in
// tgtMaskingViewID -- the ID of the MV associated with the SG
// hostID -- the ID of the host represented by the MV
// reqID -- CSI request ID
// symID -- symmetrix array ID
// devID -- the device ID of the volume to be removed
// accessMode -- the CSI request AccessMode
// response -- a channel of length 1 containing a struct that contains the error, and possibly the connections for the MV
// when the request is complete indicating the status after it is processed.
type addVolumeToSGMVRequest struct {
	tgtStorageGroupID string
	tgtMaskingViewID  string
	hostID            string
	reqID             string
	symID             string
	clientSymID       string
	devID             string
	accessMode        *csi.VolumeCapability_AccessMode
	respChan          chan addVolumeToSGMVResponse
}

type addVolumeToSGMVResponse struct {
	err         error
	connections []*types.MaskingViewConnection
}

// removeVolumeFromSGMVRequest requests a volume to be removed from a SG / MV.
// Arguments:
// tgtStorageGroupID -- ID of the SG the volume is contained in
// tgtMaskingViewID -- the ID of the MV associated with the SG
// reqID -- CSI request ID
// symID -- symmetrix array ID
// devID -- the device ID of the volume to be removed
// response -- a channel of length 1 containing a struct containing the error if any
// when the request is complete indicating the status after it is processed.
type removeVolumeFromSGMVRequest struct {
	tgtStorageGroupID string
	tgtMaskingViewID  string
	reqID             string
	symID             string
	clientSymID       string
	devID             string
	respChan          chan removeVolumeFromSGMVResponse
}

type removeVolumeFromSGMVResponse struct {
	err error
}

// updateStorageGroupState is a structure that contains state about the adding or removal of
// volumes from SGs. There is one instance of this structure for each key value in
// existence, where the key consists of the concatenation of the symID and storageGroupID.
// In the future this might be generalized to, for example, also include addVolumeToStorageGroup.
type updateStorageGroupState struct {
	// queue of incoming requests to add a single volume to the SG and MV
	addVolumeToSGMVQueue chan *addVolumeToSGMVRequest
	// queue of incoming requests to remove a single volume from the SG and MV
	removeVolumeFromSGMVQueue chan *removeVolumeFromSGMVRequest
	// this channel services as a mutex. To acquire, read from channel of queue depth one. To replease, write to the channel.
	// it is intended this will replace the mutex per StorageGroup (once addVolumeToSG is also implemented.)
	lock chan bool
}

// storageGroupSvc defines the Storage Group Service overall.
type storageGroupSvc struct {
	// A pointer to the overall service, handy for calls for example using the adminClient.
	svc *service
	// A map of keys (consisting of the concatenation of symID and storageGroupID) to updateStorageGroupState structs.
	updateSGStateMap sync.Map
}

// newStorageGroupService returns a new storage group.
func newStorageGroupService(svc *service) *storageGroupSvc {
	sgSvc := &storageGroupSvc{
		svc: svc,
	}
	return sgSvc
}

// getKey returns a key to state for a specific Storage Group
func getKey(symID, tgtStorageGroupID string) string {
	return symID + ":" + tgtStorageGroupID
}

// getUpdateStorageGroupState returns a pointer to the updateStorageGroupState for a particular SG, and the key used for that state
func (g *storageGroupSvc) getUpdateStorageGroupState(symID, tgtStorageGroupID string) (*updateStorageGroupState, string) {
	key := getKey(symID, tgtStorageGroupID)
	// Initialize maps if necessary for a given key
	sgState := updateStorageGroupState{
		addVolumeToSGMVQueue:      make(chan *addVolumeToSGMVRequest, storageGroupSvcQueueDepth),
		removeVolumeFromSGMVQueue: make(chan *removeVolumeFromSGMVRequest, storageGroupSvcQueueDepth),
		lock:                      make(chan bool, 1),
	}
	sgStateInterface, loaded := g.updateSGStateMap.LoadOrStore(key, &sgState)
	sgStateP := sgStateInterface.(*updateStorageGroupState)
	if !loaded {
		sgStateP.lock <- true
	}
	return sgStateP, key
}

// requestAddVolumeToSGMV requests that a volume be added to a specific storage group and masking view.
func (g *storageGroupSvc) requestAddVolumeToSGMV(ctx context.Context, tgtStorageGroupID, tgtMaskingViewID, hostID, reqID, clientSymID, symID, devID string, accessMode *csi.VolumeCapability_AccessMode) (chan addVolumeToSGMVResponse, chan bool, error) {
	responseChan := make(chan addVolumeToSGMVResponse, 1)
	sgStateP, key := g.getUpdateStorageGroupState(symID, tgtStorageGroupID)

	addVolumeRequest := &addVolumeToSGMVRequest{
		tgtStorageGroupID: tgtStorageGroupID,
		tgtMaskingViewID:  tgtMaskingViewID,
		hostID:            hostID,
		reqID:             reqID,
		symID:             symID,
		clientSymID:       clientSymID,
		devID:             devID,
		accessMode:        accessMode,
		respChan:          responseChan,
	}
	log.Infof("Adding %v to queue: %s", addVolumeRequest, key)
	sgStateP.addVolumeToSGMVQueue <- addVolumeRequest
	return responseChan, sgStateP.lock, nil
}

// requestRemoveVolumeFromSGMV requests that a volume be removed from a specific storage group and masking view.
// outputs include: 1)  a channel which will receive an error or nil when the request is complete,
// 2) a channel that is the lock for the service; if you can receive on the channel you have acquired the lock
// and should call runRemoveVolumesFromSGMV (which will release the lock by writing to the channel when complete)
func (g *storageGroupSvc) requestRemoveVolumeFromSGMV(ctx context.Context, tgtStorageGroupID, tgtMaskingViewID, reqID, clientSymID, symID, devID string) (chan removeVolumeFromSGMVResponse, chan bool, error) {
	responseChan := make(chan removeVolumeFromSGMVResponse, 1)
	sgStateP, key := g.getUpdateStorageGroupState(symID, tgtStorageGroupID)

	removeVolumeRequest := &removeVolumeFromSGMVRequest{
		tgtStorageGroupID: tgtStorageGroupID,
		tgtMaskingViewID:  tgtMaskingViewID,
		reqID:             reqID,
		symID:             symID,
		clientSymID:       clientSymID,
		devID:             devID,
		respChan:          responseChan,
	}
	log.Infof("Adding %v to queue: %s", removeVolumeRequest, key)
	sgStateP.removeVolumeFromSGMVQueue <- removeVolumeRequest
	return responseChan, sgStateP.lock, nil
}

// runAddVolumesToSGMV gets the applicable number of requests, calls addVolumesToSGMV to process them,
// handles any errors by calling handleAddVolumeToSGMV error, and makes sure each volume gets a return response to the error channel
func (g *storageGroupSvc) runAddVolumesToSGMV(ctx context.Context, symID, tgtStorageGroupID string, pmaxClient pmax.Pmax) {
	// Get the key and defer releasing the lock
	key := getKey(symID, tgtStorageGroupID)
	sgStateInterface, ok := g.updateSGStateMap.Load(key)
	if !ok {
		log.Errorf("sgstate missing: %s", key)
		return
	}
	sgState := sgStateInterface.(*updateStorageGroupState)
	defer func(sgState *updateStorageGroupState) {
		sgState.lock <- true
	}(sgState)

	// Pull a request of the queue and process it
	requests, deviceIDs := g.getRequestsForAddVolumesToSG(ctx, sgState)
	if len(requests) > 0 {
		req := requests[0]
		pmaxClient, err := symmetrix.GetPowerMaxClient(req.clientSymID)
		if err != nil {
			log.Error(err.Error())
			for _, req := range requests {
				g.handleAddVolumeToSGMVError(ctx, req)
			}
		}
		err = g.addVolumesToSGMV(ctx, req.reqID, req.symID, req.tgtStorageGroupID, req.tgtMaskingViewID, req.hostID, deviceIDs, pmaxClient)
		if err != nil {
			// In case of error, retry each of the requests individually if necessary.
			for _, req := range requests {
				g.handleAddVolumeToSGMVError(ctx, req)
			}
		} else {
			// Default with no connections
			connections := make([]*types.MaskingViewConnection, 0)

			// There was no error, wait for connections, get the masking view connections,
			// and then Write nil response to all requests errChan to awaken the clients
			if enableBatchGetMaskingViewConnections {
				lockNum := RequestLock(getMVLockKey(req.symID, req.tgtMaskingViewID), req.reqID)
				connections, connectionErr := pmaxClient.GetMaskingViewConnections(ctx, req.symID, req.tgtMaskingViewID, "")
				ReleaseLock(getMVLockKey(req.symID, req.tgtMaskingViewID), req.reqID, lockNum)
				if connectionErr != nil {
					connections = connections[:0]
				}
				log.Infof("GetMaskingViewConnections returned %d connections for MV %s", len(connections), req.tgtMaskingViewID)
			}
			for _, req := range requests {
				response := addVolumeToSGMVResponse{
					err:         nil,
					connections: connections,
				}
				req.respChan <- response
			}
		}
	}
}

// handleAddVolumeToSGMVError handles case where a volume received error and may need to be retried
func (g *storageGroupSvc) handleAddVolumeToSGMVError(ctx context.Context, req *addVolumeToSGMVRequest) {
	pmaxClient, err := symmetrix.GetPowerMaxClient(req.clientSymID)
	if err != nil {
		log.Error(err.Error())
		return
	}
	// Get the current state of the volume
	vol, err := pmaxClient.GetVolumeByID(ctx, req.symID, req.devID)
	if err != nil {
		response := addVolumeToSGMVResponse{
			err: err,
		}
		req.respChan <- response
		return
	}
	// Make sure the volume is still not in the storage group
	found := false
	for _, sgID := range vol.StorageGroupIDList {
		if req.tgtStorageGroupID == sgID {
			found = true
			break
		}
	}
	// Make sure the target masking view also exists
	_, err = pmaxClient.GetMaskingViewByID(ctx, req.symID, req.tgtMaskingViewID)
	// If MV exists and volume is in the SG, then no error
	if err == nil && found {
		// In the Storage Group, done
		log.Infof("Volume %s is already in StorageGroup %s, so addVolumeToSG operation completed", req.devID, req.tgtStorageGroupID)
		response := addVolumeToSGMVResponse{
			err: nil,
		}
		req.respChan <- response
		return
	}
	// Process as a single AddVolumeToSGMV request
	g.processSingleAddVolumeToSGMV(ctx, req)
}

// runRemoveVolumesFromSGMV removes zero, one, or more volumes from a single Storage Group
// that is associated with a masking view
// by coallescing individual requests into a list of deviceIDs that should be removed from
// the SG, and them doing them in one operation. When complete, the service lock is automatically
// released when we return to the caller, there is nothing more for him to do other than
// continue polling until his request completes or it's his turn to run the service again.
func (g *storageGroupSvc) runRemoveVolumesFromSGMV(ctx context.Context, symID, tgtStorageGroupID string) {
	// Get the key and defer releasing the lock
	key := getKey(symID, tgtStorageGroupID)
	// Pull a request off the queue and process it
	sgStateInterface, ok := g.updateSGStateMap.Load(key)
	if !ok {
		log.Errorf("sgstate missing: %s", key)
		return
	}
	sgState := sgStateInterface.(*updateStorageGroupState)
	defer func(sgstate *updateStorageGroupState) {
		// Free up the lock again
		sgState.lock <- true
	}(sgState)

	// Pull a request off the queue and process it
	requests, deviceIDs := g.getRequestsForRemoveVolumesFromSG(ctx, sgState)
	if len(requests) > 0 {
		req := requests[0]
		err := g.removeVolumesFromSGMV(ctx, req.clientSymID, req.tgtStorageGroupID, req.tgtMaskingViewID, req.reqID, req.symID, deviceIDs)
		if err != nil {
			// In case of error, retry each of the requests individually if necessary.
			for _, req := range requests {
				g.handleRemoveVolumeFromSGMVError(ctx, req)
			}
		} else {
			// There was no error, Write nil response to all requests errChan to awaken the clients
			for _, req := range requests {
				response := removeVolumeFromSGMVResponse{
					err: nil,
				}
				req.respChan <- response
			}
		}
	}
}

// handleRemoveVolumeFromSGMVError handles case where a volume received error and may need to be retried
func (g *storageGroupSvc) handleRemoveVolumeFromSGMVError(ctx context.Context, req *removeVolumeFromSGMVRequest) {
	pmaxClient, err := symmetrix.GetPowerMaxClient(req.clientSymID)
	if err != nil {
		log.Error(err.Error())
		return
	}
	// Get the current state of the volume
	vol, err := pmaxClient.GetVolumeByID(ctx, req.symID, req.devID)
	if err != nil {
		response := removeVolumeFromSGMVResponse{
			err: err,
		}
		req.respChan <- response
		return
	}
	// Make sure the volume is still in the storage group
	found := false
	for _, sgID := range vol.StorageGroupIDList {
		if req.tgtStorageGroupID == sgID {
			found = true
			break
		}
	}
	if !found {
		// Not in the SG, we are done
		log.Infof("Volume %s no longer in StorageGroup %s, so removeVolumeFromSG operation completed", req.devID, req.tgtStorageGroupID)
		response := removeVolumeFromSGMVResponse{
			err: nil,
		}
		req.respChan <- response
		return
	}
	// Process as a single RemoveVolumeFromSG request
	g.processSingleRemoveVolumeFromSG(ctx, req)
}

// getRequestsForAddVolumesToSG gets zero to maxAddGroupSize requests for the sgState passed in and returns a request list and devID list
func (g *storageGroupSvc) getRequestsForAddVolumesToSG(ctx context.Context, sgState *updateStorageGroupState) ([]*addVolumeToSGMVRequest, []string) {
	var initialReq *addVolumeToSGMVRequest
	devIDs := make([]string, 0)
	requests := make([]*addVolumeToSGMVRequest, 0)

	for i := 0; i < maxAddGroupSize; {
		select {
		case req := <-sgState.addVolumeToSGMVQueue:
			if initialReq == nil {
				initialReq = req
			}
			if req.symID == initialReq.symID && req.tgtStorageGroupID == initialReq.tgtStorageGroupID && req.tgtMaskingViewID == initialReq.tgtMaskingViewID {
				addToSG, createMV, err := g.addVolumeToSGMVVolumeCheck(ctx, req.clientSymID, req.symID, req.devID, req.tgtStorageGroupID, req.tgtMaskingViewID, req.accessMode)
				if err != nil {
					// received an error
					response := addVolumeToSGMVResponse{
						err: err,
					}
					req.respChan <- response
				} else if addToSG || createMV {
					if addToSG {
						log.Infof("Adding deviceID %s", req.devID)
						devIDs = append(devIDs, req.devID)
					}
					requests = append(requests, req)
					i++
				} else {
					// No, error, but no need to process further
					response := addVolumeToSGMVResponse{
						err: nil,
					}
					req.respChan <- response
				}
			} else {
				g.processSingleAddVolumeToSGMV(ctx, req)
				i = maxAddGroupSize
				break
			}
		default:
			// nothing to process now so just get out
			i = maxAddGroupSize
		}
	}
	return requests, devIDs
}

// getRequestsForRemoveVolumesFromSG gets zero to maxRemoveGroupSize requests for the sgState passed in and returns a request list and devID list
func (g *storageGroupSvc) getRequestsForRemoveVolumesFromSG(ctx context.Context, sgState *updateStorageGroupState) ([]*removeVolumeFromSGMVRequest, []string) {
	var initialReq *removeVolumeFromSGMVRequest
	devIDs := make([]string, 0)
	requests := make([]*removeVolumeFromSGMVRequest, 0)

	for i := 0; i < maxRemoveGroupSize; i++ {
		select {
		case req := <-sgState.removeVolumeFromSGMVQueue:
			if initialReq == nil {
				initialReq = req
			}
			if req.symID == initialReq.symID && req.tgtStorageGroupID == initialReq.tgtStorageGroupID && req.tgtMaskingViewID == initialReq.tgtMaskingViewID {
				log.Infof("Adding deviceID %s", req.devID)
				devIDs = append(devIDs, req.devID)
				requests = append(requests, req)
			} else {
				g.processSingleRemoveVolumeFromSG(ctx, req)
				i = maxRemoveGroupSize
				break
			}
		default:
			// nothing to process now so just get out
			i = maxRemoveGroupSize
			break
		}
	}
	return requests, devIDs
}

func (g *storageGroupSvc) processSingleAddVolumeToSGMV(ctx context.Context, req *addVolumeToSGMVRequest) {
	log.Infof("Single addVolumesToSGMV: %v", req)
	devIDs := make([]string, 1)
	devIDs[0] = req.devID
	pmaxClient, err := symmetrix.GetPowerMaxClient(req.clientSymID)
	if err != nil {
		log.Error(err.Error())
		response := addVolumeToSGMVResponse{
			err: err,
		}
		req.respChan <- response
		return
	}
	err = g.addVolumesToSGMV(ctx, req.reqID, req.symID, req.tgtStorageGroupID, req.tgtMaskingViewID, req.hostID, devIDs, pmaxClient)
	response := addVolumeToSGMVResponse{
		err: err,
	}
	req.respChan <- response
}

// Process a single request, sending the error (if any) back to the caller
func (g *storageGroupSvc) processSingleRemoveVolumeFromSG(ctx context.Context, req *removeVolumeFromSGMVRequest) {
	log.Infof("Single removeVolumesFromSGMV: %v", req)
	devIDs := make([]string, 1)
	devIDs[0] = req.devID
	err := g.removeVolumesFromSGMV(ctx, req.clientSymID, req.tgtStorageGroupID, req.tgtMaskingViewID, req.reqID, req.symID, devIDs)
	response := removeVolumeFromSGMVResponse{
		err: err,
	}
	req.respChan <- response
}

// Check that volume is not already in the storage group associated with the target masking view
// and that the access modes are compatible.
// Returns bool addVolumeToSG, bool addVolumeToMV (i.e. create MV), and error
// Can return true, true, nil -- add volume to SG; false, true, nil -- volume already in SG, check MV; false, false, nil -- done; false, false, err -- Error occured
func (g *storageGroupSvc) addVolumeToSGMVVolumeCheck(ctx context.Context, clientSymID, symID, devID, tgtStorageGroupID, tgtMaskingViewID string, am *csi.VolumeCapability_AccessMode) (bool, bool, error) {
	pmaxClient, err := symmetrix.GetPowerMaxClient(clientSymID)
	if err != nil {
		log.Error(err.Error())
		return false, false, err
	}
	vol, err := pmaxClient.GetVolumeByID(ctx, symID, devID)
	if err != nil {
		log.Error("Error retreiving volume: " + devID + ": " + err.Error())
		return false, false, err
	}
	volumeAlreadyInTargetSG := false
	currentSGIDs := vol.StorageGroupIDList
	for _, sgID := range currentSGIDs {
		if sgID == tgtStorageGroupID {
			volumeAlreadyInTargetSG = true
		}
	}
	maskingViewIDs, storageGroups, err := g.svc.GetMaskingViewAndSGDetails(ctx, symID, currentSGIDs, pmaxClient)
	if err != nil {
		log.Error("GetMaskingViewAndSGDetails Error: " + err.Error())
		return false, false, err
	}
	for _, storageGroup := range storageGroups {
		if storageGroup.StorageGroupID == tgtStorageGroupID {
			if storageGroup.SRP != "" {
				log.Error("Conflicting SG present")
				return false, false, status.Error(codes.Internal, "Conflicting SG present with SRP")
			}
		}
	}
	volumeInTargetMaskingView := false
	// First check if our masking view is in the existsing list
	for _, mvID := range maskingViewIDs {
		if mvID == tgtMaskingViewID {
			volumeInTargetMaskingView = true
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
				return false, false, status.Error(codes.Internal, "No masking views found for volume")
			}
			if len(maskingViewIDs) > 1 {
				log.Error("Volume already part of multiple Masking views")
				return false, false, status.Error(codes.Internal, "Volume already part of multiple Masking views")
			}
			if maskingViewIDs[0] == tgtMaskingViewID {
				// Do a double check for the SG as well
				if volumeAlreadyInTargetSG {
					log.Debug("volume already mapped")
					return false, false, nil
				}
				log.Error(fmt.Sprintf("ControllerPublishVolume: Masking view - %s has conflicting SG", tgtMaskingViewID))
				return false, false, status.Errorf(codes.FailedPrecondition, "Volume in conflicting masking view")
			}
			log.Error(fmt.Sprintf("ControllerPublishVolume: Volume present in a different masking view - %s", maskingViewIDs[0]))
			return false, false, status.Errorf(codes.FailedPrecondition, "volume already present in a different masking view")
		}
	}
	return !volumeAlreadyInTargetSG, !volumeInTargetMaskingView, nil
}

// addVolumesToSGMV adds volumes to StorageGroup and Masking View for a specific symID, tgtStorageGroupID, tgtMaskingViewID, hostID
// devIDs can be of zero length, in which case we'll just ensure the masking view exists
func (g *storageGroupSvc) addVolumesToSGMV(ctx context.Context, reqID, symID, tgtStorageGroupID, tgtMaskingViewID, hostID string, devIDs []string, pmaxClient pmax.Pmax) error {
	var maskingViewExists bool
	var err error
	f := log.Fields{
		"CSIRequestID": reqID,
		"MaskingView":  tgtMaskingViewID,
		"SymmetrixID":  symID,
		"StorageGroup": tgtStorageGroupID,
	}
	log.WithFields(f).Infof("addVolumesToSGMV processing: %s", devIDs)
	tgtMaskingView := &types.MaskingView{}
	// Fetch the masking view
	tgtMaskingView, err = pmaxClient.GetMaskingViewByID(ctx, symID, tgtMaskingViewID)
	if err != nil {
		log.WithFields(f).Debug("Failed to fetch masking view from array")
	} else {
		maskingViewExists = true
	}
	if maskingViewExists {
		// We just need to confirm if all the other entities are in order
		if tgtMaskingView.HostID == hostID && tgtMaskingView.StorageGroupID == tgtStorageGroupID {
			// Add the volumes to masking view, if any to be added
			if len(devIDs) > 0 {
				log.WithFields(f).Info("Calling AddVolumesToStorageGroup")
				start := time.Now()
				err := pmaxClient.AddVolumesToStorageGroupS(ctx, symID, tgtStorageGroupID, true, devIDs...)
				if err != nil {
					log.WithFields(f).Errorf("ControllerPublishVolume: Failed to add devices %s to storage group: %s", devIDs, err)
					return status.Error(codes.Internal, "Failed to add volume to storage group: "+err.Error())
				}
				dur := time.Now().Sub(start)
				log.Infof("AddVolumesToStorageGroup time %d %f", len(devIDs), dur.Seconds())
			}
		} else {
			errormsg := fmt.Sprintf(
				"ControllerPublishVolume: Existing masking view %s with conflicting SG %s or Host %s",
				tgtMaskingViewID, tgtStorageGroupID, hostID)
			log.WithFields(f).Error(errormsg)
			return status.Error(codes.Internal, errormsg)
		}
	} else {
		// We need to create a Masking view
		// First fetch the host details
		log.WithFields(f).Infof("calling GetHostByID: %s", hostID)
		host, err := pmaxClient.GetHostByID(ctx, symID, hostID)
		if err != nil {
			errormsg := fmt.Sprintf(
				"ControllerPublishVolume: Failed to fetch host details for host %s on %s with error - %s", hostID, symID, err.Error())
			log.WithFields(f).Error(errormsg)
			return status.Error(codes.NotFound, errormsg)
		}
		// Fetch or create a Port Group
		log.WithFields(f).Info("calling SelectOrCreatePortGroup")
		portGroupID, err := g.svc.SelectOrCreatePortGroup(ctx, symID, host, pmaxClient)
		if err != nil {
			errormsg := fmt.Sprintf(
				"ControllerPublishVolume: Failed to select/create PG for host %s on %s with error - %s", hostID, symID, err.Error())
			log.WithFields(f).Error(errormsg)
			return status.Error(codes.Internal, errormsg)
		}
		log.Debugf("Selected PortGroup: %s", portGroupID)
		// First check if our storage group exists
		log.WithFields(f).Info("calling GetStorageGroup")
		tgtStorageGroup, err := pmaxClient.GetStorageGroup(ctx, symID, tgtStorageGroupID)
		if err == nil {
			// Check if this SG is not managed by FAST
			if tgtStorageGroup.SRP != "" {
				log.WithFields(f).Error(fmt.Sprintf("ControllerPublishVolume: Storage group - %s exists with same name but with conflicting params", tgtStorageGroupID))
				return status.Error(codes.Internal, "Storage group exists with same name but with conflicting params")
			}
		} else {
			// Attempt to create SG
			log.WithFields(f).Info("calling CreateStorageGroup")
			tgtStorageGroup, err = pmaxClient.CreateStorageGroup(ctx, symID, tgtStorageGroupID, "None", "", false)
			if err != nil {
				log.WithFields(f).Errorf("ControllerPublishVolume: Failed to create storage group: %s", err)
				return status.Error(codes.Internal, "Failed to create storage group")
			}
		}
		// Add the volumes to storage group
		if len(devIDs) > 0 {
			log.WithFields(f).Info("calling AddVolumesToStorageGroup")
			start := time.Now()
			err = pmaxClient.AddVolumesToStorageGroup(ctx, symID, tgtStorageGroupID, true, devIDs...)
			if err != nil {
				log.WithFields(f).Errorf("ControllerPublishVolume: Failed to add device - %s to storage group", devIDs)
				return status.Error(codes.Internal, "Failed to add volume to storage group: "+err.Error())
			}
			dur := time.Now().Sub(start)
			log.WithFields(f).Infof("AddVolumesToStorageGroup time %d %f", len(devIDs), dur.Seconds())
		}
		_, err = pmaxClient.CreateMaskingView(ctx, symID, tgtMaskingViewID, tgtStorageGroupID, hostID, true, portGroupID)
		if err != nil {
			log.WithFields(f).Error(fmt.Sprintf("ControllerPublishVolume: Failed to create masking view - %s", tgtMaskingViewID))
			return status.Error(codes.Internal, "Failed to create masking view: "+err.Error())
		}
	}
	return nil
}

func (g *storageGroupSvc) removeVolumesFromSGMV(ctx context.Context, clientSymID, tgtStorageGroupID, tgtMaskingViewID, reqID, symID string, devIDs []string) error {
	var err error
	pmaxClient, err := symmetrix.GetPowerMaxClient(clientSymID)
	if err != nil {
		log.Error(err.Error())
		return err
	}
	f := log.Fields{
		"CSIRequestID": reqID,
		"MaskingView":  tgtMaskingViewID,
		"SymmetrixID":  symID,
		"StorageGroup": tgtStorageGroupID,
	}
	log.WithFields(f).Infof("removeVolumesFromSGMV processing %s", devIDs)

	tempStorageGroupList := []string{tgtStorageGroupID}
	maskingViewIDs, storageGroups, err := g.svc.GetMaskingViewAndSGDetails(ctx, symID, tempStorageGroupList, pmaxClient)
	if err != nil {
		return err
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
		log.WithFields(f).Debug("volume already unpublished")
		return nil
	}
	maskingViewDeleted := false
	//First check if these are the only volumes in the SG
	if storageGroups[0].NumOfVolumes == len(devIDs) {
		// We need to delete the MV first
		err = pmaxClient.DeleteMaskingView(ctx, symID, tgtMaskingViewID)
		if err != nil {
			log.WithFields(f).Errorf("removeVolumesFromSGMV: Failed to delete masking view Volumes: %s, status %s", devIDs, err.Error())
			return status.Errorf(codes.Internal,
				"Failed to delete masking view (Array: %s, MaskingView: %s) status %s",
				symID, tgtMaskingViewID, err.Error())
		}
		maskingViewDeleted = true
	}
	// Remove the volume from SG
	start := time.Now()
	_, err = pmaxClient.RemoveVolumesFromStorageGroup(ctx, symID, tgtStorageGroupID, true, devIDs...)
	if err != nil {
		log.Errorf("Failed to remove volume from SG (Volumes: %s, Array: %s, SG: %s) status %s", devIDs, symID, tgtStorageGroupID, err.Error())
		return status.Errorf(codes.Internal,
			"removeVolumesFromSGMV: Failed to remove volume from SG (Volumes: %s, SG: %s) status %s",
			devIDs, tgtStorageGroupID, err.Error())
	}
	dur := time.Now().Sub(start)
	log.Infof("RemoveVolumesFromStorageGroup time %d %f", len(devIDs), dur.Seconds())
	// If MV was deleted, then delete the SG as well
	if maskingViewDeleted {
		log.WithFields(f).Info("Deleting storage group")
		err = pmaxClient.DeleteStorageGroup(ctx, symID, tgtStorageGroupID)
		if err != nil {
			// We can just log a warning and continue
			log.WithFields(f).Infof("removeVolumesFromSGMV: Failed to delete storage group %s", tgtStorageGroupID)
		}
	}
	return nil
}
