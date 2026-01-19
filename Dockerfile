# Build stage
FROM golang:1.26rc2-alpine AS builder

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

# Final stage - minimal image
FROM alpine:3.23

WORKDIR /

# Copy the plugin directory for vCluster init container.
# vCluster's init container copies /plugin into /plugins/<name>/ inside the vcluster pod.
COPY --from=builder /plugin /plugin

ENTRYPOINT ["/plugin/plugin"]
