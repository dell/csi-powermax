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
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type volumeIDType string

var induceOverloadError bool // for testing only
var inducePendingError bool  //for testing only

// pendingState type limits the number of pending requests by making sure there are no other requests for the same volumeID,
// otherwise a "pending" error is returned.
// Additionally, no more than maxPending requests are processed at a time without returning an "overload" error.
type pendingState struct {
	maxPending   int
	npending     int
	pendingMutex sync.Mutex
	pendingMap   map[volumeIDType]time.Time
}

func (volID volumeIDType) checkAndUpdatePendingState(ps *pendingState) error {
	ps.pendingMutex.Lock()
	defer ps.pendingMutex.Unlock()
	if ps.pendingMap == nil {
		ps.pendingMap = make(map[volumeIDType]time.Time)
	}
	startTime := ps.pendingMap[volID]
	if startTime.IsZero() == false || inducePendingError {
		log.Infof("volumeID %s pending %s", volID, time.Now().Sub(startTime))
		return status.Errorf(codes.Unavailable, "pending")
	}
	if ps.maxPending > 0 && ps.npending >= ps.maxPending || induceOverloadError {
		return status.Errorf(codes.Unavailable, "overload")
	}
	ps.pendingMap[volID] = time.Now()
	ps.npending++
	return nil
}

func (volID volumeIDType) clearPending(ps *pendingState) {
	ps.pendingMutex.Lock()
	defer ps.pendingMutex.Unlock()
	if ps.pendingMap == nil {
		ps.pendingMap = make(map[volumeIDType]time.Time)
	}
	delete(ps.pendingMap, volID)
	ps.npending--
}
