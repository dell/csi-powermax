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
	"path"
	"testing"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	csimgr "github.com/dell/dell-csi-extensions/migration"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/metadata"
)

func TestVolumeMigrate(t *testing.T) {
	LockRequestHandler()
	volIDInvalid := s.createCSIVolumeID("", "invalidVolume", "0001", "00000")
	volIDValid := s.createCSIVolumeID("", "validVolume", "0001", "00001")

	c := mocks.NewMockPmaxClient(gomock.NewController(t))
	c.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c)
	c.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), "00000").AnyTimes().Return(nil, errors.New(notFound))
	c.EXPECT().GetVolumeByID(gomock.Any(), gomock.Any(), "00001").AnyTimes().Return(&types.Volume{
		VolumeIdentifier: "csi--validVolume",
		RDFGroupIDList:   []types.RDFGroupID{{RDFGroupNumber: 42, Label: "label"}, {RDFGroupNumber: 42, Label: "label"}},
	}, nil)
	c.EXPECT().GetStoragePoolList(gomock.Any(), "0001").AnyTimes().Return(&types.StoragePoolList{StoragePoolIDs: []string{"srp1", "srp2"}}, nil)
	c.EXPECT().GetRDFGroupByID(gomock.Any(), "0001", "42").AnyTimes().Return(&types.RDFGroup{RdfgNumber: 42, Label: "label"}, nil)
	c.EXPECT().RemoveVolumesFromProtectedStorageGroup(gomock.Any(), "0001", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroup{}, nil)
	c.EXPECT().GetRDFGroupList(gomock.Any(), "0001", gomock.Any()).AnyTimes().Return(&types.RDFGroupList{RDFGroupCount: 1, RDFGroupIDs: []types.RDFGroupIDL{{}}}, nil)
	c.EXPECT().GetFreeLocalAndRemoteRDFg(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.NextFreeRDFGroup{LocalRdfGroup: []int{42}, RemoteRdfGroup: []int{42}}, nil)
	c.EXPECT().GetLocalOnlineRDFDirs(gomock.Any(), gomock.Any()).AnyTimes().Return(&types.RDFDirList{RdfDirs: []string{}}, nil)
	c.EXPECT().ExecuteCreateRDFGroup(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil)
	c.EXPECT().GetProtectedStorageGroup(gomock.Any(), "0001", gomock.Any()).AnyTimes().Return(nil, errors.New("error"))
	c.EXPECT().GetStorageGroupIDList(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(nil, errors.New("error"))

	symmetrix.Initialize([]string{"0001"}, c)
	defer symmetrix.RemoveClient("0001")

	ctx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{
		"csi.requestid": "123",
	}))

	t.Run("Invalid requests", func(t *testing.T) {
		// Create a test request, invalid volume ID
		req := &csimgr.VolumeMigrateRequest{
			VolumeHandle: "invalid",
			ScParameters: map[string]string{
				SymmetrixIDParam:       "invalid",
				ApplicationPrefixParam: "testPrefix",
				StorageGroupParam:      "testSG",
			},
			ScSourceParameters: map[string]string{
				FsTypeParam: NFS,
			},
			ShouldClone:  true,
			StorageClass: "powermax",
			MigrateTypes: &csimgr.VolumeMigrateRequest_Type{
				Type: csimgr.MigrateTypes_NON_REPL_TO_REPL,
			},
		}

		// Call the VolumeMigrate function
		resp, err := s.VolumeMigrate(ctx, req)
		assert.Nil(t, resp)
		assert.Contains(t, err.Error(), "Invalid volume id: ")

		// Invalid Client
		req.VolumeHandle = s.createCSIVolumeID("", "invalidClient", "0000", "11111")
		_, err = s.VolumeMigrate(ctx, req)
		assert.Contains(t, err.Error(), "not found")

		// Invalid FS type
		req.VolumeHandle = volIDInvalid
		_, err = s.VolumeMigrate(ctx, req)
		assert.Contains(t, err.Error(), "volume migration is not supported on NFS volumes")

		// Invalid volume
		req.ScSourceParameters = map[string]string{
			FsTypeParam: "",
		}
		_, err = s.VolumeMigrate(ctx, req)
		assert.Contains(t, err.Error(), notFound)

		// Invalid storagepool
		req.VolumeHandle = volIDValid
		_, err = s.VolumeMigrate(ctx, req)
		assert.Contains(t, err.Error(), "A valid SRP parameter is required")

		// Invalid SLO
		req.ScParameters[StoragePoolParam] = "srp1"
		req.ScParameters[ServiceLevelParam] = "invalid"
		_, err = s.VolumeMigrate(ctx, req)
		assert.Contains(t, err.Error(), "invalid Service Level")

		// Invalid migration type
		req.MigrateTypes = &csimgr.VolumeMigrateRequest_Type{
			Type: csimgr.MigrateTypes_UNKNOWN_MIGRATE,
		}
		req.ScParameters[ServiceLevelParam] = "Bronze"
		_, err = s.VolumeMigrate(ctx, req)
		assert.Contains(t, err.Error(), "Unknown Migration Type")
	})

	t.Run("Version Upgrade", func(t *testing.T) {
		req := &csimgr.VolumeMigrateRequest{
			VolumeHandle: volIDValid,
			ScParameters: map[string]string{
				SymmetrixIDParam: "0001",
				StoragePoolParam: "srp1",
			},
			ScSourceParameters: map[string]string{},
			ShouldClone:        true,
			StorageClass:       "powermax",
			MigrateTypes: &csimgr.VolumeMigrateRequest_Type{
				Type: csimgr.MigrateTypes_VERSION_UPGRADE,
			},
		}

		_, err := s.VolumeMigrate(ctx, req)
		assert.Contains(t, err.Error(), "Unimplemented")
	})

	t.Run("Non repl to repl", func(t *testing.T) {
		req := &csimgr.VolumeMigrateRequest{
			VolumeHandle: volIDValid,
			ScParameters: map[string]string{
				SymmetrixIDParam: "0001",
				StoragePoolParam: "srp1",
			},
			ScSourceParameters: map[string]string{},
			ShouldClone:        true,
			StorageClass:       "powermax",
			MigrateTypes: &csimgr.VolumeMigrateRequest_Type{
				Type: csimgr.MigrateTypes_NON_REPL_TO_REPL,
			},
		}

		// Success path
		resp, err := s.VolumeMigrate(ctx, req)
		assert.Nil(t, err)
		assert.Equal(t, volIDValid, resp.MigratedVolume.GetVolumeId())

		// path.Join(s.opts.ReplicationPrefix, RepEnabledParam) is true
		req.ScParameters[path.Join(s.opts.ReplicationPrefix, RepEnabledParam)] = "true"
		req.ScParameters[path.Join(s.opts.ReplicationPrefix, ReplicationModeParam)] = Sync
		req.ScParameters[StorageGroupParam] = "testSG"
		_, err = s.VolumeMigrate(ctx, req)
		assert.Contains(t, err.Error(), "VerifyProtectionGroupID failed")
	})

	t.Run("Repl to non repl", func(t *testing.T) {
		req := &csimgr.VolumeMigrateRequest{
			VolumeHandle: volIDValid,
			ScParameters: map[string]string{
				SymmetrixIDParam: "0001",
				StoragePoolParam: "srp1",
			},
			ScSourceParameters: map[string]string{},
			ShouldClone:        true,
			StorageClass:       "powermax",
			MigrateTypes: &csimgr.VolumeMigrateRequest_Type{
				Type: csimgr.MigrateTypes_REPL_TO_NON_REPL,
			},
		}

		// Success path
		resp, err := s.VolumeMigrate(ctx, req)
		assert.Nil(t, err)
		assert.Equal(t, volIDValid, resp.MigratedVolume.GetVolumeId())
	})
}

func TestArrayMigrate(t *testing.T) {
	c2 := mocks.NewMockPmaxClient(gomock.NewController(t))
	symmetrix.Initialize([]string{"0002"}, c2)
	defer symmetrix.RemoveClient("0002")
	c2.EXPECT().WithSymmetrixID(gomock.Any()).AnyTimes().Return(c2)
	c2.EXPECT().GetMigrationEnvironment(gomock.Any(), gomock.Any(), gomock.Any()).MaxTimes(1).Return(&types.MigrationEnv{}, nil)
	c2.EXPECT().GetStorageGroupIDList(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(&types.StorageGroupIDList{}, nil)
	c2.EXPECT().GetStorageGroupMigration(gomock.Any(), gomock.Any()).MaxTimes(1).Return(&types.MigrationStorageGroups{}, nil)
	c2.EXPECT().DeleteMigrationEnvironment(gomock.Any(), gomock.Any(), gomock.Any()).MaxTimes(1).Return(nil)

	ctx := metadata.NewIncomingContext(context.Background(), metadata.New(map[string]string{
		"csi.requestid": "123",
	}))

	t.Run("ActionTypes_MG_MIGRATE", func(t *testing.T) {
		req := &csimgr.ArrayMigrateRequest{
			ActionTypes: &csimgr.ArrayMigrateRequest_Action{
				Action: &csimgr.Action{
					ActionTypes: csimgr.ActionTypes_MG_MIGRATE,
				},
			},
			Parameters: map[string]string{
				SymmetrixIDParam: "0002",
				RemoteSymIDParam: "srp1",
			},
		}

		resp, err := s.ArrayMigrate(ctx, req)
		assert.Nil(t, err)
		assert.True(t, resp.Success)
	})

	t.Run("ActionTypes_MG_COMMIT", func(t *testing.T) {
		req := &csimgr.ArrayMigrateRequest{
			ActionTypes: &csimgr.ArrayMigrateRequest_Action{
				Action: &csimgr.Action{
					ActionTypes: csimgr.ActionTypes_MG_COMMIT,
				},
			},
			Parameters: map[string]string{
				SymmetrixIDParam: "0002",
				RemoteSymIDParam: "srp1",
			},
		}

		resp, err := s.ArrayMigrate(ctx, req)
		assert.Nil(t, err)
		assert.True(t, resp.Success)

		// Invalid params
		req.Parameters = map[string]string{}
		_, err = s.ArrayMigrate(ctx, req)
		assert.Contains(t, err.Error(), "Invalid argument")

		// Invalid symID
		req.Parameters = map[string]string{
			RemoteSymIDParam: "srp1",
		}
		_, err = s.ArrayMigrate(ctx, req)
		assert.Contains(t, err.Error(), "A SYMID parameter is required")

		req.Parameters = map[string]string{
			SymmetrixIDParam: "srp1",
			RemoteSymIDParam: "srp1",
		}
		_, err = s.ArrayMigrate(ctx, req)
		assert.Contains(t, err.Error(), "srp1 not found")

		c2.EXPECT().GetMigrationEnvironment(gomock.Any(), gomock.Any(), gomock.Any()).MaxTimes(1).Return(nil, errors.New("GetMigrationEnvironment failed"))
		c2.EXPECT().GetStorageGroupMigration(gomock.Any(), gomock.Any()).MaxTimes(1).Return(nil, errors.New("GetStorageGroupMigration failed"))
		c2.EXPECT().DeleteMigrationEnvironment(gomock.Any(), gomock.Any(), gomock.Any()).MaxTimes(1).Return(errors.New("DeleteMigrationEnvironment failed"))

		// GetStorageGroupMigration error
		req.Parameters = map[string]string{
			SymmetrixIDParam: "0002",
			RemoteSymIDParam: "srp1",
		}
		resp, err = s.ArrayMigrate(ctx, req)
		t.Logf("resp: %v, err: %v", resp, err)
		assert.Contains(t, err.Error(), "GetStorageGroupMigration failed")

		c2.EXPECT().GetStorageGroupMigration(gomock.Any(), gomock.Any()).MaxTimes(1).Return(&types.MigrationStorageGroups{}, nil)
		req.Parameters = map[string]string{
			SymmetrixIDParam: "0002",
			RemoteSymIDParam: "srp1",
		}
		resp, err = s.ArrayMigrate(ctx, req)
		t.Logf("resp: %v, err: %v", resp, err)
		assert.Contains(t, err.Error(), "DeleteMigrationEnvironment failed")
	})
}
