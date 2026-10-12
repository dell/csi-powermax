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
	"testing"
	"time"

	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func disableRemoteIdentifierRetryWait(t *testing.T) {
	t.Helper()
	originalWait := remoteIdentifierRetryWait
	remoteIdentifierRetryWait = func(context.Context, time.Duration) error {
		return nil
	}
	t.Cleanup(func() {
		remoteIdentifierRetryWait = originalWait
	})
}

func TestSyncRemoteVolumeIdentifierWithRetry_DoesNotRetryNotFoundLookup(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	pmaxClient.EXPECT().
		GetVolumeByID(gomock.Any(), symIDRemote, validRemoteDeviceID).
		Return(nil, &types.Error{
			Message:        "getting volume by ID: volume not found",
			HTTPStatusCode: http.StatusNotFound,
		}).
		Times(1)

	err := svc.syncRemoteVolumeIdentifierWithRetry(
		context.Background(), symIDRemote, validRemoteDeviceID, localVolumeName, pmaxClient)

	assert.Error(t, err)
	assert.Equal(t, codes.Internal, status.Code(err))
}

func TestSyncRemoteVolumeIdentifierWithRetry_RetriesServiceUnavailableLookup(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	disableRemoteIdentifierRetryWait(t)

	pmaxClient.EXPECT().
		GetVolumeByID(gomock.Any(), symIDRemote, validRemoteDeviceID).
		Return(nil, &types.Error{
			Message:        "service unavailable",
			HTTPStatusCode: http.StatusServiceUnavailable,
		}).
		Times(1)
	pmaxClient.EXPECT().
		GetVolumeByID(gomock.Any(), symIDRemote, validRemoteDeviceID).
		Return(&types.Volume{
			VolumeID:         validRemoteDeviceID,
			VolumeIdentifier: "",
		}, nil)
	pmaxClient.EXPECT().
		RenameVolume(gomock.Any(), symIDRemote, validRemoteDeviceID, localVolumeName).
		Return(&types.Volume{
			VolumeID:         validRemoteDeviceID,
			VolumeIdentifier: localVolumeName,
		}, nil)

	err := svc.syncRemoteVolumeIdentifierWithRetry(
		context.Background(), symIDRemote, validRemoteDeviceID, localVolumeName, pmaxClient)

	assert.NoError(t, err)
}

func TestSyncRemoteVolumeIdentifierWithRetry_StopsWhenContextIsCanceled(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	ctx, cancel := context.WithCancel(context.Background())
	originalWait := remoteIdentifierRetryWait
	remoteIdentifierRetryWait = func(waitCtx context.Context, _ time.Duration) error {
		cancel()
		return waitCtx.Err()
	}
	defer func() { remoteIdentifierRetryWait = originalWait }()

	pmaxClient.EXPECT().
		GetVolumeByID(gomock.Any(), symIDRemote, validRemoteDeviceID).
		Return(nil, errors.New("csm-authorization: no storage pool found")).
		Times(1)

	err := svc.syncRemoteVolumeIdentifierWithRetry(
		ctx, symIDRemote, validRemoteDeviceID, localVolumeName, pmaxClient)

	assert.ErrorIs(t, err, context.Canceled)
}

func TestSyncRemoteVolumeIdentifierValue_SkipsExactMatch(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	err := svc.syncRemoteVolumeIdentifierValue(
		context.Background(),
		symIDRemote,
		validRemoteDeviceID,
		"csi-ABC-volume",
		&types.Volume{
			VolumeID:         validRemoteDeviceID,
			VolumeIdentifier: "csi-ABC-volume",
		},
		pmaxClient,
	)

	assert.NoError(t, err)
}

func TestSyncRemoteVolumeIdentifierValue_RenamesTenantPrefixedIdentifier(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()

	pmaxClient.EXPECT().
		RenameVolume(gomock.Any(), symIDRemote, validRemoteDeviceID, "csi-ABC-volume").
		Return(&types.Volume{
			VolumeID:         validRemoteDeviceID,
			VolumeIdentifier: "csi-ABC-volume",
		}, nil)

	err := svc.syncRemoteVolumeIdentifierValue(
		context.Background(),
		symIDRemote,
		validRemoteDeviceID,
		"csi-ABC-volume",
		&types.Volume{
			VolumeID:         validRemoteDeviceID,
			VolumeIdentifier: "tenant-csi-ABC-volume",
		},
		pmaxClient,
	)

	assert.NoError(t, err)
}

func TestSyncRemoteVolumeIdentifierWithRetryFromVolume_RetriesTransientRename(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	disableRemoteIdentifierRetryWait(t)

	pmaxClient.EXPECT().
		RenameVolume(gomock.Any(), symIDRemote, validRemoteDeviceID, localVolumeName).
		Return(nil, errors.New("csm-authorization: no storage pool found"))
	pmaxClient.EXPECT().
		GetVolumeByID(gomock.Any(), symIDRemote, validRemoteDeviceID).
		Return(&types.Volume{
			VolumeID:         validRemoteDeviceID,
			VolumeIdentifier: "",
		}, nil)
	pmaxClient.EXPECT().
		RenameVolume(gomock.Any(), symIDRemote, validRemoteDeviceID, localVolumeName).
		Return(&types.Volume{
			VolumeID:         validRemoteDeviceID,
			VolumeIdentifier: localVolumeName,
		}, nil)

	err := svc.syncRemoteVolumeIdentifierWithRetryFromVolume(
		context.Background(),
		symIDRemote,
		validRemoteDeviceID,
		localVolumeName,
		&types.Volume{
			VolumeID:         validRemoteDeviceID,
			VolumeIdentifier: "",
		},
		pmaxClient,
	)

	assert.NoError(t, err)
}

func TestCreateMetroVolume_IdempotentContentSourceSynchronizesTenantPrefixedIdentifier(t *testing.T) {
	svc, pmaxClient, cleanup := newCSIAddonsTestService(t)
	defer cleanup()
	svc.opts.EnableBlock = true

	originalRequestLockFunc := requestLockFunc
	originalReleaseLockFunc := releaseLockFunc
	requestLockFunc = func(_, _ string) (int, error) { return 0, nil }
	releaseLockFunc = func(_, _ string, _ int) {}
	defer func() {
		requestLockFunc = originalRequestLockFunc
		releaseLockFunc = originalReleaseLockFunc
	}()

	goodCapacityRange := &csi.CapacityRange{
		RequiredBytes: 1 * GiB,
		LimitBytes:    4 * GiB,
	}
	goodSrpCapacity := &types.SrpCap{
		UsableUsedInTB: 1.0,
		UsableTotInTB:  2.0,
	}

	pmaxClient.EXPECT().
		GetStoragePool(gomock.Any(), symIDLocal, "POOL_1").
		Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
	pmaxClient.EXPECT().
		GetStoragePool(gomock.Any(), symIDRemote, "POOL_1").
		Return(&types.StoragePool{SrpCap: goodSrpCapacity}, nil)
	pmaxClient.EXPECT().
		IsAllowedArray(gomock.Any()).
		Return(true, nil)
	pmaxClient.EXPECT().
		GetReplicationCapabilities(gomock.Any()).
		Return(&types.SymReplicationCapabilities{
			SymmetrixCapability: []types.SymmetrixCapability{
				{SymmetrixID: symIDLocal, SnapVxCapable: true},
			},
		}, nil)
	pmaxClient.EXPECT().
		GetVolumeByID(gomock.Any(), symIDLocal, validLocalDeviceID).
		Return(&types.Volume{
			VolumeID:    validLocalDeviceID,
			CapacityCYL: 100,
		}, nil)
	pmaxClient.EXPECT().
		GetProtectedStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return(&types.RDFStorageGroup{Rdf: true}, nil)
	pmaxClient.EXPECT().
		GetStorageGroupRDFInfo(gomock.Any(), symIDLocal, gomock.Any(), "10").
		AnyTimes().
		Return(&types.StorageGroupRDFG{
			VolumeRdfTypes: []string{"R1"},
			States:         []string{"Suspended"},
		}, nil)
	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDLocal, gomock.Any()).
		Return(&types.StorageGroup{}, nil)
	pmaxClient.EXPECT().
		GetStorageGroup(gomock.Any(), symIDRemote, gomock.Any()).
		Return(&types.StorageGroup{}, nil)
	pmaxClient.EXPECT().
		GetVolumeIDList(gomock.Any(), symIDLocal, gomock.Any(), false).
		Return([]string{"022CD"}, nil)
	pmaxClient.EXPECT().
		GetVolumeByID(gomock.Any(), symIDLocal, "022CD").
		Return(&types.Volume{
			VolumeID:           "022CD",
			VolumeIdentifier:   "tn1-csi-ABC-csivol-01234abcde-default",
			CapacityCYL:        547,
			CapacityGB:         1.0,
			StorageGroupIDList: []string{"csi-ABC-Diamond-POOL_1-SG"},
		}, nil)
	pmaxClient.EXPECT().
		GetRDFDevicePairInfo(gomock.Any(), symIDLocal, "10", "022CD").
		Return(&types.RDFDevicePair{
			RemoteVolumeName:     "033EF",
			RemoteRdfGroupNumber: 20,
		}, nil)
	pmaxClient.EXPECT().
		GetVolumeByID(gomock.Any(), symIDRemote, "033EF").
		Return(&types.Volume{
			VolumeID:           "033EF",
			VolumeIdentifier:   "",
			StorageGroupIDList: []string{"SG1", "SG2"},
		}, nil)
	pmaxClient.EXPECT().
		RenameVolume(gomock.Any(), symIDRemote, "033EF", "tn1-csi-ABC-csivol-01234abcde-default").
		Return(&types.Volume{
			VolumeID:         "033EF",
			VolumeIdentifier: "tn1-csi-ABC-csivol-01234abcde-default",
		}, nil)
	pmaxClient.EXPECT().
		GetSymmetrixByID(gomock.Any(), symIDLocal).
		Return(&types.Symmetrix{
			SymmetrixID: symIDLocal,
			Microcode:   "6079.325.0",
		}, nil)
	pmaxClient.EXPECT().
		CloneVolumeFromVolume(gomock.Any(), symIDLocal, gomock.Any()).
		Return(nil)
	pmaxClient.EXPECT().
		ExecuteReplicationActionOnSG(gomock.Any(), symIDLocal, Establish, gomock.Any(), "10", false, false, false).
		Return(nil)

	response, err := svc.createMetroVolume(
		context.Background(),
		&csi.CreateVolumeRequest{
			Name:          "csivol-01234abcde",
			CapacityRange: goodCapacityRange,
			VolumeCapabilities: []*csi.VolumeCapability{
				{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
				},
			},
			VolumeContentSource: &csi.VolumeContentSource{
				Type: &csi.VolumeContentSource_Volume{
					Volume: &csi.VolumeContentSource_VolumeSource{
						VolumeId: validLocalVolumeID,
					},
				},
			},
		},
		"review-idempotent-source",
		"POOL_1",
		symIDLocal,
		"",
		"Diamond",
		"",
		symIDRemote,
		"10",
		"20",
		"Diamond",
		"POOL_1",
		"default",
		"",
		"",
		"",
		"",
		"",
		"",
	)

	if assert.NoError(t, err) {
		assert.NotNil(t, response)
		assert.Contains(t, response.GetVolume().GetVolumeId(), "022CD:033EF")
	}
}
