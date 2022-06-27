# CSI Driver for Dell PowerMax
[![Go Report Card](https://goreportcard.com/badge/github.com/dell/csi-powermax?style=flat-square)](https://goreportcard.com/report/github.com/dell/csi-powermax)
[![License](https://img.shields.io/github/license/dell/csi-powermax?style=flat-square&color=blue&label=License)](https://github.com/dell/csi-powermax/blob/main/LICENSE)
[![Docker](https://img.shields.io/docker/pulls/dellemc/csi-powermax.svg?logo=docker&style=flat-square&label=Pulls)](https://hub.docker.com/r/dellemc/csi-powermax)
[![Last Release](https://img.shields.io/github/v/release/dell/csi-powermax?label=Latest&style=flat-square&logo=go)](https://github.com/dell/csi-powermax/releases)

**Repository for CSI Driver for Dell PowerMax**

## Description
CSI Driver for PowerMax is part of the [CSM (Container Storage Modules)](https://github.com/dell/csm) open-source suite of Kubernetes storage enablers for Dell products. CSI Driver for PowerMax is a Container Storage Interface (CSI) driver that provides support for provisioning persistent storage using Dell PowerMax storage array. 

It supports CSI specification version 1.5.

This project may be compiled as a stand-alone binary using Golang that, when run, provides a valid CSI endpoint. It also can be used as a precompiled container image.

## Table of Contents

* [Code of Conduct](https://github.com/dell/csm/blob/main/docs/CODE_OF_CONDUCT.md)
* [Maintainer Guide](https://github.com/dell/csm/blob/main/docs/MAINTAINER_GUIDE.md)
* [Committer Guide](https://github.com/dell/csm/blob/main/docs/COMMITTER_GUIDE.md)
* [Contributing Guide](https://github.com/dell/csm/blob/main/docs/CONTRIBUTING.md)
* [Branching Strategy](https://github.com/dell/csm/blob/main/docs/BRANCHING.md)
* [List of Adopters](https://github.com/dell/csm/blob/main/docs/ADOPTERS.md)
* [Maintainers](https://github.com/dell/csm/blob/main/docs/MAINTAINERS.md)
* [Support](#support)
* [Security](https://github.com/dell/csm/blob/main/docs/SECURITY.md)
* [Building](#building)
* [Runtime Dependecies](#runtime-dependencies)
* [Driver Installation](#driver-installation)
* [Using Driver](#using-driver)
* [Documentation](#documentation)

## Support
For any CSI driver issues, questions or feedback, please follow our [support process](https://github.com/dell/csm/blob/main/docs/SUPPORT.md)

## Building
This project is a Go module (see golang.org Module information for explanation). 
The dependencies for this project are in the go.mod file.

To build the source, execute `make clean build`.

To run unit tests, execute `make unit-test`.

To build an image, execute `make docker`.

You can run an integration test on a Linux system by populating the file `env.sh` with values for your Dell PowerMax systems and then run "`make integration-test`".

## Runtime Dependencies
Both the Controller and the Node portions of the driver can only be run on nodes which have network connectivity to a “`Unisphere for PowerMax`” server (which is used by the driver). 

If you are using ISCSI, then the Node portion of the driver can only be run on nodes that have the iscsi-initiator-utils package installed.

## Driver Installation
Please consult the [Installation Guide](https://dell.github.io/csm-docs/docs/csidriver/installation)

## Using Driver
Please refer to the section `Testing Drivers` in the [Documentation](https://dell.github.io/csm-docs/docs/csidriver/installation/test/) for more info.

## Documentation
For more detailed information on the driver, please refer to [Container Storage Modules documentation](https://dell.github.io/csm-docs/).
