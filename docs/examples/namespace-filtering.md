---
title: Widget Namespace Filtering
description: Combine global and per-resource namespace filters for Widgets.
---

This example uses both global and per-resource namespace filters to control which **vcluster namespaces** Widgets can be synced from.

## Prerequisites

Install the Widget CRD on both host and vcluster (the manifest lives in this repository — clone it first):

```bash
kubectl apply -f test/testdata/widget-crd.yaml
vcluster connect my-vcluster -- kubectl apply -f test/testdata/widget-crd.yaml
```

## vcluster.yaml

```yaml
plugin:
  generic-sync:
    version: v2
    image: ghcr.io/kupecloud/vcluster-generic-sync-plugin:1.5.0 # replace with the latest release tag (unprefixed)
    imagePullPolicy: IfNotPresent
    rbac:
      role:
        extraRules:
          - apiGroups: ["example.com"]
            resources: ["widgets"]
            verbs: ["create", "delete", "patch", "update", "get", "list", "watch"]
      clusterRole:
        extraRules:
          - apiGroups: ["apiextensions.k8s.io"]
            resources: ["customresourcedefinitions"]
            verbs: ["get", "list", "watch"]
    config:
      version: v1
      log_level: info
      globalFilters:
        include:
          - namespace: "team-*"
        exclude:
          - namespace: "kube-*"
      syncResources:
        - apiVersion: example.com/v1
          kind: Widget
          direction: toHost
          selector:
            matchNamespaces:
              - "team-*"
            excludeNamespaces:
              - "team-dev"
```

## How filtering works

| Layer | Purpose | Example |
| --- | --- | --- |
| Global filters | Apply to all syncers | Allow `team-*`, block `kube-system`. |
| Per-resource filters | Narrow a single syncer | Allow `team-*`, exclude `team-dev`. |

Global excludes are always enforced. Per-resource includes can further narrow the set of namespaces that are eligible.
