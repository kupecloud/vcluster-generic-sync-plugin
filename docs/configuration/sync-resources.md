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
| `targetNamespace` | No | `default` | Target namespace in the vcluster for `fromHost` resources. Must be RFC 1123 and not a `kube-*` system namespace. |
| `hostNamespace` | No | vcluster's own namespace | Shared host namespace for `toHost` resources (e.g. `argocd`). Must be RFC 1123 and not a `kube-*` system namespace. See [Shared host namespaces](#shared-host-namespaces). |
| `selector` | No | - | Label and namespace filters. |
| `selectorIncludeOwnerLabels` | No | `false` | Add marker/namespace labels when rewriting selectors. |
| `patches` | No | - | Reference translation patches. |
| `extraLabels` | No | - | Labels merged onto target objects (host objects for `toHost`, virtual objects for `fromHost`), applied after vCluster's label translation. Wins over `globalExtraLabels` on key conflict; cannot override the labels the plugin stamps itself (`kupe.cloud/managed-by`, `kupe.cloud/tenant`, the vCluster marker). Keys with the reserved `vcluster.loft.sh/` prefix trigger a startup warning. |
| `enforceTenantProject` | No | `false` | `toHost` only. Overwrites the synced object's `spec.project` with the tenant name derived from the vCluster's host namespace. See [Shared host namespaces](#shared-host-namespaces). |
| `hostOwnedFields` | No | - | `toHost` only. Top-level fields of the host copy owned by a host controller: never copied from or deleted for the virtual object, and host-side changes to them do not trigger a reconcile. Argo CD `Application` gets `operation` by default. `apiVersion`, `kind`, `metadata`, `status`, and `spec` cannot be listed. See [Shared host namespaces](#shared-host-namespaces). |

## Direction

- **`toHost`**: vcluster is the source; objects are created/updated on the host.
- **`fromHost`**: host is the source; objects are created/updated in the vcluster. **Namespaced resources are only read from the host vcluster namespace by default.**

## Mode

- **`sync`**: Full sync behavior. For `toHost`, changes are applied to the host; for `fromHost`, host changes update the virtual objects. Status sync is allowed if enabled and the CRD supports it.
- **`mirror`**: Read-only behavior for `fromHost` resources. Only syncer-created copies — stamped with the `kupe.cloud/synced-from` provenance annotation — are managed: when their host source is gone the mirrored virtual copy is deleted. An object created inside the virtual cluster that merely shares a name (and was never synced) is never deleted. Status sync is disabled in mirror mode. For `toHost`, `mirror` currently behaves like `sync` but status sync is still disabled.

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

## Shared host namespaces

By default, `toHost` resources are written into the vcluster's own host namespace.
Setting `hostNamespace` redirects them into a shared namespace owned by the platform
operator, such as `argocd` or `observability`, where objects from many vClusters
co-exist. This is an isolation-sensitive feature with extra invariants:

- **Naming.** Host object names encode the object name, its virtual namespace, and the
  owning vCluster's identity, and carry a deterministic hash suffix, so distinct
  vClusters — and one vCluster's same-named objects in different virtual namespaces —
  never collide on one host object. The reverse mapping reads the original
  name/namespace from translation annotations, so the host name itself is an opaque,
  collision-proof key.
- **Marker label.** In shared namespaces the `vcluster.loft.sh` marker label is overridden
  to the vCluster's own host namespace (unique per vCluster), so each vCluster's syncer
  only ever manages (adopts, updates, deletes) its own objects.
- **Ownership labels.** Synced objects carry `kupe.cloud/managed-by: vcluster-sync`, and —
  when the vCluster's host namespace follows the `vcluster-{name}--{cluster}` layout the
  tenant derivation understands — a `kupe.cloud/tenant` label, for host-side
  ownership/audit.
- **OwnerReferences.** The SDK's owner reference (to the vcluster Service) lives in the
  vcluster's own namespace and would be treated as dangling cross-namespace by Kubernetes
  GC, so owner references are stripped on create for shared-namespace objects.
- **ArgoCD `spec.project`.** When `enforceTenantProject` is set (typically on an ArgoCD
  `Application` syncer), `spec.project` is pinned to the tenant name derived from the
  vCluster's host namespace, so a vCluster user cannot self-assert a different, more
  permissive project. This derivation requires host namespaces named
  `vcluster-{tenant}--{cluster}`; on any other layout the sync fails closed rather than
  emit a host object with a user-controlled project, so platforms with a different
  namespace layout cannot use this feature.
- **Host-owned fields.** `hostOwnedFields` lists top-level fields of the host copy that a
  host controller writes and the virtual cluster never does. The syncer leaves them
  exactly as they are: not copied from the virtual object, not deleted when the virtual
  object lacks them, and a host-side change to them does not trigger a reconcile. ArgoCD
  `Application` gets `operation` by default — Argo stores a pending sync there, and
  before this the syncer deleted it in the window between Argo setting it and Argo's
  worker reading it, so automated sync silently never ran (or ran minutes late when the
  race happened to be won). `apiVersion`, `kind`, `metadata`, `status` and `spec` cannot
  be listed.

  ```yaml
  - apiVersion: argoproj.io/v1alpha1
    kind: Application
    direction: toHost
    hostNamespace: argocd
    enforceTenantProject: true
    hostOwnedFields: [operation]   # the default for this kind; shown for clarity
  ```
- **RBAC.** Because the host cache is widened to include every configured `hostNamespace`
  for all informers, the syncer ServiceAccount must have list/watch for the synced kinds
  in each shared namespace. Keep these grants scoped to the minimal kinds.

## Target namespace (fromHost)

For `fromHost` resources, `targetNamespace` selects the vcluster namespace the imported
copy is created in (default `default`). A per-object override is supported via the
`kupe.cloud/target-namespace` annotation on the host object (validated as RFC 1123;
invalid values are ignored with a warning). The target namespace is created in the vcluster
if it does not already exist.

`fromHost` namespaced resources are read **only** from the vcluster's own host namespace,
even though the host cache may be widened by `hostNamespace` overrides on other resources —
objects in shared/platform namespaces are never imported.

## Deletion propagation

- **`toHost`**: deleting the virtual object deletes the host object.
- **`fromHost` + `mirror`**: when the host source is gone, the mirrored virtual copy is
  deleted. Only syncer-created copies (marked `kupe.cloud/synced-from`) are removed; an
  object created inside the virtual cluster that merely shares a name is never deleted.
- **`fromHost` + `sync`**: when the host source object is deleted, the synced virtual copy
  is deleted as well. Syncer-created copies are marked with `kupe.cloud/synced-from`; an
  object created inside the virtual cluster that merely shares a name (and was never
  synced) is never deleted.
- **`fromHost` selector no longer matches**: if a previously imported host object stops
  matching the resource selector (e.g. the platform operator removes its sync label), the
  synced virtual copy is deleted so stale imported data — including any credential
  material — does not linger in the vcluster. This cleanup is gated on the same
  `kupe.cloud/synced-from` provenance annotation, so an object created inside the virtual
  cluster is left untouched.

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
