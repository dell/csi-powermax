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

// Package service implements the background capacity polling loop. It refreshes
// per-array free physical capacity utilization and availability at
// capacity-poll-interval cadence, and emits a Kubernetes warning event when an
// array's utilization crosses (capacity-threshold-full - 10) percent.
package service

import (
	"context"
	"strconv"
	"time"

	"github.com/dell/csmlog"
	csictx "github.com/dell/gocsi/context"
	pmax "github.com/dell/gopowermax/v2"
	corev1 "k8s.io/api/core/v1"
)

// Defaults for multi-array zone capacity polling.
const (
	DefaultCapacityPollInterval  = 5 * time.Minute
	DefaultCapacityThresholdFull = 100.0
	capacityWarningOffsetPercent = 10.0
)

// resolveCapacityPollInterval reads EnvCapacityPollInterval (a Go duration
// string, e.g. "5m") and falls back to DefaultCapacityPollInterval when
// unset or invalid.
func resolveCapacityPollInterval() time.Duration {
	raw, ok := csictx.LookupEnv(context.Background(), EnvCapacityPollInterval)
	if !ok || raw == "" {
		return DefaultCapacityPollInterval
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		csmlog.Warnf("%s value %q is not a valid duration, using default %s", EnvCapacityPollInterval, raw, DefaultCapacityPollInterval)
		return DefaultCapacityPollInterval
	}
	if d <= 0 {
		csmlog.Warnf("%s value %q is not a positive duration, using default %s", EnvCapacityPollInterval, raw, DefaultCapacityPollInterval)
		return DefaultCapacityPollInterval
	}
	return d
}

// resolveCapacityThresholdFull reads EnvCapacityThresholdFull (a percentage,
// 0-100) and falls back to DefaultCapacityThresholdFull when unset or
// invalid.
func resolveCapacityThresholdFull() float64 {
	raw, ok := csictx.LookupEnv(context.Background(), EnvCapacityThresholdFull)
	if !ok || raw == "" {
		return DefaultCapacityThresholdFull
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		csmlog.Warnf("%s value %q is not a valid number, using default %.0f", EnvCapacityThresholdFull, raw, DefaultCapacityThresholdFull)
		return DefaultCapacityThresholdFull
	}
	if v < 0 {
		csmlog.Warnf("%s value %q is below 0, clamping to 0", EnvCapacityThresholdFull, raw)
		v = 0
	} else if v > 100 {
		csmlog.Warnf("%s value %q is above 100, clamping to 100", EnvCapacityThresholdFull, raw)
		v = 100
	}
	return v
}

// capacityFetchFunc queries the current free-capacity utilization
// percentage for a single array. Implemented in production by
// (*service).fetchArrayCapacityUtilization; swappable in tests.
type capacityFetchFunc func(ctx context.Context, arrayID string) (utilizationPercent float64, err error)

// capacityWarningFunc is invoked when an array's utilization crosses the
// warning threshold for the first time in an above-threshold streak.
// Implemented in production by (*service).emitCapacityWarningEvent.
type capacityWarningFunc func(arrayID string, utilizationPercent, thresholdPercent float64)

// runCapacityPollCycle performs a single poll of all arrayIDs, updating
// cache and invoking warn (if non-nil) when an array crosses
// (thresholdFullPercent - capacityWarningOffsetPercent).
func runCapacityPollCycle(ctx context.Context, arrayIDs []string, cache *capacityCache, fetch capacityFetchFunc, pollInterval time.Duration, thresholdFullPercent float64, warn capacityWarningFunc) {
	now := time.Now()
	warningThreshold := thresholdFullPercent - capacityWarningOffsetPercent

	for _, arrayID := range arrayIDs {
		utilizationPercent, err := fetch(ctx, arrayID)
		if err != nil {
			csmlog.Warnf("capacity poll failed for array %s: %v", arrayID, err)
			cache.recordPollFailure(arrayID, pollInterval, now)
			continue
		}

		cache.recordPollSuccess(arrayID, utilizationPercent, now)

		if warn != nil && cache.crossedCapacityWarningThreshold(arrayID, utilizationPercent, warningThreshold) {
			warn(arrayID, utilizationPercent, thresholdFullPercent)
		}
	}
}

// startCapacityPoller launches the background capacity polling goroutine
// for arrayIDs. It returns a cancel function the caller must invoke on
// driver shutdown to stop the goroutine cleanly. Safe to call with an empty
// arrayIDs slice (e.g. no storage arrays configured); the goroutine will
// simply perform no-op cycles.
func (s *service) startCapacityPoller(ctx context.Context, arrayIDs []string) context.CancelFunc {
	pollCtx, cancel := context.WithCancel(ctx)
	pollInterval := resolveCapacityPollInterval()
	thresholdFull := resolveCapacityThresholdFull()

	s.capCache = newCapacityCache()
	s.capacityPollInterval = pollInterval
	s.capacityThresholdFull = thresholdFull

	csmlog.Infof("Starting capacity poller: interval=%s thresholdFull=%.0f%% arrays=%v", pollInterval, thresholdFull, arrayIDs)

	go func() {
		ticker := time.NewTicker(pollInterval)
		defer ticker.Stop()

		// Run one cycle immediately so arrays have a capacity/health
		// verdict as soon as possible after startup, rather than waiting a
		// full interval for the first tick.
		runCapacityPollCycle(pollCtx, arrayIDs, s.capCache, s.fetchArrayCapacityUtilization, pollInterval, thresholdFull, s.emitCapacityWarningEvent)
		s.syncCapacityMetricsFromCache(arrayIDs)

		for {
			select {
			case <-pollCtx.Done():
				return
			case <-ticker.C:
				runCapacityPollCycle(pollCtx, arrayIDs, s.capCache, s.fetchArrayCapacityUtilization, pollInterval, thresholdFull, s.emitCapacityWarningEvent)
				s.syncCapacityMetricsFromCache(arrayIDs)
			}
		}
	}()

	return cancel
}

// fetchArrayCapacityUtilization queries Unisphere for the free physical
// capacity utilization percentage of arrayID, aggregating across all SRPs
// on the array (SRP-level filtering is deferred to a future increment).
// Returns an error if the array's PowerMax client cannot be resolved or the
// storage pool list/details cannot be retrieved (classified by
// isConnectivityError for availability purposes).
func (s *service) fetchArrayCapacityUtilization(ctx context.Context, arrayID string) (float64, error) {
	pmaxClient, err := s.GetPowerMaxClient(arrayID)
	if err != nil {
		return 0, err
	}
	return aggregateArrayCapacityUtilization(ctx, pmaxClient, arrayID)
}

// aggregateArrayCapacityUtilization sums usable-total/usable-used free
// physical capacity (types.SrpCap) across every SRP on arrayID and returns
// the overall utilization percentage. Extracted from
// fetchArrayCapacityUtilization so it can be unit tested against a mock
// pmax.Pmax client without needing the package-level symmetrix client
// registry that GetPowerMaxClient relies on.
func aggregateArrayCapacityUtilization(ctx context.Context, pmaxClient pmax.Pmax, arrayID string) (float64, error) {
	pools, err := pmaxClient.GetStoragePoolList(ctx, arrayID)
	if err != nil {
		return 0, err
	}

	var totalTB, usedTB float64
	var usablePools int
	for _, poolID := range pools.StoragePoolIDs {
		pool, poolErr := pmaxClient.GetStoragePool(ctx, arrayID, poolID)
		if poolErr != nil {
			csmlog.Warnf("capacity poll: failed to get storage pool %s on array %s: %v", poolID, arrayID, poolErr)
			continue
		}
		if pool == nil || pool.SrpCap == nil {
			continue
		}
		if pool.SrpCap.UsableTotInTB <= 0 {
			continue
		}
		totalTB += pool.SrpCap.UsableTotInTB
		usedTB += pool.SrpCap.UsableUsedInTB
		usablePools++
	}

	// If no usable pools or zero total capacity, return 0% utilization rather
	// than an error. This allows arrays without capacity data (e.g., during
	// initial startup or with certain configurations) to remain available for
	// selection. The array will be treated as having 0% utilization.
	if usablePools == 0 || totalTB <= 0 {
		csmlog.Warnf("capacity poll: no usable SRP capacity data for array %s (usablePools=%d, totalTB=%.2f); treating as 0%% utilization", arrayID, usablePools, totalTB)
		return 0, nil
	}
	return (usedTB / totalTB) * 100, nil
}

// emitCapacityWarningEvent posts a Kubernetes warning event when arrayID's
// utilization crosses the capacity-threshold-full warning point.
// Best-effort: if no event recorder is available, the warning is logged
// only.
func (s *service) emitCapacityWarningEvent(arrayID string, utilizationPercent, thresholdPercent float64) {
	msg := csmlog.Fields{
		"array":       arrayID,
		"utilization": utilizationPercent,
		"threshold":   thresholdPercent,
	}
	csmlog.WithFields(msg).Warnf("Array %s utilization %.1f%% is approaching the full threshold of %.0f%%", arrayID, utilizationPercent, thresholdPercent)

	recorder := initEventRecorder()
	if recorder == nil {
		return
	}
	// PowerMax arrays are not represented as a Kubernetes object the driver
	// owns, so the event is posted against a synthetic reference identifying
	// the array; this mirrors the informational nature of the FR-2
	// "capacity-threshold-full - 10%" warning requirement.
	arrayRef := &corev1.ObjectReference{
		Kind: "PowerMaxArray",
		Name: arrayID,
	}
	recorder.Eventf(arrayRef, corev1.EventTypeWarning, "CapacityApproachingFull",
		"Array %s utilization %.1f%% is approaching the full threshold of %.0f%%", arrayID, utilizationPercent, thresholdPercent)
}
