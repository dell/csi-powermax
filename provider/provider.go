package provider

import (
	"github.com/dell/csi-powermax/service"
	"github.com/rexray/gocsi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"time"
)

// New returns a new Mock Storage Plug-in Provider.
func New() gocsi.StoragePluginProvider {
	// Get the MaxConcurrentStreams server option and configure it.
	maxStreams := grpc.MaxConcurrentStreams(4)
	keepaliveEnforcementPolicy := keepalive.EnforcementPolicy {
		MinTime: 10 *time.Second,
	}
	keepaliveOpt  := grpc.KeepaliveEnforcementPolicy(keepaliveEnforcementPolicy)
	serverOptions := make([]grpc.ServerOption, 2)
	serverOptions[0] = maxStreams
	serverOptions[1] = keepaliveOpt

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
		},
	}
}
