# Dockerfile to build PowerMax CSI Driver
FROM docker.io/centos:centos8.3.2011

# dependencies, following by cleaning the cache
RUN yum install -y \
    e2fsprogs \
    which \
    xfsprogs \
    device-mapper-multipath \
    && \
    yum clean all \
    && \
    rm -rf /var/cache/run

# validate some cli utilities are found
RUN which mkfs.ext4
RUN which mkfs.xfs

COPY "csi-powermax" .
COPY "csi-powermax.sh" .
ENTRYPOINT ["/csi-powermax.sh"]
