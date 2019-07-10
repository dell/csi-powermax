#!/bin/sh

# rescaletest
# This script will rescale helm deployments created as part of the scaletest


TEST="50volumes"
NAMESPACE="test"
REPLICAS=-1

# Usage information
function usage {
   echo
   echo "`basename ${0}`"
   echo "    -n namespace    - Namespace in which to place the test. Default is: ${NAMESPACE}"
   echo "    -t test         - Test to run. Default is: ${TEST}. The value must point to a Helm Chart"
   echo "    -r replicas     - Number of replicas to create"
   echo
   exit 1
}

# Parse the options passed on the command line
while getopts "n:r:t:" opt; do
  case $opt in
    t)
      TEST="${OPTARG}"
      ;;
    n)
      NAMESPACE="${OPTARG}"
      ;;
	  r)
	    REPLICAS="${OPTARG}"
	    ;;
    \?)
      echo "Invalid option: -$OPTARG" >&2
      usage
      ;;
    :)
      echo "Option -$OPTARG requires an argument." >&2
      usage
      ;;
  esac
done

if [ ${REPLICAS} -eq -1 ]; then 
	echo "No value for number of replicas provided"; 
	usage
fi

TARGET=$(expr $REPLICAS \* 3)
echo "Targeting replicas: $REPLICAS"
echo "Targeting pods: $TARGET"

helm upgrade --set "name=pool1,namespace=test,replicas=$REPLICAS,storageClass=powermax"  pool1 --namespace "${NAMESPACE}" "${TEST}"
helm upgrade --set "name=pool2,namespace=test,replicas=$REPLICAS,storageClass=powermax"  pool2 --namespace "${NAMESPACE}" "${TEST}"
helm upgrade --set "name=pool3,namespace=test,replicas=$REPLICAS,storageClass=powermax"  pool3 --namespace "${NAMESPACE}" "${TEST}"

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
	  PVCS=$(kubectl get pvc -n "${NAMESPACE}" | wc -l)
	  date
	  date >>log.output
	  echo running $RUNNING creating $CREATING pvcs $PVCS
	  echo running $RUNNING creating $CREATING pvcs $PVCS >>log.output
	  sleep 30
  done
}

waitOnRunning $TARGET

