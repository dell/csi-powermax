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

# Create the powermax-array-config configmap
./scripts/replace.sh ./config ./testfiles/template-powermax-configmap.yaml ./testfiles/tmp/powermax-configmap.yaml

# TODO: create a copy of the template csm object yaml and populate it once configmap powermax-array-config becomes optional

# Set up storageclass
./scripts/replace.sh ./config ./testfiles/template-powermax-storageclass.yaml  ./testfiles/tmp/sc.yaml


#Create SSL secret for reverse proxy
openssl genrsa -out ./testfiles/tmp/tls.key 2048
openssl req -new -key ./testfiles/tmp/tls.key -out ./testfiles/tmp/tls.csr -config ./testfiles/openssl.cnf
openssl x509 -req -in ./testfiles/tmp/tls.csr -signkey ./testfiles/tmp/tls.key -out ./testfiles/tmp/tls.crt -days 3650 -extensions req_ext -extfile ./testfiles/openssl.cnf
kubectl create secret -n powermax tls csirevproxy-tls-secret --cert=./testfiles/tmp/tls.crt --key=./testfiles/tmp/tls.key

# Apply all necessary yamls to the cluster
kubectl create secret generic powermax-creds --namespace powermax --from-file=config=./testfiles/tmp/secret.yaml
kubectl apply -f ./testfiles/tmp/powermax-configmap.yaml
kubectl apply -f ./testfiles/tmp/sc.yaml
kubectl apply -f ./testfiles/template-powermax-csm.yaml


scripts/waitForPowerMaxPods.sh

# TODO: fix the below zoning labels check to work for powermax instead of powerflex
# currently does not work - was originally designed for powerflex and will need retooling
# intent for this method is to compare zones in secret to zones in the nodes and in the pods
# then verify that everything matches.
# ./modify_zoning_labels.sh validate-zoning

# TODO: perform cert-csi test that will verify functionality

# this removes all labels that begin with the text 'zone', created above
./scripts/modify_zoning_labels.sh remove-all-zones

# Clean up resources created during this test
# Commented out right now for debugging 
#kubectl delete csm -n powermax powermax
#kubectl delete secret -n powermax powermax-creds
#kubectl delete cm -n powermax powermax-array-config
#kubectl delete secret -n powermax csirevproxy-tls-secret
#kubectl delete sc pmax-mz-1

# delete temporary testfiles
#rm -rf ./testfiles/tmp
