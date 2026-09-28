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

// Package service implements the multi-array, capacity-based array selection
// algorithm (Capacity-Based Array Selection & Health Checking).
package service

import (
	"context"
	"errors"
	"io"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dell/csmlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// arrayCapacityEntry holds the cached capacity/availability state for a
// single PowerMax array, refreshed by the background capacity poller
// (see capacity_poller.go) and by immediate cache invalidation on
// connectivity-class provisioning failures.
type arrayCapacityEntry struct {
	utilizationPercent float64
	lastPollTime       time.Time
	available          bool
	inFlight           int32 // accessed via sync/atomic; do not touch directly
	warnedAtOrAbove    bool  // whether the capacity-threshold warning event has already fired for the current above-threshold streak
}

// capacityCache tracks per-array capacity/availability state across all
// zones. It is keyed by array serial number (not by zone) because zone
// membership is already resolved by the existing topology-matching logic
// in checkTopologyRequirements; the cache only needs to answer "is this
// array healthy and how full is it" for a given candidate list.
//
// capacityCache is safe for concurrent use.
type capacityCache struct {
	mu     sync.RWMutex
	arrays map[string]*arrayCapacityEntry
}

func newCapacityCache() *capacityCache {
	return &capacityCache{arrays: make(map[string]*arrayCapacityEntry)}
}

// getOrCreate returns the entry for arrayID, creating one (defaulting to
// available=true) if it does not already exist. Arrays default to
// available so they remain usable before the first poll cycle completes.
func (c *capacityCache) getOrCreate(arrayID string) *arrayCapacityEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.arrays[arrayID]
	if !ok {
		e = &arrayCapacityEntry{available: true}
		c.arrays[arrayID] = e
	}
	return e
}

// snapshot returns the current utilization/availability/in-flight count for
// arrayID. known is false when the array has no cache entry yet (e.g. the
// poller has not completed a first cycle); such arrays are reported as
// available with 0% utilization so callers can still select them.
func (c *capacityCache) snapshot(arrayID string) (utilizationPercent float64, available bool, inFlight int32, known bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.arrays[arrayID]
	if !ok {
		return 0, true, 0, false
	}
	return e.utilizationPercent, e.available, atomic.LoadInt32(&e.inFlight), true
}

// recordPollSuccess updates the cached utilization for arrayID with the
// result of a successful capacity poll and marks it available. Recovery
// from a prior unavailable state is immediate.
func (c *capacityCache) recordPollSuccess(arrayID string, utilizationPercent float64, now time.Time) {
	e := c.getOrCreate(arrayID)
	c.mu.Lock()
	e.utilizationPercent = utilizationPercent
	e.lastPollTime = now
	e.available = true
	c.mu.Unlock()
}

// recordPollFailure records a failed capacity poll attempt for arrayID. The
// array is marked unavailable if no successful poll has completed within
// pollInterval of now (or if there has never been a successful poll).
func (c *capacityCache) recordPollFailure(arrayID string, pollInterval time.Duration, now time.Time) {
	e := c.getOrCreate(arrayID)
	c.mu.Lock()
	if e.lastPollTime.IsZero() || now.Sub(e.lastPollTime) > pollInterval {
		e.available = false
	}
	c.mu.Unlock()
}

// invalidate immediately marks arrayID unavailable. Used when a
// connectivity-class provisioning error is observed outside the normal
// poll cycle, to prevent retry storms against a known-bad array.
func (c *capacityCache) invalidate(arrayID string) {
	e := c.getOrCreate(arrayID)
	c.mu.Lock()
	e.available = false
	c.mu.Unlock()
}

// incInFlight/decInFlight adjust the in-flight provisioning counter for
// arrayID. The counter is used as a tiebreaker to distribute concurrent
// requests across arrays with equal capacity utilization.
func (c *capacityCache) incInFlight(arrayID string) {
	e := c.getOrCreate(arrayID)
	atomic.AddInt32(&e.inFlight, 1)
}

func (c *capacityCache) decInFlight(arrayID string) {
	e := c.getOrCreate(arrayID)
	atomic.AddInt32(&e.inFlight, -1)
}

// crossedCapacityWarningThreshold reports whether utilizationPercent has
// just crossed thresholdPercent from below, and records the crossing so the
// warning is only emitted once per above-threshold streak (not on every
// poll while the array remains above the threshold).
func (c *capacityCache) crossedCapacityWarningThreshold(arrayID string, utilizationPercent, thresholdPercent float64) bool {
	e := c.getOrCreate(arrayID)
	c.mu.Lock()
	defer c.mu.Unlock()
	above := utilizationPercent >= thresholdPercent
	crossed := above && !e.warnedAtOrAbove
	e.warnedAtOrAbove = above
	return crossed
}

// arrayCandidate is a lightweight view of a candidate array used for
// sorting during selection.
type arrayCandidate struct {
	id                 string
	utilizationPercent float64
	inFlight           int32
}

// selectArray picks the best array from candidates (all of which already
// matched the requested zone topology via checkTopologyRequirements) based
// on lowest capacity utilization, using the in-flight provisioning counter
// as a tiebreaker, and array ID as a final deterministic tiebreaker.
//
// zone is used only to build a clear all-arrays-unavailable error message
// and may be empty (falls back to "unknown").
//
// If the service has no capacity cache wired up (capacity polling not
// initialized - e.g. some backward-compatible or test code paths), a
// deterministic choice is still returned rather than failing, to preserve
// existing single-array behavior.
func (s *service) selectArray(zone string, candidates []string) (string, error) {
	// FR-3: measure only the in-memory selection duration (excludes any
	// Unisphere API calls, which selection never makes - it uses cached
	// data). Observed for every selection, including the fallback and
	// all-unavailable paths.
	start := time.Now()
	defer func() { s.multiArrayMetrics.observeSelectionLatency(zone, time.Since(start)) }()

	if len(candidates) == 0 {
		return "", allArraysUnavailableError(zone, 0)
	}

	if s.capCache == nil {
		sorted := append([]string(nil), candidates...)
		sort.Strings(sorted)
		s.logArraySelected(zone, sorted[0], nil)
		return sorted[0], nil
	}

	available := make([]arrayCandidate, 0, len(candidates))
	skipped := make([]skippedArray, 0, len(candidates))
	for _, id := range candidates {
		utilizationPercent, isAvailable, inFlight, _ := s.capCache.snapshot(id)
		if !isAvailable {
			skipped = append(skipped, skippedArray{id: id, reason: reasonArrayUnreachable})
			continue
		}
		if s.capacityThresholdFull > 0 && utilizationPercent >= s.capacityThresholdFull {
			skipped = append(skipped, skippedArray{id: id, reason: reasonCapacityExhausted})
			continue
		}
		available = append(available, arrayCandidate{id: id, utilizationPercent: utilizationPercent, inFlight: inFlight})
	}

	if len(available) == 0 {
		s.logAllArraysUnavailable(zone, skipped)
		return "", allArraysUnavailableError(zone, len(candidates))
	}

	sort.Slice(available, func(i, j int) bool {
		if available[i].utilizationPercent != available[j].utilizationPercent {
			return available[i].utilizationPercent < available[j].utilizationPercent
		}
		if available[i].inFlight != available[j].inFlight {
			return available[i].inFlight < available[j].inFlight
		}
		return available[i].id < available[j].id
	})

	selected := available[0].id
	s.logArraySelected(zone, selected, skipped)
	return selected, nil
}

// skippedArray records a candidate array that was not selected and why, for
// the FR-3 §2.3.1 operational logs.
type skippedArray struct {
	id     string
	reason string
}

// logArraySelected logs a successful array selection at INFO level and any
// skipped-array decisions at WARN level with their reason.
func (s *service) logArraySelected(zone, selected string, skipped []skippedArray) {
	csmlog.WithFields(csmlog.Fields{
		"zone":  zone,
		"array": selected,
	}).Infof("selected array %s for zone %s", selected, zone)

	for _, sk := range skipped {
		csmlog.WithFields(csmlog.Fields{
			"zone":           zone,
			"skipped_array":  sk.id,
			"selected_array": selected,
			"reason":         sk.reason,
		}).Warnf("array %s skipped in zone %s (reason=%s); selected %s", sk.id, zone, sk.reason, selected)
	}
}

// logAllArraysUnavailable logs the all-arrays-unavailable condition at ERROR
// level with the zone, the count of arrays attempted, and a per-array failure
// reason.
func (s *service) logAllArraysUnavailable(zone string, skipped []skippedArray) {
	fields := csmlog.Fields{
		"zone":             zone,
		"arrays_attempted": len(skipped),
	}
	for _, sk := range skipped {
		fields["array."+sk.id] = sk.reason
	}
	csmlog.WithFields(fields).Errorf("all %d arrays in zone %s are unavailable", len(skipped), zone)
}

// allArraysUnavailableError builds the distinct error required by AC-006
// when every candidate array in a zone is unavailable. The sentinel phrase
// "all arrays in zone <zone> are unavailable" and the attempted-array count
// distinguish this from a single-array failure error.
func allArraysUnavailableError(zone string, attempted int) error {
	if zone == "" {
		zone = "unknown"
	}
	return status.Errorf(codes.ResourceExhausted,
		"all arrays in zone %s are unavailable (%d arrays attempted)", zone, attempted)
}

// connectivityErrorMarkers are substrings (checked case-insensitively)
// indicative of a connectivity/unreachability failure talking to Unisphere,
// as opposed to a non-connectivity failure (parameter validation, capacity,
// configuration).
var connectivityErrorMarkers = []string{
	"connection refused",
	"no route to host",
	"i/o timeout",
	"connection reset",
	"no such host",
	"network is unreachable",
	"dial tcp",
}

// isArrayUnavailable reports whether arrayID is known to the capacity
// cache and marked unavailable or is at/above the configured full capacity
// threshold. Used to reject StorageClass-targeted (SYMID) provisioning
// requests against a known-bad array without silently falling back to
// pool-based selection (AC-007). An array with no cache entry (unknown) is
// treated as available, consistent with selectArray's treatment of unpolled
// arrays.
func (s *service) isArrayUnavailable(arrayID string) (unavailable bool, reason string) {
	if s.capCache == nil {
		return false, ""
	}
	utilization, available, _, known := s.capCache.snapshot(arrayID)
	if !known {
		return false, ""
	}
	if !available {
		return true, "no successful capacity/health poll within the configured capacity-poll-interval"
	}
	if s.capacityThresholdFull > 0 && utilization >= s.capacityThresholdFull {
		return true, "array is at or above the configured capacity-threshold-full"
	}
	return false, ""
}

// isConnectivityError classifies a provisioning error returned while
// talking to Unisphere for a given array. Connectivity-class errors trigger
// immediate failover to the next available array in the zone (and mark the
// array unavailable in the capacity cache). Non-connectivity errors
// (parameter validation, capacity, configuration) are returned to the
// caller as-is, with no failover and no change to the array's availability.
func isConnectivityError(err error) bool {
	if err == nil {
		return false
	}

	// Classify well-known network/timeout sentinels before string matching,
	// avoiding false positives from short substrings.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	msg := strings.ToLower(err.Error())
	for _, marker := range connectivityErrorMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// MaxFailoverRetries is the maximum number of arrays to try before giving up
// on a provisioning request. This prevents infinite retry loops when all
// arrays in a zone are experiencing connectivity issues.
const MaxFailoverRetries = 3

// handleConnectivityFailover is called when a connectivity error occurs
// during provisioning. It invalidates the failed array's cache entry and
// selects the next available array from the zone's candidate pool. Returns
// the next array to try, or an error if no more arrays are available or
// the retry limit has been reached.
//
// Parameters:
//   - zone: the zone label for logging and error messages
//   - failedArrayID: the array that just failed with a connectivity error
//   - candidates: the original list of candidate arrays for this zone
//   - attemptCount: how many arrays have been tried so far (1-indexed)
//
// Returns:
//   - nextArrayID: the next array to try (empty if none available)
//   - err: non-nil if no more arrays are available or retry limit reached
func (s *service) handleConnectivityFailover(zone, failedArrayID string, candidates []string, attemptCount int) (string, error) {
	// Invalidate the failed array's cache entry to prevent retry storms
	// and to ensure it's excluded from future selection until the next
	// successful capacity poll.
	if s.capCache != nil {
		s.capCache.invalidate(failedArrayID)
		csmlog.WithFields(csmlog.Fields{
			"zone":         zone,
			"failed_array": failedArrayID,
			"attempt":      attemptCount,
			"reason":       reasonArrayUnreachable,
		}).Warnf("array %s marked unavailable after connectivity failure (attempt %d)", failedArrayID, attemptCount)
	}

	// Check retry limit
	if attemptCount >= MaxFailoverRetries {
		csmlog.WithFields(csmlog.Fields{
			"zone":         zone,
			"failed_array": failedArrayID,
			"max_retries":  MaxFailoverRetries,
		}).Errorf("failover retry limit reached after %d attempts", attemptCount)
		return "", status.Errorf(codes.ResourceExhausted,
			"failover retry limit reached after %d attempts in zone %s", attemptCount, zone)
	}

	// Select the next available array (excludes the now-invalidated failed array)
	nextArrayID, err := s.selectArray(zone, candidates)
	if err != nil {
		// All arrays are now unavailable
		return "", err
	}

	csmlog.WithFields(csmlog.Fields{
		"zone":         zone,
		"failed_array": failedArrayID,
		"next_array":   nextArrayID,
		"attempt":      attemptCount + 1,
	}).Infof("failing over from array %s to array %s (attempt %d)", failedArrayID, nextArrayID, attemptCount+1)

	return nextArrayID, nil
}
