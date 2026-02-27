# vCluster Generic Sync Plugin

Sync Kubernetes resources and CRDs between host and virtual vClusters.

## What it does

- Syncs explicit resource kinds in either direction (`toHost`, `fromHost`).
- Supports `sync` and `mirror` modes with optional status sync.
- Translates names, namespaces, and selectors via patch types.
- Global and per-resource namespace filtering with include/exclude precedence.
- Status subresource detection to avoid invalid status writes across clusters.
- Event filtering to skip no-op reconciliations and reduce load.
- Built-in Prometheus metrics and trace logging for observability.

## Quick start

Add the plugin to your vcluster values and provide a minimal config. The `config` block is passed directly to the plugin.

```yaml
plugin:
  generic-sync:
    image: ghcr.io/kupecloud/vcluster-generic-sync-plugin:latest
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

- [Getting Started](docs/getting-started.md)
- [Configuration Overview](docs/configuration/index.md)
- [Design Overview](docs/design/overview.md)
- [Metrics](docs/observability/metrics.md)
- [Trace Logging](docs/observability/tracing.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Testing](docs/testing/index.md)

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

1. Fork the repository
2. Create a feature branch
3. Run tests and linting
4. Submit a pull request
