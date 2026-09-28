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
	"fmt"
	"net/http"
	"testing"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	types "github.com/dell/gopowermax/v2/types/v100"
	volumegrouprpc "github.com/csi-addons/spec/lib/go/volumegroup"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// newCSIAddonsTestService returns a *service configured the way the
// CSI-Addons code path expects (cluster prefix populated, no Unisphere
// connection, mocked pmax client registered via symmetrix.Initialize).
func newCSIAddonsTestService(t *testing.T) (*service, *mocks.MockPmaxClient, func()) {
	t.Helper()
	// The CSI-Addons mutating RPCs acquire a Storage Group lock via
	// RequestLock/ReleaseLock, which require the lock request handler
	// goroutine to be running. Start it for the test process.
	LockRequestHandler()
	ctrl := gomock.NewController(t)
	pmaxClient := mocks.NewMockPmaxClient(ctrl)
	// The symmetrix package wraps every PowerMax client with
	// WithSymmetrixID before returning it, so it must be satisfied
	// by the mock.
	pmaxClient.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(pmaxClient)
	pmaxClient.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
	if err := symmetrix.Initialize([]string{symIDLocal, symIDRemote}, pmaxClient); err != nil {
		t.Fatalf("symmetrix.Initialize: %v", err)
	}
	svc := &service{
		opts: Opts{
			ClusterPrefix: clusterPrefix,
			ManagedArrays: []string{symIDLocal, symIDRemote},
		},
	}
	cleanup := func() {
		symmetrix.RemoveClient(symIDLocal)
		symmetrix.RemoveClient(symIDRemote)
	}
	return svc, pmaxClient, cleanup
}

func defaultCSIAddonsParams() map[string]string {
	return map[string]string{
		CSIAddonsParamRemoteSystem:    symIDRemote,
		CSIAddonsParamReplicationMode: Sync,
		CSIAddonsParamRdfGroupNumber:  "5",
		CSIAddonsParamNamespace:       "ns1",
	}
}

func TestNewCSIAddonsVolumeGroupServer(t *testing.T) {
	svc := &service{}
	srv := NewCSIAddonsVolumeGroupServer(svc)
	assert.NotNil(t, srv)
	assert.Same(t, svc, srv.service)
}

func TestCreateVolumeGroup_ValidationErrors(t *testing.T) {
	svc, _, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	tests := []struct {
		name string
		req  *volumegrouprpc.CreateVolumeGroupRequest
		code codes.Code
	}{
		{
			name: "missing name",
			req: &volumegrouprpc.CreateVolumeGroupRequest{
				Name:       "",
				VolumeIds:  []string{validLocalVolumeID},
				Parameters: defaultCSIAddonsParams(),
			},
			code: codes.InvalidArgument,
		},
		{
			name: "missing volume ids",
			req: &volumegrouprpc.CreateVolumeGroupRequest{
				Name:       "vg-1",
				VolumeIds:  nil,
				Parameters: defaultCSIAddonsParams(),
			},
			code: codes.InvalidArgument,
		},
		{
			name: "missing parameters",
			req: &volumegrouprpc.CreateVolumeGroupRequest{
				Name:      "vg-1",
				VolumeIds: []string{validLocalVolumeID},
			},
			code: codes.InvalidArgument,
		},
		{
			name: "metro mode rejected",
			req: &volumegrouprpc.CreateVolumeGroupRequest{
				Name:      "vg-1",
				VolumeIds: []string{validLocalVolumeID},
				Parameters: map[string]string{
					CSIAddonsParamRemoteSystem:    symIDRemote,
					CSIAddonsParamReplicationMode: Metro,
					CSIAddonsParamRdfGroupNumber:  "5",
				},
			},
			code: codes.InvalidArgument,
		},
		{
			// parseCsiID requires at least three "-" components.
			name: "invalid volume id",
			req: &volumegrouprpc.CreateVolumeGroupRequest{
				Name:       "vg-1",
				VolumeIds:  []string{"single-token"},
				Parameters: defaultCSIAddonsParams(),
			},
			code: codes.InvalidArgument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := srv.CreateVolumeGroup(context.Background(), tt.req)
			assert.Error(t, err)
			assert.Equal(t, tt.code, status.Code(err))
			assert.Nil(t, resp)
		})
	}
}

func TestCreateVolumeGroup_HappyPath_CreatesSG(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	// SG does not yet exist -> CreateStorageGroup called.
	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return(nil, errors.New("not found"))
	pmaxClient.EXPECT().
		CreateStorageGroup(gomock.Any(), symIDLocal, gomock.Any(), "None", "", false, gomock.Nil()).
		Return(&types.StorageGroup{StorageGroupID: "sg"}, nil)
	// Pre-filter: SG is empty so all volumes must be added.
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return([]string{}, nil)
	pmaxClient.EXPECT().
		AddVolumesToStorageGroup(gomock.Any(), symIDLocal, gomock.Any(), true, gomock.Any()).
		Return(nil)
	// CreateVolumeGroup must NOT call protectStorageGroupCSIAddons;
	// SRDF protection is deferred to EnableVolumeReplication.
	// No GetProtectedStorageGroup / GetRDFGroupByID / CreateSGReplica
	// expectations are registered, so gomock will fail if they fire.
	// buildVolumeGroupResponse
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return([]string{validLocalDeviceID}, nil)

	resp, err := srv.CreateVolumeGroup(context.Background(), &volumegrouprpc.CreateVolumeGroupRequest{
		Name:       "vgrcontent-foo",
		VolumeIds:  []string{validLocalVolumeID},
		Parameters: defaultCSIAddonsParams(),
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.GetVolumeGroup().GetVolumeGroupId())
	ctx := resp.GetVolumeGroup().GetVolumeGroupContext()
	assert.Equal(t, Sync, ctx[CSIAddonsParamReplicationMode])
	assert.Equal(t, symIDRemote, ctx[CSIAddonsParamRemoteSystem])
	assert.Equal(t, CSIAddonsManagedByValue, ctx[CSIAddonsManagedByLabel])
}

func TestCreateVolumeGroup_AutoDiscoverRDFGroup(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	// Parameters WITHOUT rdfGroupNumber -- triggers auto-discovery via
	// GetOrCreateRDFGroup.
	params := map[string]string{
		CSIAddonsParamRemoteSystem:    symIDRemote,
		CSIAddonsParamReplicationMode: Sync,
		CSIAddonsParamNamespace:       "ns1",
	}

	// GetOrCreateRDFGroup first queries GetRDFGroupList to find a
	// pre-existing group with a matching label.
	expectedLabel := buildRdfLabel(Sync, "ns1", clusterPrefix)
	pmaxClient.EXPECT().
		GetRDFGroupList(gomock.Any(), symIDLocal, gomock.Any()).
		Return(&types.RDFGroupList{
			RDFGroupIDs: []types.RDFGroupIDL{
				{RDFGNumber: 7, Label: expectedLabel},
			},
		}, nil)
	// GetOrCreateRDFGroup then fetches the full group info for the match.
	pmaxClient.EXPECT().
		GetRDFGroupByID(gomock.Any(), symIDLocal, "7").
		Return(&types.RDFGroup{RdfgNumber: 7, RemoteRdfgNumber: 7, NumDevices: 0}, nil)

	// Standard CreateVolumeGroup flow with the auto-discovered rdfGrpNo=7.
	// No SRDF protection expectations: that is deferred to
	// EnableVolumeReplication.
	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return(nil, errors.New("not found"))
	pmaxClient.EXPECT().
		CreateStorageGroup(gomock.Any(), symIDLocal, gomock.Any(), "None", "", false, gomock.Nil()).
		Return(&types.StorageGroup{StorageGroupID: "sg"}, nil)
	// Pre-filter: SG is empty so all volumes must be added.
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return([]string{}, nil)
	pmaxClient.EXPECT().
		AddVolumesToStorageGroup(gomock.Any(), symIDLocal, gomock.Any(), true, gomock.Any()).
		Return(nil)
	// buildVolumeGroupResponse
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return([]string{validLocalDeviceID}, nil)

	resp, err := srv.CreateVolumeGroup(context.Background(), &volumegrouprpc.CreateVolumeGroupRequest{
		Name:       "vg-autodiscover",
		VolumeIds:  []string{validLocalVolumeID},
		Parameters: params,
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.GetVolumeGroup().GetVolumeGroupId())
	// The auto-discovered RDF group number should be encoded in the VG ID.
	assert.Contains(t, resp.GetVolumeGroup().GetVolumeGroupId(), ":7")
	vgCtx := resp.GetVolumeGroup().GetVolumeGroupContext()
	assert.Equal(t, "7", vgCtx["rdfGroupNumber"])
}

func TestCreateVolumeGroup_AutoDiscoverRDFGroup_Failure(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	params := map[string]string{
		CSIAddonsParamRemoteSystem:    symIDRemote,
		CSIAddonsParamReplicationMode: Sync,
		CSIAddonsParamNamespace:       "ns1",
	}

	// GetOrCreateRDFGroup fails because GetRDFGroupList returns an error.
	pmaxClient.EXPECT().
		GetRDFGroupList(gomock.Any(), symIDLocal, gomock.Any()).
		Return(nil, errors.New("unisphere connection failed"))

	resp, err := srv.CreateVolumeGroup(context.Background(), &volumegrouprpc.CreateVolumeGroupRequest{
		Name:       "vg-fail",
		VolumeIds:  []string{validLocalVolumeID},
		Parameters: params,
	})
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestCreateVolumeGroup_SRPAndSLIgnoredForLocalSG(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	// Even when SRP and ServiceLevel are specified in the parameters,
	// CreateStorageGroup must use SRP="None" and SL="" to avoid FAST
	// policy conflicts. The volumes already belong to a FAST-enabled
	// SG; a second FAST-enabled SG would violate the PowerMax
	// constraint "A device cannot belong to more than one storage
	// group in use by FAST".
	params := defaultCSIAddonsParams()
	// SRP and ServiceLevel keys may appear in user-supplied parameters
	// but are not used by the CSI-Addons code path.
	params["replication.storage.dell.com/srp"] = "SRP_1"
	params["replication.storage.dell.com/serviceLevel"] = "Diamond"

	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return(nil, errors.New("not found"))
	// SRP and SL must NOT be forwarded; always "None" and "".
	pmaxClient.EXPECT().
		CreateStorageGroup(gomock.Any(), symIDLocal, gomock.Any(), "None", "", false, gomock.Nil()).
		Return(&types.StorageGroup{StorageGroupID: "sg"}, nil)
	// Pre-filter: SG is empty so all volumes must be added.
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return([]string{}, nil)
	pmaxClient.EXPECT().
		AddVolumesToStorageGroup(gomock.Any(), symIDLocal, gomock.Any(), true, gomock.Any()).
		Return(nil)
	// buildVolumeGroupResponse
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return([]string{validLocalDeviceID}, nil)

	resp, err := srv.CreateVolumeGroup(context.Background(), &volumegrouprpc.CreateVolumeGroupRequest{
		Name:       "vg-srp",
		VolumeIds:  []string{validLocalVolumeID},
		Parameters: params,
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestCreateVolumeGroup_ExistingSG_Idempotent(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	// SG already exists; do NOT call CreateStorageGroup.
	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return(&types.StorageGroup{StorageGroupID: "sg"}, nil)
	// Pre-filter: SG is empty so volume will be added.
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return([]string{}, nil)
	pmaxClient.EXPECT().
		AddVolumesToStorageGroup(gomock.Any(), symIDLocal, gomock.Any(), true, gomock.Any()).
		Return(nil)
	// buildVolumeGroupResponse
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return([]string{validLocalDeviceID}, nil)

	resp, err := srv.CreateVolumeGroup(context.Background(), &volumegrouprpc.CreateVolumeGroupRequest{
		Name:       "vg-x",
		VolumeIds:  []string{validLocalVolumeID},
		Parameters: defaultCSIAddonsParams(),
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestCreateVolumeGroup_VolumesAlreadyPresent_Idempotent(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	// SG exists and already contains the requested volumes.
	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return(&types.StorageGroup{StorageGroupID: "sg"}, nil)
	// Pre-filter returns the same volumes -> nothing to add.
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return([]string{validLocalDeviceID}, nil)
	// AddVolumesToStorageGroup must NOT be called.
	// buildVolumeGroupResponse
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return([]string{validLocalDeviceID}, nil)

	resp, err := srv.CreateVolumeGroup(context.Background(), &volumegrouprpc.CreateVolumeGroupRequest{
		Name:       "vg-idem",
		VolumeIds:  []string{validLocalVolumeID},
		Parameters: defaultCSIAddonsParams(),
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestDeleteVolumeGroup_IdempotentWhenSGMissing(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(nil, errors.New("not found"))

	resp, err := srv.DeleteVolumeGroup(context.Background(), &volumegrouprpc.DeleteVolumeGroupRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestDeleteVolumeGroup_R2SideIsNoOp(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.StorageGroup{StorageGroupID: "sg-x"}, nil)
	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	// SG is R2 -> the function must short-circuit.
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R2"},
			States:         []string{Consistent},
		}, nil)

	resp, err := srv.DeleteVolumeGroup(context.Background(), &volumegrouprpc.DeleteVolumeGroupRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestDeleteVolumeGroup_SourceSide_FullCleanup(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.StorageGroup{StorageGroupID: "sg-x"}, nil)
	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Consistent},
		}, nil)
	pmaxClient.EXPECT().
		GetRDFGroupByID(gomock.Any(), symIDLocal, "5").
		Return(&types.RDFGroup{RemoteSymmetrix: symIDRemote}, nil)
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionSuspend, "sg-x", "5",
			true, false, false).
		Return(nil)
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{validLocalDeviceID}, nil)
	pmaxClient.EXPECT().
		RemoveVolumesFromProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x", symIDRemote, "sg-x", true, gomock.Any()).
		Return(&types.StorageGroup{}, nil)
	pmaxClient.EXPECT().
		DeleteStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(nil)
	pmaxClient.EXPECT().
		DeleteStorageGroup(gomock.Any(), symIDRemote, "sg-x").
		Return(nil)

	resp, err := srv.DeleteVolumeGroup(context.Background(), &volumegrouprpc.DeleteVolumeGroupRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestDeleteVolumeGroup_AlreadySuspended_SkipsSuspend(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.StorageGroup{StorageGroupID: "sg-x"}, nil)
	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Suspended},
		}, nil)
	pmaxClient.EXPECT().
		GetRDFGroupByID(gomock.Any(), symIDLocal, "5").
		Return(&types.RDFGroup{RemoteSymmetrix: symIDRemote}, nil)
	// ExecuteReplicationActionOnSG (suspend) must NOT be called.
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{validLocalDeviceID}, nil)
	pmaxClient.EXPECT().
		RemoveVolumesFromProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x", symIDRemote, "sg-x", true, gomock.Any()).
		Return(&types.StorageGroup{}, nil)
	pmaxClient.EXPECT().
		DeleteStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(nil)
	pmaxClient.EXPECT().
		DeleteStorageGroup(gomock.Any(), symIDRemote, "sg-x").
		Return(nil)

	resp, err := srv.DeleteVolumeGroup(context.Background(), &volumegrouprpc.DeleteVolumeGroupRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestDeleteVolumeGroup_SuspendFailure_Errors(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.StorageGroup{StorageGroupID: "sg-x"}, nil)
	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Consistent},
		}, nil)
	pmaxClient.EXPECT().
		GetRDFGroupByID(gomock.Any(), symIDLocal, "5").
		Return(&types.RDFGroup{RemoteSymmetrix: symIDRemote}, nil)
	// Suspend fails -> teardown must abort.
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionSuspend, "sg-x", "5",
			true, false, false).
		Return(errors.New("suspend failed"))
	// No further calls expected.

	_, err := srv.DeleteVolumeGroup(context.Background(), &volumegrouprpc.DeleteVolumeGroupRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
	})
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestDeleteVolumeGroup_InvalidID(t *testing.T) {
	svc, _, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)
	_, err := srv.DeleteVolumeGroup(context.Background(), &volumegrouprpc.DeleteVolumeGroupRequest{
		VolumeGroupId: "garbage",
	})
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestModifyVolumeGroupMembership_RejectsR2Side(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R2"},
			States:         []string{Consistent},
		}, nil)

	_, err := srv.ModifyVolumeGroupMembership(context.Background(), &volumegrouprpc.ModifyVolumeGroupMembershipRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
		VolumeIds:     []string{validLocalVolumeID},
	})
	assert.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestModifyVolumeGroupMembership_AddsAndRemoves(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	// Not SRDF-protected -> skip the personality check branch.
	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: false}, nil)
	// Current members are [validLocalDeviceID, OBSOLETE]; desired is
	// [validLocalDeviceID, NEW]. So OBSOLETE should be removed and
	// NEW should be added.
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{validLocalDeviceID, "OBSLT"}, nil)
	pmaxClient.EXPECT().
		AddVolumesToStorageGroup(gomock.Any(), symIDLocal, "sg-x", true, gomock.Any()).
		Return(nil)
	pmaxClient.EXPECT().
		RemoveVolumesFromStorageGroup(gomock.Any(), symIDLocal, "sg-x", true, gomock.Any()).
		Return(&types.StorageGroup{}, nil)
	// buildVolumeGroupResponse looks up members at the end.
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{validLocalDeviceID, "NEWDV"}, nil)

	// Construct a new CSI id pointing at "NEWDV".
	newVolID := fmt.Sprintf("%s%s-%s-%s-%s", CsiVolumePrefix, clusterPrefix, "newvol", symIDLocal, "NEWDV")
	resp, err := srv.ModifyVolumeGroupMembership(context.Background(), &volumegrouprpc.ModifyVolumeGroupMembershipRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
		VolumeIds:     []string{validLocalVolumeID, newVolID},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestModifyVolumeGroupMembership_ProtectedSG_BreaksSRDFPairs(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	// SG is SRDF-protected and R1.
	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Suspended},
		}, nil)
	pmaxClient.EXPECT().
		GetRDFGroupByID(gomock.Any(), symIDLocal, "5").
		Return(&types.RDFGroup{RemoteSymmetrix: symIDRemote}, nil)
	// Current members: [validLocalDeviceID]; desired: [] -> remove all.
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{validLocalDeviceID}, nil)
	// Must use the protected-SG API to break SRDF pairs.
	pmaxClient.EXPECT().
		RemoveVolumesFromProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x", symIDRemote, "sg-x", true, gomock.Any()).
		Return(&types.StorageGroup{}, nil)
	// buildVolumeGroupResponse
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{}, nil)

	resp, err := srv.ModifyVolumeGroupMembership(context.Background(), &volumegrouprpc.ModifyVolumeGroupMembershipRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
		VolumeIds:     []string{},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestModifyVolumeGroupMembership_ProtectedSG_AddCreatesSRDFPairs(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	// SG is SRDF-protected and R1.
	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Consistent},
		}, nil)
	pmaxClient.EXPECT().
		GetRDFGroupByID(gomock.Any(), symIDLocal, "5").
		Return(&types.RDFGroup{RemoteSymmetrix: symIDRemote}, nil)
	// Current members: []; desired: [validLocalDeviceID] -> add one.
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{}, nil)
	// Must use protected-SG API to create SRDF pairs for new volumes.
	pmaxClient.EXPECT().
		AddVolumesToProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x", symIDRemote, "sg-x", true, gomock.Any()).
		Return(nil)
	// buildVolumeGroupResponse
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{validLocalDeviceID}, nil)

	resp, err := srv.ModifyVolumeGroupMembership(context.Background(), &volumegrouprpc.ModifyVolumeGroupMembershipRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
		VolumeIds:     []string{validLocalVolumeID},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.GetVolumeGroup().GetVolumes(), 1)
}

func TestModifyVolumeGroupMembership_ProtectedSG_AddAndRemove(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	// SG is SRDF-protected and R1.
	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Consistent},
		}, nil)
	pmaxClient.EXPECT().
		GetRDFGroupByID(gomock.Any(), symIDLocal, "5").
		Return(&types.RDFGroup{RemoteSymmetrix: symIDRemote}, nil)
	// Current members: [OBSLT]; desired: [validLocalDeviceID] -> add new, remove old.
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{"OBSLT"}, nil)
	// Both add and remove must use protected-SG APIs.
	pmaxClient.EXPECT().
		AddVolumesToProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x", symIDRemote, "sg-x", true, gomock.Any()).
		Return(nil)
	pmaxClient.EXPECT().
		RemoveVolumesFromProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x", symIDRemote, "sg-x", true, gomock.Any()).
		Return(&types.StorageGroup{}, nil)
	// buildVolumeGroupResponse
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{validLocalDeviceID}, nil)

	resp, err := srv.ModifyVolumeGroupMembership(context.Background(), &volumegrouprpc.ModifyVolumeGroupMembershipRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
		VolumeIds:     []string{validLocalVolumeID},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.GetVolumeGroup().GetVolumes(), 1)
}

func TestControllerGetVolumeGroup_NotFound(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)
	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(nil, errors.New("404"))

	_, err := srv.ControllerGetVolumeGroup(context.Background(), &volumegrouprpc.ControllerGetVolumeGroupRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
	})
	assert.Error(t, err)
	assert.Equal(t, codes.NotFound, status.Code(err))
}

func TestControllerGetVolumeGroup_Found(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)
	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.StorageGroup{StorageGroupID: "sg-x"}, nil)
	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{validLocalDeviceID}, nil)

	resp, err := srv.ControllerGetVolumeGroup(context.Background(), &volumegrouprpc.ControllerGetVolumeGroupRequest{
		VolumeGroupId: encodeVolumeGroupID(symIDLocal, "sg-x", "5"),
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.GetVolumeGroup().GetVolumes())
}

func TestListVolumeGroups_OnlyManagedSGsReturned(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	managedSG := csiAddonsManagedSGPrefix + "pfx-ns1-5-" + Sync

	// Local array returns one managed SG and one unrelated SG; only the
	// managed SG must appear in the response.
	pmaxClient.EXPECT().
		GetStorageGroupIDList(gomock.Any(), symIDLocal, csiAddonsManagedSGPrefix, true).
		Return(&types.StorageGroupIDList{StorageGroupIDs: []string{managedSG, "some-application-sg"}}, nil)
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, managedSG).
		Return([]string{validLocalDeviceID}, nil)
	// Remote array has no CSI-Addons SGs.
	pmaxClient.EXPECT().
		GetStorageGroupIDList(gomock.Any(), symIDRemote, csiAddonsManagedSGPrefix, true).
		Return(&types.StorageGroupIDList{StorageGroupIDs: []string{}}, nil)

	resp, err := srv.ListVolumeGroups(context.Background(), &volumegrouprpc.ListVolumeGroupsRequest{})
	assert.NoError(t, err)
	assert.Len(t, resp.GetEntries(), 1)
	vg := resp.GetEntries()[0].GetVolumeGroup()
	assert.Equal(t, encodeVolumeGroupID(symIDLocal, managedSG, "5"), vg.GetVolumeGroupId())
	assert.Equal(t, Sync, vg.GetVolumeGroupContext()[CSIAddonsParamReplicationMode])
	assert.Len(t, vg.GetVolumes(), 1)
}

func TestListVolumeGroups_Pagination(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	// Two managed SGs on the local array; names chosen so the deterministic
	// (sorted) order is sg1 then sg2.
	sg1 := csiAddonsManagedSGPrefix + "a-ns1-5-" + Sync
	sg2 := csiAddonsManagedSGPrefix + "b-ns1-5-" + Sync

	pmaxClient.EXPECT().
		GetStorageGroupIDList(gomock.Any(), symIDLocal, csiAddonsManagedSGPrefix, true).
		Return(&types.StorageGroupIDList{StorageGroupIDs: []string{sg2, sg1}}, nil).Times(2)
	pmaxClient.EXPECT().
		GetStorageGroupIDList(gomock.Any(), symIDRemote, csiAddonsManagedSGPrefix, true).
		Return(&types.StorageGroupIDList{StorageGroupIDs: []string{}}, nil).Times(2)
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, sg1).
		Return([]string{validLocalDeviceID}, nil).Times(2)
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, sg2).
		Return([]string{validLocalDeviceID}, nil).Times(2)

	// First page: max_entries=1 -> first SG, next_token points to page 2.
	page1, err := srv.ListVolumeGroups(context.Background(), &volumegrouprpc.ListVolumeGroupsRequest{MaxEntries: 1})
	assert.NoError(t, err)
	assert.Len(t, page1.GetEntries(), 1)
	assert.Equal(t, encodeVolumeGroupID(symIDLocal, sg1, "5"), page1.GetEntries()[0].GetVolumeGroup().GetVolumeGroupId())
	assert.Equal(t, "1", page1.GetNextToken())

	// Second page: starting_token from page 1 -> second SG, no next token.
	page2, err := srv.ListVolumeGroups(context.Background(), &volumegrouprpc.ListVolumeGroupsRequest{
		MaxEntries:    1,
		StartingToken: page1.GetNextToken(),
	})
	assert.NoError(t, err)
	assert.Len(t, page2.GetEntries(), 1)
	assert.Equal(t, encodeVolumeGroupID(symIDLocal, sg2, "5"), page2.GetEntries()[0].GetVolumeGroup().GetVolumeGroupId())
	assert.Empty(t, page2.GetNextToken())
}

func TestListVolumeGroups_NegativeMaxEntriesRejected(t *testing.T) {
	svc, _, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	_, err := srv.ListVolumeGroups(context.Background(), &volumegrouprpc.ListVolumeGroupsRequest{MaxEntries: -1})
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestListVolumeGroups_ArrayErrorSkipped(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)

	// Both arrays fail to list -> empty (non-nil) response, no error.
	pmaxClient.EXPECT().
		GetStorageGroupIDList(gomock.Any(), gomock.Any(), csiAddonsManagedSGPrefix, true).
		Return(nil, errors.New("unisphere down")).Times(2)

	resp, err := srv.ListVolumeGroups(context.Background(), &volumegrouprpc.ListVolumeGroupsRequest{})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Empty(t, resp.GetEntries())
}

func TestRegisterCSIAddonsVolumeGroupServer(t *testing.T) {
	svc, _, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsVolumeGroupServer(svc)
	grpcSrv := grpc.NewServer()
	defer grpcSrv.Stop()
	// Must not panic.
	RegisterCSIAddonsVolumeGroupServer(grpcSrv, srv)
}

func TestDeleteUnprotectedSG(t *testing.T) {
	symID := symIDLocal
	sgName := "test-sg"

	t.Run("no volumes (list error), delete succeeds", func(t *testing.T) {
		svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
		defer cleanup()
		srv := NewCSIAddonsVolumeGroupServer(svc)

		pmaxClient.EXPECT().
			GetVolumeIDListInStorageGroup(gomock.Any(), symID, sgName).
			Return(nil, errors.New("empty"))
		pmaxClient.EXPECT().
			DeleteStorageGroup(gomock.Any(), symID, sgName).
			Return(nil)

		resp, err := srv.deleteUnprotectedSG(context.Background(), pmaxClient, symID, sgName)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("volumes removed then SG deleted", func(t *testing.T) {
		svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
		defer cleanup()
		srv := NewCSIAddonsVolumeGroupServer(svc)

		pmaxClient.EXPECT().
			GetVolumeIDListInStorageGroup(gomock.Any(), symID, sgName).
			Return([]string{"00001", "00002"}, nil)
		pmaxClient.EXPECT().
			RemoveVolumesFromStorageGroup(gomock.Any(), symID, sgName, true, gomock.Any()).
			Return(nil, nil)
		pmaxClient.EXPECT().
			DeleteStorageGroup(gomock.Any(), symID, sgName).
			Return(nil)

		resp, err := srv.deleteUnprotectedSG(context.Background(), pmaxClient, symID, sgName)
		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("RemoveVolumes fails -> error", func(t *testing.T) {
		svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
		defer cleanup()
		srv := NewCSIAddonsVolumeGroupServer(svc)

		pmaxClient.EXPECT().
			GetVolumeIDListInStorageGroup(gomock.Any(), symID, sgName).
			Return([]string{"00001"}, nil)
		pmaxClient.EXPECT().
			RemoveVolumesFromStorageGroup(gomock.Any(), symID, sgName, true, gomock.Any()).
			Return(nil, errors.New("remove error"))

		_, err := srv.deleteUnprotectedSG(context.Background(), pmaxClient, symID, sgName)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
	})

	t.Run("DeleteStorageGroup fails -> error", func(t *testing.T) {
		svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
		defer cleanup()
		srv := NewCSIAddonsVolumeGroupServer(svc)

		pmaxClient.EXPECT().
			GetVolumeIDListInStorageGroup(gomock.Any(), symID, sgName).
			Return(nil, errors.New("empty"))
		pmaxClient.EXPECT().
			DeleteStorageGroup(gomock.Any(), symID, sgName).
			Return(errors.New("delete error"))

		_, err := srv.deleteUnprotectedSG(context.Background(), pmaxClient, symID, sgName)
		assert.Error(t, err)
		assert.Equal(t, codes.Internal, status.Code(err))
	})
}

func TestGetExistingVolumeGroupResponse(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	server := NewCSIAddonsVolumeGroupServer(svc)
	params := &csiAddonsReplicationParams{
		rdfGroupNumber:    "5",
		replicationMode:   Sync,
		volumeGroupPrefix: "vg",
		namespace:         "ns1",
		remoteSymID:       symIDRemote,
	}
	sgName := buildCSIAddonsSGName(params.volumeGroupPrefix, params.namespace, params.rdfGroupNumber, params.replicationMode)
	pmaxClient.EXPECT().GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, sgName).Return([]string{"00001", "00002"}, nil)

	resp, err := server.getExistingVolumeGroupResponse(context.Background(), symIDLocal, params)
	assert.NoError(t, err)
	if assert.NotNil(t, resp) && assert.NotNil(t, resp.VolumeGroup) {
		assert.Len(t, resp.VolumeGroup.Volumes, 2)
		assert.Equal(t, symIDRemote, resp.VolumeGroup.VolumeGroupContext[CSIAddonsParamRemoteSystem])
	}
}

func TestGetExistingVolumeGroupResponseRequiresRDFGroup(t *testing.T) {
	svc, _, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	server := NewCSIAddonsVolumeGroupServer(svc)
	_, err := server.getExistingVolumeGroupResponse(context.Background(), symIDLocal, &csiAddonsReplicationParams{})
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetExistingVolumeGroupResponseClientError(t *testing.T) {
	server := NewCSIAddonsVolumeGroupServer(&service{})
	_, err := server.getExistingVolumeGroupResponse(context.Background(), "unregistered", &csiAddonsReplicationParams{rdfGroupNumber: "5"})
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestBuildVolumeGroupResponseFallsBackToPreferredVolumes(t *testing.T) {
	pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
	sgName := buildCSIAddonsSGName("vg", "ns1", "5", Sync)
	pmaxClient.EXPECT().GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, sgName).Return(nil, errors.New("lookup failed"))
	server := NewCSIAddonsVolumeGroupServer(&service{})
	resp := server.buildVolumeGroupResponse(context.Background(), pmaxClient, symIDLocal, sgName, "", &csiAddonsReplicationParams{}, []string{"vol-1", "vol-2"})
	if assert.NotNil(t, resp) && assert.NotNil(t, resp.VolumeGroup) {
		assert.Len(t, resp.VolumeGroup.Volumes, 2)
		assert.Equal(t, "vol-1", resp.VolumeGroup.Volumes[0].VolumeId)
		assert.Equal(t, Sync, resp.VolumeGroup.VolumeGroupContext[CSIAddonsParamReplicationMode])
	}
}
