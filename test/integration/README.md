# CSI-PowerMax Integration Tests

## How to run the integration tests
1. Update csi-powermax/env.sh with values for your Powermax system.
2. run `export PATH=$PATH:/usr/local/go/bin`
3. run `export KUBECONFIG=/root/.kube/config`
4. run `bash run.sh`

### If you want to only run some test scenarios:
   1. Add a `tag` on the tests
   2. Update the `Tags` section in file _integration_test.go_ with the same tag

### Notes on the replication test scenarios:
- Scenarios `SRDF Actions on a protected SG` and `Get Status of a protected SG` are not currently running due to timing issues in the tests themselves -- they both execute a failover on Unisphere and expect it to be done in less than a second, which is not realisitic.
- Scenarios `Create and delete replication volume with auto SRDF group` and `Create and delete replication volume with HostIOLimits` only pass with manual intervention. When the test gets stuck deleting a volume, the user needs to go to the remote array in Unisphere and manually remove the volume from any storage groups it is in and then delete it.
