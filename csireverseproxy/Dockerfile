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
############################
# STEP 1 build executable binary
############################
ARG GOIMAGE
ARG BASEIMAGE

FROM $GOIMAGE as builder

# Install git + SSL ca certificates.
# Git is required for fetching the dependencies.
# Ca-certificates is required to call HTTPS endpoints.
RUN apt-get update && apt-get -y install tzdata && update-ca-certificates

# Create revproxy
ENV USER=revproxy
ENV UID=10001

# See https://stackoverflow.com/a/55757473/12429735
RUN adduser \
    --disabled-password \
    --gecos "" \
    --home "/nonexistent" \
    --shell "/sbin/nologin" \
    --no-create-home \
    --uid "${UID}" \
    "${USER}"

RUN mkdir -p /go/src/csireverseproxy
COPY . /go/src/csireverseproxy

WORKDIR /go/src/csireverseproxy
RUN CGO_ENABLED=0 go build


############################
# STEP 2 build a small image
############################
FROM $BASEIMAGE as final
LABEL vendor="Dell Inc." \
      name="csipowermax-reverseproxy" \
      summary="CSI PowerMax Reverse Proxy" \
      description="CSI PowerMax Reverse Proxy which helps manage connections with Unisphere for PowerMax" \
      version="2.10.0" \
      license="Apache-2.0"
COPY licenses /licenses
# Import from builder.
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

# Use an unprivileged user.
USER revproxy:revproxy

COPY --from=builder /go/src/csireverseproxy/revproxy /app/revproxy

WORKDIR /app
CMD ["/app/revproxy"]

EXPOSE 2222
