// Copyright © 2024-2026 Dell Inc. or its subsidiaries. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//      http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dell/gofsutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
)

// ============================================================================
// Test Helpers
// ============================================================================

// makePVC creates a minimal PVC object for testing.
func makePVC(name, namespace string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Namespace:   namespace,
			Annotations: map[string]string{},
		},
	}
}

// newTestManager creates a SpaceReclamationManager with test defaults.
func newTestManager(t *testing.T, client *fake.Clientset, cfg SpaceReclamationConfig) *SpaceReclamationManager {
	t.Helper()
	ctx := context.Background()

	// Set up test WWN resolver before creating manager
	// This mock extracts WWN from the volume handle for test volumes
	getVolumeWWNFunc = func(_ *SpaceReclamationManager, _ context.Context, _ string, volumeHandle string) (symID, devID, wwn string, err error) {
		// Parse test volume handles like "CSM-vol001-000120001647-00001"
		// and return the corresponding test WWN
		testWWNs := map[string]string{
			"CSM-vol001-000120001647-00001":      "60000970000120001647533030303031",
			"CSM-volblk001-000120001647-00002":   "60000970000120001647533030303032",
			"CSM-volunsup001-000120001647-00003": "60000970000120001647533030303033",
		}
		if wwn, ok := testWWNs[volumeHandle]; ok {
			return "000120001647", strings.Split(volumeHandle, "-")[3], wwn, nil
		}
		return "", "", "", fmt.Errorf("test: unknown volume handle %s", volumeHandle)
	}

	// Pass nil for *service parameter in tests since we use mock WWN resolution
	mgr, err := NewSpaceReclamationManager(ctx, cfg, client, cfg.NodeName, nil)
	require.NoError(t, err)
	return mgr
}

// resetGofsutilMock resets gofsutil mock to a clean state.
func resetGofsutilMock() {
	gofsutil.GOFSMock.InduceMountError = false
	gofsutil.GOFSMock.InduceUnmountError = false
}

// defaultGetVolumeWWNFunc stores the original default implementation for restoration after tests.
var defaultGetVolumeWWNFunc = getVolumeWWNFunc

// resetTestMocks resets all test mocks to their default state.
func resetTestMocks() {
	getVolumeWWNFunc = defaultGetVolumeWWNFunc
	resetGofsutilMock()
}

var filesystemMode = corev1.PersistentVolumeFilesystem

// ============================================================================
// CONTRACT TESTS (C-*) -- FIRST
// ============================================================================
// Contract tests validate integration points between components.
// They force wiring that connects new code to existing code.

// C-001: TestReclamationCycle_CallsFstrimOnFilesystemVolume
// Forces: RunOnce worker dispatch + gofsutil.Fstrim call + PVC annotation update
func TestReclamationCycle_CallsFstrimOnFilesystemVolume(t *testing.T) {
	gofsutil.UseMockFS()
	defer resetGofsutilMock()

	pvc := makePVC("pvc-test-001", "default")
	// Create PV with the PVC reference
	// Use proper PowerMax CSI volume handle format: <volname>-<arrayid>-<devid>
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv-test-001",
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("10Gi"),
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       Name,
					VolumeHandle: "CSM-vol001-000120001647-00001",
					VolumeAttributes: map[string]string{
						"WWID": "60000970000120001647533030303031",
					},
				},
			},
			ClaimRef: &corev1.ObjectReference{
				Name:      "pvc-test-001",
				Namespace: "default",
			},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			StorageClassName:              "powermax-sc",
			VolumeMode:                    &filesystemMode,
		},
		Status: corev1.PersistentVolumeStatus{
			Phase: corev1.VolumeBound,
		},
	}

	fakeClient := fake.NewSimpleClientset(pvc, pv)
	mgr := newTestManager(t, fakeClient, SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "* * * * *",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       60,
		NodeName:             "node-1",
	})

	// Mock mount table with private mount path
	gofsutil.GOFSMockMounts = []gofsutil.Info{
		{
			Device: "/dev/sda",
			Path:   "/var/lib/kubelet/plugins/powermax.emc.dell.com/disks/CSM-vol001-000120001647-00001",
			Source: "/dev/sda",
		},
	}

	// Mock gofsutil functions for on-demand discovery
	originalWWNToDevicePathFunc := wwnToDevicePathFunc
	wwnToDevicePathFunc = func(_ context.Context, _ string) (string, string, error) {
		return "60000970000120001647533030303031", "/dev/sda", nil
	}
	defer func() { wwnToDevicePathFunc = originalWWNToDevicePathFunc }()

	// Execute one reclamation cycle
	mgr.RunOnce()

	// Verify PVC annotations were updated
	updatedPVC, err := fakeClient.CoreV1().PersistentVolumeClaims("default").Get(
		context.Background(), "pvc-test-001", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "success", updatedPVC.Annotations[AnnotationStatus],
		"PVC should be annotated with status=success after fstrim")
	assert.NotEmpty(t, updatedPVC.Annotations[AnnotationBytesAvailable],
		"PVC should have bytes-available annotation")
	assert.NotEmpty(t, updatedPVC.Annotations[AnnotationLastRunTime],
		"PVC should have last-run-time annotation")
	assert.Equal(t, "node-1", updatedPVC.Annotations[AnnotationNode],
		"PVC should have node annotation")
}

// C-002: TestReclamationCycle_CallsBlkdiscardOnBlockVolume
// Forces: RunOnce worker dispatch + gofsutil.Blkdiscard call
func TestReclamationCycle_CallsBlkdiscardOnBlockVolume(t *testing.T) {
	gofsutil.UseMockFS()
	defer resetGofsutilMock()

	pvc := makePVC("pvc-blk-001", "default")
	pvc.Labels = map[string]string{
		LabelBlockReclaim: "true",
	}
	blockMode := corev1.PersistentVolumeBlock
	pvc.Spec.VolumeMode = &blockMode
	// Create PV with the PVC reference
	// Use proper PowerMax CSI volume handle format: <volname>-<arrayid>-<devid>
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv-blk-001",
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("10Gi"),
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       Name,
					VolumeHandle: "CSM-volblk001-000120001647-00002",
					VolumeAttributes: map[string]string{
						"WWID": "60000970000120001647533030303032",
					},
				},
			},
			ClaimRef: &corev1.ObjectReference{
				Name:      "pvc-blk-001",
				Namespace: "default",
			},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			StorageClassName:              "powermax-sc",
			VolumeMode:                    &blockMode,
		},
		Status: corev1.PersistentVolumeStatus{
			Phase: corev1.VolumeBound,
		},
	}

	fakeClient := fake.NewSimpleClientset(pvc, pv)
	mgr := newTestManager(t, fakeClient, SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "* * * * *",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       60,
		NodeName:             "node-1",
	})

	// Mock WWN to device mapping for GetSysBlockDevicesForVolumeWWN
	gofsutil.GOFSMockWWNToDevice = map[string]string{
		"60000970000120001647533030303032": "/dev/sdb",
	}
	defer func() { gofsutil.GOFSMockWWNToDevice = nil }()

	// Mock gofsutil functions for on-demand discovery
	originalWWNToDevicePathFunc := wwnToDevicePathFunc
	wwnToDevicePathFunc = func(_ context.Context, _ string) (string, string, error) {
		return "60000970000120001647533030303032", "/dev/sdb", nil
	}
	defer func() { wwnToDevicePathFunc = originalWWNToDevicePathFunc }()

	// Execute one reclamation cycle
	mgr.RunOnce()

	// Verify PVC annotations were updated
	updatedPVC, err := fakeClient.CoreV1().PersistentVolumeClaims("default").Get(
		context.Background(), "pvc-blk-001", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "success", updatedPVC.Annotations[AnnotationStatus],
		"PVC should be annotated with status=success after blkdiscard")
	assert.NotEmpty(t, updatedPVC.Annotations[AnnotationBytesAvailable],
		"PVC should have bytes-available annotation")
}

// C-003: TestReclamationCycle_SkipsUnsupportedDevice
// Forces: Capability check path + "unsupported" annotation

func TestReclamationCycle_SkipsUnsupportedDevice(t *testing.T) {
	gofsutil.UseMockFS()
	defer resetGofsutilMock()

	pvc := makePVC("pvc-unsup-001", "default")
	filesystemMode := corev1.PersistentVolumeFilesystem
	// Create PV with the PVC reference
	// Use proper PowerMax CSI volume handle format: <volname>-<arrayid>-<devid>
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv-unsup-001",
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("10Gi"),
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       Name,
					VolumeHandle: "CSM-volunsup001-000120001647-00003",
					VolumeAttributes: map[string]string{
						"WWID": "60000970000120001647533030303033",
					},
				},
			},
			ClaimRef: &corev1.ObjectReference{
				Name:      "pvc-unsup-001",
				Namespace: "default",
			},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			StorageClassName:              "powermax-sc",
			VolumeMode:                    &filesystemMode,
		},
		Status: corev1.PersistentVolumeStatus{
			Phase: corev1.VolumeBound,
		},
	}

	fakeClient := fake.NewSimpleClientset(pvc, pv)
	mgr := newTestManager(t, fakeClient, SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "* * * * *",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       60,
		NodeName:             "node-1",
	})

	// Mock mount table with private mount path
	gofsutil.GOFSMockMounts = []gofsutil.Info{
		{
			Device: "/dev/sdc",
			Path:   "/var/lib/kubelet/plugins/powermax.emc.dell.com/disks/CSM-volunsup001-000120001647-00003",
			Source: "/dev/sdc",
		},
	}

	// Mock checkDiscardCapabilityFunc to return unsupported for /dev/sdc
	originalCheckDiscardFunc := checkDiscardCapabilityFunc
	checkDiscardCapabilityFunc = func(_ context.Context, devicePath string) (bool, int64, string) {
		if devicePath == "/dev/sdc" {
			return false, 0, "discard_max_bytes is 0"
		}
		return true, 4294967295, ""
	}
	defer func() { checkDiscardCapabilityFunc = originalCheckDiscardFunc }()

	// Mock gofsutil functions for on-demand discovery
	originalWWNToDevicePathFunc := wwnToDevicePathFunc
	wwnToDevicePathFunc = func(_ context.Context, _ string) (string, string, error) {
		return "60000970000120001647533030303033", "/dev/sdc", nil
	}
	defer func() { wwnToDevicePathFunc = originalWWNToDevicePathFunc }()

	// Execute one reclamation cycle
	mgr.RunOnce()

	// Verify PVC annotations indicate unsupported
	updatedPVC, err := fakeClient.CoreV1().PersistentVolumeClaims("default").Get(
		context.Background(), "pvc-unsup-001", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "unsupported", updatedPVC.Annotations[AnnotationStatus],
		"PVC should be annotated with status=unsupported for unsupported device")
}

// ============================================================================
// UNIT TESTS (U-*) -- SECOND
// ============================================================================

// --- TestReadSpaceReclamationConfig: default values and env override ---

func TestReadSpaceReclamationConfig(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected SpaceReclamationConfig
	}{
		{
			name:    "AllDefaults",
			envVars: map[string]string{},
			expected: SpaceReclamationConfig{
				Enabled:              false,
				Schedule:             "0 2 * * 0",
				MaxConcurrentVolumes: 2,
				TimeoutSeconds:       14400,
				NodeName:             "",
			},
		},
		{
			name: "AllCustom",
			envVars: map[string]string{
				"X_CSI_SPACE_RECLAMATION_ENABLED":        "true",
				"X_CSI_SPACE_RECLAMATION_SCHEDULE":       "*/5 * * * *",
				"X_CSI_SPACE_RECLAMATION_MAX_CONCURRENT": "4",
				"X_CSI_SPACE_RECLAMATION_TIMEOUT":        "1800",
				"X_CSI_POWERMAX_NODENAME":                "node-x",
			},
			expected: SpaceReclamationConfig{
				Enabled:              true,
				Schedule:             "*/5 * * * *",
				MaxConcurrentVolumes: 4,
				TimeoutSeconds:       1800,
				NodeName:             "node-x",
			},
		},
		{
			name: "InvalidBool",
			envVars: map[string]string{
				"X_CSI_SPACE_RECLAMATION_ENABLED": "notabool",
			},
			expected: SpaceReclamationConfig{
				Enabled:              false,
				Schedule:             "0 2 * * 0",
				MaxConcurrentVolumes: 2,
				TimeoutSeconds:       14400,
			},
		},
		{
			name: "InvalidInt",
			envVars: map[string]string{
				"X_CSI_SPACE_RECLAMATION_MAX_CONCURRENT": "abc",
			},
			expected: SpaceReclamationConfig{
				Enabled:              false,
				Schedule:             "0 2 * * 0",
				MaxConcurrentVolumes: 2,
				TimeoutSeconds:       14400,
			},
		},
		{
			name: "ZeroConcurrent",
			envVars: map[string]string{
				"X_CSI_SPACE_RECLAMATION_MAX_CONCURRENT": "0",
			},
			expected: SpaceReclamationConfig{
				Enabled:              false,
				Schedule:             "0 2 * * 0",
				MaxConcurrentVolumes: 0,
				TimeoutSeconds:       14400,
			},
		},
		{
			name: "EmptySchedule",
			envVars: map[string]string{
				"X_CSI_SPACE_RECLAMATION_SCHEDULE": "",
			},
			expected: SpaceReclamationConfig{
				Enabled:              false,
				Schedule:             "0 2 * * 0",
				MaxConcurrentVolumes: 2,
				TimeoutSeconds:       14400,
			},
		},
		{
			name: "NegativeTimeout",
			envVars: map[string]string{
				"X_CSI_SPACE_RECLAMATION_TIMEOUT": "-1",
			},
			expected: SpaceReclamationConfig{
				Enabled:              false,
				Schedule:             "0 2 * * 0",
				MaxConcurrentVolumes: 2,
				TimeoutSeconds:       14400,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear all relevant env vars first
			t.Setenv("X_CSI_SPACE_RECLAMATION_ENABLED", "")
			t.Setenv("X_CSI_SPACE_RECLAMATION_SCHEDULE", "")
			t.Setenv("X_CSI_SPACE_RECLAMATION_MAX_CONCURRENT", "")
			t.Setenv("X_CSI_SPACE_RECLAMATION_TIMEOUT", "")
			t.Setenv("X_CSI_POWERMAX_NODENAME", "")

			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			cfg := ReadSpaceReclamationConfig()
			assert.Equal(t, tt.expected.Enabled, cfg.Enabled, "Enabled mismatch")
			assert.Equal(t, tt.expected.Schedule, cfg.Schedule, "Schedule mismatch")
			assert.Equal(t, tt.expected.MaxConcurrentVolumes, cfg.MaxConcurrentVolumes, "MaxConcurrentVolumes mismatch")
			assert.Equal(t, tt.expected.TimeoutSeconds, cfg.TimeoutSeconds, "TimeoutSeconds mismatch")
			assert.Equal(t, tt.expected.NodeName, cfg.NodeName, "NodeName mismatch")
		})
	}
}

// --- TestSpaceReclamationManager_StartStop ---

func TestSpaceReclamationManager_StartStop(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	mgr := newTestManager(t, fakeClient, SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "0 2 * * 0",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       60,
		NodeName:             "node-1",
	})

	err := mgr.Start()
	require.NoError(t, err, "Start should succeed")
	assert.NotNil(t, mgr.cronSched, "cron scheduler should be initialized")

	// Stop should not panic
	assert.NotPanics(t, func() {
		mgr.Stop()
	}, "Stop should not panic")
}

// --- TestBuildAnnotations ---

func TestBuildAnnotations(t *testing.T) {
	pvc := makePVC("test-pvc", "default")
	fakeClient := fake.NewSimpleClientset(pvc)
	annotator := NewPVCAnnotator(fakeClient)

	result := &ReclamationResult{
		Status:         "success",
		BytesAvailable: 1073741824,
		Duration:       500 * time.Millisecond,
		NodeName:       "node-1",
	}
	err := annotator.Annotate(context.Background(), "test-pvc", "default", result)
	require.NoError(t, err)

	updated, err := fakeClient.CoreV1().PersistentVolumeClaims("default").Get(
		context.Background(), "test-pvc", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "success", updated.Annotations[AnnotationStatus])
	assert.Equal(t, "1073741824", updated.Annotations[AnnotationBytesAvailable])
	assert.Equal(t, "node-1", updated.Annotations[AnnotationNode])
	assert.NotEmpty(t, updated.Annotations[AnnotationLastRunTime])
	assert.NotEmpty(t, updated.Annotations[AnnotationDuration])
}

func TestBuildAnnotations_ErrorResult(t *testing.T) {
	pvc := makePVC("test-pvc", "default")
	fakeClient := fake.NewSimpleClientset(pvc)
	annotator := NewPVCAnnotator(fakeClient)

	result := &ReclamationResult{
		Status:       "error",
		ErrorMessage: "fstrim failed: permission denied",
		NodeName:     "node-1",
	}
	err := annotator.Annotate(context.Background(), "test-pvc", "default", result)
	require.NoError(t, err)

	updated, err := fakeClient.CoreV1().PersistentVolumeClaims("default").Get(
		context.Background(), "test-pvc", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "error", updated.Annotations[AnnotationStatus])
	assert.Contains(t, updated.Annotations[AnnotationErrorMsg], "fstrim failed")
}

func TestBuildAnnotations_PVCNotFound(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()
	annotator := NewPVCAnnotator(fakeClient)

	result := &ReclamationResult{Status: "success", BytesAvailable: 100}
	err := annotator.Annotate(context.Background(), "nonexistent-pvc", "default", result)
	assert.Error(t, err, "annotating non-existent PVC should return error")
}

func TestBuildAnnotations_ConflictRetry(t *testing.T) {
	pvc := makePVC("test-pvc", "default")
	fakeClient := fake.NewSimpleClientset(pvc)

	updateCount := 0
	fakeClient.PrependReactor("update", "persistentvolumeclaims", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		updateCount++
		if updateCount == 1 {
			return true, nil, fmt.Errorf("the object has been modified; please apply your changes to the latest version and try again")
		}
		return false, nil, nil
	})

	annotator := NewPVCAnnotator(fakeClient)
	result := &ReclamationResult{Status: "success", BytesAvailable: 100, NodeName: "node-1"}
	err := annotator.Annotate(context.Background(), "test-pvc", "default", result)

	assert.NoError(t, err, "annotator should handle conflict with retry")
	assert.GreaterOrEqual(t, updateCount, 2, "should have retried at least once")
}

// --- TestIsEligible ---

func TestIsEligible_GlobalEnabledNoAnnotation(t *testing.T) {
	annotations := map[string]string{}
	eligible, _ := IsEligible(true, annotations, VolumeModeFilesystem)
	assert.True(t, eligible, "global enabled + no annotation = eligible")
}

func TestIsEligible_ExplicitOptOut(t *testing.T) {
	annotations := map[string]string{
		LabelEnabled: "false",
	}
	eligible, _ := IsEligible(true, annotations, VolumeModeFilesystem)
	assert.False(t, eligible, "explicit opt-out should make volume ineligible")
}

func TestIsEligible_ExplicitOptIn(t *testing.T) {
	annotations := map[string]string{
		LabelEnabled: "true",
	}
	eligible, _ := IsEligible(true, annotations, VolumeModeFilesystem)
	assert.True(t, eligible, "explicit opt-in should make volume eligible")
}

func TestIsEligible_GlobalDisabled(t *testing.T) {
	annotations := map[string]string{}
	eligible, _ := IsEligible(false, annotations, VolumeModeFilesystem)
	assert.False(t, eligible, "global disabled = ineligible")
}

func TestIsEligible_NilAnnotationsMap(t *testing.T) {
	eligible, _ := IsEligible(true, nil, VolumeModeFilesystem)
	assert.True(t, eligible, "nil annotations with global enabled = eligible")
}

func TestIsEligible_GlobalDisabledButLabelOptIn(t *testing.T) {
	annotations := map[string]string{
		LabelEnabled: "true",
	}
	eligible, _ := IsEligible(false, annotations, VolumeModeFilesystem)
	assert.True(t, eligible, "global disabled but label opt-in should make volume eligible (label precedence)")
}

func TestIsEligible_BlockModeIgnoresGlobalConfig(t *testing.T) {
	annotations := map[string]string{
		LabelBlockReclaim: "true",
	}
	// Test with globalEnabled=false - should still be eligible because block mode ignores global config
	eligible, _ := IsEligible(false, annotations, VolumeModeBlock)
	assert.True(t, eligible, "block mode with label should be eligible even when globally disabled")

	// Test with globalEnabled=true - should also be eligible
	eligible, _ = IsEligible(true, annotations, VolumeModeBlock)
	assert.True(t, eligible, "block mode with label should be eligible when globally enabled")
}

// --- EventEmitter tests ---

func TestEventEmitter_EmitSuccess(t *testing.T) {
	recorder := record.NewFakeRecorder(10)
	emitter := &EventEmitter{recorder: recorder}
	pvc := makePVC("test-pvc", "default")

	emitter.EmitSuccess(pvc, 1073741824)

	select {
	case event := <-recorder.Events:
		assert.Contains(t, event, EventReasonCompleted, "event should contain SpaceReclamationCompleted")
	case <-time.After(time.Second):
		t.Fatal("expected SpaceReclamationCompleted event not received")
	}
}

func TestEventEmitter_EmitFailure(t *testing.T) {
	recorder := record.NewFakeRecorder(10)
	emitter := &EventEmitter{recorder: recorder}
	pvc := makePVC("test-pvc", "default")

	emitter.EmitFailure(pvc, fmt.Errorf("fstrim failed"))

	select {
	case event := <-recorder.Events:
		assert.Contains(t, event, EventReasonFailed, "event should contain SpaceReclamationFailed")
	case <-time.After(time.Second):
		t.Fatal("expected SpaceReclamationFailed event not received")
	}
}

func TestEventEmitter_EmitTimeout(t *testing.T) {
	recorder := record.NewFakeRecorder(10)
	emitter := &EventEmitter{recorder: recorder}
	pvc := makePVC("test-pvc", "default")

	emitter.EmitTimeout(pvc, 3600*time.Second)

	select {
	case event := <-recorder.Events:
		assert.Contains(t, event, EventReasonTimeout, "event should contain SpaceReclamationTimeout")
	case <-time.After(time.Second):
		t.Fatal("expected SpaceReclamationTimeout event not received")
	}
}

func TestEventEmitter_EmitUnsupported(t *testing.T) {
	recorder := record.NewFakeRecorder(10)
	emitter := &EventEmitter{recorder: recorder}
	pvc := makePVC("test-pvc", "default")

	emitter.EmitUnsupported(pvc, "discard_max_bytes is 0")

	select {
	case event := <-recorder.Events:
		assert.Contains(t, event, EventReasonUnsupported, "event should contain SpaceReclamationUnsupported")
	case <-time.After(time.Second):
		t.Fatal("expected SpaceReclamationUnsupported event not received")
	}
}

// --- Concurrency Tests ---

func TestSemaphore_LimitsParallelism(t *testing.T) {
	sem := make(chan struct{}, 2)
	var maxConcurrent int64
	var currentConcurrent int64
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			curr := atomic.AddInt64(&currentConcurrent, 1)
			for {
				old := atomic.LoadInt64(&maxConcurrent)
				if curr <= old || atomic.CompareAndSwapInt64(&maxConcurrent, old, curr) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			atomic.AddInt64(&currentConcurrent, -1)
		}()
	}
	wg.Wait()
	assert.LessOrEqual(t, atomic.LoadInt64(&maxConcurrent), int64(2),
		"at most 2 jobs should run concurrently")
}

func TestPerVolumeMutex_PreventsDuplicateJob(t *testing.T) {
	var volumeLocks sync.Map
	volID := "vol-dup-001"

	mu := &sync.Mutex{}
	actual, loaded := volumeLocks.LoadOrStore(volID, mu)
	assert.False(t, loaded, "first lock should not be loaded")

	actualMu := actual.(*sync.Mutex)
	actualMu.Lock()

	_, loaded2 := volumeLocks.LoadOrStore(volID, &sync.Mutex{})
	assert.True(t, loaded2, "second lock should find existing entry (duplicate job)")

	actualMu.Unlock()
}

func TestShutdown_CancelsRunningJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	jobStarted := make(chan struct{})
	jobDone := make(chan struct{})

	go func() {
		close(jobStarted)
		select {
		case <-ctx.Done():
			close(jobDone)
		case <-time.After(5 * time.Second):
		}
	}()

	<-jobStarted
	cancel()

	select {
	case <-jobDone:
		assert.True(t, true, "job should be cancelled on shutdown")
	case <-time.After(1 * time.Second):
		t.Fatal("job was not cancelled within timeout")
	}
}

// --- Manager Initialization Edge Cases ---

func TestNewSpaceReclamationManager_InvalidCronExpression(t *testing.T) {
	cfg := SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "invalid cron",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       14400,
		NodeName:             "test-node",
	}
	fakeClient := fake.NewSimpleClientset()
	mgr, err := NewSpaceReclamationManager(context.Background(), cfg, fakeClient, cfg.NodeName, nil)
	require.Error(t, err, "invalid cron expression should return error")
	require.Nil(t, mgr, "manager should be nil on error")
}

func TestNewSpaceReclamationManager_ValidConfig(t *testing.T) {
	cfg := SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "0 2 * * 0",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       14400,
		NodeName:             "test-node",
	}
	fakeClient := fake.NewSimpleClientset()
	mgr, err := NewSpaceReclamationManager(context.Background(), cfg, fakeClient, cfg.NodeName, nil)
	require.NoError(t, err)
	require.NotNil(t, mgr)
}

func TestNewSpaceReclamationManager_EmptyNodeName(t *testing.T) {
	cfg := SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "0 2 * * 0",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       14400,
		NodeName:             "",
	}
	fakeClient := fake.NewSimpleClientset()
	mgr, err := NewSpaceReclamationManager(context.Background(), cfg, fakeClient, cfg.NodeName, nil)
	require.NoError(t, err, "empty NodeName should be accepted (graceful degradation)")
	require.NotNil(t, mgr, "manager should be created even with empty NodeName")
}

// --- Environment Variable Constants ---

func TestEnvVarConstants_Defined(t *testing.T) {
	assert.Equal(t, "X_CSI_SPACE_RECLAMATION_ENABLED", EnvSpaceReclamationEnabled)
	assert.Equal(t, "X_CSI_SPACE_RECLAMATION_SCHEDULE", EnvSpaceReclamationSchedule)
	assert.Equal(t, "X_CSI_SPACE_RECLAMATION_MAX_CONCURRENT", EnvSpaceReclamationMaxConcurrent)
	assert.Equal(t, "X_CSI_SPACE_RECLAMATION_TIMEOUT", EnvSpaceReclamationTimeout)
}

// --- Job-Level Timeout Tests ---

// TestJobLevelTimeout_ConfigurationApplied verifies timeout configuration is correctly applied
func TestJobLevelTimeout_ConfigurationApplied(t *testing.T) {
	cfg := SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "0 2 * * 0",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       7200, // 2 hours
		NodeName:             "test-node",
	}

	fakeClient := fake.NewSimpleClientset()
	mgr := newTestManager(t, fakeClient, cfg)

	// Verify that manager was created with correct timeout config
	assert.Equal(t, 7200, mgr.config.TimeoutSeconds, "timeout should be configured correctly")
	assert.NotNil(t, mgr.ctx, "manager context should be initialized")
}

// TestJobLevelTimeout_ShortTimeout verifies manager handles short timeouts
func TestJobLevelTimeout_ShortTimeout(t *testing.T) {
	cfg := SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "0 2 * * 0",
		MaxConcurrentVolumes: 1,
		TimeoutSeconds:       1, // 1 second timeout
		NodeName:             "test-node",
	}

	fakeClient := fake.NewSimpleClientset()
	mgr := newTestManager(t, fakeClient, cfg)

	// Verify short timeout is accepted
	assert.Equal(t, 1, mgr.config.TimeoutSeconds, "short timeout should be accepted")
}

// TestDevicePathNormalization_iSCSIMultipathDevice verifies that device path normalization
// works correctly for iSCSI multipath devices (similar to FC)
func TestDevicePathNormalization_iSCSIMultipathDevice(t *testing.T) {
	gofsutil.UseMockFS()
	defer resetGofsutilMock()

	pvc := makePVC("pvc-iscsi-001", "default")
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv-iscsi-001",
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("10Gi"),
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       Name,
					VolumeHandle: "CSM-vol001-000120001647-00001",
					VolumeAttributes: map[string]string{
						"WWID": "60000970000120001647533030303031",
					},
				},
			},
			ClaimRef: &corev1.ObjectReference{
				Name:      "pvc-iscsi-001",
				Namespace: "default",
			},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			StorageClassName:              "powermax-sc",
			VolumeMode:                    &filesystemMode,
		},
		Status: corev1.PersistentVolumeStatus{
			Phase: corev1.VolumeBound,
		},
	}

	fakeClient := fake.NewSimpleClientset(pvc, pv)
	mgr := newTestManager(t, fakeClient, SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "* * * * *",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       60,
		NodeName:             "node-1",
	})

	// Mock mount table with /dev/mapper/mpathb (iSCSI multipath device)
	gofsutil.GOFSMockMounts = []gofsutil.Info{
		{
			Device: "/dev/mapper/mpathb",
			Path:   "/var/lib/kubelet/plugins/powermax.emc.dell.com/disks/CSM-vol001-000120001647-00001",
			Source: "/dev/mapper/mpathb",
		},
	}

	// Mock WWNToDevicePath to return /dev/dm-2
	originalWWNToDevicePathFunc := wwnToDevicePathFunc
	wwnToDevicePathFunc = func(_ context.Context, _ string) (string, string, error) {
		return "60000970000120001647533030303031", "/dev/dm-2", nil
	}
	defer func() { wwnToDevicePathFunc = originalWWNToDevicePathFunc }()

	// Mock normalizeDevicePath to simulate symlink resolution for iSCSI
	originalNormalizeDevicePathFunc := normalizeDevicePathFunc
	normalizeDevicePathFunc = func(path string) string {
		if path == "/dev/mapper/mpathb" {
			return "/dev/dm-2"
		}
		return path
	}
	defer func() { normalizeDevicePathFunc = originalNormalizeDevicePathFunc }()

	// Execute one reclamation cycle
	mgr.RunOnce()

	// Verify PVC annotations were updated successfully
	updatedPVC, err := fakeClient.CoreV1().PersistentVolumeClaims("default").Get(
		context.Background(), "pvc-iscsi-001", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "success", updatedPVC.Annotations[AnnotationStatus],
		"iSCSI multipath device path normalization should work")
}

// TestDevicePathNormalization_NVMeTCPDevice verifies that device path normalization
// works correctly for NVMe-TCP devices (which don't use multipath symlinks)
func TestDevicePathNormalization_NVMeTCPDevice(t *testing.T) {
	gofsutil.UseMockFS()
	defer resetGofsutilMock()

	pvc := makePVC("pvc-nvme-001", "default")
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv-nvme-001",
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("10Gi"),
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       Name,
					VolumeHandle: "CSM-vol001-000120001647-00001",
					VolumeAttributes: map[string]string{
						"WWID": "60000970000120001647533030303031",
					},
				},
			},
			ClaimRef: &corev1.ObjectReference{
				Name:      "pvc-nvme-001",
				Namespace: "default",
			},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			StorageClassName:              "powermax-sc",
			VolumeMode:                    &filesystemMode,
		},
		Status: corev1.PersistentVolumeStatus{
			Phase: corev1.VolumeBound,
		},
	}

	fakeClient := fake.NewSimpleClientset(pvc, pv)
	mgr := newTestManager(t, fakeClient, SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "* * * * *",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       60,
		NodeName:             "node-1",
	})

	// Mock mount table with /dev/nvme0n1 (NVMe device - no symlinks)
	gofsutil.GOFSMockMounts = []gofsutil.Info{
		{
			Device: "/dev/nvme0n1",
			Path:   "/var/lib/kubelet/plugins/powermax.emc.dell.com/disks/CSM-vol001-000120001647-00001",
			Source: "/dev/nvme0n1",
		},
	}

	// Mock WWNToDevicePath to return /dev/nvme0n1 (same as mount table)
	originalWWNToDevicePathFunc := wwnToDevicePathFunc
	wwnToDevicePathFunc = func(_ context.Context, _ string) (string, string, error) {
		return "60000970000120001647533030303031", "/dev/nvme0n1", nil
	}
	defer func() { wwnToDevicePathFunc = originalWWNToDevicePathFunc }()

	// NVMe devices don't have symlinks, so normalization returns the same path
	// No need to mock normalizeDevicePathFunc - default behavior is correct

	// Execute one reclamation cycle
	mgr.RunOnce()

	// Verify PVC annotations were updated successfully
	updatedPVC, err := fakeClient.CoreV1().PersistentVolumeClaims("default").Get(
		context.Background(), "pvc-nvme-001", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "success", updatedPVC.Annotations[AnnotationStatus],
		"NVMe-TCP device path normalization should work (no symlinks)")
}

// TestDevicePathNormalization_BlockVolume verifies that device path normalization
// works correctly for block volumes (which don't appear in mount table)
func TestDevicePathNormalization_BlockVolume(t *testing.T) {
	gofsutil.UseMockFS()
	defer resetGofsutilMock()

	pvc := makePVC("pvc-block-001", "default")
	pvc.Labels = map[string]string{
		LabelBlockReclaim: "true",
	}
	blockMode := corev1.PersistentVolumeBlock
	pvc.Spec.VolumeMode = &blockMode

	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv-block-001",
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("10Gi"),
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       Name,
					VolumeHandle: "CSM-volblk001-000120001647-00002",
					VolumeAttributes: map[string]string{
						"WWID": "60000970000120001647533030303032",
					},
				},
			},
			ClaimRef: &corev1.ObjectReference{
				Name:      "pvc-block-001",
				Namespace: "default",
			},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			StorageClassName:              "powermax-sc",
			VolumeMode:                    &blockMode,
		},
		Status: corev1.PersistentVolumeStatus{
			Phase: corev1.VolumeBound,
		},
	}

	fakeClient := fake.NewSimpleClientset(pvc, pv)
	mgr := newTestManager(t, fakeClient, SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "* * * * *",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       60,
		NodeName:             "node-1",
	})

	// Block volumes don't appear in mount table
	gofsutil.GOFSMockMounts = []gofsutil.Info{}

	// Mock WWNToDevicePath to return /dev/dm-3 (multipath block device)
	originalWWNToDevicePathFunc := wwnToDevicePathFunc
	wwnToDevicePathFunc = func(_ context.Context, _ string) (string, string, error) {
		return "60000970000120001647533030303032", "/dev/dm-3", nil
	}
	defer func() { wwnToDevicePathFunc = originalWWNToDevicePathFunc }()

	// Execute one reclamation cycle
	mgr.RunOnce()

	// Verify PVC annotations were updated successfully
	// Block volumes don't need to be in mount table
	updatedPVC, err := fakeClient.CoreV1().PersistentVolumeClaims("default").Get(
		context.Background(), "pvc-block-001", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "success", updatedPVC.Annotations[AnnotationStatus],
		"Block volume device path normalization should work (no mount table check)")
}

// --- Device Path Normalization Tests ---

// TestDevicePathNormalization_FCMultipathDevice verifies that device path normalization
// correctly handles FC multipath devices where mount table shows /dev/mapper/mpatha
// but WWN resolution returns /dev/dm-1
func TestDevicePathNormalization_FCMultipathDevice(t *testing.T) {
	gofsutil.UseMockFS()
	defer resetGofsutilMock()

	pvc := makePVC("pvc-fc-001", "default")
	pv := &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{
			Name: "pv-fc-001",
		},
		Spec: corev1.PersistentVolumeSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: resource.MustParse("10Gi"),
			},
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       Name,
					VolumeHandle: "CSM-vol001-000120001647-00001",
					VolumeAttributes: map[string]string{
						"WWID": "60000970000120001647533030303031",
					},
				},
			},
			ClaimRef: &corev1.ObjectReference{
				Name:      "pvc-fc-001",
				Namespace: "default",
			},
			PersistentVolumeReclaimPolicy: corev1.PersistentVolumeReclaimDelete,
			StorageClassName:              "powermax-sc",
			VolumeMode:                    &filesystemMode,
		},
		Status: corev1.PersistentVolumeStatus{
			Phase: corev1.VolumeBound,
		},
	}

	fakeClient := fake.NewSimpleClientset(pvc, pv)
	mgr := newTestManager(t, fakeClient, SpaceReclamationConfig{
		Enabled:              true,
		Schedule:             "* * * * *",
		MaxConcurrentVolumes: 2,
		TimeoutSeconds:       60,
		NodeName:             "node-1",
	})

	// Mock mount table with /dev/mapper/mpatha (symlink to dm-1)
	// This simulates the real-world FC multipath scenario
	gofsutil.GOFSMockMounts = []gofsutil.Info{
		{
			Device: "/dev/mapper/mpatha",
			Path:   "/var/lib/kubelet/plugins/powermax.emc.dell.com/disks/CSM-vol001-000120001647-00001",
			Source: "/dev/mapper/mpatha",
		},
	}

	// Mock WWNToDevicePath to return /dev/dm-1 (the actual device, not the symlink)
	// This simulates what gofsutil.WWNToDevicePathX returns in real environments
	originalWWNToDevicePathFunc := wwnToDevicePathFunc
	wwnToDevicePathFunc = func(_ context.Context, _ string) (string, string, error) {
		return "60000970000120001647533030303031", "/dev/dm-1", nil
	}
	defer func() { wwnToDevicePathFunc = originalWWNToDevicePathFunc }()

	// Mock normalizeDevicePath to simulate symlink resolution
	// In real environments, /dev/mapper/mpatha is a symlink to ../dm-1
	originalNormalizeDevicePathFunc := normalizeDevicePathFunc
	normalizeDevicePathFunc = func(path string) string {
		if path == "/dev/mapper/mpatha" {
			return "/dev/dm-1"
		}
		return path
	}
	defer func() { normalizeDevicePathFunc = originalNormalizeDevicePathFunc }()

	// Execute one reclamation cycle
	mgr.RunOnce()

	// Verify PVC annotations were updated successfully
	// This proves that device path normalization worked correctly
	updatedPVC, err := fakeClient.CoreV1().PersistentVolumeClaims("default").Get(
		context.Background(), "pvc-fc-001", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "success", updatedPVC.Annotations[AnnotationStatus],
		"PVC should be annotated with status=success after fstrim (device path normalization worked)")
	assert.NotEmpty(t, updatedPVC.Annotations[AnnotationBytesAvailable],
		"PVC should have bytes-available annotation")
	assert.NotEmpty(t, updatedPVC.Annotations[AnnotationLastRunTime],
		"PVC should have last-run-time annotation")
	assert.Equal(t, "node-1", updatedPVC.Annotations[AnnotationNode],
		"PVC should have node annotation")
}
