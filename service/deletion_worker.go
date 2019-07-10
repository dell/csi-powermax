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

type deletionWorkerQueue []*deletionWorkerRequest

type deletionWorkerRequest struct {
	symmetrixID           string
	volumeID              string
	volumeName            string
	volumeSizeInCylinders int64
	// These are filled in by delWorker
	state   string
	nerrors int
	err     error
	volume  *types.Volume
	job     *types.Job
	// The following item is for the priority queue implementation
	priority int64
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
		"SymmetrixID": req.symmetrixID,
		"VolumeID":    req.volumeID,
		"VolumeName":  req.volumeName,
		"State":       req.state,
		"Size":        req.volumeSizeInCylinders,
		"No. errors":  req.nerrors,
	}
	if req.job != nil {
		fields["JobID"] = req.job.JobID
	}
	return fields
}

// getKey determines the RB Key value base on buckets of the size of volumes in cylinders.
// getKey penalizes requests that have had errors
func (req *deletionWorkerRequest) getPriority() int64 {
	multiplier := 0
	switch req.nerrors {
	case 0:
		multiplier = 1
	case 1:
		multiplier = 2
	default:
		multiplier = 3
	}
	return req.volumeSizeInCylinders / int64(100) * int64(multiplier)
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
func (s *service) startDeletionWorker() error {
	if delWorker == nil {
		delWorker = new(deletionWorker)
		delWorker.MinPollingInterval = 1 * time.Second
		delWorker.PollingInterval = 3 * time.Second
		go deletionWorkerWorkerThread(delWorker, s)
	}
	delWorker.Queue = make(deletionWorkerQueue, 0)
	delWorker.CompletedRequests = make(deletionWorkerQueue, 0)

	// Rebuild the queues of volumes to be deleted by searching for volumes
	// with the _DELCSI prefix.
	if s.adminClient == nil {
		s.controllerProbe(context.Background())
	}
	if s.adminClient == nil {
		return fmt.Errorf("No adminClient so can't run deletionWorker search for volumes to be deleted")
	}
	symIDList, err := s.adminClient.GetSymmetrixIDList()
	if err != nil {
		return fmt.Errorf("Could not retrieve SymmetrixID list for deleteionWorker")
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
						req := &deletionWorkerRequest{
							symmetrixID:           symID,
							volumeID:              id,
							volumeName:            volume.VolumeIdentifier,
							volumeSizeInCylinders: int64(volume.CapacityCYL),
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
	return nil
}

// getRunningJobsForSymmetrix gets all the running jobs for a particular symmetrix for a
// particular resourceType and returns a map of resourceID to Job
func (s *service) getRunningJobsForSymmetrix(symID string, resourceType string) map[string]*types.Job {
	result := make(map[string]*types.Job)
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
			req.err = nil
			sleepTime = w.PollingInterval
			log.WithFields(req.fields()).Info("deletionWorker processing")
			// Validate the volume is still the same one.
			csiVolumeID := fmt.Sprintf("%s-%s-%s", req.volumeName, req.symmetrixID, req.volumeID)
			_, _, req.volume, req.err = s.GetVolumeByID(csiVolumeID)
			if req.err != nil {
				log.Error("deletion worker: " + req.err.Error())
				req.state = deletionStateCompleted
				w.addToCompletedRequests(req)
				req = nil
				continue
			}
			if req.volume == nil {
				log.Error("volume is nil")
				req.state = deletionStateCompleted
				w.addToCompletedRequests(req)
				req = nil
				continue
			}

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
					break
				}
				req.state = deletionStateCompleted
			}
			if req.err != nil {
				req.nerrors = req.nerrors + 1
				if req.nerrors < deletionMaxErrors {
					log.WithFields(req.fields()).Info(fmt.Sprintf("Error %d will be retried: %s", req.nerrors, req.err.Error()))
					w.queueForRetry(req)
					req = nil
				} else {
					log.WithFields(req.fields()).Error(fmt.Sprintf("Error %d will not be retried: %s", req.nerrors, req.err.Error()))
					w.addToCompletedRequests(req)
					req = nil
				}
			} else if req.job != nil {
				log.WithFields(req.fields()).Info("Waiting on job to complete...")
			} else {
				log.WithFields(req.fields()).Info("Completed...")
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
	// Repeat for any SGs the volume is a memmber of
	for _, sgID := range req.volume.StorageGroupIDList {
		fields["StorageGroup"] = sgID
		log.WithFields(fields).Debug("deletion worker: Removing volume from StorageGroup")
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
