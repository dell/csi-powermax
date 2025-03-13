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

# TODO: PowerMax pods restart many times before becoming stable, this function currently cannot detect that a running pod is not stable
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


waitForPowerMaxPods
