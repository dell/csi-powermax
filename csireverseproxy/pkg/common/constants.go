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

package common

// Constants for the proxy
const (
	DefaultCertFile             = "tls.crt"
	DefaultKeyFile              = "tls.key"
	DefaultTLSCertDirName       = "tls"
	DefaultCertDirName          = "certs"
	DefaultConfigFileName       = "config"
	DefaultConfigDir            = "deploy"
	DefaultPort                 = "2222"
	TestConfigDir               = "test-config"
	TempConfigDir               = "test-config/tmp"
	TestConfigFileName          = "config.yaml"
	TestSecretFileName          = "secret-config.yaml" // #nosec G101
	EnvCertDirName              = "X_CSI_REVPROXY_CERT_DIR"
	EnvTLSCertDirName           = "X_CSI_REVPROXY_TLS_CERT_DIR"
	EnvWatchNameSpace           = "X_CSI_REVPROXY_WATCH_NAMESPACE"
	EnvConfigFileName           = "X_CSI_REVPROXY_CONFIG_FILE_NAME"
	EnvConfigDirName            = "X_CSI_REVPROXY_CONFIG_DIR"
	EnvInClusterConfig          = "X_CSI_REVPROXY_IN_CLUSTER"
	EnvIsLeaderElectionEnabled  = "X_CSI_REVPROXY_IS_LEADER_ENABLED"
	EnvSecretFilePath           = "X_CSI_REVPROXY_SECRET_FILEPATH" // #nosec G101
	EnvReverseProxyUseSecret    = "X_CSI_REVPROXY_USE_SECRET"      // #nosec G101
	EnvPowermaxConfigPath       = "X_CSI_POWERMAX_CONFIG_PATH"
	DefaultNameSpace            = "powermax"
	MaxActiveReadRequests       = 5
	MaxOutStandingWriteRequests = 50
	MaxActiveWriteRequests      = 4
	MaxOutStandingReadRequests  = 50
	DefaultSecretPath           = "/etc/powermax/config" // #nosec G101
	// EnvSidecarProxyPort is the port on which the reverse proxy
	// server run, if run as a sidecar container
	EnvSidecarProxyPort = "X_CSI_POWERMAX_SIDECAR_PROXY_PORT"
)
