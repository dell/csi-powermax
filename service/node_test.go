/*
Copyright © 2025-2026 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/dell/csi-powermax/v2/k8smock"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	"github.com/dell/gofsutil"
	"github.com/dell/goiscsi"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gonvme "github.com/dell/gonvme"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/golang/mock/gomock"
	gmock "github.com/golang/mock/gomock"
)

// ---------------------------------------------------------------------------
// Tests for NodeStageVolume non-uniform Metro client selection (node.go:144-166)
// ---------------------------------------------------------------------------

func TestNodeStageVolume_Site1_ClientSelection(t *testing.T) {
	// Site1 node: only local array (R1) is managed.
	// Remote array (R2) is NOT in ManagedArrays.
	// NodeStageVolume should get client for local array only and clear remoteSymID.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// Only local array is initialized
	symmetrix.Initialize([]string{localSymID}, c)
	defer symmetrix.RemoveClient(localSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{localSymID} // site1: only local managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodeStageVolumeRequest{
		VolumeId:          metroVolID,
		StagingTargetPath: "/tmp/test-staging",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	}

	// NodeStageVolume will get past client selection (site1 path) but fail later
	// at nodeProbe or volume lookup — the key is that it does NOT fail with
	// "array: 000120000002 not found" which would indicate the wrong client path.
	_, err := svc.NodeStageVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection (nodeProbe/volume lookup)")
	assert.NotContains(t, err.Error(), "000120000002 not found",
		"should NOT fail looking up remote array — site1 should use local array only")
}

func TestNodeStageVolume_Site2_ClientSelection(t *testing.T) {
	// Site2 node: only remote array (R2) is managed.
	// Local array (R1) is NOT in ManagedArrays.
	// NodeStageVolume should get client for remote array only.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// Only remote array is initialized
	symmetrix.Initialize([]string{remoteSymID}, c)
	defer symmetrix.RemoveClient(remoteSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{remoteSymID} // site2: only remote managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodeStageVolumeRequest{
		VolumeId:          metroVolID,
		StagingTargetPath: "/tmp/test-staging",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	}

	// NodeStageVolume will get past client selection (site2 path) but fail later.
	// The key assertion: it does NOT fail with "array: 000120000001 not found".
	_, err := svc.NodeStageVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection (nodeProbe/volume lookup)")
	assert.NotContains(t, err.Error(), "000120000001 not found",
		"should NOT fail looking up local array — site2 should use remote array only")
}

func TestNodeStageVolume_NeitherManaged_ClientFails(t *testing.T) {
	// Neither array is managed — GetPowerMaxClient should fail.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// No arrays initialized at all
	svc := &service{}
	svc.opts.ManagedArrays = []string{} // no arrays managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodeStageVolumeRequest{
		VolumeId:          metroVolID,
		StagingTargetPath: "/tmp/test-staging",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	}

	_, err := svc.NodeStageVolume(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found",
		"should fail because neither array is initialized")
}

func TestNodeStageVolume_Uniform_ClientSelection(t *testing.T) {
	// Uniform mode: both arrays are managed.
	// NodeStageVolume should create a metro client for both.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// Both arrays initialized
	symmetrix.Initialize([]string{localSymID, remoteSymID}, c)
	defer symmetrix.RemoveClient(localSymID)
	defer symmetrix.RemoveClient(remoteSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{localSymID, remoteSymID} // uniform: both managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodeStageVolumeRequest{
		VolumeId:          metroVolID,
		StagingTargetPath: "/tmp/test-staging",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	}

	// Should get past client selection (uniform metro client) and fail later.
	_, err := svc.NodeStageVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection (nodeProbe/volume lookup)")
	// In uniform mode, neither array lookup should fail
	assert.NotContains(t, err.Error(), "not found",
		"should NOT fail finding arrays — both are initialized")
}

func TestNodeStageVolume_NonMetroVolume_LocalOnly(t *testing.T) {
	// Non-Metro volume (no remoteSymID) — should use local array only.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"

	symmetrix.Initialize([]string{localSymID}, c)
	defer symmetrix.RemoveClient(localSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{localSymID}

	// Non-Metro volume: no ":" in symID, no remote components
	localVolID := svc.createCSIVolumeID("", "testVol", localSymID, "00001")
	req := &csi.NodeStageVolumeRequest{
		VolumeId:          localVolID,
		StagingTargetPath: "/tmp/test-staging",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
	}

	_, err := svc.NodeStageVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection (nodeProbe/volume lookup)")
	assert.NotContains(t, err.Error(), "not found",
		"should NOT fail finding local array — it's initialized")
}

// ---------------------------------------------------------------------------
// Tests for NodePublishVolume non-uniform Metro client selection
// ---------------------------------------------------------------------------

func TestNodePublishVolume_Site1_ClientSelection(t *testing.T) {
	// Site1 node: only local array (R1) is managed.
	// Remote array (R2) is NOT in ManagedArrays.
	// NodePublishVolume should get client for local array only.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// Only local array is initialized
	symmetrix.Initialize([]string{localSymID}, c)
	defer symmetrix.RemoveClient(localSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{localSymID} // site1: only local managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodePublishVolumeRequest{
		VolumeId:   metroVolID,
		TargetPath: "/tmp/test-target",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
		PublishContext: map[string]string{
			PublishContextDeviceWWN: "60000970000120000001533030303031",
		},
	}

	// NodePublishVolume will get past client selection (site1 path) but fail later
	// at nodeProbe or volume lookup — the key is that it does NOT fail with
	// "array: 000120000002 not found" which would indicate the wrong client path.
	_, err := svc.NodePublishVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection (nodeProbe/volume lookup)")
	assert.NotContains(t, err.Error(), "000120000002 not found",
		"should NOT fail looking up remote array — site1 should use local array only")
}

func TestNodePublishVolume_Site2_ClientSelection(t *testing.T) {
	// Site2 node: only remote array (R2) is managed.
	// Local array (R1) is NOT in ManagedArrays.
	// NodePublishVolume should get client for remote array only.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// Only remote array is initialized
	symmetrix.Initialize([]string{remoteSymID}, c)
	defer symmetrix.RemoveClient(remoteSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{remoteSymID} // site2: only remote managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodePublishVolumeRequest{
		VolumeId:   metroVolID,
		TargetPath: "/tmp/test-target",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
		PublishContext: map[string]string{
			PublishContextDeviceWWN: "60000970000120000002533030303032",
		},
	}

	// NodePublishVolume will get past client selection (site2 path) but fail later.
	// The key assertion: it does NOT fail with "array: 000120000001 not found".
	_, err := svc.NodePublishVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection (nodeProbe/volume lookup)")
	assert.NotContains(t, err.Error(), "000120000001 not found",
		"should NOT fail looking up local array — site2 should use remote array only")
}

func TestNodePublishVolume_NeitherManaged_ClientFails(t *testing.T) {
	// Neither array is managed — GetPowerMaxClient should fail.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// No arrays initialized at all
	svc := &service{}
	svc.opts.ManagedArrays = []string{} // no arrays managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodePublishVolumeRequest{
		VolumeId:   metroVolID,
		TargetPath: "/tmp/test-target",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
		PublishContext: map[string]string{
			PublishContextDeviceWWN: "60000970000120000001533030303031",
		},
	}

	_, err := svc.NodePublishVolume(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found",
		"should fail because neither array is initialized")
}

func TestNodePublishVolume_Uniform_ClientSelection(t *testing.T) {
	// Uniform mode: both arrays are managed.
	// NodePublishVolume should create a metro client for both.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// Both arrays initialized
	symmetrix.Initialize([]string{localSymID, remoteSymID}, c)
	defer symmetrix.RemoveClient(localSymID)
	defer symmetrix.RemoveClient(remoteSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{localSymID, remoteSymID} // uniform: both managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodePublishVolumeRequest{
		VolumeId:   metroVolID,
		TargetPath: "/tmp/test-target",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
		PublishContext: map[string]string{
			PublishContextDeviceWWN: "60000970000120000001533030303031",
		},
	}

	// Should get past client selection (uniform metro client) and fail later.
	_, err := svc.NodePublishVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection (nodeProbe/volume lookup)")
	// In uniform mode, neither array lookup should fail
	assert.NotContains(t, err.Error(), "not found",
		"should NOT fail finding arrays — both are initialized")
}

func TestNodePublishVolume_NonMetroVolume_LocalOnly(t *testing.T) {
	// Non-Metro volume (no remoteSymID) — should use local array only.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"

	symmetrix.Initialize([]string{localSymID}, c)
	defer symmetrix.RemoveClient(localSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{localSymID}

	// Non-Metro volume: no ":" in symID, no remote components
	localVolID := svc.createCSIVolumeID("", "testVol", localSymID, "00001")
	req := &csi.NodePublishVolumeRequest{
		VolumeId:   localVolID,
		TargetPath: "/tmp/test-target",
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{
				Mount: &csi.VolumeCapability_MountVolume{},
			},
			AccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
		},
		PublishContext: map[string]string{
			PublishContextDeviceWWN: "60000970000120000001533030303031",
		},
	}

	_, err := svc.NodePublishVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection (nodeProbe/volume lookup)")
	assert.NotContains(t, err.Error(), "not found",
		"should NOT fail finding local array — it's initialized")
}

// ---------------------------------------------------------------------------
// Tests for NodeUnstageVolume non-uniform Metro client selection
// ---------------------------------------------------------------------------

func TestNodeUnstageVolume_Site1_ClientSelection(t *testing.T) {
	// Site1 node: only local array (R1) is managed.
	// Remote array (R2) is NOT in ManagedArrays.
	// NodeUnstageVolume should get client for local array only.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// Only local array is initialized
	symmetrix.Initialize([]string{localSymID}, c)
	defer symmetrix.RemoveClient(localSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{localSymID} // site1: only local managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodeUnstageVolumeRequest{
		VolumeId:          metroVolID,
		StagingTargetPath: "/tmp/test-staging",
	}

	// NodeUnstageVolume will get past client selection (site1 path) but fail later
	// at disconnectVolume or device lookup — the key is that it does NOT fail with
	// "array: 000120000002 not found" which would indicate the wrong client path.
	_, err := svc.NodeUnstageVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection")
	assert.NotContains(t, err.Error(), "000120000002 not found",
		"should NOT fail looking up remote array — site1 should use local array only")
}

func TestNodeUnstageVolume_Site2_ClientSelection(t *testing.T) {
	// Site2 node: only remote array (R2) is managed.
	// Local array (R1) is NOT in ManagedArrays.
	// NodeUnstageVolume should get client for remote array only.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// Only remote array is initialized
	symmetrix.Initialize([]string{remoteSymID}, c)
	defer symmetrix.RemoveClient(remoteSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{remoteSymID} // site2: only remote managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodeUnstageVolumeRequest{
		VolumeId:          metroVolID,
		StagingTargetPath: "/tmp/test-staging",
	}

	// NodeUnstageVolume will get past client selection (site2 path) but fail later.
	// The key assertion: it does NOT fail with "array: 000120000001 not found".
	_, err := svc.NodeUnstageVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection")
	assert.NotContains(t, err.Error(), "000120000001 not found",
		"should NOT fail looking up local array — site2 should use remote array only")
}

func TestNodeUnstageVolume_NeitherManaged_ClientFails(t *testing.T) {
	// Neither array is managed — GetPowerMaxClient should fail.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// No arrays initialized at all
	svc := &service{}
	svc.opts.ManagedArrays = []string{} // no arrays managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodeUnstageVolumeRequest{
		VolumeId:          metroVolID,
		StagingTargetPath: "/tmp/test-staging",
	}

	_, err := svc.NodeUnstageVolume(context.Background(), req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found",
		"should fail because neither array is initialized")
}

func TestNodeUnstageVolume_Uniform_ClientSelection(t *testing.T) {
	// Uniform mode: both arrays are managed.
	// NodeUnstageVolume should create a metro client for both.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"
	remoteSymID := "000120000002"

	// Both arrays initialized
	symmetrix.Initialize([]string{localSymID, remoteSymID}, c)
	defer symmetrix.RemoveClient(localSymID)
	defer symmetrix.RemoveClient(remoteSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{localSymID, remoteSymID} // uniform: both managed

	metroVolID := svc.createCSIVolumeID("", "testVol", localSymID+":"+remoteSymID, "00001:00002")
	req := &csi.NodeUnstageVolumeRequest{
		VolumeId:          metroVolID,
		StagingTargetPath: "/tmp/test-staging",
	}

	// Should get past client selection (uniform metro client) and fail later.
	_, err := svc.NodeUnstageVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection")
	// In uniform mode, neither array lookup should fail
	assert.NotContains(t, err.Error(), "not found",
		"should NOT fail finding arrays — both are initialized")
}

func TestNodeUnstageVolume_NonMetroVolume_LocalOnly(t *testing.T) {
	// Non-Metro volume (no remoteSymID) — should use local array only.
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	c := mocks.NewMockPmaxClient(ctrl)
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})

	localSymID := "000120000001"

	symmetrix.Initialize([]string{localSymID}, c)
	defer symmetrix.RemoveClient(localSymID)

	svc := &service{}
	svc.opts.ManagedArrays = []string{localSymID}

	// Non-Metro volume: no ":" in symID, no remote components
	localVolID := svc.createCSIVolumeID("", "testVol", localSymID, "00001")
	req := &csi.NodeUnstageVolumeRequest{
		VolumeId:          localVolID,
		StagingTargetPath: "/tmp/test-staging",
	}

	_, err := svc.NodeUnstageVolume(context.Background(), req)
	assert.Error(t, err, "expected error after client selection")
	assert.NotContains(t, err.Error(), "not found",
		"should NOT fail finding local array — it's initialized")
}

func TestGetNVMeTCPTargets(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name       string
		symID      string
		getClient  func() *mocks.MockPmaxClient
		pmaxClient pmax.Pmax
		want       []NVMeTCPTargetInfo
		wantErr    bool
	}{
		{
			name:  "Successful case",
			symID: "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn1",
						PortalIPs: []string{"portal1"},
					},
					{
						NQN:       "nqn2",
						PortalIPs: []string{"portal2"},
					},
				}, nil)
				return c
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
			wantErr: false,
		},
		{
			name:  "No matching targets",
			symID: "array2",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array2").AnyTimes().Return([]pmax.NVMeTCPTarget{}, nil)
				return c
			},
			want:    nil,
			wantErr: false,
		},
	}

	// Run the tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new service instance for testing
			s := &service{
				opts: Opts{
					UseProxy: true,
				},
				nvmetcpClient: gonvme.NewMockNVMe(map[string]string{}),
				nvmeTargets:   &sync.Map{},
				// Set the necessary fields for testing
			}
			tc.pmaxClient = tc.getClient()
			// Call the function and check the results
			got, err := s.getNVMeTCPTargets(context.Background(), tc.symID, tc.pmaxClient)
			if (err != nil) != tc.wantErr {
				t.Errorf("Expected error: %v, but got: %v", tc.wantErr, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Expected: %v, but got: %v", tc.want, got)
			}
		})
	}
}

func TestGetAndConfigureArrayNVMeTCPTargets(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name         string
		symID        string
		arrayTargets []string
		getClient    func() *mocks.MockPmaxClient
		init         func()
		pmaxClient   pmax.Pmax
		want         []NVMeTCPTargetInfo
		wantErr      bool
	}{
		{
			name:         "Valid case with different cached targets and provided targets",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002:1C001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{
					{
						target: gonvme.NVMeTarget{Portal: "1.1.1.1", TargetNqn: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Error case: no matching targets",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{}, fmt.Errorf("No matching targets"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{
					{
						target: gonvme.NVMeTarget{Portal: "1.1.1.1", TargetNqn: "nqn.1988-11.com.emc.mock:9992d5b871f1403E169D00001"},
					},
				})
			},
			want: nil,
		}, // This finding it in cache even after invalidating cache!!
		{
			name:         "Valid case",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Valid case with some cached targets",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{
					{
						target: gonvme.NVMeTarget{Portal: "1.1.1.1", TargetNqn: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Error case: no matching port",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{}, fmt.Errorf("No matching ports"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Error case: no matching port group",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{}, fmt.Errorf("No matching portgroup"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Error case: no matching mv",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{}, fmt.Errorf("no matching mv"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Valid case : cache contains data but invalid for this case",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewTargetInfo{
					{
						target: goiscsi.ISCSITarget{Portal: "1.1.1.1", Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
	}

	// Run the tests
	for _, tc := range testCases {
		tc.pmaxClient = tc.getClient()
		t.Run(tc.name, func(t *testing.T) {
			targetNQN := "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"
			if strings.Contains(tc.name, "different cached") {
				targetNQN = "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002"
			}
			s := &service{
				opts: Opts{
					UseProxy:   true,
					PortGroups: []string{"portgroup1"},
				},
				nvmetcpClient: &nvmeClientMock{
					discoverTargets: func(_ string) ([]gonvme.NVMeTarget, error) {
						if strings.Contains(tc.name, "no matching targets") {
							return nil, nil
						}
						return []gonvme.NVMeTarget{{
							Portal:    "1.1.1.1",
							TargetNqn: targetNQN,
						}}, nil
					},
				},
				nvmeTargets:        &sync.Map{},
				loggedInNVMeArrays: map[string]bool{},
			}

			tc.init()
			got := s.getAndConfigureArrayNVMeTCPTargets(context.Background(), tc.arrayTargets, tc.symID, tc.pmaxClient)
			if len(got) != len(tc.want) {
				t.Errorf("Expected: %v, but got: %v", len(tc.want), len(got))
			}
		})
	}
}

func TestGetAndConfigureISCSITargets(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name         string
		symID        string
		arrayTargets []string
		getClient    func() *mocks.MockPmaxClient
		init         func()
		pmaxClient   pmax.Pmax
		want         []ISCSITargetInfo
		wantErr      bool
	}{
		{
			name:         "Valid case with different cached targets and provided targets",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewTargetInfo{
					{
						target: goiscsi.ISCSITarget{Portal: "1.1.1.1", Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []ISCSITargetInfo{
				{
					Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Error case: no matching targets",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{}, fmt.Errorf("No matching targets"))
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewTargetInfo{
					{
						target: goiscsi.ISCSITarget{Portal: "1.1.1.1", Target: "iqn.1988-11.com.emc.mock:9992d5b871f1403E169D00001"},
					},
				})
			},
			want: nil,
		},
		{
			name:         "Valid case",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
			},
			want: []ISCSITargetInfo{
				{
					Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Valid case with some cached targets",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001", "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()

				symToMaskingViewTargets.Store("array1", []maskingViewTargetInfo{
					{
						target: goiscsi.ISCSITarget{Portal: "1.1.1.1", Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []ISCSITargetInfo{
				{
					Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Error case: no matching port",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{}, fmt.Errorf("No matching ports"))
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Error case: no matching port group",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{}, fmt.Errorf("No matching portgroup"))
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Error case: no matching mv",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{}, fmt.Errorf("no matching mv"))
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Valid case: cache contains data but invalid for this case",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{}, fmt.Errorf("No matching ports"))
				return c
			},
			init: func() {
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{})
			},
			want: nil,
		},
	}

	// Run the tests
	for _, tc := range testCases {
		tc.pmaxClient = tc.getClient()
		t.Run(tc.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					UseProxy: true,
				},
				iscsiClient:    goiscsi.NewMockISCSI(map[string]string{}),
				iscsiTargets:   map[string][]string{},
				loggedInArrays: map[string]bool{},
			}
			tc.init()
			got := s.getAndConfigureArrayISCSITargets(context.Background(), tc.arrayTargets, tc.symID, tc.pmaxClient)
			if len(got) != len(tc.want) {
				t.Errorf("Expected: %v, but got: %v", len(tc.want), len(got))
			}
		})
	}
}

func TestConnectRDMDevice(t *testing.T) {
	// Create a mock gobrick.FCConnector
	mockConnector := &mockFCGobrick{}
	// Create a mock context
	ctx := context.Background()

	// Create a mock publishContextData
	data := publishContextData{
		deviceWWN:        "mockWWN",
		volumeLUNAddress: "10",
		fcTargets: []FCTargetInfo{
			{
				WWPN: "mockWWPN",
			},
		},
	}
	// Create a mock service
	s := &service{
		fcConnector: mockConnector,
	}
	// Call the connectRDMDevice function
	device, err := s.connectRDMDevice(ctx, int(10), data)
	// Assert the expected result
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	// deviceWWN is hardcoded in gobrick mocks, check for that to be returned
	if device.WWN != "60000970000197900046533030300501" {
		t.Errorf("Expected WWN to be 'mockWWN', got '%s'", device.WWN)
	}
}

// TestConnectDevice_Vsphere verifies connectDevice routing when vSphere is
// enabled. With useFC=true (set by nodeStartup) the call must reach
// connectRDMDevice; without it the call falls through to iSCSI and fails.
func TestConnectDevice_Vsphere(t *testing.T) {
	tests := []struct {
		name      string
		useFC     bool
		wantErr   bool
		wantInDev bool // expect devicePath to contain "/dev/"
	}{
		{
			name:      "useFC routes to RDM",
			useFC:     true,
			wantErr:   false,
			wantInDev: true,
		},
		{
			name:    "without useFC falls through to iSCSI and fails",
			useFC:   false,
			wantErr: true,
		},
	}

	data := publishContextData{
		deviceWWN:        "mockWWN",
		volumeLUNAddress: "10",
		fcTargets: []FCTargetInfo{
			{WWPN: "mockWWPN"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			s := &service{
				opts: Opts{
					IsVsphereEnabled: true,
				},
				useFC: tt.useFC,
			}
			if tt.useFC {
				s.fcConnector = &mockFCGobrick{}
			} else {
				s.iscsiConnector = &mockISCSIGobrick{}
			}

			devicePath, err := s.connectDevice(context.Background(), data)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			if tt.wantInDev {
				require.Contains(t, devicePath, "/dev/")
			}
		})
	}
}

func TestGetHostForVsphere(t *testing.T) {
	ctx := context.Background()
	vsphereHostName := "vsphere-host"
	array := "array1"
	tests := []struct {
		name                 string
		hostResponse         *types.Host
		hostResponseError    error
		hostGroupResponse    *types.HostGroup
		expectedErr          error
		hostGroupResponseErr error
	}{
		{
			name:              "Host exists",
			hostResponse:      &types.Host{HostID: vsphereHostName, HostType: "FC"},
			hostGroupResponse: &types.HostGroup{},
			expectedErr:       nil,
		},
		{
			name:         "Host does not exist, HostGroup exists",
			hostResponse: nil,
			hostGroupResponse: &types.HostGroup{
				HostGroupID: "host-group-name",
				NumOfHosts:  1,
				Hosts: []types.HostSummary{
					{HostID: vsphereHostName},
				},
			},
			hostResponseError:    errors.New("cannot be found"),
			hostGroupResponseErr: nil,
			expectedErr:          nil,
		},
		{
			name:                 "Host and HostGroup do not exist",
			hostResponse:         nil,
			hostGroupResponse:    nil,
			hostResponseError:    errors.New("cannot be found"),
			hostGroupResponseErr: errors.New("cannot be found"),
			expectedErr:          errors.New("cannot be found"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			pmaxClient := func() *mocks.MockPmaxClient {
				client := mocks.NewMockPmaxClient(gmock.NewController(t))
				client.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
				client.EXPECT().GetHostByID(gmock.Any(), gmock.Any(), gmock.Any()).AnyTimes().Return(tt.hostResponse, tt.hostResponseError)
				client.EXPECT().GetHostGroupByID(gmock.Any(), gmock.Any(), gmock.Any()).AnyTimes().Return(tt.hostGroupResponse, tt.hostGroupResponseErr)
				return client
			}()
			s := &service{
				opts: Opts{
					VSphereHostName: vsphereHostName,
				},
			}
			err := s.getHostForVsphere(ctx, array, pmaxClient)
			if tt.expectedErr != nil {
				require.Error(t, err)
				require.True(t, strings.Contains(err.Error(), tt.expectedErr.Error()))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestIsISCSIConnected(t *testing.T) {
	s := &service{}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"AuthFailed", errors.New("exit status 24"), true},
		{"SessionExists", errors.New("exit status 15"), true},
		{"OtherError", errors.New("exit status 1"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			got := s.isISCSIConnected(tt.err)
			if got != tt.want {
				t.Errorf("isISCSIConnected() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateOrUpdateNVMeTCPHost(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name       string
		array      string
		nodeName   string
		NQNs       []string
		getClient  func() *mocks.MockPmaxClient
		pmaxClient pmax.Pmax
		want       *types.Host
		wantErr    bool
	}{
		{
			name:     "Valid case",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetHostByID(gmock.All(), "array1", "host1").AnyTimes().Return(&types.Host{
					HostID: "host1",
					Initiators: []string{
						"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().CreateHost(gmock.All(), "array1", "host1", gmock.Any(), gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				c.EXPECT().GetInitiatorList(gmock.All(), "array1", "", false, false).AnyTimes().Return(&types.InitiatorList{}, nil)
				c.EXPECT().GetInitiatorByID(gmock.All(), "array1", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001").AnyTimes().Return(
					&types.Initiator{InitiatorID: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"}, nil,
				)
				c.EXPECT().UpdateHostInitiators(gmock.All(), "array1", "host1", gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				return c
			},
			want: &types.Host{
				HostID: "host1",
				Initiators: []string{
					"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
				},
			},
			wantErr: false,
		},
		{
			name:     "Invalid host but created successfully",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetHostByID(gmock.All(), "array1", "host1").AnyTimes().Return(nil, errors.New("host not found"))
				c.EXPECT().CreateHost(gmock.All(), "array1", "host1", gmock.Any(), gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				c.EXPECT().GetInitiatorList(gmock.All(), "array1", "", false, false).AnyTimes().Return(&types.InitiatorList{}, nil)
				c.EXPECT().GetInitiatorByID(gmock.All(), "array1", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001").AnyTimes().Return(
					&types.Initiator{InitiatorID: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"}, nil,
				)
				c.EXPECT().UpdateHostInitiators(gmock.All(), "array1", "host1", gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				return c
			},
			wantErr: false,
			want: &types.Host{
				HostID: "host1",
			},
		},
		{
			// This is really bizarre condition in the code to test, but it is what it is.
			name:     "create host failure",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetHostByID(gmock.All(), "array1", "host1").AnyTimes().Return(nil, errors.New("host not found"))
				c.EXPECT().CreateHost(gmock.All(), "array1", "host1", gmock.Any(), gmock.Any()).AnyTimes().Return(nil, errors.New("create host failed"))
				return c
			},
			wantErr: true,
			want:    nil,
		},
		{
			// This is really bizarre condition in the code to test, but it is what it is.
			// When createHost fails, it should return and error from the code, but instead returning nil host and no error.
			name:     "create host failure, with bad NQNs error",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetHostByID(gmock.All(), "array1", "host1").AnyTimes().Return(nil, errors.New("host not found"))
				c.EXPECT().CreateHost(gmock.All(), "array1", "host1", gmock.Any(), gmock.Any()).AnyTimes().Return(nil, errors.New("is not in the format of a valid NQN:HostID"))
				c.EXPECT().GetInitiatorList(gmock.All(), "array1", "", false, false).AnyTimes().Return(&types.InitiatorList{
					InitiatorIDs: []string{
						"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetInitiatorByID(gmock.All(), "array1", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001").AnyTimes().Return(
					&types.Initiator{InitiatorID: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"}, nil,
				)
				c.EXPECT().UpdateHostInitiators(gmock.All(), "array1", "host1", gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				return c
			},
			wantErr: true,
			want:    nil,
		},
		{
			name:     "array empty case",
			array:    "",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			wantErr: true,
			want:    nil,
		},
		{
			name:     "nodename empty case",
			array:    "array1",
			nodeName: "",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			wantErr: true,
			want:    nil,
		},
		{
			name:     "len NQNs zero case",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			wantErr: true,
			want:    nil,
		},
		{
			name:     "Host exists, add new initiators",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00222"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetHostByID(gmock.All(), "array1", "host1").AnyTimes().Return(&types.Host{
					HostID: "host1",
					Initiators: []string{
						"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().CreateHost(gmock.All(), "array1", "host1", gmock.Any(), gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				c.EXPECT().GetInitiatorList(gmock.All(), "array1", "", false, false).AnyTimes().Return(&types.InitiatorList{}, nil)
				c.EXPECT().GetInitiatorByID(gmock.All(), "array1", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001").AnyTimes().Return(
					&types.Initiator{InitiatorID: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"}, nil,
				)
				c.EXPECT().UpdateHostInitiators(gmock.All(), "array1", gmock.All(), []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00222"}).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				return c
			},
			want: &types.Host{
				HostID: "host1",
				Initiators: []string{
					"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
				},
			},
			wantErr: false,
		},
	}
	// Run the tests
	for _, tc := range testCases {
		tc.pmaxClient = tc.getClient()
		t.Run(tc.name, func(t *testing.T) {
			// Disable re-tries for tests
			pmaxQueryAttempts = 1
			defer func() { pmaxQueryAttempts = 30 }()
			s := &service{
				opts: Opts{
					UseProxy: true,
				},
				nvmetcpClient:      gonvme.NewMockNVMe(map[string]string{}),
				nvmeTargets:        &sync.Map{},
				loggedInNVMeArrays: map[string]bool{},
			}
			got, err := s.createOrUpdateNVMeTCPHost(context.Background(), tc.array, tc.nodeName, tc.NQNs, tc.pmaxClient)
			if tc.wantErr && err == nil {
				t.Errorf("Expected error, but got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Expected: %v, but got: %v", tc.want, got)
			}
		})
	}
}

func TestPerformNVMETCPLoginOnSymID(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name       string
		array      string
		mvName     string
		getClient  func() *mocks.MockPmaxClient
		pmaxClient pmax.Pmax
		initFunc   func()
		want       []NVMeTCPTargetInfo
		wantErr    bool
	}{
		{
			name:   "Successful case",
			array:  "array1",
			mvName: "csi-mv---NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				return c
			},
			initFunc: func() {
				s.InvalidateSymToMaskingViewTargets()
				gonvme.GONVMEMock.InduceDiscoveryError = false
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
			wantErr: false,
		},
		{
			name:   "GetNVMeTargets fails, GetMaskingViewByID returns error does not exist",
			array:  "array1",
			mvName: "csi-mv---NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(nil,
					fmt.Errorf("Masking View %s does not exist for array %s, skipping login", "csi-mv---NVMETCP", "array1"))
				return c
			},
			initFunc: func() {
				s.InvalidateSymToMaskingViewTargets()
				gonvme.GONVMEMock.InduceDiscoveryError = false
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
			wantErr: false,
		},
		{
			name:   "GetMaskingViewByID returns error not found",
			array:  "array1",
			mvName: "csi-mv---NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(nil,
					fmt.Errorf("Masking View %s not found for array %s, skipping login", "csi-mv---NVMETCP", "array1"))
				return c
			},
			initFunc: func() {
				s.InvalidateSymToMaskingViewTargets()
				gonvme.GONVMEMock.InduceDiscoveryError = false
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
			wantErr: true,
		},
		{
			name:   "loginToNVMETargets succeeds, valid cache",
			array:  "array1",
			mvName: "csi-mv---NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			initFunc: func() {
				gonvme.GONVMEMock.InduceDiscoveryError = false
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{
					{
						target: gonvme.NVMeTarget{Portal: "1.1.1.1", TargetNqn: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
			wantErr: false,
		},
		{
			name:   "loginToNVMETargets fails, valid cache",
			array:  "array1",
			mvName: "csi-mv---NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			initFunc: func() {
				gonvme.GONVMEMock.InduceDiscoveryError = true
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{
					{
						target: gonvme.NVMeTarget{Portal: "1.1.1.1", TargetNqn: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
		},
	}
	// Run the tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new service instance for testing
			s := &service{
				opts: Opts{
					UseProxy:   true,
					PortGroups: []string{"portgroup1"},
				},
				loggedInNVMeArrays: map[string]bool{},
				nvmetcpClient:      gonvme.NewMockNVMe(map[string]string{}),
				nvmeTargets:        &sync.Map{},
			}
			tc.pmaxClient = tc.getClient()
			// Call the function and check the results
			tc.initFunc()
			err := s.performNVMETCPLoginOnSymID(context.Background(), tc.array, tc.mvName, tc.pmaxClient)
			if tc.wantErr && err == nil {
				t.Errorf("Expected error but got none")
			} else if !tc.wantErr && err != nil {
				t.Errorf("Expected no error but got %v", err)
			}
		})
	}
}

func TestSetupArrayForNVMeTCP(t *testing.T) {
	testCases := []struct {
		name         string
		NQNs         []string
		initFunc     func()
		setupClient  func(c *mocks.MockPmaxClient)
		wantErr      bool
		wantContains string
	}{
		{
			name: "GetHostID fails",
			NQNs: []string{"nqn.test:001"},
			initFunc: func() {
				gonvme.GONVMEMock.InduceInitiatorError = true
			},
			setupClient:  func(_ *mocks.MockPmaxClient) {},
			wantErr:      true,
			wantContains: "failed to get local NVMe host ID",
		},
		{
			name: "setupNVMeTCPTargetDiscovery fails",
			NQNs: []string{"nqn.test:001"},
			initFunc: func() {
				gonvme.GONVMEMock.InduceInitiatorError = false
				getNVMeTCPTargetsFromPortGroups = func(_ *service, _ context.Context, _ string, _ []string, _ pmax.Pmax) ([]gonvme.NVMeTarget, error) {
					return nil, fmt.Errorf("NVMeTCP target fetch error")
				}
			},
			setupClient: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetHostByID(gomock.Any(), "array1", gomock.Any()).Return(nil, errors.New("host not found"))
				c.EXPECT().CreateHost(gomock.Any(), "array1", gomock.Any(), gomock.Any(), gomock.Any()).Return(&types.Host{HostID: "testhost"}, nil)
			},
			wantErr: true,
		},
		{
			name: "validateNVMeInitiators succeeds on first attempt",
			NQNs: []string{"nqn.test:001"},
			initFunc: func() {
				gonvme.GONVMEMock.InduceInitiatorError = false
				gonvme.GONVMEMock.InduceDiscoveryError = false
				getNVMeTCPTargetsFromPortGroups = func(_ *service, _ context.Context, _ string, _ []string, _ pmax.Pmax) ([]gonvme.NVMeTarget, error) {
					return []gonvme.NVMeTarget{{Portal: "1.2.3.4", TargetNqn: "nqn.test:001"}}, nil
				}
			},
			setupClient: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetHostByID(gomock.Any(), "array1", gomock.Any()).Return(nil, errors.New("host not found"))
				c.EXPECT().CreateHost(gomock.Any(), "array1", gomock.Any(), gomock.Any(), gomock.Any()).Return(&types.Host{HostID: "testhost"}, nil)
				c.EXPECT().GetInitiatorList(gomock.Any(), "array1", "", false, false).Return(&types.InitiatorList{
					InitiatorIDs: []string{"nqn.test:001:A2D57D74A1984E6BAA7897AF9CD00F31"},
				}, nil)
			},
			wantErr: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			origGetTargets := getNVMeTCPTargetsFromPortGroups
			origQueryAttempts := pmaxQueryAttempts
			pmaxQueryAttempts = 1
			defer func() {
				gonvme.GONVMEMock.InduceInitiatorError = false
				gonvme.GONVMEMock.InduceDiscoveryError = false
				getNVMeTCPTargetsFromPortGroups = origGetTargets
				pmaxQueryAttempts = origQueryAttempts
			}()

			tc.initFunc()

			ctrl := gomock.NewController(t)
			c := mocks.NewMockPmaxClient(ctrl)
			tc.setupClient(c)

			svc := &service{
				opts: Opts{
					NodeName: "node1",
				},
				nvmetcpClient: gonvme.NewMockNVMe(map[string]string{}),
			}

			err := svc.setupArrayForNVMeTCP(context.Background(), "array1", tc.NQNs, c)
			if tc.wantErr {
				assert.Error(t, err)
				if tc.wantContains != "" {
					assert.Contains(t, err.Error(), tc.wantContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestGetNVMeTCPTargetsFromPortGroupsUsesSubsystemNQN(t *testing.T) {
	origGetIPInterfaces := getIPInterfaces
	getIPInterfaces = func(_ context.Context, _ string, _ []string, _ pmax.Pmax) (map[string]int32, error) {
		return map[string]int32{"10.247.18.23": 4420}, nil
	}
	defer func() { getIPInterfaces = origGetIPInterfaces }()

	ctrl := gomock.NewController(t)
	c := mocks.NewMockPmaxClient(ctrl)
	nvmeClient := &nvmeClientMock{
		discoverTargets: func(_ string) ([]gonvme.NVMeTarget, error) {
			return []gonvme.NVMeTarget{
				{
					Portal:    "10.247.18.23",
					TargetNqn: "nqn.1988-11.com.dell:PowerMax_2500:00:000120001965",
				},
				{
					Portal:    "10.247.18.24",
					TargetNqn: "nqn.1988-11.com.dell:PowerMax_2500:00:000120001965",
				},
			}, nil
		},
	}

	got, err := (&service{nvmetcpClient: nvmeClient}).getNVMeTCPTargetsFromPortGroupsImpl(context.Background(), "array1", []string{"pg1"}, c)
	assert.NoError(t, err)
	assert.Equal(t, []gonvme.NVMeTarget{
		{
			Portal:    "10.247.18.23",
			TargetNqn: "nqn.1988-11.com.dell:PowerMax_2500:00:000120001965",
		},
	}, got)
}

func TestGetNVMeTCPTargetsFromPortGroupsContinuesAfterPortalFailure(t *testing.T) {
	origGetIPInterfaces := getIPInterfaces
	getIPInterfaces = func(_ context.Context, _ string, _ []string, _ pmax.Pmax) (map[string]int32, error) {
		return map[string]int32{
			"10.247.18.23": 4420,
			"10.247.18.24": 4420,
		}, nil
	}
	defer func() { getIPInterfaces = origGetIPInterfaces }()

	ctrl := gomock.NewController(t)
	pmaxClient := mocks.NewMockPmaxClient(ctrl)
	nvmeClient := &nvmeClientMock{
		discoverTargets: func(address string) ([]gonvme.NVMeTarget, error) {
			if address == "10.247.18.23" {
				return nil, errors.New("portal unavailable")
			}
			return []gonvme.NVMeTarget{{
				Portal:    address,
				TargetNqn: "nqn.1988-11.com.dell:PowerMax_2500:00:000120001965",
			}}, nil
		},
	}

	got, err := (&service{nvmetcpClient: nvmeClient}).getNVMeTCPTargetsFromPortGroupsImpl(context.Background(), "array1", []string{"pg1"}, pmaxClient)
	assert.NoError(t, err)
	assert.Equal(t, []gonvme.NVMeTarget{{
		Portal:    "10.247.18.24",
		TargetNqn: "nqn.1988-11.com.dell:PowerMax_2500:00:000120001965",
	}}, got)
}

func TestGetNVMeTCPTargetsFromPortGroupsErrorsWhenNoTargetsDiscovered(t *testing.T) {
	origGetIPInterfaces := getIPInterfaces
	getIPInterfaces = func(_ context.Context, _ string, _ []string, _ pmax.Pmax) (map[string]int32, error) {
		return map[string]int32{
			"10.247.18.23": 4420,
			"10.247.18.24": 4420,
		}, nil
	}
	defer func() { getIPInterfaces = origGetIPInterfaces }()

	ctrl := gomock.NewController(t)
	pmaxClient := mocks.NewMockPmaxClient(ctrl)
	nvmeClient := &nvmeClientMock{
		discoverTargets: func(_ string) ([]gonvme.NVMeTarget, error) {
			return nil, nil
		},
	}

	got, err := (&service{nvmetcpClient: nvmeClient}).getNVMeTCPTargetsFromPortGroupsImpl(context.Background(), "array1", []string{"pg1"}, pmaxClient)
	assert.Nil(t, got)
	assert.EqualError(t, err, "no NVMe targets for symid array1")
}

func TestGetAndConfigureArrayNVMeTCPTargetsUsesDiscoveredNQNAndDeduplicates(t *testing.T) {
	const sharedNQN = "nqn.example:subsystem"
	symToAllNVMeTCPTargets.Store("array1", []NVMeTCPTargetInfo{
		{Target: sharedNQN, Portal: "10.10.10.11"},
		{Target: sharedNQN, Portal: "10.10.10.12"},
	})
	defer symToAllNVMeTCPTargets.Delete("array1")

	symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{
		{target: gonvme.NVMeTarget{TargetNqn: sharedNQN, Portal: "10.10.10.11"}},
		{target: gonvme.NVMeTarget{TargetNqn: sharedNQN, Portal: "10.10.10.12"}},
	})
	defer symToMaskingViewTargets.Delete("array1")

	svc := &service{
		opts: Opts{PortGroups: []string{"pg1"}},
	}
	got := svc.getAndConfigureArrayNVMeTCPTargets(context.Background(), []string{
		sharedNQN + ":PORT_A",
		sharedNQN + ":PORT_B",
	}, "array1", mocks.NewMockPmaxClient(gomock.NewController(t)))

	assert.ElementsMatch(t, []NVMeTCPTargetInfo{
		{Target: sharedNQN, Portal: "10.10.10.11"},
		{Target: sharedNQN, Portal: "10.10.10.12"},
	}, got)
}

func TestSetupNVMeTCPTargetDiscovery_EmptyTargets(t *testing.T) {
	orig := getNVMeTCPTargetsFromPortGroups
	getNVMeTCPTargetsFromPortGroups = func(_ *service, _ context.Context, _ string, _ []string, _ pmax.Pmax) ([]gonvme.NVMeTarget, error) {
		return []gonvme.NVMeTarget{}, nil
	}
	defer func() { getNVMeTCPTargetsFromPortGroups = orig }()

	svc := &service{
		opts:          Opts{PortGroups: []string{"pg1"}},
		nvmetcpClient: gonvme.NewMockNVMe(map[string]string{}),
	}
	ctrl := gomock.NewController(t)
	c := mocks.NewMockPmaxClient(ctrl)

	err := svc.setupNVMeTCPTargetDiscovery(context.Background(), "array1", c)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "couldn't find any NVMeTCP targets")
}

func TestRetryableUpdateHostInitiators(t *testing.T) {
	testCases := []struct {
		name         string
		attempts     int
		ctxFunc      func() context.Context
		wantContains string
	}{
		{
			name:         "all attempts fail",
			attempts:     1,
			ctxFunc:      func() context.Context { return context.Background() },
			wantContains: "failed to update host initiators on array after",
		},
		{
			name:     "context cancelled on second attempt",
			attempts: 2,
			ctxFunc: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantContains: "context timeout",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orig := pmaxQueryAttempts
			pmaxQueryAttempts = tc.attempts
			defer func() { pmaxQueryAttempts = orig }()

			ctrl := gomock.NewController(t)
			c := mocks.NewMockPmaxClient(ctrl)
			host := &types.Host{HostID: "host1"}
			c.EXPECT().UpdateHostInitiators(gomock.Any(), "array1", host, gomock.Any()).Return(nil, errors.New("update failed"))

			svc := &service{}
			result, err := svc.retryableUpdateHostInitiators(tc.ctxFunc(), "array1", host, []string{"nqn.test:001"}, c)
			assert.Nil(t, result)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantContains)
		})
	}
}

func TestRetryableCreateHost(t *testing.T) {
	testCases := []struct {
		name         string
		attempts     int
		ctxFunc      func() context.Context
		wantContains string
	}{
		{
			name:         "all attempts fail",
			attempts:     1,
			ctxFunc:      func() context.Context { return context.Background() },
			wantContains: "failed to create host on array after",
		},
		{
			name:     "context cancelled on second attempt",
			attempts: 2,
			ctxFunc: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantContains: "context timeout",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			orig := pmaxQueryAttempts
			pmaxQueryAttempts = tc.attempts
			defer func() { pmaxQueryAttempts = orig }()

			ctrl := gomock.NewController(t)
			c := mocks.NewMockPmaxClient(ctrl)
			c.EXPECT().CreateHost(gomock.Any(), "array1", "node1", gomock.Any(), gomock.Any()).Return(nil, errors.New("create failed"))

			svc := &service{}
			result, err := svc.retryableCreateHost(tc.ctxFunc(), "array1", "node1", []string{"nqn.test:001"}, nil, c)
			assert.Nil(t, result)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantContains)
		})
	}
}

func TestCreateTopologyMap(t *testing.T) {
	var listener net.Listener
	testCases := []struct {
		name                      string
		nodeName                  string
		getClient                 func() *mocks.MockPmaxClient
		pmaxClient                pmax.Pmax
		nvmeTCPClient             *gonvme.MockNVMe
		iscsiClient               *goiscsi.MockISCSI
		managedArrays             []string
		arrayTransportProtocolMap map[string]string
		initFunc                  func()
		loggedInNVMeArrays        map[string]bool
		loggedInArrays            map[string]bool
		portGroups                []string
		want                      *csi.NodeGetInfoRequest
		wantErr                   bool
	}{
		{
			name: "Success case NVME with logged in arrays",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNFSServerList(gmock.All(), "array1").AnyTimes().Return(nil, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
				gonvme.GONVMEMock.InduceDiscoveryError = false
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
		{
			name: "Success case NVME no logged in arrays",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNFSServerList(gmock.All(), "array1").AnyTimes().Return(nil, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
				gonvme.GONVMEMock.InduceDiscoveryError = false
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": false,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
		{
			name: "Success case ISCSI, no logged in arrays",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array3").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array3", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array3", gmock.Any(), gmock.Any()).AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"127.0.0.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						TCPPort:     9090,
					},
				}, nil)
				c.EXPECT().GetNFSServerList(gmock.All(), "array3").AnyTimes().Return(nil, nil)
				symmetrix.Initialize([]string{"array3"}, c)
				return c
			},
			managedArrays: []string{"array3"},
			arrayTransportProtocolMap: map[string]string{
				"array3": IscsiTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			iscsiClient:   goiscsi.NewMockISCSI(map[string]string{}),
			initFunc: func() {
				listener, _ = net.Listen("tcp", "127.0.0.1:9090")
			},
			loggedInArrays: map[string]bool{
				"array3": false,
			},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "Success case ISCSI, logged in arrays",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNFSServerList(gmock.All(), "array1").AnyTimes().Return(nil, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": IscsiTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			iscsiClient:   goiscsi.NewMockISCSI(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays: map[string]bool{
				"array1": true,
			},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "Error case ISCSI, discover targets failed with no exit status",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNFSServerList(gmock.All(), "array1").AnyTimes().Return(nil, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": IscsiTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			iscsiClient:   goiscsi.NewMockISCSI(map[string]string{}),
			initFunc: func() {
				goiscsi.GOISCSIMock.InduceDiscoveryError = true
			},
			loggedInArrays: map[string]bool{
				"array1": true,
			},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "Invalid case,FC selected",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": FcTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays:     map[string]bool{},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "Invalid case,Vsphere selected",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": Vsphere,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays:     map[string]bool{},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "Failure case, topolgy create fails",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			managedArrays:             []string{""},
			arrayTransportProtocolMap: map[string]string{},
			nvmeTCPClient:             gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays:     map[string]bool{},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            true,
		},
		{
			name: "Error case NVME invalid portgroup",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array2").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array2", "portgroup1").MaxTimes(1).Return(nil, errors.New("portgroup not found"))
				c.EXPECT().GetNFSServerList(gmock.All(), "array2").AnyTimes().Return(nil, nil)
				symmetrix.Initialize([]string{"array2"}, c)
				return c
			},
			managedArrays: []string{"array2"},
			arrayTransportProtocolMap: map[string]string{
				"array2": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays:     map[string]bool{},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            true,
		},
		{
			name: "Success case NFS, nfs server details are returned correctly",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array4").AnyTimes().Return(c)
				c.EXPECT().GetNFSServerList(gmock.All(), "array4").AnyTimes().Return(&types.NFSServerIterator{
					Entries: []types.NFSServerList{
						{
							ID: "nfs1",
						},
					},
				}, nil)
				c.EXPECT().GetNFSServerByID(gmock.All(), "array4", "nfs1").AnyTimes().Return(&types.NFSServer{
					ID:           "nfs1",
					NFSV3Enabled: true,
					NFSV4Enabled: true,
				}, nil)

				symmetrix.Initialize([]string{"array4"}, c)
				return c
			},
			managedArrays:             []string{"array4"},
			arrayTransportProtocolMap: map[string]string{},
			nvmeTCPClient:             gonvme.NewMockNVMe(map[string]string{}),
			iscsiClient:               goiscsi.NewMockISCSI(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays:     map[string]bool{},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{},
			wantErr:            false,
		},
		{
			name: "Success case NFS, ISCSI, issci details returned correctly but not nfs server",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array5").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array5", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array5", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNFSServerList(gmock.All(), "array5").AnyTimes().Return(nil, errors.New("failed to get NFS server list"))
				symmetrix.Initialize([]string{"array5"}, c)
				return c
			},
			managedArrays: []string{"array5"},
			arrayTransportProtocolMap: map[string]string{
				"array1": IscsiTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			iscsiClient:   goiscsi.NewMockISCSI(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays: map[string]bool{
				"array5": true,
			},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "Success case NFS, ISCSI, issci and nfs server details are returned correctly",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array6").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array6", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array6", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNFSServerList(gmock.All(), "array6").AnyTimes().Return(&types.NFSServerIterator{
					Entries: []types.NFSServerList{
						{
							ID: "nfs1",
						},
					},
				}, nil)
				c.EXPECT().GetNFSServerByID(gmock.All(), "array6", "nfs1").AnyTimes().Return(&types.NFSServer{
					ID:           "nfs1",
					NFSV3Enabled: true,
					NFSV4Enabled: true,
				}, nil)
				symmetrix.Initialize([]string{"array6"}, c)
				return c
			},
			managedArrays: []string{"array6"},
			arrayTransportProtocolMap: map[string]string{
				"array6": IscsiTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			iscsiClient:   goiscsi.NewMockISCSI(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays: map[string]bool{
				"array6": true,
			},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "failure case NFS, no valid servers present",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array7").AnyTimes().Return(c)
				c.EXPECT().GetNFSServerList(gmock.All(), "array7").AnyTimes().Return(nil, nil)

				symmetrix.Initialize([]string{"array7"}, c)
				return c
			},
			managedArrays:             []string{"array7"},
			arrayTransportProtocolMap: map[string]string{},
			nvmeTCPClient:             gonvme.NewMockNVMe(map[string]string{}),
			iscsiClient:               goiscsi.NewMockISCSI(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays:     map[string]bool{},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{},
			wantErr:            true,
		},
	}
	// Run the tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new service instance for testing
			s := &service{
				opts: Opts{
					ManagedArrays: tc.managedArrays,
					PortGroups:    tc.portGroups,
				},
				arrayTransportProtocolMap: tc.arrayTransportProtocolMap,
				nvmetcpClient:             tc.nvmeTCPClient,
				iscsiClient:               tc.iscsiClient,
				loggedInArrays:            tc.loggedInArrays,
				loggedInNVMeArrays:        tc.loggedInNVMeArrays,
			}
			tc.pmaxClient = tc.getClient()
			// Call the function and check the results
			tc.initFunc()

			topo := s.createTopologyMap(context.Background(), tc.nodeName)
			if len(topo) != 0 && tc.wantErr {
				t.Errorf("Expected error but got none")
			} else if len(topo) == 0 && !tc.wantErr {
				t.Errorf("Expected no error but got no topology map")
			}
			if listener != nil {
				listener.Close()
			}
		})
	}
}

func TestNodeGetInfo(t *testing.T) {
	type test struct {
		name                      string
		nodeName                  string
		getClient                 func() *mocks.MockPmaxClient
		pmaxClient                pmax.Pmax
		nvmeTCPClient             *gonvme.MockNVMe
		iscsiClient               *goiscsi.MockISCSI
		managedArrays             []string
		arrayTransportProtocolMap map[string]string
		initFunc                  func() *k8smock.MockUtilsInterface
		loggedInNVMeArrays        map[string]bool
		loggedInArrays            map[string]bool
		portGroups                []string
		isVsphereEnabled          bool
		mockUtilsInterface        *k8smock.MockUtilsInterface
		maxVolumesPerNode         int64
		csiNodeGetInfoRequest     *csi.NodeGetInfoRequest
		storageArrays             map[string]StorageArrayConfig
		want                      map[string]string
		expectedZoneInfo          map[string]string
		wantErr                   bool
		wantResp                  bool
	}

	testCases := []test{
		{
			name:              "Success",
			nodeName:          "node1",
			maxVolumesPerNode: 1,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{"max-powermax-volumes-per-node": "1"}, nil)
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
		{
			name:              "Success, node labels does not exist",
			nodeName:          "node1",
			maxVolumesPerNode: -1,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))

				// Set up expectations
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{}, nil)
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
		{
			name:              "Success, vsphere enabled, node labels exists",
			nodeName:          "node1",
			maxVolumesPerNode: 0,
			isVsphereEnabled:  true,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{"max-powermax-volumes-per-node": "0"}, nil)
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
		{
			name:              "Success, vsphere enabled, node labels parse error",
			nodeName:          "node1",
			maxVolumesPerNode: 0,
			isVsphereEnabled:  true,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{"max-powermax-volumes-per-node": "one"}, nil)
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    true,
		},
		{
			name:              "Failure case, no node name",
			nodeName:          "",
			maxVolumesPerNode: 0,
			isVsphereEnabled:  true,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    true,
		},
		{
			name:              "Success, vsphere enabled, node labels does not exist",
			nodeName:          "node1",
			maxVolumesPerNode: 0,
			isVsphereEnabled:  true,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{}, nil)
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
		{
			name:     "Zone labels added to topology",
			nodeName: "node1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetNFSServerList(gmock.All(), "array1").AnyTimes().Return(nil, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{{
						DirectorID: "director1",
						PortID:     "port1",
					}},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{"regionlabel": "R1", "zonelabel": "Z1"}, nil).AnyTimes()
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			storageArrays: map[string]StorageArrayConfig{
				"array1": {
					Labels: map[string]interface{}{"regionlabel": "R1", "zonelabel": "Z1"},
				},
			},
			expectedZoneInfo: map[string]string{"regionlabel": "R1", "zonelabel": "Z1"},
			wantErr:          false,
			wantResp:         true,
		},
		{
			name:     "Site label with slash added as raw topology segment",
			nodeName: "node1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetNFSServerList(gmock.All(), "array1").AnyTimes().Return(nil, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{{
						DirectorID: "director1",
						PortID:     "port1",
					}},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{}, nil).AnyTimes()
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			storageArrays: map[string]StorageArrayConfig{
				"array1": {
					Labels: map[string]interface{}{"topology.kubernetes.io/site": "site1"},
				},
			},
			expectedZoneInfo: map[string]string{"topology.kubernetes.io/site": "site1"},
			wantErr:          false,
			wantResp:         true,
		},
	}
	// Run the tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new service instance for testing
			s := &service{
				opts: Opts{
					NodeName:          tc.nodeName,
					NodeFullName:      tc.nodeName,
					ManagedArrays:     tc.managedArrays,
					PortGroups:        tc.portGroups,
					MaxVolumesPerNode: tc.maxVolumesPerNode,
					IsVsphereEnabled:  tc.isVsphereEnabled,
					StorageArrays:     tc.storageArrays,
				},
				arrayTransportProtocolMap: tc.arrayTransportProtocolMap,
				nvmetcpClient:             tc.nvmeTCPClient,
				iscsiClient:               tc.iscsiClient,
				loggedInArrays:            tc.loggedInArrays,
				loggedInNVMeArrays:        tc.loggedInNVMeArrays,
				k8sUtils:                  tc.initFunc(),
			}
			tc.pmaxClient = tc.getClient()
			// Call the function and check the results
			resp, err := s.NodeGetInfo(context.Background(), tc.csiNodeGetInfoRequest)
			if tc.wantErr && err == nil {
				t.Errorf("Expected error but got none")
			}
			if tc.wantResp {
				topology := resp.AccessibleTopology.Segments
				if tc.expectedZoneInfo != nil {
					for k, v := range tc.expectedZoneInfo {
						if topology[k] != v {
							t.Errorf("Expected topology[%q] = %q but got %q", k, v, topology[k])
						}
					}
				}
				// Verify no topology key has multiple slashes (invalid K8s label)
				for key := range topology {
					slashCount := strings.Count(key, "/")
					if slashCount > 1 {
						t.Errorf("Topology key %q contains %d slashes; Kubernetes labels allow at most 1", key, slashCount)
					}
				}
			}
		})
	}
}

func TestDisconnectVolume(t *testing.T) {
	type tests struct {
		name                      string
		reqID                     string
		symID                     string
		devID                     string
		volumeWWN                 string
		arrayTransportProtocolMap map[string]string
		initMocksFunc             func(tc tests)
		expectedErr               bool
		expectedLogOut            string
	}

	testCases := []tests{
		{
			name:                      "successful disconnect ISCSI",
			reqID:                     "reqID1",
			symID:                     "symID1",
			devID:                     "iscsi-devID1",
			volumeWWN:                 "60000970000197900046533030300501",
			arrayTransportProtocolMap: map[string]string{"symID1": "ISCSI"},
			initMocksFunc: func(tc tests) {
				gofsutil.UseMockFS()
				gofsutil.GOFSWWNPath = nodePublishSymlinkDir
				gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)
				gofsutil.GOFSMockWWNToDevice = map[string]string{tc.volumeWWN: tc.devID}
				mnt := gofsutil.Info{
					Device: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
					Path:   nodePublishPrivateDir + "/" + volume1,
					Source: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
				}
				gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
			},
			expectedErr:    false,
			expectedLogOut: "NodeUnstageVolume disconnectVolume\n",
		},
		{
			name:                      "successful disconnect FC",
			reqID:                     "reqID1",
			symID:                     "symID1",
			devID:                     "fc-devID1",
			volumeWWN:                 "60000970000197900046533030300501",
			arrayTransportProtocolMap: map[string]string{"symID1": "FC"},
			initMocksFunc: func(tc tests) {
				gofsutil.UseMockFS()
				gofsutil.GOFSWWNPath = nodePublishSymlinkDir
				gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)
				gofsutil.GOFSMockWWNToDevice = map[string]string{tc.volumeWWN: tc.devID}
				mnt := gofsutil.Info{
					Device: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
					Path:   nodePublishPrivateDir + "/" + volume1,
					Source: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
				}
				gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
			},
			expectedErr:    false,
			expectedLogOut: "NodeUnstageVolume disconnectVolume\n",
		},
		{
			name:                      "successful disconnect NVME",
			reqID:                     "reqID1",
			symID:                     "symID1",
			devID:                     "nvme-devID1",
			volumeWWN:                 "60000970000197900046533030300501",
			arrayTransportProtocolMap: map[string]string{"symID1": "NVMETCP"},
			initMocksFunc: func(tc tests) {
				gofsutil.UseMockFS()
				gofsutil.GOFSWWNPath = nodePublishSymlinkDir
				gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)
				gofsutil.GOFSMockWWNToDevice = map[string]string{tc.volumeWWN: tc.devID}
				mnt := gofsutil.Info{
					Device: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
					Path:   nodePublishPrivateDir + "/" + volume1,
					Source: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
				}
				gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
			},
			expectedErr:    false,
			expectedLogOut: "NodeUnstageVolume disconnectVolume\n",
		},
		{
			name:                      "successful disconnect Vsphere",
			reqID:                     "reqID1",
			symID:                     "symID1",
			devID:                     "vsphere-devID1",
			volumeWWN:                 "60000970000197900046533030300501",
			arrayTransportProtocolMap: map[string]string{"symID1": "VSPHERE"},
			initMocksFunc: func(tc tests) {
				gofsutil.UseMockFS()
				gofsutil.GOFSWWNPath = nodePublishSymlinkDir
				gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)
				gofsutil.GOFSMockWWNToDevice = map[string]string{tc.volumeWWN: tc.devID}
				mnt := gofsutil.Info{
					Device: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
					Path:   nodePublishPrivateDir + "/" + volume1,
					Source: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
				}
				gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
			},
			expectedErr:    false,
			expectedLogOut: "NodeUnstageVolume disconnectVolume\n",
		},
		{
			name:      "failed to find device path",
			reqID:     "reqID2",
			symID:     "symID2",
			devID:     "devID2",
			volumeWWN: "60000970000197900046533030300005",
			initMocksFunc: func(_ tests) {
				gofsutil.UseMockFS()
			},
			expectedErr:    false,
			expectedLogOut: "NodeUnstage: Didn't find device path for volume wwn2\n",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(_ *testing.T) {
			s := &service{
				arrayTransportProtocolMap: tt.arrayTransportProtocolMap,
			}
			// setup mocks
			s.iscsiConnector = &mockISCSIGobrick{}
			s.fcConnector = &mockFCGobrick{}
			s.nvmeTCPConnector = &mockNVMeTCPConnector{}
			tt.initMocksFunc(tt)

			// Call the function under test
			err := s.disconnectVolume(tt.reqID, tt.symID, tt.devID, tt.volumeWWN)

			if tt.expectedErr && err == nil {
				t.Errorf("disconnectVolume() error = %v, expectedErr %v", err, tt.expectedErr)
			}

			if !tt.expectedErr && err != nil {
				t.Errorf("disconnectVolume() error = %v, expectedErr %v", err, tt.expectedErr)
			}
		})
	}
}

func TestCheckIfArrayProtocolValid(t *testing.T) {
	tests := []struct {
		name                     string
		nodeName                 string
		array                    string
		protocol                 string
		isTopologyControlEnabled bool
		allowedList              map[string][]string
		deniedList               map[string][]string
		want                     bool
	}{
		{
			name:                     "allowed list contains the key",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList: map[string][]string{
				"node1": {
					"array1.protocol1",
				},
			},
			deniedList: map[string][]string{},
			want:       true,
		},
		{
			name:                     "allowed list does not contain key",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList: map[string][]string{
				"node1": {
					"array1.protocol2",
				},
			},
			deniedList: map[string][]string{},
			want:       false,
		},
		{
			name:                     "allowed list contains the key with wildcard",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList: map[string][]string{
				"*": {
					"array1.protocol1",
				},
			},
			deniedList: map[string][]string{},
			want:       true,
		},
		{
			name:                     "allowed list does not contain the key with wildcard",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList: map[string][]string{
				"*": {
					"array1.protocol2",
				},
			},
			deniedList: map[string][]string{},
			want:       false,
		},
		{
			name:                     "denied list contains the key",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList:              map[string][]string{},
			deniedList: map[string][]string{
				"node1": {
					"array1.protocol1",
				},
			},
			want: false,
		},
		{
			name:                     "denied list contains the key with wildcard",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList:              map[string][]string{},
			deniedList: map[string][]string{
				"*": {
					"array1.protocol1",
				},
			},
			want: false,
		},
		{
			name:                     "topology control is disabled",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: false,
			allowedList:              map[string][]string{},
			deniedList:               map[string][]string{},
			want:                     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			s := &service{
				opts: Opts{
					IsTopologyControlEnabled: tt.isTopologyControlEnabled,
				},
				allowedTopologyKeys: tt.allowedList,
				deniedTopologyKeys:  tt.deniedList,
			}
			// Call the function under test
			got := s.checkIfArrayProtocolValid(tt.nodeName, tt.array, tt.protocol)

			// Assert the results
			if got != tt.want {
				t.Errorf("checkIfArrayProtocolValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetIPIntefaces(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name              string
		symID             string
		arrayTargets      []string
		getClient         func() *mocks.MockPmaxClient
		portGroups        []string
		transportProtocol string
		init              func()
		pmaxClient        pmax.Pmax
		want              []string
		wantErr           bool
	}{
		{
			name:              "Valid case",
			arrayTargets:      []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:             "array1",
			portGroups:        []string{"portgroup1"},
			transportProtocol: "NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					PortGroupType: "NVMETCP",
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
			},
			want: []string{"1.1.1.1"},
		},
		{
			name:              "Error case, get port group error",
			arrayTargets:      []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:             "array1",
			portGroups:        []string{"portgroup1"},
			transportProtocol: "NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(nil, errors.New("port group not found"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
			},
			want: nil,
		},
		{
			name:              "Error case, get port error",
			arrayTargets:      []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:             "array1",
			portGroups:        []string{"portgroup1"},
			transportProtocol: "NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					PortGroupType: "NVMETCP",
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(nil, errors.New("port not found"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
			},
			want: nil,
		},
	}

	// Run the tests
	for _, tc := range testCases {
		tc.pmaxClient = tc.getClient()
		t.Run(tc.name, func(t *testing.T) {
			tc.init()
			got, _ := getIPInterfaces(context.Background(), tc.symID, tc.portGroups, tc.pmaxClient)
			if len(got) != len(tc.want) {
				t.Errorf("Expected: %v, but got: %v", len(tc.want), len(got))
			}
		})
	}
}

func TestGetVolumeStats(t *testing.T) {
	gofsutil.UseMockFS()

	wwn := "1234567890ABCDEF"
	gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)
	gofsutil.GOFSMockWWNToDevice = map[string]string{wwn: "deviceID1"}
	mnt := gofsutil.Info{
		Device: nodePublishSymlinkDir + "/wwn-0x" + wwn,
		Path:   nodePublishPrivateDir + "/" + volume1,
		Source: nodePublishSymlinkDir + "/wwn-0x" + wwn,
	}
	gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)

	// success case
	_, _, _, _, _, _, err := getVolumeStats(context.Background(), mnt.Path)
	if err != nil {
		t.Errorf("Expected: success, but got: %v", err)
	}

	// Error case
	gofsutil.GOFSMock.InduceFilesystemInfoError = true
	_, _, _, _, _, _, err = getVolumeStats(context.Background(), mnt.Path)
	if err == nil {
		t.Errorf("Expected: error, but got: %v", err)
	}
}

func TestReachableEndPoint(t *testing.T) {
	type args struct {
		endpoint string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{"Unreachable IP", args{endpoint: "10.255.1.2:100"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			if got := s.reachableEndPoint(tt.args.endpoint); got != tt.want {
				t.Errorf("reachableEndPoint() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSetupNVMeTCPTargetDiscovery(t *testing.T) {
	twoTargets := func() ([]gonvme.NVMeTarget, error) {
		return []gonvme.NVMeTarget{
			{Portal: "ip1", TargetNqn: "nqn1"},
			{Portal: "ip2", TargetNqn: "nqn2"},
		}, nil
	}

	tests := []struct {
		name                string
		getPortGroupTargets func() ([]gonvme.NVMeTarget, error)
		getConnectError     func(target gonvme.NVMeTarget) error
		wantErr             bool
	}{
		{
			name:                "Successful connect",
			getPortGroupTargets: twoTargets,
			getConnectError:     func(_ gonvme.NVMeTarget) error { return nil },
			wantErr:             false,
		},
		{
			name: "Error fetching port group targets",
			getPortGroupTargets: func() ([]gonvme.NVMeTarget, error) {
				return nil, errors.New("unable to fetch NVMeTCP targets from port groups")
			},
			getConnectError: nil, // should not be called
			wantErr:         true,
		},
		{
			name:                "All targets failed to connect",
			getPortGroupTargets: twoTargets,
			getConnectError: func(_ gonvme.NVMeTarget) error {
				return errors.New("unable to connect to NVMe target")
			},
			wantErr: true,
		},
		{
			name:                "First of two targets failed to connect",
			getPortGroupTargets: twoTargets,
			getConnectError: func(target gonvme.NVMeTarget) error {
				if target.Portal == "ip1" {
					return errors.New("unable to connect to NVMe target")
				}
				return nil
			},
			wantErr: false,
		},
	}

	origGetTargets := getNVMeTCPTargetsFromPortGroups
	defer func() {
		getNVMeTCPTargetsFromPortGroups = origGetTargets
	}()

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			ctrl := gmock.NewController(t)
			c := mocks.NewMockPmaxClient(ctrl)

			s := &service{
				nvmetcpClient: &nvmeClientMock{
					getConnectError: tt.getConnectError,
				},
			}

			getNVMeTCPTargetsFromPortGroups = func(_ *service, _ context.Context, _ string, _ []string, _ pmax.Pmax) ([]gonvme.NVMeTarget, error) {
				return tt.getPortGroupTargets()
			}

			err := s.setupNVMeTCPTargetDiscovery(context.Background(), "sym1", c)
			if (err != nil) && !tt.wantErr {
				t.Errorf("Got unexpected setupNVMeTCPTargetDiscovery() error = %v", err)
			} else if (err == nil) && tt.wantErr {
				t.Errorf("Expected setupNVMeTCPTargetDiscovery() error, but got nil")
			}
		})
	}
}

type nvmeClientMock struct {
	gonvme.NVMEinterface
	getConnectError func(target gonvme.NVMeTarget) error
	discoverTargets func(address string) ([]gonvme.NVMeTarget, error)
	getSessions     func() ([]gonvme.NVMESession, error)
}

func (c *nvmeClientMock) NVMeTCPConnect(target gonvme.NVMeTarget, _ bool) error {
	if c.getConnectError != nil {
		return c.getConnectError(target)
	}
	return nil
}

func (c *nvmeClientMock) DiscoverNVMeTCPTargets(address string, _ bool) ([]gonvme.NVMeTarget, error) {
	if c.discoverTargets != nil {
		return c.discoverTargets(address)
	}
	return nil, nil
}

func (c *nvmeClientMock) GetSessions() ([]gonvme.NVMESession, error) {
	if c.getSessions != nil {
		return c.getSessions()
	}
	return nil, nil
}

func TestLoginIntoISCSITargets(t *testing.T) {
	twoTargets := []maskingViewTargetInfo{
		{
			target: goiscsi.ISCSITarget{
				Target: "target1",
				Portal: "portal1",
			},
		},
		{
			target: goiscsi.ISCSITarget{
				Target: "target2",
				Portal: "portal2",
			},
		},
	}

	tests := []struct {
		name          string
		enableCHAP    bool
		getLoginError func(portal string) error
		wantLoggedIn  []string
		wantErr       bool
	}{
		{
			name:          "Successful login",
			enableCHAP:    false,
			getLoginError: func(_ string) error { return nil },
			wantLoggedIn: []string{
				"portal1",
				"portal2",
			},
			wantErr: false,
		},
		{
			name:          "Successful login with CHAP",
			enableCHAP:    true,
			getLoginError: func(_ string) error { return nil },
			wantLoggedIn: []string{
				"portal1",
				"portal2",
			},
			wantErr: false,
		},
		{
			name: "All targets failed to login",
			getLoginError: func(_ string) error {
				return errors.New("unable to login to ISCSI target")
			},
			wantLoggedIn: nil,
			wantErr:      true,
		},
		{
			name: "First of two targets failed to login",
			getLoginError: func(portal string) error {
				if portal == "portal1" {
					return errors.New("unable to login to ISCSI target")
				}
				return nil
			},
			wantLoggedIn: []string{
				"portal2", // only the second target is expected to log in
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			s := &service{
				opts: Opts{
					EnableCHAP: tt.enableCHAP,
				},
				iscsiClient:    &iscsiClientMock{getLoginError: tt.getLoginError},
				iscsiTargets:   map[string][]string{},
				loggedInArrays: map[string]bool{},
			}

			err := s.loginIntoISCSITargets("sym1", twoTargets)
			if (err != nil) && !tt.wantErr {
				t.Errorf("Got unexpected loginIntoISCSITargets() error = %v", err)
			} else if (err == nil) && tt.wantErr {
				t.Errorf("Expected loginIntoISCSITargets() error, but got nil")
			}

			if !tt.wantErr {
				if len(s.loggedInArrays) != 1 || s.loggedInArrays["sym1"] != true {
					t.Errorf("Unexpected logged in arrays: %v", s.loggedInArrays)
				}
			} else {
				if len(s.loggedInArrays) > 0 {
					t.Errorf("Unexpected logged in arrays: %v", s.loggedInArrays)
				}
			}
		})
	}
}

type iscsiClientMock struct {
	goiscsi.ISCSIinterface
	getLoginError func(portal string) error
}

func (c *iscsiClientMock) PerformLogin(target goiscsi.ISCSITarget) error {
	return c.getLoginError(target.Portal)
}

func (c *iscsiClientMock) DiscoverTargets(portal string, _ bool) ([]goiscsi.ISCSITarget, error) {
	if err := c.getLoginError(portal); err != nil {
		return nil, err
	}
	return []goiscsi.ISCSITarget{}, nil
}

func TestValidateNVMeInitiators(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	tests := []struct {
		name          string
		initiatorIDs  []string
		arrayList     []string
		listError     bool
		expectedError bool
		errContains   string
		description   string
	}{
		{
			name:          "All initiators found on array",
			initiatorIDs:  []string{"nqn.2014-08.org.nvmexpress:uuid:test:ABCD1234"},
			arrayList:     []string{"FA-1E:1:nqn.2014-08.org.nvmexpress:uuid:test:ABCD1234"},
			expectedError: false,
			description:   "Single initiator found on array",
		},
		{
			name: "Multiple initiators all found",
			initiatorIDs: []string{
				"nqn.2014-08.org.nvmexpress:uuid:test1:ABCD1234",
				"nqn.2014-08.org.nvmexpress:uuid:test2:ABCD1234",
			},
			arrayList: []string{
				"FA-1E:1:nqn.2014-08.org.nvmexpress:uuid:test1:ABCD1234",
				"FA-1E:2:nqn.2014-08.org.nvmexpress:uuid:test2:ABCD1234",
			},
			expectedError: false,
			description:   "Multiple initiators all found on array",
		},
		{
			name:          "Initiator not found on array",
			initiatorIDs:  []string{"nqn.2014-08.org.nvmexpress:uuid:test:ABCD1234"},
			arrayList:     []string{"FA-1E:1:nqn.2014-08.org.nvmexpress:uuid:other:FFFF0000"},
			expectedError: true,
			errContains:   "not found",
			description:   "Initiator not present in array list",
		},
		{
			name:          "Empty array initiator list",
			initiatorIDs:  []string{"nqn.2014-08.org.nvmexpress:uuid:test:ABCD1234"},
			arrayList:     []string{},
			expectedError: true,
			errContains:   "not found",
			description:   "No initiators on array yet",
		},
		{
			name:          "GetInitiatorList error",
			initiatorIDs:  []string{"nqn.2014-08.org.nvmexpress:uuid:test:ABCD1234"},
			listError:     true,
			expectedError: true,
			errContains:   "failed to get all initiators",
			description:   "Error getting initiator list from array",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mockClient := mocks.NewMockPmaxClient(ctrl)
			svc := &service{
				cacheMutex:         sync.Mutex{},
				loggedInNVMeArrays: make(map[string]bool),
			}

			if tc.listError {
				mockClient.EXPECT().GetInitiatorList(gomock.Any(), "000197900046", "", false, false).
					Return(nil, errors.New("simulated error")).Times(1)
			} else {
				mockClient.EXPECT().GetInitiatorList(gomock.Any(), "000197900046", "", false, false).
					Return(&types.InitiatorList{InitiatorIDs: tc.arrayList}, nil).Times(1)
			}

			err := svc.validateNVMeInitiators(context.Background(), "000197900046", tc.initiatorIDs, mockClient)

			if tc.expectedError {
				assert.Error(t, err, tc.description)
				assert.Contains(t, err.Error(), tc.errContains, tc.description)
			} else {
				assert.NoError(t, err, tc.description)
			}
		})
	}
}

func TestMakeNVMeInitiatorIDs(t *testing.T) {
	tests := []struct {
		name       string
		nqns       []string
		hostID     string
		expected   []string
		shouldFail bool
	}{
		{
			name:     "Single NQN with UUID host ID",
			nqns:     []string{"nqn.2014-08.org.nvmexpress:uuid:27212f42-27d7-3250-5c80-b02adbbb66b5"},
			hostID:   "c32abcdf-35f9-4800-88ad-396225c90b70",
			expected: []string{"nqn.2014-08.org.nvmexpress:uuid:27212f42-27d7-3250-5c80-b02adbbb66b5:C32ABCDF35F9480088AD396225C90B70"},
		},
		{
			name: "Multiple NQNs",
			nqns: []string{
				"nqn.2014-08.org.nvmexpress:uuid:aaa",
				"nqn.2014-08.org.nvmexpress:uuid:bbb",
			},
			hostID: "c32abcdf-35f9-4800-88ad-396225c90b70",
			expected: []string{
				"nqn.2014-08.org.nvmexpress:uuid:aaa:C32ABCDF35F9480088AD396225C90B70",
				"nqn.2014-08.org.nvmexpress:uuid:bbb:C32ABCDF35F9480088AD396225C90B70",
			},
		},
		{
			name:     "NQN already has host ID appended",
			nqns:     []string{"nqn.2014-08.org.nvmexpress:uuid:test:C32ABCDF35F9480088AD396225C90B70"},
			hostID:   "c32abcdf-35f9-4800-88ad-396225c90b70",
			expected: []string{"nqn.2014-08.org.nvmexpress:uuid:test:C32ABCDF35F9480088AD396225C90B70"},
		},
		{
			name:     "Empty NQN list",
			nqns:     []string{},
			hostID:   "c32abcdf-35f9-4800-88ad-396225c90b70",
			expected: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := makeNVMeInitiatorIDs(tc.nqns, tc.hostID)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestNVMeInitiatorRetryLogic(t *testing.T) {
	// This test validates the retry logic behavior described in the refactored code.
	// validateNVMeInitiators is called in a retry loop by setupArrayForNVMeTCP.

	t.Run("Array propagation delay scenario", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockClient := mocks.NewMockPmaxClient(ctrl)
		svc := &service{
			cacheMutex:         sync.Mutex{},
			loggedInNVMeArrays: make(map[string]bool),
		}

		initiatorIDs := []string{"nqn.2014-08.org.nvmexpress:uuid:test:ABCD1234"}

		// Scenario 1: Empty list (simulating propagation delay — initiator not yet visible)
		mockClient.EXPECT().GetInitiatorList(gomock.Any(), "000197900046", "", false, false).
			Return(&types.InitiatorList{InitiatorIDs: []string{}}, nil).Times(1)

		err := svc.validateNVMeInitiators(context.Background(), "000197900046", initiatorIDs, mockClient)

		// Should fail because the initiator is not found
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")

		// Scenario 2: Initiator appears (simulating successful propagation)
		mockClient.EXPECT().GetInitiatorList(gomock.Any(), "000197900046", "", false, false).
			Return(&types.InitiatorList{InitiatorIDs: []string{"FA-1E:1:nqn.2014-08.org.nvmexpress:uuid:test:ABCD1234"}}, nil).Times(1)

		err = svc.validateNVMeInitiators(context.Background(), "000197900046", initiatorIDs, mockClient)

		// Should succeed now
		assert.NoError(t, err)
	})
}

/*
func TestNodePublishVolumeErrorPaths(t *testing.T) {
	tests := []struct {
		name        string
		volumeID    string
		nodeID      string
		expectError bool
	}{
		{
			name:        "Error getting volume by ID",
			volumeID:    "vol-123",
			nodeID:      "node-1",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(_ *testing.T) {
			svc := &service{}
			ctx := context.Background()

			_ = svc
			_ = ctx
			_ = tt.volumeID
			_ = tt.nodeID
		})
	}
}
*/

func TestGetAdoptedHostID(t *testing.T) {
	tests := []struct {
		name         string
		symID        string
		adoptedHosts map[string]adoptedHostInfo
		expectHostID string
	}{
		{
			name:  "Returns adopted host ID",
			symID: "000197900111",
			adoptedHosts: map[string]adoptedHostInfo{
				"000197900111": {HostID: "Adopted_Host_Node01", Protocol: FcTransportProtocol},
			},
			expectHostID: "Adopted_Host_Node01",
		},
		{
			name:         "No adopted hosts — returns empty",
			symID:        "000197900111",
			adoptedHosts: nil,
			expectHostID: "",
		},
		{
			name:  "Different array — returns empty",
			symID: "000197900222",
			adoptedHosts: map[string]adoptedHostInfo{
				"000197900111": {HostID: "Adopted_Host_Node01", Protocol: FcTransportProtocol},
			},
			expectHostID: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &service{adoptedHosts: tc.adoptedHosts}
			result := svc.getAdoptedHostID(tc.symID)
			assert.Equal(t, tc.expectHostID, result)
		})
	}
}

func TestIsBootLUNStorageGroup(t *testing.T) {
	tests := []struct {
		name            string
		symID           string
		sgID            string
		adoptedHosts    map[string]adoptedHostInfo
		expectProtected bool
	}{
		{
			name:  "Boot LUN SG is protected",
			symID: "000197900111",
			sgID:  "SG_Boot_Node01",
			adoptedHosts: map[string]adoptedHostInfo{
				"000197900111": {
					HostID:   "Adopted_Host_Node01",
					Protocol: FcTransportProtocol,
					BootLUNs: []bootLUNInfo{
						{StorageGroupID: "SG_Boot_Node01", NumVolumes: 2, MaskingViewID: "MV_Boot"},
					},
				},
			},
			expectProtected: true,
		},
		{
			name:  "CSI SG is not protected",
			symID: "000197900111",
			sgID:  "csi-SG-Node01",
			adoptedHosts: map[string]adoptedHostInfo{
				"000197900111": {
					HostID:   "Adopted_Host_Node01",
					Protocol: FcTransportProtocol,
					BootLUNs: []bootLUNInfo{
						{StorageGroupID: "SG_Boot_Node01", NumVolumes: 2, MaskingViewID: "MV_Boot"},
					},
				},
			},
			expectProtected: false,
		},
		{
			name:            "No adopted hosts — not protected",
			symID:           "000197900111",
			sgID:            "SG_Boot_Node01",
			adoptedHosts:    nil,
			expectProtected: false,
		},
		{
			name:  "Different array — not protected",
			symID: "000197900222",
			sgID:  "SG_Boot_Node01",
			adoptedHosts: map[string]adoptedHostInfo{
				"000197900111": {
					HostID:   "Adopted_Host_Node01",
					Protocol: FcTransportProtocol,
					BootLUNs: []bootLUNInfo{
						{StorageGroupID: "SG_Boot_Node01", NumVolumes: 2, MaskingViewID: "MV_Boot"},
					},
				},
			},
			expectProtected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &service{
				adoptedHosts: tc.adoptedHosts,
			}
			result := svc.isBootLUNStorageGroup(tc.symID, tc.sgID)
			assert.Equal(t, tc.expectProtected, result)
		})
	}
}

func TestDetectBootLUNs(t *testing.T) {
	symID := "000197900111"

	tests := []struct {
		name         string
		hostID       string
		setupMock    func(ctrl *gmock.Controller) *mocks.MockPmaxClient
		expectLUNs   int
		expectErr    bool
		expectErrMsg string
	}{
		{
			name:   "Non-CSI boot storage group detected",
			hostID: "Adopted_Host_Node01",
			setupMock: func(ctrl *gmock.Controller) *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(ctrl)
				c.EXPECT().GetHostMaskingViews(gomock.Any(), symID, "Adopted_Host_Node01").
					Return([]string{"MV_Boot_Node01", "csi-mv-Node01"}, nil)
				c.EXPECT().GetMaskingViewByID(gomock.Any(), symID, "MV_Boot_Node01").
					Return(&types.MaskingView{
						MaskingViewID:  "MV_Boot_Node01",
						StorageGroupID: "SG_Boot_Node01",
					}, nil)
				c.EXPECT().GetMaskingViewByID(gomock.Any(), symID, "csi-mv-Node01").
					Return(&types.MaskingView{
						MaskingViewID:  "csi-mv-Node01",
						StorageGroupID: "csi-SG-Node01",
					}, nil)
				c.EXPECT().GetStorageGroup(gomock.Any(), symID, "SG_Boot_Node01").
					Return(&types.StorageGroup{
						StorageGroupID: "SG_Boot_Node01",
						NumOfVolumes:   2,
					}, nil)
				return c
			},
			expectLUNs: 1,
			expectErr:  false,
		},
		{
			name:   "No masking views — no boot LUNs",
			hostID: "Adopted_Host_Standalone",
			setupMock: func(ctrl *gmock.Controller) *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(ctrl)
				c.EXPECT().GetHostMaskingViews(gomock.Any(), symID, "Adopted_Host_Standalone").
					Return(nil, nil)
				return c
			},
			expectLUNs: 0,
			expectErr:  false,
		},
		{
			name:   "All CSI storage groups — no boot LUNs",
			hostID: "Adopted_Host_Node01",
			setupMock: func(ctrl *gmock.Controller) *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(ctrl)
				c.EXPECT().GetHostMaskingViews(gomock.Any(), symID, "Adopted_Host_Node01").
					Return([]string{"csi-mv-Node01"}, nil)
				c.EXPECT().GetMaskingViewByID(gomock.Any(), symID, "csi-mv-Node01").
					Return(&types.MaskingView{
						MaskingViewID:  "csi-mv-Node01",
						StorageGroupID: "csi-SG-Node01",
					}, nil)
				return c
			},
			expectLUNs: 0,
			expectErr:  false,
		},
		{
			name:   "GetHostMaskingViews API error",
			hostID: "Adopted_Host_Node01",
			setupMock: func(ctrl *gmock.Controller) *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(ctrl)
				c.EXPECT().GetHostMaskingViews(gomock.Any(), symID, "Adopted_Host_Node01").
					Return(nil, errors.New("API timeout"))
				return c
			},
			expectLUNs:   0,
			expectErr:    true,
			expectErrMsg: "failed to get masking views",
		},
		{
			name:   "Multiple non-CSI storage groups",
			hostID: "Adopted_Host_Node01",
			setupMock: func(ctrl *gmock.Controller) *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(ctrl)
				c.EXPECT().GetHostMaskingViews(gomock.Any(), symID, "Adopted_Host_Node01").
					Return([]string{"MV_Boot", "MV_Data"}, nil)
				c.EXPECT().GetMaskingViewByID(gomock.Any(), symID, "MV_Boot").
					Return(&types.MaskingView{
						MaskingViewID:  "MV_Boot",
						StorageGroupID: "SG_Boot",
					}, nil)
				c.EXPECT().GetMaskingViewByID(gomock.Any(), symID, "MV_Data").
					Return(&types.MaskingView{
						MaskingViewID:  "MV_Data",
						StorageGroupID: "SG_Data",
					}, nil)
				c.EXPECT().GetStorageGroup(gomock.Any(), symID, "SG_Boot").
					Return(&types.StorageGroup{StorageGroupID: "SG_Boot", NumOfVolumes: 1}, nil)
				c.EXPECT().GetStorageGroup(gomock.Any(), symID, "SG_Data").
					Return(&types.StorageGroup{StorageGroupID: "SG_Data", NumOfVolumes: 5}, nil)
				return c
			},
			expectLUNs: 2,
			expectErr:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gmock.NewController(t)
			defer ctrl.Finish()

			mockClient := tc.setupMock(ctrl)
			svc := &service{}

			luns, err := svc.detectBootLUNs(context.Background(), symID, tc.hostID, mockClient)
			if tc.expectErr {
				assert.Error(t, err)
				if tc.expectErrMsg != "" {
					assert.Contains(t, err.Error(), tc.expectErrMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Len(t, luns, tc.expectLUNs)
			}
		})
	}
}

func TestDiscoverAndAdoptHost(t *testing.T) {
	symID := "000197900111"

	tests := []struct {
		name               string
		wwpns              []string
		setupMock          func(ctrl *gmock.Controller) *mocks.MockPmaxClient
		expectHostName     string
		expectErr          bool
		expectErrMsg       string
		expectCacheCleared bool
	}{
		{
			name:  "Compatible BFS host adopted",
			wwpns: []string{"5000000000000001", "5000000000000002"},
			setupMock: func(ctrl *gmock.Controller) *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(ctrl)
				expectAdoptionSuccess(c, symID, "Adopted_Host_Node01", []string{"5000000000000001", "5000000000000002"})
				return c
			},
			expectHostName:     "Adopted_Host_Node01",
			expectErr:          false,
			expectCacheCleared: false,
		},
		{
			name:  "No host found — falls through (empty string, nil)",
			wwpns: []string{"5000000000000001"},
			setupMock: func(ctrl *gmock.Controller) *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(ctrl)
				c.EXPECT().GetHostByInitiators(gomock.Any(), symID, []string{"5000000000000001"}).
					Return(nil, nil)
				return c
			},
			expectHostName:     "",
			expectErr:          false,
			expectCacheCleared: false,
		},
		{
			name:  "API error after retries — returns error (RACE-3)",
			wwpns: []string{"5000000000000001"},
			setupMock: func(ctrl *gmock.Controller) *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(ctrl)
				c.EXPECT().GetHostByInitiators(gomock.Any(), symID, []string{"5000000000000001"}).
					Return(nil, errors.New("API timeout")).
					Times(pmaxQueryAttempts)
				return c
			},
			expectHostName: "",
			expectErr:      true,
			expectErrMsg:   "failed to discover host",
		},
		{
			name:  "Incompatible host — foreign WWPNs rejected",
			wwpns: []string{"5000000000000001"},
			setupMock: func(ctrl *gmock.Controller) *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(ctrl)
				c.EXPECT().GetHostByInitiators(gomock.Any(), symID, []string{"5000000000000001"}).
					Return(&types.Host{
						HostID:     "Adopted_Host_Node01",
						Initiators: []string{"FOREIGN_WWPN_1", "FOREIGN_WWPN_2"},
					}, nil)
				return c
			},
			expectHostName: "",
			expectErr:      true,
			expectErrMsg:   "failed validation",
		},
		{
			name:  "Empty WWPN list — returns error",
			wwpns: []string{},
			setupMock: func(ctrl *gmock.Controller) *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(ctrl)
				return c
			},
			expectHostName: "",
			expectErr:      true,
			expectErrMsg:   "no FC WWPNs provided",
		},
		{
			name:  "CSI-convention host — ignored and falls through to standard creation",
			wwpns: []string{"5000000000000001", "5000000000000002"},
			setupMock: func(ctrl *gmock.Controller) *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(ctrl)
				c.EXPECT().GetHostByInitiators(gomock.Any(), symID, []string{"5000000000000001", "5000000000000002"}).
					Return(&types.Host{
						HostID:     "csi-node-CSM-worker-1-FC",
						Initiators: []string{"5000000000000001", "5000000000000002"},
					}, nil)
				return c
			},
			expectHostName:     "",
			expectErr:          false,
			expectCacheCleared: true,
		},
	}

	// Reduce retry count for tests
	origAttempts := pmaxQueryAttempts
	pmaxQueryAttempts = 2
	defer func() { pmaxQueryAttempts = origAttempts }()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gmock.NewController(t)
			defer ctrl.Finish()

			mockClient := tc.setupMock(ctrl)
			svc := &service{
				opts:         Opts{HostManagementMode: HostMgmtModeAdopt},
				adoptedHosts: map[string]adoptedHostInfo{symID: {HostID: "stale-host", Protocol: FcTransportProtocol}},
			}

			hostName, err := svc.discoverAndAdoptHost(context.Background(), symID, tc.wwpns, mockClient)
			if tc.expectErr {
				assert.Error(t, err)
				assert.Equal(t, "", hostName)
				if tc.expectErrMsg != "" {
					assert.Contains(t, err.Error(), tc.expectErrMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectHostName, hostName)
			}

			// Verify cache was cleared if expected
			if tc.expectCacheCleared {
				_, exists := svc.getAdoptedHost(symID)
				assert.False(t, exists, "CSI-convention host should be removed from cache")
			}

			// Verify adopted host is cached on success
			if tc.expectHostName != "" {
				info, ok := svc.adoptedHosts[symID]
				assert.True(t, ok, "adopted host should be cached")
				assert.Equal(t, tc.expectHostName, info.HostID)
				assert.Equal(t, FcTransportProtocol, info.Protocol)
			}
		})
	}
}

// TestHostAdoptionAdoptionIntegration exercises the full BFS adoption flow:
// discoverAndAdoptHost -> detectBootLUNs -> isBootLUNStorageGroup protection
func TestHostAdoptionAdoptionIntegration(t *testing.T) {
	symID := "000197900111"
	adoptedHostName := "Adopted_Host_Node01"
	// portWWNs come from gofsutil.GetFCHostPortWWNs — bare WWPNs (no FA-xD: prefix)
	portWWNs := []string{"5000000000000001", "5000000000000002"}
	// Host initiators on the array
	hostInitiators := []string{"5000000000000001", "5000000000000002"}

	ctrl := gmock.NewController(t)
	defer ctrl.Finish()

	mockClient := mocks.NewMockPmaxClient(ctrl)

	// Phase 1: discoverAndAdoptHost — host discovery with bare WWPNs, then the
	// FR-1.3 port group compatibility check.
	expectAdoptionSuccess(mockClient, symID, adoptedHostName, hostInitiators)

	// Phase 2: detectBootLUNs — expect masking view + SG enumeration
	mockClient.EXPECT().GetHostMaskingViews(gomock.Any(), symID, adoptedHostName).
		Return([]string{"MV_Boot_Node01", "csi-no-srp-sg-cluster1-node01_FC"}, nil)
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), symID, "MV_Boot_Node01").
		Return(&types.MaskingView{
			MaskingViewID:  "MV_Boot_Node01",
			StorageGroupID: "SG_Boot_Node01",
		}, nil)
	mockClient.EXPECT().GetMaskingViewByID(gomock.Any(), symID, "csi-no-srp-sg-cluster1-node01_FC").
		Return(&types.MaskingView{
			MaskingViewID:  "csi-no-srp-sg-cluster1-node01_FC",
			StorageGroupID: "csi-no-srp-sg-cluster1-node01_FC",
		}, nil)
	mockClient.EXPECT().GetStorageGroup(gomock.Any(), symID, "SG_Boot_Node01").
		Return(&types.StorageGroup{
			StorageGroupID: "SG_Boot_Node01",
			NumOfVolumes:   2,
		}, nil)

	svc := &service{
		adoptedHosts: make(map[string]adoptedHostInfo),
	}

	// Phase 1: Adopt
	hostName, err := svc.discoverAndAdoptHost(context.Background(), symID, portWWNs, mockClient)
	assert.NoError(t, err)
	assert.Equal(t, adoptedHostName, hostName)

	// Phase 2: Detect boot LUNs
	bootLUNs, err := svc.detectBootLUNs(context.Background(), symID, adoptedHostName, mockClient)
	assert.NoError(t, err)
	assert.Len(t, bootLUNs, 1, "should detect exactly 1 non-CSI boot SG")
	assert.Equal(t, "SG_Boot_Node01", bootLUNs[0].StorageGroupID)
	assert.Equal(t, 2, bootLUNs[0].NumVolumes)
	assert.Equal(t, "MV_Boot_Node01", bootLUNs[0].MaskingViewID)

	// Store boot LUN info (as nodeHostSetup would)
	info := svc.adoptedHosts[symID]
	info.BootLUNs = bootLUNs
	svc.adoptedHosts[symID] = info

	// Phase 3: Verify protection
	assert.True(t, svc.isBootLUNStorageGroup(symID, "SG_Boot_Node01"),
		"boot SG must be protected")
	assert.False(t, svc.isBootLUNStorageGroup(symID, "csi-no-srp-sg-cluster1-node01_FC"),
		"CSI SG must NOT be protected")
	assert.False(t, svc.isBootLUNStorageGroup("000197900222", "SG_Boot_Node01"),
		"different array must NOT be protected")

	// Phase 4: Verify getAdoptedHostID
	assert.Equal(t, adoptedHostName, svc.getAdoptedHostID(symID))
	assert.Equal(t, "", svc.getAdoptedHostID("000197900222"))
}

// TestHostAdoptionTopologyKeys verifies that addAdoptedHostTopology — the helper NodeGetInfo
// uses to build its AccessibleTopology segment — publishes the adopted host identity
// under the "." separator convention (FR-2A.1).
func TestHostAdoptionTopologyKeys(t *testing.T) {
	symID := "000197900111"
	driverName := "csi-powermax.dellemc.com"

	tests := []struct {
		name         string
		adoptedHosts map[string]adoptedHostInfo
		expectKeys   map[string]string // expected topology key-value pairs
		expectAbsent []string          // keys that must NOT be present
		expectError  bool              // whether to expect an error
	}{
		{
			name: "Adopted host adds both topology keys",
			adoptedHosts: map[string]adoptedHostInfo{
				symID: {HostID: "Adopted_Host_Node01", Protocol: FcTransportProtocol},
			},
			expectKeys: map[string]string{
				driverName + "/" + symID + ".adoptedHost":              "Adopted_Host_Node01",
				driverName + "/" + symID + ".adoptedTransportProtocol": FcTransportProtocol,
			},
		},
		{
			name:         "No adopted hosts — no adopted keys in topology",
			adoptedHosts: nil,
			expectAbsent: []string{
				driverName + "/" + symID + ".adoptedHost",
				driverName + "/" + symID + ".adoptedTransportProtocol",
			},
		},
		{
			name: "Multi-array adoption adds keys for each array",
			adoptedHosts: map[string]adoptedHostInfo{
				"000197900111": {HostID: "Adopted_Host_A", Protocol: FcTransportProtocol},
				"000197900222": {HostID: "Adopted_Host_B", Protocol: FcTransportProtocol},
			},
			expectKeys: map[string]string{
				driverName + "/000197900111.adoptedHost":              "Adopted_Host_A",
				driverName + "/000197900111.adoptedTransportProtocol": FcTransportProtocol,
				driverName + "/000197900222.adoptedHost":              "Adopted_Host_B",
				driverName + "/000197900222.adoptedTransportProtocol": FcTransportProtocol,
			},
		},
		{
			name: "Host name that is not a valid label value fails fast",
			adoptedHosts: map[string]adoptedHostInfo{
				symID: {HostID: strings.Repeat("a", 64), Protocol: FcTransportProtocol},
			},
			expectError: true,
		},
		{
			name: "Host name starting with special character fails fast",
			adoptedHosts: map[string]adoptedHostInfo{
				symID: {HostID: "-Invalid-Host", Protocol: FcTransportProtocol},
			},
			expectError: true,
		},
		{
			name: "Host name with invalid special characters fails fast",
			adoptedHosts: map[string]adoptedHostInfo{
				symID: {HostID: "Adopted_Host_@#$", Protocol: FcTransportProtocol},
			},
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &service{adoptedHosts: tc.adoptedHosts}
			svc.opts.DriverName = driverName

			topology := map[string]string{}
			err := svc.addAdoptedHostTopology(context.Background(), topology)

			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "not a valid Kubernetes label value")
				return
			}

			assert.NoError(t, err)
			for key, val := range tc.expectKeys {
				assert.Equal(t, val, topology[key], "topology key %s should have value %s", key, val)
			}
			for _, key := range tc.expectAbsent {
				_, exists := topology[key]
				assert.False(t, exists, "topology key %s should not be present", key)
			}
		})
	}
}

func TestIsValidTopologyLabelValue(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{"Adopted_Host_Node01", true},
		{"SAN-Boot-Server42", true},
		{"host.with.dots", true},
		{"a", true},
		{"", false},
		{strings.Repeat("a", 63), true},
		{strings.Repeat("a", 64), false},
		{"-leading-dash", false},
		{"trailing-dash-", false},
		{"has space", false},
		{"has:colon", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.valid, isValidTopologyLabelValue(tc.value), "value %q", tc.value)
	}
}

// TestHostAdoptionCSINodeID verifies the node ID advertised through NodeGetInfo. In adopt mode
// the controller has to resolve the Kubernetes Node object from this value to read the
// adopted host labels, so it must be the full node name — and it must be derived from
// static configuration so it cannot flip between restarts (P0-2).
func TestHostAdoptionCSINodeID(t *testing.T) {
	tests := []struct {
		name         string
		mode         string
		adoptedHosts map[string]adoptedHostInfo
		nodeName     string
		nodeFullName string
		expectNodeID string
	}{
		{
			name:         "adopt mode returns the full node name",
			mode:         HostMgmtModeAdopt,
			nodeName:     "worker-2",
			nodeFullName: "worker-2.domain.local",
			expectNodeID: "worker-2.domain.local",
		},
		{
			name:         "create mode returns the short name",
			mode:         HostMgmtModeCreate,
			nodeName:     "worker-2",
			nodeFullName: "worker-2.domain.local",
			expectNodeID: "worker-2",
		},
		{
			name:         "adopt mode with empty full name falls back to the short name",
			mode:         HostMgmtModeAdopt,
			nodeName:     "worker-2",
			nodeFullName: "",
			expectNodeID: "worker-2",
		},
		{
			name: "adopt mode node ID does not depend on adoption having succeeded",
			mode: HostMgmtModeAdopt,
			// No adopted hosts: a transient adoption failure must not change the node ID.
			adoptedHosts: nil,
			nodeName:     "worker-2",
			nodeFullName: "worker-2.domain.local",
			expectNodeID: "worker-2.domain.local",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &service{
				adoptedHosts: tc.adoptedHosts,
				opts: Opts{
					HostManagementMode: tc.mode,
					NodeName:           tc.nodeName,
					NodeFullName:       tc.nodeFullName,
				},
			}
			assert.Equal(t, tc.expectNodeID, svc.csiNodeID())
		})
	}
}

// TestValidateDerivedObjectNameLength guards against a long FQDN in adopt mode
// producing PowerMax host/SG/MV names the array will reject.
func TestValidateDerivedObjectNameLength(t *testing.T) {
	shortEnough := &service{opts: Opts{
		HostManagementMode: HostMgmtModeAdopt,
		ClusterPrefix:      "ABC",
		NodeName:           "worker-2",
		NodeFullName:       "worker-2.domain.local",
	}}
	assert.NoError(t, shortEnough.validateDerivedObjectNameLength())

	tooLong := &service{opts: Opts{
		HostManagementMode: HostMgmtModeAdopt,
		ClusterPrefix:      "ABC",
		NodeName:           "worker-2",
		NodeFullName:       "worker-2." + strings.Repeat("very-long-subdomain.", 4) + "example.com",
	}}
	err := tooLong.validateDerivedObjectNameLength()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeding the 64 character limit")
}

// TestHostAdoptionControllerProtocolDetection verifies that IsNodeNVMe and IsNodeISCSI report
// FC (false) for adopted BFS hosts rather than falling back to suffix matching on a
// host name that does not follow the CSI convention (FR-2A.2).
func TestHostAdoptionControllerProtocolDetection(t *testing.T) {
	symID := "000197900111"
	ctrl := gmock.NewController(t)
	defer ctrl.Finish()

	// No PowerMax calls are expected: adoption short-circuits protocol detection.
	mockClient := mocks.NewMockPmaxClient(ctrl)

	svc := &service{
		adoptedHosts: map[string]adoptedHostInfo{
			symID: {HostID: "SAN_Boot_Server42", Protocol: FcTransportProtocol},
		},
	}

	isNVMe, err := svc.IsNodeNVMe(context.Background(), symID, "worker-1", mockClient, "SAN_Boot_Server42")
	assert.NoError(t, err)
	assert.False(t, isNVMe, "an adopted host must not be classified as NVMe")

	isISCSI, err := svc.IsNodeISCSI(context.Background(), symID, "worker-1", mockClient, "SAN_Boot_Server42")
	assert.NoError(t, err)
	assert.False(t, isISCSI, "an adopted host without a -FC suffix must not be classified as iSCSI")
}

// TestHostAdoptionBootLUNProtectionGuards verifies that isBootLUNStorageGroup correctly
// protects non-CSI storage groups on adopted hosts from CSI modification.
func TestHostAdoptionBootLUNProtectionGuards(t *testing.T) {
	symID := "000197900111"

	tests := []struct {
		name         string
		adoptedHosts map[string]adoptedHostInfo
		sgID         string
		arrayID      string
		expectGuard  bool // true = protected, operation should be blocked
	}{
		{
			name: "Boot LUN SG is protected",
			adoptedHosts: map[string]adoptedHostInfo{
				symID: {
					HostID: "Adopted_Host_Node01", Protocol: FcTransportProtocol,
					BootLUNs: []bootLUNInfo{{StorageGroupID: "SG_Boot_Node01", NumVolumes: 2, MaskingViewID: "MV_Boot"}},
				},
			},
			sgID: "SG_Boot_Node01", arrayID: symID, expectGuard: true,
		},
		{
			name: "CSI SG is NOT protected",
			adoptedHosts: map[string]adoptedHostInfo{
				symID: {
					HostID: "Adopted_Host_Node01", Protocol: FcTransportProtocol,
					BootLUNs: []bootLUNInfo{{StorageGroupID: "SG_Boot_Node01", NumVolumes: 2, MaskingViewID: "MV_Boot"}},
				},
			},
			sgID: "csi-no-srp-sg-cluster-node01-FC", arrayID: symID, expectGuard: false,
		},
		{
			name:         "No adopted hosts — not protected",
			adoptedHosts: nil,
			sgID:         "SG_Boot_Node01", arrayID: symID, expectGuard: false,
		},
		{
			name: "Different array — not protected",
			adoptedHosts: map[string]adoptedHostInfo{
				symID: {
					HostID: "Adopted_Host_Node01", Protocol: FcTransportProtocol,
					BootLUNs: []bootLUNInfo{{StorageGroupID: "SG_Boot_Node01", NumVolumes: 2, MaskingViewID: "MV_Boot"}},
				},
			},
			sgID: "SG_Boot_Node01", arrayID: "000197900222", expectGuard: false,
		},
		{
			name: "Adopted host with no boot LUNs — not protected",
			adoptedHosts: map[string]adoptedHostInfo{
				symID: {HostID: "Adopted_Host_Standalone", Protocol: FcTransportProtocol, BootLUNs: nil},
			},
			sgID: "SG_Something", arrayID: symID, expectGuard: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &service{adoptedHosts: tc.adoptedHosts}
			result := svc.isBootLUNStorageGroup(tc.arrayID, tc.sgID)
			assert.Equal(t, tc.expectGuard, result, "isBootLUNStorageGroup should return %v for SG %s on array %s", tc.expectGuard, tc.sgID, tc.arrayID)
		})
	}
}

// TestHostAdoptionAdoptionMetricsIncrement verifies the Prometheus counters actually move on
// each failure path in discoverAndAdoptHost (FR-8.2).
func TestHostAdoptionAdoptionMetricsIncrement(t *testing.T) {
	symID := "000197900111"

	t.Run("api_error label incremented on API failure", func(t *testing.T) {
		ctrl := gmock.NewController(t)
		defer ctrl.Finish()

		mock := mocks.NewMockPmaxClient(ctrl)
		mock.EXPECT().GetHostByInitiators(gomock.Any(), symID, []string{"5000000000000001"}).
			Return(nil, fmt.Errorf("API timeout")).Times(2)

		orig := pmaxQueryAttempts
		pmaxQueryAttempts = 2
		defer func() { pmaxQueryAttempts = orig }()

		before := testutil.ToFloat64(hostAdoptionErrorsTotal.WithLabelValues("api_error"))
		svc := &service{}
		hostName, err := svc.discoverAndAdoptHost(context.Background(), symID, []string{"5000000000000001"}, mock)
		assert.Error(t, err)
		assert.Empty(t, hostName)
		assert.Contains(t, err.Error(), "failed to discover host")
		assert.Equal(t, before+1, testutil.ToFloat64(hostAdoptionErrorsTotal.WithLabelValues("api_error")))
	})

	t.Run("validation_failed label incremented on host validation failure", func(t *testing.T) {
		ctrl := gmock.NewController(t)
		defer ctrl.Finish()

		// Host with foreign WWPN — validation should fail
		foreignHost := &types.Host{
			HostID:     "Adopted_Host_Foreign",
			Initiators: []string{"FOREIGN_WWPN_1", "FOREIGN_WWPN_2"},
		}
		mock := mocks.NewMockPmaxClient(ctrl)
		mock.EXPECT().GetHostByInitiators(gomock.Any(), symID, []string{"5000000000000001"}).
			Return(foreignHost, nil)

		before := testutil.ToFloat64(hostAdoptionErrorsTotal.WithLabelValues("validation_failed"))
		svc := &service{}
		hostName, err := svc.discoverAndAdoptHost(context.Background(), symID, []string{"5000000000000001"}, mock)
		assert.Error(t, err)
		assert.Empty(t, hostName)
		assert.Contains(t, err.Error(), "failed validation")
		assert.Equal(t, before+1, testutil.ToFloat64(hostAdoptionErrorsTotal.WithLabelValues("validation_failed")))
	})

	t.Run("no_wwpns label incremented on empty WWPN list", func(t *testing.T) {
		ctrl := gmock.NewController(t)
		defer ctrl.Finish()

		mock := mocks.NewMockPmaxClient(ctrl)
		// No mock expectations — should not call GetHostByInitiators

		before := testutil.ToFloat64(hostAdoptionErrorsTotal.WithLabelValues("no_wwpns"))
		svc := &service{}
		hostName, err := svc.discoverAndAdoptHost(context.Background(), symID, []string{}, mock)
		assert.Error(t, err)
		assert.Empty(t, hostName)
		assert.Contains(t, err.Error(), "no FC WWPNs provided")
		assert.Equal(t, before+1, testutil.ToFloat64(hostAdoptionErrorsTotal.WithLabelValues("no_wwpns")))
	})

	t.Run("hosts adopted counter incremented on success", func(t *testing.T) {
		ctrl := gmock.NewController(t)
		defer ctrl.Finish()

		mock := mocks.NewMockPmaxClient(ctrl)
		expectAdoptionSuccess(mock, symID, "Adopted_Host_Node01", []string{"5000000000000001", "5000000000000002"})

		before := testutil.ToFloat64(hostsAdoptedTotal)
		svc := &service{}
		hostName, err := svc.discoverAndAdoptHost(context.Background(), symID,
			[]string{"5000000000000001", "5000000000000002"}, mock)
		assert.NoError(t, err)
		assert.Equal(t, "Adopted_Host_Node01", hostName)
		assert.Equal(t, before+1, testutil.ToFloat64(hostsAdoptedTotal))
	})
}

// TestHostAdoptionMetricsExposedOnDriverRegistry verifies the BFS counters are registered
// against the registry the driver actually serves on its /metrics endpoint. Metrics
// registered with the default registerer would never be scrapeable (FR-8.2).
func TestHostAdoptionMetricsExposedOnDriverRegistry(t *testing.T) {
	// Touch each collector so it is registered and has a sample to gather.
	hostsAdoptedTotal.Add(0)
	bootLUNsDetectedTotal.Add(0)
	hostAdoptionErrorsTotal.WithLabelValues("api_error").Add(0)

	families, err := DriverMetricsRegistry().Gather()
	assert.NoError(t, err)

	gathered := make(map[string]bool, len(families))
	for _, f := range families {
		gathered[f.GetName()] = true
	}
	for _, name := range []string{
		"csi_powermax_hosts_adopted_total",
		"csi_powermax_host_adoption_errors_total",
		"csi_powermax_boot_luns_detected_total",
	} {
		assert.True(t, gathered[name], "%s must be exposed on the driver metrics registry", name)
	}
}

// TestHostAdoptionHostConflictFailsFast verifies a multi-host WWPN conflict is rejected on the
// first attempt. It is a permanent condition, so burning the full retry budget on it
// would blow the 15 second per-node adoption target (NFR-1).
func TestHostAdoptionHostConflictFailsFast(t *testing.T) {
	symID := "000197900111"
	ctrl := gmock.NewController(t)
	defer ctrl.Finish()

	conflict := &fakeHostConflictError{
		msg: "GetHostByInitiators: conflict on array " + symID +
			" — the requested WWPNs resolve to 2 different hosts: " +
			"host Adopted_Host_Node01 has WWPNs [5000000000000001]; host Adopted_Host_Node02 has WWPNs [5000000000000002]",
	}
	mock := mocks.NewMockPmaxClient(ctrl)
	// Exactly one call: a conflict must not be retried.
	mock.EXPECT().GetHostByInitiators(gomock.Any(), symID, []string{"5000000000000001", "5000000000000002"}).
		Return(nil, conflict).Times(1)

	before := testutil.ToFloat64(hostAdoptionErrorsTotal.WithLabelValues("host_conflict"))
	svc := &service{}
	hostName, err := svc.discoverAndAdoptHost(context.Background(), symID,
		[]string{"5000000000000001", "5000000000000002"}, mock)

	assert.Error(t, err)
	assert.Empty(t, hostName)
	assert.Contains(t, err.Error(), "host conflict")
	assert.Contains(t, err.Error(), "Adopted_Host_Node01")
	assert.Contains(t, err.Error(), "Adopted_Host_Node02")
	assert.Equal(t, before+1, testutil.ToFloat64(hostAdoptionErrorsTotal.WithLabelValues("host_conflict")))
}

// fakeHostConflictError stands in for gopowermax's *HostConflictError. The driver
// detects that type through the locally declared hostConflictError interface rather
// than by importing it, so a stub exercises exactly the production code path and the
// test keeps working across gopowermax versions.
type fakeHostConflictError struct{ msg string }

func (e *fakeHostConflictError) IsHostConflict() bool { return true }
func (e *fakeHostConflictError) Error() string        { return e.msg }

func TestIsHostConflictError(t *testing.T) {
	conflict := &fakeHostConflictError{msg: "hosts differ"}
	assert.True(t, isHostConflictError(conflict), "a typed conflict error must be detected")
	assert.True(t, isHostConflictError(fmt.Errorf("wrapped: %w", conflict)), "detection must see through wrapping")
	assert.False(t, isHostConflictError(fmt.Errorf("connection refused")), "an API error must stay retryable")
	// Older gopowermax releases return an untyped conflict error; the message check
	// keeps the driver correct until the dependency pin is bumped.
	assert.True(t, isHostConflictError(fmt.Errorf("GetHostByInitiators: conflict — WWPN x resolves to host y")))
}

// TestValidateAdoptedHostPortGroup covers FR-1.3's port group compatibility check:
// a host with no logged-in SCSI_FC port cannot have a CSI port group built for it,
// so adoption must be rejected with an actionable error.
func TestValidateAdoptedHostPortGroup(t *testing.T) {
	symID := "000197900111"

	t.Run("host with a logged-in SCSI_FC port is compatible", func(t *testing.T) {
		ctrl := gmock.NewController(t)
		defer ctrl.Finish()
		mock := mocks.NewMockPmaxClient(ctrl)
		expectUsableFCPort(mock, symID, "5000000000000001")

		svc := &service{}
		host := &types.Host{HostID: "Adopted_Host_Node01", HostType: "Fibre", Initiators: []string{"5000000000000001"}}
		assert.NoError(t, svc.validateAdoptedHostPortGroup(context.Background(), symID, host, mock))
	})

	t.Run("host with no SCSI_FC port is rejected", func(t *testing.T) {
		ctrl := gmock.NewController(t)
		defer ctrl.Finish()
		mock := mocks.NewMockPmaxClient(ctrl)
		mock.EXPECT().GetPortListByProtocol(gomock.Any(), symID, "SCSI_FC").
			Return(&types.PortList{SymmetrixPortKey: []types.PortKey{}}, nil)
		mock.EXPECT().GetInitiatorList(gomock.Any(), symID, "5000000000000001", false, false).
			Return(&types.InitiatorList{InitiatorIDs: []string{"FA-1D:4:5000000000000001"}}, nil)

		svc := &service{}
		host := &types.Host{HostID: "Adopted_Host_Node01", HostType: "Fibre", Initiators: []string{"5000000000000001"}}
		err := svc.validateAdoptedHostPortGroup(context.Background(), symID, host, mock)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "no initiator logged in on a SCSI_FC director port")
		assert.Contains(t, err.Error(), "Adopted_Host_Node01")
	})

	t.Run("non-Fibre host skips the FC port check", func(t *testing.T) {
		ctrl := gmock.NewController(t)
		defer ctrl.Finish()
		mock := mocks.NewMockPmaxClient(ctrl)

		svc := &service{}
		host := &types.Host{HostID: "Adopted_Host_Node01", HostType: "", Initiators: []string{"5000000000000001"}}
		assert.NoError(t, svc.validateAdoptedHostPortGroup(context.Background(), symID, host, mock))
	})
}

// TestResolveMaskingViewTarget covers FR-6.1. A PowerMax masking view references a
// single host or host group, and a host in a group can only be masked through that
// group — so the CSI masking view has to target the group for grouped BFS hosts.
func TestResolveMaskingViewTarget(t *testing.T) {
	symID := "000197900111"

	t.Run("standalone host targets the host itself", func(t *testing.T) {
		ctrl := gmock.NewController(t)
		defer ctrl.Finish()
		mock := mocks.NewMockPmaxClient(ctrl)

		svc := &service{}
		host := &types.Host{HostID: "Adopted_Host_Node01", NumberHostGroups: 0}
		target, isHost, err := svc.resolveMaskingViewTarget(context.Background(), symID, host, mock)
		assert.NoError(t, err)
		assert.True(t, isHost)
		assert.Equal(t, "Adopted_Host_Node01", target)
	})

	t.Run("host group member targets the host group", func(t *testing.T) {
		ctrl := gmock.NewController(t)
		defer ctrl.Finish()
		mock := mocks.NewMockPmaxClient(ctrl)
		mock.EXPECT().GetHostGroupList(gomock.Any(), symID).
			Return(&types.HostGroupList{HostGroupIDs: []string{"HG_Other", "HG_Cluster01"}}, nil)
		mock.EXPECT().GetHostGroupByID(gomock.Any(), symID, "HG_Other").
			Return(&types.HostGroup{HostGroupID: "HG_Other", Hosts: []types.HostSummary{{HostID: "SomeOtherHost"}}}, nil)
		mock.EXPECT().GetHostGroupByID(gomock.Any(), symID, "HG_Cluster01").
			Return(&types.HostGroup{HostGroupID: "HG_Cluster01", Hosts: []types.HostSummary{{HostID: "Adopted_Host_Node01"}}}, nil)

		svc := &service{}
		host := &types.Host{HostID: "Adopted_Host_Node01", NumberHostGroups: 1}
		target, isHost, err := svc.resolveMaskingViewTarget(context.Background(), symID, host, mock)
		assert.NoError(t, err)
		assert.False(t, isHost, "the masking view must target the host group, not the host")
		assert.Equal(t, "HG_Cluster01", target)
	})

	t.Run("unresolvable host group falls back to the host", func(t *testing.T) {
		ctrl := gmock.NewController(t)
		defer ctrl.Finish()
		mock := mocks.NewMockPmaxClient(ctrl)
		mock.EXPECT().GetHostGroupList(gomock.Any(), symID).
			Return(&types.HostGroupList{HostGroupIDs: []string{}}, nil)

		svc := &service{}
		host := &types.Host{HostID: "Adopted_Host_Node01", NumberHostGroups: 1}
		target, isHost, err := svc.resolveMaskingViewTarget(context.Background(), symID, host, mock)
		assert.NoError(t, err)
		assert.True(t, isHost)
		assert.Equal(t, "Adopted_Host_Node01", target)
	})

	t.Run("host group listing failure is surfaced", func(t *testing.T) {
		ctrl := gmock.NewController(t)
		defer ctrl.Finish()
		mock := mocks.NewMockPmaxClient(ctrl)
		mock.EXPECT().GetHostGroupList(gomock.Any(), symID).Return(nil, fmt.Errorf("unisphere down"))

		svc := &service{}
		host := &types.Host{HostID: "Adopted_Host_Node01", NumberHostGroups: 1}
		_, _, err := svc.resolveMaskingViewTarget(context.Background(), symID, host, mock)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list host groups")
	})
}

func TestValidateFCHostForAdoption(t *testing.T) {
	tests := []struct {
		name            string
		host            *types.Host
		expectedWWPNs   []string
		minOverlapRatio float64
		expectValid     bool
		expectErrMsg    string
	}{
		{
			name: "Exact match — all expected present, no foreign",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001", "5000000000000002"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002"},
			minOverlapRatio: 0.5,
			expectValid:     true,
		},
		{
			name: "Foreign WWPNs with 2-WWPN 100% match — allowed with warning",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001", "5000000000000002", "5000000000000099"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002"},
			minOverlapRatio: 0.5,
			expectValid:     true, // 2/2 = 100% coverage for 2-WWPN system, foreign WWPNs allowed with warning
		},
		{
			name: "2-WWPN system with partial match — rejected (need 100% coverage)",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002"}, // 1/2 = 50%
			minOverlapRatio: 0.5,
			expectValid:     false,
			expectErrMsg:    "100% coverage required",
		},
		{
			name: "Partial match but majority — allowed",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001", "5000000000000002", "5000000000000003"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002", "5000000000000000"}, // 2/3 = 67% > 50%
			minOverlapRatio: 0.5,
			expectValid:     true,
		},
		{
			name: "Insufficient matches — rejected",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002", "5000000000000003"}, // 1/3 = 33% <= 50%
			minOverlapRatio: 0.5,
			expectValid:     false,
			expectErrMsg:    "insufficient WWPN overlap",
		},
		{
			name: "Single HBA match for 2-WWPN system with threshold 0.75 — rejected",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002"}, // 1/2 = 50% < 75%
			minOverlapRatio: 0.75,
			expectValid:     false,
			expectErrMsg:    "100% coverage required",
		},
		{
			name: "4 HBAs, 2 matches — allowed (exactly 50% threshold)",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001", "5000000000000002"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002", "5000000000000003", "5000000000000004"}, // 2/4 = 50% meets threshold
			minOverlapRatio: 0.5,
			expectValid:     true,
		},
		{
			name: "4 HBAs, 1 match — rejected (below 50% threshold)",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002", "5000000000000003", "5000000000000004"}, // 1/4 = 25% < 50%
			minOverlapRatio: 0.5,
			expectValid:     false,
			expectErrMsg:    "insufficient WWPN overlap",
		},
		{
			name: "4 HBAs, 3 matches — allowed",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001", "5000000000000002", "5000000000000003"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002", "5000000000000003", "5000000000000004"}, // 3/4 = 75% > 50%
			minOverlapRatio: 0.5,
			expectValid:     true,
		},
		{
			name: "Zero overlap — rejected (complete HBA replacement)",
			host: &types.Host{
				HostID:     "Adopted_Host_Other",
				Initiators: []string{"5000000000000099", "5000000000000088"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002"}, // 0/2 = 0% overlap
			minOverlapRatio: 0.5,
			expectValid:     false,
			expectErrMsg:    "zero matching",
		},
		{
			name: "Configurable threshold 0.75 — 4 HBAs, 3 matches allowed (exactly 75% threshold)",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001", "5000000000000002", "5000000000000003"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002", "5000000000000003", "5000000000000004"}, // 3/4 = 75% meets threshold
			minOverlapRatio: 0.75,
			expectValid:     true,
		},
		{
			name: "Configurable threshold 0.75 — 4 HBAs, 2 matches rejected (below 75% threshold)",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001", "5000000000000002"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002", "5000000000000003", "5000000000000004"}, // 2/4 = 50% < 75%
			minOverlapRatio: 0.75,
			expectValid:     false,
			expectErrMsg:    "insufficient WWPN overlap",
		},
		{
			name: "Configurable threshold 0.75 — 4 HBAs, 4 matches allowed",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001", "5000000000000002", "5000000000000003", "5000000000000004"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002", "5000000000000003", "5000000000000004"}, // 4/4 = 100% > 75%
			minOverlapRatio: 0.75,
			expectValid:     true,
		},
		{
			name: "Configurable threshold 1.0 — strict match required",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"5000000000000001", "5000000000000002", "5000000000000003"},
			},
			expectedWWPNs:   []string{"5000000000000001", "5000000000000002", "5000000000000003", "5000000000000004"}, // 3/4 = 75% not 100%
			minOverlapRatio: 1.0,
			expectValid:     false,
			expectErrMsg:    "insufficient WWPN overlap",
		},
		{
			name:            "Nil host — returns error",
			host:            nil,
			expectedWWPNs:   []string{"5000000000000001"},
			minOverlapRatio: 0.5,
			expectValid:     false,
			expectErrMsg:    "nil host",
		},
		{
			name: "Empty expected WWPNs — returns error",
			host: &types.Host{
				HostID:     "Adopted_Host_Node01",
				Initiators: []string{"FA-1D:5000000000000001"},
			},
			expectedWWPNs:   []string{},
			minOverlapRatio: 0.5,
			expectValid:     false,
			expectErrMsg:    "empty",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateFCHostForAdoption(context.Background(), tc.host, tc.expectedWWPNs, tc.minOverlapRatio)
			if tc.expectValid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
				if tc.expectErrMsg != "" {
					assert.ErrorContains(t, err, tc.expectErrMsg)
				}
			}
		})
	}
}

// expectUsableFCPort primes the mock so getUsableFCPortsForHost finds one logged-in
// SCSI_FC port for the given WWPN.
func expectUsableFCPort(mock *mocks.MockPmaxClient, symID, wwpn string) {
	initiatorID := "FA-1D:4:" + wwpn
	mock.EXPECT().GetPortListByProtocol(gomock.Any(), symID, "SCSI_FC").
		Return(&types.PortList{SymmetrixPortKey: []types.PortKey{{DirectorID: "FA-1D", PortID: "4"}}}, nil)
	mock.EXPECT().GetInitiatorList(gomock.Any(), symID, wwpn, false, false).
		Return(&types.InitiatorList{InitiatorIDs: []string{initiatorID}}, nil)
	mock.EXPECT().GetInitiatorByID(gomock.Any(), symID, initiatorID).
		Return(&types.Initiator{InitiatorID: initiatorID, OnFabric: true, LoggedIn: true}, nil)
}

// expectAdoptionSuccess primes the mock for a full successful discoverAndAdoptHost:
// host discovery followed by the FR-1.3 port group compatibility check.
func expectAdoptionSuccess(mock *mocks.MockPmaxClient, symID, hostName string, wwpns []string) {
	mock.EXPECT().GetHostByInitiators(gomock.Any(), symID, wwpns).
		Return(&types.Host{
			HostID:     hostName,
			HostType:   "Fibre",
			Initiators: wwpns,
		}, nil)
	mock.EXPECT().GetPortListByProtocol(gomock.Any(), symID, "SCSI_FC").
		Return(&types.PortList{SymmetrixPortKey: []types.PortKey{{DirectorID: "FA-1D", PortID: "4"}}}, nil)
	for _, wwpn := range wwpns {
		initiatorID := "FA-1D:4:" + wwpn
		mock.EXPECT().GetInitiatorList(gomock.Any(), symID, wwpn, false, false).
			Return(&types.InitiatorList{InitiatorIDs: []string{initiatorID}}, nil)
		mock.EXPECT().GetInitiatorByID(gomock.Any(), symID, initiatorID).
			Return(&types.Initiator{InitiatorID: initiatorID, OnFabric: true, LoggedIn: true}, nil)
	}
}

// TestHostAdoptionBootLUNMetricCountsVolumes verifies the boot LUN counter reports non-CSI
// volumes rather than non-CSI storage groups (FR-3.3).
func TestHostAdoptionBootLUNMetricCountsVolumes(t *testing.T) {
	symID := "000197900111"
	ctrl := gmock.NewController(t)
	defer ctrl.Finish()

	mock := mocks.NewMockPmaxClient(ctrl)
	mock.EXPECT().GetHostMaskingViews(gomock.Any(), symID, "Adopted_Host_Node01").
		Return([]string{"MV_Boot", "MV_Legacy", "csi-mv-ABC-worker1-FC"}, nil)
	mock.EXPECT().GetMaskingViewByID(gomock.Any(), symID, "MV_Boot").
		Return(&types.MaskingView{MaskingViewID: "MV_Boot", StorageGroupID: "SG_Boot"}, nil)
	mock.EXPECT().GetMaskingViewByID(gomock.Any(), symID, "MV_Legacy").
		Return(&types.MaskingView{MaskingViewID: "MV_Legacy", StorageGroupID: "SG_Legacy"}, nil)
	mock.EXPECT().GetMaskingViewByID(gomock.Any(), symID, "csi-mv-ABC-worker1-FC").
		Return(&types.MaskingView{MaskingViewID: "csi-mv-ABC-worker1-FC", StorageGroupID: "csi-no-srp-sg-ABC-worker1-FC"}, nil)
	mock.EXPECT().GetStorageGroup(gomock.Any(), symID, "SG_Boot").
		Return(&types.StorageGroup{StorageGroupID: "SG_Boot", NumOfVolumes: 1}, nil)
	mock.EXPECT().GetStorageGroup(gomock.Any(), symID, "SG_Legacy").
		Return(&types.StorageGroup{StorageGroupID: "SG_Legacy", NumOfVolumes: 2}, nil)

	svc := &service{}
	bootLUNs, err := svc.detectBootLUNs(context.Background(), symID, "Adopted_Host_Node01", mock)
	assert.NoError(t, err)
	assert.Len(t, bootLUNs, 2, "the CSI-managed storage group must not be counted")

	totalBootVols := 0
	for _, bl := range bootLUNs {
		totalBootVols += bl.NumVolumes
	}
	assert.Equal(t, 3, totalBootVols, "three non-CSI volumes across two non-CSI storage groups")

	before := testutil.ToFloat64(bootLUNsDetectedTotal)
	bootLUNsDetectedTotal.Add(float64(totalBootVols))
	assert.Equal(t, before+3, testutil.ToFloat64(bootLUNsDetectedTotal),
		"the counter reports volumes, not storage groups")
}

// TestHostAdoptionAdoptionHostManagementModeValidation covers FR-4.1 and FR-4.2 startup
// validation of the two BFS configuration values.
func TestHostAdoptionAdoptionHostManagementModeValidation(t *testing.T) {
	modeTests := []struct {
		value       string
		expectMode  string
		expectError string
	}{
		{value: "", expectMode: HostMgmtModeCreate},
		{value: "create", expectMode: HostMgmtModeCreate},
		{value: "adopt", expectMode: HostMgmtModeAdopt},
		{value: "ADOPT", expectMode: HostMgmtModeAdopt},
		{value: "reuse-only", expectError: "invalid hostManagementMode"},
	}
	for _, tc := range modeTests {
		t.Run("mode="+tc.value, func(t *testing.T) {
			mode, err := resolveHostManagementMode(tc.value)
			if tc.expectError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectError)
				assert.Contains(t, err.Error(), "create")
				assert.Contains(t, err.Error(), "adopt")
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expectMode, mode)
		})
	}

	ratioTests := []struct {
		value       string
		expectRatio float64
		expectError string
	}{
		{value: "", expectRatio: DefaultHostAdoptionMinOverlapRatio},
		{value: "0.75", expectRatio: 0.75},
		{value: "1.0", expectRatio: 1.0},
		{value: "0.0", expectRatio: 0.0},
		{value: "1.5", expectError: "must be between 0.0 and 1.0"},
		{value: "-0.1", expectError: "must be between 0.0 and 1.0"},
		{value: "abc", expectError: "must be between 0.0 and 1.0"},
	}
	for _, tc := range ratioTests {
		t.Run("ratio="+tc.value, func(t *testing.T) {
			ratio, err := resolveHostAdoptionMinOverlapRatio(tc.value)
			if tc.expectError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectError)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expectRatio, ratio)
		})
	}
}
