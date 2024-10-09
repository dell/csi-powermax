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
ARG GOPROXY
ARG GOIMAGE
ARG BASEIMAGE
ARG DIGEST

# Stage to build the driver
FROM $GOIMAGE as builder
ARG GOPROXY
RUN mkdir -p /go/src
COPY ./ /go/src/
WORKDIR /go/src/
RUN CGO_ENABLED=0 \
    make build

# Stage to build the driver image
FROM $BASEIMAGE AS final
COPY --from=builder /go/src/csi-powermax .
COPY "csi-powermax.sh" .
ENTRYPOINT ["/csi-powermax.sh"]
RUN chmod +x /csi-powermax.sh
LABEL vendor="Dell Inc." \
    name="csi-powermax" \
    summary="CSI Driver for Dell EMC PowerMax" \
    description="CSI Driver for provisioning persistent storage from Dell EMC PowerMax" \
    version="2.11.0" \
    license="Apache-2.0"
COPY ./licenses /licenses


# WORKDIR /go/src/csi-powermax
# RUN go mod download
# # Run check.sh to make sure there are no linting errors
# RUN ./check.sh
# RUN go generate
# RUN CGO_ENABLED=0 go build
# # Print the version
# RUN go run core/semver/semver.go -f mk

# FROM ${SOURCE_REPO}:${SOURCE_IMAGE_TAG} as driver-others
# ONBUILD RUN yum install -y e2fsprogs which xfsprogs device-mapper-multipath nfs-utils libxcrypt-compat libblockdev nfs4-acl-tools \
#     && \
#     yum clean all \
#     && \
#     rm -rf /var/cache/run

# FROM ${SOURCE_REPO}:${SOURCE_IMAGE_TAG} as driver-ubim
# ONBUILD RUN microdnf update -y \
#     && \
#     microdnf install -y e2fsprogs which xfsprogs nfs-utils nfs4-acl-tools libxcrypt-compat libblockdev device-mapper-multipath \
#     && \
#     microdnf clean all
     
# FROM ${SOURCE_REPO}:${SOURCE_IMAGE_TAG} as driver-ubimicro

# FROM driver-${IMAGE_TYPE} as verify
# ONBUILD RUN which mkfs.ext4
# ONBUILD RUN which mkfs.xfs

# FROM driver-${IMAGE_TYPE} as driver
# COPY --from=builder /go/src/csi-powermax/csi-powermax .
# COPY "csi-powermax/csi-powermax.sh" .
# ENTRYPOINT ["/csi-powermax.sh"]

# # Stage to check for critical and high CVE issues via Trivy (https://github.com/aquasecurity/trivy)
# # will break image build if CRITICAL issues found
# # will print out all HIGH issues found

# FROM driver as trivy-ubim
# ONBUILD RUN microdnf install -y tar

# FROM driver as trivy-others
# ONBUILD RUN echo "Not a UBI minimal image"

# # final stage
# FROM driver as final

# LABEL vendor="Dell Inc." \
#       name="csi-powermax" \
#       summary="CSI Driver for Dell PowerMax" \
#       description="CSI Driver for provisioning persistent storage from Dell PowerMax" \
#       version="2.11.0" \
#       license="Apache-2.0"
# COPY csi-powermax/licenses /licenses
