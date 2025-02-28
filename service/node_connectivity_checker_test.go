/*
 *
 * Copyright © 2022-2024 Dell Inc. or its subsidiaries. All Rights Reserved.
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

package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dell/gopowermax/v2/mock"
)

func TestApiRouter2(t *testing.T) {
	// server should not be up and running
	s.opts.PodmonPort = ":abc"
	s.apiRouter(context.Background())

	resp, err := http.Get("http://localhost:8083/node-status")
	if err == nil || resp != nil {
		t.Errorf("Error while probing node status")
	}
}

func TestApiRouter(t *testing.T) {
	s.opts.PodmonPort = ":8083"
	go s.apiRouter(context.Background())
	time.Sleep(2 * time.Second)

	resp4, err := http.Get("http://localhost:8083/array-status")
	if err != nil || resp4.StatusCode != 500 {
		t.Errorf("Error while probing array status %v", err)
	}
	// fill some invalid dummy data in the cache and try to fetch
	s.newProbeStatus()
	s.probeStatus.Store("SymID2", "status")

	resp5, err := http.Get("http://localhost:8083/array-status")
	if err != nil || resp5.StatusCode != 500 {
		t.Errorf("Error while probing array status %v, %d", err, resp5.StatusCode)
	}

	// fill some dummy data in the cache and try to fetch
	var status ArrayConnectivityStatus
	status.LastSuccess = time.Now().Unix()
	status.LastAttempt = time.Now().Unix()
	s.newProbeStatus()
	s.probeStatus.Store("SymID", status)

	// array status
	resp2, err := http.Get("http://localhost:8083/array-status")
	if err != nil || resp2.StatusCode != 200 {
		t.Errorf("Error while probing array status %v", err)
	}

	resp3, err := http.Get("http://localhost:8083/array-status/SymIDNotPresent")
	if err != nil || resp3.StatusCode != 404 {
		t.Errorf("Error while probing array status %v", err)
	}
	value := make(chan int)
	s.probeStatus.Store("SymID3", value)
	resp9, err := http.Get("http://localhost:8083/array-status/SymID3")
	if err != nil || resp9.StatusCode != 500 {
		t.Errorf("Error while probing array status %v", err)
	}
	resp10, err := http.Get("http://localhost:8083/array-status/SymID")
	if err != nil || resp10.StatusCode != 200 {
		t.Errorf("Error while probing array status %v", err)
	}
}

func TestMarshalSyncMapToJSON(t *testing.T) {
	type args struct {
		m *sync.Map
	}
	sample := new(sync.Map)
	sample2 := new(sync.Map)
	var status ArrayConnectivityStatus
	status.LastSuccess = time.Now().Unix()
	status.LastAttempt = time.Now().Unix()

	sample.Store("SymID", status)
	sample2.Store("key", "2.adasd")

	tests := []struct {
		name string
		args args
	}{
		{"storing valid value in map cache", args{m: sample}},
		{"storing valid value in map cache", args{m: sample2}},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, _ := MarshalSyncMapToJSON(tt.args.m)
			if len(data) == 0 && i == 0 {
				t.Errorf("MarshalSyncMapToJSON() expecting some data from cache in the response")
				return
			}
		})
	}
}

func TestStartAPIService(_ *testing.T) {
	s.opts.IsPodmonEnabled = true
	s.opts.ManagedArrays = []string{mock.DefaultSymmetrixID}
	s.startAPIService(context.Background())
}

func TestStartAPIServiceNoPodmon(_ *testing.T) {
	s.opts.IsPodmonEnabled = false
	s.startAPIService(context.Background())
}

func TestConnectivityStatus(t *testing.T) {
	// Initialize the probeStatus variable

	// Create a valid ArrayConnectivityStatus instance
	status := ArrayConnectivityStatus{
		LastSuccess: time.Now().Unix(),
		LastAttempt: time.Now().Unix(),
	}

	// Store valid data in probeStatus
	s.probeStatus.Store("SymID", status)

	// Test cases
	tests := []struct {
		name         string
		probeStatus  *sync.Map
		expectedCode int
	}{
		{
			name:         "Empty probeStatus",
			probeStatus:  nil,
			expectedCode: http.StatusInternalServerError,
		},
		{
			name:         "Valid probeStatus",
			probeStatus:  s.probeStatus,
			expectedCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set the global probeStatus for the test
			s.newProbeStatus()

			if tt.probeStatus != nil {
				tt.probeStatus.Range(func(key, value interface{}) bool {
					s.probeStatus.Store(key, value)
					return true
				})
			}

			// Create a response recorder
			recorder := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/connectivityStatus", nil)

			// Call the function
			s.connectivityStatus(recorder, req)

			// Check the response code
			if recorder.Code != tt.expectedCode {
				t.Errorf("expected %d, got %d", tt.expectedCode, recorder.Code)
			}
		})
	}
}
