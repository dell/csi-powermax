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
	"math/rand"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
)

type lockWorkers struct {
}

var lockWorker *lockWorkers

// LockRequest - Input structure to specify a request for locking a resource
type LockRequest struct {
	ResourceID  string
	LockNumber  int
	Unlock      bool
	WaitChannel chan int
}

// LockRequestInfo - Stores information about each lock request
type LockRequestInfo struct {
	LockNumber  int
	WaitChannel chan int
}

// LockInfo - Stores information about each resource id in the map
type LockInfo struct {
	LockRequests       chan LockRequestInfo
	CurrentLockNumber  int
	CurrentWaitChannel chan int
	Count              int
}

var lockMutex sync.Mutex
var fifolocks = make(map[string]*LockInfo)

var lockRequestsQueue = make(chan LockRequest, 1000)

// LockRequestHandler - goroutine which listens for any lock/unlock requests
func LockRequestHandler() {
	go func() {
		log.Info("Successfully started the lock request handler")
		for {
			select {
			case request := <-lockRequestsQueue:
				lockMutex.Lock()
				lockInfo, ok := fifolocks[request.ResourceID]
				if !ok {
					// ResourceID not present in fifolocks map
					if request.Unlock {
						// Invalid unlock request as there is no entry for the resource ID in the fifolocks map
						log.Warning("There is no lock to be released!")
					} else {
						// Create an entry in the fifolocks map as this is the first call for this resource id
						waitChannels := make(chan LockRequestInfo, 100)
						lockInfo := LockInfo{
							CurrentLockNumber:  request.LockNumber,
							CurrentWaitChannel: request.WaitChannel,
							LockRequests:       waitChannels,
							Count:              1,
						}
						fifolocks[request.ResourceID] = &lockInfo
						request.WaitChannel <- 1
					}
				} else {
					//ResourceID is present in the fifolocks map
					if lockInfo.CurrentLockNumber == request.LockNumber {
						// RequestID matches with the CurrentRequestId for the resourceID
						// Lock is held by this request id
						if request.Unlock {
							// This is an unlock request
							// First close the channel
							close(lockInfo.CurrentWaitChannel)
							select {
							case lockRequestInfo := <-lockInfo.LockRequests:
								// Grant the lock and notify the next goroutine waiting for the lock
								lockInfo.CurrentLockNumber = lockRequestInfo.LockNumber
								lockInfo.CurrentWaitChannel = lockRequestInfo.WaitChannel
								lockInfo.Count = 1
								lockRequestInfo.WaitChannel <- 1
							default:
								// No goroutines waiting for this lock. Set CurrentRequestID to -1
								lockInfo.CurrentLockNumber = -1
								lockInfo.Count = 0
							}
						} else {
							// This is a request to lock
							// Invalid lock request as a lock is already held for the same resource id and request id
							log.Warning("Invalid request. There is a lock held with the same request")
						}
					} else {
						// RequestID doesn't match with the CurrentRequestID for the resourceID
						if request.Unlock {
							// Attempt to release a lock not held by the caller
							log.Warning("You don't hold the lock")
						} else {
							if len(lockInfo.LockRequests) == 0 && (lockInfo.CurrentLockNumber == -1) {
								// Entry for resource ID already present in fifolocks
								// Grant the lock to caller as no goroutines waiting
								lockInfo.CurrentLockNumber = request.LockNumber
								lockInfo.CurrentWaitChannel = request.WaitChannel
								request.WaitChannel <- 1
							} else {
								// Queue a lock request
								lockRequestInfo := LockRequestInfo{
									LockNumber:  request.LockNumber,
									WaitChannel: request.WaitChannel,
								}
								lockInfo.LockRequests <- lockRequestInfo
							}
						}
					}
					//fmt.Printf("Current number of requests pending in the queue: %d\n", len(lockInfo.LockRequests))
				}
				lockMutex.Unlock()
			}
		}
	}()
}

//CleanupMapEntries - clean up stale entries from the map
func CleanupMapEntries(duration time.Duration) {
	ticker := time.NewTicker(duration)
	go func() {
		log.Infof("CleanupMapEntries: Successfully started the cleanup worker. This will wake up every %.2f minutes to clean up stale entries",
			duration.Minutes())
		for {
			select {
			case <-ticker.C:
				lockMutex.Lock()
				// Don't hold the mutex for more than 20 milliseconds
				now := time.Now()
				log.Debugf("CleanupMapEntries: Current number of entries in lock map: %d", len(fifolocks))
				for resourceID, lockInfo := range fifolocks {
					if time.Since(now) > 20*time.Millisecond {
						log.Debugf("CleanupMapEntries: Held the lock mutex for 20 milliseconds. Releasing it now")
						break
					}
					if lockInfo.CurrentLockNumber == -1 {
						log.Debugf("CleanupMapEntries: Removing stale entry from the lockmap for: %s\n", resourceID)
						delete(fifolocks, resourceID)
					}
				}
				lockMutex.Unlock()
			}
		}
	}()
}

// RequestLock - Request for lock for a given resource ID
// requestID is optional
// returns a lock number which is used later to release the lock
func RequestLock(resourceID string, requestID string) int {
	if requestID != "" {
		log.Debugf("Requesting a lock for %s with requestID: %s at: %v", resourceID, requestID, time.Now())
	}
	// Get a random number between 1,000,000 and 10,000,000
	lockNum := rand.Intn(10000000-1000000) + 1000000 // #nosec G404
	ch := make(chan int, 1)
	lockReq := LockRequest{ResourceID: resourceID, WaitChannel: ch, LockNumber: lockNum, Unlock: false}
	lockRequestsQueue <- lockReq
	<-ch
	log.Debugf("Acquired - Lock Number:%d, requestID: %s, resourceID: %s at: %v",
		lockNum, requestID, resourceID, time.Now())
	return lockNum
}

// ReleaseLock - Release a held lock for resourceID
// Input lockNum should be the same as one returned by RequestLock
func ReleaseLock(resourceID string, requestID string, lockNum int) {
	lockReleaseRequest := LockRequest{ResourceID: resourceID, LockNumber: lockNum, Unlock: true}
	lockRequestsQueue <- lockReleaseRequest
	log.Debugf("Released - Lock Number: %d, requestID: %s, resourceID: %s",
		lockNum, requestID, resourceID)
}

// StartLockManager - Used to start the lock request handler & clean up workers
func (s *service) StartLockManager(duration time.Duration) {
	if lockWorker == nil {
		lockWorker = new(lockWorkers)
		LockRequestHandler()
		CleanupMapEntries(duration)
	}
}
