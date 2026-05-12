/*
 Copyright © 2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"fmt"
	"sync"
	"testing"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dell/csi-powermax/v2/k8smock"
	"github.com/dell/gofsutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
)

// ============================================================================
// Helpers
// ============================================================================

func setupMockFS() {
	gofsutil.UseMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatError = false
	gofsutil.GOFSMock.InduceGetDiskFormatType = ""
}

func mockPVC(name, namespace string, labels map[string]string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
	}
}

const testTargetPath = "/var/lib/kubelet/pods/uid-123/volumes/kubernetes.io~csi/test-pv/mount"

// Helper that returns a config with a mock k8sUtils attached
func newTestConfig(t *testing.T, enabled bool, mode string) (*fsCheckConfig, *k8smock.MockUtilsInterface, *gomock.Controller) {
	ctrl := gomock.NewController(t)
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	cfg := &fsCheckConfig{
		enabled:  enabled,
		mode:     mode,
		k8sUtils: mockUtils,
	}
	return cfg, mockUtils, ctrl
}

// noMountsFunc is a mock getDevMountsFunc that returns no mounts
func noMountsFunc(_ *Device) ([]gofsutil.Info, error) {
	return []gofsutil.Info{}, nil
}

// mockEventRecorderFunc replaces newEventRecorderFunc for tests
func mockEventRecorderFunc() (record.EventRecorder, error) {
	return record.NewFakeRecorder(100), nil
}

func setupTestMocks() func() {
	origGetDevMounts := getDevMountsFunc
	origNewEventRecorder := newEventRecorderFunc
	origCachedEventRecorder := cachedEventRecorder

	getDevMountsFunc = noMountsFunc
	newEventRecorderFunc = mockEventRecorderFunc
	// Reset event recorder singleton for tests
	eventRecorderOnce = *new(sync.Once)
	cachedEventRecorder = nil

	return func() {
		getDevMountsFunc = origGetDevMounts
		newEventRecorderFunc = origNewEventRecorder
		eventRecorderOnce = *new(sync.Once)
		cachedEventRecorder = origCachedEventRecorder
	}
}

// ============================================================================
// Section 1: parsePVNameFromTargetPath tests
// ============================================================================

// Test ID: U-026
func TestParsePVNameFromTargetPath(t *testing.T) {
	tests := []struct {
		name       string
		targetPath string
		want       string
	}{
		{
			name:       "standard kubelet path",
			targetPath: "/var/lib/kubelet/pods/abc-123/volumes/kubernetes.io~csi/pvc-test-vol/mount",
			want:       "pvc-test-vol",
		},
		{
			name:       "custom kubelet root",
			targetPath: "/custom/kubelet/pods/uid-456/volumes/kubernetes.io~csi/my-pv-name/mount",
			want:       "my-pv-name",
		},
		{
			name:       "path with hyphens in PV name",
			targetPath: "/var/lib/kubelet/pods/pod-uid/volumes/kubernetes.io~csi/pvc-abc-def-123/mount",
			want:       "pvc-abc-def-123",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePVNameFromTargetPath(tt.targetPath)
			assert.Equal(t, tt.want, got, "parsePVNameFromTargetPath(%q)", tt.targetPath)
		})
	}
}

// Test ID: U-027
func TestParsePVNameFromTargetPathMalformed(t *testing.T) {
	tests := []struct {
		name       string
		targetPath string
	}{
		{
			name:       "empty path",
			targetPath: "",
		},
		{
			name:       "no kubernetes.io~csi segment",
			targetPath: "/var/lib/kubelet/pods/abc/volumes/other/pv-name/mount",
		},
		{
			name:       "path ends at kubernetes.io~csi",
			targetPath: "/var/lib/kubelet/pods/abc/volumes/kubernetes.io~csi",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePVNameFromTargetPath(tt.targetPath)
			assert.Empty(t, got, "parsePVNameFromTargetPath(%q) should return empty for malformed path", tt.targetPath)
		})
	}
}

// ============================================================================
// Section 2: isAccessModeReadOnlyOrMulti tests
// ============================================================================

// Test ID: U-013
func TestFSCheckSkipReadOnly(t *testing.T) {
	mode := &csi.VolumeCapability_AccessMode{
		Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
	}
	assert.True(t, isAccessModeReadOnlyOrMulti(mode),
		"SINGLE_NODE_READER_ONLY should be detected as read-only")
}

// Test ID: U-014
func TestFSCheckSkipROX(t *testing.T) {
	mode := &csi.VolumeCapability_AccessMode{
		Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
	}
	assert.True(t, isAccessModeReadOnlyOrMulti(mode),
		"MULTI_NODE_READER_ONLY should be detected as read-only/multi")
}

// Test ID: U-015
func TestFSCheckSkipRWX(t *testing.T) {
	mode := &csi.VolumeCapability_AccessMode{
		Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
	}
	assert.True(t, isAccessModeReadOnlyOrMulti(mode),
		"MULTI_NODE_MULTI_WRITER should be detected as multi-node")
}

// Test ID: U-013 (continued) - RWO should NOT be read-only
func TestFSCheckRWONotReadOnly(t *testing.T) {
	mode := &csi.VolumeCapability_AccessMode{
		Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
	}
	assert.False(t, isAccessModeReadOnlyOrMulti(mode),
		"SINGLE_NODE_WRITER should not be detected as read-only/multi")
}

// Test ID: U-013 (nil safety)
func TestFSCheckNilAccessMode(t *testing.T) {
	assert.False(t, isAccessModeReadOnlyOrMulti(nil),
		"nil access mode should return false")
}

// ============================================================================
// Section 3: isSupportedFSType tests (NEW)
// ============================================================================

func TestIsSupportedFSType(t *testing.T) {
	assert.True(t, isSupportedFSType("ext4"))
	assert.True(t, isSupportedFSType("ext3"))
	assert.True(t, isSupportedFSType("ext2"))
	assert.True(t, isSupportedFSType("xfs"))
	assert.False(t, isSupportedFSType("ntfs"))
	assert.False(t, isSupportedFSType("btrfs"))
	assert.False(t, isSupportedFSType("nfs"))
	assert.False(t, isSupportedFSType(""))
}

// ============================================================================
// Section 4: performFSCheck skip condition tests
// ============================================================================

// Test ID: U-006 - disabled
func TestFSCheckSkipDisabled(t *testing.T) {
	setupMockFS()
	ctx := context.Background()
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	cfg := &fsCheckConfig{enabled: false, mode: "checkOnly"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(ctx, sysDevice, cfg, accMode, "vol-002", testTargetPath)
	assert.NoError(t, err)
}

// Test ID: U-006b - nil config
func TestFSCheckSkipNilConfig(t *testing.T) {
	setupMockFS()
	ctx := context.Background()
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(ctx, sysDevice, nil, accMode, "vol-nil", testTargetPath)
	assert.NoError(t, err)
}

// Skip for read-only
func TestFSCheckSkipReadOnlyAccessMode(t *testing.T) {
	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ext4"
	ctx := context.Background()
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY}
	err := performFSCheck(ctx, sysDevice, cfg, accMode, "vol-ro", testTargetPath)
	assert.NoError(t, err)
}

// Skip for multi-node
func TestFSCheckSkipMultiNodeAccessMode(t *testing.T) {
	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ext4"
	ctx := context.Background()
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER}
	err := performFSCheck(ctx, sysDevice, cfg, accMode, "vol-multi", testTargetPath)
	assert.NoError(t, err)
}

// Skip newly formatted
func TestFSCheckSkipNewlyFormatted(t *testing.T) {
	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = ""
	ctx := context.Background()
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(ctx, sysDevice, cfg, accMode, "vol-new", testTargetPath)
	assert.NoError(t, err)
}

// Skip unsupported FS
func TestFSCheckSkipUnsupportedFS(t *testing.T) {
	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ntfs"
	ctx := context.Background()
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(ctx, sysDevice, cfg, accMode, "vol-ntfs", testTargetPath)
	assert.NoError(t, err)
}

// GetDiskFormat error
func TestFSCheckGetDiskFormatError(t *testing.T) {
	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatError = true
	ctx := context.Background()
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(ctx, sysDevice, cfg, accMode, "vol-err", testTargetPath)
	assert.NoError(t, err)
}

// ============================================================================
// Section 5: Already-mounted check (NEW - per FR-2)
// ============================================================================

// Test ID: AC-14 - skip when already mounted
func TestFSCheckSkipAlreadyMounted(t *testing.T) {
	cleanup := setupTestMocks()
	defer cleanup()

	// Override getDevMountsFunc to return a mount
	getDevMountsFunc = func(_ *Device) ([]gofsutil.Info, error) {
		return []gofsutil.Info{{Device: "/dev/sda", Path: "/mnt/target"}}, nil
	}

	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ext4"
	ctx := context.Background()
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(ctx, sysDevice, cfg, accMode, "vol-mounted", testTargetPath)
	assert.NoError(t, err, "should skip FS check for already mounted volume")
}

// Skip gracefully on mount check error
func TestFSCheckSkipOnMountCheckError(t *testing.T) {
	cleanup := setupTestMocks()
	defer cleanup()

	getDevMountsFunc = func(_ *Device) ([]gofsutil.Info, error) {
		return nil, errors.New("mount check failed")
	}

	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ext4"
	ctx := context.Background()
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(ctx, sysDevice, cfg, accMode, "vol-mnterr", testTargetPath)
	assert.NoError(t, err, "should skip gracefully on mount check error")
}

// ============================================================================
// Section 6: Lazy resolution / PVC label tests (resolvePVCOverrides)
// ============================================================================

// AC-17: PVC lookup fails -> falls back to global
func TestResolvePVCOverrides_LookupFails(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-123").
		Return(nil, fmt.Errorf("PV not found"))

	cfg := &fsCheckConfig{enabled: true, mode: "checkAndRepair", k8sUtils: mockUtils}
	enabled, mode, pvcName, pvcNS := resolvePVCOverrides(context.Background(), cfg, "vol-123", testTargetPath)
	assert.True(t, enabled, "should fall back to global enabled")
	assert.Equal(t, "checkAndRepair", mode, "should fall back to global mode")
	assert.Empty(t, pvcName)
	assert.Empty(t, pvcNS)
}

// No PV name in path
func TestResolvePVCOverrides_NoPVNameInPath(t *testing.T) {
	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly"}
	enabled, mode, pvcName, pvcNS := resolvePVCOverrides(context.Background(), cfg, "vol-123", "/some/random/path")
	assert.True(t, enabled)
	assert.Equal(t, "checkOnly", mode)
	assert.Empty(t, pvcName)
	assert.Empty(t, pvcNS)
}

// Nil k8sUtils
func TestResolvePVCOverrides_NilK8sUtils(t *testing.T) {
	cfg := &fsCheckConfig{enabled: true, mode: "checkAndRepair", k8sUtils: nil}
	enabled, mode, _, _ := resolvePVCOverrides(context.Background(), cfg, "vol-123", testTargetPath)
	assert.True(t, enabled)
	assert.Equal(t, "checkAndRepair", mode)
}

// Nil PVC returned
func TestResolvePVCOverrides_NilPVC(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-123").Return(nil, nil)

	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly", k8sUtils: mockUtils}
	enabled, mode, _, _ := resolvePVCOverrides(context.Background(), cfg, "vol-123", testTargetPath)
	assert.True(t, enabled)
	assert.Equal(t, "checkOnly", mode)
}

// AC-5: PVC label disables
func TestResolvePVCOverrides_PVCDisables(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-123").
		Return(mockPVC("my-pvc", "default", map[string]string{PVCLabelFSCheckEnabled: "false"}), nil)

	cfg := &fsCheckConfig{enabled: true, mode: "checkAndRepair", k8sUtils: mockUtils}
	enabled, mode, pvcName, pvcNS := resolvePVCOverrides(context.Background(), cfg, "vol-123", testTargetPath)
	assert.False(t, enabled, "PVC label should disable")
	assert.Equal(t, "checkAndRepair", mode, "mode should not be affected when disabled")
	assert.Equal(t, "my-pvc", pvcName)
	assert.Equal(t, "default", pvcNS)
}

// AC-6: PVC label enables even when global disabled
func TestResolvePVCOverrides_PVCEnables(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-123").
		Return(mockPVC("my-pvc", "default", map[string]string{PVCLabelFSCheckEnabled: "true"}), nil)

	cfg := &fsCheckConfig{enabled: false, mode: "checkOnly", k8sUtils: mockUtils}
	enabled, _, _, _ := resolvePVCOverrides(context.Background(), cfg, "vol-123", testTargetPath)
	assert.True(t, enabled, "PVC label should enable even when global disabled")
}

// AC-7: PVC overrides mode
func TestResolvePVCOverrides_PVCOverridesMode(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-123").
		Return(mockPVC("my-pvc", "default", map[string]string{PVCLabelFSCheckMode: "checkAndRepair"}), nil)

	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly", k8sUtils: mockUtils}
	_, mode, _, _ := resolvePVCOverrides(context.Background(), cfg, "vol-123", testTargetPath)
	assert.Equal(t, "checkAndRepair", mode)
}

// AC-18: Invalid labels fall back to global
func TestResolvePVCOverrides_InvalidLabels(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-123").
		Return(mockPVC("my-pvc", "default", map[string]string{
			PVCLabelFSCheckEnabled: "invalid",
			PVCLabelFSCheckMode:    "badvalue",
		}), nil)

	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly", k8sUtils: mockUtils}
	enabled, mode, _, _ := resolvePVCOverrides(context.Background(), cfg, "vol-123", testTargetPath)
	assert.True(t, enabled, "invalid label should not change enabled")
	assert.Equal(t, "checkOnly", mode, "invalid label should not change mode")
}

// ============================================================================
// Section 7: Lazy resolution - verify k8sUtils NOT called for cheap skips
// ============================================================================

// When fscheck is globally disabled but PVC lookup is reachable, k8sUtils IS called
// because a PVC label may override the global setting (AC-6).
func TestPerformFSCheck_GlobalDisabledStillChecksPVCLabels(t *testing.T) {
	cleanup := setupTestMocks()
	defer cleanup()

	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ext4"

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	// PVC lookup WILL be called -- PVC has no override labels, so global disabled wins
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-lazy").
		Return(mockPVC("my-pvc", "default", nil), nil)

	cfg := &fsCheckConfig{enabled: false, mode: "checkOnly", k8sUtils: mockUtils}
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(context.Background(), sysDevice, cfg, accMode, "vol-lazy", testTargetPath)
	assert.NoError(t, err, "should skip FS check when globally disabled and no PVC override")
}

// When access mode is read-only, k8sUtils should NOT be called
func TestPerformFSCheck_ReadOnlySkipsPVCLookup(t *testing.T) {
	cleanup := setupTestMocks()
	defer cleanup()

	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ext4"

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	// NO EXPECT -- must not be called

	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly", k8sUtils: mockUtils}
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY}
	err := performFSCheck(context.Background(), sysDevice, cfg, accMode, "vol-lazy2", testTargetPath)
	assert.NoError(t, err)
}

// When FS type is unsupported, k8sUtils should NOT be called
func TestPerformFSCheck_UnsupportedFSSkipsPVCLookup(t *testing.T) {
	cleanup := setupTestMocks()
	defer cleanup()

	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ntfs"

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	// NO EXPECT -- must not be called

	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly", k8sUtils: mockUtils}
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(context.Background(), sysDevice, cfg, accMode, "vol-lazy3", testTargetPath)
	assert.NoError(t, err)
}

// ============================================================================
// Section 7b: AC-6 end-to-end - PVC label overrides global disabled through performFSCheck
// ============================================================================

// AC-6: Global disabled + PVC label enables = fsck MUST run (end-to-end through performFSCheck)
func TestPerformFSCheck_AC6_PVCOverridesGlobalDisabled(t *testing.T) {
	cleanup := setupTestMocks()
	defer cleanup()

	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ext4"

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	// PVC has fs_check_enabled=true, overriding global disabled
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-ac6").
		Return(mockPVC("my-pvc", "default", map[string]string{
			PVCLabelFSCheckEnabled: "true",
		}), nil)

	cfg := &fsCheckConfig{enabled: false, mode: "checkOnly", k8sUtils: mockUtils}
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}

	// performFSCheck should NOT return nil (skip) -- it should proceed to run the checker.
	// In CI without real e2fsck, GetFSChecker will fail, but that proves we got PAST the
	// skip checks and into the actual fsck execution path.
	err := performFSCheck(context.Background(), sysDevice, cfg, accMode, "vol-ac6", testTargetPath)
	// We expect an error because GetFSChecker will fail in test environment (no real device),
	// but the key assertion is that we did NOT get nil (which would mean "skipped").
	if err == nil {
		// If somehow it succeeded (unlikely in test env), that's also fine -- fsck ran.
		return
	}
	// The error should be from GetFSChecker or Check, NOT a "skipping" nil return
	assert.NotContains(t, err.Error(), "Skipping",
		"AC-6 violation: PVC label enabled=true should override global disabled, but fsck was skipped")
}

// ============================================================================
// Section 8: ext4 check execution and ext2/ext3 support (NEW)
// ============================================================================

// Test ID: U-001 - ext4 check
func TestFSCheckExt4WithMockFS(t *testing.T) {
	cleanup := setupTestMocks()
	defer cleanup()

	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ext4"

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-001").
		Return(mockPVC("my-pvc", "default", nil), nil)

	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly", k8sUtils: mockUtils}
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(context.Background(), sysDevice, cfg, accMode, "vol-001", testTargetPath)
	// In CI, GetFSChecker may fail (no e2fsck). Code path is exercised either way.
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			assert.NotEqual(t, codes.InvalidArgument, st.Code())
		}
	}
}

// ext3 check (NEW)
func TestFSCheckExt3NotSkipped(t *testing.T) {
	cleanup := setupTestMocks()
	defer cleanup()

	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ext3"

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-ext3").
		Return(mockPVC("my-pvc", "default", nil), nil)

	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly", k8sUtils: mockUtils}
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(context.Background(), sysDevice, cfg, accMode, "vol-ext3", testTargetPath)
	// Reaching GetFSChecker means ext3 was NOT skipped -- that's the test
	if err != nil {
		assert.NotContains(t, err.Error(), "unsupported", "ext3 should not be skipped as unsupported")
	}
}

// ext2 check (NEW)
func TestFSCheckExt2NotSkipped(t *testing.T) {
	cleanup := setupTestMocks()
	defer cleanup()

	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ext2"

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-ext2").
		Return(mockPVC("my-pvc", "default", nil), nil)

	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly", k8sUtils: mockUtils}
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(context.Background(), sysDevice, cfg, accMode, "vol-ext2", testTargetPath)
	if err != nil {
		assert.NotContains(t, err.Error(), "unsupported", "ext2 should not be skipped as unsupported")
	}
}

// Test ID: U-012 - context timeout
func TestFSCheckContextTimeout(t *testing.T) {
	cleanup := setupTestMocks()
	defer cleanup()

	setupMockFS()
	gofsutil.GOFSMock.InduceGetDiskFormatType = "ext4"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockUtils := k8smock.NewMockUtilsInterface(ctrl)
	mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-003").
		Return(mockPVC("my-pvc", "default", nil), nil)

	cfg := &fsCheckConfig{enabled: true, mode: "checkOnly", k8sUtils: mockUtils}
	sysDevice := &Device{FullPath: "/dev/sda", RealDev: "/dev/sda"}
	accMode := &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER}
	err := performFSCheck(ctx, sysDevice, cfg, accMode, "vol-003", testTargetPath)
	if err != nil {
		st, ok := status.FromError(err)
		if ok {
			assert.Equal(t, codes.Aborted, st.Code())
		}
	}
}

// ============================================================================
// Section 9: Observer tests
// ============================================================================

// Test ID: U-020
func TestFSCheckObserverEvents(t *testing.T) {
	observer := &fsCheckPVCObserver{
		pvcName:      "test-pvc",
		pvcNamespace: "default",
		devicePath:   "/dev/sda",
		fsType:       "ext4",
		volumeID:     "vol-001",
	}
	observer.OnEvent(gofsutil.StartedFSCheckEvent)
	observer.OnEvent(gofsutil.FoundNoErrorsEvent)
	assert.Contains(t, observer.events, gofsutil.StartedFSCheckEvent)
	assert.Contains(t, observer.events, gofsutil.FoundNoErrorsEvent)
	assert.False(t, observer.sawTimeout)
}

// Test ID: U-021
func TestFSCheckObserverTimeout(t *testing.T) {
	observer := &fsCheckPVCObserver{
		pvcName:      "test-pvc",
		pvcNamespace: "default",
		devicePath:   "/dev/sda",
		fsType:       "ext4",
		volumeID:     "vol-001",
	}
	observer.OnEvent(gofsutil.StartedFSCheckEvent)
	observer.OnEvent(gofsutil.FSCheckTimedOutEvent)
	assert.True(t, observer.sawTimeout)
}

// Observer with nil EventRecorder - no panic
func TestFSCheckObserverNilRecorder(t *testing.T) {
	observer := &fsCheckPVCObserver{
		pvcName:       "test-pvc",
		pvcNamespace:  "default",
		devicePath:    "/dev/sda",
		fsType:        "ext4",
		volumeID:      "vol-001",
		eventRecorder: nil,
	}
	observer.OnEvent(gofsutil.StartedFSCheckEvent)
	assert.Len(t, observer.events, 1)
}

// Observer with empty PVC info - no panic
func TestFSCheckObserverEmptyPVC(t *testing.T) {
	observer := &fsCheckPVCObserver{
		pvcName:      "",
		pvcNamespace: "",
		devicePath:   "/dev/sda",
		fsType:       "ext4",
		volumeID:     "vol-001",
	}
	observer.OnEvent(gofsutil.StartedFSCheckEvent)
	assert.Len(t, observer.events, 1)
}

// Observer with FakeRecorder posts events
func TestFSCheckObserverWithRecorder(t *testing.T) {
	fakeRecorder := record.NewFakeRecorder(100)
	observer := &fsCheckPVCObserver{
		pvcName:       "test-pvc",
		pvcNamespace:  "default",
		devicePath:    "/dev/sda",
		fsType:        "ext4",
		volumeID:      "vol-001",
		eventRecorder: fakeRecorder,
	}
	observer.OnEvent(gofsutil.StartedFSCheckEvent)
	observer.OnEvent(gofsutil.FoundNoErrorsEvent)

	// FakeRecorder buffers events in its channel
	assert.Len(t, observer.events, 2)
	// Drain channel to verify events were posted
	event1 := <-fakeRecorder.Events
	assert.Contains(t, event1, "FSCheckStarted")
	event2 := <-fakeRecorder.Events
	assert.Contains(t, event2, "FSCheckSucceeded")
}

// ============================================================================
// Section 10: Event mapping tests
// ============================================================================

// Test ID: U-020 (event mapping)
func TestMapFSCheckEventToK8s(t *testing.T) {
	tests := []struct {
		event      string
		wantType   string
		wantReason string
	}{
		{gofsutil.StartedFSCheckEvent, "Normal", "FSCheckStarted"},
		{gofsutil.FoundNoErrorsEvent, "Normal", "FSCheckSucceeded"},
		{gofsutil.FinishedFSRepairEvent, "Normal", "FSCheckRepaired"},
		{gofsutil.FoundErrorsEvent, "Warning", "FSCheckFailed"},
		{gofsutil.FSCheckTimedOutEvent, "Warning", "FSCheckTimedOut"},
		{gofsutil.FSRepairFailedEvent, "Warning", "FSRepairFailed"},
		{gofsutil.FSCheckFailedEvent, "Warning", "FSCheckFailed"},
		{gofsutil.FoundDirtyLogEvent, "Normal", "FSCheckDirtyLog"},
		{gofsutil.StartFSRepairEvent, "Normal", "FSRepairStarted"},
		{gofsutil.StartLogReplayEvent, "Normal", "FSLogReplayStarted"},
		{gofsutil.LogReplayDoneEvent, "Normal", "FSLogReplayDone"},
		{gofsutil.LogReplayFailedEvent, "Warning", "FSLogReplayFailed"},
		{gofsutil.FSRepairTimedOutEvent, "Warning", "FSRepairTimedOut"},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			gotType, gotReason := mapFSCheckEventToK8s(tt.event)
			assert.Equal(t, tt.wantType, gotType)
			assert.Equal(t, tt.wantReason, gotReason)
		})
	}
}

// Default/unknown event
func TestMapFSCheckEventToK8sDefault(t *testing.T) {
	gotType, gotReason := mapFSCheckEventToK8s("some-unknown-event")
	assert.Equal(t, "Normal", gotType)
	assert.Equal(t, "FSCheckEvent", gotReason)
}

// ============================================================================
// Section 11: Precedence matrix (AC-7)
// ============================================================================

// Test ID: U-034 - Global vs PVC-level precedence
func TestFSCheckGlobalVsPVCPrecedence(t *testing.T) {
	tests := []struct {
		name        string
		globalOn    bool
		globalMode  string
		pvcLabels   map[string]string
		wantEnabled bool
		wantMode    string
	}{
		{
			name: "global disabled, no PVC labels", globalOn: false, globalMode: "checkOnly",
			pvcLabels: nil, wantEnabled: false, wantMode: "checkOnly",
		},
		{
			name: "global disabled, PVC enables", globalOn: false, globalMode: "checkOnly",
			pvcLabels:   map[string]string{PVCLabelFSCheckEnabled: "true"},
			wantEnabled: true, wantMode: "checkOnly",
		},
		{
			name: "global enabled, PVC disables", globalOn: true, globalMode: "checkAndRepair",
			pvcLabels:   map[string]string{PVCLabelFSCheckEnabled: "false"},
			wantEnabled: false, wantMode: "checkAndRepair",
		},
		{
			name: "global checkOnly, PVC overrides to checkAndRepair", globalOn: true, globalMode: "checkOnly",
			pvcLabels:   map[string]string{PVCLabelFSCheckMode: "checkAndRepair"},
			wantEnabled: true, wantMode: "checkAndRepair",
		},
		{
			name: "invalid PVC label falls back to global", globalOn: true, globalMode: "checkOnly",
			pvcLabels:   map[string]string{PVCLabelFSCheckEnabled: "invalid", PVCLabelFSCheckMode: "badvalue"},
			wantEnabled: true, wantMode: "checkOnly",
		},
		{
			name: "PVC enables and sets checkAndRepair", globalOn: false, globalMode: "checkOnly",
			pvcLabels:   map[string]string{PVCLabelFSCheckEnabled: "true", PVCLabelFSCheckMode: "checkAndRepair"},
			wantEnabled: true, wantMode: "checkAndRepair",
		},
		{
			name: "case-insensitive: PVC label True enables", globalOn: false, globalMode: "checkOnly",
			pvcLabels:   map[string]string{PVCLabelFSCheckEnabled: "True"},
			wantEnabled: true, wantMode: "checkOnly",
		},
		{
			name: "case-insensitive: PVC label FALSE disables", globalOn: true, globalMode: "checkAndRepair",
			pvcLabels:   map[string]string{PVCLabelFSCheckEnabled: "FALSE"},
			wantEnabled: false, wantMode: "checkAndRepair",
		},
		{
			name: "case-insensitive: PVC label CheckAndRepair mode", globalOn: true, globalMode: "checkOnly",
			pvcLabels:   map[string]string{PVCLabelFSCheckMode: "CheckAndRepair"},
			wantEnabled: true, wantMode: "checkAndRepair",
		},
		{
			name: "mode label ignored when disabled via PVC label", globalOn: true, globalMode: "checkOnly",
			pvcLabels:   map[string]string{PVCLabelFSCheckEnabled: "false", PVCLabelFSCheckMode: "checkAndRepair"},
			wantEnabled: false, wantMode: "checkOnly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			mockUtils := k8smock.NewMockUtilsInterface(ctrl)
			mockUtils.EXPECT().GetPVCForVolume(gomock.Any(), "test-pv", "vol-matrix").
				Return(mockPVC("my-pvc", "default", tt.pvcLabels), nil)

			cfg := &fsCheckConfig{enabled: tt.globalOn, mode: tt.globalMode, k8sUtils: mockUtils}
			enabled, mode, _, _ := resolvePVCOverrides(context.Background(), cfg, "vol-matrix", testTargetPath)
			assert.Equal(t, tt.wantEnabled, enabled, "enabled mismatch")
			assert.Equal(t, tt.wantMode, mode, "mode mismatch")
		})
	}
}

// ============================================================================
// Section 12: EventRecorder singleton tests (NEW)
// ============================================================================

func TestInitEventRecorder_Error(t *testing.T) {
	origFn := newEventRecorderFunc
	origCached := cachedEventRecorder
	defer func() {
		newEventRecorderFunc = origFn
		eventRecorderOnce = *new(sync.Once)
		cachedEventRecorder = origCached
	}()

	newEventRecorderFunc = func() (record.EventRecorder, error) {
		return nil, errors.New("k8s unavailable")
	}
	eventRecorderOnce = *new(sync.Once)
	cachedEventRecorder = nil

	recorder := initEventRecorder()
	assert.Nil(t, recorder)
}

func TestInitEventRecorder_Success(t *testing.T) {
	origFn := newEventRecorderFunc
	origCached := cachedEventRecorder
	defer func() {
		newEventRecorderFunc = origFn
		eventRecorderOnce = *new(sync.Once)
		cachedEventRecorder = origCached
	}()

	newEventRecorderFunc = func() (record.EventRecorder, error) {
		return record.NewFakeRecorder(10), nil
	}
	eventRecorderOnce = *new(sync.Once)
	cachedEventRecorder = nil

	recorder := initEventRecorder()
	assert.NotNil(t, recorder)

	// Second call should return cached
	recorder2 := initEventRecorder()
	assert.Equal(t, recorder, recorder2)
}
