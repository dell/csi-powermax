#!/bin/bash

# Copyright © 2020-2025 Dell Inc. or its subsidiaries. All Rights Reserved.
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
#

#!/bin/bash

DEFAULT_NAMESPACE="test"
DEFAULT_SC="powermax"
DEFAULT_SC_SUFFIX="xfs"

# Usage information
function usage {
   echo
   echo "`basename ${0}`"
   echo "    -n namespace     - Namespace in which to place the test. Default is: ${DEFAULT_NAMESPACE}"
   echo "    -s storageclass  - Storage Class to be used for creating PVCs. Default is ${DEFAULT_SC}"
   echo "                     - XFS storage class must exist with a suffix $DEFAULT_SC_SUFFIX"
   echo "    -h help          - Help"
}

# Parse the options passed on the command line
while getopts "n:s:h" opt; do
  case $opt in
    n)
      NAMESPACE="${OPTARG}"
      ;;
    s)
      STORAGE_CLASS="${OPTARG}"
      ;;
    h)
      usage
      exit 0
      ;;
    \?)
      echo "Invalid option: -$OPTARG" >&2
      usage
      exit 1
      ;;
    :)
      echo "Option -$OPTARG requires an argument." >&2
      usage
      exit 1
      ;;
  esac
done

if [ "${NAMESPACE}" == "" ]; then
  echo "Namespace not specified. Defaulting to $DEFAULT_NAMESPACE"
  NAMESPACE=$DEFAULT_NAMESPACE
fi

# Validate that the namespace exists
NUM=`kubectl get namespaces | grep "^${NAMESPACE} " | wc -l`
if [ $NUM -ne 1 ]; then
  echo "Unable to find a namespace called: ${NAMESPACE}"
  exit 1
fi

# Validate that the storage class exists
if [ "${STORAGE_CLASS}" == "" ]; then
  echo "Storage Class not specified. Defaulting to $DEFAULT_SC"
  STORAGE_CLASS=$DEFAULT_SC
fi
STORAGE_CLASS_XFS="$STORAGE_CLASS-$DEFAULT_SC_SUFFIX"

SC=`kubectl get sc $STORAGE_CLASS`
if [ $? -ne 0 ]; then
  echo "Error in fetching storage class $STORAGE_CLASS. Make sure it exists"
  exit 1
fi

SC=`kubectl get sc $STORAGE_CLASS_XFS`
if [ $? -ne 0 ]; then
  echo "Error in fetching storage class $STORAGE_CLASS_XFS. Make sure it exists"
  exit 1
fi

echo "installing a 2 volume container"
bash starttest.sh -t 2vols -n $NAMESPACE -s $STORAGE_CLASS
echo "done installing a 2 volume container"
echo "marking volume"
kubectl exec -n $NAMESPACE powermaxtest-0 -- touch /data0/orig
kubectl exec -n $NAMESPACE powermaxtest-0 -- ls -l /data0
kubectl exec -n $NAMESPACE powermaxtest-0 -- sync
kubectl exec -n $NAMESPACE powermaxtest-0 -- sync
echo "creating snap1 of pvol0"
kubectl create -f snap1.yaml --namespace $NAMESPACE
sleep 10
kubectl get volumesnapshot -n $NAMESPACE
echo "updating container to add a volume sourced from snapshot"
helm upgrade -n $NAMESPACE 2vols 2vols+restore --set sc=$STORAGE_CLASS --set scxfs=$STORAGE_CLASS_XFS
echo "waiting for container to upgrade/stabilize"
sleep 20
up=0
while [ $up -lt 1 ];
do
    sleep 5
    kubectl get pods -n $NAMESPACE
    up=`kubectl get pods -n $NAMESPACE | grep '1/1 *Running' | wc -l`
done
kubectl describe pods -n $NAMESPACE
kubectl exec -n $NAMESPACE powermaxtest-0 -- df | grep data
kubectl exec -n $NAMESPACE powermaxtest-0 -- mount | grep data
echo "updating container finished"
echo "marking volume"
kubectl exec -n $NAMESPACE powermaxtest-0 -- touch /data2/new
echo "listing /data0"
kubectl exec -n $NAMESPACE powermaxtest-0 -- ls -l /data0
echo "listing /data2"
kubectl exec -n $NAMESPACE powermaxtest-0 -- ls -l /data2
sleep 20

echo "deleting container"
echo helm delete -n $NAMESPACE 2vols
helm delete -n $NAMESPACE 2vols

echo "delete the snapshot"
echo kubectl delete volumesnapshot -n $NAMESPACE pvol0-snap1
kubectl delete volumesnapshot -n $NAMESPACE pvol0-snap1
sleep 10
kubectl get volumesnapshot -n $NAMESPACE

echo "deleting the pvcs"
echo bash deletepvcs.sh -sh -n $NAMESPACE
bash deletepvcs.sh -n $NAMESPACE
sleep 20
kubectl get pvc -n $NAMESPACE

echo "removing the lock file"
rm -f "__test-2vols__.yaml"

