---
title: Sync Resources
description: Define which kinds are synced and how they behave.
---

Each entry in `syncResources` defines a resource kind to sync and how to handle it.

## Fields

| Field | Required | Default | Description |
| --- | --- | --- | --- |
| `apiVersion` | Yes | - | API version (e.g., `v1`, `gateway.networking.k8s.io/v1`). |
| `kind` | Yes | - | Kind name (e.g., `Secret`, `HTTPRoute`). |
| `direction` | Yes | - | `toHost` or `fromHost`. |
| `mode` | No | `sync` | `sync` or `mirror` (see below). |
| `statusSync` | No | `false` | Sync status subresource (only when supported). |
| `targetNamespace` | No | `default` | Target namespace for `fromHost` resources. |
| `selector` | No | - | Label and namespace filters. |
| `selectorIncludeOwnerLabels` | No | `false` | Add marker/namespace labels when rewriting selectors. |
| `patches` | No | - | Reference translation patches. |

## Direction

- **`toHost`**: vcluster is the source; objects are created/updated on the host.
- **`fromHost`**: host is the source; objects are created/updated in the vcluster.

## Mode

- **`sync`**: Full sync behavior. For `toHost`, changes are applied to the host; for `fromHost`, host changes update the virtual objects. Status sync is allowed if enabled and the CRD supports it.
- **`mirror`**: Read-only behavior for `fromHost` resources. If a virtual-only object appears, it is deleted to enforce read-only semantics. Status sync is disabled in mirror mode. For `toHost`, `mirror` currently behaves like `sync` but status sync is still disabled.

## Status sync

`statusSync` only takes effect when:

- The resource is in `sync` mode (not `mirror`), and
- The resource exposes a `status` subresource (detected automatically during registration).

This works for both CRDs and core API resources. For example:
- **Pods, Services, Deployments**: Have status subresources, status sync works
- **Secrets, ConfigMaps**: No status subresources, status sync is disabled

Status always flows from host to virtual cluster, regardless of sync direction. If either condition is not met, status sync is skipped and a warning is logged.

## Naming behavior

For namespaced resources synced to the host, names are translated using the vcluster naming convention:

```
<name>-x-<namespace>-x-<vcluster>
```

Cluster-scoped resources use a host-safe translated name without namespaces.

## Ownership label

If an object has a `vcluster.loft.sh/controlled-by` label or annotation with a value other than `generic-sync`, the plugin skips it. This prevents conflicts with other vcluster controllers.

## Example

```yaml
syncResources:
  - apiVersion: gateway.networking.k8s.io/v1
    kind: HTTPRoute
    direction: toHost
    mode: sync
    statusSync: true
    selector:
      matchLabels:
        app: edge
    patches:
      - path: spec.parentRefs[*]
        type: rewriteHostRef
      - path: spec.rules[*].backendRefs[*]
        type: rewriteRef
```
