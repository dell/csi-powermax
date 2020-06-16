#!/bin/bash
echo "installing a 2 volume container"
bash starttest.sh -t 2vols -n test
echo "done installing a 2 volume container"
echo "marking volume"
kubectl exec -n test powermaxtest-0 -- touch /data0/orig
kubectl exec -n test powermaxtest-0 -- ls -l /data0
kubectl exec -n test powermaxtest-0 -- sync
kubectl exec -n test powermaxtest-0 -- sync
echo
echo
echo "Calculating checksum of /data0/orig"
data0checksum=$(kubectl exec powermaxtest-0 -n test -- md5sum /data0/orig)
echo $data0checksum
echo
echo
echo "updating container to add a volume cloned from another volume"
helm upgrade -n test 2vols 2vols+clone
echo "waiting for container to upgrade/stabalize"
sleep 20
up=0
while [ $up -lt 1 ];
do
    sleep 5
    kubectl get pods -n test
    up=`kubectl get pods -n test | grep '1/1 *Running' | wc -l`
done
kubectl describe pods -n test
kubectl exec -n test powermaxtest-0 -it df | grep data
kubectl exec -n test powermaxtest-0 -it mount | grep data
echo "updating container finished"
echo "marking volume"
kubectl exec -n test powermaxtest-0 -- touch /data2/new
echo "listing /data0"
kubectl exec -n test powermaxtest-0 -- ls -l /data0
echo "listing /data2"
kubectl exec -n test powermaxtest-0 -- ls -l /data2
echo
echo
echo "Calculating checksum of the cloned file(/data2/orig)"
data2checksum=$(kubectl exec powermaxtest-0 -n test -- md5sum /data2/orig)
echo $data2checksum
echo
echo
echo "Comparing checksums"
echo $data0checksum
echo $data2checksum
data0chs=$(echo $data0checksum | awk '{print $1}')
data2chs=$(echo $data2checksum | awk '{print $1}')
if [ "$data0chs" = "$data2chs" ]; then
echo "Both the checksums match!!!"
else
echo "Checksums don't match"
fi
echo
echo

sleep 20

echo "deleting container"
echo helm delete -n test 2vols
helm delete -n test 2vols

echo "deleteing the pvcs"
echo bash deletepvcs.sh -sh -n test
bash deletepvcs.sh -n test
sleep 20
kubectl get pvc -n test

echo "removing the lock file"
rm -f "__test-2vols__.yaml"

