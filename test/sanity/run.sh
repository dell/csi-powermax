#!/bin/bash

NRUNS=10

run() {
rm -rf /tmp/csi-mount
rm -rf /tmp/csi-staging
export GRPC_GO_LOG_SEVERITY_LEVEL="info"
export GRPC_GO_LOG_VERBOSITY_LEVEL=2
csi-sanity --ginkgo.v \
	--csi.endpoint=$(pwd)/unix_sock \
	--csi.secrets=secrets.yaml \
	--ginkgo.skip "GetCapacity|ListSnapshots|create a volume with already existing name and different capacity" \

}

i=0
while [ $i -lt $NRUNS ];
do
	i=$(expr $i + 1)
	echo "*********************** run $i **************************"
	run
done
