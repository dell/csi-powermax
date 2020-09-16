# CSI Driver for Dell EMC PowerMax
**Repository for CSI Driver for Dell EMC PowerMax development project**

## Description
CSI Driver for Dell EMC PowerMax is a Container Storage Interface (CSI) driver that provides support for provisioning persistent storage using Dell EMC PowerMax storage array. 

It supports CSI specification version 1.1

This project may be compiled as a stand-alone binary using Golang that, when run, provides a valid CSI endpoint. This project can also be built as a Golang plug-in in order to extend the functionality of other programs.

## Support
The CSI Driver for Dell EMC PowerMax image, which is the built driver code, is available on Dockerhub and is officially supported by Dell EMC.  

The source code for CSI Driver for Dell EMC PowerMax available on Github is unsupported and provided solely under the terms of the license attached to the source code. For clarity, Dell EMC does not provide support for any source code modifications.  

For any CSI driver issues, questions or feedback, join the [Dell EMC Container community](https://www.dell.com/community/Containers/bd-p/Containers).

## Building
This project is a Go module (see golang.org Module information for explanation). 
The dependencies for this project are in the go.mod file.

To build the source, execute `make clean build`.

To run unit tests, execute `make unit-test`.

To build a docker image, execute `make docker`.

You can run an integration test on a Linux system by populating the file env.sh with values for your Dell EMC PowerMax systems and then run "`make integration-test`".

## Runtime Dependencies
Both the Controller and the Node portions of the driver can only be run on nodes which have network connectivity to a “`Unisphere for PowerMax`” server (which is used by the driver). 

If you are using ISCSI, then the Node portion of the driver can only be run on nodes that have the iscsi-initiator-utils package installed.

## Installation
Installation in a Kubernetes cluster should be done using the scripts within the `dell-csi-helm-installer` directory. 

For more information, consult the [README.md](dell-csi-helm-installer/README.md)

## Using driver
A number of test helm charts and scripts are found in the directory test/helm. Product Guide provides descriptions of how to run these and explains how they work.

## Parameters
When using the driver, some additional parameters have to be specified in the storage class in Kubernetes. 
These parameters influence how the volume is provisoned on the PowerMax array.

Those parameters are listed here.

*   `SYMID`        : The symmetrix ID of the PowerMax array
*   `SRP`          : The Storage Resource Pool (SRP) name
*   `ServiceLevel` : Service Level on the PowerMax array (Optional). If not specified, the driver will default to `Optimized` Service Level.

## Capable operational modes
The CSI spec defines a set of AccessModes that a volume can have. 
CSI Driver for Dell EMC PowerMax supports the following modes for volumes that will be mounted as a filesystem:
```
// Can only be published once as read/write on a single node,
// at any given time.
SINGLE_NODE_WRITER = 1;

// Can only be published once as readonly on a single node,
// at any given time.
SINGLE_NODE_READER_ONLY = 2;
```
This means that volumes can be mounted to a single node at a time, with read-write or read-only permission

In general, volumes should be formatted with xfs or ext4.

CSI Driver for Dell EMC PowerMax supports the following modes for block volumes:
* ReadWriteOnce
* ReadWriteMany
* ReadOnlyMany (for block Volumes which have been previously initialized)
