---
title: Base Plugin Values
description: Baseline Helm values for installing the plugin, with the RBAC shape explained.
---

Use this baseline as the starting point for all examples. It is a valid Helm values fragment that you can merge into your vcluster values file. The `config` block from each example should be added under `plugin.generic-sync.config`.

```yaml
plugin:
  generic-sync:
    version: v2
    image: ghcr.io/kupecloud/vcluster-generic-sync-plugin:latest # pin a release in production; release tags are unprefixed, e.g. 1.5.0
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

The two `version` fields are unrelated. `plugin.generic-sync.version: v2` is vCluster's plugin format version. `config.version: v1` (set in each example's `config` block) is this plugin's own configuration schema version.

## What each RBAC rule is for

The `rbac` blocks are standard vCluster chart values, not plugin configuration. The chart folds every `extraRules` entry into the Role and ClusterRole it creates for the syncer pod, which is where the plugin runs.

- `plugin.generic-sync.rbac.role.extraRules` is added to the namespaced Role in the vCluster's host namespace. `toHost` objects are created there by default, so each kind synced in that direction needs the full write set: `create`, `delete`, `patch`, `update`, `get`, `list`, `watch`.
- `plugin.generic-sync.rbac.clusterRole.extraRules` is added to the chart's ClusterRole. Two things belong here: read access to `customresourcedefinitions`, which the plugin requires to discover the synced kinds and their status subresources, and any cluster-scoped kinds you sync. A read-only `mirror` of a cluster-scoped kind, such as `GatewayClass`, needs only `get`, `list`, `watch`.
- The top-level `rbac.clusterRole.enabled: true` makes the chart render the ClusterRole. The chart's default `auto` mode also detects plugin `extraRules`; setting `true` keeps the requirement explicit.

A `hostNamespace` override points a `toHost` syncer at a namespace the Role above does not cover. Grant that access with a dedicated Role and RoleBinding in the target namespace rather than widening the ClusterRole: a cluster-wide grant on `secrets`, for example, would let every vCluster's plugin read every other vCluster's secrets.

Scope every rule to the exact kinds and verbs you sync. A `*` grant is easier to write and much harder to audit.
