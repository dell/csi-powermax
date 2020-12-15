#!/bin/bash

get_size() {
   echo $(kubectl exec -n test powermaxtest-0 -- df -h /data0 | awk 'NR==2 {print $2}')
}

echo "installing a 1 volume container"
bash starttest.sh -t 1vol -n test
echo "done installing a 1 volume container"
echo "marking volume"
echo "creating a file on the volume"
kubectl exec -n test powermaxtest-0 -- touch /data0/orig
kubectl exec -n test powermaxtest-0 -- ls -l /data0
kubectl exec -n test powermaxtest-0 -- sync
kubectl exec -n test powermaxtest-0 -- sync
echo
echo "calculating the initial size of the volume"
initialSize=$(get_size)
echo "INITIAL SIZE: " $initialSize
echo
echo
echo "calculating checksum of /data0/orig"
data0checksum=$(kubectl exec powermaxtest-0 -n test -- md5sum /data0/orig)
echo $data0checksum
echo
echo
echo "expanding the volume"
cat 1vol/templates/pvc0.yaml  | sed 's/{{ .Values.namespace }}/test/g;s/storage: [0-9]\+Gi/storage: 15Gi/' | kubectl apply -f -
size=$(get_size)
echo -ne "Processing: "
while [ "$initialSize" = "$size" ]; do
  sleep 3
  size=$(get_size)
  echo -ne "#"
done
echo
echo
echo
sleep 5
echo "volume expanded to $(get_size)"
echo "calculating checksum again"
newdata0checksum=$(kubectl exec powermaxtest-0 -n test -- md5sum /data0/orig)
echo $newdata0checksum
echo
echo
echo
echo "Comparing checksums"
echo $data0checksum
echo $newdata0checksum
data0chs=$(echo $data0checksum | awk '{print $1}')
newdata0chs=$(echo $newdata0checksum | awk '{print $1}')
if [ "$data0chs" = "$newdata0chs" ]; then
echo "Both the checksums match!!!"
else
echo "Checksums don't match"
fi
echo
echo

sleep 5
echo "cleaning up..."
bash stoptest.sh -t 1vol -n test

