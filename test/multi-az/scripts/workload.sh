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
DEFAULT_WAIT_SECONDS=180

function create_app() {
  app_name=$1
  storage_class=$2
  node_name=$3

  print_msg "Creating app $app_name in namespace $APP_NAMESPACE..."

  # validate inputs
  if [ -z "$app_name" ] || [ -z "$node_name" ] || [ -z "$storage_class" ]; then
    print_err "app name or node name or storage class not specified"
    return 1
  fi

  # check that the app does not exist yet
  if kubectl -n $APP_NAMESPACE get pod $app_name &>/dev/null; then
    print_err "cannot create the app $app_name in namespace $APP_NAMESPACE, since it already exists"
    return 1
  fi

  # make sure the app namespace exists
  if ! kubectl get ns $APP_NAMESPACE &>/dev/null; then
    if ! kubectl create ns $APP_NAMESPACE; then
      print_err "failed to create test app namespace"
      return 1
    fi
  fi

  # create a workload app
  make_app_spec $app_name $storage_class $node_name | kubectl -n $APP_NAMESPACE create -f -

  # wait until the app pod is in the Running state
  if ! wait_pod_ready $app_name; then
    print_err "timed out waiting for the app pod $app_name in namespace $APP_NAMESPACE to be Running"
    return 1
  fi
}

function delete_app() {
  app_name=$1

  print_msg "Deleting app $app_name in namespace $APP_NAMESPACE..."

  # validate inputs
  if [ -z "$app_name" ]; then
    print_err "app name not specified"
    return 1
  fi

  # delete app pod if it exists
  if kubectl -n $APP_NAMESPACE get pod $app_name &>/dev/null; then
    make_app_spec $app_name $storage_class $node_name | kubectl -n $APP_NAMESPACE delete -f -
    if [ $? -ne 0 ]; then
      print_err "failed to delete app $app_name in namespace $APP_NAMESPACE"
      return 1
    fi
#    if ! kubectl -n $APP_NAMESPACE delete pod $app_name; then
#      print_err "failed to delete pod $app_name in namespace $APP_NAMESPACE"
#      return 1
#    fi
  fi

  # check that the app pod has been deleted
  if ! check_no_pod $app_name; then
    print_err "the app pod $app_name in namespace $APP_NAMESPACE is not fully deleted"
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
    if ! check_pod_ready $app_name; then
      print_err "app pod $app_name in namespace $APP_NAMESPACE state hasn't been stable for 10 seconds"
      return 1
    fi
    sleep 2
  done

}

make_app_spec() {
    app_name=$1
    storage_class=$2
    node_name=$3

    cat << EOF
apiVersion: v1
kind: Pod
metadata:
  name: $app_name
spec:
  nodeName: $node_name
  containers:
  - name: $app_name
    image: $SIMPLE_APP_IMG
    command: ["/bin/sh"]
    args: ["-c", "tail -f /dev/null"]
    volumeMounts:
    - name: pmaxvol
      mountPath: /data
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
  - ReadWriteOnce
  resources:
    requests:
      storage: 8Gi
  storageClassName: $storage_class
  volumeMode: Filesystem
EOF
}

k8s_count_resource() {
  resource=$1
  kubectl -n $APP_NAMESPACE get $resource -o name | wc -l
}

check_pod_ready() {
  pod_name=$1
  res=$(kubectl -n $APP_NAMESPACE get pod $pod_name --no-headers 2>/dev/null | awk '$3=="Running" {print "true"}')
  [ "$res" == "true" ]
}

check_no_pod() {
  pod_name=$1
  if [ -z "$pod_name" ]; then
      [ $(k8s_count_resource pod) -eq 0 ]
  else
      kubectl -n $APP_NAMESPACE get pod $pod_name -o name >/dev/null 2>&1 && return 1 || return 0
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

wait_pod_ready() {
  pod_name=$1
  echo -n "waiting pod ready "
  wait_with_timeout $DEFAULT_WAIT_SECONDS check_pod_ready $pod_name
}

create_app "abtest" "vxflexos" "worker-2-pklwnootxu0ap.domain"
validate_app "abtest"
delete_app "abtest"
