/*
 Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dell/gofsutil"
	csi "github.com/container-storage-interface/spec/lib/go/csi"
)

func Test_singleAccessMode(t *testing.T) {
	type args struct {
		accMode *csi.VolumeCapability_AccessMode
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "volume capability mode is single-access node writer",
			args: args{
				accMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			},
			want: true,
		},
		{
			name: "volume capability mode is single-access node reader-only",
			args: args{
				accMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY},
			},
			want: true,
		},
		{
			name: "volume capability mode is multi-access node writer",
			args: args{
				accMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := singleAccessMode(tt.args.accMode); got != tt.want {
				t.Errorf("singleAccessMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func setupUnmountVolumeTest(t *testing.T) (string, string, string) {
	t.Helper()
	gofsutil.UseMockFS()
	gofsutil.GOFSMock.InduceGetMountsError = false
	gofsutil.GOFSMock.InduceUnmountError = false
	gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)

	t.Cleanup(func() {
		gofsutil.GOFSMock.InduceGetMountsError = false
		gofsutil.GOFSMock.InduceUnmountError = false
		gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)
	})

	tmpDir := t.TempDir()
	devicePath := filepath.Join(tmpDir, "dev")
	if err := os.WriteFile(devicePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("failed to create mock device file: %v", err)
	}

	realDev, err := filepath.EvalSymlinks(devicePath)
	if err != nil {
		t.Fatalf("failed to resolve device path: %v", err)
	}

	privDir := filepath.Join(tmpDir, "private")
	if err := os.MkdirAll(privDir, 0o750); err != nil {
		t.Fatalf("failed to create private mount dir: %v", err)
	}

	oldEmulateBlockDevice := unitTestEmulateBlockDevice
	unitTestEmulateBlockDevice = true
	t.Cleanup(func() {
		unitTestEmulateBlockDevice = oldEmulateBlockDevice
	})

	return devicePath, realDev, privDir
}

func getMockMountPaths() map[string]bool {
	paths := map[string]bool{}
	for _, m := range gofsutil.GOFSMockMounts {
		paths[m.Path] = true
	}
	return paths
}

func TestIsRequestedTargetMountVariant(t *testing.T) {
	target := "/var/lib/kubelet/pods/pod-2/volumes/kubernetes.io~csi/pvc-123/mount"
	dataVariant := strings.Replace(target, "/var/lib/kubelet", "/data/kubelet", 1)
	otherPod := "/var/lib/kubelet/pods/pod-1/volumes/kubernetes.io~csi/pvc-123/mount"

	tests := []struct {
		name   string
		path   string
		target string
		want   bool
	}{
		{
			name:   "exact target path",
			path:   target,
			target: target,
			want:   true,
		},
		{
			name:   "noderoot target path",
			path:   "/noderoot" + target,
			target: target,
			want:   true,
		},
		{
			name:   "bind mount variant for same pod",
			path:   dataVariant,
			target: target,
			want:   true,
		},
		{
			name:   "different pod path should not match",
			path:   otherPod,
			target: target,
			want:   false,
		},
		{
			name:   "non-pod custom path only matches exact target",
			path:   "/tmp/target-a",
			target: "/tmp/target-b",
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRequestedTargetMountVariant(tt.path, tt.target); got != tt.want {
				t.Fatalf("isRequestedTargetMountVariant(%s, %s) = %v, want %v", tt.path, tt.target, got, tt.want)
			}
		})
	}
}

func TestIsPrivateMountVariant(t *testing.T) {
	privTgt := "/var/lib/kubelet/plugins/powermax.emc.dell.com/disks/csi-vol-abc123"
	bindVariant := strings.Replace(privTgt, "/var/lib/kubelet", "/data/kubelet", 1)

	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "exact private mount path",
			path: privTgt,
			want: true,
		},
		{
			name: "noderoot private mount path",
			path: "/noderoot" + privTgt,
			want: true,
		},
		{
			name: "bind mount private path",
			path: bindVariant,
			want: true,
		},
		{
			name: "different volume private path should not match",
			path: "/var/lib/kubelet/plugins/powermax.emc.dell.com/disks/csi-vol-abc123-backup",
			want: false,
		},
		{
			name: "pod consumer mount should not match",
			path: "/var/lib/kubelet/pods/pod-1/volumes/kubernetes.io~csi/pvc-123/mount",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isPrivateMountVariant(tt.path, privTgt); got != tt.want {
				t.Fatalf("isPrivateMountVariant(%s, %s) = %v, want %v", tt.path, privTgt, got, tt.want)
			}
		})
	}
}

func TestUnmountPrivMountSkipsWhenConsumerMountExists(t *testing.T) {
	devicePath, realDev, privDir := setupUnmountVolumeTest(t)

	volumeID := "pvc-123"
	privTgt := getPrivateMountPoint(privDir, volumeID)
	consumerMount := "/var/lib/kubelet/pods/pod-1/volumes/kubernetes.io~csi/pvc-123/mount"

	gofsutil.GOFSMockMounts = []gofsutil.Info{
		{Device: realDev, Source: realDev, Path: privTgt},
		{Device: realDev, Source: privTgt, Path: consumerMount},
	}

	dev := &Device{FullPath: devicePath, RealDev: realDev}
	lastUnmounted, err := unmountPrivMount(context.Background(), dev, privTgt)
	if err != nil {
		t.Fatalf("unmountPrivMount returned error: %v", err)
	}
	if lastUnmounted {
		t.Fatalf("expected lastUnmounted=false when consumer mount is still present")
	}

	paths := getMockMountPaths()
	if !paths[privTgt] || !paths[consumerMount] {
		t.Fatalf("expected mounts to remain untouched when consumer mount exists, got %#v", gofsutil.GOFSMockMounts)
	}
}

func TestUnpublishVolumeUnmountsOnlyRequestedTargetVariants(t *testing.T) {
	devicePath, realDev, privDir := setupUnmountVolumeTest(t)

	volumeID := "pvc-123"
	privTgt := getPrivateMountPoint(privDir, volumeID)

	targetPod2 := "/var/lib/kubelet/pods/pod-2/volumes/kubernetes.io~csi/pvc-123/mount"
	targetPod2Data := strings.Replace(targetPod2, "/var/lib/kubelet", "/data/kubelet", 1)
	targetPod1 := "/var/lib/kubelet/pods/pod-1/volumes/kubernetes.io~csi/pvc-123/mount"
	targetPod1Data := strings.Replace(targetPod1, "/var/lib/kubelet", "/data/kubelet", 1)

	gofsutil.GOFSMockMounts = []gofsutil.Info{
		{Device: realDev, Source: realDev, Path: privTgt},
		{Device: realDev, Source: realDev, Path: "/noderoot" + privTgt},
		{Device: realDev, Source: privTgt, Path: targetPod2},
		{Device: realDev, Source: privTgt, Path: "/noderoot" + targetPod2},
		{Device: realDev, Source: privTgt, Path: targetPod2Data},
		{Device: realDev, Source: privTgt, Path: "/noderoot" + targetPod2Data},
		{Device: realDev, Source: privTgt, Path: targetPod1},
		{Device: realDev, Source: privTgt, Path: "/noderoot" + targetPod1},
		{Device: realDev, Source: privTgt, Path: targetPod1Data},
		{Device: realDev, Source: privTgt, Path: "/noderoot" + targetPod1Data},
	}

	req := &csi.NodeUnpublishVolumeRequest{
		VolumeId:   volumeID,
		TargetPath: targetPod2,
	}

	lastUnmounted, err := unpublishVolume(req, privDir, devicePath, "req-1")
	if err != nil {
		t.Fatalf("unpublishVolume returned error: %v", err)
	}
	if lastUnmounted {
		t.Fatalf("expected lastUnmounted=false when another pod is still mounted")
	}

	paths := getMockMountPaths()
	for _, removed := range []string{targetPod2, "/noderoot" + targetPod2, targetPod2Data, "/noderoot" + targetPod2Data} {
		if paths[removed] {
			t.Fatalf("requested target variant %s should have been unmounted", removed)
		}
	}

	for _, remaining := range []string{targetPod1, "/noderoot" + targetPod1, targetPod1Data, "/noderoot" + targetPod1Data, privTgt} {
		if !paths[remaining] {
			t.Fatalf("mount %s should remain for still-running pod", remaining)
		}
	}
}

func TestUnpublishVolumeLastUnmountedTrueWhenAllPodMountsGone(t *testing.T) {
	devicePath, realDev, privDir := setupUnmountVolumeTest(t)

	volumeID := "pvc-123"
	privTgt := getPrivateMountPoint(privDir, volumeID)
	target := "/var/lib/kubelet/pods/pod-2/volumes/kubernetes.io~csi/pvc-123/mount"
	targetData := strings.Replace(target, "/var/lib/kubelet", "/data/kubelet", 1)

	gofsutil.GOFSMockMounts = []gofsutil.Info{
		{Device: realDev, Source: realDev, Path: privTgt},
		{Device: realDev, Source: realDev, Path: "/noderoot" + privTgt},
		{Device: realDev, Source: privTgt, Path: target},
		{Device: realDev, Source: privTgt, Path: "/noderoot" + target},
		{Device: realDev, Source: privTgt, Path: targetData},
		{Device: realDev, Source: privTgt, Path: "/noderoot" + targetData},
	}

	req := &csi.NodeUnpublishVolumeRequest{
		VolumeId:   volumeID,
		TargetPath: target,
	}

	lastUnmounted, err := unpublishVolume(req, privDir, devicePath, "req-2")
	if err != nil {
		t.Fatalf("unpublishVolume returned error: %v", err)
	}
	if !lastUnmounted {
		t.Fatalf("expected lastUnmounted=true when no other pod mounts remain")
	}
	if len(gofsutil.GOFSMockMounts) != 0 {
		t.Fatalf("expected all mounts to be unmounted, got %#v", gofsutil.GOFSMockMounts)
	}
}

func Test_validateVolumeCapability(t *testing.T) {
	type args struct {
		volCap *csi.VolumeCapability
	}
	tests := []struct {
		name           string
		args           args
		isBlock        bool
		wantMount      *csi.VolumeCapability_MountVolume
		wantAccessMode *csi.VolumeCapability_AccessMode
		wantAccessFlag string
		wantErr        bool
	}{
		{
			name: "volume is a block, multi-node reader-only",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
					},
				},
			},
			isBlock:   true,
			wantMount: nil,
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
			},
			wantAccessFlag: "ro",
			wantErr:        false,
		},
		{
			name: "volume is a block, multi-node multi-writer",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
					},
				},
			},
			isBlock:   true,
			wantMount: nil,
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
			},
			wantAccessFlag: "rw",
			wantErr:        false,
		},
		{
			name: "volume is a block, single-node writer",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
			isBlock:   true,
			wantMount: nil,
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
			wantAccessFlag: "",
			wantErr:        false,
		},
		{
			name: "volume is a mount, multi-node reader-only",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
					},
				},
			},
			isBlock:   false,
			wantMount: &csi.VolumeCapability_MountVolume{},
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
			},
			wantAccessFlag: "ro",
			wantErr:        false,
		},
		{
			name: "volume is missing access mode",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
				},
			},
			isBlock:        false,
			wantMount:      nil,
			wantAccessMode: nil,
			wantAccessFlag: "",
			wantErr:        true,
		},
		{
			name: "volume is block with unknown access mode",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
					},
				},
			},
			isBlock:   true,
			wantMount: nil,
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
			},
			wantAccessFlag: "",
			wantErr:        true,
		},
		{
			name: "volume is mount with unknown access mode",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
					},
				},
			},
			isBlock:   false,
			wantMount: &csi.VolumeCapability_MountVolume{},
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
			},
			wantAccessFlag: "",
			wantErr:        true,
		},
		{
			name: "volume is a mount, multi-node multi-writer",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
					},
				},
			},
			isBlock:   false,
			wantMount: &csi.VolumeCapability_MountVolume{},
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
			},
			wantAccessFlag: "",
			wantErr:        true,
		},
		{
			name: "volume type cannot be determined",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
					},
				},
			},
			isBlock:   false,
			wantMount: nil,
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
			},
			wantAccessFlag: "",
			wantErr:        true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIsBlock, gotMount, gotAccessMode, gotAccessFlag, err := validateVolumeCapability(tt.args.volCap, false)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateVolumeCapability() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotIsBlock != tt.isBlock {
				t.Errorf("validateVolumeCapability() gotIsBlock = %v, want %v", gotIsBlock, tt.isBlock)
			}
			if !reflect.DeepEqual(gotMount, tt.wantMount) {
				t.Errorf("validateVolumeCapability() gotMount = %v, want %v", gotMount, tt.wantMount)
			}
			if !reflect.DeepEqual(gotAccessMode, tt.wantAccessMode) {
				t.Errorf("validateVolumeCapability() gotAccessMode = %v, want %v", gotAccessMode, tt.wantAccessMode)
			}
			if gotAccessFlag != tt.wantAccessFlag {
				t.Errorf("validateVolumeCapability() gotAccessFlag = %v, want %v", gotAccessFlag, tt.wantAccessFlag)
			}
		})
	}
}
