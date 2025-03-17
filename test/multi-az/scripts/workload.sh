#!/bin/bash
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

# This script contains definitions for managing test workloads.

. ./log_utils.sh

APP_NAMESPACE=pmax-az-test
SIMPLE_APP_IMG=quay.io/dell/container-storage-modules/csi-vxflexos:nightly
REPLICAS=10
DEFAULT_WAIT_SECONDS=120

# Main script calls this function to create an app that is a deployment with 10 replicas and 1 volume.
# This deployment has a soft pod anti-affinity rule to instruct k8s to try and spread replicas evenly across workers.
# Our zoning configuration, however, should overrule this even scheduling and instead cause all replicas to be scheduled
# only on worker(s) that match the zone specified in the storage class. So, if we later find any replica on any other worker,
# that would indicate a test failure.
function create_app() {
  app_name=$1
  storage_class=$2

  print_msg "Creating app $app_name in namespace $APP_NAMESPACE..."

  # validate inputs
  if [ -z "$app_name" ] || [ -z "$storage_class" ]; then
    print_err "app name or storage class not specified"
    return 1
  fi

  # check that the app does not exist yet
  if ! check_no_deployment $app_name; then
    print_err "cannot create the app deployment $app_name in namespace $APP_NAMESPACE, since it already exists"
    return 1
  fi

  # make sure the app namespace exists
  if ! kubectl get ns $APP_NAMESPACE &>/dev/null; then
    if ! kubectl create ns $APP_NAMESPACE; then
      print_err "failed to create test app namespace"
      return 1
    fi
  fi

  # create the workload app
  make_app_spec $app_name $storage_class | kubectl -n $APP_NAMESPACE create -f -

  # wait until the app is up and running
  if ! wait_app_ready $app_name; then
    print_err "timed out waiting for the app deployment $app_name in namespace $APP_NAMESPACE to be ready"
    return 1
  fi
}

function delete_app() {
  print_msg "Deleting app namespace $APP_NAMESPACE..."

  # delete app namespace with all its resources if it exists
  if kubectl get ns $APP_NAMESPACE &>/dev/null; then
    kubectl delete ns $APP_NAMESPACE
    if [ $? -ne 0 ]; then
      print_err "failed to delete app namespace $APP_NAMESPACE"
      return 1
    fi
  fi

  # check that the app namespace has been deleted
  if kubectl get ns $APP_NAMESPACE &>/dev/null; then
    print_err "the app namespace $APP_NAMESPACE is not fully deleted"
    return 1
  fi

}

function validate_app() {
  app_name=$1

  print_msg "Validating app $app_name in namespace $APP_NAMESPACE..."

  # validate inputs
  if [ -z "$app_name" ]; then
    print_err "app name not specified"
    return 1
  fi

  # check that the app state is stable, by observing it for 10 seconds
  for i in $(seq 5); do
    if ! check_app_ready $app_name; then
      print_err "app $app_name replicas in namespace $APP_NAMESPACE haven't been stable for 10 seconds"
      return 1
    fi
    sleep 2
  done

}

make_app_spec() {
    app_name=$1
    storage_class=$2

    cat << EOF
kind: Deployment
apiVersion: apps/v1
metadata:
  name: $app_name
spec:
  selector:
    matchLabels:
      app: $app_name
  replicas: $REPLICAS
  template:
    metadata:
      labels:
        app: $app_name
    spec:
      affinity:
        podAntiAffinity:
          preferredDuringSchedulingIgnoredDuringExecution:
          - weight: 100
            podAffinityTerm:
              labelSelector:
                matchExpressions:
                - key: app
                  operator: In
                  values:
                  - $app_name
              topologyKey: kubernetes.io/hostname
      containers:
      - name: $app_name
        image: $SIMPLE_APP_IMG
        command: ["/bin/sh"]
        args: ["-c", "tail -f /dev/null"]
        volumeDevices:
        - devicePath: "/dev/pmaxdata"
          name: pmaxvol
      volumes:
      - name: pmaxvol
        persistentVolumeClaim:
          claimName: "$app_name-vol1"
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: "$app_name-vol1"
spec:
  accessModes:
  - ReadWriteMany
  resources:
    requests:
      storage: 8Gi
  storageClassName: $storage_class
  volumeMode: Block
EOF
}

k8s_count_resource() {
  resource=$1
  kubectl -n $APP_NAMESPACE get $resource -o name | wc -l
}

check_no_deployment() {
  name=$1
  if [ -z "$name" ]; then
      [ $(k8s_count_resource deployment) -eq 0 ]
  else
      kubectl -n $APP_NAMESPACE get deployment $name -o name >/dev/null 2>&1 && return 1 || return 0
  fi
}

wait_with_timeout() {
  wait_seconds=$1
  for i in $(seq $wait_seconds); do
      [ $i -gt 1 ] && printf "\b\b\b"
      printf "%3d" $(($wait_seconds - $i + 1))
      eval "${@:2}" && echo && return 0
      [ $i -lt $wait_seconds ] && sleep 1 || echo
  done
  return 1
}

check_app_ready() {
    name=$1
    # compare 'available' against 'desired'
    res=$(kubectl -n $APP_NAMESPACE get deployment $name --no-headers 2>/dev/null | awk -v replicas=$REPLICAS '$2==$4"/"$4 && $4==replicas {print "true"}')
    [ "$res" == "true" ]
}

wait_app_ready() {
  name=$1
  echo -n "waiting deployment ready "
  wait_with_timeout $DEFAULT_WAIT_SECONDS check_app_ready $name
}

# DELETE: test calls
create_app "abtest" "vxflexos" "worker-2-pklwnootxu0ap.domain"
validate_app "abtest"
delete_app "abtest"
