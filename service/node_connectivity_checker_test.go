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
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/dell/gopowermax/v2/mock"
)

func TestApiRouter2(t *testing.T) {
	// An invalid port must make the server return instead of starting a listener.
	done := make(chan struct{})
	s.opts.PodmonPort = ":abc"
	go func() {
		s.apiRouter(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("apiRouter did not return for an invalid port")
	}
}

func TestApiRouter(t *testing.T) {
	// Use a dynamic port to avoid conflicts
	listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
	if listenErr != nil {
		t.Fatalf("Failed to allocate port: %v", listenErr)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	s.opts.PodmonPort = fmt.Sprintf(":%d", port)
	go s.apiRouter(context.Background())

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	// Wait for the server to start accepting connections
	var resp4 *http.Response
	var err error
	for i := 0; i < 40; i++ {
		resp4, err = http.Get(baseURL + "/array-status")
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || resp4.StatusCode != 500 {
		t.Errorf("Error while probing array status %v", err)
	}
	// fill some invalid dummy data in the cache and try to fetch
	s.newProbeStatus()
	s.probeStatus.Store("SymID2", "status")

	resp5, err := http.Get(baseURL + "/array-status")
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
	resp2, err := http.Get(baseURL + "/array-status")
	if err != nil || resp2.StatusCode != 200 {
		t.Errorf("Error while probing array status %v", err)
	}

	resp3, err := http.Get(baseURL + "/array-status/SymIDNotPresent")
	if err != nil || resp3.StatusCode != 404 {
		t.Errorf("Error while probing array status %v", err)
	}
	value := make(chan int)
	s.probeStatus.Store("SymID3", value)
	resp9, err := http.Get(baseURL + "/array-status/SymID3")
	if err != nil || resp9.StatusCode != 500 {
		t.Errorf("Error while probing array status %v", err)
	}
	resp10, err := http.Get(baseURL + "/array-status/SymID")
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
		t.Run(tt.name, func(_ *testing.T) {
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
		t.Run(tt.name, func(_ *testing.T) {
			// Set the global probeStatus for the test
			if tt.probeStatus != nil {
				s.newProbeStatus()
				tt.probeStatus.Range(func(key, value interface{}) bool {
					s.probeStatus.Store(key, value)
					return true
				})
			} else {
				s.probeStatus = nil
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

// TestPodmonAuthMiddleware manipulates the package-level PodmonAPIToken; it must not run in parallel.
func TestPodmonAuthMiddleware(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tests := []struct {
		name       string
		token      string
		authHeader string
		wantStatus int
	}{
		{"no token configured", "", "", http.StatusOK},
		{"valid bearer token", "test-token", "Bearer test-token", http.StatusOK},
		{"valid bearer token with extra whitespace", "test-token", "Bearer test-token   ", http.StatusOK},
		{"valid lowercase bearer token (RFC 6750)", "test-token", "bearer test-token", http.StatusOK},
		{"valid mixed case bearer token (RFC 6750)", "test-token", "BEARER test-token", http.StatusOK},
		{"missing authorization header", "test-token", "", http.StatusUnauthorized},
		{"invalid bearer token", "test-token", "Bearer wrong-token", http.StatusUnauthorized},
		{"malformed authorization header", "test-token", "test-token", http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := PodmonAPIToken
			PodmonAPIToken = tt.token
			defer func() { PodmonAPIToken = old }()

			req := httptest.NewRequest(http.MethodGet, "/array-status", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()

			podmonAuthMiddleware(next).ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

/*
func TestNodeConnectivityCheckerErrorPaths(t *testing.T) {
	tests := []struct {
		name        string
		setupMock   func()
		expectError bool
	}{
		{
			name: "Error checking array connectivity",
			setupMock: func() {
				s.newProbeStatus()
			},
			expectError: true,
		},
		{
			name: "Error with invalid probe status data",
			setupMock: func() {
				s.newProbeStatus()
				s.probeStatus.Store("invalid", make(chan int))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			tt.setupMock()
			_ = tt.expectError
		})
	}
}
*/
