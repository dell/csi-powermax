#!/bin/sh
rm -rf /tmp/csi-mount
csi-sanity --ginkgo.v \
	--csi.endpoint=/home/tom/csi-powermax/test/sanity/unix_sock \
	--csi.secrets=secrets.yaml \
	--ginkgo.skip "GetCapacity|create a volume with already existing name and different capacity" \
