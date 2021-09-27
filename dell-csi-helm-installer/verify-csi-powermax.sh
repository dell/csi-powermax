#!/bin/bash
#
# Copyright (c) 2021 Dell Inc., or its subsidiaries. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#  http://www.apache.org/licenses/LICENSE-2.0

# verify-csi-powermax method
function verify-csi-powermax() {
  verify_k8s_versions "1.20" "1.22"
  verify_openshift_versions "4.6" "4.8"  
  verify_helm_values_version "2.0.0"
  verify_namespace "${NS}"
  verify_required_secrets "${RELEASE}-creds"
  verify_optional_secrets "${RELEASE}-certs"
  verify_optional_secrets "csirevproxy-tls-secret"
  verify_alpha_snap_resources
  verify_snap_requirements
  verify_optional_replication_requirements
  verify_iscsi_installation
  verify_helm_3
}

function verify_optional_replication_requirements() {
  log step "Verifying Replication requirements"
  decho
  log arrow
  log smart_step "Verifying that Dell CSI Replication CRDs are available" "small"

  error=0
  # check for the CRDs. These are required for installation
  CRDS=("DellCSIReplicationGroups")
  for C in "${CRDS[@]}"; do
    # Verify if the CRD is present on the system
    run_command kubectl explain ${C} 2> /dev/null | grep "dell" --quiet
    if [ $? -ne 0 ]; then
      error=1
      found_warning "The CRD for ${C} is not installed. This needs to be installed if you are going to enable replication support"
    fi
  done
  check_error error
}

