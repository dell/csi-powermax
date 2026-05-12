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

	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	types "github.com/dell/gopowermax/v2/types/v100"
	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	gmock "github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
)

// Helper functions for common mock setups
func setupGetStorageGroupAndSnapshot(c *mocks.MockPmaxClient, symID, sgName, snapName string) {
	c.EXPECT().GetStorageGroup(gmock.Any(), symID, sgName).Times(1).Return(&types.StorageGroup{}, nil)
	c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), symID, sgName, snapName).Times(1).Return(
		&types.SnapID{SnapIDs: []int64{12345}}, nil)
	c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), symID, sgName, snapName, "12345").Times(1).Return(
		&types.StorageGroupSnap{
			Name: snapName,
			SourceVolume: []types.SourceVolume{
				{Name: "011AB", Capacity: 1000, CapacityGb: 1.0},
				{Name: "011CD", Capacity: 2000, CapacityGb: 2.0},
			},
		}, nil)
}

func setupNotFoundErrorResponse() *types.Error {
	return &types.Error{Message: "Not Found", HTTPStatusCode: http.StatusNotFound}
}

// --- Helper function tests ---

func TestParseGroupSnapshotID(t *testing.T) {
	tests := []struct {
		name       string
		id         string
		wantSymID  string
		wantSG     string
		wantSnap   string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name:      "valid ID",
			id:        "000120000001/csi-ABC-Diamond-SRP_1-SG/csi-GRP-ABC-mysnap",
			wantSymID: "000120000001",
			wantSG:    "csi-ABC-Diamond-SRP_1-SG",
			wantSnap:  "csi-GRP-ABC-mysnap",
		},
		{
			name:       "too few parts",
			id:         "000120000001/sgName",
			wantErr:    true,
			wantErrMsg: "expected format",
		},
		{
			name:       "too many parts",
			id:         "a/b/c/d/e",
			wantErr:    true,
			wantErrMsg: "expected format",
		},
		{
			name:       "empty string",
			id:         "",
			wantErr:    true,
			wantErrMsg: "expected format",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			symID, sg, snap, err := parseGroupSnapshotID(tt.id)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tt.wantErrMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantSymID, symID)
				assert.Equal(t, tt.wantSG, sg)
				assert.Equal(t, tt.wantSnap, snap)
			}
		})
	}
}

func TestBuildGroupSnapshotID(t *testing.T) {
	id := buildGroupSnapshotID("000120000001", "mySG", "mySnap")
	assert.Equal(t, "000120000001/mySG/mySnap", id)
}

func TestBuildMemberSnapshotID(t *testing.T) {
	id := buildMemberSnapshotID("csi-ABC-grp-snap", "000120000001", "011AB")
	assert.Equal(t, "csi-ABC-grp-snap-000120000001-011AB", id)
}

func TestBuildGroupSnapName(t *testing.T) {
	tests := []struct {
		name          string
		clusterPrefix string
		reqName       string
		wantPrefix    string
		wantLenLt     int
	}{
		{
			name:          "short name",
			clusterPrefix: "ABC",
			reqName:       "snap1",
			wantPrefix:    "csi-ABC-grp-snap1",
		},
		{
			name:          "long name gets truncated",
			clusterPrefix: "ABC",
			reqName:       "this-is-a-very-long-snapshot-name-that-exceeds-the-limit",
			wantLenLt:     MaxSnapIdentifierLength,
		},
		{
			name:          "groupsnapshot with UUID",
			clusterPrefix: "ABC",
			reqName:       "groupsnapshot-440da11b-234c-4147-8168-b5b142580865",
			wantPrefix:    "csi-ABC-grp-440da11b-234c-4147",
		},
		{
			name:          "groupsnapshot with consecutive hyphens",
			clusterPrefix: "ABC",
			reqName:       "groupsnapshot--name--with--many--hyphens",
			wantPrefix:    "csi-ABC-grp-name--with--many--h",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildGroupSnapName(tt.clusterPrefix, tt.reqName)
			if tt.wantPrefix != "" {
				assert.Equal(t, tt.wantPrefix, result)
			}
			if tt.wantLenLt > 0 {
				assert.Less(t, len(result), tt.wantLenLt)
			}
			// Must always start with the prefix
			assert.Contains(t, result, CsiVolumePrefix+tt.clusterPrefix+"-grp-")
		})
	}
}

func TestIsSnapshotReady(t *testing.T) {
	assert.True(t, isSnapshotReady([]string{"Established"}))
	assert.True(t, isSnapshotReady([]string{"CopyInProgress", "Established"}))
	assert.False(t, isSnapshotReady([]string{"CopyInProgress"}))
	assert.False(t, isSnapshotReady([]string{}))
	assert.False(t, isSnapshotReady(nil))
}

func TestResolveStorageGroup(t *testing.T) {
	prefix := "ABC"
	csiSGName := "csi-ABC-Diamond-SRP_1-SG"

	tests := []struct {
		name       string
		volumes    []*types.Volume
		wantSG     string
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "all volumes in same SG",
			volumes: []*types.Volume{
				{StorageGroupIDList: []string{csiSGName}},
				{StorageGroupIDList: []string{csiSGName, "other-sg"}},
			},
			wantSG: csiSGName,
		},
		{
			name: "all volumes with no CSI SG",
			volumes: []*types.Volume{
				{StorageGroupIDList: []string{"other-sg"}},
				{StorageGroupIDList: []string{}},
			},
			wantErr:    true,
			wantErrMsg: "volume must already belong",
		},
		{
			name: "mixed membership - some in SG, some not",
			volumes: []*types.Volume{
				{StorageGroupIDList: []string{csiSGName}},
				{StorageGroupIDList: []string{}},
			},
			wantErr:    true,
			wantErrMsg: "volume must already belong",
		},
		{
			name: "volumes in different CSI SGs",
			volumes: []*types.Volume{
				{StorageGroupIDList: []string{csiSGName}},
				{StorageGroupIDList: []string{"csi-ABC-Silver-SRP_1-SG"}},
			},
			wantErr:    true,
			wantErrMsg: "inconsistent StorageGroup",
		},
		{
			name: "single volume in SG",
			volumes: []*types.Volume{
				{StorageGroupIDList: []string{csiSGName}},
			},
			wantSG: csiSGName,
		},
		{
			name: "single volume with no SG",
			volumes: []*types.Volume{
				{StorageGroupIDList: []string{}},
			},
			wantErr:    true,
			wantErrMsg: "volume must already belong",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sg, err := resolveStorageGroup(tt.volumes, prefix)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tt.wantErrMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.wantSG, sg)
			}
		})
	}
}

// --- RPC tests ---
// ... (rest of the code remains the same)

func newTestService(clusterPrefix string) *service {
	return &service{
		opts: Opts{
			ClusterPrefix: clusterPrefix,
		},
		mutex:                 sync.Mutex{},
		cacheMutex:            sync.Mutex{},
		nodeProbeMutex:        sync.Mutex{},
		probeStatusMutex:      sync.Mutex{},
		pollingFrequencyMutex: sync.Mutex{},
		waitGroup:             sync.WaitGroup{},
	}
}

func initMockClient(t *testing.T, symIDs ...string) *mocks.MockPmaxClient {
	t.Helper()
	c := mocks.NewMockPmaxClient(gmock.NewController(t))
	for _, id := range symIDs {
		c.EXPECT().WithSymmetrixID(id).AnyTimes().Return(c)
	}
	c.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
	err := symmetrix.Initialize(symIDs, c)
	if err != nil {
		t.Fatalf("failed to initialize mock client: %s", err)
	}
	return c
}

func cleanMockClients(symIDs ...string) {
	for _, id := range symIDs {
		symmetrix.RemoveClient(id)
	}
}

func TestGroupControllerGetCapabilities(t *testing.T) {
	s := newTestService("ABC")
	resp, err := s.GroupControllerGetCapabilities(context.Background(), &csi.GroupControllerGetCapabilitiesRequest{})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Capabilities, 1)
	assert.Equal(t,
		csi.GroupControllerServiceCapability_RPC_CREATE_DELETE_GET_VOLUME_GROUP_SNAPSHOT,
		resp.Capabilities[0].GetRpc().GetType(),
	)
}

func Test_service_CreateVolumeGroupSnapshot(t *testing.T) {
	const (
		sym1   = "000120000001"
		sym2   = "000120000002"
		dev1   = "011AB"
		dev2   = "011CD"
		sgName = "csi-ABC-Diamond-SRP_1-SG"
	)
	vol1ID := CsiVolumePrefix + clusterPrefix + "-pmax-vol1-ns1-nsx-" + sym1 + "-" + dev1
	vol2ID := CsiVolumePrefix + clusterPrefix + "-pmax-vol2-ns1-nsx-" + sym1 + "-" + dev2

	tests := []struct {
		name       string
		req        *csi.CreateVolumeGroupSnapshotRequest
		before     func(c *mocks.MockPmaxClient)
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "empty name",
			req: &csi.CreateVolumeGroupSnapshotRequest{
				Name:            "",
				SourceVolumeIds: []string{vol1ID},
			},
			before:     func(_ *mocks.MockPmaxClient) {},
			wantErr:    true,
			wantErrMsg: "required: Name",
		},
		{
			name: "no source volumes",
			req: &csi.CreateVolumeGroupSnapshotRequest{
				Name:            "test-snap",
				SourceVolumeIds: []string{},
			},
			before:     func(_ *mocks.MockPmaxClient) {},
			wantErr:    true,
			wantErrMsg: "required: SourceVolumeIds",
		},
		{
			name: "invalid volume ID",
			req: &csi.CreateVolumeGroupSnapshotRequest{
				Name:            "test-snap",
				SourceVolumeIds: []string{"bad-id"},
			},
			before:     func(_ *mocks.MockPmaxClient) {},
			wantErr:    true,
			wantErrMsg: "failed to parse volume ID",
		},
		{
			name: "remote volume component not supported",
			req: &csi.CreateVolumeGroupSnapshotRequest{
				Name: "test-snap",
				SourceVolumeIds: []string{
					CsiVolumePrefix + clusterPrefix + "-pmax-vol1-ns1-nsx-" + sym1 + ":" + sym2 + "-" + dev1 + ":" + dev2,
				},
			},
			before:     func(_ *mocks.MockPmaxClient) {},
			wantErr:    true,
			wantErrMsg: "group snapshots are not supported on PowerMax metro volumes",
		},
		{
			name: "volumes on different arrays",
			req: &csi.CreateVolumeGroupSnapshotRequest{
				Name: "test-snap",
				SourceVolumeIds: []string{
					vol1ID,
					CsiVolumePrefix + clusterPrefix + "-pmax-vol3-ns1-nsx-" + sym2 + "-" + dev2,
				},
			},
			before:     func(_ *mocks.MockPmaxClient) {},
			wantErr:    true,
			wantErrMsg: "same PowerMax array",
		},
		{
			name: "array not licensed for snapshots",
			req: &csi.CreateVolumeGroupSnapshotRequest{
				Name:            "snap1",
				SourceVolumeIds: []string{vol1ID},
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().IsAllowedArray(sym1).Times(1).Return(false, errors.New("not licensed"))
			},
			wantErr:    true,
			wantErrMsg: "Snapshot is not licensed",
		},
		{
			name: "volume not found on array",
			req: &csi.CreateVolumeGroupSnapshotRequest{
				Name:            "snap1",
				SourceVolumeIds: []string{vol1ID},
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().IsAllowedArray(sym1).Times(1).Return(true, nil)
				c.EXPECT().GetReplicationCapabilities(gmock.Any()).Times(1).Return(
					&types.SymReplicationCapabilities{
						SymmetrixCapability: []types.SymmetrixCapability{
							{SymmetrixID: sym1, SnapVxCapable: true},
						},
					}, nil)
				c.EXPECT().GetVolumeByID(gmock.Any(), sym1, dev1).Times(1).Return(nil, errors.New("not found"))
			},
			wantErr:    true,
			wantErrMsg: "not found",
		},
		{
			name: "happy path - volumes in same SG",
			req: &csi.CreateVolumeGroupSnapshotRequest{
				Name:            "snap1",
				SourceVolumeIds: []string{vol1ID, vol2ID},
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().IsAllowedArray(sym1).Times(1).Return(true, nil)
				c.EXPECT().GetReplicationCapabilities(gmock.Any()).Times(1).Return(
					&types.SymReplicationCapabilities{
						SymmetrixCapability: []types.SymmetrixCapability{
							{SymmetrixID: sym1, SnapVxCapable: true},
						},
					}, nil)
				// Optimized: GetVolumeByID called only for first volume to discover SG
				c.EXPECT().GetVolumeByID(gmock.Any(), sym1, dev1).Times(1).Return(
					&types.Volume{
						VolumeID:           dev1,
						CapacityGB:         1.0,
						StorageGroupIDList: []string{sgName},
					}, nil)
				// Optimized: GetVolumeIDListInStorageGroup called once to validate all volumes
				c.EXPECT().GetVolumeIDListInStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return([]string{dev1, dev2}, nil)
				// Idempotency check: snapshot does not exist yet
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, "csi-ABC-grp-snap1").Times(1).Return(nil, &types.Error{Message: "Not Found", HTTPStatusCode: http.StatusNotFound})
				c.EXPECT().CreateSnapshot(gmock.Any(), sym1, "csi-ABC-grp-snap1", gmock.Any(), int64(0)).Times(1).Return(nil)
				// Optimized: GetStorageGroupSnapshotSnapIDs and GetStorageGroupSnapshotSnap called to get volume capacities
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, "csi-ABC-grp-snap1").Times(1).Return(
					&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), sym1, sgName, "csi-ABC-grp-snap1", "12345").Times(1).Return(
					&types.StorageGroupSnap{
						Name: "csi-ABC-grp-snap1",
						SourceVolume: []types.SourceVolume{
							{Name: dev1, Capacity: 1000, CapacityGb: 1.0},
							{Name: dev2, Capacity: 2000, CapacityGb: 2.0},
						},
					}, nil)
			},
			wantErr: false,
		},
		{
			name: "idempotent - snapshot already exists on all volumes",
			req: &csi.CreateVolumeGroupSnapshotRequest{
				Name:            "snap1",
				SourceVolumeIds: []string{vol1ID, vol2ID},
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().IsAllowedArray(sym1).Times(1).Return(true, nil)
				c.EXPECT().GetReplicationCapabilities(gmock.Any()).Times(1).Return(
					&types.SymReplicationCapabilities{
						SymmetrixCapability: []types.SymmetrixCapability{
							{SymmetrixID: sym1, SnapVxCapable: true},
						},
					}, nil)
				// Optimized: GetVolumeByID called only for first volume to discover SG
				c.EXPECT().GetVolumeByID(gmock.Any(), sym1, dev1).Times(1).Return(
					&types.Volume{
						VolumeID:           dev1,
						CapacityGB:         1.0,
						StorageGroupIDList: []string{sgName},
					}, nil)
				// Optimized: GetVolumeIDListInStorageGroup called once to validate all volumes
				c.EXPECT().GetVolumeIDListInStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return([]string{dev1, dev2}, nil)
				// Idempotency check: snapshot already exists on all volumes
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, "csi-ABC-grp-snap1").Times(1).Return(
					&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), sym1, sgName, "csi-ABC-grp-snap1", "12345").Times(1).Return(
					&types.StorageGroupSnap{
						Name: "csi-ABC-grp-snap1",
						SourceVolume: []types.SourceVolume{
							{Name: dev1, Capacity: 1000, CapacityGb: 1.0},
							{Name: dev2, Capacity: 2000, CapacityGb: 2.0},
						},
					}, nil)
				// CreateSnapshot should NOT be called
			},
			wantErr: false,
		},
		{
			name: "partial match - snapshot exists on some volumes",
			req: &csi.CreateVolumeGroupSnapshotRequest{
				Name:            "snap1",
				SourceVolumeIds: []string{vol1ID, vol2ID},
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().IsAllowedArray(sym1).Times(1).Return(true, nil)
				c.EXPECT().GetReplicationCapabilities(gmock.Any()).Times(1).Return(
					&types.SymReplicationCapabilities{
						SymmetrixCapability: []types.SymmetrixCapability{
							{SymmetrixID: sym1, SnapVxCapable: true},
						},
					}, nil)
				// Optimized: GetVolumeByID called only for first volume to discover SG
				c.EXPECT().GetVolumeByID(gmock.Any(), sym1, dev1).Times(1).Return(
					&types.Volume{
						VolumeID:           dev1,
						CapacityGB:         1.0,
						StorageGroupIDList: []string{sgName},
					}, nil)
				// Optimized: GetVolumeIDListInStorageGroup called once to validate all volumes
				c.EXPECT().GetVolumeIDListInStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return([]string{dev1, dev2}, nil)
				// Snapshot exists but with fewer source volumes (partial match)
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, "csi-ABC-grp-snap1").Times(1).Return(
					&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), sym1, sgName, "csi-ABC-grp-snap1", "12345").Times(1).Return(
					&types.StorageGroupSnap{
						Name: "csi-ABC-grp-snap1",
						SourceVolume: []types.SourceVolume{
							{Name: dev1, Capacity: 1000, CapacityGb: 1.0}, // Only one volume
						},
					}, nil)
				// CreateSnapshot should NOT be called
			},
			wantErr:    true,
			wantErrMsg: "already exists on 1 of 2 volumes",
		},
		{
			name: "create SG snapshot API fails",
			req: &csi.CreateVolumeGroupSnapshotRequest{
				Name:            "snap1",
				SourceVolumeIds: []string{vol1ID},
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().IsAllowedArray(sym1).Times(1).Return(true, nil)
				c.EXPECT().GetReplicationCapabilities(gmock.Any()).Times(1).Return(
					&types.SymReplicationCapabilities{
						SymmetrixCapability: []types.SymmetrixCapability{
							{SymmetrixID: sym1, SnapVxCapable: true},
						},
					}, nil)
				// Optimized: GetVolumeByID called only for first volume to discover SG
				c.EXPECT().GetVolumeByID(gmock.Any(), sym1, dev1).Times(1).Return(
					&types.Volume{
						VolumeID:           dev1,
						CapacityGB:         1.0,
						StorageGroupIDList: []string{sgName},
					}, nil)
				// Optimized: GetVolumeIDListInStorageGroup called once to validate all volumes
				c.EXPECT().GetVolumeIDListInStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return([]string{dev1}, nil)
				// Idempotency check: snapshot does not exist yet
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, "csi-ABC-grp-snap1").Times(1).Return(nil, &types.Error{Message: "Not Found", HTTPStatusCode: http.StatusNotFound})
				c.EXPECT().CreateSnapshot(gmock.Any(), sym1, "csi-ABC-grp-snap1", gmock.Any(), int64(0)).Times(1).Return(errors.New("create snapshot failed"))
			},
			wantErr:    true,
			wantErrMsg: "failed to create multi-volume snapshot",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService(clusterPrefix)
			// Clear snapshot license cache between tests
			RemoveReplicationCapability(sym1)

			c := initMockClient(t, sym1, sym2)
			defer cleanMockClients(sym1, sym2)
			tt.before(c)

			resp, err := s.CreateVolumeGroupSnapshot(context.Background(), tt.req)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tt.wantErrMsg)
				}
				assert.Nil(t, resp)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
				assert.NotNil(t, resp.GroupSnapshot)
				assert.NotEmpty(t, resp.GroupSnapshot.GroupSnapshotId)
				assert.Len(t, resp.GroupSnapshot.Snapshots, len(tt.req.SourceVolumeIds))
				if len(tt.req.SourceVolumeIds) > 0 {
					wantSources := make(map[string]bool, len(tt.req.SourceVolumeIds))
					for _, id := range tt.req.SourceVolumeIds {
						wantSources[id] = true
					}
					for _, sn := range resp.GroupSnapshot.Snapshots {
						assert.True(t, wantSources[sn.SourceVolumeId], "expected SourceVolumeId to be a CSI volume handle")
					}
				}
				assert.True(t, resp.GroupSnapshot.ReadyToUse)
			}
		})
	}
}

func Test_service_DeleteVolumeGroupSnapshot(t *testing.T) {
	const sym1 = "000120000001"
	sgName := "csi-ABC-Diamond-SRP_1-SG"
	snapName := "csi-GRP-ABC-snap1"
	groupSnapID := buildGroupSnapshotID(sym1, sgName, snapName)

	tests := []struct {
		name       string
		req        *csi.DeleteVolumeGroupSnapshotRequest
		before     func(c *mocks.MockPmaxClient)
		wantErr    bool
		wantErrMsg string
	}{
		{
			name: "empty group snapshot ID",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: "",
			},
			before:     func(_ *mocks.MockPmaxClient) {},
			wantErr:    true,
			wantErrMsg: "required: GroupSnapshotId",
		},
		{
			name: "invalid group snapshot ID format",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: "bad-format",
			},
			before:     func(_ *mocks.MockPmaxClient) {},
			wantErr:    true,
			wantErrMsg: "invalid group snapshot ID format",
		},
		{
			name: "happy path - delete succeeds",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				setupGetStorageGroupAndSnapshot(c, sym1, sgName, snapName)
				c.EXPECT().DeleteSnapshotS(gmock.Any(), sym1, snapName, gmock.Any(), int64(0)).Times(1).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "idempotent - snapshot not found",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(&types.StorageGroup{}, nil)
				nf := setupNotFoundErrorResponse()
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, snapName).Times(1).Return(nil, nf)
			},
			wantErr: false,
		},
		{
			name: "delete API error (non-not-found)",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				setupGetStorageGroupAndSnapshot(c, sym1, sgName, snapName)
				c.EXPECT().DeleteSnapshotS(gmock.Any(), sym1, snapName, gmock.Any(), int64(0)).Times(1).Return(errors.New("internal error"))
			},
			wantErr:    true,
			wantErrMsg: "failed to delete multi-volume snapshot",
		},
		{
			name: "snapshot_ids prefix mismatch",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
				SnapshotIds:     []string{"wrong-prefix-011AB"},
			},
			before: func(c *mocks.MockPmaxClient) {
				setupGetStorageGroupAndSnapshot(c, sym1, sgName, snapName)
			},
			wantErr:    true,
			wantErrMsg: "does not match group snapshot",
		},
		{
			name: "snapshot_ids valid - delete succeeds",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
				SnapshotIds:     []string{"csi-GRP-ABC-snap1-000120000001-011AB"},
			},
			before: func(c *mocks.MockPmaxClient) {
				setupGetStorageGroupAndSnapshot(c, sym1, sgName, snapName)
				c.EXPECT().DeleteSnapshotS(gmock.Any(), sym1, snapName, gmock.Any(), int64(0)).Times(1).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "StorageGroup not found - empty deletion succeeds",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				nf := setupNotFoundErrorResponse()
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(nil, nf)
			},
			wantErr: false,
		},
		{
			name: "GetStorageGroup fails with non-NotFound error",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(nil, errors.New("internal error"))
			},
			wantErr:    true,
			wantErrMsg: "failed to get StorageGroup",
		},
		{
			name: "GetStorageGroupSnapshotSnapIDs returns NotFound - empty deletion succeeds",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(&types.StorageGroup{}, nil)
				nf := setupNotFoundErrorResponse()
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, snapName).Times(1).Return(nil, nf)
			},
			wantErr: false,
		},
		{
			name: "GetStorageGroupSnapshotSnapIDs returns internal error",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(&types.StorageGroup{}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, snapName).Times(1).Return(nil, errors.New("internal error"))
			},
			wantErr:    true,
			wantErrMsg: "failed to get storage group snapshot IDs",
		},
		{
			name: "GetStorageGroupSnapshotSnapIDs returns empty snap IDs",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(&types.StorageGroup{}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, snapName).Times(1).Return(&types.SnapID{SnapIDs: []int64{}}, nil)
			},
			wantErr: false,
		},
		{
			name: "GetStorageGroupSnapshotSnap returns NotFound - assume snapshot doesn't exist",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(&types.StorageGroup{}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, snapName).Times(1).Return(
					&types.SnapID{SnapIDs: []int64{12345}}, nil)
				nf := &types.Error{Message: "Not Found", HTTPStatusCode: http.StatusNotFound}
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), sym1, sgName, snapName, "12345").Times(1).Return(nil, nf)
			},
			wantErr: false,
		},
		{
			name: "GetStorageGroupSnapshotSnap returns internal error",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(&types.StorageGroup{}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, snapName).Times(1).Return(
					&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), sym1, sgName, snapName, "12345").Times(1).Return(nil, errors.New("internal error"))
			},
			wantErr:    true,
			wantErrMsg: "failed to get storage group snapshot details",
		},
		{
			name: "GetStorageGroupSnapshotSnap returns nil snapshot",
			req: &csi.DeleteVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(&types.StorageGroup{}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, snapName).Times(1).Return(
					&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), sym1, sgName, snapName, "12345").Times(1).Return(nil, nil)
				c.EXPECT().DeleteSnapshotS(gmock.Any(), sym1, snapName, gmock.Any(), int64(0)).Times(1).Return(nil)
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService(clusterPrefix)
			c := initMockClient(t, sym1)
			defer cleanMockClients(sym1)
			tt.before(c)

			resp, err := s.DeleteVolumeGroupSnapshot(context.Background(), tt.req)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tt.wantErrMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
			}
		})
	}
}

func Test_service_GetVolumeGroupSnapshot(t *testing.T) {
	const sym1 = "000120000001"
	sgName := "csi-ABC-Diamond-SRP_1-SG"
	snapName := "csi-ABC-grp-snap1"
	groupSnapID := buildGroupSnapshotID(sym1, sgName, snapName)

	tests := []struct {
		name        string
		req         *csi.GetVolumeGroupSnapshotRequest
		before      func(c *mocks.MockPmaxClient)
		wantErr     bool
		wantErrMsg  string
		wantReady   bool
		wantMembers int
	}{
		{
			name: "empty group snapshot ID",
			req: &csi.GetVolumeGroupSnapshotRequest{
				GroupSnapshotId: "",
			},
			before:     func(_ *mocks.MockPmaxClient) {},
			wantErr:    true,
			wantErrMsg: "required: GroupSnapshotId",
		},
		{
			name: "invalid format",
			req: &csi.GetVolumeGroupSnapshotRequest{
				GroupSnapshotId: "invalid",
			},
			before:     func(_ *mocks.MockPmaxClient) {},
			wantErr:    true,
			wantErrMsg: "invalid group snapshot ID format",
		},
		{
			name: "snapshot not found",
			req: &csi.GetVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(&types.StorageGroup{}, nil)
				c.EXPECT().GetVolumeIDListInStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return([]string{"011AB"}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, snapName).Times(1).Return(
					&types.SnapID{SnapIDs: []int64{}}, nil)
			},
			wantErr:    true,
			wantErrMsg: "group snapshot",
		},
		{
			name: "happy path - established",
			req: &csi.GetVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(&types.StorageGroup{}, nil)
				c.EXPECT().GetVolumeIDListInStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return([]string{"011AB", "011CD"}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, snapName).Times(1).Return(
					&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), sym1, sgName, snapName, "12345").Times(1).Return(
					&types.StorageGroupSnap{
						Name: snapName,
						SourceVolume: []types.SourceVolume{
							{Name: "011AB", Capacity: 1000, CapacityGb: 1.0},
							{Name: "011CD", Capacity: 2000, CapacityGb: 2.0},
						},
					}, nil)
			},
			wantErr:     false,
			wantReady:   true,
			wantMembers: 2,
		},
		{
			name: "partial - one member missing",
			req: &csi.GetVolumeGroupSnapshotRequest{
				GroupSnapshotId: groupSnapID,
			},
			before: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return(&types.StorageGroup{}, nil)
				c.EXPECT().GetVolumeIDListInStorageGroup(gmock.Any(), sym1, sgName).Times(1).Return([]string{"011AB", "011CD"}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), sym1, sgName, snapName).Times(1).Return(
					&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), sym1, sgName, snapName, "12345").Times(1).Return(
					&types.StorageGroupSnap{
						Name: snapName,
						SourceVolume: []types.SourceVolume{
							{Name: "011AB", Capacity: 1000, CapacityGb: 1.0},
							// Note: 011CD is missing from SourceVolume, simulating partial snapshot
						},
					}, nil)
			},
			wantErr:     false,
			wantReady:   false,
			wantMembers: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService(clusterPrefix)
			c := initMockClient(t, sym1)
			defer cleanMockClients(sym1)
			tt.before(c)

			resp, err := s.GetVolumeGroupSnapshot(context.Background(), tt.req)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tt.wantErrMsg)
				}
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, resp)
				assert.NotNil(t, resp.GroupSnapshot)
				assert.Equal(t, tt.wantReady, resp.GroupSnapshot.ReadyToUse)
				assert.Len(t, resp.GroupSnapshot.Snapshots, tt.wantMembers)
			}
		})
	}
}

func Test_service_checkGroupSnapshotIdempotency(t *testing.T) {
	const (
		symID           = "000120000001"
		snapName        = "test-snap"
		groupSnapshotID = "test-group-snap"
		sgName          = "test-sg"
		vol1ID          = "00001"
		vol2ID          = "00002"
		csiVol1ID       = "csi-vol1"
		csiVol2ID       = "csi-vol2"
	)

	tests := []struct {
		name            string
		volDetails      []*types.Volume
		devIDToCSIVolID map[string]string
		mockSetup       func(*mocks.MockPmaxClient)
		wantResponse    bool
		wantErr         bool
		wantErrMsg      string
	}{
		{
			name: "snapshot does not exist - GetStorageGroupSnapshotSnapIDs returns error",
			volDetails: []*types.Volume{
				{VolumeID: vol1ID, CapacityGB: 1.0},
				{VolumeID: vol2ID, CapacityGB: 2.0},
			},
			devIDToCSIVolID: map[string]string{vol1ID: csiVol1ID, vol2ID: csiVol2ID},
			mockSetup: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), symID, sgName, snapName).Times(1).Return(nil, &types.Error{Message: "Not Found", HTTPStatusCode: http.StatusNotFound})
			},
			wantResponse: false,
			wantErr:      false,
		},
		{
			name: "snapshot exists - full match (all source volumes)",
			volDetails: []*types.Volume{
				{VolumeID: vol1ID, CapacityGB: 1.0},
				{VolumeID: vol2ID, CapacityGB: 2.0},
			},
			devIDToCSIVolID: map[string]string{vol1ID: csiVol1ID, vol2ID: csiVol2ID},
			mockSetup: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), symID, sgName, snapName).Times(1).Return(&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), symID, sgName, snapName, "12345").Times(1).Return(&types.StorageGroupSnap{
					Name: snapName,
					SourceVolume: []types.SourceVolume{
						{Name: vol1ID, Capacity: 1000, CapacityGb: 1.0},
						{Name: vol2ID, Capacity: 2000, CapacityGb: 2.0},
					},
				}, nil)
			},
			wantResponse: true,
			wantErr:      false,
		},
		{
			name: "snapshot exists - partial match (fewer source volumes)",
			volDetails: []*types.Volume{
				{VolumeID: vol1ID, CapacityGB: 1.0},
				{VolumeID: vol2ID, CapacityGB: 2.0},
			},
			devIDToCSIVolID: map[string]string{vol1ID: csiVol1ID, vol2ID: csiVol2ID},
			mockSetup: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), symID, sgName, snapName).Times(1).Return(&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), symID, sgName, snapName, "12345").Times(1).Return(&types.StorageGroupSnap{
					Name: snapName,
					SourceVolume: []types.SourceVolume{
						{Name: vol1ID, Capacity: 1000, CapacityGb: 1.0}, // Only one volume
					},
				}, nil)
			},
			wantResponse: false,
			wantErr:      true,
			wantErrMsg:   "already exists",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gmock.NewController(t)
			defer ctrl.Finish()

			mockClient := mocks.NewMockPmaxClient(ctrl)
			if tt.mockSetup != nil {
				tt.mockSetup(mockClient)
			}

			s := &service{}
			resp, err := s.checkGroupSnapshotIdempotency(
				context.Background(),
				mockClient,
				symID,
				snapName,
				groupSnapshotID,
				sgName,
				tt.volDetails,
				tt.devIDToCSIVolID,
			)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tt.wantErrMsg)
				}
				assert.Nil(t, resp)
			} else {
				assert.NoError(t, err)
				if tt.wantResponse {
					assert.NotNil(t, resp)
					assert.NotNil(t, resp.GroupSnapshot)
					assert.Equal(t, groupSnapshotID, resp.GroupSnapshot.GroupSnapshotId)
					assert.Len(t, resp.GroupSnapshot.Snapshots, len(tt.volDetails))
				} else {
					assert.Nil(t, resp)
				}
			}
		})
	}
}

func Test_service_getStorageGroupSnapshotDetails(t *testing.T) {
	const (
		symID    = "000120000001"
		sgName   = "test-sg"
		snapName = "test-snap"
	)

	tests := []struct {
		name         string
		mockSetup    func(*mocks.MockPmaxClient)
		wantErr      bool
		wantErrMsg   string
		wantSnapshot bool
	}{
		{
			name: "GetStorageGroupSnapshotSnapIDs returns NotFound error",
			mockSetup: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), symID, sgName, snapName).Times(1).Return(nil, &types.Error{Message: "Not Found", HTTPStatusCode: http.StatusNotFound})
			},
			wantErr:    true,
			wantErrMsg: "Group snapshot test-snap not found on array 000120000001",
		},
		{
			name: "happy path - snapshot found",
			mockSetup: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), symID, sgName, snapName).Times(1).Return(&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), symID, sgName, snapName, "12345").Times(1).Return(&types.StorageGroupSnap{
					SourceVolume: []types.SourceVolume{
						{Name: "vol1", CapacityGb: 1.0},
						{Name: "vol2", CapacityGb: 2.0},
					},
				}, nil)
			},
			wantErr:      false,
			wantSnapshot: true,
		},
		{
			name: "GetStorageGroupSnapshotSnapIDs returns empty snap IDs",
			mockSetup: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), symID, sgName, snapName).Times(1).Return(&types.SnapID{SnapIDs: []int64{}}, nil)
			},
			wantErr:    true,
			wantErrMsg: "group snapshot test-snap not found on array 000120000001",
		},
		{
			name: "GetStorageGroupSnapshotSnap returns nil snapshot",
			mockSetup: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), symID, sgName, snapName).Times(1).Return(&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), symID, sgName, snapName, "12345").Times(1).Return(nil, nil)
			},
			wantErr:    true,
			wantErrMsg: "group snapshot test-snap not found on array 000120000001",
		},
		{
			name: "GetStorageGroupSnapshotSnapIDs returns internal error",
			mockSetup: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), symID, sgName, snapName).Times(1).Return(nil, errors.New("internal error"))
			},
			wantErr:    true,
			wantErrMsg: "failed to get storage group snapshot IDs: internal error",
		},
		{
			name: "GetStorageGroupSnapshotSnap returns internal error",
			mockSetup: func(c *mocks.MockPmaxClient) {
				c.EXPECT().GetStorageGroupSnapshotSnapIDs(gmock.Any(), symID, sgName, snapName).Times(1).Return(&types.SnapID{SnapIDs: []int64{12345}}, nil)
				c.EXPECT().GetStorageGroupSnapshotSnap(gmock.Any(), symID, sgName, snapName, "12345").Times(1).Return(nil, errors.New("internal error"))
			},
			wantErr:    true,
			wantErrMsg: "failed to get storage group snapshot details: internal error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("ABC")
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			c := mocks.NewMockPmaxClient(ctrl)

			tt.mockSetup(c)

			sgSnapshot, err := s.getStorageGroupSnapshotDetails(context.Background(), c, symID, sgName, snapName)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tt.wantErrMsg)
				}
				assert.Nil(t, sgSnapshot)
			} else {
				assert.NoError(t, err)
				if tt.wantSnapshot {
					assert.NotNil(t, sgSnapshot)
					assert.Len(t, sgSnapshot.SourceVolume, 2)
				} else {
					assert.Nil(t, sgSnapshot)
				}
			}
		})
	}
}

func Test_service_getGroupVolumeDetails(t *testing.T) {
	const symID = "000120000001"

	tests := []struct {
		name       string
		vols       []groupVolInfo
		mockSetup  func(*mocks.MockPmaxClient)
		wantErr    bool
		wantErrMsg string
		wantSgName string
	}{
		{
			name: "no volumes provided",
			vols: []groupVolInfo{},
			mockSetup: func(_ *mocks.MockPmaxClient) {
				// No setup needed
			},
			wantErr:    true,
			wantErrMsg: "no volumes provided",
		},
		{
			name: "GetVolumeByID returns NotFound",
			vols: []groupVolInfo{
				{csiVolID: "csi-vol1", volName: "vol1", symID: symID, devID: "011AB"},
			},
			mockSetup: func(c *mocks.MockPmaxClient) {
				nf := &types.Error{Message: "Not Found", HTTPStatusCode: http.StatusNotFound}
				c.EXPECT().GetVolumeByID(gmock.Any(), symID, "011AB").Times(1).Return(nil, nf)
			},
			wantErr:    true,
			wantErrMsg: "volume 011AB not found on array 000120000001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService("ABC")
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			c := mocks.NewMockPmaxClient(ctrl)

			tt.mockSetup(c)

			volDetails, devIDToCSIVolID, sgNameResult, err := s.getGroupVolumeDetails(context.Background(), c, symID, tt.vols)

			if tt.wantErr {
				assert.Error(t, err)
				if tt.wantErrMsg != "" {
					assert.Contains(t, err.Error(), tt.wantErrMsg)
				}
				assert.Nil(t, volDetails)
				assert.Nil(t, devIDToCSIVolID)
				assert.Empty(t, sgNameResult)
			} else {
				assert.NoError(t, err)
				if tt.wantSgName != "" {
					assert.Equal(t, tt.wantSgName, sgNameResult)
				}
				assert.NotNil(t, volDetails)
				assert.NotNil(t, devIDToCSIVolID)
			}
		})
	}
}
