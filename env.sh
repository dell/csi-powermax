#!/bin/bash

export X_CSI_POWERMAX_CONFIG_PATH="deploy/config.yaml"

# Comma separated list of array managed by the driver
export X_CSI_MANAGED_ARRAYS="000000000001"

# HTTP endpoint of Unisphere
export X_CSI_POWERMAX_ENDPOINT="https://0.0.0.1:8443"

# EnvUser is the name of the environment variable used to set the
# username when authenticating to Unisphere
export X_CSI_POWERMAX_USER="username"

# EnvPassword is the name of the environment variable used to set the
# user's password when authenticating to Unisphere
export X_CSI_POWERMAX_PASSWORD="password"

# EnvSkipCertificateValidation  is the name of the environment variable used to specify
# that Unisphere's certificate chain and hostname should not
# be verified
export X_CSI_POWERMAX_SKIP_CERTIFICATE_VALIDATION="true"

# EnvNodeName is the name of the environment variable used to set the
# hostname where the node service is running
export X_CSI_POWERMAX_NODENAME=`hostname`

# EnvClusterPrefix is the name of the environment variable that is used
# to specify as a prefix to apply to objects created via this K8s cluster
export X_CSI_K8S_CLUSTER_PREFIX="XYZ"

# EnvAutoProbe is the name of the environment variable used to specify
# that the controller service should automatically probe itself if it
# receives incoming requests before having been probed, indirect
# violation of the CSI spec
export X_CSI_POWERMAX_AUTOPROBE="true"

# EnvPortGroups is the name of the environment variable that is used
# to specify a list of Port Groups that the driver can choose from
# These Port Groups must exist and be populated
export X_CSI_POWERMAX_PORTGROUPS="iscsi_ports"

# Enable/Disable CSI request and response logging
# setting them to true sets X_CSI_REQ_ID_INJECTION to true 
export X_CSI_REQ_LOGGING="true"
export X_CSI_REP_LOGGING="true"

# Variables for the integration test code.
export CSI_ENDPOINT=`pwd`/unix_sock
export SYMID="000000000001"
export SRPID="SRP_1"
export SERVICELEVEL="Bronze"
export LOCALRDFGROUP="000"
export REMOTESYMID="000000000001"
export REMOTESERVICELEVEL="Gold"
export X_CSI_ENABLE_BLOCK="true"
export REMOTERDFGROUP="000"
export X_CSI_REPLICATION_CONTEXT_PREFIX="powermax"
export X_CSI_REPLICATION_PREFIX="replication.storage.dell.com"

# EnvPreferredTransportProtocol enables you to be able to force the transport protocol.
# Valid values are "FC" or "ISCSI" or "". Value "" will choose FC if both are available.
# This is mainly for testing.
export X_CSI_TRANSPORT_PROTOCOL=""
# Setting X_CSI_REVERSE_PROXY_ENABLED tells the driver that the endpoint being connected to is
# actually the reverse proxy server
export X_CSI_REVERSE_PROXY_ENABLED=""
# Set this value to a higher number is a proxy is being used
export X_CSI_GRPC_MAX_THREADS="50"
# Set this value to the timeout as: "300ms", "1.5h" or "2h45m".
# Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h"
export X_CSI_UNISPHERE_TIMEOUT="5m"
