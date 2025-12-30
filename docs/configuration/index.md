---
title: Configuration Overview
description: Global plugin settings and configuration schema.
---

The plugin configuration is provided in the `config` block under your plugin entry in the vcluster values. The chart passes this to the plugin as `PLUGIN_CONFIG` (legacy `CONFIG` is still supported for direct env injection).

## Minimal schema

```yaml
version: v1
log_level: info
syncResources:
  - apiVersion: <group>/<version>
    kind: <Kind>
    direction: toHost | fromHost
```

## Global fields

| Field | Required | Default | Description |
| --- | --- | --- | --- |
| `version` | Yes | `v1` | Config schema version. |
| `log_level` | No | `info` | Log verbosity: `error`, `warning`, `info`, `debug`, `trace`. |
| `events_enabled` | No | `true` | Emit Kubernetes Events for sync operations. |
| `max_concurrent_reconciles` | No | `10` | Max reconciles per syncer. Clamped to `100`. |
| `disable_event_filtering` | No | `false` | Disables the no-op update filter (useful for debugging). |
| `globalFilters` | No | `null` | Global namespace include/exclude rules. |
| `syncResources` | Yes | `[]` | List of resources to sync. An empty list means nothing is synced. |

## Global namespace filter

`globalFilters` applies across all syncers. Excludes are always enforced; includes restrict which namespaces are allowed. If no includes are set, all namespaces are allowed (subject to excludes). Currently, global filters only apply to namespaces.

```yaml
globalFilters:
  include:
    - namespace: "team-*"
  exclude:
    - namespace: "kube-*"
```

You can scope rules to specific resources using `resources`:

```yaml
globalFilters:
  exclude:
    - namespace: "kube-system"
      resources:
        - Secret
        - v1/ConfigMap
```

## Sync resources

See the dedicated pages for per-resource behavior and filtering:

- [Sync Resources](sync-resources.md)
- [Selectors and Namespace Filters](selectors-namespaces.md)
- [Patches](patches.md)

## Example config

```yaml
version: v1
log_level: info
events_enabled: true
max_concurrent_reconciles: 20
globalFilters:
  exclude:
    - namespace: "kube-*"
syncResources:
  - apiVersion: gateway.networking.k8s.io/v1
    kind: HTTPRoute
    direction: toHost
    statusSync: true
    patches:
      - path: spec.parentRefs[*]
        type: rewriteHostRef
      - path: spec.rules[*].backendRefs[*]
        type: rewriteRef
```
