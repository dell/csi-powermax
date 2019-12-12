#!/bin/bash

NRUNS=10

run() {
rm -rf /tmp/csi-mount
rm -rf /tmp/csi-staging
export GRPC_GO_LOG_SEVERITY_LEVEL="info"
export GRPC_GO_LOG_VERBOSITY_LEVEL=2
csi-sanity --ginkgo.v \
	--csi.endpoint=/home/tom/csi-powermax/test/sanity/unix_sock \
	--csi.secrets=secrets.yaml \
	--test.failfast \
	--ginkgo.skip "GetCapacity|CreateSnapshot|ListSnapshots|DeleteSnapshot|ExpandVolume|create a volume with already existing name and different capacity" \

        #--ginkgo.focus "Node Service" \
}

i=0
while [ $i -lt $NRUNS ];
do
	i=$(expr $i + 1)
	echo "*********************** run $i **************************"
	run
done
