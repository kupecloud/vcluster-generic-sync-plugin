---
title: Design Overview
description: High-level design of the Generic Sync Plugin.
---

This page provides a high-level view of how the plugin works and why it is structured the way it is. It is meant to be read alongside the configuration reference.

## Goals

- Sync selected resource kinds between vcluster and host with minimal friction.
- Keep behavior predictable: no implicit resource discovery or wildcard syncing.
- Make filtering and reference translation explicit and configurable.
- Provide clear observability signals (logs, events, metrics).

## Non-goals

- Automatic conflict resolution for true multi-writer, bi-directional sync.
- Arbitrary mutation of object specs (patches only translate references).

## Core concepts

### Sync resources are explicit

Only kinds listed in `syncResources` are synced. Global filters and selectors only restrict *where* those resources can come from.

### Direction and mode

- `toHost`: vcluster is the source of truth; objects are created on host.
- `fromHost`: host is the source of truth; objects are created in vcluster. Namespaced resources are read from the host vcluster namespace by default.
- `sync`: normal sync flow; status sync is allowed when supported.
- `mirror`: read-only for `fromHost` resources; only syncer-created copies (stamped `kupe.cloud/synced-from`) are deleted when their host source is gone — a tenant's own object is never deleted.

### Reference translation

Patches translate references (names, namespaces, label selectors) so objects can point to the correct counterpart after name/namespace translation.

## High-level flow

```
Configure syncResources
        |
        v
Create syncer per resource
        |
        v
Watch source cluster events
        |
        v
Filter by namespace + selector
        |
        v
Translate names/refs
        |
        v
Create/Update/Delete on target
        |
        v
Record metrics + logs + events
```

## Key components

| Component | Responsibility |
| --- | --- |
| Config loader | Parses the plugin `config` block (passed as `PLUGIN_CONFIG`), validates schema. |
| Syncer factory | Creates a syncer per resource definition. |
| Namespace matcher | Applies global + per-resource namespace filters. |
| Selector matcher | Applies label selectors per resource. |
| Patcher | Translates references and selectors. |
| Excluder | Skips objects controlled by other vcluster controllers. |
| Metrics recorder | Emits Prometheus counters/histograms. |
| Trace logger | Logs full object content at `trace` level. |
| Event emitter | Optional Kubernetes events for sync ops. |

## Safety rails

- **Controlled-by label/annotation**: objects labeled by other controllers are skipped.
- **Namespace filtering**: global and per-resource include/exclude controls.
- **Event filtering**: skips metadata-only updates by default.
- **Concurrency limits**: `max_concurrent_reconciles` per syncer.

## Observability outputs

- **Logs**: structured logs at levels `error`..`trace`.
- **Events**: optional, per-object Kubernetes events.
- **Metrics**: Prometheus metrics on the vcluster metrics endpoint.

## Extensibility

- Add new patch types for additional reference translations.
- Extend global filters beyond namespaces.
- Add new syncer behaviors for specialized resource kinds.
