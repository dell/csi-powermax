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

#!/bin/bash

function waitForPowerMaxPods() {

 echo "Checking PowerMax pods..."

 pmaxPods=$(kubectl get pods -n powermax  -o custom-columns=NAME:.metadata.name --no-headers)
 numOfPods=$(kubectl get pods -n powermax  -o custom-columns=NAME:.metadata.name --no-headers | wc -l)

 # to start, assume one pod is not ready
 notReady=1

 time=$(date +%s)
 secondsToWait=300
 timeToEnd=$((time + $secondsToWait))

 while  ( ! [ $notReady -eq 0 ] || [ $numOfPods -eq 0 ] ) && [ $time -lt $timeToEnd ]; do
  if [ $numOfPods -eq 0 ]; then
   echo "No pods in powermax namespace" 
  fi
  notReady=0
  numOfPods=$(kubectl get pods -n powermax  -o custom-columns=NAME:.metadata.name --no-headers | wc -l)
  pmaxPods=$(kubectl get pods -n powermax  -o custom-columns=NAME:.metadata.name --no-headers)
  for pod in $pmaxPods; do
   state=$(kubectl get pod $pod -n powermax  -o custom-columns=STATUS:.status.phase --no-headers)
   echo Pod: $pod state is $state
   if [ $state != "Running" ]; then
    ((notReady++))
   fi
  done
  time=$(date +%s)
  sleep 1
 done

 if ! [ $time -lt $timeToEnd ]; then
  echo Powermax pods not ready in $secondsToWait seconds
  exit 1
 fi
}

# PowerMax pods restart many times before becoming stable, so this function ensures containers in powermax pods have been running for at least $timeForStableRun 
function ensurePodsAreStable() {
 
 # assume one container is not stable to start while loop
 notStable=1

 # how long (in seconds) a container must be running for us to consider it stable
 timeForStableRun=80


 #timeout values
 time=$(date +%s)
 secondsToWait=600
 timeToEnd=$((time + $secondsToWait))


 echo Ensuring containers have been running for at least $timeForStableRun seconds
 pmaxPods=$(kubectl get pods -n powermax  -o custom-columns=NAME:.metadata.name --no-headers)
 while [ $notStable -ne 0 ] && [ $time -lt $timeToEnd ]; do
  echo sleeping 60 seconds
  sleep 60
  for pod in $pmaxPods; do
   notStable=0
   containers=$(kubectl get pod -n powermax $pod  -o jsonpath='{.status.containerStatuses[*].name}')
   read -a containersArray <<< "$containers"
   array_length=${#containersArray[@]}
   for (( i=0; i<array_length; i++ )); do

     # define the jsonpaths needed to get container name, restart time, restart count, start time
     jsonPathName="{.status.containerStatuses[$i].name}"
     jsonPathRestartTime="{.status.containerStatuses[$i].lastState.terminated.finishedAt}"
     jsonPathRestartCount="{.status.containerStatuses[$i].restartCount}"
     jsonPathStartTime="{.status.containerStatuses[$i].state.running.startedAt}"

     # using jsonPaths above, use kubectl command to get container name, restart time, and restart count
     name=$(kubectl get pod -n powermax $pod  -o jsonpath=$jsonPathName)
     restartTime=$(kubectl get pod -n powermax $pod  -o jsonpath=$jsonPathRestartTime)
     restartCount=$(kubectl get pod -n powermax $pod  -o jsonpath=$jsonPathRestartCount)

     # convert restart time to seconds
     timeInSeconds=$(date -d "$restartTime" +%s)
     time=$(date +%s)

     # if the container hasn't been restarted, this container has been running for current time - start time
     if [ $restartCount -eq 0 ]; then
      startedTime=$(kubectl get pod -n powermax $pod  -o jsonpath=$jsonPathStartTime)
      startedTimeInSeconds=$(date -d "$startedTime" +%s)
      timeDiff=$[ $time - $startedTimeInSeconds ]
     else
      # if container has been restarted, this container has been running for current time - last restart time
      timeDiff=$[ $time - $timeInSeconds ]
     fi

     echo container: $name in pod: $pod has been running for $timeDiff seconds

     # if container has been running for at least $timeForStableRun, consider it stable, if not, we assume unstable
     if [ $timeDiff -lt $timeForStableRun ]; then
      ((notStable++))
     fi
   done
  done
 done

 if ! [ $time -lt $timeToEnd ]; then
  echo Powermax pods not stable in $secondsToWait seconds
  exit 1
 fi

}

 
waitForPowerMaxPods
ensurePodsAreStable

kubectl get pods -n powermax

