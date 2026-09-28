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

package symmetrix

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dell/csmlog"
)

// ErrUnsafeRDFState is returned by reconcileMetroPairing when the SRDF pair
// is in an unsafe state (Split, Mixed, Invalid, Unknown).  ReconcileDeferredOperations
// treats this as a hard failure: the operation is removed from the journal and
// the caller is expected to emit a Kubernetes hard-error event (FR-3.2, NFR-2).
var ErrUnsafeRDFState = errors.New("SRDF pair is in an unsafe state")

// UnsafeRDFStateError wraps ErrUnsafeRDFState and carries the state string
// so callers can include it in event messages.
type UnsafeRDFStateError struct {
	State string
}

func (e *UnsafeRDFStateError) Error() string {
	return fmt.Sprintf("%s: %q", ErrUnsafeRDFState, e.State)
}

func (e *UnsafeRDFStateError) Unwrap() error { return ErrUnsafeRDFState }

// IsUnsafeRDFState reports whether err is an UnsafeRDFStateError.
func IsUnsafeRDFState(err error) bool {
	return errors.Is(err, ErrUnsafeRDFState)
}

// MetroSiteState represents the connectivity state of a Metro array.
type MetroSiteState int

const (
	// SiteUnknown is the initial state before any check.
	SiteUnknown MetroSiteState = iota
	// SiteReachable means the array is responding to API calls.
	SiteReachable
	// SiteUnreachable means the array is not responding.
	SiteUnreachable
)

// String returns a human-readable label for the site state.
func (s MetroSiteState) String() string {
	switch s {
	case SiteReachable:
		return "Reachable"
	case SiteUnreachable:
		return "Unreachable"
	default:
		return "Unknown"
	}
}

// SiteStateTracker maintains the last-known connectivity state for each
// array and detects transitions (unreachable → reachable) that should
// trigger reconciliation.
type SiteStateTracker struct {
	mu     sync.Mutex
	states map[string]MetroSiteState
}

// NewSiteStateTracker creates a new tracker with all arrays in Unknown state.
func NewSiteStateTracker() *SiteStateTracker {
	return &SiteStateTracker{
		states: make(map[string]MetroSiteState),
	}
}

// UpdateState records the current connectivity state for an array and
// returns true if a reconnection transition (Unreachable → Reachable)
// was detected.
func (t *SiteStateTracker) UpdateState(arrayID string, newState MetroSiteState) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	prev := t.states[arrayID]
	t.states[arrayID] = newState
	return prev == SiteUnreachable && newState == SiteReachable
}

// GetState returns the last-known state for an array.
func (t *SiteStateTracker) GetState(arrayID string) MetroSiteState {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.states[arrayID]
}

// ReconciliationResult captures the outcome of replaying a single deferred
// operation.
type ReconciliationResult struct {
	Token       string
	Success     bool
	Error       error
	UnsafeState bool // true when the operation was abandoned due to ErrUnsafeRDFState
	MaxRetries  bool // true when the operation was abandoned due to MaxReconciliationRetries
}

const (
	// MaxReconciliationRetries is the maximum number of replay attempts
	// before a deferred operation is considered permanently failed.
	MaxReconciliationRetries = 10
	// ReconciliationBackoffBase is the base duration for exponential backoff
	// between retry attempts.
	ReconciliationBackoffBase = 5 * time.Second
)

// ReconcileFunc is the callback invoked for each deferred operation during
// reconciliation. It receives the operation and should return nil on success.
type ReconcileFunc func(ctx context.Context, op DeferredOperation) error

// ReconcileDeferredOperations replays all pending deferred operations for
// the specified array in submission-time order. Each operation is passed to
// the reconcileFunc callback; on success the operation is removed from the
// journal, on failure the retry count and error are updated.
// Implements automatic retry loop - continues retrying operations
// until success, unsafe state, max retries exceeded, or context cancellation.
func ReconcileDeferredOperations(ctx context.Context, journal *VolumeJournal, arrayID string, reconcileFunc ReconcileFunc) []ReconciliationResult {
	ops, listErr := journal.getDeferredOperations(ctx, arrayID)
	if listErr != nil {
		csmlog.WithContext(ctx).Errorf("Reconciliation: failed to read durable operations for array %s: %v", arrayID, listErr)
		return []ReconciliationResult{{Success: false, Error: listErr}}
	}
	if len(ops) == 0 {
		return nil
	}

	// Sort by submission time to preserve ordering guarantees.
	sort.Slice(ops, func(i, j int) bool {
		return ops[i].SubmissionTime.Before(ops[j].SubmissionTime)
	})

	var results []ReconciliationResult
	// Keep the sorted slice as the work queue. A map would discard the
	// submission-time ordering guaranteed by this function.
	pendingOps := ops

	// Retry loop: continue until all operations are resolved or context cancelled.
	for len(pendingOps) > 0 {
		select {
		case <-ctx.Done():
			csmlog.WithContext(ctx).Warnf("Reconciliation: context cancelled, %d operations still pending", len(pendingOps))
			for _, op := range pendingOps {
				results = append(results, ReconciliationResult{Token: op.IdempotencyToken, Error: ctx.Err()})
			}
			return results
		default:
		}

		nextPending := make([]DeferredOperation, 0, len(pendingOps))
		retryNeeded := false
		for _, op := range pendingOps {
			if op.RetryCount >= MaxReconciliationRetries {
				csmlog.Warnf("Reconciliation: operation %s (type=%s, vol=%s) — max retries (%d) exceeded; marking as terminal",
					op.IdempotencyToken, op.OperationType, op.VolumeID, MaxReconciliationRetries)
				if updateErr := journal.UpdateDeferredOperationStatus(ctx, op.IdempotencyToken, OpStatusTerminal); updateErr != nil {
					csmlog.WithContext(ctx).Errorf("Reconciliation: failed to mark operation %s as terminal: %v", op.IdempotencyToken, updateErr)
				}
				results = append(results, ReconciliationResult{Token: op.IdempotencyToken, MaxRetries: true, Error: fmt.Errorf("max retries exceeded (%d)", MaxReconciliationRetries)})
				continue
			}

			if op.RetryCount > 0 {
				backoff := backoffDuration(journal.GetReconciliationBackoff(), op.RetryCount)
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					nextPending = append(nextPending, op)
					results = append(results, ReconciliationResult{Token: op.IdempotencyToken, Error: ctx.Err()})
					continue
				}
			}

			err := reconcileFunc(ctx, op)
			if err != nil {
				if IsUnsafeRDFState(err) {
					if updateErr := journal.UpdateDeferredOperationStatus(ctx, op.IdempotencyToken, OpStatusTerminal); updateErr != nil {
						csmlog.WithContext(ctx).Errorf("Reconciliation: failed to mark operation %s as terminal: %v", op.IdempotencyToken, updateErr)
					}
					results = append(results, ReconciliationResult{Token: op.IdempotencyToken, UnsafeState: true, Error: err})
					continue
				}
				if updateErr := journal.UpdateDeferredOperation(ctx, op.IdempotencyToken, op.RetryCount+1, err.Error()); updateErr != nil {
					csmlog.WithContext(ctx).Errorf("Reconciliation: failed to persist retry count for %s: %v", op.IdempotencyToken, updateErr)
				}
				op.RetryCount++
				op.LastError = err.Error()
				nextPending = append(nextPending, op)
				retryNeeded = true
				results = append(results, ReconciliationResult{Token: op.IdempotencyToken, Error: err})
				continue
			}

			if removeErr := journal.RemoveDeferredOperation(ctx, op.IdempotencyToken); removeErr != nil {
				nextPending = append(nextPending, op)
				csmlog.WithContext(ctx).Errorf("Reconciliation: failed to remove completed operation %s: %v", op.IdempotencyToken, removeErr)
				results = append(results, ReconciliationResult{Token: op.IdempotencyToken, Error: removeErr})
				continue
			}
			results = append(results, ReconciliationResult{Token: op.IdempotencyToken, Success: true})
		}
		pendingOps = nextPending
		if !retryNeeded {
			break
		}
	}

	return results
}

// backoffDurationFromBase computes the exponential backoff duration for a given
// retry count using ReconciliationBackoffBase: base * 2^retryCount, capped
// at 5 minutes.
func backoffDurationFromBase(retryCount int) time.Duration {
	return backoffDuration(ReconciliationBackoffBase, retryCount)
}

// backoffDuration computes base * 2^retryCount capped at 5 minutes.
func backoffDuration(base time.Duration, retryCount int) time.Duration {
	d := base * time.Duration(1<<uint(retryCount))
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}
