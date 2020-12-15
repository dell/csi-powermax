# Release Notes - CSI PowerMax v1.5.0

## New Features/Changes
- Added support for OpenShift 4.5/4.6 with RHEL and CoreOS worker nodes
- Added support for Red Hat Enterprise Linux (RHEL) 7.9
- Added support for Ubuntu 20.04
- Added support for Docker EE 3.1
- Added support for Controller high availability (multiple-controllers)
- Added support for Topology
- Added support for mount options
- Changed driver base image to UBI 8.x

## Fixed Issues
There are no fixed issues in this release.

## Known Issues

| Issue | Workaround |
|-------|------------|
| Slow volume attached/detach | If your Kubernetes 1.17 or 1.18 cluster has a lot of VolumeAttachment objects, the attach/detach operations will be very slow. This is a known issue and affects all CSI plugins. It is tracked here: CSI VolumeAttachment slows pod startup time. To get around this problem you can upgrade to latest Kubernetes/OpenShift patches, which contains a partial fix: 1.17.8+, 1.18.5+|
| Delete Volume fails with error message: volume is part of masking view | This issue is due to limitations in Unisphere and occurs when Unisphere is overloaded. Currently, there is no workaround for this but can be avoided by making sure Unisphere is not overloaded during such operations. The Unisphere team is assessing a fix for this in a future Unisphere release|
