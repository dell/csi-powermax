#!/bin/bash
#
# Copyright © 2021-2025 Dell Inc. or its subsidiaries. All Rights Reserved.
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

SCRIPTDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" >/dev/null 2>&1 && pwd)"
DRIVERDIR="${SCRIPTDIR}/../"

PROG="${0}"
NODE_VERIFY=1
VERIFY=1
MODE="install"
DEFAULT_DRIVER_VERSION="v2.14.0"
WATCHLIST=""

#
# usage will print command execution help and then exit
function usage() {
  echo
  echo "Help for $PROG"
  echo
  echo "Usage: $PROG options..."
  echo "Options:"
  echo "  Required"
  echo "  --namespace[=]<namespace>                Kubernetes namespace containing the CSI driver"
  echo "  --values[=]<values.yaml>                 Values file, which defines configuration values"

  echo "  Optional"
  echo "  --release[=]<helm release>               Name to register with helm, default value will match the driver name"
  echo "  --upgrade                                Perform an upgrade of the specified driver, default is false"
  echo "  --version                                Use this version for CSI Driver Image"
  echo "  --helm-charts-version                    Pass the helm chart version "
  echo "  --node-verify-user[=]<username>          Username to SSH to worker nodes as, used to validate node requirements. Default is root"
  echo "  --skip-verify                            Skip the kubernetes configuration verification to use the CSI driver, default will run verification"
  echo "  --skip-verify-node                       Skip worker node verification checks"
  echo "  -h                                       Help"
  echo

  exit 0
}

DRIVERVERSION="csi-powermax-2.14.0"

while getopts ":h-:" optchar; do
  case "${optchar}" in
  -)
    case "${OPTARG}" in
    skip-verify)
      VERIFY=0
      ;;
    skip-verify-node)
      NODE_VERIFY=0
      ;;
    upgrade)
      MODE="upgrade"
      ;;
      # NAMESPACE
    version)
      DRIVER_VERSION="${!OPTIND}"
      OPTIND=$((OPTIND + 1))
      ;;
      # DRIVER IMAGE VERSION
    namespace)
      NS="${!OPTIND}"
      if [[ -z ${NS} || ${NS} == "--skip-verify" ]]; then
        NS=${DEFAULT_NS}
      else
        OPTIND=$((OPTIND + 1))
      fi
      ;;
    namespace=*)
      NS=${OPTARG#*=}
      if [[ -z ${NS} ]]; then NS=${DEFAULT_NS}; fi
      ;;
      # RELEASE
    release)
      RELEASE="${!OPTIND}"
      OPTIND=$((OPTIND + 1))
      ;;
    release=*)
      RELEASE=${OPTARG#*=}
      ;;
    # helm chart version
    helm-charts-version)
      HELMCHARTVERSION="${!OPTIND}"
      OPTIND=$((OPTIND + 1))
      ;;
    # VALUES
    values)
      VALUES="${!OPTIND}"

      OPTIND=$((OPTIND + 1))
      ;;
    values=*)
      VALUES=${OPTARG#*=}
      ;;
      # NODEUSER
    node-verify-user)
      NODEUSER="${!OPTIND}"
      OPTIND=$((OPTIND + 1))
      ;;
    node-verify-user=*)
      HODEUSER=${OPTARG#*=}
      ;;
    *)
      echo "Unknown option --${OPTARG}"
      echo "For help, run $PROG -h"
      exit 1
      ;;
    esac
    ;;
  h)
    usage
    ;;
  *)
    echo "Unknown option -${OPTARG}"
    echo "For help, run $PROG -h"
    exit 1
    ;;
  esac
done

if [ -n "$HELMCHARTVERSION" ]; then
  DRIVERVERSION=$HELMCHARTVERSION
fi

if [ ! -d "$DRIVERDIR/helm-charts" ]; then

  if  [ ! -d "$SCRIPTDIR/helm-charts" ]; then
    git clone --quiet -c advice.detachedHead=false  -b $DRIVERVERSION https://github.com/dell/helm-charts
  fi
  mv helm-charts $DRIVERDIR
else 
  if [  -d "$SCRIPTDIR/helm-charts" ]; then
    rm -rf $SCRIPTDIR/helm-charts
  fi
fi
DRIVERDIR="${SCRIPTDIR}/../helm-charts/charts"
DRIVER="csi-powermax"
VERIFYSCRIPT="${SCRIPTDIR}/verify.sh"

# export the name of the debug log, so child processes will see it
export DEBUGLOG="${SCRIPTDIR}/install-debug.log"
declare -a VALIDDRIVERS

source "$SCRIPTDIR"/common.sh

if [ -f "${DEBUGLOG}" ]; then
  rm -f "${DEBUGLOG}"
fi
# warning, with an option for users to continue
function warning() {
  log separator
  printf "${YELLOW}WARNING:${NC}\n"
  for N in "$@"; do
    echo $N
  done
  echo
  if [ "${ASSUMEYES}" == "true" ]; then
    echo "Continuing as '-Y' argument was supplied"
    return
  fi
  read -n 1 -p "Press 'y' to continue or any other key to exit: " CONT
  echo
  if [ "${CONT}" != "Y" -a "${CONT}" != "y" ]; then
    echo "quitting at user request"
    exit 2
  fi
}


# print header information
function header() {
  log section "Installing CSI Driver: ${DRIVER} on ${kMajorVersion}.${kMinorVersion}"
}

#
# check_for_driver will see if the driver is already installed within the namespace provided
function check_for_driver() {
  log section "Checking to see if CSI Driver is already installed"
  NUM=$(run_command helm list --namespace "${NS}" | grep "^${RELEASE}\b" | wc -l)
  if [ "${1}" == "install" -a "${NUM}" != "0" ]; then
    # grab the status of the existing chart release
    debuglog_helm_status "${NS}"  "${RELEASE}"
    log error "The CSI Driver is already installed"
  fi
  if [ "${1}" == "upgrade" -a "${NUM}" == "0" ]; then
    log error "The CSI Driver is not installed"
  fi
}

#
# validate_params will validate the parameters passed in
function validate_params() {
  # make sure the driver was specified
  if [ -z "${DRIVER}" ]; then
    echo "No driver specified"
    usage
    exit 1
  fi
  # the namespace is required
  if [ -z "${NS}" ]; then
    echo "No namespace specified"
    usage
    exit 1
  fi
  # values file
  if [ -z "${VALUES}" ]; then
    echo "No values file was specified"
    usage
    exit 1
  fi
  if [ ! -f "${VALUES}" ]; then
    echo "Unable to read values file at: ${VALUES}"
    usage
    exit 1
  fi
}

#
# install_driver uses helm to install the driver with a given name
function install_driver() {
  if [ "${1}" == "upgrade" ]; then
    log step "Upgrading Driver"
  else
    log step "Installing Driver"
  fi

  # run driver specific install script
  local SCRIPTNAME="install-${DRIVER}.sh"
  if [ -f "${SCRIPTDIR}/${SCRIPTNAME}" ]; then
    source "${SCRIPTDIR}/${SCRIPTNAME}"
  fi

  HELMOUTPUT="/tmp/csi-install.$$.out"
  run_command helm ${1} \
    --set openshift=${OPENSHIFT} \
    --values "${VALUES}" \
    --namespace ${NS} "${RELEASE}" \
    "${DRIVERDIR}/${DRIVER}" >"${HELMOUTPUT}" 2>&1

  if [ $? -ne 0 ]; then
    cat "${HELMOUTPUT}"
    log error "Helm operation failed, output can be found in ${HELMOUTPUT}. The failure should be examined, before proceeding. Additionally, running csi-uninstall.sh may be needed to clean up partial deployments."
  fi
  log step_success
  getWhatToWatch "${NS}" "${RELEASE}"
  # wait for the deployment to finish, use the default timeout
  waitOnRunning "${NS}" "${WATCHLIST}"
  if [ $? -eq 1 ]; then
    warning "Timed out waiting for the operation to complete." \
      "This does not indicate a fatal error, pods may take a while to start." \
      "Progress can be checked by running \"kubectl get pods -n ${NS}\""
    debuglog_helm_status "${NS}" "${RELEASE}"
  fi
}

# Print a nice summary at the end
function summary() {
  log section "Operation complete"
}

# getWhatToWatch
# will retrieve the list of statefulsets, deployments, and daemonsets running in a target namespace
# and sets a global variable formatted such that it can be passed to waitOnRunning to monitor the rollout
#
# This expects resources to be named with a prefix of the helm release name
#
# expects two argumnts:
# $1: required: namespace
# $2: required: helm release name
function getWhatToWatch() {
  if [ -z "${2}" ]; then
    echo "No namespace and/or helm release name were supplied These fields are required for getWhatToWatch"
    exit 1
  fi

  local NS="${1}"
  local RN="${2}"

  for T in StatefulSet Deployment DaemonSet; do
    ALL=$(run_command kubectl -n "${NS}" get "${T}" -o jsonpath="{.items[*].metadata.name}")
    for ENTITY in $ALL; do
        if [[ "${ENTITY}" == ${RN}-* ]]; then
            if [ "${ENTITY}" != "" ]; then
                if [ "${WATCHLIST}" != "" ]; then
                    WATCHLIST="${WATCHLIST},"
                fi
                WATCHLIST="${WATCHLIST}${T} ${ENTITY}"
            fi
        fi
    done
  done

}

# waitOnRunning
# will wait, for a timeout period, for a number of pods to go into Running state within a namespace
# arguments:
#  $1: required: namespace to watch
#  $2: required: comma seperated list of deployment type and name pairs
#      for example: "statefulset mystatefulset,daemonset mydaemonset"
#  $3: optional: timeout value, 300 seconds is the default.
function waitOnRunning() {
  if [ -z "${2}" ]; then
    echo "No namespace and/or list of deployments was supplied. This field is required for waitOnRunning"
    return 1
  fi
  # namespace
  local NS="${1}"
  # pods
  IFS="," read -r -a PODS <<<"${2}"
  # timeout value passed in, or 300 seconds as a default
  local TIMEOUT="300"
  if [ -n "${3}" ]; then
    TIMEOUT="${3}"
  fi

  error=0
  for D in "${PODS[@]}"; do
    log arrow
    log smart_step "Waiting for $D to be ready" "small"
    run_command kubectl -n "${NS}" rollout status --timeout=${TIMEOUT}s ${D} >/dev/null 2>&1
    if [ $? -ne 0 ]; then
      error=1
      log step_failure
    else
      log step_success
    fi
  done

  if [ $error -ne 0 ]; then
    return 1
  fi
  return 0
}

function kubectl_safe() {
  eval "kubectl $1"
  exitcode=$?
  if [[ $exitcode != 0 ]]; then
    echo "$2"
    echo "Command was: kubectl $1"
    echo "Output was:"
    eval "kubectl $1"
    exit $exitcode
  fi
}

# verify_unisphere
# will verify if the unisphere to be used is at required REST version
function verify_unisphere() {
  if [ $VERIFY -eq 0 ]; then
    echo "Skipping unisphere verification at user request"
  else
    warning "CSI Driver for Powermax v2.5 and above requires 10.0 Unisphere REST endpoint support"
  fi
}

# verify_kubernetes
# will run a driver specific function to verify environmental requirements
function verify_kubernetes() {
  EXTRA_OPTS=""
  if [ $VERIFY -eq 0 ]; then
    echo "Skipping verification at user request"
  else
    if [ $NODE_VERIFY -eq 0 ]; then
      EXTRA_OPTS="$EXTRA_OPTS --skip-verify-node"
    fi
    "${VERIFYSCRIPT}" --version "${VERSION}" --driver-version "${DRIVER_VERSION}" --namespace "${NS}" --release "${RELEASE}" --values "${VALUES}" --node-verify-user "${NODEUSER}" ${EXTRA_OPTS}
    VERIFYRC=$?
    case $VERIFYRC in
    0) ;;

    1)
      warning "Kubernetes validation failed but installation can continue. " \
        "This may affect driver installation."
      ;;
    *)
      log error "Kubernetes validation failed."
      ;;
    esac
  fi
}

#
# main
#
VERIFYOPTS=""
ASSUMEYES="false"

# get the driver directory  that contain helm charts
DRIVERDIR="${SCRIPTDIR}/../helm-charts/charts"

# by default the NAME of the helm release of the driver is the same as the driver name
RELEASE=$(get_release_name "${DRIVER}")
# by default, NODEUSER is root
NODEUSER="${NODEUSER:-root}"
if [[ -z ${DRIVER_VERSION} ]]; then
   DRIVER_VERSION=${DEFAULT_DRIVER_VERSION}
fi


# make sure kubectl is available
kubectl --help >&/dev/null || {
  echo "kubectl required for installation... exiting"
  exit 2
}
# make sure helm is available
helm --help >&/dev/null || {
  echo "helm required for installation... exiting"
  exit 2
}

OPENSHIFT=$(isOpenShift)

# Get the kubernetes major and minor version numbers.
kMajorVersion=$(run_command kubectl version | grep 'Server Version' | sed -E 's/.*v([0-9]+)\.[0-9]+\.[0-9]+.*/\1/')
kMinorVersion=$(run_command kubectl version | grep 'Server Version' | sed -E 's/.*v[0-9]+\.([0-9]+)\.[0-9]+.*/\1/')
kNonGAVersion=$(run_command kubectl version | grep 'Server Version' | sed -n 's/.*\(-[alpha|beta][^ ]*\).*/\1/p')

# validate the parameters passed in
validate_params "${MODE}"

header
check_for_driver "${MODE}"
verify_unisphere
verify_kubernetes

# all good, keep processing
install_driver "${MODE}"
summary
