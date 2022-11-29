# Copyright © 2020 Dell Inc. or its subsidiaries. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#      http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
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
