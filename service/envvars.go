/*
 Copyright © 2020 Dell Inc. or its subsidiaries. All Rights Reserved.

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

const (
	// EnvEndpoint is the name of the enviroment variable used to set the
	// HTTP endpoint of Unisphere
	EnvEndpoint = "X_CSI_POWERMAX_ENDPOINT"

	// EnvUser is the name of the enviroment variable used to set the
	// username when authenticating to Unisphere
	EnvUser = "X_CSI_POWERMAX_USER"

	// EnvPassword is the name of the enviroment variable used to set the
	// user's password when authenticating to Unisphere
	EnvPassword = "X_CSI_POWERMAX_PASSWORD"

	// EnvVersion is the name of the enviroment variable used to set the
	// U4P version when authenticating to Unisphere
	EnvVersion = "X_CSI_POWERMAX_VERSION"

	// EnvInsecure is the name of the enviroment variable used to specify
	// that Unisphere's certificate chain and host name should not
	// be validated.
	// This is deprecated- use X_CSI_POWERMAX_SKIP_CERTIFICATE_VALIDATION instead.
	EnvInsecure = "X_CSI_POWERMAX_INSECURE"

	// EnvSkipCertificateValidation is the name of the environment variable used
	// to specify Unisphere's certificate chain and host name should not
	// be validated.
	EnvSkipCertificateValidation = "X_CSI_POWERMAX_SKIP_CERTIFICATE_VALIDATION"

	// EnvNodeName is the name of the enviroment variable used to set the
	// hostname where the node service is running
	EnvNodeName = "X_CSI_POWERMAX_NODENAME"

	// EnvThick is the name of the enviroment variable used to specify
	// that thick provisioning should be used when creating volumes
	EnvThick = "X_CSI_POWERMAX_THICKPROVISIONING"

	// EnvAutoProbe is the name of the environment variable used to specify
	// that the controller service should automatically probe itself if it
	// receives incoming requests before having been probed, in direct
	// violation of the CSI spec
	EnvAutoProbe = "X_CSI_POWERMAX_AUTOPROBE"

	// EnvPortGroups is the name of the environment variable that is used
	// to specifiy a list of Port Groups that the driver can choose from
	// These Port Groups must exist and be populated
	EnvPortGroups = "X_CSI_POWERMAX_PORTGROUPS"

	// EnvClusterPrefix is the name of the environment variable that is used
	// to specifiy a a prefix to apply to objects creaated via this CSI cluster
	EnvClusterPrefix = "X_CSI_K8S_CLUSTER_PREFIX"
	// EnvArrayWhitelist is the name of the environment variable that is used
	// to specifiy a list of Arrays the the driver can choose from.
	// An empty list will allow all arrays known to Unisphere to be used.
	EnvArrayWhitelist = "X_CSI_POWERMAX_ARRAYS"

	// EnvISCSIChroot is the path to which the driver will chroot before
	// running any iscsi commands. This value should only be set when instructed
	// by technical support.
	EnvISCSIChroot = "X_CSI_ISCSI_CHROOT"

	// EnvGrpcMaxThreads is the configuration value of the maximum number of concurrent
	// grpc requests. This value should be an integer string.
	EnvGrpcMaxThreads = "X_CSI_GRPC_MAX_THREADS"

	// EnvEnableBlock enables block capabilities support.
	EnvEnableBlock = "X_CSI_ENABLE_BLOCK"

	// EnvPreferredTransportProtocol enables you to be able to force the transport protocol.
	// Valid values are "FC" or "ISCSI" or "". If "", will choose FC if both are available.
	// This is mainly for testing.
	EnvPreferredTransportProtocol = "X_CSI_TRANSPORT_PROTOCOL"
)
