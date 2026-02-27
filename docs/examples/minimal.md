---
title: Minimal Plugin Install
description: Install the plugin with no syncers enabled.
---

This is the smallest possible configuration that runs the plugin but does not sync any resources. Use it to verify image pull, startup logs, and RBAC wiring.

## vcluster.yaml

```yaml
plugin:
  generic-sync:
    image: ghcr.io/kupecloud/vcluster-generic-sync-plugin:latest
    imagePullPolicy: IfNotPresent
    config:
      version: v1
      log_level: info
      syncResources: []
```

## Behavior

| Setting | Result |
| --- | --- |
| `syncResources: []` | No syncers are registered, so nothing is synced. |
| `log_level: info` | Standard startup logs only. |

## Notes

- No additional RBAC is required because no resources are synced.
- Use this as a starting point to confirm the plugin runs before adding syncers.
