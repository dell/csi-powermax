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

get_size() {
   echo $(kubectl exec -n $NAMESPACE powermaxtest-0 -- df -h /data0 | awk 'NR==2 {print $2}')
}

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

echo "installing a 1 volume container"
bash starttest.sh -t 1vol -n $NAMESPACE -s $STORAGE_CLASS
echo "done installing a 1 volume container"
echo "marking volume"
echo "creating a file on the volume"
kubectl exec -n $NAMESPACE powermaxtest-0 -- touch /data0/orig
kubectl exec -n $NAMESPACE powermaxtest-0 -- ls -l /data0
kubectl exec -n $NAMESPACE powermaxtest-0 -- sync
kubectl exec -n $NAMESPACE powermaxtest-0 -- sync
echo
echo "calculating the initial size of the volume"
initialSize=$(get_size)
echo "INITIAL SIZE: " $initialSize
echo
echo
echo "calculating checksum of /data0/orig"
data0checksum=$(kubectl exec powermaxtest-0 -n $NAMESPACE -- md5sum /data0/orig)
echo $data0checksum
echo
echo
echo "expanding the volume"
cat 1vol/templates/pvc0.yaml  | sed "s/{{ .Values.sc }}/$STORAGE_CLASS/g;s/{{ .Values.namespace }}/$NAMESPACE/g;s/storage: [0-9]\+Gi/storage: 15Gi/" | kubectl apply -f -
size=$(get_size)
echo -ne "Processing: "
while [ "$initialSize" = "$size" ]; do
  sleep 3
  size=$(get_size)
  echo -ne "#"
done
echo
echo
echo
sleep 5
echo "volume expanded to $(get_size)"
echo "calculating checksum again"
newdata0checksum=$(kubectl exec powermaxtest-0 -n $NAMESPACE -- md5sum /data0/orig)
echo $newdata0checksum
echo
echo
echo
echo "Comparing checksums"
echo $data0checksum
echo $newdata0checksum
data0chs=$(echo $data0checksum | awk '{print $1}')
newdata0chs=$(echo $newdata0checksum | awk '{print $1}')
if [ "$data0chs" = "$newdata0chs" ]; then
echo "Both the checksums match!!!"
else
echo "Checksums don't match"
fi
echo
echo

sleep 5
echo "cleaning up..."
bash stoptest.sh -t 1vol -n $NAMESPACE

