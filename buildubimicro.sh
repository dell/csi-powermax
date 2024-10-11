#!/bin/bash
microcontainer=$(buildah from $1)
micromount=$(buildah mount $microcontainer)
dnf install --installroot $micromount --releasever=9 --nodocs --setopt install_weak_deps=false --setopt=reposdir=/etc/yum.repos.d/ rpm e2fsprogs nfs-utils nfs4-acl-tools acl which xfsprogs device-mapper-multipath libxcrypt-compat libblockdev util-linux -y
dnf clean all --installroot $micromount
buildah umount $microcontainer
buildah commit $microcontainer csipowermax-ubimicro
