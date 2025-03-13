# Multi-Access Zone Testing Suite

## Description
This folder and all files within are a set of simple tests to automate the process of verifying multi-access zone support for the CSI PowerMax driver. The intention is to create a simple and reusable pattern that can be exercised in all Dell CSI drivers that will support multi-access zone partitioning, while reusing and standardizing tools across Dell storage platforms.

## Prerequisites
All of the following will be necessary to perform the test.
- A Kubernetes cluster
    - This cluster must have two worker nodes to validate node zoning
    - This cluster must have the latest version of CSM Operator deployed for driver installations
    - This cluster must have a valid kubeconfig file in place on the machine running the test to access it via `kubectl`
- `openssl` installed on the build machine for TLS secret generation (PowerMax requirement)
- Two PowerMax storage arrays (one for each zone to verify zoning functionality)

## Running the Test 
Running the tests is intended to be a simple process: fill in a configuration file with real PowerMax storage array values, run the included `run.sh` shell script with CSM Operator installed to manage driver deployments, and receive a passing or failing result. 

1. Populate the `csi-powermax/test/multi-az/config` file with values that satisfy both PowerMax arrays under test.
2. Run the shell script using `./run.sh` from within the `csi-powermax/test/multi-az` directory.
3. That's it! The `run.sh` script will produce a log of the test run.

## Design Methodology
This script-based testing method was born from a need to repeatably test and verify multi-access zone support for PowerMax. Leveraging the existing end-to-end work in [csm-operator](https://github.com/dell/csm-operator/blob/main/tests/e2e/modify_zoning_labels.sh), as well as a similar structure of templates that are filled with user-provided values for the user.

It is the hope that this script setup can be extended to other storage platforms as multi-access zone support is added to them with minimal effort between platforms.