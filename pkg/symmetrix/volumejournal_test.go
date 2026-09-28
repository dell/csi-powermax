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
	"testing"
	"time"
)

func TestVolumeJournal_CreateAndGet(t *testing.T) {
	j := NewVolumeJournal()
	token, err := j.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-001",
		ArrayID:       "000120000001",
		RDFGroupNo:    "10",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty idempotency token")
	}

	ops := j.GetDeferredOperations("000120000001")
	if len(ops) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(ops))
	}
	if ops[0].VolumeID != "vol-001" {
		t.Errorf("expected vol-001, got %s", ops[0].VolumeID)
	}
	if ops[0].IdempotencyToken != token {
		t.Errorf("token mismatch: %s vs %s", ops[0].IdempotencyToken, token)
	}
}

func TestVolumeJournal_DuplicateDetection(t *testing.T) {
	j := NewVolumeJournal()
	token1, _ := j.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-001",
		ArrayID:       "000120000001",
	})
	// Same compound key should return existing token without creating a duplicate.
	token2, err := j.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-001",
		ArrayID:       "000120000001",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if token1 != token2 {
		t.Errorf("expected same token for duplicate, got %s and %s", token1, token2)
	}
	if len(j.GetAllOperations()) != 1 {
		t.Error("expected exactly 1 operation after duplicate insert")
	}
}

func TestVolumeJournal_DifferentOpsNotDuplicate(t *testing.T) {
	j := NewVolumeJournal()
	j.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-001",
		ArrayID:       "000120000001",
	})
	j.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpDeviceCleanup,
		VolumeID:      "vol-001",
		ArrayID:       "000120000001",
	})
	if len(j.GetAllOperations()) != 2 {
		t.Error("expected 2 operations for different operation types")
	}
}

func TestVolumeJournal_UpdateAndRemove(t *testing.T) {
	j := NewVolumeJournal()
	token, _ := j.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpDeviceCleanup,
		VolumeID:      "vol-002",
		ArrayID:       "000120000002",
	})

	err := j.UpdateDeferredOperation(context.Background(), token, 3, "timeout connecting")
	if err != nil {
		t.Fatalf("update failed: %v", err)
	}

	ops := j.GetDeferredOperations("000120000002")
	if ops[0].RetryCount != 3 {
		t.Errorf("expected retry count 3, got %d", ops[0].RetryCount)
	}
	if ops[0].LastError != "timeout connecting" {
		t.Errorf("expected last error 'timeout connecting', got %s", ops[0].LastError)
	}

	err = j.RemoveDeferredOperation(context.Background(), token)
	if err != nil {
		t.Fatalf("remove failed: %v", err)
	}
	if len(j.GetAllOperations()) != 0 {
		t.Error("expected 0 operations after remove")
	}
}

func TestVolumeJournal_UpdateNotFound(t *testing.T) {
	j := NewVolumeJournal()
	err := j.UpdateDeferredOperation(context.Background(), "nonexistent", 1, "err")
	if err == nil {
		t.Fatal("expected error for nonexistent token")
	}
}

func TestVolumeJournal_RemoveNotFound(t *testing.T) {
	j := NewVolumeJournal()
	err := j.RemoveDeferredOperation(context.Background(), "nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent token")
	}
}

func TestVolumeJournal_QueueHardLimit(t *testing.T) {
	j := NewVolumeJournal()
	for i := 0; i < QueueHardLimit; i++ {
		_, err := j.CreateDeferredOperation(context.Background(), DeferredOperation{
			OperationType: OpMetroPairing,
			VolumeID:      "vol-" + time.Now().String() + string(rune(i)),
			ArrayID:       "000120000001",
		})
		if err != nil {
			t.Fatalf("unexpected error at operation %d: %v", i, err)
		}
	}

	_, err := j.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-overflow",
		ArrayID:       "000120000001",
	})
	if !errors.Is(err, ErrQueueFull) {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}
}

func TestVolumeJournal_QueueStatus_Warning(t *testing.T) {
	j := NewVolumeJournal()
	for i := 0; i < QueueWarningThreshold; i++ {
		j.CreateDeferredOperation(context.Background(), DeferredOperation{
			OperationType: OpMetroPairing,
			VolumeID:      "vol-" + time.Now().String() + string(rune(i)),
			ArrayID:       "000120000001",
		})
	}
	qs := j.GetQueueStatus()
	if !qs.AtWarning {
		t.Error("expected AtWarning true at warning threshold")
	}
	if qs.AtLimit {
		t.Error("expected AtLimit false below hard limit")
	}
}

func TestVolumeJournal_QueueStatus_Empty(t *testing.T) {
	j := NewVolumeJournal()
	qs := j.GetQueueStatus()
	if qs.Count != 0 || qs.AtWarning || qs.AtLimit {
		t.Errorf("unexpected queue status for empty journal: %+v", qs)
	}
}

func TestVolumeJournal_GetByArray_FiltersByArray(t *testing.T) {
	j := NewVolumeJournal()
	j.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-a",
		ArrayID:       "array-1",
	})
	j.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-b",
		ArrayID:       "array-2",
	})
	j.CreateDeferredOperation(context.Background(), DeferredOperation{
		OperationType: OpDeviceCleanup,
		VolumeID:      "vol-c",
		ArrayID:       "array-1",
	})

	ops := j.GetDeferredOperations("array-1")
	if len(ops) != 2 {
		t.Errorf("expected 2 operations for array-1, got %d", len(ops))
	}
	ops = j.GetDeferredOperations("array-2")
	if len(ops) != 1 {
		t.Errorf("expected 1 operation for array-2, got %d", len(ops))
	}
}

// ─── SetReconciliationBackoff / GetReconciliationBackoff ──────────────────────

func TestVolumeJournal_GetReconciliationBackoff_Default(t *testing.T) {
	j := NewVolumeJournal()
	got := j.GetReconciliationBackoff()
	if got != ReconciliationBackoffBase {
		t.Errorf("expected default %v, got %v", ReconciliationBackoffBase, got)
	}
}

func TestVolumeJournal_SetReconciliationBackoff_And_Get(t *testing.T) {
	j := NewVolumeJournal()
	j.SetReconciliationBackoff(3 * time.Second)
	got := j.GetReconciliationBackoff()
	if got != 3*time.Second {
		t.Errorf("expected 3s, got %v", got)
	}
}

// ─── GetQueueStatusForArray ───────────────────────────────────────────────────

func TestVolumeJournal_GetQueueStatusForArray_FiltersByArray(t *testing.T) {
	j := NewVolumeJournal()
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		j.CreateDeferredOperation(ctx, DeferredOperation{
			OperationType: OpMetroPairing,
			VolumeID:      fmt.Sprintf("vol-A%d", i),
			ArrayID:       "000120000001",
		})
	}
	j.CreateDeferredOperation(ctx, DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-B0",
		ArrayID:       "000120000002",
	})

	// Count always reflects global depth across all arrays.
	qsA := j.GetQueueStatusForArray("000120000001")
	if qsA.Count != 4 {
		t.Errorf("expected global count 4, got %d", qsA.Count)
	}

	// For an array that has no pending ops, OldestAge is zero (per-array filter).
	qsC := j.GetQueueStatusForArray("000120000003")
	if qsC.OldestAge != 0 {
		t.Errorf("expected OldestAge 0 for array with no ops, got %v", qsC.OldestAge)
	}
}

func TestVolumeJournal_GetQueueStatusForArray_EmptyQueue(t *testing.T) {
	j := NewVolumeJournal()
	qs := j.GetQueueStatusForArray("000120000001")
	if qs.Count != 0 {
		t.Errorf("expected 0 ops, got %d", qs.Count)
	}
}
