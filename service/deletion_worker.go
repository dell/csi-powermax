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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix"

	pmax "github.com/dell/gopowermax"
	"github.com/dell/gopowermax/types/v90"
	log "github.com/sirupsen/logrus"
)

// Constants used by deletion worker
const (
	DeletionQueueLength         = 10000
	MaxRequestsPerStep          = 1000
	MaxErrorsStored             = 5
	MaxErrorCount               = 100
	CacheValidTime              = 30 * time.Minute
	MinPollingInterval          = 3 * time.Second
	WaitTillSyncInProgTime      = 20 * time.Second
	initialStep                 = "initialStep"
	cleanupSnapshotStep         = "cleanupSnapshotStep"
	removeVolumesFromSGStep     = "removeVolumesFromSGStep"
	deleteVolumeStep            = "deleteVolumeStep"
	pruneDeletionQueuesStep     = "pruneDeletionQueuesStep"
	deletionStateCleanupSnaps   = "cleanupSnaps"
	deletionStateDisAssociateSG = "disAssociateSG"
	deletionStateDeleteVol      = "deleteVolume"
	deleted                     = "deleted"
	maxedOutState               = "maxedOut"
	FinalError                  = "Final error: Max error count reached, device will be removed from Deletion Queue"
)

// symDeviceID - holds a hexadecimal device id in string as well as the corresponding integer value
type symDeviceID struct {
	DeviceID string
	IntVal   int
}

// deletionRequestStatus - status of the device deletion request
type deletionRequestStatus struct {
	AdditionTime time.Time
	LastUpdate   time.Time
	nErrors      int
	ErrorMsgs    []string
	State        string
}

// deletionRequest - request for deleting a volume
type deletionRequest struct {
	DeviceID     string
	VolumeHandle string
	SymID        string
	errChan      chan error
}

// csiDevice - holds information for the device being deleted
type csiDevice struct {
	SymDeviceID      symDeviceID
	SymID            string
	SymVolumeCache   symVolumeCache
	VolumeIdentifier string
	Status           deletionRequestStatus
}

// symVolumeCache - timed cache which holds volume information from the sym
type symVolumeCache struct {
	volume     *types.Volume
	lastUpdate time.Time
}

// deletionQueue - queue holding the devices being deleted
type deletionQueue struct {
	DeviceList   []*csiDevice
	SymID        string
	DeleteTracks bool
	lock         sync.Mutex
}

// deletionWorker - represents the deletion worker
type deletionWorker struct {
	DeletionQueues      map[string]*deletionQueue
	lock                sync.Mutex
	SymmetrixIDs        []string
	ClusterPrefix       string
	DeletionRequestChan chan deletionRequest
	DeletionQueueChan   chan csiDevice
	State               string
}

func (req *deletionRequest) isValid(clusterPrefix string) error {
	if req.DeviceID == "" {
		return fmt.Errorf("device id can't be empty")
	}
	if req.SymID == "" {
		return fmt.Errorf("sym id can't be empty")
	}
	if !strings.Contains(req.VolumeHandle, clusterPrefix) {
		return fmt.Errorf("device id doesn't contain the cluster prefix")
	}
	if !strings.Contains(req.VolumeHandle, "_DEL") {
		return fmt.Errorf("device has not been marked for deletion")
	}
	return nil
}

func (vol *symVolumeCache) getOrUpdateVolume(symID, volumeID string, pmaxClient pmax.Pmax, forceUpdate bool) (*types.Volume, error) {
	updateCache := false
	if vol.volume == nil {
		updateCache = true
	} else if (time.Since(vol.lastUpdate) > CacheValidTime) || forceUpdate {
		updateCache = true
	}
	if updateCache {
		volume, err := pmaxClient.GetVolumeByID(context.Background(), symID, volumeID)
		if err != nil {
			vol.volume = nil
			return nil, err
		}
		log.Debugf("(Device ID: %s, Sym ID: %s): Successfully refreshed cache\n", volumeID, symID)
		vol.volume = volume
		vol.lastUpdate = time.Now()
	}
	return vol.volume, nil
}

func (device *csiDevice) equals(csiDevice csiDevice) bool {
	return device.SymDeviceID.DeviceID == csiDevice.SymDeviceID.DeviceID
}

func (device *csiDevice) print() string {
	return fmt.Sprintf("(Device ID: %s, SymID: %s)", device.SymDeviceID.DeviceID, device.SymID)
}

func (device *csiDevice) updateStatus(state, errorMsg string) {
	updateTime := time.Now()
	device.Status.LastUpdate = updateTime
	if device.Status.State != state {
		log.Infof("%s: State change from %s to %s\n", device.print(), device.Status.State, state)
	}
	device.Status.State = state
	if errorMsg != "" {
		device.Status.nErrors++
		if len(device.Status.ErrorMsgs) == MaxErrorsStored {
			device.Status.ErrorMsgs = device.Status.ErrorMsgs[1:]
		}
		if device.Status.nErrors >= MaxErrorCount {
			// Push the device to prune state when max error is reached
			device.Status.ErrorMsgs = append(device.Status.ErrorMsgs, FinalError)
			log.Infof("Max Error count reached for: %s", device.print())
			log.Infof("%s: State change from %s to %s\n", device.print(), device.Status.State, maxedOutState)
			device.Status.State = maxedOutState
		} else {
			device.Status.ErrorMsgs = append(device.Status.ErrorMsgs, errorMsg)
			log.Debugf("%s: Current Error: %s, Total Number of errors: %d\n", device.print(), errorMsg, device.Status.nErrors)
		}
	}
}

func (queue *deletionQueue) QueueDeviceForDeletion(device csiDevice) error {
	queue.lock.Lock()
	defer queue.lock.Unlock()
	for _, dev := range queue.DeviceList {
		if dev.equals(device) {
			msg := fmt.Sprintf("%s: found existing entry in deletion queue with volume handle: %s, added at: %v\n",
				dev.print(), dev.VolumeIdentifier, dev.Status.AdditionTime)
			return fmt.Errorf(msg)
		}
	}
	queue.DeviceList = append(queue.DeviceList, &device)
	log.Infof("%s: Added to deletion queue. Initial State: %s\n", device.print(), device.Status.State)
	log.Debugf("%s: Time spent in deletion queue channel: %v\n", device.print(), time.Since(device.Status.LastUpdate))
	return nil
}

// Print - Prints the entire deletion queue
func (queue *deletionQueue) Print() {
	queue.lock.Lock()
	defer queue.lock.Unlock()
	log.Info("Deletion queue")
	queue.print()
}

func (queue *deletionQueue) print() {
	log.Debugf("Length of deletion queue for: %s - %d\n", queue.SymID, len(queue.DeviceList))
	for _, dev := range queue.DeviceList {
		log.Infof("%s: State: %s\n", dev.print(), dev.Status.State)
	}
}

// expectation is that if you know the snapshot id, you also know the generation
// otherwise snapshot should be left blank
func unlinkTarget(tgtVol *types.Volume, snapID, srcDevID, symID string, snapGeneration int64, pmaxClient pmax.Pmax) error {
	if tgtVol != nil {
		isDefined := false
		if snapID == "" {
			snapInfo, err := pmaxClient.GetVolumeSnapInfo(context.Background(), symID, tgtVol.VolumeID)
			if err != nil {
				return fmt.Errorf("failed to find snapshot info for tgt vol. error: %s", err.Error())
			}
			if snapInfo.VolumeSnapshotLink != nil &&
				len(snapInfo.VolumeSnapshotLink) > 0 {
				// There will always be a single link if device is just a target
				if snapInfo.VolumeSnapshotLink[0].Defined {
					isDefined = true
					// Overwrite whatever source device id information was sent
					srcDevID = snapInfo.VolumeSnapshotLink[0].LinkSource
					// get source details
					srcSnapInfo, err := pmaxClient.GetVolumeSnapInfo(context.Background(), symID, srcDevID)
					if err != nil {
						return fmt.Errorf("failed to obtain snapshot info for source device")
					}
					for _, snapSrc := range srcSnapInfo.VolumeSnapshotSource {
						snapID = snapSrc.SnapshotName
						for _, linkedDevice := range snapSrc.LinkedVolumes {
							if linkedDevice.TargetDevice == tgtVol.VolumeID {
								// Found our match
								snapGeneration = snapSrc.Generation
								break
							}
						}
					}
				}
			}
		}
		if !isDefined {
			return fmt.Errorf("can't unlink as link state is not defined. retry after sometime")
		}
		if snapID != "" && srcDevID != "" {
			sourceList := make([]types.VolumeList, 0)
			sourceList = append(sourceList, types.VolumeList{Name: srcDevID})
			tgtList := make([]types.VolumeList, 0)
			tgtList = append(tgtList, types.VolumeList{Name: tgtVol.VolumeID})
			err := pmaxClient.ModifySnapshotS(context.Background(), symID, sourceList, tgtList, snapID,
				"Unlink", "", snapGeneration)
			if err != nil {
				return fmt.Errorf("failed to unlink snapshot. error: %s", err.Error())
			}
			return nil
		}
		return fmt.Errorf("unable to identify snapshot name/src device id")
	}
	return fmt.Errorf("target volume details can't be nil")
}

func unlinkTargetsAndTerminateSnapshot(srcVol *types.Volume, symID string, pmaxClient pmax.Pmax) error {
	// If we got a source device here, it can mean 2 things
	// 1. Device has snapshots which were marked for deletion
	// 2. Device has temporary snapshots
	// deletion_worker will never unlink/terminate any other types of snapshot
	if srcVol != nil {
		snapInfo, err := pmaxClient.GetVolumeSnapInfo(context.Background(), symID, srcVol.VolumeID)
		if err != nil {
			return err
		}
		if len(snapInfo.VolumeSnapshotSource) > 0 {
			sourceSnapSessions := snapInfo.VolumeSnapshotSource
			sort.Slice(sourceSnapSessions[:], func(i, j int) bool {
				return sourceSnapSessions[i].Generation > sourceSnapSessions[j].Generation
			})
			for _, snapSrcInfo := range sourceSnapSessions {
				snapName := snapSrcInfo.SnapshotName
				if strings.Contains(snapName, TempSnap) || strings.HasPrefix(snapName, "DEL") {
					allLinksDefined := true
					tgtVolumes := make([]types.VolumeList, 0)
					for _, lnk := range snapSrcInfo.LinkedVolumes {
						if lnk.Defined {
							tgtVolumes = append(tgtVolumes, types.VolumeList{Name: lnk.TargetDevice})
						} else {
							allLinksDefined = false
						}
					}
					sourceVolumes := make([]types.VolumeList, 0)
					sourceVolumes = append(sourceVolumes, types.VolumeList{Name: srcVol.VolumeID})
					if len(tgtVolumes) != 0 {
						err := pmaxClient.ModifySnapshotS(context.Background(), symID, sourceVolumes, tgtVolumes, snapName,
							"Unlink", "", snapSrcInfo.Generation)
						if err != nil {
							return fmt.Errorf("failed to unlink snapshot. error: %s", err.Error())
						}
						if allLinksDefined {
							// Terminate the snapshot generation
							err = pmaxClient.DeleteSnapshotS(context.Background(), symID, snapName, sourceVolumes, snapSrcInfo.Generation)
							if err != nil {
								return fmt.Errorf("failed to terminate the snapshot. error: %s", err.Error())
							}
						}
					} else {
						// we just need to terminate the snapshot
						// Terminate the snapshot generation
						err = pmaxClient.DeleteSnapshotS(context.Background(), symID, snapName, sourceVolumes, snapSrcInfo.Generation)
						if err != nil {
							return fmt.Errorf("failed to terminate the snapshot. error: %s", err.Error())
						}
					}
				} else {
					log.Debugf("Ignoring snapshot %s as it can't be deleted by the deletion worker\n", snapName)
					continue
				}
			}
		}
	}
	return nil
}

func (queue *deletionQueue) cleanupSnapshots(pmaxClient pmax.Pmax) bool {
	queue.lock.Lock()
	defer queue.lock.Unlock()
	if len(queue.DeviceList) == 0 {
		return false
	}
	count := 0
	for _, device := range queue.DeviceList {
		if count == 5 {
			break
		}
		if device.Status.State == deletionStateCleanupSnaps {
			volumeID := device.SymDeviceID.DeviceID
			// Deletion worker should only process volumes which are
			// 1. Source for temporary snapshots
			// 2. Targets (the links exist because of clone from vol or clone from snap operation)
			symVol, err := device.SymVolumeCache.getOrUpdateVolume(queue.SymID, volumeID, pmaxClient, false)
			if err != nil {
				device.updateStatus(device.Status.State, err.Error())
				return false
			}
			if symVol.SnapTarget {
				if count == 5 {
					continue
				}
				// If device is target (or both target & source), unlink it from source
				err = unlinkTarget(symVol, "", "", queue.SymID, 0, pmaxClient)
				device.updateStatus(device.Status.State, errorMsg(err))
				_, _ = device.SymVolumeCache.getOrUpdateVolume(queue.SymID, volumeID, pmaxClient, true)
				count++
				continue
			} else if symVol.SnapSource {
				if count == 5 {
					continue
				}
				// if device is only source
				err = unlinkTargetsAndTerminateSnapshot(symVol, queue.SymID, pmaxClient)
				device.updateStatus(device.Status.State, errorMsg(err))
				_, _ = device.SymVolumeCache.getOrUpdateVolume(queue.SymID, volumeID, pmaxClient, true)
				count++
				continue
			} else { // device is neither a source or target
				if len(symVol.StorageGroupIDList) == 0 {
					device.updateStatus(deletionStateDeleteVol, "")
				} else {
					log.Warningf("%s: Unexpected error. Moving back volume to disAssociateSG step", device.print())
					device.updateStatus(deletionStateDisAssociateSG, "")
				}
			}
		} else {
			continue
		}
	}
	if count > 0 {
		return true
	}
	return false
}

func (queue *deletionQueue) removeVolumesFromStorageGroup(pmaxClient pmax.Pmax) bool {
	queue.lock.Lock()
	defer queue.lock.Unlock()
	if len(queue.DeviceList) == 0 {
		return false
	}
	sgID := ""
	volumeIDs := make([]string, 0)
	count := 0
	for _, device := range queue.DeviceList {
		// If state is disassociateSG
		if device.Status.State != deletionStateDisAssociateSG {
			continue
		} else {
			// First get the volume from cache
			symVol, err := device.SymVolumeCache.getOrUpdateVolume(queue.SymID, device.SymDeviceID.DeviceID, pmaxClient, false)
			if err != nil {
				device.updateStatus(device.Status.State, err.Error())
				continue
			}
			if len(symVol.StorageGroupIDList) > 0 {
				if sgID == "" {
					// Iterate through the SG list to find a SG which we can process
					for _, storageGroupID := range symVol.StorageGroupIDList {
						sg, err := pmaxClient.GetStorageGroup(context.Background(), queue.SymID, storageGroupID)
						if err != nil {
							// failed to fetch the SG details
							// update error for this device
							device.updateStatus(device.Status.State, errorMsg(err))
							continue
						} else {
							if sg.NumOfMaskingViews > 0 {
								log.Warningf("%s: SG: %s in masking view. Can't proceed with deletion of devices\n",
									device.print(), storageGroupID)
								device.updateStatus(device.Status.State, "device is in masking view, can't delete")
								continue
							}
							sgID = storageGroupID
							break
						}
					}
					if sgID == "" {
						log.Debugf("%s: couldn't find any sg from which this volume could be removed. Proceeding to the next volume\n",
							device.print())
						continue
					}
					volumeIDs = append(volumeIDs, device.SymDeviceID.DeviceID)
					count++
				} else {
					for _, storageGroupID := range symVol.StorageGroupIDList {
						if storageGroupID == sgID {
							volumeIDs = append(volumeIDs, device.SymDeviceID.DeviceID)
							count++
							break
						}
					}
				}
			} else {
				if symVol.SnapSource || symVol.SnapTarget {
					device.updateStatus(deletionStateCleanupSnaps, "")
				} else {
					device.updateStatus(deletionStateDeleteVol, "")
				}
			}
		}
		if count == MaxRequestsPerStep {
			break
		}
	}
	// Remove the volumes from SG
	if len(volumeIDs) > 0 {
		sg, err := pmaxClient.GetStorageGroup(context.Background(), queue.SymID, sgID)
		if err == nil {
			if sg.NumOfMaskingViews > 0 {
				log.Errorf("SG: %s in masking view. Can't proceed with deletion of devices\n", sgID)
				return false
			}
		} else {
			// We failed to get SG details. This could be a transient error
			// Proceed with the removal of volumes
		}
		ns, rdfNo, mode, err := GetRDFInfoFromSGID(sgID)
		if err != nil {
			log.Debugf("GetRDFInfoFromSGID failed for (%s) on symID (%s). Proceeding for RemoveVolumesFromStorageGroup", sgID, queue.SymID)
			// This is the default SG in which all the volumes are replicated
			_, err = pmaxClient.RemoveVolumesFromStorageGroup(context.Background(), queue.SymID, sgID, true, volumeIDs...)
		} else {
			// replicated volumes
			// RemoveVolumesFromProtectedStorageGroup should be done only from r1 type
			psg, err := pmaxClient.GetStorageGroupRDFInfo(context.Background(), queue.SymID, sgID, rdfNo)
			if err != nil {
				log.Errorf("GetStorageGroupRDFInfo failed for (%s) on symID (%s)", sgID, queue.SymID)
				return false
			}
			if psg.VolumeRdfTypes[0] != "R1" {
				log.Debugf("Skipping remove volume from protected SG from R2 type")
				return true
			}
			log.Debugf("LocalSG: (%s), Mode: (%s), RDF No: (%s), Namespace: (%s)", sgID, mode, rdfNo, ns)
			rdfInfo, err := pmaxClient.GetRDFGroup(context.Background(), queue.SymID, rdfNo)
			if err != nil {
				log.Errorf("GetRDFGroup failed for (%s) on symID (%s)", sgID, queue.SymID)
				return false
			}
			if mode == Metro {
				state := psg.States[0]
				if state != Suspended {
					//SUSPEND the protected storage group
					err := suspend(context.Background(), queue.SymID, sgID, rdfNo, pmaxClient)
					if err != nil {
						log.Error(errorMsg(err))
						return false
					}
					time.Sleep(WaitTillSyncInProgTime)
				}
			}
			// build remoteSGID
			remoteSGID := buildProtectionGroupID(ns, strconv.Itoa(rdfInfo.RemoteRdfgNumber), mode)
			_, err = pmaxClient.RemoveVolumesFromProtectedStorageGroup(context.Background(), queue.SymID, sgID, rdfInfo.RemoteSymmetrix, remoteSGID, true, volumeIDs...)
			if mode == Metro {
				// After suspend, Establish the RDF group if it has volumes
				var psg *types.RDFStorageGroup
				psg, err = pmaxClient.GetProtectedStorageGroup(context.Background(), queue.SymID, sgID)
				if err != nil {
					log.Errorf("GetProtectedStorageGroup failed for (%s) on symID (%s)", sgID, queue.SymID)
					return false
				}
				if psg.Rdf {
					err = establish(context.Background(), queue.SymID, sgID, rdfNo, true, pmaxClient)
					time.Sleep(WaitTillSyncInProgTime)
				}
			}
		}

		for _, volumeID := range volumeIDs {
			device := getDevice(volumeID, queue.DeviceList)
			if device != nil {
				symVol, updateErr := device.SymVolumeCache.getOrUpdateVolume(queue.SymID, volumeID, pmaxClient, true)
				if updateErr != nil {
					device.updateStatus(device.Status.State, updateErr.Error())
					continue
				}
				if len(symVol.StorageGroupIDList) == 0 {
					if symVol.SnapTarget || symVol.SnapSource {
						device.updateStatus(deletionStateCleanupSnaps, errorMsg(err))
					} else {
						device.updateStatus(deletionStateDeleteVol, errorMsg(err))
					}
				} else {
					device.updateStatus(device.Status.State, errorMsg(err))
				}
			}
		}
	} else {
		return false
	}
	return true
}

func getDevice(deviceID string, deviceList []*csiDevice) *csiDevice {
	for _, device := range deviceList {
		if device.SymDeviceID.DeviceID == deviceID {
			return device
		}
	}
	return nil
}

func (queue *deletionQueue) deleteVolumes(pmaxClient pmax.Pmax) bool {
	// Go through all the volumes in the queue which are in deletevol state and then form device ranges and delete them
	queue.lock.Lock()
	defer queue.lock.Unlock()
	deviceIDS := make([]symDeviceID, 0)
	if len(queue.DeviceList) == 0 {
		return false
	}
	for _, device := range queue.DeviceList {
		if device.Status.State == deletionStateDeleteVol {
			// Check once more if volume is part of any storage groups
			vol, err := device.SymVolumeCache.getOrUpdateVolume(queue.SymID, device.SymDeviceID.DeviceID, pmaxClient, false)
			if err != nil {
				device.updateStatus(device.Status.State, err.Error())
				continue
			}
			if len(vol.StorageGroupIDList) != 0 {
				log.Errorf("%s: is part of some storage groups\n", device.print())
				device.updateStatus(deletionStateDisAssociateSG, "")
				continue
			}
			// Check one final time if the device being deleted is the same one as the one originally requested
			// This would automatically check if _DEL identifier has been set
			if vol.VolumeIdentifier != device.VolumeIdentifier {
				errorMsg := fmt.Sprintf("%s: volume identifiers don't match(Orig: %s, Req: %s)\n",
					device.print(), device.VolumeIdentifier, vol.VolumeIdentifier)
				log.Error(errorMsg)
				device.updateStatus(deletionStateDeleteVol, errorMsg)
			}
			deviceIDS = append(deviceIDS, device.SymDeviceID)
		}
	}
	if len(deviceIDS) > 0 {
		unsorteddeviceIDS := make([]symDeviceID, len(deviceIDS))
		copy(unsorteddeviceIDS, deviceIDS)
		sort.SliceStable(deviceIDS, func(i, j int) bool {
			return deviceIDS[i].IntVal < deviceIDS[j].IntVal
		})
		devRanges := getDeviceRanges(deviceIDS)
		deviceRangeToBeDeleted := deviceRange{}
		for _, deviceID := range unsorteddeviceIDS {
			for _, devRange := range devRanges {
				if devRange.isDeviceIDInRange(deviceID) {
					deviceRangeToBeDeleted = devRange
					break
				}
			}
		}
		log.Info("Deleting device range: " + deviceRangeToBeDeleted.get())
		err := pmaxClient.DeleteVolume(context.Background(), queue.SymID, deviceRangeToBeDeleted.get())
		if err != nil {
			log.Error(err.Error())
		}
		volumeIDs := deviceRangeToBeDeleted.getDeviceIDs()
		for _, volumeID := range volumeIDs {
			for _, dev := range queue.DeviceList {
				if volumeID == dev.SymDeviceID.DeviceID {
					if err != nil {
						dev.updateStatus(dev.Status.State, err.Error())
					} else {
						dev.updateStatus(deleted, "")
					}
				}
			}
		}
		if err == nil {
			log.Infof("Number of devices deleted: %d\n", len(volumeIDs))
		}
	} else {
		return false
	}
	return true
}

func (worker *deletionWorker) nextStep() {
	switch worker.State {
	case initialStep:
		worker.State = removeVolumesFromSGStep
	case removeVolumesFromSGStep:
		worker.State = cleanupSnapshotStep
	case cleanupSnapshotStep:
		worker.State = deleteVolumeStep
	case deleteVolumeStep:
		worker.State = pruneDeletionQueuesStep
	case pruneDeletionQueuesStep:
		worker.State = removeVolumesFromSGStep
	}
}

func (worker *deletionWorker) pruneDeletionQueues() {
	for _, queue := range worker.DeletionQueues {
		queue.lock.Lock()
		i := 0 // output index
		for _, dev := range queue.DeviceList {
			if dev.Status.State != deleted && dev.Status.State != maxedOutState {
				// copy and increment index
				queue.DeviceList[i] = dev
				i++
			} else {
				log.Infof("%s: removed from deletion queue. Total time spent: %v\n",
					dev.print(), time.Since(dev.Status.AdditionTime))
			}
		}
		for j := i; j < len(queue.DeviceList); j++ {
			queue.DeviceList[j] = nil
		}
		queue.DeviceList = queue.DeviceList[:i]
		queue.lock.Unlock()
	}
}

func (worker *deletionWorker) updateDeletionQueues(duration time.Duration) {
	for afterCh := time.After(duration); ; {
		select {
		case device := <-worker.DeletionQueueChan:
			queue, ok := worker.DeletionQueues[device.SymID]
			if ok {
				err := queue.QueueDeviceForDeletion(device)
				if err != nil {
					log.Errorf("%s: Failed to add device to the deletion queue. Error: %s\n",
						device.print(), err.Error())
				}
			} else {
				log.Errorf("unexpected error - SymID: %s is not managed by the deletion worker\n", device.SymID)
			}
		case <-afterCh:
			return
		}
	}
}

// QueueDeviceForDeletion - Queue a device for deletion
func (worker *deletionWorker) QueueDeviceForDeletion(devID string, volumeIdentifier, symID string) error {
	delRequest := deletionRequest{
		DeviceID:     devID,
		VolumeHandle: volumeIdentifier,
		SymID:        symID,
		errChan:      make(chan error, 1),
	}
	select {
	case worker.DeletionRequestChan <- delRequest:
		err := <-delRequest.errChan
		if err != nil {
			return err
		}
		log.Debugf("(Device ID: %s, SymID: %s): Successfully queued request\n", devID, symID)
	default:
		log.Error("Deletion request queue full. Retry after sometime")
		return fmt.Errorf("deletion request queue full. retry after sometime")
	}
	return nil
}

func (worker *deletionWorker) deletionRequestHandler() {
	log.Info("Starting deletion request handler goroutine")
	for req := range worker.DeletionRequestChan {
		log.Infof("Received deletion request for Device ID: %s, Sym ID: %s\n", req.DeviceID, req.SymID)
		if !isStringInSlice(req.SymID, worker.SymmetrixIDs) {
			req.errChan <- fmt.Errorf("unable to process device deletion request as sym id is not managed by deletion worker")
			continue
		}
		pmaxClient, err := symmetrix.GetPowerMaxClient(req.SymID)
		if err != nil {
			log.Error(err.Error())
			req.errChan <- fmt.Errorf("unable to process device deletion request as sym id is not managed by deletion worker")
			continue
		}
		vol, err := pmaxClient.GetVolumeByID(context.Background(), req.SymID, req.DeviceID)
		if err != nil {
			req.errChan <- err
			continue
		}
		err = req.isValid(worker.ClusterPrefix)
		if err != nil {
			req.errChan <- err
			continue
		}
		deviceID, err := getDeviceID(req.DeviceID)
		if err != nil {
			req.errChan <- err
			continue
		}
		currentTime := time.Now()
		symVolume := symVolumeCache{
			volume:     vol,
			lastUpdate: currentTime,
		}

		initialState := deletionStateDisAssociateSG
		if len(vol.StorageGroupIDList) == 0 {
			if vol.SnapSource || vol.SnapTarget {
				initialState = deletionStateCleanupSnaps
			} else {
				initialState = deletionStateDeleteVol
			}
		}
		device := csiDevice{
			SymDeviceID:      deviceID,
			SymID:            req.SymID,
			SymVolumeCache:   symVolume,
			VolumeIdentifier: req.VolumeHandle,
			Status: deletionRequestStatus{
				AdditionTime: currentTime,
				LastUpdate:   currentTime,
				State:        initialState,
			},
		}
		worker.DeletionQueueChan <- device
		req.errChan <- err
	}
}

func (worker *deletionWorker) deletionWorker() {
	log.Info("Starting deletion worker")
	numOfArrays := len(worker.SymmetrixIDs)
	symIndex := 0
	updated := false
	for {
		worker.nextStep()
		switch worker.State {
		case cleanupSnapshotStep:
			symID := worker.SymmetrixIDs[symIndex]
			pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
			if err != nil {
				log.Error(err.Error())
				continue
			}
			updated = worker.DeletionQueues[symID].cleanupSnapshots(pmaxClient)
			if updated {
				worker.updateDeletionQueues(MinPollingInterval)
			}
		case removeVolumesFromSGStep:
			symID := worker.SymmetrixIDs[symIndex]
			pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
			if err != nil {
				log.Error(err.Error())
				continue
			}
			updated = worker.DeletionQueues[symID].removeVolumesFromStorageGroup(pmaxClient)
			if updated {
				worker.updateDeletionQueues(MinPollingInterval)
			}
		case deleteVolumeStep:
			for i := 0; i < 5; i++ {
				symID := worker.SymmetrixIDs[symIndex]
				pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
				if err != nil {
					log.Error(err.Error())
					continue
				}
				updated := worker.DeletionQueues[symID].deleteVolumes(pmaxClient)
				if updated {
					worker.updateDeletionQueues(time.Millisecond * 500)
				} else {
					break
				}
			}
			worker.updateDeletionQueues(MinPollingInterval)
		case pruneDeletionQueuesStep:
			worker.pruneDeletionQueues()
			if symIndex < numOfArrays-1 {
				symIndex++
			} else {
				symIndex = 0
			}
		}
	}
}

func (worker *deletionWorker) populateDeletionQueue() {
	for _, symID := range worker.SymmetrixIDs {
		log.Infof("Processing symmetrix %s for volumes to be deleted with cluster prefix: %s", symID, worker.ClusterPrefix)
		volDeletePrefix := DeletionPrefix + CSIPrefix + "-" + worker.ClusterPrefix
		log.Infof("Deletion Prefix: " + volDeletePrefix)
		pmaxClient, err := symmetrix.GetPowerMaxClient(symID)
		if err != nil {
			log.Error(err.Error())
			continue
		}
		volList, err := pmaxClient.GetVolumeIDList(context.Background(), symID, volDeletePrefix, true)
		if err != nil {
			log.Errorf("Could not retrieve volume IDs to be deleted. Error: %s", err.Error())
			continue
		} else {
			log.Infof("Total number of volumes found which have been tagged for deletion: %d\n", len(volList))
			if len(volList) > 0 {
				log.Infof("Volumes with the prefix: %s - %v\n", volDeletePrefix, volList)
			}
			for _, id := range volList {
				volume, err := pmaxClient.GetVolumeByID(context.Background(), symID, id)
				if err != nil {
					log.Warningf("Could not retrieve details for volume: %s. Ignoring it", id)
					continue
				} else {
					// Put volume on the queue if appropriate
					if strings.HasPrefix(volume.VolumeIdentifier, volDeletePrefix) {
						err = worker.QueueDeviceForDeletion(id, volume.VolumeIdentifier, symID)
						if err != nil {
							log.Errorf("Error in queuing device for deletion. Error: %s", err.Error())
						}
					} else {
						log.Warningf("(Device ID: %s, SymID: %s): skipping as it is not tagged for deletion\n",
							volume.VolumeID, symID)
					}
				}
			}
		}
	}
	log.Infof("Finished populating devices in the deletion worker queue")
}

// NewDeletionWorker - Creates an instance of the deletion worker
func (s *service) NewDeletionWorker(clusterPrefix string, symIDs []string) {
	if s.deletionWorker == nil {
		delWorker := new(deletionWorker)
		delWorker.DeletionQueueChan = make(chan csiDevice, DeletionQueueLength)
		delWorker.State = initialStep
		delWorker.ClusterPrefix = clusterPrefix
		delWorker.SymmetrixIDs = symIDs
		delWorker.DeletionRequestChan = make(chan deletionRequest, DeletionQueueLength)
		delWorker.DeletionQueues = make(map[string]*deletionQueue, 0)
		for _, symID := range symIDs {
			delWorker.DeletionQueues[symID] = &deletionQueue{
				DeviceList: make([]*csiDevice, 0),
				SymID:      symID,
			}
		}
		log.Infof("Configuring deletion worker with Cluster Prefix: %s, Sym IDs: %v",
			clusterPrefix, symIDs)
		go delWorker.deletionRequestHandler()
		go delWorker.populateDeletionQueue()
		go delWorker.deletionWorker()
		s.deletionWorker = delWorker
	}
}

func errorMsg(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

type deviceRange struct {
	Start symDeviceID
	End   symDeviceID
}

func (devRange *deviceRange) get() string {
	if devRange.Start.IntVal == devRange.End.IntVal {
		return fmt.Sprintf("%s", devRange.Start.DeviceID)
	}
	return fmt.Sprintf("%s-%s", devRange.Start.DeviceID, devRange.End.DeviceID)
}

func (devRange *deviceRange) isDeviceIDInRange(device symDeviceID) bool {
	if device.IntVal == devRange.Start.IntVal {
		return true
	} else if (device.IntVal > devRange.Start.IntVal) && (device.IntVal <= devRange.End.IntVal) {
		return true
	}
	return false
}

func (devRange *deviceRange) getDeviceIDs() []string {
	volumeIDs := make([]string, 0)
	for i := devRange.Start.IntVal; i <= devRange.End.IntVal; i++ {
		volumeIDs = append(volumeIDs, strings.ToUpper(fmt.Sprintf("%05x", i)))
	}
	return volumeIDs
}

func getDeviceRanges(devices []symDeviceID) []deviceRange {
	deviceRanges := make([]deviceRange, 0)
	start := 0
	end := 0
	length := len(devices)
	for i := 0; i < length; i++ {
		if i == 0 {
			end = i
			continue
		}
		if devices[i].IntVal-devices[end].IntVal == 1 {
			end = i
			continue
		}
		if start != end {
			deviceRanges = append(deviceRanges, deviceRange{
				Start: devices[start],
				End:   devices[end],
			})
		} else {
			deviceRanges = append(deviceRanges, deviceRange{
				Start: devices[start],
				End:   devices[start],
			})
		}
		start = i
		end = i
	}
	if start <= length-1 {
		deviceRanges = append(deviceRanges, deviceRange{
			Start: devices[start],
			End:   devices[length-1],
		})
	}
	return deviceRanges
}

func isStringInSlice(str string, slice []string) bool {
	for _, ele := range slice {
		if ele == str {
			return true
		}
	}
	return false
}

// returns a symDeviceID struct containing the hex string and corresponding integer value
func getDeviceID(deviceID string) (symDeviceID, error) {
	trimmedDeviceID := strings.TrimPrefix(deviceID, "0")
	output, err := strconv.ParseInt(trimmedDeviceID, 16, 64)
	if err != nil {
		return symDeviceID{}, err
	}
	return symDeviceID{
		IntVal:   int(output),
		DeviceID: deviceID,
	}, nil
}
