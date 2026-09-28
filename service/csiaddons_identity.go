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

	"github.com/dell/csmlog"
	csiaddonsidentity "github.com/csi-addons/spec/lib/go/identity"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// CSIAddonsIdentityServer implements the CSI-Addons Identity service.
// This is required by the CSI-Addons sidecar for probing the driver and
// advertising the optional capabilities (volume replication, volume group)
// that the PowerMax driver supports through the CSI-Addons spec.
type CSIAddonsIdentityServer struct {
	csiaddonsidentity.UnimplementedIdentityServer
	service *service
}

// NewCSIAddonsIdentityServer creates a new CSI-Addons identity server.
func NewCSIAddonsIdentityServer(svc *service) *CSIAddonsIdentityServer {
	return &CSIAddonsIdentityServer{service: svc}
}

// RegisterCSIAddonsIdentityServer registers the CSI-Addons identity server
// with the given gRPC server.
func RegisterCSIAddonsIdentityServer(server *grpc.Server, srv *CSIAddonsIdentityServer) {
	csiaddonsidentity.RegisterIdentityServer(server, srv)
}

// GetIdentity returns the identity of the CSI-Addons driver.
func (s *CSIAddonsIdentityServer) GetIdentity(
	ctx context.Context,
	_ *csiaddonsidentity.GetIdentityRequest,
) (*csiaddonsidentity.GetIdentityResponse, error) {
	csmlog.WithContext(ctx).Info("CSI-Addons GetIdentity called")
	return &csiaddonsidentity.GetIdentityResponse{
		Name:          Name,
		VendorVersion: ManifestSemver,
		Manifest: map[string]string{
			"vendor": "Dell Inc.",
		},
	}, nil
}

// GetCapabilities returns the capabilities of the CSI-Addons driver.
//
// PowerMax advertises VOLUME_REPLICATION (SRDF/S and SRDF/A) and the
// VOLUME_GROUP capabilities required for VolumeGroupReplication. Metro is
// intentionally NOT advertised because CSI-Addons does not provide a clean
// model for active-active replication and Metro is explicitly out of scope
// for the CSI-Addons integration.
func (s *CSIAddonsIdentityServer) GetCapabilities(
	ctx context.Context,
	_ *csiaddonsidentity.GetCapabilitiesRequest,
) (*csiaddonsidentity.GetCapabilitiesResponse, error) {
	csmlog.WithContext(ctx).Info("CSI-Addons GetCapabilities called")
	return &csiaddonsidentity.GetCapabilitiesResponse{
		Capabilities: []*csiaddonsidentity.Capability{
			{
				Type: &csiaddonsidentity.Capability_Service_{
					Service: &csiaddonsidentity.Capability_Service{
						Type: csiaddonsidentity.Capability_Service_CONTROLLER_SERVICE,
					},
				},
			},
			{
				Type: &csiaddonsidentity.Capability_VolumeReplication_{
					VolumeReplication: &csiaddonsidentity.Capability_VolumeReplication{
						Type: csiaddonsidentity.Capability_VolumeReplication_VOLUME_REPLICATION,
					},
				},
			},
			{
				Type: &csiaddonsidentity.Capability_VolumeReplication_{
					VolumeReplication: &csiaddonsidentity.Capability_VolumeReplication{
						Type: csiaddonsidentity.Capability_VolumeReplication_GET_REPLICATION_DESTINATION_INFO,
					},
				},
			},
			{
				Type: &csiaddonsidentity.Capability_VolumeGroup_{
					VolumeGroup: &csiaddonsidentity.Capability_VolumeGroup{
						Type: csiaddonsidentity.Capability_VolumeGroup_VOLUME_GROUP,
					},
				},
			},
			{
				Type: &csiaddonsidentity.Capability_VolumeGroup_{
					VolumeGroup: &csiaddonsidentity.Capability_VolumeGroup{
						Type: csiaddonsidentity.Capability_VolumeGroup_MODIFY_VOLUME_GROUP,
					},
				},
			},
			{
				Type: &csiaddonsidentity.Capability_VolumeGroup_{
					VolumeGroup: &csiaddonsidentity.Capability_VolumeGroup{
						Type: csiaddonsidentity.Capability_VolumeGroup_GET_VOLUME_GROUP,
					},
				},
			},
			{
				Type: &csiaddonsidentity.Capability_VolumeGroup_{
					VolumeGroup: &csiaddonsidentity.Capability_VolumeGroup{
						Type: csiaddonsidentity.Capability_VolumeGroup_LIST_VOLUME_GROUPS,
					},
				},
			},
			{
				// PowerMax DeleteVolumeGroup only removes volumes from the
				// Storage Group and deletes the (empty) SG; the underlying
				// volume devices are preserved. Advertise that deleting the
				// VG does not delete the member volumes.
				Type: &csiaddonsidentity.Capability_VolumeGroup_{
					VolumeGroup: &csiaddonsidentity.Capability_VolumeGroup{
						Type: csiaddonsidentity.Capability_VolumeGroup_DO_NOT_ALLOW_VG_TO_DELETE_VOLUMES,
					},
				},
			},
		},
	}, nil
}

// Probe checks if the CSI-Addons driver is ready. We simply report ready
// when the gRPC server is reachable; full Unisphere connectivity is probed
// by the main CSI Identity Probe RPC.
func (s *CSIAddonsIdentityServer) Probe(
	ctx context.Context,
	_ *csiaddonsidentity.ProbeRequest,
) (*csiaddonsidentity.ProbeResponse, error) {
	csmlog.WithContext(ctx).Info("CSI-Addons Probe called")
	return &csiaddonsidentity.ProbeResponse{
		Ready: wrapperspb.Bool(true),
	}, nil
}
