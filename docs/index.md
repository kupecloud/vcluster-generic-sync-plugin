---
title: vCluster Generic Sync Plugin
description: Sync Kubernetes resources between vcluster and host with flexible selectors, patches, and observability.
---

The Generic Sync Plugin lets you synchronize selected Kubernetes resources between a vcluster and its host cluster. It is designed for CRDs and shared infrastructure resources where you want precise control over direction, filtering, and reference translation.

## What this plugin does

- Syncs specific resource kinds (CRDs or core resources) in either direction.
- Supports `sync` and `mirror` modes with optional status sync.
- Filters by labels and namespaces at both global and per-resource levels.
- Translates names, namespaces, and references through configurable patches.
- Exposes Prometheus metrics and optional trace-level logging.

## Quick links

- [Getting Started](getting-started.md)
- [Configuration Overview](configuration/index.md)
- [Design Overview](design/overview.md)
- [Sync Resources](configuration/sync-resources.md)
- [Selectors and Namespace Filters](configuration/selectors-namespaces.md)
- [Patches](configuration/patches.md)
- [Examples](examples/index.md)
- [Metrics](observability/metrics.md)
- [Trace Logging](observability/tracing.md)
- [Testing](testing/index.md)
