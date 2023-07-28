#!/bin/bash

TEST=""
NAMESPACE="test"

# Usage information
function usage {
   echo
   echo "`basename ${0}`"
   echo "    -t test         - Test to stop"
   echo "    -n namespace    - Namespace in which the release is running. Default is: ${NAMESPACE}"
   echo "    -h help          - Help"
}

# Parse the options passed on the command line
while getopts "t:n:h" opt; do
  case $opt in
    t)
      TEST="${OPTARG}"
      ;;
    n)
      NAMESPACE="${OPTARG}"
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

# Ensure a test was named and that it exists
if [ "${TEST}" == "" ]; then
  echo "The name of a test must be specified"
  usage
fi
if [ ! -d "${TEST}" ]; then
  echo "Unable to find test named: ${TEST}"
  usage
fi

# the helm release name will be the basename of the test
RELEASE=`basename "${TEST}"`

VALUES="__${NAMESPACE}-${RELEASE}__.yaml"

helm -n ${NAMESPACE} delete "${RELEASE}"
sleep 10
kubectl get pods -n "${NAMESPACE}"
echo "waiting for persistent volumes to be cleaned up"
sleep 90
bash deletepvcs.sh -n "${NAMESPACE}"
kubectl get persistentvolumes -o wide

if [ -f "${VALUES}" ]; then
  rm "${VALUES}"
fi



