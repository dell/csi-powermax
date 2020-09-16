#!/bin/bash
# Make sure to run the 2vols test in the namespace test before running this test
VALUES="__test-2vols__.yaml"
if [ -f "${VALUES}" ]; then
  echo "2vols test is running"
else
  echo "Error: Make sure to run the 2vols test before running this test"
  exit 1
fi
echo "******************Begin Test*********************"
echo "creating snapshot of pvol1 (xfs volume)"
echo "************************************************"
kubectl create -f betaSnap2.yaml
echo "Sleeping for 25 seconds to allow creation of snapshot"
sleep 25
kubectl get volumesnapshot -n test
echo "************************************************"
echo "Creating a new pod with a volume which is sourced from the snapshot"
echo "************************************************"
bash starttest.sh -t 1clonevol -n test
echo "Successfully created the pod"
echo "*************Test Successful********************"
echo "Deleting the pod with the cloned volume"
echo "************************************************"
echo 'helm delete -n test 1clonevol'
helm delete -n test 1clonevol
echo "************************************************"
echo "Deleting the PVC"
echo "************************************************"
echo "kubectl delete pvc restorepvc -n test"
kubectl delete pvc restorepvc -n test
echo "************************************************"
echo "Deleting the snapshot"
echo "************************************************"
kubectl delete -f betaSnap2.yaml
echo "************************************************"
echo "Deleting the original pod"
echo "************************************************"
bash stoptest.sh -t 2vols -n test
kubectl get pvc -n test
rm -f __test-1clonevol__.yaml
echo "*************End of test************************"

