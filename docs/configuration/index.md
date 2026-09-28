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
| `globalExtraLabels` | No | `null` | Labels merged onto every synced target object. Per-resource `extraLabels` win on key conflict. See below. |
| `syncResources` | No | `[]` | List of resources to sync. May be empty or absent — only `version` is required — in which case nothing is synced. |

## Global extra labels

`globalExtraLabels` is merged onto all synced target objects (host objects for `toHost`, virtual objects for `fromHost`), after vCluster's standard label translation. Merge precedence, from lowest to highest:

1. `globalExtraLabels`
2. per-resource `extraLabels` (wins on key conflict)
3. labels the plugin itself stamps — `kupe.cloud/managed-by`, `kupe.cloud/tenant`, and the vCluster marker label — which extra labels can never override once set

Keys using the vCluster-reserved `vcluster.loft.sh/` prefix trigger a startup warning: overwriting those labels can break vCluster's object tracking.

```yaml
globalExtraLabels:
  environment: production
```

## Global namespace filter

`globalFilters` applies across all syncers. Excludes are always enforced; includes restrict which namespaces are allowed. If no includes are set, all namespaces are allowed (subject to excludes). Currently, global filters only apply to namespaces.

**Note:** For `fromHost` **namespaced** resources, the default host cache only watches the vcluster namespace. Namespace filters still apply, but they won’t expand the watched host namespaces unless you customize the host cache.

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
