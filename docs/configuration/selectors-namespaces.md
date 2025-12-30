---
title: Selectors and Namespace Filters
description: Control which objects are eligible for syncing.
---

Filtering is applied in two layers:

1. **Namespace filtering** (global and per-resource)
2. **Label selector filtering** (per-resource)

If a resource passes namespace rules and label selectors, it is eligible for sync. Only resources listed in `syncResources` are ever synced.

## Label selectors

Use `selector.matchLabels` to require exact label matches. All key/value pairs must match.

```yaml
selector:
  matchLabels:
    sync: "true"
    team: "payments"
```

If `selector` is omitted or `matchLabels` is empty, **all objects of that kind** are eligible (subject to namespace filtering).

## Per-resource namespace filters

`selector.matchNamespaces` and `selector.excludeNamespaces` apply to a single syncer.

```yaml
selector:
  matchNamespaces:
    - "app-*"
  excludeNamespaces:
    - "app-dev"
```

Patterns support standard glob syntax (`*`, `?`).

## Global namespace filter

`globalFilters` applies across all syncers. Rules can be scoped to specific resources using `resources`.

```yaml
globalFilters:
  exclude:
    - namespace: "kube-*"
  include:
    - namespace: "team-*"
      resources:
        - Secret
        - gateway.networking.k8s.io/v1/Gateway
```

### Precedence

Namespace filtering is evaluated in this order:

1. Global excludes (always enforced)
2. Per-resource excludes
3. Includes (resource-level includes override global includes)
4. If no includes are specified, all namespaces are allowed

Cluster-scoped resources ignore namespace filters.

## Selector label translation

When you use the `rewriteLabelSelector` patch type, selector labels are translated between virtual and host forms. If you set `selectorIncludeOwnerLabels: true`, the plugin also injects marker/namespace labels during translation to preserve vcluster ownership semantics.

## Example

```yaml
globalFilters:
  exclude:
    - namespace: "kube-*"

syncResources:
  - apiVersion: v1
    kind: ConfigMap
    direction: fromHost
    selector:
      matchLabels:
        sync: "true"
      matchNamespaces:
        - "team-*"
      excludeNamespaces:
        - "team-dev"
```
