//go:generate go generate ./core

package main

import (
	"context"

	"github.com/dell/csi-powermax/provider"
	"github.com/dell/csi-powermax/service"
	"github.com/rexray/gocsi"
)

// main is ignored when this package is built as a go plug-in
func main() {
	gocsi.Run(
		context.Background(),
		service.Name,
		"A PowerMax Container Storage Interface (CSI) Plugin",
		usage,
		provider.New())
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
