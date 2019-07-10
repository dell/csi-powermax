# Dockerfile to build PowerMax CSI Driver
FROM centos:7.6.1810

# dependencies, following by cleaning the cache
RUN yum install -y \
    e2fsprogs \
    which \
    xfsprogs \
    && \
    yum clean all \
    && \
    rm -rf /var/cache/run

# validate some cli utilities are found
RUN which mkfs.ext4
RUN which mkfs.xfs

COPY "csi-powermax" .
ENTRYPOINT ["/csi-powermax"]
