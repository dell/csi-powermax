package service

import (
	"container/heap"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	types "github.com/dell/csi-powermax/pmax/types/v90"
	log "github.com/sirupsen/logrus"
)

const (
	volDeleteKey                           = "_DELcsi"
	deletionStateQueued                    = "QUEUED"
	deletionStateDisassociateSG            = "DISASSOCIATE_STORAGE_GROUPS"
	deletionStateDeletingTracks            = "DELETING_TRACKS"
	deletionStateDeletingVolume            = "DELETING_VOLUME"
	deletionStateCompleted                 = "COMPLETED"
	deletionMaxErrors                      = 4
	maxRetryDuration                       = 30 * time.Minute
	delayBetweenRetries                    = 10 * time.Second
	maxRetryDeleteCount                    = 125
	deletionCompletedRequestsHistoryLength = 10
)

type deletionWorker struct {
	MinPollingInterval time.Duration
	PollingInterval    time.Duration
	Mutex              sync.Mutex
	Queue              deletionWorkerQueue
	CompletedRequests  deletionWorkerQueue
}

var delWorker *deletionWorker

// DelayBetweenRetries - Specifies the delay between retries for a volume when an error is encountered
var DelayBetweenRetries time.Duration

// MaxRetryDeleteCount - This indicates the maximum number of retries which will be done for a volume
var MaxRetryDeleteCount int

// MaxRetryDuration - This indicates the maximum time a volume with errors can be retried for deletion
var MaxRetryDuration time.Duration

type deletionWorkerQueue []*deletionWorkerRequest

type deletionWorkerRequest struct {
	symmetrixID           string
	volumeID              string
	volumeName            string
	volumeSizeInCylinders int64
	skipDeallocate        bool
	// These are filled in by delWorker
	state   string
	nerrors int
	err     error
	volume  *types.Volume
	job     *types.Job
	// The following item is for the priority queue implementation
	priority            int64
	additionTimeStamp   time.Time
	lastErrorTimeStamp  time.Time
	firstErrorTimeStamp time.Time
}

func (q deletionWorkerQueue) Len() int {
	return len(q)
}

// Less compares two elements in the queue, return true if the ith is less than the jth
func (q deletionWorkerQueue) Less(i, j int) bool {
	return q[i].priority < q[j].priority
}

// Swap swaps two elements in the queue and updates their index.
func (q deletionWorkerQueue) Swap(i, j int) {
	q[i], q[j] = q[j], q[i]
}

// Push puts a new element at the end of the queue and then fixes the heap.
func (q *deletionWorkerQueue) Push(x interface{}) {
	req := x.(*deletionWorkerRequest)
	n := len(*q)
	req.priority = req.getPriority()
	*q = append(*q, req)
	heap.Fix(q, n)
}

// Pop takes the takes the element off the top end of the queue.
func (q *deletionWorkerQueue) Pop() interface{} {
	old := *q
	n := len(old)
	req := old[n-1]
	*q = old[0 : n-1]
	return *req
}

func (req *deletionWorkerRequest) fields() map[string]interface{} {
	fields := map[string]interface{}{
		"SymmetrixID":         req.symmetrixID,
		"VolumeID":            req.volumeID,
		"VolumeName":          req.volumeName,
		"State":               req.state,
		"Size":                req.volumeSizeInCylinders,
		"No. errors":          req.nerrors,
		"SkipDeAllocate":      req.skipDeallocate,
		"additionTimeStamp":   req.additionTimeStamp,
		"lastErrorTimeStamp":  req.lastErrorTimeStamp,
		"firstErrorTimeStamp": req.firstErrorTimeStamp,
	}
	if req.job != nil {
		fields["JobID"] = req.job.JobID
	}
	return fields
}

// getPriority determines the RB Key value base on buckets of the size of volumes in cylinders.
// getPriority penalizes requests that have had errors
func (req *deletionWorkerRequest) getPriority() int64 {
	multiplier := 5 * (req.nerrors + 1)
	timeSinceLastError := time.Now().Sub(req.lastErrorTimeStamp)
	timeBeforeNextRetry := 0
	if timeSinceLastError >= delayBetweenRetries {
		timeBeforeNextRetry = 0
	} else {
		timeBeforeNextRetry = int((delayBetweenRetries - timeSinceLastError).Seconds())
	}
	return req.volumeSizeInCylinders / int64(100) * int64(timeBeforeNextRetry+multiplier)
}

// This gives the current status of the deletion being processed
type deletionWorkerStatus struct {
	request *deletionWorkerRequest
	state   string
	jobID   string
}

// requestDeletion adds a deletion request to the queue for the keyvalue of the node.
// Note there can only be one Red-Black node entry for any given key.
// The keyValue is computed by from the sizeInCylinders requested and the number of errors,
// such that smaller requests with no retries are processed first, and largest requests with retries
// are processed last.
func (w *deletionWorker) requestDeletion(req *deletionWorkerRequest) {
	fields := req.fields()
	req.state = deletionStateQueued
	w.Mutex.Lock()
	defer w.Mutex.Unlock()
	for i := range w.Queue {
		if w.Queue[i].volumeID == req.volumeID && w.Queue[i].symmetrixID == req.symmetrixID {
			// Found!
			log.Warningf("Volume ID: %s already present in the deletion queue", req.volumeID)
			return
		}
	}
	req.additionTimeStamp = time.Now()
	heap.Push(&w.Queue, req)
	log.WithFields(fields).Debug("Queued for Deletion")
}

func (w *deletionWorker) queueForRetry(req *deletionWorkerRequest) {
	w.Mutex.Lock()
	defer w.Mutex.Unlock()
	heap.Push(&w.Queue, req)
}

// removeItem removes the top priority item so it can be worked on
func (w *deletionWorker) removeItem() *deletionWorkerRequest {
	w.Mutex.Lock()
	defer w.Mutex.Unlock()
	if len(w.Queue) == 0 {
		return nil
	}
	reqx := heap.Pop(&w.Queue)
	req := reqx.(deletionWorkerRequest)
	return &req
}

// startDeletionWorker starts the deletion worker thread(s).
// It should be called when the driver is initializing.
func (s *service) startDeletionWorker(defaultRetryValues bool) error {
	if s.adminClient == nil {
		err := s.controllerProbe(context.Background())
		if err != nil {
			fmt.Printf("Failed to controller probe\n")
			return err
		}
	}
	if defaultRetryValues {
		MaxRetryDeleteCount = maxRetryDeleteCount
		MaxRetryDuration = maxRetryDuration
		DelayBetweenRetries = delayBetweenRetries
	} else {
		// Only used for unit testing
		MaxRetryDeleteCount = 5
		MaxRetryDuration = 10 * time.Second
		DelayBetweenRetries = 1 * time.Second
	}
	if delWorker == nil {
		delWorker = new(deletionWorker)
		delWorker.MinPollingInterval = 1 * time.Second
		delWorker.PollingInterval = 3 * time.Second
		delWorker.Queue = make(deletionWorkerQueue, 0)
		delWorker.CompletedRequests = make(deletionWorkerQueue, 0)
		go deletionWorkerWorkerThread(delWorker, s)
	}
	// Run a separate goroutine to rebuild the queues of volumes
	// to be deleted by searching for volumes
	// with the _DELCSI prefix.
	s.waitGroup.Wait() // This is only for testing for the cases when the startDeletionWorker is called again
	s.waitGroup.Add(1)
	go s.runPopulateDeletionQueuesThread()
	return nil
}

func (s *service) runPopulateDeletionQueuesThread() error {
	return populateDeletionQueuesThread(delWorker, s)
}

// populateDeletionQueuesThread - Populates the deletion queues
func populateDeletionQueuesThread(w *deletionWorker, s *service) error {
	defer s.waitGroup.Done()
	if s.adminClient == nil {
		errormsg := "No adminClient. So can't run deletionWorker search for volumes to be deleted"
		log.Error(errormsg)
		return fmt.Errorf(errormsg)
	}
	symIDList, err := s.adminClient.GetSymmetrixIDList()
	if err != nil {
		errormsg := "Could not retrieve SymmetrixID list for deleteionWorker"
		log.Error(errormsg)
		return fmt.Errorf(errormsg)
	}
	for _, symID := range symIDList.SymmetrixIDs {
		log.Printf("Processing symmetrix %s for volumes to be deleted", symID)
		deviceIDToJob := s.getRunningJobsForSymmetrix(symID, "volume")
		fmt.Printf("deviceIDToJob %#v\n", deviceIDToJob)
		volDeletePrefix := volDeleteKey + "-" + s.getClusterPrefix()
		volList, err := s.adminClient.GetVolumeIDList(symID, volDeletePrefix, true)
		if err != nil {
			log.Error("Could not retrieve Volume IDs to be deleted")
			continue
		} else {
			for _, id := range volList {
				volume, err := s.adminClient.GetVolumeByID(symID, id)
				if err != nil {
					log.Error("Could not retrieve Volume: " + id)
					continue
				} else {
					// Put volume on the queue if appropriate
					if strings.HasPrefix(volume.VolumeIdentifier, volDeletePrefix) {
						// Fetch the uCode version details
						isPostElmSR, err := s.isPostElmSR(symID)
						if err != nil {
							log.Error("Failed to symmetrix uCode version details")
							continue
						}
						req := &deletionWorkerRequest{
							symmetrixID:           symID,
							volumeID:              id,
							volumeName:            volume.VolumeIdentifier,
							volumeSizeInCylinders: int64(volume.CapacityCYL),
							skipDeallocate:        isPostElmSR,
						}
						// If there is an existing job associated with this id, associate it
						job := deviceIDToJob[id]
						if job != nil {
							fmt.Printf("Associating job %s with device %s\n", job.JobID, id)
							req.job = job
						}
						delWorker.requestDeletion(req)
					}
				}
			}
		}
	}
	log.Info("Finished populating devices in the deletion worker queue")
	return nil
}

// getRunningJobsForSymmetrix gets all the running jobs for a particular symmetrix for a
// particular resourceType and returns a map of resourceID to Job
func (s *service) getRunningJobsForSymmetrix(symID string, resourceType string) map[string]*types.Job {
	result := make(map[string]*types.Job)
	if s.adminClient == nil {
		log.Error("deletionWorker: No adminclient. Can't query the array for running jobs")
		return result
	}
	jobIDList, err := s.adminClient.GetJobIDList(symID, types.JobStatusRunning)
	if err != nil {
		log.Error(err)
		return result
	}
	for _, jobID := range jobIDList {
		job, err := s.adminClient.GetJobByID(symID, jobID)
		if err != nil {
			log.Error(err)
			continue
		}
		jobSymID, jobResourceType, jobResourceID := job.GetJobResource()
		if jobSymID == symID && jobResourceType == resourceType {
			result[jobResourceID] = job
		}
	}
	return result
}

// This is an endless thread that will move volume deletions forward.
func deletionWorkerWorkerThread(w *deletionWorker, s *service) {
	var req *deletionWorkerRequest
	for {
		sleepTime := w.MinPollingInterval
		// Get a new request if we have none
		if req == nil {
			req = w.removeItem()
		}
		// If we have a request process it
		if req != nil {
			skipRetry := false
			req.err = nil
			sleepTime = w.PollingInterval
			log.WithFields(req.fields()).Info("deletionWorker processing")
			// Validate the volume is still the same one.
			csiVolumeID := fmt.Sprintf("%s-%s-%s", req.volumeName, req.symmetrixID, req.volumeID)
			_, _, req.volume, req.err = s.GetVolumeByID(csiVolumeID)
			if req.err != nil || req.volume == nil {
				if req.err != nil {
					log.Error("deletion worker: " + req.err.Error())
				}
				req.state = deletionStateCompleted
				log.Infof("CSI Volume: %s spent %.2f seconds in the deletion queue",
					csiVolumeID, time.Since(req.additionTimeStamp).Seconds())
				w.addToCompletedRequests(req)
				req = nil
				continue
			}
			if req.nerrors > 0 {
				if time.Since(req.lastErrorTimeStamp) < DelayBetweenRetries {
					log.WithFields(req.fields()).Info("Waiting before the next retry")
					skipRetry = true
				}
			}
			if !skipRetry {
				switch req.state {
				case deletionStateQueued:
					req.state = deletionStateDisassociateSG
					fallthrough
				case deletionStateDisassociateSG:
					req.err = req.disassociateFromStorageGroups(s)
					if req.err != nil {
						break
					}
					req.state = deletionStateDeletingTracks
					fallthrough
				case deletionStateDeletingTracks:
					req.err = req.deleteTracks(s)
					if req.err != nil {
						break
					}
					if req.job != nil {
						// job still running
						break
					}
					// No err, no job, we're done
					req.state = deletionStateDeletingVolume
					fallthrough
				case deletionStateDeletingVolume:
					req.err = req.deleteVolume(s)
					if req.err != nil {
						if strings.Contains(req.err.Error(), errDeviceInStorageGrp) {
							if req.nerrors < MaxRetryDeleteCount-1 {
								// Put the request back in the queue and attempt to remove device from SG again
								log.WithFields(req.fields()).Error(fmt.Sprintf("Error - %s. Retrying by moving the deletion request state to DISASSOCIATE SG",
									req.err.Error()))
								req.state = deletionStateDisassociateSG
								skipRetry = true
							}
						}
						break
					}
					req.state = deletionStateCompleted
				}
			}
			if req.err != nil {
				if req.nerrors == 0 {
					req.firstErrorTimeStamp = time.Now()
				}
				req.nerrors = req.nerrors + 1
				req.lastErrorTimeStamp = time.Now()
			}
			if skipRetry {
				// Just add the req back to the queue
				w.queueForRetry(req)
				req = nil
				skipRetry = false
				sleepTime = w.MinPollingInterval
			} else if req.err != nil {
				queueForRetry := false
				if req.state == deletionStateDeletingVolume {
					// Do this only when the volume in the state - deleting volume
					if req.nerrors >= MaxRetryDeleteCount {
						// Keep trying until the max retry duration even if the max number of retries are hit
						if time.Since(req.firstErrorTimeStamp) < MaxRetryDuration {
							queueForRetry = true
						}
					} else {
						// Keep trying until the max number of retries are hit
						queueForRetry = true
					}
				} else {
					// For any other state, just check against deletion max errors
					if req.nerrors < deletionMaxErrors {
						// Keep retrying until you hit the deletion max errors
						queueForRetry = true
					}
				}
				if queueForRetry {
					log.WithFields(req.fields()).Error(fmt.Sprintf("Error %d will be retried: %s", req.nerrors, req.err))
					w.queueForRetry(req)
					req = nil
				} else {
					log.WithFields(req.fields()).Error(fmt.Sprintf("Error %d will not be retried: %s", req.nerrors, req.err))
					log.Infof("CSI Volume: %s spent %.2f seconds in the deletion queue",
						csiVolumeID, time.Since(req.additionTimeStamp).Seconds())
					w.addToCompletedRequests(req)
					req = nil
				}
			} else if req.job != nil {
				log.WithFields(req.fields()).Info("Waiting on job to complete...")
			} else {
				log.WithFields(req.fields()).Info("Completed...")
				log.Infof("CSI Volume: %s spent %.2f seconds in the deletion queue",
					csiVolumeID, time.Since(req.additionTimeStamp).Seconds())
				w.addToCompletedRequests(req)
				req = nil
				sleepTime = w.MinPollingInterval
			}
		}
		time.Sleep(sleepTime)
	}
}

// Append a request to the history
func (w *deletionWorker) addToCompletedRequests(req *deletionWorkerRequest) {
	if len(w.CompletedRequests)+1 >= deletionCompletedRequestsHistoryLength {
		w.CompletedRequests = w.CompletedRequests[1:deletionCompletedRequestsHistoryLength]
	}
	w.CompletedRequests = append(w.CompletedRequests, req)
}

// disassociatedFromStorageGroups disassociates the requested volume from all storage groups.
func (req *deletionWorkerRequest) disassociateFromStorageGroups(s *service) error {
	fields := req.fields()
	// First, fetch the volume.
	if req.volume == nil {
		return fmt.Errorf("req.volume is nil")
	}
	// Check all the storage groups for masking views.
	for _, sgID := range req.volume.StorageGroupIDList {
		fields["StorageGroup"] = sgID
		// Check that the SG has no masking views by reading it first
		sg, err := s.adminClient.GetStorageGroup(req.symmetrixID, sgID)
		if err != nil {
			log.WithFields(fields).Error("Failed to GetStorageGroupByID: " + err.Error())
			req.volume = nil
			return err
		}
		if sg.NumOfMaskingViews > 0 {
			fields["StorageGroup"] = sgID
			log.WithFields(fields).Error("Storage group has masking views")
			err := fmt.Errorf("Storage group %s has masking views so cannot remove volume", sgID)
			return err
		}
	}
	if len(req.volume.StorageGroupIDList) == 0 {
		log.WithFields(fields).Info("deletion worker: volume is not present in any SG")
		return nil
	}
	// Repeat for any SGs the volume is a memmber of
	for _, sgID := range req.volume.StorageGroupIDList {
		fields["StorageGroup"] = sgID
		log.WithFields(fields).Info("deletion worker: Removing volume from StorageGroup")
		_, err := s.adminClient.RemoveVolumesFromStorageGroup(req.symmetrixID, sgID, req.volumeID)
		if err != nil {
			log.WithFields(fields).Error("Failed RemoveVolumesFromStorageGroup: " + err.Error())
			req.volume = nil
			return err
		}
	}
	return nil
}

func (req *deletionWorkerRequest) deleteTracks(s *service) error {
	var err error
	fields := req.fields()
	if req.skipDeallocate {
		log.WithFields(fields).Info("deletion worker: Array supports rapid TDEV deallocate. Skipping deallocate step")
		return nil
	}
	// If haven't initiated job yet, then do so.
	if req.job == nil {
		log.WithFields(fields).Debug("deletion worker: Initiating removal of tracks")
		req.job, err = s.adminClient.InitiateDeallocationOfTracksFromVolume(req.symmetrixID, req.volumeID)
		if err != nil {
			log.WithFields(fields).Error("Failed InitiateDeallocationOfTracksFromVolume: " + err.Error())
			return err
		}
	}
	fields["JobID"] = req.job.JobID
	// Look to see if job has finished.
	req.job, err = s.adminClient.GetJobByID(req.symmetrixID, req.job.JobID)
	if err != nil {
		log.WithFields(fields).Error("Failed GetJobByID: " + err.Error())
		req.job = nil
		return err
	}
	switch req.job.Status {
	case types.JobStatusSucceeded:
		req.job = nil
		return nil
	case types.JobStatusFailed:
		if strings.Contains(req.job.Result, "The device is already in the requested state") {
			req.job = nil
			return nil
		}
		err := fmt.Errorf("Job Failed: %s", req.job.Result)
		req.job = nil
		return err
	default:
		// Job presumably still running, return no error and leave job present.
		return nil
	}
}

func (req *deletionWorkerRequest) deleteVolume(s *service) error {
	log.WithFields(req.fields()).Debug("deletion worker: Deleting volume")
	err := s.adminClient.DeleteVolume(req.symmetrixID, req.volumeID)
	return err
}
