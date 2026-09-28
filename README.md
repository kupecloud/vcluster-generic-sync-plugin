<!-- markdownlint-disable MD013 -->
# vCluster Generic Sync Plugin

[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)
[![CI](https://github.com/kupecloud/vcluster-generic-sync-plugin/actions/workflows/main.yaml/badge.svg)](https://github.com/kupecloud/vcluster-generic-sync-plugin/actions/workflows/main.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/kupecloud/vcluster-generic-sync-plugin)](https://goreportcard.com/report/github.com/kupecloud/vcluster-generic-sync-plugin)
![Go Version](https://img.shields.io/github/go-mod/go-version/kupecloud/vcluster-generic-sync-plugin)

Sync Kubernetes resources and CRDs between host and virtual vClusters.

<!-- toc -->

* [What it does](#what-it-does)
* [Prerequisites](#prerequisites)
* [Quick start](#quick-start)
* [Documentation](#documentation)
* [Development](#development)
* [Contributing](#contributing)
* [License](#license)

<!-- Regenerate with "pre-commit run -a markdown-toc" -->

<!-- tocstop -->

## What it does

* Syncs explicit resource kinds in either direction (`toHost`, `fromHost`).
* Supports `sync` and `mirror` modes with optional status sync.
* Translates names, namespaces, and selectors via patch types.
* Global and per-resource namespace filtering with include/exclude precedence.
* Status subresource detection to avoid invalid status writes across clusters.
* Event filtering to skip no-op reconciliations and reduce load.
* Built-in Prometheus metrics and trace logging for observability.

## Prerequisites

The plugin is built and tested against:

* vCluster 0.37.x (Helm chart and SDK)
* Kubernetes 1.35.x (kind, in the E2E suite)
* Go 1.26 (to build from source)

Other versions may work but are untested.

## Quick start

Add the plugin to your vcluster values and provide a minimal config. The `config` block is passed directly to the plugin. Replace `vX.Y.Z` with the latest [release](https://github.com/kupecloud/vcluster-generic-sync-plugin/releases) — pin a tag rather than using `latest`.

```yaml
plugin:
  generic-sync:
    version: v2
    image: ghcr.io/kupecloud/vcluster-generic-sync-plugin:vX.Y.Z
    imagePullPolicy: IfNotPresent
    config:
      version: v1
      syncResources:
        - apiVersion: example.com/v1
          kind: Widget
          direction: toHost
```

> Grant RBAC for every resource you sync, plus CRD discovery on the host. See [Base Plugin Values](docs/examples/base-config.md).

## Documentation

* [Getting Started](docs/getting-started.md)
* [Configuration Overview](docs/configuration/index.md)
* [Design Overview](docs/design/overview.md)
* [Examples](docs/examples/index.md)
* [Metrics](docs/observability/metrics.md)
* [Trace Logging](docs/observability/tracing.md)
* [Troubleshooting](docs/troubleshooting.md)
* [Testing](docs/testing/index.md)

## Development

```bash
make build         # Build plugin binary
make test          # Run unit/integration tests
make e2e           # Run E2E suite (kind + vcluster)
make dev           # DevSpace workflow
make dev-purge     # Cleanup DevSpace
```

See [Testing](docs/testing/index.md) for E2E details and environment variables.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, commit
conventions, and pull request guidelines.

Please read our [Code of Conduct](CODE_OF_CONDUCT.md) before participating.

To report security vulnerabilities, see [SECURITY.md](SECURITY.md).

## License

This project is licensed under the Apache License 2.0 — see the
[LICENSE](LICENSE) file for details.
