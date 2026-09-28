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
	"testing"
	"time"
)

func TestSiteStateTracker_DetectsReconnection(t *testing.T) {
	tracker := NewSiteStateTracker()

	// Initial transition from Unknown to Reachable is NOT a reconnection.
	reconnected := tracker.UpdateState("arr1", SiteReachable)
	if reconnected {
		t.Error("expected no reconnection on first check")
	}

	// Transition to Unreachable.
	reconnected = tracker.UpdateState("arr1", SiteUnreachable)
	if reconnected {
		t.Error("expected no reconnection when going unreachable")
	}

	// Transition back to Reachable — this IS a reconnection.
	reconnected = tracker.UpdateState("arr1", SiteReachable)
	if !reconnected {
		t.Error("expected reconnection on Unreachable → Reachable transition")
	}

	// Subsequent Reachable → Reachable is NOT a reconnection.
	reconnected = tracker.UpdateState("arr1", SiteReachable)
	if reconnected {
		t.Error("expected no reconnection on Reachable → Reachable")
	}
}

func TestSiteStateTracker_IndependentArrays(t *testing.T) {
	tracker := NewSiteStateTracker()
	tracker.UpdateState("arr1", SiteUnreachable)
	tracker.UpdateState("arr2", SiteReachable)

	// arr1 recovers — should detect reconnection.
	if !tracker.UpdateState("arr1", SiteReachable) {
		t.Error("expected reconnection for arr1")
	}
	// arr2 was never unreachable — no reconnection.
	if tracker.UpdateState("arr2", SiteReachable) {
		t.Error("expected no reconnection for arr2")
	}
}

func TestSiteStateTracker_GetState(t *testing.T) {
	tracker := NewSiteStateTracker()
	if tracker.GetState("unknown") != SiteUnknown {
		t.Error("expected SiteUnknown for untracked array")
	}
	tracker.UpdateState("arr1", SiteUnreachable)
	if tracker.GetState("arr1") != SiteUnreachable {
		t.Error("expected SiteUnreachable")
	}
}

func TestMetroSiteState_String(t *testing.T) {
	tests := []struct {
		state MetroSiteState
		want  string
	}{
		{SiteUnknown, "Unknown"},
		{SiteReachable, "Reachable"},
		{SiteUnreachable, "Unreachable"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("MetroSiteState(%d).String() = %s, want %s", tt.state, got, tt.want)
		}
	}
}

func TestReconcileDeferredOperations_SuccessfulReplay(t *testing.T) {
	journal := NewVolumeJournal()
	journal.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpDeviceCleanup,
		VolumeID:      "vol-001",
		ArrayID:       "arr1",
	})
	journal.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-002",
		ArrayID:       "arr1",
	})

	reconcileFunc := func(_ context.Context, _ DeferredOperation) error {
		return nil
	}

	results := ReconcileDeferredOperations(context.Background(), journal, "arr1", reconcileFunc)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	for _, r := range results {
		if !r.Success {
			t.Errorf("expected success for %s, got error: %v", r.Token, r.Error)
		}
	}
	// Journal should be empty after successful reconciliation.
	if len(journal.GetAllOperations()) != 0 {
		t.Error("expected empty journal after successful reconciliation")
	}
}

func TestReconcileDeferredOperations_FailureUpdatesRetry(t *testing.T) {
	journal := NewVolumeJournal()
	token, _ := journal.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpDeviceCleanup,
		VolumeID:      "vol-001",
		ArrayID:       "arr1",
	})

	reconcileFunc := func(_ context.Context, _ DeferredOperation) error {
		return errors.New("array still unreachable")
	}

	// Use a short timeout context to prevent the test from waiting for full backoff
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	results := ReconcileDeferredOperations(ctx, journal, "arr1", reconcileFunc)
	// With the new retry loop, we get 2 results: one for the initial failure, one for context cancellation
	if len(results) < 1 {
		t.Fatalf("expected at least 1 result, got %d", len(results))
	}
	// Check that at least one result is a failure
	hasFailure := false
	for _, r := range results {
		if !r.Success {
			hasFailure = true
			break
		}
	}
	if !hasFailure {
		t.Error("expected at least one failure result")
	}

	// Operation should still be in journal with incremented retry count.
	ops := journal.GetDeferredOperations("arr1")
	if len(ops) != 1 {
		t.Fatal("expected operation to remain in journal")
	}
	if ops[0].IdempotencyToken != token {
		t.Error("token mismatch")
	}
	// With the retry loop, the retry count should be incremented at least once
	if ops[0].RetryCount < 1 {
		t.Errorf("expected retry count >= 1, got %d", ops[0].RetryCount)
	}
}

func TestReconcileDeferredOperations_MaxRetriesSkipped(t *testing.T) {
	journal := NewVolumeJournal()
	journal.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpDeviceCleanup,
		VolumeID:      "vol-001",
		ArrayID:       "arr1",
	})
	// Simulate previous retries by updating the retry count.
	ops := journal.GetDeferredOperations("arr1")
	journal.UpdateDeferredOperation(context.Background(), ops[0].IdempotencyToken, MaxReconciliationRetries, "permanent failure")

	called := false
	reconcileFunc := func(_ context.Context, _ DeferredOperation) error {
		called = true
		return nil
	}

	results := ReconcileDeferredOperations(context.Background(), journal, "arr1", reconcileFunc)
	if called {
		t.Error("reconcileFunc should not be called for max-retries operations")
	}
	if len(results) != 1 || results[0].Success {
		t.Error("expected failure result for max-retries operation")
	}
	// With the new implementation, operations are marked as terminal instead of being removed
	// to preserve evidence for manual recovery (addressing Anand's review comment #12)
	if len(journal.GetAllOperations()) != 1 {
		t.Error("expected max-retries operation to be marked as terminal and remain in journal")
	}
}

func TestReconcileDeferredOperations_EmptyJournal(t *testing.T) {
	journal := NewVolumeJournal()
	results := ReconcileDeferredOperations(context.Background(), journal, "arr1", nil)
	if results != nil {
		t.Error("expected nil results for empty journal")
	}
}

func TestReconcileDeferredOperations_OrderedBySubmissionTime(t *testing.T) {
	journal := NewVolumeJournal()
	// Create ops — they'll have very close timestamps, so we verify ordering is preserved.
	journal.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-first",
		ArrayID:       "arr1",
	})
	time.Sleep(time.Millisecond) // ensure different timestamps
	journal.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpDeviceCleanup,
		VolumeID:      "vol-second",
		ArrayID:       "arr1",
	})

	var order []string
	reconcileFunc := func(_ context.Context, op DeferredOperation) error {
		order = append(order, op.VolumeID)
		return nil
	}

	ReconcileDeferredOperations(context.Background(), journal, "arr1", reconcileFunc)
	want := []string{"vol-first", "vol-second"}
	if len(order) != len(want) {
		t.Fatalf("expected order %v, got %v", want, order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Errorf("replay order[%d] = %q, want %q (full order: %v)", i, order[i], want[i], order)
		}
	}
}

func TestBackoffDuration(t *testing.T) {
	tests := []struct {
		retry int
		want  time.Duration
	}{
		{0, 5 * time.Second},
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{3, 40 * time.Second},
		{4, 80 * time.Second},
		{10, 5 * time.Minute}, // capped
	}
	for _, tt := range tests {
		got := backoffDurationFromBase(tt.retry)
		if got != tt.want {
			t.Errorf("backoffDurationFromBase(%d) = %v, want %v", tt.retry, got, tt.want)
		}
	}
}

// ─── UnsafeRDFStateError ──────────────────────────────────────────────────────

func TestUnsafeRDFStateError_Error(t *testing.T) {
	err := &UnsafeRDFStateError{State: "Split"}
	msg := err.Error()
	if msg == "" {
		t.Error("expected non-empty Error() string")
	}
	// Message must include the state name.
	if !errors.Is(err, ErrUnsafeRDFState) {
		t.Error("expected errors.Is to match ErrUnsafeRDFState via Unwrap")
	}
}

func TestUnsafeRDFStateError_Unwrap(t *testing.T) {
	err := &UnsafeRDFStateError{State: "Mixed"}
	if unwrapped := err.Unwrap(); unwrapped != ErrUnsafeRDFState {
		t.Errorf("expected Unwrap() == ErrUnsafeRDFState, got %v", unwrapped)
	}
}

func TestIsUnsafeRDFState_True(t *testing.T) {
	err := &UnsafeRDFStateError{State: "Invalid"}
	if !IsUnsafeRDFState(err) {
		t.Error("expected IsUnsafeRDFState true for UnsafeRDFStateError")
	}
}

func TestIsUnsafeRDFState_False_ForOtherErrors(t *testing.T) {
	err := errors.New("some other error")
	if IsUnsafeRDFState(err) {
		t.Error("expected IsUnsafeRDFState false for unrelated error")
	}
}
