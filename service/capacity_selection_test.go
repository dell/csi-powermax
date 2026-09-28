/*
 Copyright © 2026 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// testTimeoutErr is a net.Error with Timeout() == true for use in
// isConnectivityError classification tests.
type testTimeoutErr struct{ msg string }

func (e testTimeoutErr) Error() string   { return e.msg }
func (e testTimeoutErr) Timeout() bool   { return true }
func (e testTimeoutErr) Temporary() bool { return false }

// --- capacityCache tests ---------------------------------------------------

func TestCapacityCache_SnapshotUnknownArrayIsAvailable(t *testing.T) {
	c := newCapacityCache()
	utilization, available, inFlight, known := c.snapshot("unknown-array")
	assert.False(t, known)
	assert.True(t, available)
	assert.Equal(t, 0.0, utilization)
	assert.Equal(t, int32(0), inFlight)
}

func TestCapacityCache_RecordPollSuccessMarksAvailable(t *testing.T) {
	c := newCapacityCache()
	now := time.Now()
	c.recordPollSuccess("array1", 42.5, now)

	utilization, available, _, known := c.snapshot("array1")
	assert.True(t, known)
	assert.True(t, available)
	assert.Equal(t, 42.5, utilization)
}

func TestCapacityCache_RecordPollFailureMarksUnavailableWhenStale(t *testing.T) {
	c := newCapacityCache()
	pollInterval := 5 * time.Minute
	base := time.Now()

	// First failure with no prior successful poll: immediately unavailable.
	c.recordPollFailure("array1", pollInterval, base)
	_, available, _, _ := c.snapshot("array1")
	assert.False(t, available)

	// Establish a successful poll, then a failure within the interval should
	// NOT mark unavailable yet.
	c.recordPollSuccess("array1", 10.0, base)
	c.recordPollFailure("array1", pollInterval, base.Add(1*time.Minute))
	_, available, _, _ = c.snapshot("array1")
	assert.True(t, available, "array should remain available within the poll interval after a single missed poll")

	// A failure after the poll interval has elapsed marks it unavailable.
	c.recordPollFailure("array1", pollInterval, base.Add(6*time.Minute))
	_, available, _, _ = c.snapshot("array1")
	assert.False(t, available)
}

func TestCapacityCache_RecoverImmediatelyOnNextSuccessfulPoll(t *testing.T) {
	c := newCapacityCache()
	pollInterval := 5 * time.Minute
	base := time.Now()

	c.recordPollFailure("array1", pollInterval, base)
	_, available, _, _ := c.snapshot("array1")
	assert.False(t, available)

	c.recordPollSuccess("array1", 5.0, base.Add(1*time.Second))
	_, available, _, _ = c.snapshot("array1")
	assert.True(t, available, "array must transition back to available immediately on the next successful poll")
}

func TestCapacityCache_InvalidateMarksUnavailableImmediately(t *testing.T) {
	c := newCapacityCache()
	c.recordPollSuccess("array1", 10.0, time.Now())
	c.invalidate("array1")

	_, available, _, _ := c.snapshot("array1")
	assert.False(t, available)
}

func TestCapacityCache_InFlightCounter(t *testing.T) {
	c := newCapacityCache()
	c.incInFlight("array1")
	c.incInFlight("array1")
	_, _, inFlight, _ := c.snapshot("array1")
	assert.Equal(t, int32(2), inFlight)

	c.decInFlight("array1")
	_, _, inFlight, _ = c.snapshot("array1")
	assert.Equal(t, int32(1), inFlight)
}

func TestCapacityCache_ConcurrentInFlightUpdates(t *testing.T) {
	c := newCapacityCache()
	const goroutines = 50
	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			c.incInFlight("array1")
			done <- struct{}{}
		}()
	}
	for i := 0; i < goroutines; i++ {
		<-done
	}
	_, _, inFlight, _ := c.snapshot("array1")
	assert.Equal(t, int32(goroutines), inFlight)
}

func TestCapacityCache_ConcurrentSnapshotAndRecordNoRace(_ *testing.T) {
	c := newCapacityCache()
	const iterations = 1000
	done := make(chan struct{}, 2)

	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < iterations; i++ {
			c.recordPollSuccess("array1", float64(i%100), time.Now())
		}
	}()

	go func() {
		defer func() { done <- struct{}{} }()
		for i := 0; i < iterations; i++ {
			c.snapshot("array1")
		}
	}()

	<-done
	<-done
}

// --- selectArray tests -------------------------------------------------------

func TestService_SelectArray_LowestUtilizationWins(t *testing.T) {
	s := &service{capCache: newCapacityCache()}
	now := time.Now()
	s.capCache.recordPollSuccess("array-a", 60.0, now)
	s.capCache.recordPollSuccess("array-b", 40.0, now)

	got, err := s.selectArray("us-east-1a", []string{"array-a", "array-b"})
	assert.NoError(t, err)
	assert.Equal(t, "array-b", got)
}

func TestService_SelectArray_InFlightTiebreaker(t *testing.T) {
	s := &service{capCache: newCapacityCache()}
	now := time.Now()
	s.capCache.recordPollSuccess("array-a", 50.0, now)
	s.capCache.recordPollSuccess("array-b", 50.0, now)
	s.capCache.incInFlight("array-a")
	s.capCache.incInFlight("array-a")

	got, err := s.selectArray("us-east-1a", []string{"array-a", "array-b"})
	assert.NoError(t, err)
	assert.Equal(t, "array-b", got, "array with fewer in-flight requests should win when utilization is tied")
}

func TestService_SelectArray_SkipsUnavailableArrays(t *testing.T) {
	s := &service{capCache: newCapacityCache()}
	now := time.Now()
	s.capCache.recordPollSuccess("array-a", 10.0, now)
	s.capCache.invalidate("array-a")
	s.capCache.recordPollSuccess("array-b", 90.0, now)

	got, err := s.selectArray("us-east-1a", []string{"array-a", "array-b"})
	assert.NoError(t, err)
	assert.Equal(t, "array-b", got, "should skip the unavailable array even though it has lower utilization")
}

func TestService_SelectArray_UnknownArraysTreatedAsAvailable(t *testing.T) {
	// No capacity cache data at all - both candidates are unknown to the
	// poller (e.g. before it has completed its first cycle). Selection must
	// still succeed deterministically instead of failing.
	s := &service{capCache: newCapacityCache()}
	got, err := s.selectArray("us-east-1a", []string{"array-b", "array-a"})
	assert.NoError(t, err)
	assert.Equal(t, "array-a", got, "deterministic tiebreak by array ID when utilization is otherwise equal/unknown")
}

func TestService_SelectArray_NilCacheFallsBackToDeterministicChoice(t *testing.T) {
	s := &service{capCache: nil}
	got, err := s.selectArray("us-east-1a", []string{"array-b", "array-a"})
	assert.NoError(t, err)
	assert.Equal(t, "array-a", got)
}

func TestService_SelectArray_AllUnavailableReturnsDistinctError(t *testing.T) {
	s := &service{capCache: newCapacityCache()}
	now := time.Now()
	s.capCache.recordPollSuccess("array-a", 10.0, now)
	s.capCache.invalidate("array-a")
	s.capCache.recordPollSuccess("array-b", 20.0, now)
	s.capCache.invalidate("array-b")

	got, err := s.selectArray("us-east-1a", []string{"array-a", "array-b"})
	assert.Empty(t, got)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, st.Code())
	assert.Contains(t, err.Error(), "all arrays in zone us-east-1a are unavailable")
	assert.Contains(t, err.Error(), "2 arrays attempted")
}

func TestService_SelectArray_NoCandidatesReturnsError(t *testing.T) {
	s := &service{capCache: newCapacityCache()}
	got, err := s.selectArray("us-east-1a", []string{})
	assert.Empty(t, got)
	assert.Error(t, err)
}

func TestService_SelectArray_SkipsArraysAtCapacityThreshold(t *testing.T) {
	s := &service{capCache: newCapacityCache(), capacityThresholdFull: 90}
	now := time.Now()
	s.capCache.recordPollSuccess("array-a", 95.0, now)
	s.capCache.recordPollSuccess("array-b", 10.0, now)

	got, err := s.selectArray("us-east-1a", []string{"array-a", "array-b"})
	assert.NoError(t, err)
	assert.Equal(t, "array-b", got, "should skip array at or above capacity-threshold-full")
}

func TestService_SelectArray_AllArraysAtCapacityThresholdReturnsError(t *testing.T) {
	s := &service{capCache: newCapacityCache(), capacityThresholdFull: 90}
	now := time.Now()
	s.capCache.recordPollSuccess("array-a", 95.0, now)
	s.capCache.recordPollSuccess("array-b", 100.0, now)

	got, err := s.selectArray("us-east-1a", []string{"array-a", "array-b"})
	assert.Empty(t, got)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, st.Code())
	assert.Contains(t, err.Error(), "all arrays in zone us-east-1a are unavailable")
}

func TestService_SelectArray_ZeroCapacityThresholdDoesNotFilter(t *testing.T) {
	s := &service{capCache: newCapacityCache(), capacityThresholdFull: 0}
	now := time.Now()
	s.capCache.recordPollSuccess("array-a", 100.0, now)

	got, err := s.selectArray("us-east-1a", []string{"array-a"})
	assert.NoError(t, err)
	assert.Equal(t, "array-a", got)
}

// --- isConnectivityError tests ----------------------------------------------

func TestIsConnectivityError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "connection refused", err: errors.New("dial tcp 10.0.0.1:8443: connect: connection refused"), want: true},
		{name: "no route to host", err: errors.New("dial tcp: no route to host"), want: true},
		{name: "i/o timeout", err: errors.New("read tcp: i/o timeout"), want: true},
		{name: "context deadline exceeded sentinel", err: context.DeadlineExceeded, want: true},
		{name: "io.EOF sentinel", err: io.EOF, want: true},
		{name: "io.ErrUnexpectedEOF sentinel", err: io.ErrUnexpectedEOF, want: true},
		{name: "net.Error timeout", err: testTimeoutErr{msg: "request timed out"}, want: true},
		{name: "parameter validation error", err: errors.New("invalid ServiceLevel parameter"), want: false},
		{name: "capacity error", err: errors.New("insufficient capacity in storage pool"), want: false},
		{name: "generic internal error", err: errors.New("internal server error"), want: false},
		{name: "non-connectivity string containing eof substring", err: errors.New("coefficient of eof mismatch"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, isConnectivityError(tt.err))
		})
	}
}

// --- allArraysUnavailableError tests -----------------------------------------

func TestAllArraysUnavailableError(t *testing.T) {
	err := allArraysUnavailableError("us-east-1a", 2)
	assert.Error(t, err)
	st, ok := status.FromError(err)
	assert.True(t, ok)
	assert.Equal(t, codes.ResourceExhausted, st.Code())
	assert.Contains(t, err.Error(), "all arrays in zone us-east-1a are unavailable")
	assert.Contains(t, err.Error(), "2 arrays attempted")

	// Distinct from a single-array failure error (different sentinel phrase).
	singleArrayErr := status.Errorf(codes.Internal, "array us-east-1a-array1 provisioning failed")
	assert.NotEqual(t, err.Error(), singleArrayErr.Error())
}

func TestAllArraysUnavailableError_EmptyZoneDefaultsToUnknown(t *testing.T) {
	err := allArraysUnavailableError("", 1)
	assert.Contains(t, err.Error(), "all arrays in zone unknown are unavailable")
}

// --- handleConnectivityFailover tests ----------------------------------------

func TestHandleConnectivityFailover_InvalidatesFailedArrayAndSelectsNext(t *testing.T) {
	s := &service{capCache: newCapacityCache()}
	now := time.Now()
	// Set up two arrays: array1 at 30%, array2 at 50%
	s.capCache.recordPollSuccess("array1", 30, now)
	s.capCache.recordPollSuccess("array2", 50, now)

	// Simulate failover from array1 (connectivity failure)
	nextArray, err := s.handleConnectivityFailover("us-east-1a", "array1", []string{"array1", "array2"}, 1)

	assert.NoError(t, err)
	assert.Equal(t, "array2", nextArray)

	// Verify array1 is now marked unavailable
	_, available, _, _ := s.capCache.snapshot("array1")
	assert.False(t, available, "failed array should be marked unavailable")
}

func TestHandleConnectivityFailover_ReturnsErrorWhenAllArraysUnavailable(t *testing.T) {
	s := &service{capCache: newCapacityCache()}
	now := time.Now()
	// Set up two arrays, both will become unavailable
	s.capCache.recordPollSuccess("array1", 30, now)
	s.capCache.recordPollSuccess("array2", 50, now)

	// First failover: array1 fails, select array2
	nextArray, err := s.handleConnectivityFailover("us-east-1a", "array1", []string{"array1", "array2"}, 1)
	assert.NoError(t, err)
	assert.Equal(t, "array2", nextArray)

	// Second failover: array2 also fails, no arrays left
	_, err = s.handleConnectivityFailover("us-east-1a", "array2", []string{"array1", "array2"}, 2)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "all arrays in zone us-east-1a are unavailable")
}

func TestHandleConnectivityFailover_RespectsRetryLimit(t *testing.T) {
	s := &service{capCache: newCapacityCache()}
	now := time.Now()
	// Set up many arrays
	for i := 1; i <= 5; i++ {
		s.capCache.recordPollSuccess(fmt.Sprintf("array%d", i), float64(i*10), now)
	}

	// Simulate hitting the retry limit
	_, err := s.handleConnectivityFailover("us-east-1a", "array3", []string{"array1", "array2", "array3", "array4", "array5"}, MaxFailoverRetries)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failover retry limit reached")
	assert.Contains(t, err.Error(), fmt.Sprintf("%d attempts", MaxFailoverRetries))
}

func TestHandleConnectivityFailover_NilCacheStillSelectsNext(t *testing.T) {
	s := &service{capCache: nil}

	// With nil cache, failover should still work (selectArray returns
	// deterministic choice based on sorted array IDs)
	nextArray, err := s.handleConnectivityFailover("us-east-1a", "array2", []string{"array1", "array2", "array3"}, 1)
	assert.NoError(t, err)
	// With nil cache, selectArray sorts and returns first available
	assert.Equal(t, "array1", nextArray)
}
