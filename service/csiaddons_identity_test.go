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
	"testing"

	csiaddonsidentity "github.com/csi-addons/spec/lib/go/identity"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
)

func TestNewCSIAddonsIdentityServer(t *testing.T) {
	svc := &service{}
	srv := NewCSIAddonsIdentityServer(svc)
	assert.NotNil(t, srv)
	assert.Same(t, svc, srv.service)
}

func TestCSIAddonsIdentity_GetIdentity(t *testing.T) {
	srv := NewCSIAddonsIdentityServer(&service{})
	resp, err := srv.GetIdentity(context.Background(), &csiaddonsidentity.GetIdentityRequest{})
	assert.NoError(t, err)
	assert.Equal(t, Name, resp.GetName())
	assert.Equal(t, ManifestSemver, resp.GetVendorVersion())
	assert.Equal(t, "Dell Inc.", resp.GetManifest()["vendor"])
}

func TestCSIAddonsIdentity_GetCapabilities(t *testing.T) {
	srv := NewCSIAddonsIdentityServer(&service{})
	resp, err := srv.GetCapabilities(context.Background(), &csiaddonsidentity.GetCapabilitiesRequest{})
	assert.NoError(t, err)
	caps := resp.GetCapabilities()
	assert.NotEmpty(t, caps)

	// Track which capabilities are advertised so that the suite catches
	// accidental removals when proto definitions evolve.
	var hasController, hasVolumeRep, hasGetRepDest, hasVolumeGroup, hasModifyVG, hasGetVG, hasListVG, hasNoDeleteVols bool
	for _, c := range caps {
		if svc := c.GetService(); svc != nil && svc.GetType() == csiaddonsidentity.Capability_Service_CONTROLLER_SERVICE {
			hasController = true
		}
		if vr := c.GetVolumeReplication(); vr != nil {
			switch vr.GetType() {
			case csiaddonsidentity.Capability_VolumeReplication_VOLUME_REPLICATION:
				hasVolumeRep = true
			case csiaddonsidentity.Capability_VolumeReplication_GET_REPLICATION_DESTINATION_INFO:
				hasGetRepDest = true
			}
		}
		if vg := c.GetVolumeGroup(); vg != nil {
			switch vg.GetType() {
			case csiaddonsidentity.Capability_VolumeGroup_VOLUME_GROUP:
				hasVolumeGroup = true
			case csiaddonsidentity.Capability_VolumeGroup_MODIFY_VOLUME_GROUP:
				hasModifyVG = true
			case csiaddonsidentity.Capability_VolumeGroup_GET_VOLUME_GROUP:
				hasGetVG = true
			case csiaddonsidentity.Capability_VolumeGroup_LIST_VOLUME_GROUPS:
				hasListVG = true
			case csiaddonsidentity.Capability_VolumeGroup_DO_NOT_ALLOW_VG_TO_DELETE_VOLUMES:
				hasNoDeleteVols = true
			}
		}
	}
	assert.True(t, hasController, "CONTROLLER_SERVICE capability must be advertised")
	assert.True(t, hasVolumeRep, "VOLUME_REPLICATION capability must be advertised")
	assert.True(t, hasGetRepDest, "GET_REPLICATION_DESTINATION_INFO capability must be advertised")
	assert.True(t, hasVolumeGroup, "VOLUME_GROUP capability must be advertised")
	assert.True(t, hasModifyVG, "MODIFY_VOLUME_GROUP capability must be advertised")
	assert.True(t, hasGetVG, "GET_VOLUME_GROUP capability must be advertised")
	assert.True(t, hasListVG, "LIST_VOLUME_GROUPS capability must be advertised")
	assert.True(t, hasNoDeleteVols, "DO_NOT_ALLOW_VG_TO_DELETE_VOLUMES capability must be advertised")
}

func TestCSIAddonsIdentity_Probe(t *testing.T) {
	srv := NewCSIAddonsIdentityServer(&service{})
	resp, err := srv.Probe(context.Background(), &csiaddonsidentity.ProbeRequest{})
	assert.NoError(t, err)
	assert.True(t, resp.GetReady().GetValue())
}

func TestRegisterCSIAddonsIdentityServer(_ *testing.T) {
	srv := NewCSIAddonsIdentityServer(&service{})
	grpcSrv := grpc.NewServer()
	defer grpcSrv.Stop()
	// Must not panic.
	RegisterCSIAddonsIdentityServer(grpcSrv, srv)
}
