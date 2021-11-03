#!/bin/bash
# This will run coverage analysis using the integration testing.
# The env.sh must point to a valid Unisphere deployment and the iscsi packages must be installed
# on this system. This will make real calls to  Unisphere

mkdir -p /var/run/csi
rm -f /var/run/csi/csi.sock
. env.sh
echo ENDPOINT $X_CSI_POWERMAX_ENDPOINT
export CSI_ENDPOINT=/var/run/csi/csi.sock
export X_CSI_MODE="controller"
go generate
CGO_ENABLED=0 GOOS=linux GO111MODULE=on go build
./csi-powermax

