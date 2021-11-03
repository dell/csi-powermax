#!/bin/bash

TEST=""
NAMESPACE="test"
DEFAULT_SC="powermax"
DEFAULT_SC_SUFFIX="xfs"

# Usage information
function usage {
   echo
   echo "`basename ${0}`"
   echo "    -t test          - Test to run. Should be the name of a directory holding a Helm Chart"
   echo "    -n namespace     - Namespace in which to place the test. Default is: ${NAMESPACE}"
   echo "    -s storageclass  - Storage Class to be used for creating PVCs. Default is ${DEFAULT_SC}"
   echo "                     - XFS storage class should also exist with a suffix $DEFAULT_SC_SUFFIX"
   echo "    -h help          - Help"
}

# Parse the options passed on the command line
while getopts "t:n:s:h" opt; do
  case $opt in
    t)
      TEST="${OPTARG}"
      ;;
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

waitOnRunning() {
  if [ "$1" = "" ];
    then echo "arg: target" ;
    exit 2;
  fi
  WAITINGFOR=$1

  RUNNING=$(kubectl get pods -n "${NAMESPACE}" | grep "Running" | wc -l)
  while [ $RUNNING -ne $WAITINGFOR ];
  do
          RUNNING=$(kubectl get pods -n "${NAMESPACE}" | grep "Running" | wc -l)
          CREATING=$(kubectl get pods -n "${NAMESPACE}" | grep "ContainerCreating" | wc -l)
          TERMINATING=$(kubectl get pods -n "${NAMESPACE}" | grep "Terminating" | wc -l)
          PVCS=$(kubectl get pvc -n "${NAMESPACE}" | grep -v "STATUS" | wc -l)
          date
          date >>log.output
                echo running $RUNNING creating $CREATING terminating $TERMINATING pvcs $PVCS
                echo running $RUNNING creating $CREATING terminating $TERMINATING pvcs $PVCS >>log.output
          sleep 30
  done
}


# Ensure a test was named and that it exists
if [ "${TEST}" == "" ]; then
  echo "The name of a test must be specified"
  usage
fi
if [ ! -d "${TEST}" ]; then
  echo "Unable to find test named: ${TEST}"
  usage
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

# the helm release name will be the basename of the test
RELEASE=`basename "${TEST}"`

# create a temporary values file to hold the user supplied information
VALUES="__${NAMESPACE}-${RELEASE}__.yaml"
if [ -f "${VALUES}" ]; then
  echo "There appears to already be a release/test named ${RELEASE} in namespace ${NAMESPACE}"
  echo "Please stop it with the stoptest.sh script before starting another"
  exit 1
fi

echo "namespace: ${NAMESPACE}" >> "${VALUES}"
echo "release: ${RELEASE}" >> "${VALUES}"

# Start the tests
helm install -n ${NAMESPACE} "${RELEASE}" -f "${VALUES}" "${TEST}" --set sc=$STORAGE_CLASS --set scxfs=$STORAGE_CLASS_XFS
echo "waiting 60 seconds on pod to initialize"
sleep 60
kubectl describe pods -n "${NAMESPACE}"
waitOnRunning 1
kubectl describe pods -n "${NAMESPACE}"
kubectl exec -n "${NAMESPACE}" powermaxtest-0 -it df | grep data
kubectl exec -n "${NAMESPACE}" powermaxtest-0 -it mount | grep data
