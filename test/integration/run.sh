#!/bin/bash
# This will run coverage analysis using the integration testing.
# The env.sh must point to a valid Unisphere deployment and the iscsi packages must be installed
# on this system. This will make real calls to  Unisphere

rm -f unix_sock
. ../../env.sh
rm -rf /dev/disk/csi-powermax/*
rm -rf datadir*
echo ENDPOINT $X_CSI_POWERMAX_ENDPOINT

GOOS=linux CGO_ENABLED=0 GO111MODULE=on go test -v -coverprofile=c.linux.out -timeout 180m -coverpkg=../../service *test.go 
