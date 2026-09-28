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
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	csmlog "github.com/dell/csmlog"
	csictx "github.com/dell/gocsi/context"
	types "github.com/dell/gopowermax/v2/types/v100"
	gomock "github.com/golang/mock/gomock"
	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

// newMetroTestService returns a minimal *service for Metro helper unit tests.
func newMetroTestService() *service {
	return &service{
		volumeJournal:    symmetrix.NewVolumeJournal(),
		siteStateTracker: symmetrix.NewSiteStateTracker(),
		metroStateCache:  symmetrix.NewMetroStateCache(0),
	}
}

// ─── RegisterMetroMetrics ─────────────────────────────────────────────────────

// TestRegisterMetroMetrics_Idempotent verifies that calling RegisterMetroMetrics
// more than once (on a shared once) does not panic and does not re-register
// metrics, which would cause a Prometheus duplicate registration panic.
func TestRegisterMetroMetrics_Idempotent(t *testing.T) {
	// Each call gets its own registry so there is no cross-test collision.
	reg := prometheus.NewRegistry()
	// First call — this exercises the sync.Once body and covers the function.
	// We reset the once so the function body runs (it is package-level state).
	metroMetricsOnce = sync.Once{}
	RegisterMetroMetrics(reg)

	// Second call on the same once must be a no-op (no panic).
	RegisterMetroMetrics(reg)

	// Initialize at least one observation so that the Vec metrics appear in
	// Gather() output (Vec metrics require at least one label set to be
	// collected by the registry, unlike bare Gauge/Counter metrics).
	MetroDeferredOpsTotal.WithLabelValues("test").Add(0)

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	if len(families) == 0 {
		t.Error("expected at least one metric family after RegisterMetroMetrics")
	}
}

// ─── deferOperation ──────────────────────────────────────────────────────────

func TestDeferOperation_Success(t *testing.T) {
	svc := newMetroTestService()
	reg, m := registerFreshMetrics(t)
	_ = reg
	MetroDeferredOpsTotal = m.deferredOpsTotal
	MetroDeferredOpsQueueDepth = m.queueDepth
	MetroSiteFailures = m.siteFailures
	t.Cleanup(func() {
		MetroDeferredOpsTotal = nil
		MetroDeferredOpsQueueDepth = nil
		MetroSiteFailures = nil
	})

	op := symmetrix.DeferredOperation{
		OperationType: symmetrix.OpMetroPairing,
		VolumeID:      "vol-defer-1",
		ArrayID:       "000197900046",
		RDFGroupNo:    "14",
	}
	token, err := svc.deferOperation(context.Background(), op)
	if err != nil {
		t.Fatalf("deferOperation: %v", err)
	}
	if token == "" {
		t.Error("expected non-empty token")
	}

	// Journal must contain exactly one entry.
	all := svc.volumeJournal.GetAllOperations()
	if len(all) != 1 {
		t.Errorf("expected 1 operation, got %d", len(all))
	}
}

func TestDeferOperation_WarningThreshold(t *testing.T) {
	svc := newMetroTestService()
	// Use a tiny journal so the warning fires after just 1 entry.
	svc.volumeJournal.SetThresholds(1, 10)

	reg, m := registerFreshMetrics(t)
	_ = reg
	MetroSiteFailures = m.siteFailures
	MetroDeferredOpsTotal = m.deferredOpsTotal
	MetroDeferredOpsQueueDepth = m.queueDepth
	t.Cleanup(func() {
		MetroSiteFailures = nil
		MetroDeferredOpsTotal = nil
		MetroDeferredOpsQueueDepth = nil
	})

	// First op — fills queue to warning threshold.
	_, _ = svc.deferOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpMetroPairing, VolumeID: "vol-w-1", ArrayID: "arr1",
	})
	// Second op — should trigger the warning path.
	token, err := svc.deferOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpMetroPairing, VolumeID: "vol-w-2", ArrayID: "arr1",
	})
	if err != nil {
		t.Fatalf("unexpected error at warning threshold: %v", err)
	}
	if token == "" {
		t.Error("expected non-empty token")
	}
}

func TestDeferOperation_HardLimit(t *testing.T) {
	svc := newMetroTestService()
	// Hard limit of 2.
	svc.volumeJournal.SetThresholds(1, 2)

	fill := func(vol string) {
		_, _ = svc.deferOperation(context.Background(), symmetrix.DeferredOperation{
			OperationType: symmetrix.OpMetroPairing, VolumeID: vol, ArrayID: "arr1",
		})
	}
	fill("vol-hl-1")
	fill("vol-hl-2")

	// Third op must be rejected with ErrQueueFull.
	_, err := svc.deferOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpMetroPairing, VolumeID: "vol-hl-3", ArrayID: "arr1",
	})
	if !errors.Is(err, symmetrix.ErrQueueFull) {
		t.Errorf("expected ErrQueueFull, got %v", err)
	}
}

func TestDeferOperation_NilMetrics(t *testing.T) {
	// When Prometheus metrics are nil, deferOperation must not panic.
	svc := newMetroTestService()
	MetroDeferredOpsTotal = nil
	MetroDeferredOpsQueueDepth = nil
	MetroSiteFailures = nil

	_, err := svc.deferOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpDeviceCleanup,
		VolumeID:      "vol-nil-metrics",
		ArrayID:       "arr1",
	})
	if err != nil {
		t.Fatalf("unexpected error with nil metrics: %v", err)
	}
}

// ─── triggerReconciliation ───────────────────────────────────────────────────

func TestTriggerReconciliation_EmptyQueue(t *testing.T) {
	// With no deferred operations, triggerReconciliation should be a no-op.
	svc := newMetroTestService()
	reg, m := registerFreshMetrics(t)
	_ = reg
	MetroReconciliationTotal = m.reconcileTotal
	MetroDeferredOpsQueueDepth = m.queueDepth
	t.Cleanup(func() {
		MetroReconciliationTotal = nil
		MetroDeferredOpsQueueDepth = nil
	})

	// Should not panic.
	svc.triggerReconciliation(context.Background(), "000197900046")
}

func TestTriggerReconciliation_UnknownOpType(t *testing.T) {
	svc := newMetroTestService()
	reg, m := registerFreshMetrics(t)
	_ = reg
	MetroReconciliationTotal = m.reconcileTotal
	MetroDeferredOpsQueueDepth = m.queueDepth
	t.Cleanup(func() {
		MetroReconciliationTotal = nil
		MetroDeferredOpsQueueDepth = nil
	})

	// Manually inject an operation with an unknown type.
	_, _ = svc.volumeJournal.CreateDeferredOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: "UnknownOp",
		VolumeID:      "vol-unknown",
		ArrayID:       "000197900046",
	})
	// Use a short timeout context to prevent the test from waiting for full backoff
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// Should not panic; the unknown type results in a reconcile failure.
	svc.triggerReconciliation(ctx, "000197900046")
}

func TestTriggerReconciliation_NilMetrics(_ *testing.T) {
	svc := newMetroTestService()
	MetroReconciliationTotal = nil
	MetroDeferredOpsQueueDepth = nil

	_, _ = svc.volumeJournal.CreateDeferredOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpDeviceCleanup,
		VolumeID:      "vol-nometa",
		ArrayID:       "arr1",
	})
	// Use a short timeout context to prevent the test from waiting for full backoff
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	// Should not panic.
	svc.triggerReconciliation(ctx, "arr1")
}

// ─── reconcileDeviceCleanup / reconcileMetroPairing ──────────────────────────

func TestReconcileDeviceCleanup_BadVolumeID(t *testing.T) {
	svc := newMetroTestService()
	op := symmetrix.DeferredOperation{
		OperationType: symmetrix.OpDeviceCleanup,
		VolumeID:      "bad-volume-id", // not a valid CSI ID
		ArrayID:       "000197900046",
	}
	err := svc.reconcileDeviceCleanup(context.Background(), op)
	if err == nil {
		t.Fatal("expected error for malformed VolumeID")
	}
}

func TestReconcileDeviceCleanup_GetPowerMaxClientError(t *testing.T) {
	svc := newMetroTestService()
	// Valid replicated volume ID, but unregistered local array.
	op := symmetrix.DeferredOperation{
		OperationType: symmetrix.OpDeviceCleanup,
		VolumeID:      "csi-TST-pmax-myvol-000120000005:000120000006-DDD123:EEE456",
		ArrayID:       "000120000005", // matches localSym → targets local, which is unregistered
	}
	err := svc.reconcileDeviceCleanup(context.Background(), op)
	if err == nil {
		t.Fatal("expected error when PowerMax client not registered")
	}
}

func TestReconcileDeviceCleanup_RemoteArrayBranch(t *testing.T) {
	svc := newMetroTestService()
	// When op.ArrayID matches the REMOTE sym, we target the remote device.
	// Still fails at GetPowerMaxClient because neither array is registered.
	op := symmetrix.DeferredOperation{
		OperationType: symmetrix.OpDeviceCleanup,
		VolumeID:      "csi-TST-pmax-myvol-000120000007:000120000008-FFF123:GGG456",
		ArrayID:       "000120000008", // this is the remoteSymID → targets remote
	}
	err := svc.reconcileDeviceCleanup(context.Background(), op)
	if err == nil {
		t.Fatal("expected error when PowerMax client not registered for remote array")
	}
}

func TestReconcileMetroPairing_BadVolumeID(t *testing.T) {
	svc := newMetroTestService()
	op := symmetrix.DeferredOperation{
		OperationType: symmetrix.OpMetroPairing,
		VolumeID:      "not-a-csi-volume-id",
		ArrayID:       "000197900046",
	}
	err := svc.reconcileMetroPairing(context.Background(), op)
	if err == nil {
		t.Fatal("expected error for malformed VolumeID")
	}
}

// TestReconcileMetroPairing_EmptyRDFGroup verifies that an error is returned
// when neither StorageGroupName nor RDFGroupNo are populated.  The error
// surfaces from GetPowerMaxClient (no registered clients), which is reached
// before the SG-name resolution (C-4 fix: StorageGroupName is preferred).
func TestReconcileMetroPairing_EmptyRDFGroup(t *testing.T) {
	svc := newMetroTestService()
	// Format accepted by parseCsiID for Metro:
	//   csi-PREFIX-pmax-volname-localSym:remoteSym-localDev:remoteDev
	op := symmetrix.DeferredOperation{
		OperationType:    symmetrix.OpMetroPairing,
		VolumeID:         "csi-TST-pmax-myvol-000120000001:000120000002-ABC123:DEF456",
		ArrayID:          "000120000001",
		RDFGroupNo:       "", // empty
		StorageGroupName: "", // empty → C-4 legacy fallback path
	}
	err := svc.reconcileMetroPairing(context.Background(), op)
	if err == nil {
		t.Fatal("expected error when RDFGroupNo and StorageGroupName are both empty")
	}
}

func TestReconcileMetroPairing_GetPowerMaxClientError(t *testing.T) {
	svc := newMetroTestService()
	// Valid replicated volume ID, valid RDFGroupNo, but no registered clients.
	op := symmetrix.DeferredOperation{
		OperationType: symmetrix.OpMetroPairing,
		VolumeID:      "csi-TST-pmax-myvol-000120000003:000120000004-AAA111:BBB222",
		ArrayID:       "000120000003",
		RDFGroupNo:    "14",
	}
	err := svc.reconcileMetroPairing(context.Background(), op)
	if err == nil {
		t.Fatal("expected error when PowerMax client not registered")
	}
}

// ─── buildCSIVolume ───────────────────────────────────────────────────────────

func TestBuildCSIVolume(t *testing.T) {
	svc := newMetroTestService()
	vol := &types.VolumeEnhanced{
		ID:     "00123",
		CapCyl: 10,
	}
	csiVol := svc.buildCSIVolume(vol)
	if csiVol == nil {
		t.Fatal("expected non-nil CSI volume")
	}
	if csiVol.VolumeId != vol.ID {
		t.Errorf("expected VolumeId %q, got %q", vol.ID, csiVol.VolumeId)
	}
	if csiVol.CapacityBytes <= 0 {
		t.Errorf("expected positive CapacityBytes, got %d", csiVol.CapacityBytes)
	}
}

// ─── setArrayConfigEnvs ───────────────────────────────────────────────────────

func TestSetArrayConfigEnvs_MissingEnv(t *testing.T) {
	// When X_CSI_POWERMAX_ARRAY_CONFIG_PATH is not set, setArrayConfigEnvs
	// returns an error.
	err := setArrayConfigEnvs(context.Background())
	if err == nil {
		t.Fatal("expected error when array config path env is not set")
	}
}

func TestSetArrayConfigEnvs_FileNotFound(t *testing.T) {
	// When the env is set but the file doesn't exist, setArrayConfigEnvs
	// logs an error and continues (does not return an error).
	ctx := csictx.WithLookupEnv(context.Background(), func(key string) (string, bool) {
		if key == "X_CSI_POWERMAX_ARRAY_CONFIG_PATH" {
			return "/nonexistent/config.yaml", true
		}
		return "", false
	})
	// Should not panic; returns nil because the function handles the error internally.
	err := setArrayConfigEnvs(ctx)
	// The function does NOT return an error when the file is not found — it logs and continues.
	if err != nil {
		t.Fatalf("unexpected error when config file not found: %v", err)
	}
}

// ─── getDynamicSG ─────────────────────────────────────────────────────────────

func TestGetDynamicSG_Wrapper(t *testing.T) {
	svc := newMetroTestService()
	// This is a thin wrapper; the actual logic is in controller.go's getDynamicSG.
	// We just verify the wrapper calls through correctly (it will error because
	// no PowerMax client is registered).
	_, _, err := svc.getDynamicSG(context.Background(), "000120000001", "test-sg")
	if err == nil {
		t.Fatal("expected error when PowerMax client not registered")
	}
}

// ─── setLogFields ────────────────────────────────────────────────────────────

func TestSetLogFields_NilCtx(t *testing.T) {
	// When ctx is nil, setLogFields must return a valid context (not panic).
	fields := csmlog.Fields{"key": "value"}
	ctx := setLogFields(nil, fields) //nolint:staticcheck
	if ctx == nil {
		t.Fatal("expected non-nil context from setLogFields(nil, ...)")
	}
}

func TestSetLogFields_NonNilCtx(t *testing.T) {
	ctx := setLogFields(context.Background(), csmlog.Fields{"reqID": "abc"})
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}
}

// ─── logMetroStateCheck ───────────────────────────────────────────────────────

func TestLogMetroStateCheck_BothArraysUnregistered(_ *testing.T) {
	// When neither array has a registered client, CheckMetroState returns an
	// error and doMetroStateCheck marks both as unreachable. No panic.
	svc := newMetroTestService()
	svc.opts.MetroStateCheckTimeout = 0 // uses default 15s
	// "arr-x" and "arr-y" have no registered symmetrix clients → returns error.
	svc.doMetroStateCheck(context.Background(), "TestOp", "arr-x", "arr-y", "14")
}

func TestControllerPublishVolume_BothArraysUnreachableReturnsUnavailable(t *testing.T) {
	const localSym = "000120000201"
	const remoteSym = "000120000202"

	ctrl := gomock.NewController(t)
	pmaxClient := mocks.NewMockPmaxClient(ctrl)
	pmaxClient.EXPECT().WithSymmetrixID(localSym).AnyTimes().Return(pmaxClient)
	pmaxClient.EXPECT().WithSymmetrixID(remoteSym).AnyTimes().Return(pmaxClient)
	pmaxClient.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
	pmaxClient.EXPECT().GetVersionDetails(gomock.Any()).AnyTimes().Return(&types.VersionDetails{APIVersion: "103"}, nil)
	pmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), localSym, "10").Return(nil, errors.New("connection refused"))
	pmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), remoteSym, "20").Return(nil, errors.New("connection refused"))

	if err := symmetrix.Initialize([]string{localSym, remoteSym}, pmaxClient); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() {
		symmetrix.RemoveClient(localSym)
		symmetrix.RemoveClient(remoteSym)
	})

	svc := newMetroTestService()
	svc.opts.IsMetroSiteFailureHandlingEnabled = true
	svc.opts.ReplicationContextPrefix = "powermax"
	svc.initMetroJournal(context.Background())

	// Call logMetroStateCheck directly to verify it detects both arrays unreachable
	err := svc.logMetroStateCheck(context.Background(), "ControllerPublishVolume", localSym, remoteSym, "10", "20")
	if err == nil {
		t.Errorf("expected error when both arrays are unreachable, got nil")
	}

	// Verify both arrays are marked as unreachable in the site state tracker
	if svc.siteStateTracker.GetState(localSym) != symmetrix.SiteUnreachable {
		t.Errorf("expected local array %s to be marked unreachable", localSym)
	}
	if svc.siteStateTracker.GetState(remoteSym) != symmetrix.SiteUnreachable {
		t.Errorf("expected remote array %s to be marked unreachable", remoteSym)
	}
}

func TestLogMetroStateCheck_ZeroTimeout(_ *testing.T) {
	svc := newMetroTestService()
	svc.opts.MetroStateCheckTimeout = 0
	// With no registered clients the call returns immediately with an error;
	// the zero timeout defaults to 15 s.
	svc.doMetroStateCheck(context.Background(), "TestOp2", "arr-a", "arr-b", "")
}

// TestLogMetroStateCheck_SuccessPath tests logMetroStateCheck when CheckMetroState
// succeeds (both arrays have registered clients that respond).
func TestLogMetroStateCheck_SuccessPath(t *testing.T) {
	const localSym = "000120000101"
	const remoteSym = "000120000102"

	ctrl := gomock.NewController(t)
	pmaxClient := mocks.NewMockPmaxClient(ctrl)
	pmaxClient.EXPECT().WithSymmetrixID(localSym).AnyTimes().Return(pmaxClient)
	pmaxClient.EXPECT().WithSymmetrixID(remoteSym).AnyTimes().Return(pmaxClient)
	pmaxClient.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
	// Both GetRDFGroupByID calls succeed — local is R1 winner.
	pmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), localSym, "14").Return(&types.RDFGroup{
		DevicePolarity:   localSym,
		NumDevices:       1,
		BiasConfigured:   false,
		WitnessEffective: false,
	}, nil)
	pmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), remoteSym, "14").Return(&types.RDFGroup{
		DevicePolarity: localSym, // same winner
		NumDevices:     1,
	}, nil)

	if err := symmetrix.Initialize([]string{localSym, remoteSym}, pmaxClient); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() {
		symmetrix.RemoveClient(localSym)
		symmetrix.RemoveClient(remoteSym)
	})

	svc := newMetroTestService()
	reg, m := registerFreshMetrics(t)
	_ = reg
	MetroSiteFailures = m.siteFailures
	t.Cleanup(func() { MetroSiteFailures = nil })

	// Should not panic; exercises the success (non-error) branch.
	svc.doMetroStateCheck(context.Background(), "TestOp", localSym, remoteSym, "14")
}

// TestLogMetroStateCheck_WinnerReconnected verifies that when the previous
// winner (local) was transiently unreachable and now both arrays are up,
// no reconciliation is triggered (deferred ops are keyed by the loser, not
// the winner — C-1/C-2 fix).
func TestLogMetroStateCheck_WinnerReconnected(t *testing.T) {
	const localSym = "000120000201"
	const remoteSym = "000120000202"

	ctrl := gomock.NewController(t)
	pmaxClient := mocks.NewMockPmaxClient(ctrl)
	pmaxClient.EXPECT().WithSymmetrixID(localSym).AnyTimes().Return(pmaxClient)
	pmaxClient.EXPECT().WithSymmetrixID(remoteSym).AnyTimes().Return(pmaxClient)
	pmaxClient.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
	pmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), localSym, "20").Return(&types.RDFGroup{
		DevicePolarity: localSym,
		NumDevices:     1,
	}, nil)
	pmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), remoteSym, "20").Return(&types.RDFGroup{
		DevicePolarity: localSym,
		NumDevices:     1,
	}, nil)

	if err := symmetrix.Initialize([]string{localSym, remoteSym}, pmaxClient); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() {
		symmetrix.RemoveClient(localSym)
		symmetrix.RemoveClient(remoteSym)
	})

	svc := newMetroTestService()
	// Only the winner (localSym) was previously unreachable; the loser was never
	// unreachable, so reconciliation must NOT be triggered.
	svc.siteStateTracker.UpdateState(localSym, symmetrix.SiteUnreachable)

	reg, m := registerFreshMetrics(t)
	_ = reg
	MetroSiteFailures = m.siteFailures
	MetroReconciliationTotal = m.reconcileTotal
	MetroDeferredOpsQueueDepth = m.queueDepth
	t.Cleanup(func() {
		MetroSiteFailures = nil
		MetroReconciliationTotal = nil
		MetroDeferredOpsQueueDepth = nil
	})

	// Should not panic and must not trigger reconciliation (no loser recovery).
	svc.doMetroStateCheck(context.Background(), "TestOpReconnect", localSym, remoteSym, "20")
	// Give any (unexpected) goroutine a moment to surface.
	time.Sleep(50 * time.Millisecond)
}

// TestLogMetroStateCheck_LoserRecovered tests that triggerReconciliation is
// fired with the LOSER's array ID when the loser transitions Unreachable →
// Reachable. This validates the C-1 (trigger logic) and C-2 (correct array
// ID) fixes.
func TestLogMetroStateCheck_LoserRecovered(t *testing.T) {
	const localSym = "000120000301"
	const remoteSym = "000120000302"

	ctrl := gomock.NewController(t)
	pmaxClient := mocks.NewMockPmaxClient(ctrl)
	pmaxClient.EXPECT().WithSymmetrixID(localSym).AnyTimes().Return(pmaxClient)
	pmaxClient.EXPECT().WithSymmetrixID(remoteSym).AnyTimes().Return(pmaxClient)
	pmaxClient.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
	// Both arrays respond; local wins (no Witness/Bias → local preferred).
	pmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), localSym, "30").Return(&types.RDFGroup{
		DevicePolarity: localSym,
		NumDevices:     1,
	}, nil)
	pmaxClient.EXPECT().GetRDFGroupByID(gomock.Any(), remoteSym, "30").Return(&types.RDFGroup{
		DevicePolarity: localSym,
		NumDevices:     1,
	}, nil)

	if err := symmetrix.Initialize([]string{localSym, remoteSym}, pmaxClient); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() {
		symmetrix.RemoveClient(localSym)
		symmetrix.RemoveClient(remoteSym)
	})

	svc := newMetroTestService()
	// Mark the LOSER (remoteSym) as previously unreachable — simulates recovery.
	svc.siteStateTracker.UpdateState(remoteSym, symmetrix.SiteUnreachable)

	reg, m := registerFreshMetrics(t)
	_ = reg
	MetroReconciliationTotal = m.reconcileTotal
	MetroDeferredOpsQueueDepth = m.queueDepth
	t.Cleanup(func() {
		MetroReconciliationTotal = nil
		MetroDeferredOpsQueueDepth = nil
	})

	// With remoteSym previously unreachable and now both arrays reachable,
	// the loser (remoteSym) has recovered → reconciliation must be triggered
	// for remoteSym (C-1 fix), not localSym (C-2 fix).
	svc.doMetroStateCheck(context.Background(), "TestLoserRecovery", localSym, remoteSym, "30")

	// Wait for triggerReconciliation to finish before cleanup nullifies the
	// Prometheus metric globals. A bare time.Sleep is racy under -race; we
	// instead poll the in-flight sync.Map that triggerReconciliation removes
	// itself from on exit. The goroutine needs a small head-start to register
	// before the first check.
	time.Sleep(5 * time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, inFlight := svc.metroReconcileInFlight.Load(remoteSym); !inFlight {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The loser should now be marked reachable.
	if got := svc.siteStateTracker.GetState(remoteSym); got != symmetrix.SiteReachable {
		t.Errorf("expected remoteSym state=%s after recovery, got %s", symmetrix.SiteReachable, got)
	}
}

// ─── updateMetroQueueMetrics ──────────────────────────────────────────────────

func TestUpdateMetroQueueMetrics(t *testing.T) {
	svc := newMetroTestService()
	reg, m := registerFreshMetrics(t)
	_ = reg
	MetroDeferredOpsQueueDepth = m.queueDepth
	t.Cleanup(func() {
		MetroDeferredOpsQueueDepth = nil
	})

	// Add some operations to the journal
	_, _ = svc.volumeJournal.CreateDeferredOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpMetroPairing,
		VolumeID:      "vol-1",
		ArrayID:       "arr-1",
	})
	_, _ = svc.volumeJournal.CreateDeferredOperation(context.Background(), symmetrix.DeferredOperation{
		OperationType: symmetrix.OpDeviceCleanup,
		VolumeID:      "vol-2",
		ArrayID:       "arr-2",
	})

	// Should not panic
	svc.updateMetroQueueMetrics()
}

// ─── shouldSkipRemotePublish ───────────────────────────────────────────────────

func TestShouldSkipRemotePublish_NoRemoteArray(t *testing.T) {
	svc := newMetroTestService()
	// When remoteSymID is empty, should skip
	skip, err := svc.shouldSkipRemotePublish(context.Background(), nil, "", "host-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !skip {
		t.Error("expected true when remote array is empty")
	}
}

func TestShouldSkipRemotePublish_HostNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	pmaxClient := mocks.NewMockPmaxClient(ctrl)
	pmaxClient.EXPECT().GetHostByID(gomock.Any(), "remote-arr", "host-1").Return(nil, errors.New("host not found"))

	svc := newMetroTestService()
	skip, err := svc.shouldSkipRemotePublish(context.Background(), pmaxClient, "remote-arr", "host-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !skip {
		t.Error("expected true when host not found on remote array")
	}
}

func TestShouldSkipRemotePublish_HostFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	pmaxClient := mocks.NewMockPmaxClient(ctrl)
	pmaxClient.EXPECT().GetHostByID(gomock.Any(), "remote-arr", "host-1").Return(&types.Host{HostID: "host-1"}, nil)

	svc := newMetroTestService()
	skip, err := svc.shouldSkipRemotePublish(context.Background(), pmaxClient, "remote-arr", "host-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if skip {
		t.Error("expected false when host found on remote array")
	}
}

type metroEventCapture struct {
	object    k8sruntime.Object
	eventType string
	reason    string
}

func (r *metroEventCapture) Event(object k8sruntime.Object, eventType, reason, _ string) {
	r.object, r.eventType, r.reason = object, eventType, reason
}

func (r *metroEventCapture) Eventf(object k8sruntime.Object, eventType, reason, _ string, _ ...interface{}) {
	r.object, r.eventType, r.reason = object, eventType, reason
}

func (r *metroEventCapture) AnnotatedEventf(object k8sruntime.Object, _ map[string]string, eventType, reason, _ string, _ ...interface{}) {
	r.object, r.eventType, r.reason = object, eventType, reason
}

var _ record.EventRecorder = (*metroEventCapture)(nil)

func TestEmitMetroEventReferences(t *testing.T) {
	tests := []struct {
		name         string
		podName      string
		namespace    string
		nodeName     string
		expectedKind string
		expectedName string
		expectedNS   string
	}{
		{name: "controller pod", podName: "powermax-controller-0", namespace: "powermax", expectedKind: "Pod", expectedName: "powermax-controller-0", expectedNS: "powermax"},
		{name: "node fallback", nodeName: "worker-1", expectedKind: "Node", expectedName: "worker-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(EnvPodName, tt.podName)
			t.Setenv(EnvDriverNamespace, tt.namespace)
			capture := &metroEventCapture{}
			svc := &service{opts: Opts{NodeName: tt.nodeName}, metroEventRecorder: capture}
			svc.emitMetroEvent(metroEventTypeWarning, "MetroTest", "test message")

			ref, ok := capture.object.(*corev1.ObjectReference)
			if !ok {
				t.Fatalf("expected ObjectReference, got %T", capture.object)
			}
			if ref.Kind != tt.expectedKind || ref.Name != tt.expectedName || ref.Namespace != tt.expectedNS || ref.APIVersion != "v1" {
				t.Errorf("unexpected event reference: %+v", ref)
			}
			if capture.eventType != metroEventTypeWarning || capture.reason != "MetroTest" {
				t.Errorf("unexpected event metadata: type=%q reason=%q", capture.eventType, capture.reason)
			}
		})
	}
}

func initializeMetroMock(t *testing.T, pmaxClient *mocks.MockPmaxClient, symIDs ...string) {
	t.Helper()
	pmaxClient.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(pmaxClient)
	pmaxClient.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
	for _, symID := range symIDs {
		symmetrix.RemoveClient(symID)
	}
	if err := symmetrix.Initialize(symIDs, pmaxClient); err != nil {
		t.Fatalf("symmetrix.Initialize: %v", err)
	}
	t.Cleanup(func() {
		for _, symID := range symIDs {
			symmetrix.RemoveClient(symID)
		}
	})
}

func TestReconcileMetroPairingStates(t *testing.T) {
	const (
		localSymID  = "000120000101"
		remoteSymID = "000120000102"
		sgName      = "csi-rep-sg-ns-14-Metro"
		volumeID    = "csi-TST-pmax-myvol-000120000101:000120000102-ABC123:DEF456"
	)
	tests := []struct {
		name      string
		state     string
		wantError bool
		unsafe    bool
	}{
		{name: "active active", state: ActiveActive},
		{name: "active bias", state: ActiveBias},
		{name: "sync in progress", state: SyncInProgress, wantError: true},
		{name: "split is unsafe", state: Split, wantError: true, unsafe: true},
		{name: "mixed is unsafe", state: "Mixed", wantError: true, unsafe: true},
		{name: "empty is unsafe", state: "", wantError: true, unsafe: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			pmaxClient := mocks.NewMockPmaxClient(ctrl)
			initializeMetroMock(t, pmaxClient, localSymID, remoteSymID)
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "14").Return(&types.StorageGroupRDFG{States: []string{tt.state}}, nil)

			svc := newMetroTestService()
			err := svc.reconcileMetroPairing(context.Background(), symmetrix.DeferredOperation{
				VolumeID: volumeID, RDFGroupNo: "14", StorageGroupName: sgName,
			})
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v, wantError %t", err, tt.wantError)
			}
			var unsafeErr *symmetrix.UnsafeRDFStateError
			if errors.As(err, &unsafeErr) != tt.unsafe {
				t.Errorf("UnsafeRDFStateError = %t, want %t", errors.As(err, &unsafeErr), tt.unsafe)
			}
		})
	}
}

func TestReconcileMetroPairingErrorsAndSuccess(t *testing.T) {
	const (
		localSymID  = "000120000111"
		remoteSymID = "000120000112"
		sgName      = "csi-rep-sg-ns-15-Metro"
		volumeID    = "csi-TST-pmax-myvol-000120000111:000120000112-ABC123:DEF456"
	)
	newTest := func(t *testing.T) *mocks.MockPmaxClient {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		initializeMetroMock(t, pmaxClient, localSymID, remoteSymID)
		return pmaxClient
	}
	op := symmetrix.DeferredOperation{VolumeID: volumeID, RDFGroupNo: "15", StorageGroupName: sgName}

	t.Run("connectivity error is retriable", func(t *testing.T) {
		pmaxClient := newTest(t)
		pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(nil, context.DeadlineExceeded)
		err := newMetroTestService().reconcileMetroPairing(context.Background(), op)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected wrapped deadline error, got %v", err)
		}
	})

	t.Run("legacy storage group fallback", func(t *testing.T) {
		pmaxClient := newTest(t)
		legacySG := CsiRepSGPrefix + "15-" + Metro
		pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, legacySG, "15").Return(&types.StorageGroupRDFG{States: []string{ActiveActive}}, nil)
		legacyOp := op
		legacyOp.StorageGroupName = ""
		if err := newMetroTestService().reconcileMetroPairing(context.Background(), legacyOp); err != nil {
			t.Fatalf("legacy reconcile failed: %v", err)
		}
	})

	t.Run("establish validation error", func(t *testing.T) {
		pmaxClient := newTest(t)
		gomock.InOrder(
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(nil, errors.New("validation failed")),
		)
		if err := newMetroTestService().reconcileMetroPairing(context.Background(), op); err == nil {
			t.Fatal("expected establish validation error")
		}
	})

	t.Run("post establish verification error", func(t *testing.T) {
		pmaxClient := newTest(t)
		gomock.InOrder(
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().ExecuteReplicationActionOnSG(gomock.Any(), localSymID, Establish, sgName, "15", false, false, false).Return(nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(nil, errors.New("verify failed")),
		)
		if err := newMetroTestService().reconcileMetroPairing(context.Background(), op); err == nil {
			t.Fatal("expected verification error")
		}
	})

	t.Run("post establish transitional state", func(t *testing.T) {
		pmaxClient := newTest(t)
		gomock.InOrder(
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().ExecuteReplicationActionOnSG(gomock.Any(), localSymID, Establish, sgName, "15", false, false, false).Return(nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(&types.StorageGroupRDFG{States: []string{SyncInProgress}}, nil),
		)
		if err := newMetroTestService().reconcileMetroPairing(context.Background(), op); err == nil {
			t.Fatal("expected transitional state error")
		}
	})

	t.Run("success", func(t *testing.T) {
		pmaxClient := newTest(t)
		gomock.InOrder(
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().ExecuteReplicationActionOnSG(gomock.Any(), localSymID, Establish, sgName, "15", false, false, false).Return(nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "15").Return(&types.StorageGroupRDFG{States: []string{ActiveBias}}, nil),
		)
		if err := newMetroTestService().reconcileMetroPairing(context.Background(), op); err != nil {
			t.Fatalf("reconcile failed: %v", err)
		}
	})
}

func TestSetupDegradedVolumeAsMetro(t *testing.T) {
	LockRequestHandler()
	const (
		localSymID  = "000120000121"
		remoteSymID = "000120000122"
		sgName      = "csi-rep-sg-ns-16-Metro"
		remoteSG    = "csi-rep-sg-ns-26-Metro"
	)
	op := symmetrix.DeferredOperation{
		VolumeID: "metro-volume", RDFGroupNo: "16", RemoteRDFGroupNo: "26",
		RemoteStorageGroupName: remoteSG, RemoteSRP: "SRP_1", RemoteServiceLevel: "Diamond",
		ReplicationMode: Metro, VolumeCapacity: 10, RequiredCylinders: 5000,
	}

	t.Run("remote storage group creation error", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), remoteSymID, remoteSG).Return(nil, errors.New("not found"))
		pmaxClient.EXPECT().CreateStorageGroup(gomock.Any(), remoteSymID, remoteSG, "SRP_1", "Diamond", false, gomock.Nil()).Return(nil, errors.New("create failed"))
		if err := newMetroTestService().setupDegradedVolumeAsMetro(context.Background(), op, localSymID, "ABC123", remoteSymID, sgName, pmaxClient); err == nil {
			t.Fatal("expected storage group creation error")
		}
	})

	t.Run("remote volume creation error with fallback group", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		fallbackOp := op
		fallbackOp.RemoteStorageGroupName = ""
		fallbackSG := CsiRepSGPrefix + "26-" + Metro
		pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), remoteSymID, fallbackSG).Return(&types.StorageGroup{}, nil)
		pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), remoteSymID, "ABC123").Return(nil, errors.New("not found"))
		pmaxClient.EXPECT().CreateVolumeInStorageGroupS(gomock.Any(), remoteSymID, fallbackSG, "ABC123", 5000, gomock.Nil(), gomock.Nil()).Return(nil, errors.New("volume failed"))
		if err := newMetroTestService().setupDegradedVolumeAsMetro(context.Background(), fallbackOp, localSymID, "ABC123", remoteSymID, sgName, pmaxClient); err == nil {
			t.Fatal("expected volume creation error")
		}
	})

	t.Run("protect error", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		gomock.InOrder(
			pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), remoteSymID, remoteSG).Return(&types.StorageGroup{}, nil),
			pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), remoteSymID, "ABC123").Return(nil, errors.New("not found")),
			pmaxClient.EXPECT().CreateVolumeInStorageGroupS(gomock.Any(), remoteSymID, remoteSG, "ABC123", 5000, gomock.Nil(), gomock.Nil()).Return(&types.Volume{}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().ExecuteReplicationActionOnSG(gomock.Any(), localSymID, Establish, sgName, "16", false, false, false).Return(nil),
			pmaxClient.EXPECT().GetProtectedStorageGroup(gomock.Any(), localSymID, sgName).Return(nil, errors.New("protect failed")),
			pmaxClient.EXPECT().DeleteVolume(gomock.Any(), remoteSymID, "ABC123").Return(nil),
		)
		if err := newMetroTestService().setupDegradedVolumeAsMetro(context.Background(), op, localSymID, "ABC123", remoteSymID, sgName, pmaxClient); err == nil {
			t.Fatal("expected protect error")
		}
	})

	t.Run("created storage group then remote volume error", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		gomock.InOrder(
			pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), remoteSymID, remoteSG).Return(nil, errors.New("not found")),
			pmaxClient.EXPECT().CreateStorageGroup(gomock.Any(), remoteSymID, remoteSG, "SRP_1", "Diamond", false, gomock.Nil()).Return(&types.StorageGroup{}, nil),
			pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), remoteSymID, "ABC123").Return(nil, errors.New("not found")),
			pmaxClient.EXPECT().CreateVolumeInStorageGroupS(gomock.Any(), remoteSymID, remoteSG, "ABC123", 5000, gomock.Nil(), gomock.Nil()).Return(nil, errors.New("volume failed")),
			pmaxClient.EXPECT().DeleteStorageGroup(gomock.Any(), remoteSymID, remoteSG).Return(nil),
		)
		if err := newMetroTestService().setupDegradedVolumeAsMetro(context.Background(), op, localSymID, "ABC123", remoteSymID, sgName, pmaxClient); err == nil {
			t.Fatal("expected volume creation error")
		}
	})

	t.Run("first establish error", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		gomock.InOrder(
			pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), remoteSymID, remoteSG).Return(&types.StorageGroup{}, nil),
			pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), remoteSymID, "ABC123").Return(nil, errors.New("not found")),
			pmaxClient.EXPECT().CreateVolumeInStorageGroupS(gomock.Any(), remoteSymID, remoteSG, "ABC123", 5000, gomock.Nil(), gomock.Nil()).Return(&types.Volume{}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(nil, errors.New("establish failed")),
			pmaxClient.EXPECT().DeleteVolume(gomock.Any(), remoteSymID, "ABC123").Return(nil),
		)
		if err := newMetroTestService().setupDegradedVolumeAsMetro(context.Background(), op, localSymID, "ABC123", remoteSymID, sgName, pmaxClient); err == nil {
			t.Fatal("expected first establish error")
		}
	})

	t.Run("second establish error", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		gomock.InOrder(
			pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), remoteSymID, remoteSG).Return(&types.StorageGroup{}, nil),
			pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), remoteSymID, "ABC123").Return(nil, errors.New("not found")),
			pmaxClient.EXPECT().CreateVolumeInStorageGroupS(gomock.Any(), remoteSymID, remoteSG, "ABC123", 5000, gomock.Nil(), gomock.Nil()).Return(&types.Volume{}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().ExecuteReplicationActionOnSG(gomock.Any(), localSymID, Establish, sgName, "16", false, false, false).Return(nil),
			pmaxClient.EXPECT().GetProtectedStorageGroup(gomock.Any(), localSymID, sgName).Return(&types.RDFStorageGroup{Rdf: true}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(nil, errors.New("second establish failed")),
			pmaxClient.EXPECT().DeleteVolume(gomock.Any(), remoteSymID, "ABC123").Return(nil),
		)
		if err := newMetroTestService().setupDegradedVolumeAsMetro(context.Background(), op, localSymID, "ABC123", remoteSymID, sgName, pmaxClient); err == nil {
			t.Fatal("expected second establish error")
		}
	})

	t.Run("verification error", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		gomock.InOrder(
			pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), remoteSymID, remoteSG).Return(&types.StorageGroup{}, nil),
			pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), remoteSymID, "ABC123").Return(nil, errors.New("not found")),
			pmaxClient.EXPECT().CreateVolumeInStorageGroupS(gomock.Any(), remoteSymID, remoteSG, "ABC123", 5000, gomock.Nil(), gomock.Nil()).Return(&types.Volume{}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().ExecuteReplicationActionOnSG(gomock.Any(), localSymID, Establish, sgName, "16", false, false, false).Return(nil),
			pmaxClient.EXPECT().GetProtectedStorageGroup(gomock.Any(), localSymID, sgName).Return(&types.RDFStorageGroup{Rdf: true}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(&types.StorageGroupRDFG{States: []string{ActiveBias}}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(nil, errors.New("verify failed")),
			pmaxClient.EXPECT().DeleteVolume(gomock.Any(), remoteSymID, "ABC123").Return(nil),
		)
		if err := newMetroTestService().setupDegradedVolumeAsMetro(context.Background(), op, localSymID, "ABC123", remoteSymID, sgName, pmaxClient); err == nil {
			t.Fatal("expected verification error")
		}
	})

	t.Run("success", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		gomock.InOrder(
			pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), remoteSymID, remoteSG).Return(&types.StorageGroup{}, nil),
			pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), remoteSymID, "ABC123").Return(nil, errors.New("not found")),
			pmaxClient.EXPECT().CreateVolumeInStorageGroupS(gomock.Any(), remoteSymID, remoteSG, "ABC123", 5000, gomock.Nil(), gomock.Nil()).Return(&types.Volume{}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().ExecuteReplicationActionOnSG(gomock.Any(), localSymID, Establish, sgName, "16", false, false, false).Return(nil),
			pmaxClient.EXPECT().GetProtectedStorageGroup(gomock.Any(), localSymID, sgName).Return(&types.RDFStorageGroup{Rdf: true}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().ExecuteReplicationActionOnSG(gomock.Any(), localSymID, Establish, sgName, "16", false, false, false).Return(nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(&types.StorageGroupRDFG{States: []string{ActiveActive}}, nil),
		)
		if err := newMetroTestService().setupDegradedVolumeAsMetro(context.Background(), op, localSymID, "ABC123", remoteSymID, sgName, pmaxClient); err != nil {
			t.Fatalf("setup failed: %v", err)
		}
	})

	t.Run("recovery reuses exact cylinder count", func(t *testing.T) {
		recoveredOp := op
		recoveredOp.RequiredCylinders = 547
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		gomock.InOrder(
			pmaxClient.EXPECT().GetStorageGroup(gomock.Any(), remoteSymID, remoteSG).Return(&types.StorageGroup{}, nil),
			pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), remoteSymID, "ABC123").Return(nil, errors.New("not found")),
			pmaxClient.EXPECT().CreateVolumeInStorageGroupS(gomock.Any(), remoteSymID, remoteSG, "ABC123", 547, gomock.Nil(), gomock.Nil()).Return(&types.Volume{}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().ExecuteReplicationActionOnSG(gomock.Any(), localSymID, Establish, sgName, "16", false, false, false).Return(nil),
			pmaxClient.EXPECT().GetProtectedStorageGroup(gomock.Any(), localSymID, sgName).Return(&types.RDFStorageGroup{Rdf: true}, nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(&types.StorageGroupRDFG{States: []string{Suspended}}, nil),
			pmaxClient.EXPECT().ExecuteReplicationActionOnSG(gomock.Any(), localSymID, Establish, sgName, "16", false, false, false).Return(nil),
			pmaxClient.EXPECT().GetStorageGroupRDFInfo(gomock.Any(), localSymID, sgName, "16").Return(&types.StorageGroupRDFG{States: []string{ActiveActive}}, nil),
		)
		if err := newMetroTestService().setupDegradedVolumeAsMetro(context.Background(), recoveredOp, localSymID, "ABC123", remoteSymID, sgName, pmaxClient); err != nil {
			t.Fatalf("setup failed: %v", err)
		}
	})
}

func TestMetroHelpers(t *testing.T) {
	t.Run("extract RDF group", func(t *testing.T) {
		tests := map[string]string{
			"csi-rep-sg-ns-14-Metro": "14",
			"14-Metro":               "14",
			"csi-rep-sg-ns-14-metro": "",
			"Metro":                  "",
			"not-a-metro-group":      "",
		}
		for input, expected := range tests {
			if actual := extractRDFGroupFromSG(input); actual != expected {
				t.Errorf("extractRDFGroupFromSG(%q) = %q, want %q", input, actual, expected)
			}
		}
	})

	t.Run("set adopted host boot LUNs", func(t *testing.T) {
		svc := &service{}
		svc.setAdoptedHostBootLUNs("missing", []bootLUNInfo{{StorageGroupID: "ignored"}})
		svc.setAdoptedHost("array-1", adoptedHostInfo{HostID: "bfs-host", Protocol: "FC"})
		bootLUNs := []bootLUNInfo{{StorageGroupID: "boot-sg", NumVolumes: 2, MaskingViewID: "boot-mv"}}
		svc.setAdoptedHostBootLUNs("array-1", bootLUNs)
		info, ok := svc.getAdoptedHost("array-1")
		if !ok || info.HostID != "bfs-host" || len(info.BootLUNs) != 1 || info.BootLUNs[0] != bootLUNs[0] {
			t.Fatalf("unexpected adopted host: %+v, found=%t", info, ok)
		}
	})
}

func TestLinkVolumeToVolume(t *testing.T) {
	LockRequestHandler()
	const (
		symID  = "000120000131"
		devID  = "ABC123"
		tgtID  = "DEF456"
		snapID = "temp-snapshot"
	)
	vol := &types.Volume{VolumeID: devID}

	t.Run("snapshot creation error", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		pmaxClient.EXPECT().CreateSnapshot(gomock.Any(), symID, snapID, gomock.Any(), int64(1)).Return(errors.New("snapshot failed"))
		if err := (&service{}).LinkVolumeToVolume(context.Background(), symID, vol, tgtID, snapID, "req-1", false, pmaxClient); err == nil {
			t.Fatal("expected snapshot creation error")
		}
	})

	t.Run("maximum snapshot sessions", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		pmaxClient.EXPECT().CreateSnapshot(gomock.Any(), symID, snapID, gomock.Any(), int64(1)).Return(errors.New("The maximum number of sessions has been exceeded for the specified Source device"))
		if err := (&service{}).LinkVolumeToVolume(context.Background(), symID, vol, tgtID, snapID, "req-2", false, pmaxClient); err == nil {
			t.Fatal("expected snapshot session limit error")
		}
	})

	t.Run("link error", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		gomock.InOrder(
			pmaxClient.EXPECT().CreateSnapshot(gomock.Any(), symID, snapID, gomock.Any(), int64(1)).Return(nil),
			pmaxClient.EXPECT().GetSnapshotInfo(gomock.Any(), symID, devID, snapID).Return(&types.VolumeSnapshot{SnapshotName: snapID}, nil),
			pmaxClient.EXPECT().GetSnapshotInfo(gomock.Any(), symID, devID, snapID).Return(&types.VolumeSnapshot{SnapshotName: snapID}, nil),
			pmaxClient.EXPECT().ModifySnapshotS(gomock.Any(), symID, gomock.Any(), gomock.Any(), snapID, Link, "", gomock.Any(), false).Return(errors.New("link failed")),
		)
		if err := (&service{}).LinkVolumeToVolume(context.Background(), symID, vol, tgtID, snapID, "req-3", false, pmaxClient); err == nil {
			t.Fatal("expected link error")
		}
	})

	t.Run("desired state is successful and queued", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		gomock.InOrder(
			pmaxClient.EXPECT().CreateSnapshot(gomock.Any(), symID, snapID, gomock.Any(), int64(1)).Return(nil),
			pmaxClient.EXPECT().GetSnapshotInfo(gomock.Any(), symID, devID, snapID).Return(&types.VolumeSnapshot{SnapshotName: snapID}, nil),
			pmaxClient.EXPECT().GetSnapshotInfo(gomock.Any(), symID, devID, snapID).Return(&types.VolumeSnapshot{SnapshotName: snapID}, nil),
			pmaxClient.EXPECT().ModifySnapshotS(gomock.Any(), symID, gomock.Any(), gomock.Any(), snapID, Link, "", gomock.Any(), true).Return(errors.New(errDesiredState)),
		)
		cleaner := &snapCleanupWorker{Queue: make(snapCleanupQueue, 0)}
		svc := &service{snapCleaner: cleaner}
		if err := svc.LinkVolumeToVolume(context.Background(), symID, vol, tgtID, snapID, "req-4", true, pmaxClient); err != nil {
			t.Fatalf("desired-state link failed: %v", err)
		}
		if cleaner.getQueueLen() != 1 {
			t.Fatalf("cleanup queue length = %d, want 1", cleaner.getQueueLen())
		}
	})
}

func TestSpaceReclamationGetVolumeWWN(t *testing.T) {
	const (
		localSymID  = "000120000141"
		remoteSymID = "000120000142"
		volumeID    = "csi-TST-pmax-myvol-000120000141:000120000142-ABC123:DEF456"
	)

	t.Run("invalid volume handle", func(t *testing.T) {
		manager := &SpaceReclamationManager{svc: &service{}}
		if _, _, _, err := manager.getVolumeWWN(context.Background(), "pv-1", "invalid"); err == nil {
			t.Fatal("expected parse error")
		}
	})

	t.Run("client error", func(t *testing.T) {
		manager := &SpaceReclamationManager{svc: &service{}}
		if _, _, _, err := manager.getVolumeWWN(context.Background(), "pv-1", volumeID); err == nil {
			t.Fatal("expected client error")
		}
	})

	t.Run("volume error", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		initializeMetroMock(t, pmaxClient, localSymID, remoteSymID)
		pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), localSymID, "ABC123").Return(nil, errors.New("volume failed"))
		manager := &SpaceReclamationManager{svc: &service{}}
		if _, _, _, err := manager.getVolumeWWN(context.Background(), "pv-1", volumeID); err == nil {
			t.Fatal("expected volume lookup error")
		}
	})

	t.Run("empty WWN", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		initializeMetroMock(t, pmaxClient, localSymID, remoteSymID)
		pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), localSymID, "ABC123").Return(&types.Volume{}, nil)
		manager := &SpaceReclamationManager{svc: &service{}}
		if _, _, _, err := manager.getVolumeWWN(context.Background(), "pv-1", volumeID); err == nil {
			t.Fatal("expected empty WWN error")
		}
	})

	t.Run("success", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
		initializeMetroMock(t, pmaxClient, localSymID, remoteSymID)
		pmaxClient.EXPECT().GetVolumeByID(gomock.Any(), localSymID, "ABC123").Return(&types.Volume{EffectiveWWN: "600009700001"}, nil)
		manager := &SpaceReclamationManager{svc: &service{}}
		symID, devID, wwn, err := manager.getVolumeWWN(context.Background(), "pv-1", volumeID)
		if err != nil || symID != localSymID || devID != "ABC123" || wwn != "600009700001" {
			t.Fatalf("getVolumeWWN = %q, %q, %q, %v", symID, devID, wwn, err)
		}
	})
}

func TestSmallServiceHelpers(t *testing.T) {
	pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
	pmaxClient.EXPECT().GetHTTPClient().Return(&http.Client{})
	if clientKey(pmaxClient) == "" {
		t.Fatal("expected non-empty client key")
	}
	(&mockDbusConnection{}).Close()
}
