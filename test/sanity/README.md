# Kubernetes Sanity Script Test

This test runs the Kubernetes sanity test at https://github.com/kubernetes-csi/csi-test.
The driver was qualified with test version v2.0.1 earlier.
The driver was most recently qualified with v2.3.0 on 11/8/2019

To run the test, follow these steps:

1. "go get github.com/kubernetes-csi/csi-test"
2. Build and install the executable, csi-sanity,  in a directory in your $PATH.
3. Make sure your env.sh is up to date so the CSI driver can be run.
4. Edit the secrets.yaml to have the correct SYMID, ServiceLevel, SRP, and ApplicationPrefix.
5. Use the start_driver.sh to start the driver. USE AN OTHERWISE IDLE ARRAY FOR THE TEST.
6. Wait until the driver has fully come up and completed node setup. If you remain attached the logs will print on the screen.
7. Use the script run.sh to start csi-sanity (best if you do this in a separate window.)

## Excluded Tests

The following tests were excluded for the reasons specified:

1. GetCapacity -- the test does not support supplying the Parameters fields that are required (for things like SYMID).
The test returns an appropriate error: "GetCapacity: Required StoragePool and SymID in parameters"

2. An idempotent volume test that attempts to create a volume with a different size as the existing volume. It appears to have a problem;
the new size is over the maximum capability, and we detect that error first and disqualify the request, which I think is valid.
The error message is: " rpc error: code = OutOfRange desc = bad capacity: size in bytes 10738728960 exceeds limit size bytes 10737418240"

3. A test to create a volume from an existing source snapshot. Same problem as 2.

4. A test to create a volume from a non-exiting snapshot. Same problem as 2.

5. A test to delete a snapshot. Same problem as 2.

6. An idempotent test to create a snapshot with already existing name and same source volume_id. Same problem as 2.

7. A test to create a snapshot with maximum-length name. Same problem as 2.

8. A test to delete a snapshot with an invalid name. The test fails because the csi-powermax driver follows a certain naming convention and the passed invalid snapshot name doesn't comply with that.

9. A test to create a volume from an existing source volume. Same problem as 2.

10. A test to expand volume fails because the returned expanded byte size is approximate size in cylinder bytes. Whereas the test expects the volume size to be same as requested size. 