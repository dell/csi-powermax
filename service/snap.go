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
	"container/heap"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	pmax "github.com/dell/gopowermax"

	types "github.com/dell/gopowermax/types/v90"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The follow constants are for internal use within the pmax library.
const (
	TempSnap       = "CSI_TEMP_SNAP"
	Defined        = "Defined"
	Link           = "Link"
	Unlink         = "Unlink"
	Rename         = "Rename"
	MaxUnlinkCount = 5
)

var cleanupStarted = false
var licenseCached = false
var symmRepCapabilities *types.SymReplicationCapabilities
var mutex sync.Mutex
var snapCleaner *snapCleanupWorker

// SnapSession is an intermediate structure to share session info
type SnapSession struct {
	Source     string
	Name       string
	Generation int64
	Expired    bool
	Target     []types.SnapTarget
}

type snapCleanupWorker struct {
	PollingInterval time.Duration
	Mutex           sync.Mutex
	Queue           snapCleanupQueue
	MaxRetries      int
}

// snapCleanupRequest holds information required for clean up action
type snapCleanupRequest struct {
	symmetrixID string
	snapshotID  string
	volumeID    string
	requestID   string
	retries     int
}

type snapCleanupQueue []*snapCleanupRequest

func (q snapCleanupQueue) Len() int {
	return len(q)
}

// Less compares two elements in the queue, return true if the ith is less than the jth
func (q snapCleanupQueue) Less(i, j int) bool {
	// Return dummy to make the impl happy
	return false
}

// Swap swaps two elements in the queue and updates their index.
func (q snapCleanupQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
}

// Push puts a new element at the end of the queue.
func (q *snapCleanupQueue) Push(x interface{}) {
	req := x.(*snapCleanupRequest)
	n := len(*q)
	*q = append(*q, req)
	heap.Fix(q, n)
}

// Pop takes the takes the element off the top end of the queue.
func (q *snapCleanupQueue) Pop() interface{} {
	old := *q
	n := len(old)
	req := old[n-1]
	*q = old[0 : n-1]
	return *req
}

func (req *snapCleanupRequest) fields() map[string]interface{} {
	fields := map[string]interface{}{
		"SymmetrixID": req.symmetrixID,
		"SnapshotID":  req.snapshotID,
		"VolumeID":    req.volumeID,
		"RequestID":   req.requestID,
	}
	return fields
}
func (scw *snapCleanupWorker) getQueueLen() int {
	scw.Mutex.Lock()
	defer scw.Mutex.Unlock()
	return len(scw.Queue)
}
func (scw *snapCleanupWorker) requestCleanup(req *snapCleanupRequest) {
	fields := req.fields()
	scw.Mutex.Lock()
	defer scw.Mutex.Unlock()
	for i := range scw.Queue {
		if scw.Queue[i].snapshotID == req.snapshotID && scw.Queue[i].symmetrixID == req.symmetrixID {
			// Found!
			log.Warningf("Snapshot ID: %s already present in the deletion queue", req.snapshotID)
			return
		}
	}
	heap.Push(&scw.Queue, req)
	log.WithFields(fields).Debug("Queued for Deletion")
}

func (scw *snapCleanupWorker) queueForRetry(req *snapCleanupRequest) {
	scw.Mutex.Lock()
	defer scw.Mutex.Unlock()
	heap.Push(&scw.Queue, req)
}

// removeItem removes the top priority item so it can be worked on
func (scw *snapCleanupWorker) removeItem() *snapCleanupRequest {
	scw.Mutex.Lock()
	defer scw.Mutex.Unlock()
	if len(scw.Queue) == 0 {
		return nil
	}
	reqx := heap.Pop(&scw.Queue)
	req := reqx.(snapCleanupRequest)
	return &req
}

//UnlinkTargets unlinks all the target devices from the snapshot
func (s *service) UnlinkTargets(ctx context.Context, symID, srcDevID string, pmaxClient pmax.Pmax) error {
	//Get all the snapshot relation on the volume
	SrcSession, _, err := s.GetSnapSessions(ctx, symID, srcDevID, pmaxClient)
	if err != nil {
		return err
	}
	//unlink the target from snapshot
	if SrcSession != nil {
		err := s.UnlinkSnapshot(ctx, symID, &SrcSession[0], MaxUnlinkCount, pmaxClient)
		if err != nil {
			log.Error("UnlinkSnapshot failed for target session:" + srcDevID)
			return err
		}
	}
	return nil
}

//IsSnapshotLicensed return true if the symmetrix array has
// SnapVX license
func (s *service) IsSnapshotLicensed(ctx context.Context, symID string, pmaxClient pmax.Pmax) (err error) {
	if _, err := pmaxClient.IsAllowedArray(symID); err != nil {
		return err
	}
	mutex.Lock()
	defer mutex.Unlock()
	if licenseCached == false {
		symmRepCapabilities, err = pmaxClient.GetReplicationCapabilities(ctx)
		if err != nil {
			return err
		}
		licenseCached = true
		log.Infof("License information with Powermax %s is cached", symID)
	}
	for i := range symmRepCapabilities.SymmetrixCapability {
		if symmRepCapabilities.SymmetrixCapability[i].SymmetrixID == symID {
			if symmRepCapabilities.SymmetrixCapability[i].SnapVxCapable {
				return nil
			}
			return fmt.Errorf("PowerMax array (%s) doesn't have Snapshot license", symID)
		}
	}
	return fmt.Errorf("PowerMax array (%s) is not being managed by Unisphere", symID)
}

// UnlinkAndTerminate executes cleanup operation on the source/target volume

//deviceID: This can be a source or target device or it can be both.
//	A. If it is only a target, this function will execute Unlink action and returns
//	B. If it a source, it unlinks all targets of the source snapshot and then terminate
//	   the snapshot as specified in snapID or terminates all snapshot if snapID is empty
//	C. If it is self linked snapshot i.e. both a source and a target, it executes both A & B
//	D. If this is a soft deleted device, it sends a delete volume request to deletion worker
//	   after terminating the last snapshot
//snapID: It can be empty to terminate all the snapshots on a source volume or terminates the
//spefified snapshot
func (s *service) UnlinkAndTerminate(ctx context.Context, symID, deviceID, snapID string, pmaxClient pmax.Pmax) error {
	var noOfSnapsOnSrc int
	//Get all the snapshot relation on the volume
	SrcSessions, TgtSession, err := s.GetSnapSessions(ctx, symID, deviceID, pmaxClient)
	if err != nil {
		return err
	}
	//Get number of source sessions on the volume
	noOfSnapsOnSrc = len(SrcSessions)

	if TgtSession == nil && noOfSnapsOnSrc == 0 {
		//This volume is not participating in any snap relationships
		log.Debugf("Couldn't find any snapshot on %s", deviceID)
		return fmt.Errorf("Couldn't find any source or target session for %s", deviceID)
	}

	if TgtSession != nil {
		err = s.UnlinkSnapshot(ctx, symID, TgtSession, 0, pmaxClient)
		if err != nil {
			log.Error("UnlinkSnapshot failed for target session" + TgtSession.Source)
			return err
		}
	}

	log.Debugf("Source sesion length (%d) : Snapshot name (%s)", noOfSnapsOnSrc, snapID)

	if noOfSnapsOnSrc > 0 {
		//The list needs to be sorted in descending order to terminate the
		//snapshot with higher generation first so that Powermax doesn't reset
		//the generation
		sort.Slice(SrcSessions[:], func(i, j int) bool {
			return SrcSessions[i].Generation > SrcSessions[j].Generation
		})
		//Remove all the temporary snapshots (snapID == "")
		//Remove a particular snapshot and all its generation when snapID is specified
		//Take a note if we terminated all the snapshots from source volume
		for i := range SrcSessions {
			if SrcSessions[i].Name == snapID || snapID == "" {
				err = s.UnlinkSnapshot(ctx, symID, &SrcSessions[i], 0, pmaxClient)
				if err != nil {
					log.Error("UnlinkSnapshot failed for source session: " + SrcSessions[i].Source)
					return err
				}
				if SrcSessions[i].Expired {
					continue
				}
				err = s.TerminateSnapshot(ctx, symID, SrcSessions[i].Source, SrcSessions[i].Name, pmaxClient)
				if err != nil {
					log.Error("Failed to terminate snapshot (%s)" + SrcSessions[i].Name)
					return fmt.Errorf("Failed to terminate snapshot - Error(%s)", err.Error())
				}
				noOfSnapsOnSrc--
			}
		}
	}

	//if all the snapshots on source volume are terminated, create a delete volume request
	//for source volumes having 'DS' tag
	if noOfSnapsOnSrc == 0 {
		var vol *types.Volume
		//A failure in RequestSoftVolDelete() after the snapshot termination
		//will be picked up by populateDeletionQueuesThread to push soft deleted
		//volume to deletion worker queue
		vol, err = pmaxClient.GetVolumeByID(ctx, symID, deviceID)
		if err != nil {
			log.Errorf("Failed to find source snapshot volume. Error (%s) ", err.Error())
			return nil
		}
		if s.isSourceTaggedToDelete(vol.VolumeIdentifier) {
			err = s.MarkVolumeForDeletion(ctx, symID, vol, pmaxClient)
			if err != nil {
				log.Error("MarkVolumeForDeletion failed with error - ", err.Error())
			}
		}
	}
	return nil
}

// UnlinkSnapshot unlinks all targets of the snapshot session.
// parameter maxUnlinkCount is to throttle the number of target it should
// unlink in one go. A value of 0 means, it unlinks all the targets else specified
// by number of targets in maxUnlinkCount
func (s *service) UnlinkSnapshot(ctx context.Context, symID string, snapSession *SnapSession, maxUnlinkCount int, pmaxClient pmax.Pmax) (err error) {
	if snapSession.Target == nil {
		return
	}
	var counter int
	for _, target := range snapSession.Target {
		if target.Defined {
			TargetList := []types.VolumeList{{Name: target.Target}}
			SourceList := []types.VolumeList{{Name: snapSession.Source}}
			log.Debugf("Executing Unlink on (%s) with source (%v) target (%v)", snapSession.Name, SourceList, TargetList)
			err = pmaxClient.ModifySnapshotS(ctx, symID, SourceList, TargetList, snapSession.Name, Unlink, "", snapSession.Generation)
			if err != nil {
				if strings.Contains(err.Error(), "The Device(s) is (are) already in the desired state or mode") {
					log.Debugf("Unlink on (%s) with source (%v) target (%v) is already done", snapSession.Name, SourceList, TargetList)
					return nil
				}
				return err
			}
			if maxUnlinkCount != 0 {
				counter++
				if counter == maxUnlinkCount {
					log.Debugf("Max Unlink count reached")
					break
				}
			}
		} else {
			return fmt.Errorf("Not all the targets are in Defined state")
		}
	}
	return nil
}

// TerminateSnapshot terminates the snapshot.
// The caller of this function should take a lock on the source device
// before making a call to this function
func (s *service) TerminateSnapshot(ctx context.Context, symID string, srcDev string, snapID string, pmaxClient pmax.Pmax) (err error) {
	//Ensure that the snapshot is not already deleted by a simultaneous operation
	snap, err := pmaxClient.GetSnapshotInfo(ctx, symID, srcDev, snapID)
	if err != nil || snap.VolumeSnapshotSource == nil {
		log.Info("Snapshot is already deleted: " + snapID)
		return nil
	}

	err = s.RemoveSnapshot(ctx, symID, srcDev, snapID, 0, pmaxClient)
	if err != nil {
		return err
	}
	return nil
}

//RemoveSnapshot deletes a snapshot
func (s *service) RemoveSnapshot(ctx context.Context, symID string, srcDev string, snapID string, Generation int64, pmaxClient pmax.Pmax) (err error) {
	log.Info(fmt.Sprintf("Deleting snapshot (%s) with generation (%d)", snapID, Generation))

	sourceVolumes := []types.VolumeList{}
	sourceVolumes = append(sourceVolumes, types.VolumeList{Name: srcDev})
	err = pmaxClient.DeleteSnapshotS(ctx, symID, snapID, sourceVolumes, Generation)
	if err != nil {
		return fmt.Errorf("DeleteSnapshot failed with error (%s)", err.Error())
	}
	return nil
}

// IsVolumeInSnapSession returns if the volume is a source/target in a snap session
func (s *service) IsVolumeInSnapSession(ctx context.Context, symID, deviceID string, pmaxClient pmax.Pmax) (source, target bool, err error) {
	vol, err := pmaxClient.GetVolumeByID(ctx, symID, deviceID)
	if err != nil {
		return false, false, err
	}
	return vol.SnapSource, vol.SnapTarget, nil
}

// GetSnapSessions return snapshot source and target sessions
func (s *service) GetSnapSessions(ctx context.Context, symID, deviceID string, pmaxClient pmax.Pmax) (srcSession []SnapSession, tgtSession *SnapSession, err error) {
	snapInfo, err := pmaxClient.GetVolumeSnapInfo(ctx, symID, deviceID)
	if err != nil {
		log.Errorf("GetVolumeSnapInfo failed for (%s): (%s)\n", deviceID, err.Error())
		return
	}
	log.Debugf("For Volume (%s), Snap Info: %v\n", deviceID, snapInfo)
	for _, volumeSnapshotSource := range snapInfo.VolumeSnapshotSource {
		snapSession := SnapSession{
			Source:     deviceID,
			Generation: volumeSnapshotSource.Generation,
			Name:       volumeSnapshotSource.SnapshotName,
			Expired:    volumeSnapshotSource.Expired,
		}
		for _, targets := range volumeSnapshotSource.LinkedVolumes {
			snapTgt := types.SnapTarget{
				Target:  targets.TargetDevice,
				CpMode:  targets.Copy,
				Defined: targets.Defined}
			snapSession.Target = append(snapSession.Target, snapTgt)
		}
		srcSession = append(srcSession, snapSession)
	}

	if snapInfo.VolumeSnapshotLink != nil &&
		len(snapInfo.VolumeSnapshotLink) > 0 {
		var pVolInfo *types.VolumeResultPrivate
		pVolInfo, err = pmaxClient.GetPrivVolumeByID(ctx, symID, deviceID)
		if err != nil {
			log.Errorf("GetPrivVolumeByID failed for (%s): (%s)\n", deviceID, err.Error())
			return
		}
		log.Debugf("For Volume (%s), Priv Vol Info: %v\n", deviceID, pVolInfo)
		//Ensure that this indeed is a target device
		if &pVolInfo.TimeFinderInfo != nil &&
			pVolInfo.TimeFinderInfo.SnapVXTgt {
			snapSession := pVolInfo.TimeFinderInfo.SnapVXSession[0].TargetSourceSnapshotGenInfo
			if snapSession != nil {
				tgtSession = &SnapSession{
					Name:       snapSession.SnapshotName,
					Expired:    snapSession.Expired,
					Generation: snapSession.Generation,
					Source:     snapSession.SourceDevice,
					Target: []types.SnapTarget{{
						Target:  snapSession.TargetDevice,
						CpMode:  snapInfo.VolumeSnapshotLink[0].Copy,
						Defined: snapInfo.VolumeSnapshotLink[0].Defined}},
				}
			}
		}
	}
	return
}

// LinkVolumeToSnapshot helps CreateVolume call to link the newly created
// volume as a target to a snapshot
func (s *service) LinkVolumeToSnapshot(ctx context.Context, symID, srcDevID, tgtDevID, snapID string, reqID string, pmaxClient pmax.Pmax) (err error) {
	lockHandle := fmt.Sprintf("%s%s", srcDevID, symID)
	lockNum := RequestLock(lockHandle, reqID)
	defer ReleaseLock(lockHandle, reqID, lockNum)

	// Verify that the snapshot exists on the array
	_, err = pmaxClient.GetSnapshotInfo(ctx, symID, srcDevID, snapID)
	if err != nil {
		return err
	}
	sourceList := []types.VolumeList{}
	targetList := []types.VolumeList{}
	sourceList = append(sourceList, types.VolumeList{Name: srcDevID})
	targetList = append(targetList, types.VolumeList{Name: tgtDevID})

	// Link the newly created volume as a target of the snapshot
	err = pmaxClient.ModifySnapshotS(ctx, symID, sourceList, targetList, snapID, Link, "", 0)
	if err != nil {
		if strings.Contains(err.Error(), "The maximum number of sessions has been exceeded for the specified Source device") {
			return status.Errorf(codes.FailedPrecondition, "Failed to link volumes: %s", err.Error())
		}
		return err
	}
	return nil
}

// LinkVolumeToVolume attaches the newly created volume
// to a temporary snapshot created from the source volume
func (s *service) LinkVolumeToVolume(ctx context.Context, symID string, vol *types.Volume, tgtDevID, snapID string, reqID string, pmaxClient pmax.Pmax) error {
	// Create a snapshot from the Source
	// Set max 1 hr life time for the temporary snapshot
	log.Debugf("Creating snapshot %s on %s and linking it to %s", snapID, vol.VolumeID, tgtDevID)
	var TTL int64 = 1
	snapInfo, err := s.CreateSnapshotFromVolume(ctx, symID, vol, snapID, TTL, reqID, pmaxClient)
	if err != nil {
		if strings.Contains(err.Error(), "The maximum number of sessions has been exceeded for the specified Source device") {
			return status.Errorf(codes.FailedPrecondition, "Failed to create snapshot: %s", err.Error())
		}
		return err
	}
	// Link the Target to the created snapshot
	err = s.LinkVolumeToSnapshot(ctx, symID, vol.VolumeID, tgtDevID, snapID, reqID, pmaxClient)
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

//CreateSnapshotFromVolume creates a snapshot on a source volume
func (s *service) CreateSnapshotFromVolume(ctx context.Context, symID string, vol *types.Volume, snapID string, TTL int64, reqID string, pmaxClient pmax.Pmax) (snapshot *types.VolumeSnapshot, err error) {
	lockHandle := fmt.Sprintf("%s%s", vol.VolumeID, symID)
	lockNum := RequestLock(lockHandle, reqID)
	defer ReleaseLock(lockHandle, reqID, lockNum)

	log.Debugf("Creating snapshot %s on %s", snapID, vol.VolumeID)
	deviceID := vol.VolumeID
	// Unlink this device if it is a target of another snapshot
	if vol.SnapSource || vol.SnapTarget {
		srcSessions, tgtSession, err := s.GetSnapSessions(ctx, symID, deviceID, pmaxClient)
		if err != nil {
			return nil, err
		}
		if len(srcSessions) > 0 {
			for i := range srcSessions {
				if srcSessions[i].Name == snapID {
					// return the existing snapshot to remain idempotent
					return pmaxClient.GetSnapshotInfo(ctx, symID, deviceID, snapID)
				}
			}
		}
		if tgtSession != nil {
			if tgtSession.Target[0].Defined {
				//At times, source and target can be same
				if vol.VolumeID != tgtSession.Source {
					lockTarget := fmt.Sprintf("%s%s", tgtSession.Source, symID)
					lockNum := RequestLock(lockTarget, reqID)
					defer ReleaseLock(lockTarget, reqID, lockNum)
				}
				//Snapshot for which deviceID is a target, might have got terminated by now
				//verify if it exists to execute unlink
				srcSessions, tgtSession, err = s.GetSnapSessions(ctx, symID, deviceID, pmaxClient)
				if err != nil {
					return nil, err
				}
				if tgtSession != nil {
					sourceList := []types.VolumeList{}
					targetList := []types.VolumeList{}
					sourceList = append(sourceList, types.VolumeList{Name: tgtSession.Source})
					targetList = append(targetList, types.VolumeList{Name: tgtSession.Target[0].Target})
					// Unlink the source device which is a target of another snapshot
					err = pmaxClient.ModifySnapshotS(ctx, symID, sourceList, targetList, tgtSession.Name, Unlink, "", tgtSession.Generation)
					if err != nil {
						return nil, err
					}
				}
			} else {
				return nil, fmt.Errorf("The source device (%s) is not in Defined state to execute unlink", tgtSession.Source)
			}
		}
	}
	// Create a new snapshot
	log.Info(fmt.Sprintf("Creating snapshot (%s) of source (%s) on PMAX array (%s)", snapID, deviceID, symID))
	SourceList := []types.VolumeList{}
	SourceList = append(SourceList, types.VolumeList{Name: deviceID})
	err = pmaxClient.CreateSnapshot(ctx, symID, snapID, SourceList, TTL)
	if err != nil {
		return nil, fmt.Errorf("CreateSnapshot failed with error (%s)", err.Error())
	}
	log.Info(fmt.Sprintf("Snapshot (%s) created successfully", snapID))
	return pmaxClient.GetSnapshotInfo(ctx, symID, deviceID, snapID)
}

// IsSnapshotSource returns true if the volume is a snapshots source
func (s *service) IsSnapshotSource(ctx context.Context, symID, devID string, pmaxClient pmax.Pmax) (snapSrc bool, err error) {
	var tempSnapTag string
	var delSnapTag string

	srcSessions, _, err := s.GetSnapSessions(ctx, symID, devID, pmaxClient)
	if err != nil {
		log.Error("Failed to determine volume as a snapshot source: Error - ", err.Error())
		if strings.Contains(err.Error(), "Volume is neither a source nor target") {
			err = nil
		}
		return false, err
	}
	tempSnapTag = fmt.Sprintf("%s%s", TempSnap, s.getClusterPrefix())
	delSnapTag = fmt.Sprintf("%s-%s%s", SnapDelPrefix, CsiVolumePrefix, s.getClusterPrefix())
	if len(srcSessions) > 0 {
		for _, session := range srcSessions {
			if !strings.HasPrefix(session.Name, tempSnapTag) &&
				!strings.HasPrefix(session.Name, delSnapTag) {
				return true, nil
			}
		}
	}
	return false, nil
}

// startSnapCleanupWorker starts the snapshot housekeeping worker thread(s).
// It should be called when the driver is initializing.
func (s *service) startSnapCleanupWorker() error {
	if s.adminClient == nil {
		err := s.controllerProbe(context.Background())
		if err != nil {
			fmt.Printf("Failed to controller probe\n")
			return err
		}
	}
	if snapCleaner == nil {
		snapCleaner = new(snapCleanupWorker)
		snapCleaner.PollingInterval = 3 * time.Minute
		snapCleaner.Queue = make(snapCleanupQueue, 0)
		snapCleaner.MaxRetries = 10
	}

	log.Infof("Starting snapshots cleanup worker thread")
	if !cleanupStarted {
		go snapCleanupThread(context.Background(), snapCleaner, s)
		cleanupStarted = true
	}
	return nil
}

// snapCleanupThread - Deletes temporary snapshots and snapshots
// that are pending but marked for deletion
func snapCleanupThread(ctx context.Context, scw *snapCleanupWorker, s *service) {
	var tempSnapTag string
	var delSnapTag string

	tempSnapTag = fmt.Sprintf("%s%s", TempSnap, s.getClusterPrefix())
	delSnapTag = fmt.Sprintf("%s-%s%s", SnapDelPrefix, CsiVolumePrefix, s.getClusterPrefix())

	/*for i := 0; i < 10; i++ {
		symIDList, err = pmaxClient.GetSymmetrixIDList()
		if err != nil {
			log.Error("Could not retrieve SymmetrixID list: " + err.Error())
			time.Sleep(1 * time.Minute)
		} else if symIDList != nil {
			break
		}
	}
	if symIDList == nil {
		panic("Couldn't fetch SymmetrixID list")
	}*/

	for _, symID := range s.opts.ManagedArrays {
		pmaxClient, err := s.GetPowerMaxClient(symID)
		if err != nil {
			log.Error(err.Error())
			continue
		}
		if err := s.IsSnapshotLicensed(ctx, symID, pmaxClient); err != nil {
			continue
		}
		volList, err := pmaxClient.GetSnapVolumeList(ctx, symID, types.QueryParams{
			types.IncludeDetails: true,
		})
		if err != nil {
			log.Error("Could not retrieve Snapshot IDs to be deleted")
			continue
		} else {
			for _, id := range volList.SymDevice {
				for _, snap := range id.Snapshot {
					if snap.Generation == 0 {
						success, snapID := s.findSnapIDFromSnapName(snap.Name)
						if success {
							if (strings.HasPrefix(snapID, tempSnapTag)) ||
								strings.HasPrefix(snapID, delSnapTag) {
								// Push the snapshot to cleanup worker
								var cleanReq snapCleanupRequest
								cleanReq.snapshotID = snapID
								cleanReq.symmetrixID = symID
								cleanReq.volumeID = id.Name
								log.Debugf("Pushing (%s) on vol (%s) to the queue", snapID, id.Name)
								snapCleaner.requestCleanup(&cleanReq)
							}
						} else {
							log.Infof("Snapshot ID (%s) is not in supported format", snapID)
						}
					}
				}
			}
		}
	}
	for {
		req := scw.removeItem()
		if req != nil {
			var reqID string
			if req.requestID == "" {
				reqID = fmt.Sprintf("ReqID%d", time.Now().Nanosecond())
			} else {
				reqID = req.requestID
			}
			lockHandle := fmt.Sprintf("%s%s", req.volumeID, req.symmetrixID)
			lockNum := RequestLock(lockHandle, reqID)
			pmaxClient, err := s.GetPowerMaxClient(req.symmetrixID)
			if err != nil {
				log.Error(err.Error())
				continue
			}
			err = s.UnlinkAndTerminate(ctx, req.symmetrixID, req.volumeID, req.snapshotID, pmaxClient)
			if err != nil {
				//Check if Snapshot is already deleted
				if strings.Contains(err.Error(), "Volume is neither a source nor target") {
					log.Errorf("Snapshot (%s) already terminated from Volume (%s) on PowerMax (%s)", req.snapshotID, req.volumeID, req.symmetrixID)
				} else {
					if req.retries == scw.MaxRetries {
						//push back to the que for retry
						req.retries++
						scw.queueForRetry(req)
					}
					log.Infof("Could not terminate Snapshot (%s) Error (%s)", req.snapshotID, err.Error())
				}
			} else {
				log.Infof("Snapshot (%s) is terminated from Volume (%s) on PowerMax (%s)", req.snapshotID, req.volumeID, req.symmetrixID)
			}
			ReleaseLock(lockHandle, reqID, lockNum)
		}
		time.Sleep(scw.PollingInterval)
	}
}

//isSourceTaggedToDelete returns true if the volume has a delete snapshot source volume tag
//appended with the volume name
func (s *service) isSourceTaggedToDelete(volName string) (ok bool) {
	//A soft deleted volume name has Csi&ClusterPrefix-VolumeName-DS
	volComponents := strings.Split(volName, "-")
	if len(volComponents) < 3 {
		ok = false
		return
	}
	//Get the last substring containing DS tag
	ds := volComponents[len(volComponents)-1]
	if ds == delSrcTag {
		ok = true
	}
	return
}

//findSnapIDFromSnapName returns the snapID from snapshot name in Device list
func (s *service) findSnapIDFromSnapName(snapName string) (ok bool, snapID string) {
	snapComponents := strings.Split(snapName, "-")
	if len(snapComponents) < 4 {
		ok = false
		return
	}
	ok = true
	//Extract the snapshot name from name found in volume information
	//which is in devid-src/lnk-snapshotname-generation
	snapID = strings.Join(snapComponents[2:len(snapComponents)-1], "-")
	return
}
