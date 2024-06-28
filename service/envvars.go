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

package service

const (
	// EnvDriverName is the name of the enviroment variable used to set the
	// name of the driver
	EnvDriverName = "X_CSI_POWERMAX_DRIVER_NAME"
	// EnvEndpoint is the name of the enviroment variable used to set the
	// HTTP endpoint of Unisphere
	EnvEndpoint = "X_CSI_POWERMAX_ENDPOINT"

	// EnvUser is the name of the enviroment variable used to set the
	// username when authenticating to Unisphere
	EnvUser = "X_CSI_POWERMAX_USER"

	// EnvPassword is the name of the enviroment variable used to set the
	// user's password when authenticating to Unisphere
	// #nosec G101
	EnvPassword = "X_CSI_POWERMAX_PASSWORD" // #nosec G101

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
	EnvAutoProbe = "X_CSI_POWERMAX_AUTOPROBE" // #nosec 101

	// EnvPortGroups is the name of the environment variable that is used
	// to specify a list of Port Groups that the driver can choose from
	// These Port Groups must exist and be populated
	EnvPortGroups = "X_CSI_POWERMAX_PORTGROUPS"

	// EnvClusterPrefix is the name of the environment variable that is used
	// to specify a prefix to apply to objects created via this CSI cluster
	EnvClusterPrefix = "X_CSI_K8S_CLUSTER_PREFIX" // #nosec 101

	// EnvNodeChroot is the path to which the driver will chroot before
	// running any iscsi/nvme commands. This value should only be set when instructed
	// by technical support.
	EnvNodeChroot = "X_CSI_NODE_CHROOT"

	// EnvGrpcMaxThreads is the configuration value of the maximum number of concurrent
	// grpc requests. This value should be an integer string.
	EnvGrpcMaxThreads = "X_CSI_GRPC_MAX_THREADS"

	// EnvEnableBlock enables block capabilities support.
	EnvEnableBlock = "X_CSI_ENABLE_BLOCK"

	// EnvPreferredTransportProtocol enables you to be able to force the transport protocol.
	// Valid values are "FC" or "ISCSI" or "". If "", will choose FC if both are available.
	// This is mainly for testing.
	EnvPreferredTransportProtocol = "X_CSI_TRANSPORT_PROTOCOL" // #nosec 101

	// EnvUnisphereProxyServiceName is the name of the proxy service in kubernetes
	// If set, then driver will attempt to read the associated env value
	// If set to none, then the driver will connect to Unisphere
	EnvUnisphereProxyServiceName = "X_CSI_POWERMAX_PROXY_SERVICE_NAME"

	// EnvSidecarProxyPort is the port on which the reverse proxy
	// server run, if run as a sidecar container
	EnvSidecarProxyPort = "X_CSI_POWERMAX_SIDECAR_PROXY_PORT"

	// EnvEnableCHAP is the flag which determines if the driver is going
	// to set the CHAP credentials in the ISCSI node database at the time
	// of node plugin boot
	EnvEnableCHAP = "X_CSI_POWERMAX_ISCSI_ENABLE_CHAP"

	// EnvISCSICHAPUserName is the username for the ISCSI CHAP
	// authentication for the host initiator(s)
	// If set to none, then the driver will use the ISCSI IQN as the username
	EnvISCSICHAPUserName = "X_CSI_POWERMAX_ISCSI_CHAP_USERNAME"

	// EnvISCSICHAPPassword is the password for the ISCSI CHAP
	// authentication for the host initiator(s)
	// #nosec G101
	EnvISCSICHAPPassword = "X_CSI_POWERMAX_ISCSI_CHAP_PASSWORD" // #nosec 101

	// EnvNodeNameTemplate is the templatized name to construct node names
	// by the driver based on a name format as specified by the user in this
	// variable
	EnvNodeNameTemplate = "X_CSI_IG_NODENAME_TEMPLATE"

	// EnvModifyHostName when this value is set to "true", the driver will
	// modify the existing host name to a new name as specified in the EnvNodeNameTemplate
	EnvModifyHostName = "X_CSI_IG_MODIFY_HOSTNAME" // #nosec 101

	// EnvReplicationContextPrefix enables sidecars to read required information from volume context
	EnvReplicationContextPrefix = "X_CSI_REPLICATION_CONTEXT_PREFIX"

	// EnvReplicationPrefix is used as a prefix to find out if replication is enabled
	EnvReplicationPrefix = "X_CSI_REPLICATION_PREFIX" // #nosec 101

	// EnvManagedArrays is an env variable with a list of space separated arrays.
	EnvManagedArrays = "X_CSI_MANAGED_ARRAYS"

	// EnvKubeConfigPath indicates kubernetes configuration that has to be used by CSI Driver
	EnvKubeConfigPath = "KUBECONFIG"

	// EnvConfigFilePath is an env variable which contains the full path for the config file
	EnvConfigFilePath = "X_CSI_POWERMAX_CONFIG_PATH"

	// EnvMaxVolumesPerNode specifies maximum number of volumes that controller can publish to the node.
	EnvMaxVolumesPerNode = "X_CSI_MAX_VOLUMES_PER_NODE"

	// EnvHealthMonitorEnabled is an env variable which indicated if volume health monitor is enabled
	EnvHealthMonitorEnabled = "X_CSI_HEALTH_MONITOR_ENABLED"

	// EnvTopoConfigFilePath is an env variable which contains the full path for topology config file
	EnvTopoConfigFilePath = "X_CSI_POWERMAX_TOPOLOGY_CONFIG_PATH"

	// EnvTopologyFilterEnabled is an env variable which indicates if volume health monitor is enabled
	EnvTopologyFilterEnabled = "X_CSI_TOPOLOGY_CONTROL_ENABLED"

	// EnvVSphereEnabled is an env variable which indicates if FC vsphere is enabled
	EnvVSphereEnabled = "X_CSI_VSPHERE_ENABLED"

	// EnvVSpherePortGroup is an env variable which has FC portGroup for vSphere
	EnvVSpherePortGroup = "X_CSI_VSPHERE_PORTGROUP"

	// EnvVSphereHostName is an env variable which has FC host for vSphere
	EnvVSphereHostName = "X_CSI_VSPHERE_HOSTNAME"

	// EnvVCHost is an env variable that has vCenter Host endpoint
	EnvVCHost = "X_CSI_VCENTER_HOST"

	// EnvVCUsername is an env variable that has vCenter username
	EnvVCUsername = "X_CSI_VCENTER_USERNAME"

	// EnvVCPassword is an env variable that has vCenter password
	EnvVCPassword = "X_CSI_VCENTER_PWD" // #nosec G101

	// EnvPodmonEnabled indicates that podmon is enabled
	EnvPodmonEnabled = "X_CSI_PODMON_ENABLED"

	// EnvPodmonArrayConnectivityAPIPORT indicates the port to be used for exposing podmon API health
	EnvPodmonArrayConnectivityAPIPORT = "X_CSI_PODMON_API_PORT"

	// EnvPodmonArrayConnectivityPollRate indicates the polling frequency to check array connectivity
	EnvPodmonArrayConnectivityPollRate = "X_CSI_PODMON_ARRAY_CONNECTIVITY_POLL_RATE"

	// EnvIfaceExcludeFilter is an env variable with a regex of interface names to exclude
	EnvIfaceExcludeFilter = "X_CSI_IFACE_EXCLUDE_FILTER"
)
