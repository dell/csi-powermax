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


# Make sure to run the 2vols test before running this test
VALUES="__$NAMESPACE-2vols__.yaml"
if [ -f "${VALUES}" ]; then
  echo "2vols test is running"
else
  echo "Error: Make sure to run the 2vols test before running this test"
  exit 1
fi

echo "******************Begin Test*********************"
echo "creating snapshot of pvol1 (xfs volume)"
echo "************************************************"
kubectl create -f snap2.yaml --namespace $NAMESPACE
echo "Sleeping for 25 seconds to allow creation of snapshot"
sleep 25
kubectl get volumesnapshot -n $NAMESPACE
echo "************************************************"
echo "Creating a new pod with a volume which is sourced from the snapshot"
echo "************************************************"
bash starttest.sh -t 1clonevol -n $NAMESPACE -s $STORAGE_CLASS
echo "Successfully created the pod"
echo "*************Test Successful********************"
echo "Deleting the pod with the cloned volume"
echo "************************************************"
echo 'helm delete -n $NAMESPACE 1clonevol'
helm delete -n $NAMESPACE 1clonevol
echo "************************************************"
echo "Deleting the PVC"
echo "************************************************"
echo "kubectl delete pvc restorepvc -n $NAMESPACE"
kubectl delete pvc restorepvc -n $NAMESPACE
echo "************************************************"
echo "Deleting the snapshot"
echo "************************************************"
kubectl delete -f snap2.yaml --namespace $NAMESPACE
echo "************************************************"
echo "Deleting the original pod"
echo "************************************************"
bash stoptest.sh -t 2vols -n $NAMESPACE
kubectl get pvc -n $NAMESPACE
rm -f __$NAMESPACE-1clonevol__.yaml
echo "*************End of test************************"

