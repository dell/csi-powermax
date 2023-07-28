#!/bin/bash
# This will run coverage analysis using the integration testing.
# The env.sh must point to a valid Unisphere deployment and the iscsi packages must be installed
# on this system. This will make real calls to  Unisphere

rm -f unix_sock
. ../../env.sh
export X_CSI_POWERMAX_RESPONSE_TIMES="true"
echo ENDPOINT $X_CSI_POWERMAX_ENDPOINT
echo "Starting the csi-powermax driver. You should wait until the node setup is complete before running tests."
../../csi-powermax
