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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
)

// ─── minimal fake dynamic client ─────────────────────────────────────────────

// fakeDynamicResource implements dynamic.ResourceInterface backed by a simple
// in-memory map. Only the methods used by crdJournal are implemented.
type fakeDynamicResource struct {
	mu                sync.Mutex
	objects           map[string]*unstructured.Unstructured
	listErr           error
	patches           [][]byte
	patchSubresources [][]string
}

func newFakeDynamicResource() *fakeDynamicResource {
	return &fakeDynamicResource{objects: map[string]*unstructured.Unstructured{}}
}

// jsonCopy performs a deep copy by marshalling/unmarshalling through JSON,
// avoiding the DeepCopyJSONValue panic on []byte fields.
func jsonCopy(obj *unstructured.Unstructured) (*unstructured.Unstructured, error) {
	data, err := json.Marshal(obj.Object)
	if err != nil {
		return nil, err
	}
	cp := &unstructured.Unstructured{}
	return cp, json.Unmarshal(data, &cp.Object)
}

func (f *fakeDynamicResource) Create(_ context.Context, obj *unstructured.Unstructured, _ metav1.CreateOptions, _ ...string) (*unstructured.Unstructured, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, exists := f.objects[obj.GetName()]; exists {
		return nil, fmt.Errorf("already exists: %s", obj.GetName())
	}
	cp, err := jsonCopy(obj)
	if err != nil {
		return nil, err
	}
	f.objects[cp.GetName()] = cp
	return cp, nil
}

func (f *fakeDynamicResource) Update(_ context.Context, obj *unstructured.Unstructured, _ metav1.UpdateOptions, _ ...string) (*unstructured.Unstructured, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[obj.GetName()]; !ok {
		return nil, fmt.Errorf("not found: %s", obj.GetName())
	}
	cp, err := jsonCopy(obj)
	if err != nil {
		return nil, err
	}
	f.objects[cp.GetName()] = cp
	return cp, nil
}

func (f *fakeDynamicResource) Delete(_ context.Context, name string, _ metav1.DeleteOptions, _ ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[name]; !ok {
		return fmt.Errorf("not found: %s", name)
	}
	delete(f.objects, name)
	return nil
}

func (f *fakeDynamicResource) Get(_ context.Context, name string, _ metav1.GetOptions, _ ...string) (*unstructured.Unstructured, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	obj, ok := f.objects[name]
	if !ok {
		return nil, fmt.Errorf("not found: %s", name)
	}
	return jsonCopy(obj)
}

func (f *fakeDynamicResource) List(_ context.Context, opts metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	list := &unstructured.UnstructuredList{}
	for _, obj := range f.objects {
		// Simple label selector: comma-separated "key=value" expressions.
		if opts.LabelSelector != "" {
			resourceLabels := obj.GetLabels()
			matches := true
			for _, expression := range strings.Split(opts.LabelSelector, ",") {
				parts := strings.SplitN(expression, "=", 2)
				if len(parts) != 2 || resourceLabels[parts[0]] != parts[1] {
					matches = false
					break
				}
			}
			if !matches {
				continue
			}
		}
		cp, cpErr := jsonCopy(obj)
		if cpErr != nil {
			continue
		}
		list.Items = append(list.Items, *cp)
	}
	return list, nil
}

// Stub implementations for unused ResourceInterface methods.
func (f *fakeDynamicResource) UpdateStatus(_ context.Context, obj *unstructured.Unstructured, _ metav1.UpdateOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeDynamicResource) DeleteCollection(_ context.Context, _ metav1.DeleteOptions, _ metav1.ListOptions) error {
	return nil
}

func (f *fakeDynamicResource) Watch(_ context.Context, _ metav1.ListOptions) (watch.Interface, error) {
	return watch.NewFake(), nil
}

func (f *fakeDynamicResource) Patch(_ context.Context, _ string, _ types.PatchType, patch []byte, _ metav1.PatchOptions, subresources ...string) (*unstructured.Unstructured, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.patches = append(f.patches, append([]byte(nil), patch...))
	f.patchSubresources = append(f.patchSubresources, append([]string(nil), subresources...))
	return nil, nil
}

func (f *fakeDynamicResource) Apply(_ context.Context, _ string, obj *unstructured.Unstructured, _ metav1.ApplyOptions, _ ...string) (*unstructured.Unstructured, error) {
	return obj, nil
}

func (f *fakeDynamicResource) ApplyStatus(_ context.Context, _ string, obj *unstructured.Unstructured, _ metav1.ApplyOptions) (*unstructured.Unstructured, error) {
	return obj, nil
}

// fakeDynamicClient implements dynamic.Interface and returns the single fake
// resource regardless of which GVR is requested.
type fakeDynamicClient struct {
	res *fakeDynamicResource
}

func newFakeDynamicClient() *fakeDynamicClient {
	return &fakeDynamicClient{res: newFakeDynamicResource()}
}

func (c *fakeDynamicClient) Resource(_ schema.GroupVersionResource) dynamic.NamespaceableResourceInterface {
	return c.res
}

// Namespace satisfies the NamespaceableResourceInterface embedding on fakeDynamicResource.
func (f *fakeDynamicResource) Namespace(_ string) dynamic.ResourceInterface { return f }

// ─── tests ────────────────────────────────────────────────────────────────────

func TestNewCRDVolumeJournal(t *testing.T) {
	fdc := newFakeDynamicClient()
	j := NewCRDVolumeJournal(fdc)
	if j == nil {
		t.Fatal("expected non-nil VolumeJournal")
	}
	if j.crd == nil {
		t.Fatal("expected non-nil crd field")
	}
	if j.warningThreshold != QueueWarningThreshold {
		t.Errorf("expected warningThreshold %d, got %d", QueueWarningThreshold, j.warningThreshold)
	}
}

func TestCRDJournal_QueueLeaseAllowsSingleOwner(t *testing.T) {
	fdc := newFakeDynamicClient()
	first := &crdJournal{client: fdc, holderIdentity: "first"}
	second := &crdJournal{client: fdc, holderIdentity: "second"}

	results := make(chan error, 2)
	go func() { results <- first.acquireQueueLease(context.Background()) }()
	go func() { results <- second.acquireQueueLease(context.Background()) }()

	successes := 0
	for i := 0; i < 2; i++ {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("expected exactly one queue lease owner, got %d", successes)
	}
}

func TestVolumeJournal_SetThresholds(t *testing.T) {
	j := NewVolumeJournal()

	// Custom thresholds.
	j.SetThresholds(50, 80)
	if j.warningThreshold != 50 {
		t.Errorf("expected warningThreshold 50, got %d", j.warningThreshold)
	}
	if j.hardLimit != 80 {
		t.Errorf("expected hardLimit 80, got %d", j.hardLimit)
	}

	// Zero/negative values are ignored.
	j.SetThresholds(0, -1)
	if j.warningThreshold != 50 {
		t.Errorf("zero warning should be ignored; got %d", j.warningThreshold)
	}
	if j.hardLimit != 80 {
		t.Errorf("negative limit should be ignored; got %d", j.hardLimit)
	}
}

func TestCrdOpToUnstructured_RoundTrip(t *testing.T) {
	op := DeferredOperation{ // nosec G101
		OperationType:    OpMetroPairing,
		VolumeID:         "vol-123",
		ArrayID:          "000197900046",
		RDFGroupNo:       "14",
		NodeName:         "worker-1",
		IdempotencyToken: "uuid-abc-123",
		SubmissionTime:   time.Now().UTC().Truncate(time.Second),
	}

	obj, err := crdOpToUnstructured(op)
	if err != nil {
		t.Fatalf("crdOpToUnstructured error: %v", err)
	}
	if obj == nil {
		t.Fatal("expected non-nil unstructured object")
	}
	if obj.GetName() != op.IdempotencyToken {
		t.Errorf("expected name %s, got %s", op.IdempotencyToken, obj.GetName())
	}

	// Round-trip back.
	recovered, err := crdUnstructuredToOp(obj)
	if err != nil {
		t.Fatalf("crdUnstructuredToOp error: %v", err)
	}
	if recovered.VolumeID != op.VolumeID {
		t.Errorf("expected VolumeID %s, got %s", op.VolumeID, recovered.VolumeID)
	}
	if recovered.ArrayID != op.ArrayID {
		t.Errorf("expected ArrayID %s, got %s", op.ArrayID, recovered.ArrayID)
	}
	if recovered.OperationType != op.OperationType {
		t.Errorf("expected OperationType %s, got %s", op.OperationType, recovered.OperationType)
	}
}

func TestCrdOpToUnstructured_EmptyNodeName(t *testing.T) {
	op := DeferredOperation{ //nolint:gosec // G101 - test data, not real credentials
		OperationType:    OpDeviceCleanup,
		VolumeID:         "vol-456",
		ArrayID:          "000197900047",
		IdempotencyToken: "uuid-def-456",
		SubmissionTime:   time.Now().UTC(),
	}
	obj, err := crdOpToUnstructured(op)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Verify the "host" field defaulted to "unknown".
	entries, _, _ := unstructured.NestedSlice(obj.Object, "spec", "journalEntries")
	if len(entries) == 0 {
		t.Fatal("expected journalEntries")
	}
	entry, ok := entries[0].(map[string]interface{})
	if !ok {
		t.Fatal("unexpected entry type")
	}
	host, _, _ := unstructured.NestedString(entry, "host")
	if host != "unknown" {
		t.Errorf("expected host 'unknown', got %q", host)
	}
}

func TestCrdUnstructuredToOp_BadEntries(t *testing.T) {
	// No journalEntries field.
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "dr.storage.dell.com/v1",
		"kind":       "VolumeJournal",
		"metadata":   map[string]interface{}{"name": "bad"},
		"spec":       map[string]interface{}{},
	}}
	if _, err := crdUnstructuredToOp(obj); err == nil {
		t.Error("expected error for missing journalEntries")
	}

	// journalEntries present but request field is wrong type.
	// Use int64 (not plain int) — DeepCopyJSONValue only handles int64.
	obj2 := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "dr.storage.dell.com/v1",
		"kind":       "VolumeJournal",
		"metadata":   map[string]interface{}{"name": "bad2"},
		"spec": map[string]interface{}{
			"journalEntries": []interface{}{
				map[string]interface{}{
					"request": int64(12345), // unexpected type (not []byte or string)
				},
			},
		},
	}}
	if _, err := crdUnstructuredToOp(obj2); err == nil {
		t.Error("expected error for unexpected request field type")
	}
}

func TestCRDJournal_UpdatePatchesStatusConditions(t *testing.T) {
	fdc := newFakeDynamicClient()
	ctx := context.Background()
	op := DeferredOperation{
		OperationType:    OpMetroPairing,
		VolumeID:         "status-volume",
		ArrayID:          "status-array",
		IdempotencyToken: "status-token",
		Status:           OpStatusTerminal,
		RetryCount:       MaxReconciliationRetries,
		LastError:        "unsafe RDF state",
	}
	obj, err := crdOpToUnstructured(op)
	if err != nil {
		t.Fatalf("crdOpToUnstructured: %v", err)
	}
	if _, err := fdc.res.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed resource: %v", err)
	}

	if err := (&crdJournal{client: fdc}).crdUpdate(ctx, op); err != nil {
		t.Fatalf("crdUpdate: %v", err)
	}
	if len(fdc.res.patches) != 2 {
		t.Fatalf("expected spec and status patches, got %d", len(fdc.res.patches))
	}
	if len(fdc.res.patchSubresources[1]) != 1 || fdc.res.patchSubresources[1][0] != "status" {
		t.Fatalf("expected second patch to target status subresource, got %v", fdc.res.patchSubresources[1])
	}
	if !strings.Contains(string(fdc.res.patches[1]), "ReconciliationReady") || !strings.Contains(string(fdc.res.patches[1]), "Terminal") {
		t.Fatalf("status patch missing terminal condition: %s", fdc.res.patches[1])
	}
}

func TestCRDJournal_CreateUpdateDeleteList(t *testing.T) {
	fdc := newFakeDynamicClient()
	j := NewCRDVolumeJournal(fdc)
	ctx := context.Background()

	op := DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-crd-1",
		ArrayID:       "000197900046",
		RDFGroupNo:    "14",
	}

	// CreateDeferredOperation should write to the fake CRD store.
	token, err := j.CreateDeferredOperation(ctx, op)
	if err != nil {
		t.Fatalf("CreateDeferredOperation: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	// GetDeferredOperations should return the op via in-memory cache.
	ops := j.GetDeferredOperations(op.ArrayID)
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}

	// UpdateDeferredOperation should update retry count and propagate to CRD.
	if err := j.UpdateDeferredOperation(ctx, token, 2, "timeout"); err != nil {
		t.Fatalf("UpdateDeferredOperation: %v", err)
	}

	// Verify the CRD object was updated via crdUpdate.
	stored, getErr := fdc.res.Get(ctx, token, metav1.GetOptions{})
	if getErr != nil {
		t.Fatalf("Get from fake store: %v", getErr)
	}
	if stored == nil {
		t.Fatal("expected stored object")
	}

	// RemoveDeferredOperation should delete from CRD store.
	if err := j.RemoveDeferredOperation(ctx, token); err != nil {
		t.Fatalf("RemoveDeferredOperation: %v", err)
	}
	if _, err := fdc.res.Get(ctx, token, metav1.GetOptions{}); err == nil {
		t.Error("expected 'not found' after delete")
	}
}

func TestCRDJournal_ConcurrentAdmissionHonorsSharedLimit(t *testing.T) {
	fdc := newFakeDynamicClient()
	first := NewCRDVolumeJournal(fdc)
	second := NewCRDVolumeJournal(fdc)
	first.SetThresholds(1, 1)
	second.SetThresholds(1, 1)

	results := make(chan error, 2)
	go func() {
		_, err := first.CreateDeferredOperation(context.Background(), DeferredOperation{VolumeID: "first", ArrayID: "array-1", OperationType: OpDeviceCleanup})
		results <- err
	}()
	go func() {
		_, err := second.CreateDeferredOperation(context.Background(), DeferredOperation{VolumeID: "second", ArrayID: "array-1", OperationType: OpDeviceCleanup})
		results <- err
	}()

	successes := 0
	queueFull := 0
	for i := 0; i < 2; i++ {
		switch err := <-results; {
		case err == nil:
			successes++
		case errors.Is(err, ErrQueueFull):
			queueFull++
		default:
			t.Fatalf("unexpected admission error: %v", err)
		}
	}
	if successes != 1 || queueFull != 1 {
		t.Fatalf("expected one admission and one queue-full rejection, got successes=%d queueFull=%d", successes, queueFull)
	}
}

func TestCRDJournal_OwnerFiltering(t *testing.T) {
	fdc := newFakeDynamicClient()
	ctx := context.Background()
	for _, op := range []DeferredOperation{
		{VolumeID: "owned", ArrayID: "array-1", IdempotencyToken: "owned-token", DriverName: "driver-a", InstanceUID: "instance-a"},
		{VolumeID: "other-instance", ArrayID: "array-1", IdempotencyToken: "other-instance-token", DriverName: "driver-a", InstanceUID: "instance-b"},
		{VolumeID: "other-driver", ArrayID: "array-1", IdempotencyToken: "other-driver-token", DriverName: "driver-b", InstanceUID: "instance-a"},
	} {
		obj, err := crdOpToUnstructured(op)
		if err != nil {
			t.Fatalf("crdOpToUnstructured: %v", err)
		}
		if _, err := fdc.res.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed resource: %v", err)
		}
	}

	journal := NewCRDVolumeJournalForOwner(fdc, "driver-a", "instance-a")
	ops, err := journal.crd.crdList(ctx, "array-1")
	if err != nil {
		t.Fatalf("crdList: %v", err)
	}
	if len(ops) != 1 || ops[0].VolumeID != "owned" {
		t.Fatalf("expected only the matching owner, got %#v", ops)
	}
}

func TestCRDJournal_ListReturnsMalformedResourceError(t *testing.T) {
	fdc := newFakeDynamicClient()
	ctx := context.Background()
	obj := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "dr.storage.dell.com/v1",
		"kind":       "VolumeJournal",
		"metadata": map[string]interface{}{
			"name": "malformed-token",
			"labels": map[string]interface{}{
				labelDriverType: "powermax",
				labelArrayID:    "array-1",
			},
		},
		"spec": map[string]interface{}{"journalEntries": []interface{}{}},
	}}
	if _, err := fdc.res.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed malformed resource: %v", err)
	}
	if _, err := (&crdJournal{client: fdc}).crdList(ctx, "array-1"); err == nil {
		t.Fatal("expected malformed durable record to fail the list")
	}
}

func TestCRDJournal_ListOnlyPowerMaxOwnedResources(t *testing.T) {
	fdc := newFakeDynamicClient()
	ctx := context.Background()

	powerMaxOp := DeferredOperation{
		OperationType:    OpMetroPairing,
		VolumeID:         "powermax-volume",
		ArrayID:          "powermax-array",
		IdempotencyToken: "powermax-token",
	}
	powerMaxObj, err := crdOpToUnstructured(powerMaxOp)
	if err != nil {
		t.Fatalf("crdOpToUnstructured PowerMax operation: %v", err)
	}
	if _, err := fdc.res.Create(ctx, powerMaxObj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed PowerMax resource: %v", err)
	}

	csmDROp := powerMaxOp
	csmDROp.VolumeID = "csm-dr-volume"
	csmDROp.IdempotencyToken = "csm-dr-token"
	csmDROp.ArrayID = "csm-dr-array"
	csmDRObj, err := crdOpToUnstructured(csmDROp)
	if err != nil {
		t.Fatalf("crdOpToUnstructured CSM-DR operation: %v", err)
	}
	csmDRObj.SetLabels(map[string]string{"dr.storage.dell.com/driver-type": "powerstore"})
	if _, err := fdc.res.Create(ctx, csmDRObj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed CSM-DR resource: %v", err)
	}

	journal := NewCRDVolumeJournal(fdc)
	ops, err := journal.crd.crdList(ctx, "")
	if err != nil {
		t.Fatalf("crdList: %v", err)
	}
	if len(ops) != 1 || ops[0].VolumeID != powerMaxOp.VolumeID {
		t.Fatalf("expected only PowerMax operation, got %#v", ops)
	}
	if err := journal.crd.crdDelete(ctx, csmDROp.IdempotencyToken); err != nil {
		t.Fatalf("crdDelete foreign resource: %v", err)
	}
	if _, err := fdc.res.Get(ctx, csmDROp.IdempotencyToken, metav1.GetOptions{}); err != nil {
		t.Fatalf("expected foreign resource to remain after PowerMax delete: %v", err)
	}
}

func TestCRDJournal_ListExcludesTerminalAndCompletedResources(t *testing.T) {
	fdc := newFakeDynamicClient()
	ctx := context.Background()
	for _, status := range []string{OpStatusTerminal, OpStatusCompleted} {
		op := DeferredOperation{
			OperationType:    OpDeviceCleanup,
			VolumeID:         status,
			ArrayID:          "array-1",
			IdempotencyToken: status + "-token",
			Status:           status,
		}
		obj, err := crdOpToUnstructured(op)
		if err != nil {
			t.Fatalf("crdOpToUnstructured: %v", err)
		}
		if _, err := fdc.res.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
			t.Fatalf("seed %s resource: %v", status, err)
		}
	}

	pending := DeferredOperation{
		OperationType:    OpDeviceCleanup,
		VolumeID:         "pending",
		ArrayID:          "array-1",
		IdempotencyToken: "pending-token",
		Status:           OpStatusPending,
	}
	obj, err := crdOpToUnstructured(pending)
	if err != nil {
		t.Fatalf("crdOpToUnstructured pending: %v", err)
	}
	if _, err := fdc.res.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed pending resource: %v", err)
	}

	ops, err := (&crdJournal{client: fdc}).crdList(ctx, "array-1")
	if err != nil {
		t.Fatalf("crdList: %v", err)
	}
	if len(ops) != 1 || ops[0].Status != OpStatusPending {
		t.Fatalf("expected only pending operation, got %#v", ops)
	}
}

func TestCRDJournal_QueueStatusUsesDurableStore(t *testing.T) {
	fdc := newFakeDynamicClient()
	j := NewCRDVolumeJournal(fdc)
	j.SetThresholds(1, 1)
	if _, err := j.CreateDeferredOperation(context.Background(), DeferredOperation{VolumeID: "queue-status", ArrayID: "array-1", OperationType: OpDeviceCleanup}); err != nil {
		t.Fatalf("CreateDeferredOperation: %v", err)
	}
	status := j.GetQueueStatus()
	if status.Count != 1 || !status.AtLimit || !status.AtWarning {
		t.Fatalf("unexpected durable queue status: %#v", status)
	}
	arrayStatus := j.GetQueueStatusForArray("array-1")
	if arrayStatus.Count != 1 || !arrayStatus.AtLimit {
		t.Fatalf("unexpected durable array queue status: %#v", arrayStatus)
	}
}

func TestVolumeJournal_SyncFromCRD_EmptyStore(t *testing.T) {
	fdc := newFakeDynamicClient()
	j := NewCRDVolumeJournal(fdc)
	ctx := context.Background()

	// Sync from an empty CRD store — should succeed without errors.
	if err := j.SyncFromCRD(ctx); err != nil {
		t.Fatalf("SyncFromCRD on empty store: %v", err)
	}
	if len(j.GetAllOperations()) != 0 {
		t.Error("expected 0 operations after sync from empty store")
	}
}

func TestVolumeJournal_SyncFromCRD_WithExistingOps(t *testing.T) {
	fdc := newFakeDynamicClient()
	j := NewCRDVolumeJournal(fdc)
	ctx := context.Background()

	// Seed the fake store with one op directly.
	op := DeferredOperation{ // nosec G101
		OperationType:    OpDeviceCleanup,
		VolumeID:         "vol-sync-1",
		ArrayID:          "000197900046",
		IdempotencyToken: "sync-token-1",
		SubmissionTime:   time.Now().UTC(),
	}
	obj, _ := crdOpToUnstructured(op)
	if _, err := fdc.res.Create(ctx, obj, metav1.CreateOptions{}); err != nil {
		t.Fatalf("seed fake store: %v", err)
	}

	// Sync should populate in-memory cache.
	if err := j.SyncFromCRD(ctx); err != nil {
		t.Fatalf("SyncFromCRD: %v", err)
	}
	all := j.GetAllOperations()
	if len(all) != 1 {
		t.Fatalf("expected 1 op after sync, got %d", len(all))
	}
	if all[0].VolumeID != op.VolumeID {
		t.Errorf("expected VolumeID %s, got %s", op.VolumeID, all[0].VolumeID)
	}
}

func TestVolumeJournal_CRDReadErrorDoesNotFallbackToLocalCache(t *testing.T) {
	fdc := newFakeDynamicClient()
	fdc.res.listErr = fmt.Errorf("API server unavailable")
	j := NewCRDVolumeJournal(fdc)
	j.operations = []DeferredOperation{{VolumeID: "local-only", ArrayID: "array-1", Status: OpStatusPending}}

	if ops := j.GetDeferredOperations("array-1"); len(ops) != 0 {
		t.Fatalf("expected no local fallback after durable read failure, got %#v", ops)
	}
}

func TestVolumeJournal_SyncFromCRD_ListError(t *testing.T) {
	fdc := newFakeDynamicClient()
	fdc.res.listErr = fmt.Errorf("API server unavailable")
	j := NewCRDVolumeJournal(fdc)

	err := j.SyncFromCRD(context.Background())
	if err == nil {
		t.Fatal("expected error when list fails")
	}
}

func TestCRDJournal_NoCRD_FallbackInMemory(t *testing.T) {
	// VolumeJournal without CRD client should work in-memory only.
	j := NewVolumeJournal()
	op := DeferredOperation{
		OperationType: OpMetroPairing,
		VolumeID:      "vol-mem",
		ArrayID:       "000197900046",
	}
	token, err := j.CreateDeferredOperation(context.Background(), op)
	if err != nil {
		t.Fatalf("in-memory create: %v", err)
	}
	if err := j.UpdateDeferredOperation(context.Background(), token, 1, "retry"); err != nil {
		t.Fatalf("in-memory update: %v", err)
	}
	if err := j.RemoveDeferredOperation(context.Background(), token); err != nil {
		t.Fatalf("in-memory remove: %v", err)
	}
	if len(j.GetAllOperations()) != 0 {
		t.Error("expected empty journal after remove")
	}
}

func TestVolumeJournal_QueueStatus_AtLimit(t *testing.T) {
	j := NewVolumeJournal()
	j.SetThresholds(2, 3) // low thresholds for the test
	for i := 0; i < 3; i++ {
		_, err := j.CreateDeferredOperation(context.Background(), DeferredOperation{
			OperationType: OpMetroPairing,
			VolumeID:      fmt.Sprintf("vol-%d", i),
			ArrayID:       "arr1",
		})
		if err != nil {
			t.Fatalf("op %d: unexpected error: %v", i, err)
		}
	}
	qs := j.GetQueueStatus()
	if !qs.AtLimit {
		t.Error("expected AtLimit true at hard limit")
	}
	if !qs.AtWarning {
		t.Error("expected AtWarning true at hard limit")
	}
}
