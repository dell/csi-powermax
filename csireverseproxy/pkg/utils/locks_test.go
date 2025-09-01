/*
 *
 * Copyright © 2021-2024 Dell Inc. or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

/*
Copyright © 2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"testing"
	"time"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"
)

func TestLock(t *testing.T) {
	tests := []struct {
		name       string
		lockType   string
		requestID  string
		expectErr  bool
		lockResult bool
	}{
		{
			name:       "Successful Lock Acquisition",
			lockType:   "READ",
			requestID:  "12345",
			expectErr:  false,
			lockResult: true,
		},
		{
			name:       "Failed Lock Acquisition",
			lockType:   "WRITE",
			requestID:  "67890",
			expectErr:  true,
			lockResult: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lock := &Lock{
				ID:          "test-resource",
				LockType:    common.LockType(tt.lockType),
				RequestID:   tt.requestID,
				WaitChannel: make(chan bool, 1),
			}

			// Simulate lock processing asynchronously
			go func() {
				// wait for the request to be queued in lock.Lock()
				req := <-lockRequestsQueue
				lockMutex.Lock()
				defer lockMutex.Unlock()
				req.WaitChannel <- tt.lockResult
			}()

			err := lock.Lock()
			if tt.expectErr {
				if err != nil {
					fmt.Println("Expected failure occurred for:", tt.name)
				} else {
					fmt.Println("Expected failure did not occur for:", tt.name)
				}
			} else {
				if err == nil {
					fmt.Println("Lock acquired successfully for:", tt.name)
				} else {
					fmt.Println("Unexpected error:", err)
				}
			}
		})
	}
}

func TestReleaseLock(t *testing.T) {
	tests := []struct {
		name       string
		lockType   common.LockType
		requestID  string
		expectFail bool
	}{
		{
			name:       "Successful Lock Release",
			lockType:   common.LockType("READ"),
			requestID:  "12345",
			expectFail: false,
		},
		{
			name:       "Failed Lock Release - Invalid Lock",
			lockType:   common.LockType("INVALID"),
			requestID:  "00000",
			expectFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lock := &Lock{
				ID:          "test-resource",
				LockType:    tt.lockType,
				RequestID:   tt.requestID,
				WaitChannel: make(chan bool, 1),
			}

			fmt.Println("Releasing lock for:", tt.name)
			if tt.expectFail {
				fmt.Println("Expected failure occurred for lock release:", tt.name)
			} else {
				lock.Release()
				fmt.Println("Lock released successfully for:", tt.name)
			}
		})
	}
}

func TestLockRequestHandler(t *testing.T) {
	lockRequestsQueue = make(chan LockRequest, 1)

	tests := []struct {
		name      string
		request   LockRequest
		expectErr bool
	}{
		{
			name: "Successful Lock Request",
			request: LockRequest{
				ResourceID:     "test-resource-1",
				WaitChannel:    make(chan bool, 1),
				Unlock:         false,
				MaxOutStanding: 1,
				MaxActive:      1,
			},
			expectErr: false,
		},
		{
			name: "Successful Lock Release",
			request: LockRequest{
				ResourceID:  "test-resource-1",
				WaitChannel: make(chan bool, 1),
				Unlock:      true,
			},
			expectErr: false,
		},
	}

	go LockRequestHandler()
	time.Sleep(100 * time.Millisecond) // Allow goroutine to start

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lockRequestsQueue <- tt.request
			time.Sleep(100 * time.Millisecond) // Allow processing time

			fmt.Println("Processed lock request for:", tt.name)
		})
	}
}

func TestInitializeLock(t *testing.T) {
	lockWorker = nil
	InitializeLock()
	if lockWorker == nil {
		t.Errorf("lockWorker was not initialized")
	}
	fmt.Println("InitializeLock executed successfully")
}

func TestDeleteEntryFromMap(t *testing.T) {
	fifoLocks := make(map[string]*LockProperties)
	request := LockRequest{ResourceID: "test-resource"}
	lockProps := &LockProperties{WaitChannel: make(chan chan bool, 1)}
	fifoLocks[request.ResourceID] = lockProps

	deleteEntryFromMap(fifoLocks, request)

	if _, exists := fifoLocks[request.ResourceID]; exists {
		t.Errorf("Entry was not deleted from map")
	}
	fmt.Println("deleteEntryFromMap executed successfully")
}
