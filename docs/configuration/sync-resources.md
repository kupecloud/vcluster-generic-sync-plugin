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
| `virtualControlledBy` | No | `false` | `fromHost` only. Labels virtual copies `vcluster.loft.sh/controlled-by: generic-sync` so vCluster's built-in syncers leave them alone. See [Ownership label](#ownership-label). |
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

vCluster's built-in syncers skip, in turn, any virtual object with a non-empty
`vcluster.loft.sh/controlled-by` label. A `fromHost` copy of a kind vCluster syncs to the
host itself (a `Service`, `Endpoints`, `Secret`, ...) is otherwise picked up by that
syncer and written back to the host as a translated object. Set `virtualControlledBy:
true` on the resource to label every copy `vcluster.loft.sh/controlled-by: generic-sync`:
vCluster's syncers then ignore it, while the plugin still updates and deletes it. The label
is set on every create and update; turning the option off removes it again. Leave the
option off for kinds that must still reach the host through vCluster, such as a `Secret`
mounted by a pod. Only `fromHost` resources accept it.

```yaml
- apiVersion: v1
  kind: Service
  direction: fromHost
  virtualControlledBy: true
  selector:
    matchLabels:
      sync: "true"
```

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

## Target name (fromHost)

By default the imported copy keeps the host object's name. The `kupe.cloud/target-name`
annotation on the host object sets a different name for the copy, which lets a platform
operator give host objects opaque or collision-proof names while the copy carries the
name workloads in the vcluster expect. It combines with `kupe.cloud/target-namespace`.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: binding-7f3a9c        # host name: any unique name
  namespace: vcluster-my-vcluster
  labels:
    sync: "true"
  annotations:
    kupe.cloud/target-namespace: shop
    kupe.cloud/target-name: orders-db   # the copy is shop/orders-db
```

- **Validation.** The value must be a name the API server accepts for the kind: a
  DNS-1035 label for `Service` (at most 63 characters, lowercase letters, digits and `-`,
  starting with a letter), an RFC 1123 label for `Namespace`, and an RFC 1123 subdomain
  for every other kind. An empty value is treated as unset.
- **Invalid values are skipped, never renamed to the host name.** A host object with an
  invalid `kupe.cloud/target-name` is not imported. The plugin logs a warning and records
  an `InvalidTargetName` warning event on the host object. (An invalid
  `kupe.cloud/target-namespace`, by contrast, falls back to the configured namespace.)
- **Changing or removing the annotation** moves the copy: it is created at the new name
  and the copy at the old name is deleted. If the annotation becomes invalid, the old copy
  is deleted and nothing replaces it. As with every delete the plugin performs, only a
  copy carrying this host object's `kupe.cloud/synced-from` provenance is removed; an
  object created inside the vcluster at the old name is left alone.
- **Host deletion** removes the renamed copy, as for any `fromHost` object in `sync` or
  `mirror` mode.
- The copy keeps the host object's annotations, including `kupe.cloud/target-name`; the
  plugin uses it, with `kupe.cloud/synced-from`, to pair the copy with its host source. A
  copy a user makes of it under another name is not mistaken for the plugin's own copy,
  but a copy with the same name in another namespace that keeps `kupe.cloud/synced-from`
  is treated as a stale copy and deleted, exactly as for `kupe.cloud/target-namespace`:
  remove that annotation from copies you make yourself.
- Events about host objects are written to the host namespace, which the vcluster's
  default host Role allows (`events` `create`).

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
