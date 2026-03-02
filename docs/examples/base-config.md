---
title: Base Plugin Values
description: Baseline Helm values for installing the plugin with RBAC scaffolding.
---

Use this baseline as the starting point for all examples. It is a valid Helm values fragment that you can merge into your vcluster values file. The `config` block from each example should be added under `plugin.generic-sync.config`.

```yaml
plugin:
  generic-sync:
    image: ghcr.io/kupecloud/vcluster-generic-sync-plugin:latest
    imagePullPolicy: IfNotPresent
    rbac:
      role:
        extraRules:
          # Replace with permissions for YOUR CRDs
          - apiGroups: ["example.com"]
            resources: ["widgets"]
            verbs: ["create", "delete", "patch", "update", "get", "list", "watch"]
      clusterRole:
        extraRules:
          - apiGroups: ["apiextensions.k8s.io"]
            resources: ["customresourcedefinitions"]
            verbs: ["get", "list", "watch"]

# Required for plugin to watch CRDs
rbac:
  clusterRole:
    enabled: true
```
