---
title: Gateway API (Mirror + Sync)
description: Mirror Gateways from host to vcluster and sync HTTPRoutes back to host.
---

This example mirrors `Gateway` objects from the host into the vcluster (read-only), while syncing `HTTPRoute` objects from the vcluster to the host. It is a good starting point for shared ingress infrastructure.

**Note:** By default, the vCluster SDK only watches the vcluster namespace on the host. That means host `Gateway` objects must live in the vcluster namespace to be discovered by this plugin (unless you customize host cache behavior).

## Prerequisites

Install Gateway API CRDs on both host and vcluster. This example uses Gateway API v1.4.1:

```bash
kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.1/standard-install.yaml
vcluster connect my-vcluster -- kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.4.1/standard-install.yaml
```

## vcluster.yaml

```yaml
plugin:
  generic-sync:
    image: ghcr.io/kupecloud/vcluster-generic-sync-plugin:latest
    imagePullPolicy: IfNotPresent
    rbac:
      role:
        extraRules:
          - apiGroups: ["gateway.networking.k8s.io"]
            resources: ["gateways", "httproutes"]
            verbs: ["create", "delete", "patch", "update", "get", "list", "watch"]
      clusterRole:
        extraRules:
          - apiGroups: ["apiextensions.k8s.io"]
            resources: ["customresourcedefinitions"]
            verbs: ["get", "list", "watch"]
    config:
      version: v1
      log_level: info
      syncResources:
        - apiVersion: gateway.networking.k8s.io/v1
          kind: Gateway
          direction: fromHost
          mode: mirror
          selector:
            matchLabels:
              edge.kupecloud.io/sync: "true"
        - apiVersion: gateway.networking.k8s.io/v1
          kind: HTTPRoute
          direction: toHost
          statusSync: true
          selector:
            matchLabels:
              edge.kupecloud.io/sync: "true"
          patches:
            - path: spec.parentRefs[*]
              type: rewriteHostRef
            - path: spec.rules[*].backendRefs[*]
              type: rewriteRef
```

## Shared Gateway in another host namespace

A `fromHost` mirror only admits host objects from the vcluster's own host
namespace (the managed-object check in `syncers/fromhost.go`), so a shared
Gateway that lives elsewhere, for example `kube-system/external-gateway`,
**cannot be mirrored**. Do not add a `Gateway` mirror entry for it: it will
never import anything.

Instead, let tenants reference the host Gateway directly and copy `parentRefs`
verbatim:

```yaml
        - apiVersion: gateway.networking.k8s.io/v1
          kind: HTTPRoute
          direction: toHost
          statusSync: true
          patches:
            # Tenants write parentRefs: {name: external-gateway, namespace: kube-system}
            - path: spec.parentRefs[*]
              type: none
            - path: spec.rules[*].backendRefs[*]
              type: rewriteRef
```

With `type: none` the plugin does not constrain which Gateway a route targets,
so enforcement must live on the host:

- each Gateway listener's `allowedRoutes` should admit routes only from the
  intended vcluster host namespace(s), with a listener `hostname` scoped to
  that tenant, and
- an admission policy on the host (e.g. Kyverno) should restrict HTTPRoute
  hostnames and `parentRefs` in vcluster host namespaces.

This is how kupe runs it. vcluster 0.35+ also ships a native OSS Gateway import
(`sync.fromHost.gateways`), but the imported copy exposes every listener on the
shared Gateway (all tenants' hostnames and custom domains), and filtering it
needs vcluster Pro `patches`, so kupe does not use it.

## Behavior summary

| Resource | Direction | Mode | Notes |
| --- | --- | --- | --- |
| `Gateway` | `fromHost` | `mirror` | Host is source of truth. Only syncer-created copies (stamped `kupe.cloud/synced-from`) are deleted when their host source is gone; a tenant's own Gateway is never deleted. Status sync is disabled. |
| `HTTPRoute` | `toHost` | `sync` | Virtual is source of truth. Host objects are updated. Status sync flows host to virtual. |

## Label requirements

This example only syncs objects labeled with:

```
edge.kupecloud.io/sync: "true"
```

Apply the label to host Gateways and vcluster HTTPRoutes you want to manage.
