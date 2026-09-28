---
title: Full Example
description: Combine global filters, selectors, and patches in one config.
---

This example shows a complete HTTPRoute sync configuration: global namespace filters, label selectors, reference patches, and status sync.

## vcluster.yaml

```yaml
plugin:
  generic-sync:
    version: v2
    image: ghcr.io/kupecloud/vcluster-generic-sync-plugin:vX.Y.Z  # pin the latest release
    imagePullPolicy: IfNotPresent
    rbac:
      role:
        extraRules:
          - apiGroups: ["gateway.networking.k8s.io"]
            resources: ["httproutes"]
            verbs: ["create", "delete", "patch", "update", "get", "list", "watch"]
      clusterRole:
        extraRules:
          - apiGroups: ["apiextensions.k8s.io"]
            resources: ["customresourcedefinitions"]
            verbs: ["get", "list", "watch"]
    config:
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
          selector:
            matchLabels:
              routes.kupecloud.io/sync: "true"
          patches:
            - path: spec.parentRefs[*]
              type: rewriteHostRef
            - path: spec.rules[*].backendRefs[*]
              type: rewriteRef
```

## Example HTTPRoute

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: my-route
  namespace: default
  labels:
    routes.kupecloud.io/sync: "true"
spec:
  parentRefs:
    - name: shared-gateway
      namespace: gateway-ns
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /api
      backendRefs:
        - name: api-service
          namespace: default
          port: 8080
```

## Why this is "full"

| Capability | Where it is configured |
| --- | --- |
| Global namespace control | `globalFilters.exclude` |
| Per-resource selection | `selector.matchLabels` |
| Reference translation | `patches` on `parentRefs` and `backendRefs` |
| Status propagation | `statusSync: true` |
| Concurrency tuning | `max_concurrent_reconciles: 20` |
| Kubernetes events | `events_enabled: true` |
