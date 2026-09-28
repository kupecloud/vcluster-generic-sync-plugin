# Build stage
#
# The builder tag MUST be >= the `go` directive in go.mod (currently 1.26.5).
# The official golang images set GOTOOLCHAIN=local, so the toolchain will NOT
# auto-download a newer Go: a builder older than the go.mod directive hard-fails
# the very first `go mod download` with
#   go: go.mod requires go >= 1.26.5 (running 1.26.3; GOTOOLCHAIN=local)
# Bump this line in the same commit as any go.mod Go bump.
FROM golang:1.27.1-alpine3.23@sha256:d9e2f2f07b10cc922da3e80e035c3058810b328d5aef82d2c63680967c5e2ec9 AS builder

WORKDIR /vcluster

# Install build dependencies
RUN apk add --no-cache git

# Build arguments for version injection
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG BUILD_DATE=unknown

# Copy go mod files and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY main.go ./
COPY syncers/ syncers/
COPY config/ config/
COPY logging/ logging/
COPY metrics/ metrics/
COPY patches/ patches/

# Build the plugin with version info.
# Place it under /plugin/plugin so vCluster's init container can copy the directory.
RUN mkdir -p /plugin && CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-X github.com/kupecloud/vcluster-generic-sync-plugin/syncers.Version=${VERSION} \
              -X github.com/kupecloud/vcluster-generic-sync-plugin/syncers.GitCommit=${GIT_COMMIT} \
              -X github.com/kupecloud/vcluster-generic-sync-plugin/syncers.BuildDate=${BUILD_DATE}" \
    -o /plugin/plugin main.go

# Runtime stage — alpine is required because vCluster's plugin init container
# uses "sh -c cp ..." to copy the plugin binary into the vcluster pod.
# distroless images have no shell and fail at this step.
FROM alpine:3.24@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6

# Copy the plugin directory for vCluster init container.
# vCluster's init container copies /plugin into /plugins/<name>/ inside the vcluster pod.
COPY --from=builder /plugin /plugin

RUN adduser -D -u 65532 nonroot
USER 65532:65532

ENTRYPOINT ["/plugin/plugin"]
