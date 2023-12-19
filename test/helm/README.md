# Helm tests
This folder contains Helm charts and shell scripts which can be used to test various features of the CSI PowerMax driver.

The Helm charts typically deploy a StatefulSet with a number of PVCs created using the provided storage class names.  
For e.g. - the test `2vols` will deploy a StatefulSet which runs a single CentOS container and uses `2` PVCs.

Additionally, some tests create cloned volumes using a source `Volume` or a `VolumeSnapshot`

## Helm charts

| Name    | Description |
|---------|-------|
|2vols    | Creates 2 filesystem mounts |
|7vols	  | Creates 7 filesystem mounts |
|10vols	  | Creates 10 filesystem mounts |
|xfspre   | Create an XFS formated PV and attaches to a pod |
|2block | Create 2 raw block volumes and attach to a pod |
| 2vols + clone | 


## Scripts
| Name           | Description |
|----------------|-------|
| deletepvcs.sh  | Script to delete all PVCS in a namespace
| get.volume.ids | Script to list the volume IDs for all PVS in a namespace
| logit.sh       | Script to print number of pods and pvcs in a namespace
| postgres.sh    | Script used to startup a postgres pod that is backed by a PV
| starttest.sh   | Used to instantiate one of the Helm charts above. Requires argument of Helm chart
| stoptest.sh    | Stops currently running Helm chart and deletes all PVCS
| volumeclonetest.sh | Tests volume clones
| snaprestoretest.sh | Tests restore from a VolumeSnapshot (using clones) 
| volumeexpansiontest.sh | Tests volume expansion


## Usage
All the test scripts require these 2 optional arguments
  * -n \<namespace> => Namespace where the sample applications will be deployed
  * -s \<sc-name> => Name of the storage class which will be used for provisioning PVCs.
  
Some tests also require another storage class which provision XFS  mount volumes. If you provided `sc-name` as the storage class name, ensure that a storage class with the name `sc-name-xfs` exists before running the tests.

If you don't provide the namespace option, the test scripts will default to using `test` namespace.

If you don't provide the storage class name, the test scripts will default to `powermax` & `powermax-xfs` storage class names.


### starttest.sh
The starttest.sh script is used to deploy Helm charts that test the deployment of a simple pod
with various storage configurations. The stoptest.sh script will delete the Helm chart and cleanup after the test.
Procedure
1. Navigate to the test/helm directory, which contains the starttest.sh and various Helm charts.

2. Run the starttest.sh script with an argument of the specific Helm chart to deploy and test. For example:
> bash starttest.sh -t <testname> -n <namespance> -s <sc-name>
  Example  -> bash starttest.sh -t 2vols -n test -s powermax	
3. After the test has completed, run the stoptest.sh script to delete the Helm chart and cleanup the volumes.
> bash stoptest.sh -t <testname> -n <namespace>
 Example -> bash stoptest.sh -t 2vols -n test 

### Other scripts
The following set of scripts have been provided to help test additonal CSI functionalities like snapshots, clones, volume expansion.

* snaprestoretest.sh
* volumeclonetest.sh
* volumeexpansiontest.sh
* volumeexpansiontest.sh

To run these tests, follow the procedure given below:
1. Navigate to the test/helm directory
2. Run the desired script with the following command
    ```
   bash <script-name> -n <namespace> -s <sc-name>
    ```
