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
//go:generate go generate ./core

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/dell/csi-powermax/v2/k8sutils"
	"github.com/dell/csi-powermax/v2/provider"
	"github.com/dell/csi-powermax/v2/service"
	"github.com/dell/gocsi"
)

// main is ignored when this package is built as a go plug-in
func main() {
	// Always set X_CSI_DEBUG to false irrespective of what user has specified
	_ = os.Setenv(gocsi.EnvVarDebug, "false")
	// We always want to enable Request and Response logging (no reason for users to control this)
	_ = os.Setenv(gocsi.EnvVarReqLogging, "true")
	_ = os.Setenv(gocsi.EnvVarRepLogging, "true")
	enableLeaderElection := flag.Bool("leader-election", false, "boolean to enable leader election")
	leaderElectionNamespace := flag.String("leader-election-namespace", "", "namespace where leader election lease will be created")
	kubeconfig := flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	flag.Parse()
	run := func(ctx context.Context) {
		gocsi.Run(ctx, service.Name, "A PowerMax Container Storage Interface (CSI) Plugin",
			usage, provider.New())
	}
	if !*enableLeaderElection {
		run(context.TODO())
	} else {
		driverName := strings.Replace(service.Name, ".", "-", -1)
		lockName := fmt.Sprintf("driver-%s", driverName)
		k8sclientset, err := k8sutils.CreateKubeClientSet(*kubeconfig)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "failed to initialize leader election: %v", err)
			os.Exit(1)
		}
		// Attempt to become leader and start the driver
		k8sutils.LeaderElection(k8sclientset, lockName, *leaderElectionNamespace, run)
	}
}

const usage = `    X_CSI_POWERMAX_ENDPOINT
        Specifies the HTTP endpoint for Unisphere. This parameter is
        required when running the Controller service.

        The default value is empty.

    X_CSI_POWERMAX_USER
        Specifies the user name when authenticating to Unisphere.

        The default value is admin.

    X_CSI_POWERMAX_PASSWORD
        Specifies the password of the user defined by X_CSI_POWERMAX_USER to use
        when authenticating to Unisphere. This parameter is required
        when running the Controller service.

        The default value is empty.

    X_CSI_POWERMAX_SKIP_CERTIFICATE_VALIDATION
        Specifies that the Unisphere's hostname and certificate chain
	should not be validated.

        The default value is false.

    X_CSI_POWERMAX_NODENAME
        Specifies the name of the node where the Node plugin is running

        The default value is empty

    X_CSI_POWERMAX_PORTGROUPS
        Specifies a list of Port Groups that the driver can choose from

        The default value is an empty list

    X_CSI_K8S_CLUSTER_PREFIX 
        Specifies a prefix to apply to objects created via this K8s/CSI cluster
         
        The default value is empty
    X_CSI_POWERMAX_ARRAYS
        Specifies a list of Arrays that the driver can choose from

        The default value is an empty list, allowing all arrays to be used

    X_CSI_GRPC_MAX_THREADS
        Specifies the maximum number of current gprc calls processed

    X_CSI_POWERMAX_DEBUG
        Turns on debugging of the PowerMax (REST interface to Unisphere) layer
`
