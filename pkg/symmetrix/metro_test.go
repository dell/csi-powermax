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

package symmetrix

import (
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	"github.com/golang/mock/gomock"
)

func TestMetroClient_healthHandler(t *testing.T) {
	tests := []struct {
		name           string
		failureWeight  int32
		failureCount   int32
		lastFailure    time.Time
		expectedCount  int32
		expectedActive string
	}{
		{
			name:           "failure count below threshold",
			failureWeight:  1,
			failureCount:   1,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedCount:  2,
			expectedActive: "primaryArray",
		},
		{
			name:           "failure count at threshold",
			failureWeight:  1,
			failureCount:   failoverThereshold,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedCount:  6,
			expectedActive: "primaryArray",
		},
		{
			name:           "failure count above threshold",
			failureWeight:  1,
			failureCount:   failoverThereshold + 1,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedCount:  7,
			expectedActive: "primaryArray",
		},
		{
			name:           "last failure more than failure time threshold",
			failureWeight:  1,
			failureCount:   1,
			lastFailure:    time.Now().Add(-2 * time.Minute),
			expectedCount:  1,
			expectedActive: "primaryArray",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &metroClient{
				primaryArray:   "primaryArray",
				secondaryArray: "secondaryArray",
				activeArray:    "primaryArray",
				failureCount:   tt.failureCount,
				lastFailure:    tt.lastFailure,
			}

			m.healthHandler(tt.failureWeight)

			if m.failureCount != tt.expectedCount {
				t.Errorf("expected failure count %d, but got %d", tt.expectedCount, m.failureCount)
			}

			if m.activeArray != tt.expectedActive {
				t.Errorf("expected active array %s, but got %s", tt.expectedActive, m.activeArray)
			}
		})
	}
}

func TestTransport_RoundTrip(t *testing.T) {
	type args struct {
		req *http.Request
	}
	tests := []struct {
		name    string
		wantRes *http.Response
		wantErr bool
		err     error
	}{
		{
			name:    "Success",
			wantRes: &http.Response{},
			wantErr: false,
		},
		{
			name:    "Expected error",
			wantRes: &http.Response{},
			err:     errors.New("error"),
			wantErr: true,
		},
		{
			name:    "Response code 401",
			wantRes: &http.Response{StatusCode: 401},
			wantErr: false,
		},
		{
			name:    "Response code 500",
			wantRes: &http.Response{StatusCode: 500},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &metroClient{
				primaryArray:   "primaryArray",
				secondaryArray: "secondaryArray",
				activeArray:    "primaryArray",
				failureCount:   1,
				lastFailure:    time.Now().Add(-1 * time.Minute),
			}
			roundTripper := mocks.NewMockRoundTripperInterface(gomock.NewController(t))
			roundTripper.EXPECT().RoundTrip(&http.Request{}).Return(tt.wantRes, tt.err).AnyTimes()
			tr := &transport{
				roundTripper,
				m.healthHandler,
			}

			gotRes, err := tr.RoundTrip(&http.Request{})
			if (err != nil) != tt.wantErr {
				t.Errorf("RoundTrip() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotRes, tt.wantRes) {
				t.Errorf("RoundTrip() = %v, want %v", gotRes, tt.wantRes)
			}
		})
	}
}

func TestMetroClient_getActiveArray(t *testing.T) {
	tests := []struct {
		name           string
		failureWeight  int
		failureCount   int32
		lastFailure    time.Time
		expectedActive string
		activeArray    string
	}{
		{
			name:           "failure count below threshold",
			failureWeight:  1,
			failureCount:   1,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedActive: "primaryArray",
			activeArray:    "primaryArray",
		},
		{
			name:           "failure count at threshold",
			failureWeight:  1,
			failureCount:   failoverThereshold,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedActive: "secondaryArray",
			activeArray:    "primaryArray",
		},
		{
			name:           "failure count above threshold",
			failureWeight:  1,
			failureCount:   failoverThereshold + 1,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedActive: "secondaryArray",
			activeArray:    "primaryArray",
		},
		{
			name:           "last failure more than failure time threshold",
			failureWeight:  1,
			failureCount:   1,
			lastFailure:    time.Now().Add(-2 * time.Minute),
			expectedActive: "primaryArray",
			activeArray:    "primaryArray",
		},
		{
			name:           "failure count above threshold and active array was secondaryArray",
			failureWeight:  1,
			failureCount:   failoverThereshold + 1,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedActive: "primaryArray",
			activeArray:    "secondaryArray",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &metroClient{
				primaryArray:   "primaryArray",
				secondaryArray: "secondaryArray",
				activeArray:    tt.activeArray,
				failureCount:   tt.failureCount,
				lastFailure:    tt.lastFailure,
			}

			if got := m.getActiveArray(); got != tt.expectedActive {
				t.Errorf("getActiveArray() = %v, want %v", got, tt.expectedActive)
			}
		})
	}
}
