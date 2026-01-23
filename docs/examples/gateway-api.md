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
    image: ghcr.io/kupe/vcluster-generic-sync-plugin:latest
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

## Behavior summary

| Resource | Direction | Mode | Notes |
| --- | --- | --- | --- |
| `Gateway` | `fromHost` | `mirror` | Host is source of truth. Virtual-only objects are deleted to enforce read-only. Status sync is disabled. |
| `HTTPRoute` | `toHost` | `sync` | Virtual is source of truth. Host objects are updated. Status sync flows host to virtual. |

## Label requirements

This example only syncs objects labeled with:

```
edge.kupecloud.io/sync: "true"
```

Apply the label to host Gateways and vcluster HTTPRoutes you want to manage.
