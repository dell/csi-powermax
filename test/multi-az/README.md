# Multi-Access Zone Testing Suite

## Description
This folder and all files within are a set of simple tests to automate the process of verifying multi-access zone support for the CSI PowerMax driver. The intention is to create a simple and reusable pattern that can be exercised in all Dell CSI drivers that will support multi-access zone partitioning, while reusing and standardizing tools (such as [cert-csi](https://github.com/dell/cert-csi)) across Dell storage platforms. 

## Running the Test 
Running the tests is intended to be a simple process: fill in a configuration file with real PowerMax storage array values, run the included `run.sh` shell script with CSM Operator installed to manage driver deployments, and receive a passing or failing result. 

1. Populate the `csi-powermax/test/multi-az/config` file with...
2. TODO