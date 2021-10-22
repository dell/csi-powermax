/*
 Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package provider

import (
	"os"
	"strconv"
	"time"

	"github.com/dell/csi-powermax/v2/service"
	"github.com/dell/gocsi"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
)

// New returns a new Mock Storage Plug-in Provider.
func New() gocsi.StoragePluginProvider {
	// Get the MaxConcurrentStreams server option and configure it.
	maxConcurrentStreams := uint32(4)
	if maxThreads, found := os.LookupEnv(service.EnvGrpcMaxThreads); found {
		tempConcurrentStreams, err := strconv.Atoi(maxThreads)
		if err == nil {
			maxConcurrentStreams = uint32(tempConcurrentStreams)
		}
	}
	maxStreams := grpc.MaxConcurrentStreams(maxConcurrentStreams)
	keepaliveEnforcementPolicy := keepalive.EnforcementPolicy{
		MinTime: 10 * time.Second,
	}
	keepaliveOpt := grpc.KeepaliveEnforcementPolicy(keepaliveEnforcementPolicy)
	serverOptions := make([]grpc.ServerOption, 2)
	serverOptions[0] = maxStreams
	serverOptions[1] = keepaliveOpt

	svc := service.New()
	return &gocsi.StoragePlugin{
		Controller:                svc,
		Identity:                  svc,
		Node:                      svc,
		BeforeServe:               svc.BeforeServe,
		ServerOpts:                serverOptions,
		RegisterAdditionalServers: svc.RegisterAdditionalServers,

		EnvVars: []string{
			// Enable request validation
			gocsi.EnvVarSpecReqValidation + "=true",

			// Enable serial volume access
			gocsi.EnvVarSerialVolAccess + "=true",
		},
	}
}
