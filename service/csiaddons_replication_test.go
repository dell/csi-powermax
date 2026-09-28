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
	"testing"

	types "github.com/dell/gopowermax/v2/types/v100"
	csiaddonsreplication "github.com/csi-addons/spec/lib/go/replication"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func vgSource(vgID string) *csiaddonsreplication.ReplicationSource {
	return &csiaddonsreplication.ReplicationSource{
		Type: &csiaddonsreplication.ReplicationSource_Volumegroup{
			Volumegroup: &csiaddonsreplication.ReplicationSource_VolumeGroupSource{
				VolumeGroupId: vgID,
			},
		},
	}
}

func volumeSource(volID string) *csiaddonsreplication.ReplicationSource {
	return &csiaddonsreplication.ReplicationSource{
		Type: &csiaddonsreplication.ReplicationSource_Volume{
			Volume: &csiaddonsreplication.ReplicationSource_VolumeSource{
				VolumeId: volID,
			},
		},
	}
}

// testManagedSG is a CSI-Addons managed storage group name (carries the
// csi-rep-sg-addons- prefix) used by tests that exercise the co-existence
// guard in DisableVolumeReplication.
var testManagedSG = csiAddonsManagedSGPrefix + "vg-ns1-5-" + Sync

func TestNewCSIAddonsReplicationServer(t *testing.T) {
	svc := &service{}
	srv := NewCSIAddonsReplicationServer(svc)
	assert.NotNil(t, srv)
	assert.Same(t, svc, srv.service)
}

func TestResolveVGFromSource(t *testing.T) {
	srv := NewCSIAddonsReplicationServer(&service{})

	_, err := srv.resolveVGFromSource(nil)
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// Volume-level rejected.
	_, err = srv.resolveVGFromSource(volumeSource(validLocalVolumeID))
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))

	// VG-level accepted.
	vgID := encodeVolumeGroupID(symIDLocal, "sg-x", "5")
	got, err := srv.resolveVGFromSource(vgSource(vgID))
	assert.NoError(t, err)
	assert.Equal(t, vgID, got)
}

func TestEnableVolumeReplication_RejectsMetro(t *testing.T) {
	svc, _, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	_, err := srv.EnableVolumeReplication(context.Background(), &csiaddonsreplication.EnableVolumeReplicationRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
		Parameters: map[string]string{
			CSIAddonsParamRemoteSystem:    symIDRemote,
			CSIAddonsParamReplicationMode: Metro,
		},
	})
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), metroErrorMessage)
}

func TestEnableVolumeReplication_AlreadyProtected_Idempotent(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	// CreateSGReplica MUST NOT be called.

	// syncRemoteVolumeIdentifiers still runs on the idempotent path
	// (to fix up any R2 volumes that were not renamed on a prior attempt).
	// R2 already has the correct identifier -> no RenameVolume call.
	pmaxClient.EXPECT().
		GetVersionDetails(gomock.Any()).
		Return(api103Version(), nil).
		AnyTimes()
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validLocalDeviceID, Identifier: localVolumeName}}}, nil)
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDRemote, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validRemoteDeviceID, Identifier: localVolumeName}}}, nil)
	pmaxClient.EXPECT().
		GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "5", validLocalDeviceID).
		Return(&types.RDFDevicePair{RemoteVolumeName: validRemoteDeviceID}, nil)
	// RenameVolume NOT called because R2 identifier already matches R1.

	resp, err := srv.EnableVolumeReplication(context.Background(), &csiaddonsreplication.EnableVolumeReplicationRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
		Parameters: map[string]string{
			CSIAddonsParamRemoteSystem:    symIDRemote,
			CSIAddonsParamReplicationMode: Sync,
		},
	})
	assert.NoError(t, err)
	assert.NotNil(t, resp)
}

func TestEnableVolumeReplication_NewProtectionCallsCreateSGReplica(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: false}, nil)
	// protectStorageGroupCSIAddons validates the RDF group ...
	pmaxClient.EXPECT().
		GetRDFGroupByID(gomock.Any(), symIDLocal, "5").
		Return(&types.RDFGroup{NumDevices: 0}, nil)
	// ... and clears any stale (empty) remote SG before protecting.
	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDRemote, "sg-x").
		Return(nil, errors.New("not found"))
	pmaxClient.EXPECT().
		CreateSGReplica(gomock.Any(), symIDLocal, symIDRemote, Async, "5",
			"sg-x", "sg-x", "", false).
		Return(&types.SGRDFInfo{}, nil)
	// syncRemoteVolumeIdentifiers: gather identifiers, get pair, rename R2.
	pmaxClient.EXPECT().
		GetVersionDetails(gomock.Any()).
		Return(api103Version(), nil).
		AnyTimes()
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validLocalDeviceID, Identifier: localVolumeName}}}, nil)
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDRemote, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validRemoteDeviceID, Identifier: ""}}}, nil)
	pmaxClient.EXPECT().
		GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "5", validLocalDeviceID).
		Return(&types.RDFDevicePair{RemoteVolumeName: validRemoteDeviceID}, nil)
	pmaxClient.EXPECT().
		RenameVolume(gomock.Any(), symIDRemote, validRemoteDeviceID, localVolumeName).
		Return(&types.Volume{VolumeID: validRemoteDeviceID, VolumeIdentifier: localVolumeName}, nil)

	_, err := srv.EnableVolumeReplication(context.Background(), &csiaddonsreplication.EnableVolumeReplicationRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
		Parameters: map[string]string{
			CSIAddonsParamRemoteSystem:    symIDRemote,
			CSIAddonsParamReplicationMode: Async,
		},
	})
	assert.NoError(t, err)
}

func TestEnableVolumeReplication_RejectsAsyncRDFGroupWithPairings(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.RDFStorageGroup{Rdf: false}, nil)
	// An ASYNC RDF group that already carries device pairings cannot be
	// reused, so protection must be rejected before CreateSGReplica.
	pmaxClient.EXPECT().
		GetRDFGroupByID(gomock.Any(), symIDLocal, "5").
		Return(&types.RDFGroup{Async: true, NumDevices: 3}, nil)

	_, err := srv.EnableVolumeReplication(context.Background(), &csiaddonsreplication.EnableVolumeReplicationRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
		Parameters: map[string]string{
			CSIAddonsParamRemoteSystem:    symIDRemote,
			CSIAddonsParamReplicationMode: Async,
		},
	})
	assert.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestEnableVolumeReplication_EmptyRDFGroup_Rejected(t *testing.T) {
	svc, _, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	// Volume group ID with an empty RDF group is rejected because
	// auto-discovery is only performed in CreateVolumeGroup.
	vgID := encodeVolumeGroupID(symIDLocal, "sg-x", "")

	_, err := srv.EnableVolumeReplication(context.Background(), &csiaddonsreplication.EnableVolumeReplicationRequest{
		ReplicationSource: vgSource(vgID),
		Parameters: map[string]string{
			CSIAddonsParamRemoteSystem:    symIDRemote,
			CSIAddonsParamReplicationMode: Sync,
			CSIAddonsParamNamespace:       "ns1",
		},
	})
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
	assert.Contains(t, err.Error(), "rdfGroupNumber must be encoded")
}

// ─── syncRemoteVolumeIdentifiers unit tests ─────────────────────────────────

// api103Version returns a VersionDetails for Unisphere 10.3.0.2 (APIVersion "103").
// It satisfies the 10.1 bulk identifier lookup threshold, so the bulk GET endpoint is used.
func api103Version() *types.VersionDetails {
	return &types.VersionDetails{Version: "10.3.0.2", APIVersion: "103"}
}

func TestSyncRemoteVolumeIdentifiers_EmptySG_NoOp(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	pmaxClient.EXPECT().
		GetVersionDetails(gomock.Any()).
		Return(api103Version(), nil).
		AnyTimes()
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{}}, nil)

	err := svc.syncRemoteVolumeIdentifiers(context.Background(),
		symIDLocal, symIDRemote, "sg-x", "5", pmaxClient)
	assert.NoError(t, err)
}

func TestSyncRemoteVolumeIdentifiers_RenamesR2Volume_Legacy(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	// Per-volume rename path; identifier lookup uses the 10.1+ bulk GET.
	pmaxClient.EXPECT().
		GetVersionDetails(gomock.Any()).
		Return(api103Version(), nil).
		AnyTimes()
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validLocalDeviceID, Identifier: localVolumeName}}}, nil)
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDRemote, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validRemoteDeviceID, Identifier: ""}}}, nil)
	pmaxClient.EXPECT().
		GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "5", validLocalDeviceID).
		Return(&types.RDFDevicePair{RemoteVolumeName: validRemoteDeviceID}, nil)
	pmaxClient.EXPECT().
		RenameVolume(gomock.Any(), symIDRemote, validRemoteDeviceID, localVolumeName).
		Return(&types.Volume{VolumeID: validRemoteDeviceID, VolumeIdentifier: localVolumeName}, nil)

	err := svc.syncRemoteVolumeIdentifiers(context.Background(),
		symIDLocal, symIDRemote, "sg-x", "5", pmaxClient)
	assert.NoError(t, err)
}

func TestSyncRemoteVolumeIdentifiers_SkipsAlreadyCorrect(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	pmaxClient.EXPECT().
		GetVersionDetails(gomock.Any()).
		Return(api103Version(), nil).
		AnyTimes()
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validLocalDeviceID, Identifier: localVolumeName}}}, nil)
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDRemote, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validRemoteDeviceID, Identifier: localVolumeName}}}, nil)
	pmaxClient.EXPECT().
		GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "5", validLocalDeviceID).
		Return(&types.RDFDevicePair{RemoteVolumeName: validRemoteDeviceID}, nil)
	// RenameVolume NOT called because R2 identifier already matches R1.

	err := svc.syncRemoteVolumeIdentifiers(context.Background(),
		symIDLocal, symIDRemote, "sg-x", "5", pmaxClient)
	assert.NoError(t, err)
}

func TestSyncRemoteVolumeIdentifiers_RenameFailure_ReturnsError(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	pmaxClient.EXPECT().
		GetVersionDetails(gomock.Any()).
		Return(api103Version(), nil).
		AnyTimes()
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validLocalDeviceID, Identifier: localVolumeName}}}, nil)
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDRemote, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validRemoteDeviceID, Identifier: ""}}}, nil)
	pmaxClient.EXPECT().
		GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "5", validLocalDeviceID).
		Return(&types.RDFDevicePair{RemoteVolumeName: validRemoteDeviceID}, nil)
	pmaxClient.EXPECT().
		RenameVolume(gomock.Any(), symIDRemote, validRemoteDeviceID, localVolumeName).
		Return(nil, errors.New("rename API error"))

	err := svc.syncRemoteVolumeIdentifiers(context.Background(),
		symIDLocal, symIDRemote, "sg-x", "5", pmaxClient)
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Contains(t, err.Error(), "could not be renamed")
}

func TestSyncRemoteVolumeIdentifiers_MultipleVolumes_PartialFailure(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	dev1 := "01AAA"
	dev2 := "01BBB"
	rDev1 := "02AAA"

	pmaxClient.EXPECT().
		GetVersionDetails(gomock.Any()).
		Return(api103Version(), nil).
		AnyTimes()
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{
			{ID: dev1, Identifier: "vol-1"},
			{ID: dev2, Identifier: "vol-2"},
		}}, nil)
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDRemote, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{
			{ID: rDev1, Identifier: ""},
			{ID: "02BBB", Identifier: ""},
		}}, nil)
	pmaxClient.EXPECT().
		GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "5", dev1).
		Return(&types.RDFDevicePair{RemoteVolumeName: rDev1}, nil)
	// Volume 2: RDF pair lookup fails during gather.
	pmaxClient.EXPECT().
		GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "5", dev2).
		Return(nil, errors.New("pair not found"))

	// Per-volume rename of volume 1 succeeds.
	pmaxClient.EXPECT().
		RenameVolume(gomock.Any(), symIDRemote, rDev1, "vol-1").
		Return(&types.Volume{VolumeID: rDev1, VolumeIdentifier: "vol-1"}, nil)

	err := svc.syncRemoteVolumeIdentifiers(context.Background(),
		symIDLocal, symIDRemote, "sg-x", "5", pmaxClient)
	// Returns error because volume 2 failed during gather.
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestSyncRemoteVolumeIdentifiers_LocalVolumeNoIdentifier_Skips(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	pmaxClient.EXPECT().
		GetVersionDetails(gomock.Any()).
		Return(api103Version(), nil).
		AnyTimes()
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validLocalDeviceID, Identifier: ""}}}, nil)
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDRemote, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{}}, nil)
	// No further calls expected -- R1 has no identifier to propagate.

	err := svc.syncRemoteVolumeIdentifiers(context.Background(),
		symIDLocal, symIDRemote, "sg-x", "5", pmaxClient)
	assert.NoError(t, err)
}

func TestSyncRemoteVolumeIdentifiers_ListVolumesFails_ReturnsError(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	pmaxClient.EXPECT().
		GetVersionDetails(gomock.Any()).
		Return(nil, errors.New("version unavailable")).
		AnyTimes()
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(nil, errors.New("SG not found"))

	err := svc.syncRemoteVolumeIdentifiers(context.Background(),
		symIDLocal, symIDRemote, "sg-x", "5", pmaxClient)
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Contains(t, err.Error(), "failed to list volumes")
}

func TestSyncRemoteVolumeIdentifiers_VersionError_FallsBackToPerVolume(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	// GetVersionDetails fails → falls back to per-volume identifier lookup and rename.
	pmaxClient.EXPECT().
		GetVersionDetails(gomock.Any()).
		Return(nil, errors.New("version unavailable")).
		AnyTimes()
	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{validLocalDeviceID}, nil)
	pmaxClient.EXPECT().
		GetVolumeByID(gomock.Any(), symIDLocal, validLocalDeviceID).
		Return(&types.Volume{VolumeID: validLocalDeviceID, VolumeIdentifier: localVolumeName}, nil)
	pmaxClient.EXPECT().
		GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "5", validLocalDeviceID).
		Return(&types.RDFDevicePair{RemoteVolumeName: validRemoteDeviceID}, nil)
	pmaxClient.EXPECT().
		GetVolumeByID(gomock.Any(), symIDRemote, validRemoteDeviceID).
		Return(&types.Volume{VolumeID: validRemoteDeviceID, VolumeIdentifier: ""}, nil)
	pmaxClient.EXPECT().
		RenameVolume(gomock.Any(), symIDRemote, validRemoteDeviceID, localVolumeName).
		Return(&types.Volume{VolumeID: validRemoteDeviceID, VolumeIdentifier: localVolumeName}, nil)

	err := svc.syncRemoteVolumeIdentifiers(context.Background(),
		symIDLocal, symIDRemote, "sg-x", "5", pmaxClient)
	assert.NoError(t, err)
}

func TestDisableVolumeReplication_NotProtected_Idempotent(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, testManagedSG).
		Return(&types.RDFStorageGroup{Rdf: false}, nil)
	// No further calls expected.

	_, err := srv.DisableVolumeReplication(context.Background(), &csiaddonsreplication.DisableVolumeReplicationRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, testManagedSG, "5")),
	})
	assert.NoError(t, err)
}

func TestDisableVolumeReplication_RejectsNonManagedSG(t *testing.T) {
	svc, _, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	// "sg-x" is not a CSI-Addons managed SG -> must be refused before any
	// array call.
	_, err := srv.DisableVolumeReplication(context.Background(), &csiaddonsreplication.DisableVolumeReplicationRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestDisableVolumeReplication_AlreadySuspended_Idempotent(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, testManagedSG).
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, testManagedSG, "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Suspended},
		}, nil)
	// ExecuteReplicationActionOnSG MUST NOT be called.

	_, err := srv.DisableVolumeReplication(context.Background(), &csiaddonsreplication.DisableVolumeReplicationRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, testManagedSG, "5")),
	})
	assert.NoError(t, err)
}

func TestDisableVolumeReplication_SuspendsActiveSession(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, testManagedSG).
		Return(&types.RDFStorageGroup{Rdf: true, RDFGroups: []int{5}}, nil)
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, testManagedSG, "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Synchronized},
		}, nil)
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionSuspend, testManagedSG, "5", true, false, false).
		Return(nil)

	_, err := srv.DisableVolumeReplication(context.Background(), &csiaddonsreplication.DisableVolumeReplicationRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, testManagedSG, "5")),
	})
	assert.NoError(t, err)
}

func TestPromoteVolume_AlreadyR1_Idempotent(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Synchronized},
		}, nil)
	// ExecuteReplicationActionOnSG MUST NOT be called.

	_, err := srv.PromoteVolume(context.Background(), &csiaddonsreplication.PromoteVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
}

func TestPromoteVolume_PlannedFailoverAndSwap(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R2"},
			States:         []string{Consistent},
		}, nil)
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionFailover, "sg-x", "5", false, true /* exemptConsistency */, false).
		Return(nil)
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionSwap, "sg-x", "5", false, false, false).
		Return(nil)

	_, err := srv.PromoteVolume(context.Background(), &csiaddonsreplication.PromoteVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
}

func TestPromoteVolume_UnplannedSkipsSwap(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R2"},
			States:         []string{Suspended, Consistent}, // mixed allowed when force=true
		}, nil)
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionFailover, "sg-x", "5", true, true /* exemptConsistency */, false).
		Return(nil)
	// Swap MUST NOT be called.

	_, err := srv.PromoteVolume(context.Background(), &csiaddonsreplication.PromoteVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
		Force:             true,
	})
	assert.NoError(t, err)
}

func TestPromoteVolume_MixedPersonalities_Rejected(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1", "R2"},
			States:         []string{Consistent, Consistent},
		}, nil)

	_, err := srv.PromoteVolume(context.Background(), &csiaddonsreplication.PromoteVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestPromoteVolume_MixedStatesRequireForce(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R2", "R2"},
			States:         []string{Consistent, Suspended},
		}, nil)

	_, err := srv.PromoteVolume(context.Background(), &csiaddonsreplication.PromoteVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestDemoteVolume_AlreadyR2_Idempotent(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R2"},
			States:         []string{Consistent},
		}, nil)
	// No SRDF action expected.

	_, err := srv.DemoteVolume(context.Background(), &csiaddonsreplication.DemoteVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
}

func TestDemoteVolume_R1Consistent_FailoverIssued(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Consistent},
		}, nil)
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionFailover, "sg-x", "5", false, true /* exemptConsistency */, false).
		Return(nil)

	_, err := srv.DemoteVolume(context.Background(), &csiaddonsreplication.DemoteVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
}

func TestResyncVolume_AlreadyHealthyReturnsReady(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Synchronized},
		}, nil)

	resp, err := srv.ResyncVolume(context.Background(), &csiaddonsreplication.ResyncVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
	assert.True(t, resp.GetReady())
}

func TestResyncVolume_ActiveBias_ReturnsReady(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	// ActiveBias is treated as a healthy/synchronized state.
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{ActiveBias},
		}, nil)

	resp, err := srv.ResyncVolume(context.Background(), &csiaddonsreplication.ResyncVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
	assert.True(t, resp.GetReady())
}

func TestResyncVolume_Partitioned_IssuesResume(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	// Partitioned (link down) recovers via Resume.
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Partitioned},
		}, nil)
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionResume, "sg-x", "5", false, true /* exemptConsistency */, false).
		Return(nil)

	resp, err := srv.ResyncVolume(context.Background(), &csiaddonsreplication.ResyncVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
	assert.False(t, resp.GetReady())
}

func TestResyncVolume_Suspended_IssuesResume(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Suspended},
		}, nil)
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionResume, "sg-x", "5", false, true /* exemptConsistency */, false).
		Return(nil)

	resp, err := srv.ResyncVolume(context.Background(), &csiaddonsreplication.ResyncVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
	assert.False(t, resp.GetReady())
}

func TestResyncVolume_FailedOver_IssuesSwap(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{FailedOver},
		}, nil)
	// After demote the link is FailedOver; ResyncVolume issues a Swap to
	// relabel R1/R2 and transition the link to Suspended, from which the
	// next poll will Resume -> SyncInProgress -> Consistent.
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionSwap, "sg-x", "5", false, false /* exemptConsistency */, false).
		Return(nil)

	resp, err := srv.ResyncVolume(context.Background(), &csiaddonsreplication.ResyncVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
	assert.False(t, resp.GetReady())
}

func TestResyncVolume_StillPrimary_Consistent_ReturnsReady(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	// If ResyncVolume is called before DemoteVolume (or when demotion was
	// not required), the side is still R1 and healthy -> idempotent Ready.
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Consistent},
		}, nil)
	// No SRDF action expected.

	resp, err := srv.ResyncVolume(context.Background(), &csiaddonsreplication.ResyncVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
	assert.True(t, resp.GetReady())
}

func TestResyncVolume_Split_IssuesEstablish(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{Split},
		}, nil)
	// A Split link must be re-paired with Establish (design 4.7), not
	// Resume. Establish does not exempt consistency.
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionEstablish, "sg-x", "5", false, false, false).
		Return(nil)

	resp, err := srv.ResyncVolume(context.Background(), &csiaddonsreplication.ResyncVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
	assert.False(t, resp.GetReady())
}

func TestResyncVolume_SyncInProgress_WaitsNoAction(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{SyncInProgress},
		}, nil)
	// No SRDF action must be issued while a sync is in progress.

	resp, err := srv.ResyncVolume(context.Background(), &csiaddonsreplication.ResyncVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
	assert.False(t, resp.GetReady())
}

func TestResyncVolume_UnknownState_WaitsNoAction(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{"SomeUnhandledState"},
		}, nil)
	// An unhandled state must not trigger a blind Establish.

	resp, err := srv.ResyncVolume(context.Background(), &csiaddonsreplication.ResyncVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
	assert.False(t, resp.GetReady())
}

func TestPromoteVolume_R2AlreadyFailedOver_Idempotent(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R2"},
			States:         []string{FailedOver},
		}, nil)
	// R2 in FailedOver -> already promoted, no Failover must be re-issued,
	// even with force=true.

	_, err := srv.PromoteVolume(context.Background(), &csiaddonsreplication.PromoteVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
		Force:             true,
	})
	assert.NoError(t, err)
}

func TestPromoteVolume_R1FailedOver_IssuesFailback(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	// R1 in FailedOver means a prior DemoteVolume (Failover) transferred
	// control to R2 and the resync loop has not run yet. PromoteVolume
	// must issue a Failback to reclaim R1 as primary.
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{FailedOver},
		}, nil)
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionFailback, "sg-x", "5", false, true /* exemptConsistency */, false).
		Return(nil)

	_, err := srv.PromoteVolume(context.Background(), &csiaddonsreplication.PromoteVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
}

func TestPromoteVolume_R1FailedOver_FailbackError(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{FailedOver},
		}, nil)
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, csiAddonsActionFailback, "sg-x", "5", false, true, false).
		Return(errors.New("failback rejected"))

	_, err := srv.PromoteVolume(context.Background(), &csiaddonsreplication.PromoteVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
	assert.Contains(t, err.Error(), "failback")
}

func TestDemoteVolume_MixedStates_Rejected(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1", "R1"},
			States:         []string{Consistent, Suspended},
		}, nil)
	// A planned failover (demote) cannot run on mixed SRDF states.

	_, err := srv.DemoteVolume(context.Background(), &csiaddonsreplication.DemoteVolumeRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestGetVolumeReplicationInfo_StatusMapping(t *testing.T) {
	tests := []struct {
		name       string
		state      string
		wantStatus csiaddonsreplication.GetVolumeReplicationInfoResponse_Status
	}{
		{"Synchronized->HEALTHY", Synchronized, csiaddonsreplication.GetVolumeReplicationInfoResponse_HEALTHY},
		{"Consistent->HEALTHY", Consistent, csiaddonsreplication.GetVolumeReplicationInfoResponse_HEALTHY},
		{"ActiveBias->HEALTHY", ActiveBias, csiaddonsreplication.GetVolumeReplicationInfoResponse_HEALTHY},
		{"Suspended->DEGRADED", Suspended, csiaddonsreplication.GetVolumeReplicationInfoResponse_DEGRADED},
		{"Split->DEGRADED", Split, csiaddonsreplication.GetVolumeReplicationInfoResponse_DEGRADED},
		{"Partitioned->DEGRADED", Partitioned, csiaddonsreplication.GetVolumeReplicationInfoResponse_DEGRADED},
		{"FailedOver->DEGRADED", FailedOver, csiaddonsreplication.GetVolumeReplicationInfoResponse_DEGRADED},
		{"SyncInProgress->DEGRADED", SyncInProgress, csiaddonsreplication.GetVolumeReplicationInfoResponse_DEGRADED},
		{"Invalid->ERROR", Invalid, csiaddonsreplication.GetVolumeReplicationInfoResponse_ERROR},
		{"Unknown->UNKNOWN", "Unknown", csiaddonsreplication.GetVolumeReplicationInfoResponse_UNKNOWN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
			defer cleanup()
			srv := NewCSIAddonsReplicationServer(svc)

			pmaxClient.EXPECT().
				GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, "sg-x", "5").
				Return(&types.StorageGroupRDFG{
					VolumeRdfTypes: []string{"R1"},
					States:         []string{tt.state},
				}, nil)
			resp, err := srv.GetVolumeReplicationInfo(context.Background(), &csiaddonsreplication.GetVolumeReplicationInfoRequest{
				ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
			})
			assert.NoError(t, err)
			assert.Equal(t, tt.wantStatus, resp.GetStatus())
			// LastSyncTime is set for states where local data is
			// consistent: Consistent, Synchronized, ActiveBias, FailedOver.
			if tt.state == Consistent || tt.state == Synchronized || tt.state == ActiveBias || tt.state == FailedOver {
				assert.NotNil(t, resp.GetLastSyncTime())
			} else {
				assert.Nil(t, resp.GetLastSyncTime())
			}
		})
	}
}

func TestEnableVolumeReplication_InvalidVGID(t *testing.T) {
	svc, _, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	_, err := srv.EnableVolumeReplication(context.Background(), &csiaddonsreplication.EnableVolumeReplicationRequest{
		ReplicationSource: vgSource("not-an-id"),
		Parameters: map[string]string{
			CSIAddonsParamRemoteSystem:    symIDRemote,
			CSIAddonsParamReplicationMode: Sync,
		},
	})
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetReplicationDestinationInfo_VolumeSourceRejected(t *testing.T) {
	svc, _, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	_, err := srv.GetReplicationDestinationInfo(context.Background(), &csiaddonsreplication.GetReplicationDestinationInfoRequest{
		ReplicationSource: volumeSource(validLocalVolumeID),
	})
	assert.Error(t, err)
	assert.Equal(t, codes.InvalidArgument, status.Code(err))
}

func TestGetReplicationDestinationInfo_HappyPath(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetVersionDetails(gomock.Any()).
		Return(&types.VersionDetails{Version: "10.1.0.0", APIVersion: "101"}, nil).
		AnyTimes()

	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{validLocalDeviceID}, nil)

	// Since we use the bulk v1 query for APIVersion >= 10.1, but the version test mock might
	// return a lower version or we can just mock GetVolumeByID as fallback. Let's mock GetVolumeByID fallback.
	// We'll set up both just in case, using Any() to let it match either based on how getVersionCache behaves in test.
	pmaxClient.EXPECT().
		GetVolumesIdentifiersInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return(&types.Volumev1{Volumes: []types.VolumeEnhanced{{ID: validLocalDeviceID, Identifier: localVolumeName}}}, nil).
		AnyTimes()

	pmaxClient.EXPECT().
		GetVolumeByID(gomock.Any(), symIDLocal, validLocalDeviceID).
		Return(&types.Volume{VolumeIdentifier: localVolumeName}, nil).
		AnyTimes()

	pmaxClient.EXPECT().
		GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "5", validLocalDeviceID).
		Return(&types.RDFDevicePair{
			RemoteSymmID:         symIDRemote,
			RemoteVolumeName:     validRemoteDeviceID,
			RemoteRdfGroupNumber: 6,
		}, nil)

	resp, err := srv.GetReplicationDestinationInfo(context.Background(), &csiaddonsreplication.GetReplicationDestinationInfoRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.NoError(t, err)
	dest := resp.GetReplicationDestination().GetVolumegroup()
	assert.NotNil(t, dest)
	assert.Equal(t, encodeVolumeGroupID(symIDRemote, "sg-x", "6"), dest.GetVolumeGroupId())

	// Check that the returned IDs follow the PV volumeHandle format
	expectedLocalCSIID := localVolumeName + "-" + symIDLocal + "-" + validLocalDeviceID
	expectedRemoteCSIID := localVolumeName + "-" + symIDRemote + "-" + validRemoteDeviceID
	assert.Equal(t,
		expectedRemoteCSIID,
		dest.GetVolumeIds()[expectedLocalCSIID])
}

func TestGetReplicationDestinationInfo_EmptySG(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	srv := NewCSIAddonsReplicationServer(svc)

	pmaxClient.EXPECT().
		GetVolumeIDListInStorageGroup(gomock.Any(), symIDLocal, "sg-x").
		Return([]string{}, nil)

	_, err := srv.GetReplicationDestinationInfo(context.Background(), &csiaddonsreplication.GetReplicationDestinationInfoRequest{
		ReplicationSource: vgSource(encodeVolumeGroupID(symIDLocal, "sg-x", "5")),
	})
	assert.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestRegisterCSIAddonsReplicationServer(_ *testing.T) {
	srv := NewCSIAddonsReplicationServer(&service{})
	grpcSrv := grpc.NewServer()
	defer grpcSrv.Stop()
	// Must not panic.
	RegisterCSIAddonsReplicationServer(grpcSrv, srv)
}
