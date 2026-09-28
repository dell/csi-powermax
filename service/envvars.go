/*
 Copyright © 2021-2026 Dell Inc. or its subsidiaries. All Rights Reserved.

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

	// EnvArrayConfigPath is an env variable which contains the full path for the config file
	EnvArrayConfigPath = "X_CSI_POWERMAX_ARRAY_CONFIG_PATH"

	// EnvMaxVolumesPerNode specifies maximum number of volumes that controller can publish to the node.
	EnvMaxVolumesPerNode = "X_CSI_MAX_VOLUMES_PER_NODE"

	// EnvHealthMonitorEnabled is an env variable which indicated if volume health monitor is enabled
	EnvHealthMonitorEnabled = "X_CSI_HEALTH_MONITOR_ENABLED"

	// EnvCSIAddonsReplicationEnabled is an env variable which indicates if CSI-Addons
	// replication and volume group servers should be registered. Default: false.
	EnvCSIAddonsReplicationEnabled = "X_CSI_CSIADDONS_REPLICATION_ENABLED"

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

	// EnvPodmonAPIToken is the shared secret token used to authenticate requests
	// between the CSI controller and node podmon API endpoints.
	// When set, both the node HTTP server and the controller HTTP client
	// will use Bearer token authentication. If unset, authentication is skipped
	// for backward compatibility.
	EnvPodmonAPIToken = "X_CSI_PODMON_API_TOKEN" // #nosec G101

	// EnvTLSCertDirName is an env variable that contains the path of reverseproxy tls certificate
	EnvTLSCertDirName = "X_CSI_REVPROXY_TLS_CERT_DIR"

	// EnvRevProxyUseSecret is an env variable that indicates if reverseproxy should use secret
	EnvRevProxyUseSecret = "X_CSI_REVPROXY_USE_SECRET" // #nosec 101

	// EnvRevProxySecretPath is an env variable that indicates reverseproxy secret path
	EnvRevProxySecretPath = "X_CSI_REVPROXY_SECRET_FILEPATH" // #nosec 101

	// EnvDynamicSGEnabled is an env variable which indicates if dynamic SG creation is enabled
	EnvDynamicSGEnabled = "X_CSI_DYNAMIC_SG_ENABLED"

	// EnvSGVolumeLimit is an env variable which indicates the configured storage group volume limit
	EnvSGVolumeLimit = "X_CSI_STORAGE_GROUP_VOLUME_LIMIT"

	// EnvFsCheckEnabled enables file system check before mount
	EnvFsCheckEnabled = "X_CSI_FS_CHECK_ENABLED"

	// EnvFsCheckMode controls the file system check operation mode
	EnvFsCheckMode = "X_CSI_FS_CHECK_MODE"
	// EnvSpaceReclamationEnabled enables/disables space reclamation
	EnvSpaceReclamationEnabled = "X_CSI_SPACE_RECLAMATION_ENABLED"

	// EnvSpaceReclamationSchedule is the cron schedule for space reclamation
	EnvSpaceReclamationSchedule = "X_CSI_SPACE_RECLAMATION_SCHEDULE"

	// EnvSpaceReclamationMaxConcurrent is the max concurrent reclamation operations
	EnvSpaceReclamationMaxConcurrent = "X_CSI_SPACE_RECLAMATION_MAX_CONCURRENT"

	// EnvSpaceReclamationTimeout is the timeout for each reclamation operation
	EnvSpaceReclamationTimeout = "X_CSI_SPACE_RECLAMATION_TIMEOUT"

	// EnvProxyAuthTokenFile is the path to a file containing a shared auth token
	// used for authenticating requests between the CSI driver and the reverse proxy.
	// Both driver and proxy must mount the same K8s Secret.
	EnvProxyAuthTokenFile = "X_CSI_REVPROXY_AUTH_TOKEN_FILE" // #nosec G101

	// EnvMetricsEnabled controls whether the driver metrics endpoint is active
	EnvMetricsEnabled = "X_CSI_METRICS_ENABLED"

	// EnvMetricsPort is the TCP port on which the metrics HTTP(S) server listens
	EnvMetricsPort = "X_CSI_METRICS_PORT"

	// EnvMetricsTLSCertFile is the path to the TLS certificate file for the metrics server
	EnvMetricsTLSCertFile = "X_CSI_METRICS_TLS_CERT_FILE" // #nosec G101

	// EnvMetricsTLSKeyFile is the path to the TLS private key file for the metrics server
	EnvMetricsTLSKeyFile = "X_CSI_METRICS_TLS_KEY_FILE" // #nosec G101

	// EnvMetricsArrayCBThreshold is the circuit breaker failure threshold for PowerMax API calls
	EnvMetricsArrayCBThreshold = "X_CSI_METRICS_ARRAY_CB_THRESHOLD"

	// EnvMetricsArrayCBResetTimeout is the circuit breaker reset timeout for PowerMax API calls
	EnvMetricsArrayCBResetTimeout = "X_CSI_METRICS_ARRAY_CB_RESET_TIMEOUT"

	// EnvMetricsArrayTimeout is the timeout for PowerMax metrics API calls
	EnvMetricsArrayTimeout = "X_CSI_METRICS_ARRAY_TIMEOUT"

	// EnvMetricsArrayRateLimit is the rate limit for metrics API calls
	EnvMetricsArrayRateLimit = "X_CSI_METRICS_ARRAY_RATE_LIMIT"

	// EnvMetricsCollectionInterval is the collection interval for metrics collection
	EnvMetricsCollectionInterval = "X_CSI_METRICS_COLLECTION_INTERVAL"

	// EnvMetricsCollectionCacheTTL is the cache TTL for metrics results
	EnvMetricsCollectionCacheTTL = "X_CSI_METRICS_COLLECTION_CACHE_TTL"

	// EnvHostManagementMode controls how the driver manages host objects on
	// PowerMax arrays. Valid values: "create" (default) creates new hosts,
	// "adopt" discovers and adopts pre-existing BFS hosts by WWPN match.
	EnvHostManagementMode = "X_CSI_POWERMAX_HOST_MGMT_MODE"

	// EnvNVMeTCPConnMode selects who owns NVMe/TCP fabric sessions. Valid values:
	// "driver" (default) has the driver discover targets and connect, "host" has
	// the driver use sessions the host established and never create its own.
	// The value applies to every array in X_CSI_MANAGED_ARRAYS.
	EnvNVMeTCPConnMode = "X_CSI_POWERMAX_NVMETCP_CONN_MODE"

	// EnvHostAdoptionMinOverlapRatio controls the minimum overlap ratio for
	// host adoption validation. Valid values: 0.0 to 1.0. Default is 0.5 for
	// directional relaxation with strict majority. For 2-WWPN systems, this
	// is treated as 1.0 (100% coverage required). Regulated environments
	// can set to 1.0 for strict match enforcement.
	EnvHostAdoptionMinOverlapRatio = "X_CSI_POWERMAX_HOST_ADOPTION_MIN_OVERLAP_RATIO"

	// EnvMetroSiteFailureHandlingEnabled enables SRDF/Metro site-failure
	// handling: automatic winner/loser detection, degraded-mode I/O routing,
	// deferred operation queuing, and reconciliation after site recovery.
	EnvMetroSiteFailureHandlingEnabled = "X_CSI_POWERMAX_METRO_SITE_FAILURE_HANDLING_ENABLED"

	// EnvMetroStateCheckTimeout is the timeout in seconds for the Metro
	// state detection API call to each array (default: 15).
	EnvMetroStateCheckTimeout = "X_CSI_POWERMAX_METRO_STATE_CHECK_TIMEOUT"

	// EnvMetroQueueWarningThreshold overrides the default deferred operation
	// queue depth at which a warning event is emitted (default: 75).
	EnvMetroQueueWarningThreshold = "X_CSI_POWERMAX_METRO_QUEUE_WARNING_THRESHOLD"

	// EnvMetroQueueHardLimit overrides the default deferred operation queue
	// depth at which new deferrals are rejected (default: 100).
	EnvMetroQueueHardLimit = "X_CSI_POWERMAX_METRO_QUEUE_HARD_LIMIT"

	// EnvMetroReconciliationBackoff is the base backoff duration in seconds
	// for reconciliation retry attempts (default: 5).
	EnvMetroReconciliationBackoff = "X_CSI_POWERMAX_METRO_RECONCILIATION_BACKOFF"

	// EnvCapacityPollInterval is the interval at which the background capacity
	// poller refreshes per-array free-capacity utilization and health for
	// multi-array zones. Accepts a Go duration string (e.g. "5m"). Default: 5m.
	EnvCapacityPollInterval = "X_CSI_CAPACITY_POLL_INTERVAL"

	// EnvCapacityThresholdFull is the capacity utilization percentage (0-100)
	// at which an array is considered full for warning-event purposes.
	// A warning event is emitted when utilization crosses (threshold - 10)
	// percent. Default: 100.
	EnvCapacityThresholdFull = "X_CSI_CAPACITY_THRESHOLD_FULL"

	// EnvPodName is the name of the controller pod, injected via the Kubernetes
	// downward API (fieldRef: metadata.name). Used by Metro site-failure handling
	// to scope Kubernetes events and Lease objects to the running pod.
	EnvPodName = "POD_NAME"

	// EnvDriverNamespace is the namespace in which the controller pod runs,
	// injected via the Kubernetes downward API (fieldRef: metadata.namespace).
	// Used by Metro site-failure handling to create namespace-scoped Lease
	// objects and to emit events visible via `kubectl get events -n <namespace>`.
	EnvDriverNamespace = "X_CSI_DRIVER_NAMESPACE"

	// EnvDriverInstanceUID is the unique identifier of the CSM instance,
	// injected via the Kubernetes downward API (fieldRef: metadata.uid).
	// Used by Metro site-failure handling for ownership isolation of VolumeJournal CRD resources.
	EnvDriverInstanceUID = "X_CSI_DRIVER_INSTANCE_UID"
)
