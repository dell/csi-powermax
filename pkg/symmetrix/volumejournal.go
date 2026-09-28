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
	"sync"
	"time"

	"github.com/dell/csmlog"
	"github.com/google/uuid"
)

// DeferredOperationType identifies the kind of operation that was deferred
// during a Metro site failure.
type DeferredOperationType string

const (
	// OpMetroPairing is deferred SRDF Metro pairing for a degraded volume.
	OpMetroPairing DeferredOperationType = "MetroPairing"
	// OpDeviceCleanup is deferred device deletion on the offline array.
	OpDeviceCleanup DeferredOperationType = "DeviceCleanup"
)

// DeferredOperation represents a single deferred operation recorded
// during an SRDF/Metro site failure.
type DeferredOperation struct {
	// IdempotencyToken is a UUID assigned at creation time to guard against
	// duplicate replay during reconciliation.
	IdempotencyToken string
	// OperationType identifies the kind of deferred work.
	OperationType DeferredOperationType
	// VolumeID is the CSI volume identifier.
	VolumeID string
	// ArrayID is the symmetrix ID of the offline array.
	ArrayID string
	// RDFGroupNo is the RDF group number (may be empty for non-RDF ops).
	RDFGroupNo string
	// StorageGroupName is the exact SG name used when provisioning the volume.
	// Stored so reconcileMetroPairing can call Establish with the correct SG
	// rather than re-building the name (which requires the namespace).
	StorageGroupName string
	// NodeName is the Kubernetes node (optional, for publish/unpublish).
	NodeName string
	// SubmissionTime is when the operation was deferred.
	SubmissionTime time.Time
	// RetryCount tracks how many times reconciliation has attempted this op.
	RetryCount int
	// LastError is the error message from the most recent retry attempt.
	LastError string
	// RemoteRDFGroupNo is the remote RDF group number (for Metro pairing recovery).
	RemoteRDFGroupNo string
	// RemoteStorageGroupName is the remote storage group name (for Metro pairing recovery).
	RemoteStorageGroupName string
	// RemoteSRP is the remote storage resource pool (for Metro pairing recovery).
	RemoteSRP string
	// RemoteServiceLevel is the remote service level (for Metro pairing recovery).
	RemoteServiceLevel string
	// VolumeCapacity is retained for compatibility with older journal records.
	VolumeCapacity float64
	// RequiredCylinders is the exact local device size used to create the
	// remote device during Metro recovery.
	RequiredCylinders int
	// VolumeName is the CSI volume name (for Metro pairing recovery).
	VolumeName string
	// Namespace is the PVC namespace (for Metro pairing recovery).
	Namespace string
	// ReplicationMode is the SRDF mode (e.g., "Metro") (for Metro pairing recovery).
	ReplicationMode string
	// LocalSymID is the local symmetrix ID (for Metro pairing recovery).
	LocalSymID string
	// DriverName is the name of the driver instance (for ownership isolation).
	DriverName string
	// InstanceUID is the unique identifier of the CSM instance (for ownership isolation).
	InstanceUID string
	// Status is the current status of the operation (pending, failed, terminal, etc.)
	Status string
}

const (
	// QueueWarningThreshold is the count at which a warning event is emitted.
	QueueWarningThreshold = 75
	// QueueHardLimit is the count at which new deferrals are rejected.
	QueueHardLimit = 100
	// QueueAgeWarningThreshold is the oldest-pending age at which a warning is emitted.
	QueueAgeWarningThreshold = 30 * time.Minute
)

// OperationStatus constants for deferred operation lifecycle.
const (
	OpStatusPending   = "pending"
	OpStatusFailed    = "failed"
	OpStatusTerminal  = "terminal"
	OpStatusCompleted = "completed"
)

// ErrQueueFull is returned when the deferred operation queue has reached
// QueueHardLimit and cannot accept new entries.
var ErrQueueFull = errors.New("deferred operation queue is full")

func isPendingOperation(status string) bool {
	return status != OpStatusTerminal && status != OpStatusCompleted
}

// VolumeJournal manages deferred operations during Metro site failures.
// When crd is nil the journal uses an in-memory slice (suitable for unit
// tests). When crd is non-nil all mutations are persisted to the cluster via
// the VolumeJournal CRD and the in-memory slice acts as a write-through cache
// for fast status queries without an API round-trip.
type VolumeJournal struct {
	mu                    sync.RWMutex
	operations            []DeferredOperation
	crd                   *crdJournal // nil → in-memory only mode
	warningThreshold      int
	hardLimit             int
	reconciliationBackoff time.Duration // base backoff for exponential retry; 0 → default; always access under mu
}

// SetReconciliationBackoff sets the base backoff duration for reconciliation
// retries. Thread-safe.
func (j *VolumeJournal) SetReconciliationBackoff(d time.Duration) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.reconciliationBackoff = d
}

// GetReconciliationBackoff returns the configured base backoff, falling back
// to ReconciliationBackoffBase when unset.
func (j *VolumeJournal) GetReconciliationBackoff() time.Duration {
	j.mu.RLock()
	defer j.mu.RUnlock()
	if j.reconciliationBackoff > 0 {
		return j.reconciliationBackoff
	}
	return ReconciliationBackoffBase
}

// NewVolumeJournal creates a new, empty VolumeJournal with default thresholds.
func NewVolumeJournal() *VolumeJournal {
	return &VolumeJournal{
		warningThreshold: QueueWarningThreshold,
		hardLimit:        QueueHardLimit,
	}
}

// SetThresholds overrides the default queue warning and hard-limit thresholds.
// Values ≤ 0 are ignored (defaults are kept). This must be called before the
// journal is used.
func (j *VolumeJournal) SetThresholds(warning, limit int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if warning > 0 {
		j.warningThreshold = warning
	}
	if limit > 0 {
		j.hardLimit = limit
	}
}

// QueueStatus reports the current queue depth and oldest pending age.
type QueueStatus struct {
	Count     int
	OldestAge time.Duration
	AtWarning bool
	AtLimit   bool
}

// CreateDeferredOperation records a new deferred operation. It returns
// ErrQueueFull if the queue has reached the hard limit.
func (j *VolumeJournal) CreateDeferredOperation(ctx context.Context, op DeferredOperation) (string, error) {
	j.mu.Lock()
	defer j.mu.Unlock()

	limit := j.hardLimit
	if limit <= 0 {
		limit = QueueHardLimit
	}
	if j.crd == nil && len(j.operations) >= limit {
		return "", ErrQueueFull
	}

	// Check for duplicate compound key (VolumeID + OperationType + ArrayID + RDFGroupNo).
	for _, existing := range j.operations {
		if isPendingOperation(existing.Status) && existing.VolumeID == op.VolumeID &&
			existing.OperationType == op.OperationType &&
			existing.ArrayID == op.ArrayID &&
			existing.RDFGroupNo == op.RDFGroupNo {
			return existing.IdempotencyToken, nil
		}
	}

	op.IdempotencyToken = uuid.New().String()
	op.SubmissionTime = time.Now()
	op.RetryCount = 0
	op.Status = OpStatusPending

	// Persist to Kubernetes CRD when configured. The lease covers the
	// authoritative count check and create, making admission atomic across
	// controller replicas.
	if j.crd != nil {
		duplicateToken := ""
		crdErr := j.crd.withQueueLease(ctx, func() error {
			sharedOps, err := j.crd.crdList(ctx, "")
			if err != nil {
				return fmt.Errorf("check queue depth: %w", err)
			}
			if len(sharedOps) >= limit {
				return ErrQueueFull
			}
			for _, existing := range sharedOps {
				if existing.VolumeID == op.VolumeID && existing.OperationType == op.OperationType && existing.ArrayID == op.ArrayID && existing.RDFGroupNo == op.RDFGroupNo {
					duplicateToken = existing.IdempotencyToken
					return nil
				}
			}
			return j.crd.crdCreate(ctx, op)
		})
		if crdErr != nil {
			return "", fmt.Errorf("crdJournal: persist VolumeJournal CR for %s: %w", op.VolumeID, crdErr)
		}
		if duplicateToken != "" {
			return duplicateToken, nil
		}
	}

	j.operations = append(j.operations, op)
	return op.IdempotencyToken, nil
}

// GetDeferredOperations returns all pending operations for the given array.
// Results are returned in insertion order; callers that require ordering
// must sort by SubmissionTime.
// When using CRD-backed journal, reads directly from Kubernetes
// to ensure authoritative state across controller replicas.
func (j *VolumeJournal) getDeferredOperations(ctx context.Context, arrayID string) ([]DeferredOperation, error) {
	if j.crd != nil {
		return j.crd.crdList(ctx, arrayID)
	}

	j.mu.RLock()
	defer j.mu.RUnlock()
	var result []DeferredOperation
	for _, op := range j.operations {
		if op.ArrayID == arrayID && isPendingOperation(op.Status) {
			result = append(result, op)
		}
	}
	return result, nil
}

func (j *VolumeJournal) GetDeferredOperations(arrayID string) []DeferredOperation {
	ops, err := j.getDeferredOperations(context.Background(), arrayID)
	if err != nil {
		csmlog.Errorf("VolumeJournal: failed to read deferred operations for array %s: %v", arrayID, err)
		return nil
	}
	return ops
}

// GetAllOperations returns all pending deferred operations.
func (j *VolumeJournal) GetAllOperations() []DeferredOperation {
	j.mu.RLock()
	defer j.mu.RUnlock()

	result := make([]DeferredOperation, len(j.operations))
	copy(result, j.operations)
	return result
}

// UpdateDeferredOperation updates the retry count, last error, and status for an
// operation identified by its idempotency token.
// Also updates the status field to support terminal state tracking.
func (j *VolumeJournal) UpdateDeferredOperation(ctx context.Context, token string, retryCount int, lastError string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	for i := range j.operations {
		if j.operations[i].IdempotencyToken == token {
			j.operations[i].RetryCount = retryCount
			j.operations[i].LastError = lastError
			if j.operations[i].Status != OpStatusTerminal && j.operations[i].Status != OpStatusCompleted {
				j.operations[i].Status = OpStatusFailed
			}
			// Persist retry state to CRD. This is authoritative — a failure
			// means the retry count may be lost on pod restart, causing
			// operations that already exhausted retries to replay.
			if j.crd != nil {
				if crdErr := j.crd.crdUpdate(ctx, j.operations[i]); crdErr != nil {
					return fmt.Errorf("crdJournal: update VolumeJournal CR %s: %w", token, crdErr)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("deferred operation with token %s not found", token)
}

// UpdateDeferredOperationStatus updates only the status field for an operation.
// Used to mark operations as terminal without changing retry count.
func (j *VolumeJournal) UpdateDeferredOperationStatus(ctx context.Context, token string, status string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	for i := range j.operations {
		if j.operations[i].IdempotencyToken == token {
			j.operations[i].Status = status
			// Persist status to CRD.
			if j.crd != nil {
				if crdErr := j.crd.crdUpdate(ctx, j.operations[i]); crdErr != nil {
					return fmt.Errorf("crdJournal: update VolumeJournal CR %s: %w", token, crdErr)
				}
			}
			return nil
		}
	}
	return fmt.Errorf("deferred operation with token %s not found", token)
}

// RemoveDeferredOperation removes a completed operation by its token.
func (j *VolumeJournal) RemoveDeferredOperation(ctx context.Context, token string) error {
	j.mu.Lock()
	defer j.mu.Unlock()

	for i := range j.operations {
		if j.operations[i].IdempotencyToken == token {
			// Delete from the durable store first. If this fails, retain the
			// operation in memory so a later reconciliation can retry the delete.
			if j.crd != nil {
				if crdErr := j.crd.crdDelete(ctx, token); crdErr != nil {
					return fmt.Errorf("crdJournal: delete VolumeJournal CR %s: %w", token, crdErr)
				}
			}
			j.operations = append(j.operations[:i], j.operations[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("deferred operation with token %s not found", token)
}

// GetQueueStatus returns the overall queue depth and oldest pending age,
// along with flags indicating whether warning or hard-limit thresholds
// have been reached.  OldestAge reflects the oldest operation in the
// entire journal (across all arrays).
func (j *VolumeJournal) GetQueueStatus() QueueStatus {
	j.mu.RLock()
	crd := j.crd
	j.mu.RUnlock()
	if crd != nil {
		ops, err := crd.crdList(context.Background(), "")
		if err != nil {
			csmlog.Errorf("VolumeJournal: failed to read durable queue status: %v", err)
			return QueueStatus{}
		}
		return j.queueStatusForOperations(ops, "", false)
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.queueStatusLocked("", false)
}

// GetQueueStatusForArray returns the queue status scoped to a single array.
// OldestAge and AtWarning are computed only from operations for that array,
// preventing operations for a recovered array from triggering false-positive
// age warnings for a currently-failing array.
func (j *VolumeJournal) GetQueueStatusForArray(arrayID string) QueueStatus {
	j.mu.RLock()
	crd := j.crd
	j.mu.RUnlock()
	if crd != nil {
		ops, err := crd.crdList(context.Background(), arrayID)
		if err != nil {
			csmlog.Errorf("VolumeJournal: failed to read durable queue status for array %s: %v", arrayID, err)
			return QueueStatus{}
		}
		return j.queueStatusForOperations(ops, arrayID, true)
	}
	j.mu.RLock()
	defer j.mu.RUnlock()
	return j.queueStatusLocked(arrayID, true)
}

// queueStatusLocked computes queue status while the read lock is held.
// When filterByArray is true only operations whose ArrayID == arrayID
// are counted; otherwise all operations are included.
func (j *VolumeJournal) queueStatusLocked(arrayID string, filterByArray bool) QueueStatus {
	return j.queueStatusForOperations(j.operations, arrayID, filterByArray)
}

func (j *VolumeJournal) queueStatusForOperations(operations []DeferredOperation, arrayID string, filterByArray bool) QueueStatus {
	warn := j.warningThreshold
	if warn <= 0 {
		warn = QueueWarningThreshold
	}
	limit := j.hardLimit
	if limit <= 0 {
		limit = QueueHardLimit
	}
	var ops []DeferredOperation
	pendingCount := 0
	for _, op := range operations {
		if !isPendingOperation(op.Status) {
			continue
		}
		pendingCount++
		if !filterByArray || op.ArrayID == arrayID {
			ops = append(ops, op)
		}
	}

	qs := QueueStatus{Count: pendingCount}
	if !filterByArray {
		qs.AtLimit = qs.Count >= limit
	}
	if len(ops) == 0 {
		return qs
	}

	oldest := ops[0].SubmissionTime
	for _, op := range ops[1:] {
		if op.SubmissionTime.Before(oldest) {
			oldest = op.SubmissionTime
		}
	}
	qs.OldestAge = time.Since(oldest)
	qs.AtWarning = qs.Count >= warn || qs.OldestAge >= QueueAgeWarningThreshold
	qs.AtLimit = qs.Count >= limit
	return qs
}

// SyncFromCRD populates the in-memory cache from the Kubernetes VolumeJournal
// CRD resources. This must be called once at driver startup (after the CRD
// backend is configured) to restore operations that persisted across pod
// restarts. It is a no-op when the CRD backend is not configured.
//
// the Kubernetes API call (crdList) is performed outside the mutex so
// that a slow or retried API response does not block concurrent deferOperation /
// GetAllOperations calls for up to 14 seconds during startup. The lock is only
// held for the final in-memory slice swap.
func (j *VolumeJournal) SyncFromCRD(ctx context.Context) error {
	// Quick non-blocking check: if no CRD backend is configured we're done.
	j.mu.RLock()
	crd := j.crd
	j.mu.RUnlock()
	if crd == nil {
		return nil
	}

	// Perform the Kubernetes API call WITHOUT holding the lock.
	ops, err := crd.crdList(ctx, "")
	if err != nil {
		return fmt.Errorf("SyncFromCRD: list VolumeJournal CRs: %w", err)
	}

	// Swap the in-memory slice under the write lock (fast, no I/O).
	j.mu.Lock()
	j.operations = ops
	j.mu.Unlock()

	csmlog.Infof("SyncFromCRD: restored %d deferred operations from cluster", len(ops))
	return nil
}
