---
title: Widget Label Selection
description: Sync Widgets only when they match a label selector.
---

This example syncs Widgets from the vcluster to the host, but only when they carry a specific label. Use this when you want opt-in syncing per object.

## Prerequisites

Install the Widget CRD on both host and vcluster:

```bash
kubectl apply -f test/testdata/widget-crd.yaml
vcluster connect my-vcluster -- kubectl apply -f test/testdata/widget-crd.yaml
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
      syncResources:
        - apiVersion: example.com/v1
          kind: Widget
          direction: toHost
          selector:
            matchLabels:
              widgets.kupecloud.io/sync: "true"
```

## Example Widget

```yaml
apiVersion: example.com/v1
kind: Widget
metadata:
  name: blue-widget
  namespace: default
  labels:
    widgets.kupecloud.io/sync: "true"
spec:
  model: "sparkle-9000"
  color: "blue"
  size: "pocket"
  features:
    - "whisper-mode"
  owner:
    name: "platform"
    team: "core"
```

## What gets synced

Only Widgets with `widgets.kupecloud.io/sync: "true"` are eligible. All other Widgets are ignored.
