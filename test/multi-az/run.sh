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
# TODO: use env.sh to get all secret/configmap fields instead of the current config file
CONFIGFILE="./config"

# get the nodes, for verification of zoning
nodes=($(kubectl get nodes -A | grep -v -E 'master|control-plane' | grep -v NAME | awk '{ print $1 }'))
node1=${nodes[0]}
node2=${nodes[1]}

main() {
    # perform universal setup steps
    setup

    ##################################
    ##################################
    ### TEST CASES ###################
    ##################################
    ##################################


    #############
    # Test case 1: zones in secret, zones in storage classes, nodes correctly applied
    #############
    print_test_case "TEST CASE 1"
    # this will add zone label zoneA to worker-1 and zoneB to worker 2
    # since we want to support multi-AZ matching EVERY label, we're adding two separate labels
    echo "Adding labels to worker nodes for topology..."
    ./scripts/modify_zoning_labels.sh add zone.topology.kubernetes.io/region=zoneA zone.topology.kubernetes.io/region=zoneB
    ./scripts/modify_zoning_labels.sh add zone.topology.kubernetes.io/zone=zoneC zone.topology.kubernetes.io/zone=zoneD

    # apply the kubernetes objects for this test case
    echo "Applying test case YAML files via kubectl..."
    kubectl create secret generic powermax-creds --namespace powermax --from-file=config=./testfiles/tmp/secret.yaml
    kubectl apply -f ./testfiles/tmp/powermax-configmap.yaml
    kubectl apply -f ./testfiles/tmp/powermax-csm.yaml

    # Wait for all pods to be ready
    echo "Waiting for driver pods to be ready..."
    ./scripts/waitForPowerMaxPods.sh

    # Verify that all nodes that have a pod running have their labels accordingly
    echo "Verifying node labels match pods + secrets..."
    ./scripts/modify_zoning_labels.sh validate-zoning powermax

    # these will verify that zoning works using storage classes that have zone information
    echo "Testing zone 1..."
    ./scripts/workload.sh create_app zone-a-test pmax-mz-1
    ./scripts/workload.sh validate_app zone-a-test $node1
    ./scripts/workload.sh delete_app zone-a-test

    echo "Testing zone 2..."
    ./scripts/workload.sh create_app zone-b-test pmax-mz-2
    ./scripts/workload.sh validate_app zone-b-test $node2
    ./scripts/workload.sh delete_app zone-b-test

    # this will verify that provisioning with no zone in storage class still works
    # no validate app step-- it coming online is sufficient, and we do not care which zone it's on
    # that will be decided by the allowed topologies of the node the workload is put on.
    echo "Creating workload with no specific zone in storage class..."
    ./scripts/workload.sh create_app zoneless-test pmax-mz-none
    echo "Removing workload after successful install..."
    ./scripts/workload.sh delete_app zoneless-test

    ##################################
    ##################################
    ### END TEST CASES ###############
    ##################################
    ##################################
    # TODO: Some form of summary of the test output would be good to print here, so the user doesn't have to scroll
    # remove all artifacts for clean environment
    cleanup
}

# Generic setup: create all temp files using replacements, apply all of the ones that do not need to be test case specific
# this includes all storage classes, the SSL secret, 
setup() {
    # TODO: Make a blanket copy-replace on ALL template files into the tmp directory (also remove 'template-'), removing the need to individually make each file. 
    # create the secret yaml file
    ./scripts/replace.sh $CONFIGFILE ./testfiles/templates/template-powermax-secret.yaml ./testfiles/tmp/secret.yaml

    # Create the powermax-array-config configmap yaml file
    ./scripts/replace.sh $CONFIGFILE ./testfiles/templates/template-powermax-configmap.yaml ./testfiles/tmp/powermax-configmap.yaml

    # Create the CSM object yaml file
    ./scripts/replace.sh $CONFIGFILE ./testfiles/templates/template-powermax-csm.yaml ./testfiles/tmp/powermax-csm.yaml

    # Set up storageclasses-- two zoned in the SC and one without zoning right now
    # TODO: SCs for more use cases
    ./scripts/replace.sh $CONFIGFILE ./testfiles/templates/template-powermax-storageclass-1.yaml  ./testfiles/tmp/sc1.yaml
    ./scripts/replace.sh $CONFIGFILE ./testfiles/templates/template-powermax-storageclass-2.yaml  ./testfiles/tmp/sc2.yaml
    ./scripts/replace.sh $CONFIGFILE ./testfiles/templates/template-powermax-storageclass-zoneless.yaml  ./testfiles/tmp/sc3.yaml

    # Ensure the namespace exists
    kubectl create ns powermax

    # Create SSL secret for reverse proxy
    # This should be universal across all tests, so we can do it here. 
    echo "Generating SSL secret..."
    openssl genrsa -out ./testfiles/tmp/tls.key 2048
    openssl req -new -key ./testfiles/tmp/tls.key -out ./testfiles/tmp/tls.csr -config ./testfiles/openssl.cnf
    openssl x509 -req -in ./testfiles/tmp/tls.csr -signkey ./testfiles/tmp/tls.key -out ./testfiles/tmp/tls.crt -days 3650 -extensions req_ext -extfile ./testfiles/openssl.cnf
    kubectl create secret -n powermax tls csirevproxy-tls-secret --cert=./testfiles/tmp/tls.crt --key=./testfiles/tmp/tls.key

    # Apply all universally used yamls to the cluster
    echo "Applying storage class YAML files via kubectl..."
    kubectl apply -f ./testfiles/tmp/sc1.yaml
    kubectl apply -f ./testfiles/tmp/sc2.yaml
    kubectl apply -f ./testfiles/tmp/sc3.yaml
}

# TODO: Finish this, it'll simplify adding more files later. Untested and mostly generated by codeium.
# Will also need other code changes.
replace_all_templates() {
    # create a directory to store temporary files generated from templates
    echo "Generating temporary YAML files for kubectl..."
    mkdir ./testfiles/tmp    
    for file in "./testfiles/templates/*"; do
        output_file=${file#"template-"}
        output_file="./testfiles/tmp/${output_file##*/}"
        ./scripts/replace.sh $CONFIGFILE $file $output_file

}

# Header print for test cases
print_test_case() {
    echo "##########"
    echo $1
    echo "##########"
}

# Secrets and configmaps should be removed before creating the new ones from the files. 
remove_secret() {
    kubectl delete secret -n powermax powermax-creds
}
remove_configmap() {
    kubectl delete cm -n powermax powermax-array-config
}

# After replacing the secret, the driver must be restarted. 
restart_driver() {
    kubectl -n powermax rollout restart deploy/powermax-controller
    kubectl -n powermax rollout restart ds/powermax-node
    sleep 5 # so the pods have time to restart/clear their uptime
    ./scripts/waitForPowerMaxPods.sh
}

# This method sets the environment entirely clean-- deletes all storage classes, CSM objects, node labels, everything.
cleanup() {
    # this removes all labels that begin with the text 'zone', created above
    echo "Removing zoning labels from nodes..."
    ./scripts/modify_zoning_labels.sh remove-all-zones

    # Clean up resources created during this test
    # Commented out right now for debugging 
    echo "Removing all Kubernetes objects from this test via kubectl..."
    kubectl delete csm -n powermax powermax
    remove_secret
    remove_configmap
    kubectl delete secret -n powermax csirevproxy-tls-secret
    kubectl delete sc pmax-mz-1
    kubectl delete sc pmax-mz-2
    kubectl delete sc pmax-mz-none

    # delete temporary testfiles
    echo "Removing temporary YAML files as final cleanup step..."
    rm -rf ./testfiles/tmp
}

main