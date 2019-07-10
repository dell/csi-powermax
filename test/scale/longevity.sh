#!/bin/sh


# longevity.sh
# This script will kick off a test designed to run forever, validiting the longevity of the driver.
#
# The test will continue to run until a file named 'stop' is placed in the script directory.


TEST="10volumes"
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

# remove old output and stop file
rm -f stop
rm -f log.output


# fill an array of controller and worker node names
declare -a NODES_CONT
declare -a NODES_WORK

while read -r line; do
  P=$(echo $line | awk '{ print $1 }')
  NODES_CONT+=("$P")
done < <(kubectl get pods -n powermax | grep controller)
while read -r line; do
  P=$(echo $line | awk '{ print $1 }')
  NODES_WORK+=("$P")
done < <(kubectl get pods -n powermax | grep node)

deployPods() {
	echo "Deploying pods, replicase: $REPLICAS, target $TARGET"
	helm install --set "name=pool1,namespace="${NAMESPACE}",replicas=$REPLICAS,storageClass=powermax"  -n pool1 --namespace "${NAMESPACE}"  "${TEST}"
	helm install --set "name=pool2,namespace="${NAMESPACE}",replicas=$REPLICAS,storageClass=powermax"  -n pool2 --namespace "${NAMESPACE}"  "${TEST}"
	helm install --set "name=pool3,namespace="${NAMESPACE}",replicas=$REPLICAS,storageClass=powermax"  -n pool3 --namespace "${NAMESPACE}"  "${TEST}"
}

rescalePods() {
	echo "Rescaling pods, replicase: $REPLICAS, target $TARGET"
	helm upgrade --set "name=pool1,namespace="${NAMESPACE}",replicas=$1,storageClass=powermax"  pool1 --namespace "${NAMESPACE}"  "${TEST}"
	helm upgrade --set "name=pool2,namespace="${NAMESPACE}",replicas=$1,storageClass=powermax"  pool2 --namespace "${NAMESPACE}"  "${TEST}"
	helm upgrade --set "name=pool3,namespace="${NAMESPACE}",replicas=$1,storageClass=powermax"  pool3 --namespace "${NAMESPACE}"  "${TEST}"
}

helmDelete() {
	echo "Deleting helm charts"
	helm delete --purge pool1
	helm delete --purge pool2
	helm delete --purge pool3
}

printVmsize() {
	for N in "${NODES_CONT[@]}"; do
  	kubectl exec $N -n powermax --container driver -- ps -eo cmd,vsz,rss | grep csi-powermax
		echo -n "$N " >>log.output
		kubectl exec $N -n powermax --container driver -- ps -eo cmd,vsz,rss | grep csi-powermax >>log.output
	done
	for N in "${NODES_WORK[@]}"; do
  	kubectl exec $N -n powermax --container driver -- ps -eo cmd,vsz,rss | grep csi-powermax
		echo -n "$N " >>log.output
		kubectl exec $N -n powermax --container driver -- ps -eo cmd,vsz,rss | grep csi-powermax >>log.output
	done
}

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
	  PVCS=$(kubectl get pvc -n "${NAMESPACE}" | wc -l)
	  date
	  date >>log.output
		echo running $RUNNING creating $CREATING terminating $TERMINATING pvcs $PVCS
		echo running $RUNNING creating $CREATING terminating $TERMINATING pvcs $PVCS >>log.output
		printVmsize
	  sleep 30
  done
}

waitOnNoPods() {
	COUNT=$(kubectl get pods -n "${NAMESPACE}" | wc -l)
	while [ $COUNT -gt 0 ];
	do
		echo "Waiting on all $COUNT pods to be deleted"
		echo "Waiting on all $COUNT pods to be deleted" >>log.output
		sleep 30
		COUNT=$(kubectl get pods -n "${NAMESPACE}" | wc -l)
		echo pods $COUNT
	done
}

waitOnNoVolumeAttachments() {
	COUNT=$(kubectl get volumeattachments -n "${NAMESPACE}" | grep -v NAME | wc -l)
	while [ $COUNT -gt 0 ];
	do
		echo "Waiting on all volume attachments to be deleted: $COUNT"
		echo "Waiting on all volume attachments to be deleted: $COUNT" >>log.output
		sleep 30
		COUNT=$(kubectl get volumeattachments -n "${NAMESPACE}" | grep -v NAME | wc -l)
	done
}

deletePvcs() {
	FORCE=""
	PVCS=$(kubectl get pvc -n "${NAMESPACE}" | awk '/pvol/ { print $1; }')
	echo deleting... $PVCS
	for P in $PVCS; do
		if [ "$FORCE" == "yes" ]; then
			echo kubectl delete --force --grace-period=0 pvc $P -n "${NAMESPACE}"
			kubectl delete --force --grace-period=0 pvc $P -n "${NAMESPACE}"
		else
			echo kubectl delete pvc $P -n "${NAMESPACE}"
			echo kubectl delete pvc $P -n "${NAMESPACE}" >>log.output
			kubectl delete pvc $P -n "${NAMESPACE}"
		fi
	done
}

validateServiceAccount() {
  # validate that the service account exists
  ACCOUNTS=$(kubectl describe serviceaccount -n "${NAMESPACE}" "powermaxtest")
  if [ $? -ne 0 ]; then
    echo "Creating Service Account"
    kubectl create -n ${NAMESPACE} -f serviceAccount.yaml
  fi
}

validateServiceAccount

#
# Longevity test loop. Runs until a "stop" file is found.
#
ITER=1
while true;
do
	TS=$(date)
	#replicas=$REPLICAS
	#target=$Target
	echo "Longevity test iteration $ITER replicas $REPLICAS target $TARGET $TS"
	echo "Longevity test iteration $ITER replicas $REPLICAS target $TARGET $TS" >>log.output

	echo "deploying pods" >>log.output
	deployPods
	echo "waiting on running $target" >>log.output
	waitOnRunning $TARGET
	echo "rescaling pods 0" >>log.output
	rescalePods 0
	echo "waiting on running 0" >>log.output
	waitOnRunning 0
	echo "waiting on no pods" >>log.output
	waitOnNoPods

	waitOnNoVolumeAttachments
	deletePvcs
	helmDelete

	echo "Longevity test iteration $ITER completed $TS"
	echo "Longevity test iteration $ITER completed $TS" >>log.output
	
	if [ -f stop ]; 
	then
		echo "stop detected... exiting"
		exit 0
	fi
	ITER=$(expr $ITER \+ 1)
done
exit 0
