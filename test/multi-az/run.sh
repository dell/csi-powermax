# Copyright © 2025 Dell Inc. or its subsidiaries. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#      http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

#####################################################################
# This script will run multi-az testing.
# It is assumed that it is running on a machine with kubeconfig
# in place for a 2 worker node cluster.
# It is also assumed that the latest version of CSM Operator is installed
# on the cluster for deploying the driver.
#####################################################################

#!/bin/bash

# TODO: use env.sh to get all secret/configmap fields

# this will add zone label zoneA to worker-1 and zoneB to worker 2
# since we want to support multi-AZ matching EVERY label, we're adding two separate labels
./scripts/modify_zoning_labels.sh add zone.topology.kubernetes.io/region=zoneA zone.topology.kubernetes.io/region=zoneB
./scripts/modify_zoning_labels.sh add zone.topology.kubernetes.io/zone=zoneC zone.topology.kubernetes.io/zone=zoneD

# create a directory to store temporary files generated from templates
mkdir ./testfiles/tmp

# TODO: create a copy of the template secret yaml and populate it with values from env.sh
# For now, we are simply using a config file that contains all relevant values for prototyping/configurability
./scripts/replace.sh ./config ./testfiles/template-powermax-secret.yaml ./testfiles/tmp/secret.yaml

# TODO: create a copy of the template configmap yaml and populate it with values from env.sh

# TODO: create a copy of the template csm object yaml and populate it with values from env.sh

# apply all necessary yamls to the cluster
#kubectl apply -f ./testfiles/tmp/secret.yaml
#kubectl apply -f ./testfiles/tmp/configmap.yaml
#kubectl apply -f ./testfiles/tmp/csm.yaml

# TODO: fix the below zoning labels check to work for powermax instead of powerflex
# currently does not work - was originally designed for powerflex and will need retooling
# intent for this method is to compare zones in secret to zones in the nodes and in the pods
# then verify that everything matches.
# ./modify_zoning_labels.sh validate-zoning

# TODO: perform cert-csi test that will verify functionality

# this removes all labels that begin with the text 'zone', created above
./scripts/modify_zoning_labels.sh remove-all-zones

# TODO: remove storage class

# TODO: remove csm object for powermax driver

# TODO: remove configmap and secret

# delete temporary testfiles
rm -rf ./testfiles/tmp