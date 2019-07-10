package provider

import (
	"github.com/dell/csi-powermax/service"
	"github.com/rexray/gocsi"
	"google.golang.org/grpc"
)

// New returns a new Mock Storage Plug-in Provider.
func New() gocsi.StoragePluginProvider {
	// Get the MaxConcurrentStreams server option and configure it.
	maxStreams := grpc.MaxConcurrentStreams(4)
	serverOptions := make([]grpc.ServerOption, 1)
	serverOptions[0] = maxStreams

	svc := service.New()
	return &gocsi.StoragePlugin{
		Controller:  svc,
		Identity:    svc,
		Node:        svc,
		BeforeServe: svc.BeforeServe,
		ServerOpts:  serverOptions,

		EnvVars: []string{
			// Enable request validation
			gocsi.EnvVarSpecReqValidation + "=true",

			// Enable serial volume access
			gocsi.EnvVarSerialVolAccess + "=true",

			// Treat the following fields as required:
			//    * ControllerPublishVolumeRequest.NodeId
			//    * GetNodeIDResponse.NodeId
			gocsi.EnvVarRequireNodeID + "=true",
		},
	}
}
