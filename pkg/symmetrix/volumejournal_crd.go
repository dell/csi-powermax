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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/dell/csmlog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8stypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
)

// volumeJournalGVR is the GroupVersionResource for the VolumeJournal CRD.
var volumeJournalGVR = schema.GroupVersionResource{
	Group:    "dr.storage.dell.com",
	Version:  "v1",
	Resource: "volumejournals",
}

var volumeJournalLeaseGVR = schema.GroupVersionResource{
	Group:    "coordination.k8s.io",
	Version:  "v1",
	Resource: "leases",
}

const (
	volumeJournalLeaseName      = "csi-powermax-metro-journal-queue"
	volumeJournalLeaseNamespace = "kube-system"
	volumeJournalLeaseDuration  = 30 * time.Second
)

// volumeJournalLabels maps label key names for the VolumeJournal CRD resources.
const (
	labelArrayID       = "dr.storage.dell.com/array-id"
	labelOperationType = "dr.storage.dell.com/operation-type"
	labelDriverType    = "dr.storage.dell.com/driver-type"
	labelDriverName    = "dr.storage.dell.com/driver-name"
	labelInstanceUID   = "dr.storage.dell.com/instance-uid"
)

// crdJournal is a VolumeJournal that persists deferred operations as
// VolumeJournal CRD resources in the Kubernetes API. Each DeferredOperation
// is stored as a single cluster-scoped VolumeJournal resource named by its
// idempotency token (UUID).
//
// The full DeferredOperation is JSON-encoded in the spec.journalEntries[0].request
// field (base64 per CRD schema). All other required CRD fields are populated from
// the DeferredOperation struct fields.
//
// Array-specific listing uses a label selector on labelArrayID for O(1) server-side
// filtering instead of listing all resources and filtering client-side.
type crdJournal struct {
	client         dynamic.Interface
	holderIdentity string
	driverName     string
	instanceUID    string
}

// NewCRDVolumeJournal creates a VolumeJournal backed by the VolumeJournal CRD.
// In production, pass the in-cluster dynamic client. For unit testing use
// NewVolumeJournal (in-memory) instead.
func NewCRDVolumeJournal(client dynamic.Interface) *VolumeJournal {
	return NewCRDVolumeJournalForOwner(client, "", "")
}

// NewCRDVolumeJournalForOwner creates a journal restricted to one PowerMax
// driver instance. Empty owner values retain compatibility for legacy callers.
func NewCRDVolumeJournalForOwner(client dynamic.Interface, driverName, instanceUID string) *VolumeJournal {
	j := &VolumeJournal{
		warningThreshold: QueueWarningThreshold,
		hardLimit:        QueueHardLimit,
	}
	j.crd = &crdJournal{
		client:         client,
		holderIdentity: uuid.NewString(),
		driverName:     driverName,
		instanceUID:    instanceUID,
	}
	return j
}

// crdOpToUnstructured serialises a DeferredOperation into an unstructured
// VolumeJournal resource. The full operation JSON is base64-embedded in
// spec.journalEntries[0].request per the CRD schema.
// includes ownership labels (driverType, driverName, instanceUID)
// for isolation between drivers and CSM instances.
func crdOpToUnstructured(op DeferredOperation) (*unstructured.Unstructured, error) {
	opJSON, err := json.Marshal(op)
	if err != nil {
		return nil, fmt.Errorf("crdJournal: marshal DeferredOperation: %w", err)
	}

	// Kubernetes names must be valid DNS subdomain; UUIDs satisfy this.
	name := op.IdempotencyToken
	nodeName := op.NodeName
	if nodeName == "" {
		nodeName = "unknown"
	}
	status := op.Status
	if status == "" {
		status = OpStatusPending
	}

	// Build labels with ownership isolation (Issue 7 fix)
	labels := map[string]interface{}{
		labelArrayID:       op.ArrayID,
		labelOperationType: string(op.OperationType),
		labelDriverType:    "powermax", // Hardcoded for PowerMax driver
	}
	if op.DriverName != "" {
		labels[labelDriverName] = op.DriverName
	}
	if op.InstanceUID != "" {
		labels[labelInstanceUID] = op.InstanceUID
	}

	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "dr.storage.dell.com/v1",
			"kind":       "VolumeJournal",
			"metadata": map[string]interface{}{
				"name":   name,
				"labels": labels,
			},
			"spec": map[string]interface{}{
				"driverType":    "powermax",
				"driverName":    op.DriverName,
				"instanceUID":   op.InstanceUID,
				"volumeUUID":    op.IdempotencyToken,
				"volumeHandle":  op.VolumeID,
				"failoverArray": op.ArrayID,
				"originalArray": "unknown",
				"sourceCluster": "unknown",
				"targetCluster": "unknown",
				"journalEntries": []interface{}{
					map[string]interface{}{
						"array":     op.ArrayID,
						"host":      nodeName,
						"operation": string(op.OperationType),
						"request":   base64.StdEncoding.EncodeToString(opJSON), // base64-encoded per CRD format: byte schema
						"status":    status,
						"time":      op.SubmissionTime.UTC().Format(time.RFC3339),
					},
				},
			},
		},
	}
	return obj, nil
}

// crdUnstructuredToOp deserialises a VolumeJournal unstructured resource back
// into a DeferredOperation by decoding the JSON embedded in
// spec.journalEntries[0].request.
func crdUnstructuredToOp(obj *unstructured.Unstructured) (DeferredOperation, error) {
	journalEntries, found, err := unstructured.NestedSlice(obj.Object, "spec", "journalEntries")
	if err != nil || !found || len(journalEntries) == 0 {
		return DeferredOperation{}, fmt.Errorf("crdJournal: journalEntries not found in %s", obj.GetName())
	}

	entry, ok := journalEntries[0].(map[string]interface{})
	if !ok {
		return DeferredOperation{}, fmt.Errorf("crdJournal: unexpected journalEntry type in %s", obj.GetName())
	}

	// The request field is stored as base64-encoded JSON (per CRD format: byte schema).
	raw, _, _ := unstructured.NestedFieldNoCopy(entry, "request")
	var rawStr string
	switch v := raw.(type) {
	case []byte:
		rawStr = string(v)
	case string:
		rawStr = v
	default:
		return DeferredOperation{}, fmt.Errorf("crdJournal: unexpected request field type %T in %s", raw, obj.GetName())
	}

	// Decode: must be valid base64.  Fallback to treating as raw JSON is
	// deliberately removed — it silently swallowed corruption (L-2).
	decoded, b64Err := base64.StdEncoding.DecodeString(rawStr)
	if b64Err != nil {
		return DeferredOperation{}, fmt.Errorf("crdJournal: base64 decode request in %s: %w", obj.GetName(), b64Err)
	}
	var op DeferredOperation
	if jsonErr := json.Unmarshal(decoded, &op); jsonErr != nil {
		return DeferredOperation{}, fmt.Errorf("crdJournal: unmarshal request in %s: %w", obj.GetName(), jsonErr)
	}
	return op, nil
}

func (c *crdJournal) leaseResource() dynamic.ResourceInterface {
	return c.client.Resource(volumeJournalLeaseGVR).Namespace(volumeJournalLeaseNamespace)
}

func (c *crdJournal) acquireQueueLease(ctx context.Context) error {
	resource := c.leaseResource()
	now := time.Now().UTC()
	lease, err := resource.Get(ctx, volumeJournalLeaseName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) || (err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")) {
		obj := &unstructured.Unstructured{Object: map[string]interface{}{
			"apiVersion": "coordination.k8s.io/v1",
			"kind":       "Lease",
			"metadata": map[string]interface{}{
				"name":      volumeJournalLeaseName,
				"namespace": volumeJournalLeaseNamespace,
			},
			"spec": map[string]interface{}{
				"holderIdentity":       c.holderIdentity,
				"leaseDurationSeconds": int64(volumeJournalLeaseDuration / time.Second),
				"acquireTime":          now.Format(time.RFC3339Nano),
			},
		}}
		_, err = resource.Create(ctx, obj, metav1.CreateOptions{})
		if err != nil && strings.Contains(strings.ToLower(err.Error()), "already exists") {
			return fmt.Errorf("VolumeJournal queue is busy")
		}
		return err
	}
	if err != nil {
		return err
	}

	holder, _, _ := unstructured.NestedString(lease.Object, "spec", "holderIdentity")
	acquiredAt, _, _ := unstructured.NestedString(lease.Object, "spec", "acquireTime")
	acquiredTime, parseErr := time.Parse(time.RFC3339Nano, acquiredAt)
	if holder != c.holderIdentity && parseErr == nil && now.Before(acquiredTime.Add(volumeJournalLeaseDuration)) {
		return fmt.Errorf("VolumeJournal queue is busy")
	}
	if holder != c.holderIdentity && parseErr != nil {
		return fmt.Errorf("VolumeJournal queue lease has invalid acquireTime")
	}
	_ = unstructured.SetNestedField(lease.Object, c.holderIdentity, "spec", "holderIdentity")
	_ = unstructured.SetNestedField(lease.Object, int64(volumeJournalLeaseDuration/time.Second), "spec", "leaseDurationSeconds")
	_ = unstructured.SetNestedField(lease.Object, now.Format(time.RFC3339Nano), "spec", "acquireTime")
	_, err = resource.Update(ctx, lease, metav1.UpdateOptions{})
	if apierrors.IsConflict(err) || (err != nil && strings.Contains(strings.ToLower(err.Error()), "conflict")) {
		return fmt.Errorf("VolumeJournal queue is busy")
	}
	return err
}

func (c *crdJournal) releaseQueueLease(ctx context.Context) error {
	err := c.leaseResource().Delete(ctx, volumeJournalLeaseName, metav1.DeleteOptions{})
	if apierrors.IsNotFound(err) || (err != nil && strings.Contains(strings.ToLower(err.Error()), "not found")) {
		return nil
	}
	return err
}

func (c *crdJournal) withQueueLease(ctx context.Context, fn func() error) error {
	for {
		err := c.acquireQueueLease(ctx)
		if err == nil {
			break
		}
		if !strings.Contains(err.Error(), "VolumeJournal queue is busy") {
			return err
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	defer func() {
		if err := c.releaseQueueLease(context.Background()); err != nil {
			csmlog.Warnf("crdJournal: failed to release queue lease: %v", err)
		}
	}()
	return fn()
}

// crdCreate persists a new DeferredOperation in the cluster.
func (c *crdJournal) crdCreate(ctx context.Context, op DeferredOperation) error {
	obj, err := crdOpToUnstructured(op)
	if err != nil {
		return err
	}
	_, err = c.client.Resource(volumeJournalGVR).Create(ctx, obj, metav1.CreateOptions{})
	return err
}

func (c *crdJournal) getPowerMaxResource(ctx context.Context, token string) (*unstructured.Unstructured, error) {
	obj, err := c.client.Resource(volumeJournalGVR).Get(ctx, token, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if !c.ownsResource(obj) {
		return nil, nil
	}
	return obj, nil
}

func (c *crdJournal) ownsResource(obj *unstructured.Unstructured) bool {
	resourceLabels := obj.GetLabels()
	if resourceLabels[labelDriverType] != "powermax" {
		return false
	}
	if c.driverName != "" && resourceLabels[labelDriverName] != c.driverName {
		return false
	}
	if c.instanceUID != "" && resourceLabels[labelInstanceUID] != c.instanceUID {
		return false
	}
	return true
}

// crdUpdate patches the journalEntry status, retryCount, lastError, and status for the
// named resource using a server-side merge patch to avoid resourceVersion
// conflicts when multiple controller replicas are running concurrently.
func (c *crdJournal) crdUpdate(ctx context.Context, op DeferredOperation) error {
	owned, err := c.getPowerMaxResource(ctx, op.IdempotencyToken)
	if err != nil {
		return err
	}
	if owned == nil {
		return nil
	}
	// Re-encode the updated operation.
	opJSON, err := json.Marshal(op)
	if err != nil {
		return fmt.Errorf("crdJournal: marshal updated op: %w", err)
	}

	nodeName := op.NodeName
	if nodeName == "" {
		nodeName = "unknown"
	}

	conditionStatus := "Unknown"
	reason := "Pending"
	switch op.Status {
	case OpStatusCompleted:
		conditionStatus = "True"
		reason = "Completed"
	case OpStatusFailed:
		conditionStatus = "False"
		reason = "Retrying"
	case OpStatusTerminal:
		conditionStatus = "False"
		reason = "Terminal"
	}
	message := op.LastError
	if message == "" {
		message = fmt.Sprintf("VolumeJournal operation is %s", op.Status)
	}
	status := map[string]interface{}{
		"phase":          op.Status,
		"operationCount": int64(1),
		"retryCount":     int64(op.RetryCount),
		"lastError":      op.LastError,
		"conditions": []interface{}{
			map[string]interface{}{
				"type":               "ReconciliationReady",
				"status":             conditionStatus,
				"reason":             reason,
				"message":            message,
				"lastTransitionTime": time.Now().UTC().Format(time.RFC3339),
			},
		},
	}
	patch := map[string]interface{}{
		"spec": map[string]interface{}{
			"driverType":  "powermax",
			"driverName":  op.DriverName,
			"instanceUID": op.InstanceUID,
			"journalEntries": []interface{}{
				map[string]interface{}{
					"array":     op.ArrayID,
					"host":      nodeName,
					"operation": string(op.OperationType),
					"request":   base64.StdEncoding.EncodeToString(opJSON),
					"status":    op.Status,
					"time":      op.SubmissionTime.UTC().Format(time.RFC3339),
				},
			},
		},
	}
	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("crdJournal: marshal spec patch: %w", err)
	}
	if _, err = c.client.Resource(volumeJournalGVR).Patch(ctx, op.IdempotencyToken, k8stypes.MergePatchType, patchBytes, metav1.PatchOptions{}); err != nil {
		return err
	}
	statusBytes, err := json.Marshal(map[string]interface{}{"status": status})
	if err != nil {
		return fmt.Errorf("crdJournal: marshal status patch: %w", err)
	}
	_, err = c.client.Resource(volumeJournalGVR).Patch(ctx, op.IdempotencyToken, k8stypes.MergePatchType, statusBytes, metav1.PatchOptions{}, "status")
	return err
}

// crdDelete removes a VolumeJournal resource by name (idempotency token).
func (c *crdJournal) crdDelete(ctx context.Context, token string) error {
	owned, err := c.getPowerMaxResource(ctx, token)
	if err != nil {
		return err
	}
	if owned == nil {
		return nil
	}
	return c.client.Resource(volumeJournalGVR).Delete(ctx, token, metav1.DeleteOptions{})
}

// crdList returns all DeferredOperations, optionally filtered by arrayID.
func (c *crdJournal) crdList(ctx context.Context, arrayID string) ([]DeferredOperation, error) {
	selector := labels.Set{labelDriverType: "powermax"}
	if c.driverName != "" {
		selector[labelDriverName] = c.driverName
	}
	if c.instanceUID != "" {
		selector[labelInstanceUID] = c.instanceUID
	}
	if arrayID != "" {
		// Use labels.Set.String() to produce a properly formatted selector
		// (L-1: avoids injection of special characters in the array ID).
		selector[labelArrayID] = arrayID
	}
	opts := metav1.ListOptions{LabelSelector: selector.String()}
	list, err := c.client.Resource(volumeJournalGVR).List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("crdJournal: list: %w", err)
	}
	ops := make([]DeferredOperation, 0, len(list.Items))
	for i := range list.Items {
		op, convErr := crdUnstructuredToOp(&list.Items[i])
		if convErr != nil {
			return nil, fmt.Errorf("crdJournal: malformed resource %s: %w", list.Items[i].GetName(), convErr)
		}
		if !isPendingOperation(op.Status) {
			continue
		}
		ops = append(ops, op)
	}
	return ops, nil
}
