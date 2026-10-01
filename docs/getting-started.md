---
title: Getting Started
description: Install the plugin, configure it, and sync your first resource.
---

This guide shows how to install and configure the vcluster Generic Sync Plugin and sync a sample CRD.

## Prerequisites

- A Kubernetes cluster with vcluster installed
- `kubectl` and `vcluster` CLI

## Install the plugin

Add the plugin to your vcluster values file (or `vcluster.yaml`). The `config` block is passed directly to the plugin. If your chart uses `plugins:` instead of `plugin:`, adjust the key accordingly. Pin the latest [release](https://github.com/kupecloud/vcluster-generic-sync-plugin/releases) tag rather than using `latest`; release tags are unprefixed, for example `1.5.0`.

```yaml
plugin:
  generic-sync:
    version: v2
    image: ghcr.io/kupecloud/vcluster-generic-sync-plugin:1.5.0 # replace with the latest release tag (unprefixed)
    imagePullPolicy: IfNotPresent
    rbac:
      role:
        extraRules:
          # Add permissions for your CRDs
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
          mode: sync
```

Create the vcluster:

```bash
vcluster create my-vcluster -f vcluster.yaml
```

## Quick example: sync a Widget CRD

The sample manifests live in this repository — clone it first:

```bash
git clone https://github.com/kupecloud/vcluster-generic-sync-plugin.git
cd vcluster-generic-sync-plugin
```

### 1. Ensure the CRD exists on the host

For `toHost` syncers, the CRD must exist on the host cluster.

```bash
kubectl apply -f test/testdata/widget-crd.yaml
```

### 2. Create a Widget in vcluster

```bash
vcluster connect my-vcluster -- kubectl apply -f test/testdata/widget.yaml
```

### 3. Verify sync to host

```bash
kubectl get widgets -n vcluster-my-vcluster

# NAME
# my-widget-x-default-x-my-vcluster
```

## Next steps

- [Configuration Overview](configuration/index.md)
- [Sync Resources](configuration/sync-resources.md)
- [Selectors and Namespace Filters](configuration/selectors-namespaces.md)
- [Patches](configuration/patches.md)
- [Troubleshooting](troubleshooting.md)
