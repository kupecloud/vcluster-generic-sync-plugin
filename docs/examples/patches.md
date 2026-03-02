---
title: Patch Examples
description: Translate names, namespaces, and selectors with patch types.
---

Patches translate names, namespaces, and selectors when syncing objects. They do not perform general spec mutation.

## HTTPRoute Example (Gateway API)

HTTPRoute is a common use case for reference translation. Routes reference Gateways (on the host) and Services (in the vcluster).

### Example HTTPRoute

```yaml
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: my-route
  namespace: default
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

### Configuration with patches

```yaml
plugin:
  generic-sync:
    image: ghcr.io/kupecloud/vcluster-generic-sync-plugin:latest
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

## Patch types explained

| Type | Use case | What it does |
| --- | --- | --- |
| `rewriteRef` | Backend services | Translates `{name, namespace}` to vcluster naming convention |
| `rewriteHostRef` | Parent gateways | Keeps namespace as-is (host resource) |
| `rewriteName` | Single name field | Translates just the name field |
| `rewriteNamespace` | Single namespace field | Translates just the namespace field |
| `rewriteLabelSelector` | Selector fields | Translates label keys in matchLabels |
| `none` | Skip translation | Copies value as-is |

## Split name and namespace patches

When name and namespace are in separate fields:

```yaml
patches:
  - path: spec.someRef.name
    type: rewriteName
  - path: spec.someRef.namespace
    type: rewriteNamespace
```

## Label selector translation

For resources with selector fields:

```yaml
patches:
  - path: spec.selector
    type: rewriteLabelSelector
```

This translates label keys like `app` to include vcluster namespacing.
