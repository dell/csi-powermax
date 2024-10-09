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
