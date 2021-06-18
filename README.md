# CSI Driver for Dell EMC PowerMax
[![Go Report Card](https://goreportcard.com/badge/github.com/dell/csi-powermax?style=flat-square)](https://goreportcard.com/report/github.com/dell/csi-powermax)
[![License](https://img.shields.io/github/license/dell/csi-powermax?style=flat-square&color=blue&label=License)](https://github.com/dell/csi-powermax/blob/master/LICENSE)
[![Docker](https://img.shields.io/docker/pulls/dellemc/csi-powermax.svg?logo=docker&style=flat-square&label=Pulls)](https://hub.docker.com/r/dellemc/csi-powermax)
[![Last Release](https://img.shields.io/github/v/release/dell/csi-powermax?label=Latest&style=flat-square&logo=go)](https://github.com/dell/csi-powermax/releases)

**Repository for CSI Driver for Dell EMC PowerMax**

## Description
CSI Driver for Dell EMC PowerMax is a Container Storage Interface [(CSI)](https://github.com/container-storage-interface/spec) driver that provides support for provisioning persistent storage using Dell EMC PowerMax storage array. 

It supports CSI specification version 1.3.

This project may be compiled as a stand-alone binary using Golang that, when run, provides a valid CSI endpoint. It also can be used as a precompiled container image.

## Support
The CSI Driver for Dell EMC PowerMax image, which is the built driver code, is available on Dockerhub and is officially supported by Dell EMC.  

The source code for CSI Driver for Dell EMC PowerMax available on Github is unsupported and provided solely under the terms of the license attached to the source code. 

For clarity, Dell EMC does not provide support for any source code modifications.  

For any CSI driver issues, questions or feedback, join the [Dell EMC Container community](https://www.dell.com/community/Containers/bd-p/Containers).

## Building
This project is a Go module (see golang.org Module information for explanation). 
The dependencies for this project are in the go.mod file.

To build the source, execute `make clean build`.

To run unit tests, execute `make unit-test`.

To build an image, execute `make docker`.

You can run an integration test on a Linux system by populating the file `env.sh` with values for your Dell EMC PowerMax systems and then run "`make integration-test`".

## Runtime Dependencies
Both the Controller and the Node portions of the driver can only be run on nodes which have network connectivity to a “`Unisphere for PowerMax`” server (which is used by the driver). 

If you are using ISCSI, then the Node portion of the driver can only be run on nodes that have the iscsi-initiator-utils package installed.

## Driver Installation
Please consult the [Installation Guide](https://dell.github.io/storage-plugin-docs/docs/installation/)

As referenced in the guide, installation in a Kubernetes cluster should be done using the scripts within the `dell-csi-helm-installer` directory. For more detailed information on the scripts, consult the [README.md](dell-csi-helm-installer/README.md)

## Using driver
A number of test helm charts and scripts are found in the directory test/helm. Please refer to the section `Testing Drivers` in the [Documentation](https://dell.github.io/storage-plugin-docs/docs/installation/test/) for more info.

## Documentation
For more detailed information on the driver, please refer to [Dell Storage Documentation](https://dell.github.io/storage-plugin-docs/docs/) 

For a detailed set of information on supported platforms and driver capabilities, please refer to the [Features and Capabilities Documentation](https://dell.github.io/storage-plugin-docs/docs/dell-csi-driver/) 
