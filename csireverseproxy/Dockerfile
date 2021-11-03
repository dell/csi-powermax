############################
# STEP 1 build executable binary
############################
# golang alpine 1.14.1
FROM golang@sha256:244a736db4a1d2611d257e7403c729663ce2eb08d4628868f9d9ef2735496659 as builder

# Install git + SSL ca certificates.
# Git is required for fetching the dependencies.
# Ca-certificates is required to call HTTPS endpoints.
RUN apk update && apk add --no-cache tzdata && update-ca-certificates

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
FROM scratch
LABEL vendor="Dell Inc." \
      name="csipowermax-reverseproxy" \
      summary="CSI PowerMax Reverse Proxy" \
      description="CSI PowerMax Reverse Proxy which helps manage connections with Unisphere for PowerMax" \
      version="1.4.0" \
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
