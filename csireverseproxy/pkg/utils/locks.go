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

package utils

import (
	"fmt"
	"revproxy/v2/pkg/common"
	"sync"

	log "github.com/sirupsen/logrus"
)

const (
	// LockRequestQueueLength is the maximum number of lock requests which can be queued
	LockRequestQueueLength = 100000
)

// LockRequest - Input structure to specify a request for locking a resource
type LockRequest struct {
	ResourceID     string
	Unlock         bool
	WaitChannel    chan bool
	MaxOutStanding int
	MaxActive      int
}

// LockProperties - properties of the lock object
type LockProperties struct {
	MaxActive      int
	MaxOutStanding int
	Active         int
	WaitChannel    chan chan bool
	Modified       bool
}

// Queue - queues a lock request
func (lockProp *LockProperties) Queue(request LockRequest) {
	if lockProp.MaxActive != request.MaxActive || lockProp.MaxOutStanding != request.MaxOutStanding {
		// set the dirty bit
		lockProp.Modified = true
	}
	if lockProp.Active < lockProp.MaxActive {
		lockProp.Active++
		request.WaitChannel <- true
	} else {
		select {
		case lockProp.WaitChannel <- request.WaitChannel:
			log.Infof("Request queued: %s\n", request.ResourceID)
		default:
			log.Infof("Max number of outstanding requests already queued for: %s\n", request.ResourceID)
			request.WaitChannel <- false
		}
	}
}

// Release - Releases a lock
func (lockProp *LockProperties) Release(request LockRequest, fifoLocks map[string]*LockProperties) {
	if lockProp.Active == lockProp.MaxActive {
		select {
		case nextRequest := <-lockProp.WaitChannel:
			nextRequest <- true
		default:
			lockProp.Active--
		}
	} else {
		lockProp.Active--
	}
	// if dirty bit is set and no outstanding requests
	// cleanup the entry from map so that the lock properties
	// can be updated
	if lockProp.Active == 0 && lockProp.Modified {
		deleteEntryFromMap(fifoLocks, request)
	}
}

// String - helper function which helps log the current state of lock
func (lockProp *LockProperties) String() string {
	return fmt.Sprintf("Active(%d/%d), Queued(%d/%d)\n", lockProp.Active, lockProp.MaxActive,
		len(lockProp.WaitChannel), lockProp.MaxOutStanding)
}

func addEntryToMap(fifoLocks map[string]*LockProperties, request LockRequest) *LockProperties {
	// this is the first request for this resource
	lockProps := LockProperties{}
	lockProps.MaxActive = request.MaxActive
	lockProps.MaxOutStanding = request.MaxOutStanding
	lockProps.WaitChannel = make(chan chan bool, lockProps.MaxOutStanding)
	fifoLocks[request.ResourceID] = &lockProps
	return &lockProps
}

func deleteEntryFromMap(fifoLocks map[string]*LockProperties, request LockRequest) {
	// Close the channel
	lockProps, ok := fifoLocks[request.ResourceID]
	if ok {
		close(lockProps.WaitChannel)
		delete(fifoLocks, request.ResourceID)
	}
}

var lockMutex sync.Mutex

var lockRequestsQueue = make(chan LockRequest, LockRequestQueueLength)

type lockWorkers struct {
}

var lockWorker *lockWorkers

// InitializeLock - initializes the lockHandler
func InitializeLock() {
	if lockWorker == nil {
		lockWorker = new(lockWorkers)
		LockRequestHandler()
	}
}

// LockRequestHandler - goroutine which listens for any lock/unlock requests
func LockRequestHandler() {
	var fifoLocks = make(map[string]*LockProperties)
	go func() {
		log.Debug("Successfully started the lock request handler")
		for {
			select {
			case request := <-lockRequestsQueue:
				lockMutex.Lock()
				lockProps, ok := fifoLocks[request.ResourceID]
				if !ok {
					lockProps = addEntryToMap(fifoLocks, request)
				}
				if request.Unlock {
					lockProps.Release(request, fifoLocks)
				} else {
					lockProps.Queue(request)
				}
				log.Infof("Lock: %s, %s", request.ResourceID, lockProps.String())
				lockMutex.Unlock()
			}
		}
	}()
}

// Lock is used to queue requests for a given resource
type Lock struct {
	ID             string
	LockType       common.LockType
	RequestID      string
	WaitChannel    chan bool
	MaxOutStanding int
	MaxActive      int
}

// Lock - Attempt to take a lock on a resource
func (l *Lock) Lock() error {
	defer Elapsed(l.RequestID, fmt.Sprintf("%s Lock", string(l.LockType)))()
	waitChannel := make(chan bool, 1)
	l.WaitChannel = waitChannel
	resourceID := l.ID + "-" + string(l.LockType)
	lockRequest := LockRequest{
		ResourceID:     resourceID,
		WaitChannel:    l.WaitChannel,
		Unlock:         false,
		MaxOutStanding: l.MaxOutStanding,
		MaxActive:      l.MaxActive,
	}
	lockRequestsQueue <- lockRequest
	isLocked := <-l.WaitChannel
	if !isLocked {
		return fmt.Errorf("failed to obtain lock")
	}
	log.Infof("Request ID: %s - Obtained %s lock\n", l.RequestID, string(l.LockType))
	return nil
}

// Release - releases a previously held lock
func (l *Lock) Release() {
	resourceID := l.ID + "-" + string(l.LockType)
	lockReleaseRequest := LockRequest{
		ResourceID:  resourceID,
		Unlock:      true,
		WaitChannel: l.WaitChannel,
	}
	lockRequestsQueue <- lockReleaseRequest
	close(l.WaitChannel)
}
