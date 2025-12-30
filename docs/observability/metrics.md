---
title: Metrics
description: Prometheus metrics exposed by the plugin.
---

The plugin registers Prometheus metrics with the controller-runtime registry, so they are exposed on the **same `/metrics` endpoint** as vcluster (default port **8080**).

## Accessing metrics

```bash
kubectl port-forward -n <namespace> <vcluster-pod> 8080:8080
curl http://localhost:8080/metrics | grep generic_sync_
```

Metrics are always enabled when the plugin is loaded.

`events_emitted_total` is recorded only when the plugin actually emits a Kubernetes event. If `events_enabled` is set to `false`, no events are emitted and the metric does not increment.

## Metric reference

All metrics are prefixed with `generic_sync_`.

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `operations_total` | Counter | `direction`, `kind`, `operation`, `status` | Total sync operations. |
| `operation_duration_seconds` | Histogram | `direction`, `kind`, `operation` | Sync operation latency. |
| `errors_total` | Counter | `direction`, `kind`, `error_type` | Sync errors by class. |
| `resources_managed` | Gauge | `direction`, `kind` | Currently managed resources. |
| `reconcile_total` | Counter | `direction`, `kind` | Reconcile attempts. |
| `reconcile_duration_seconds` | Histogram | `direction`, `kind` | Reconcile duration. |
| `syncer_info` | Gauge | `direction`, `kind`, `api_version`, `mode`, `status_sync` | Registered syncers (value is 1). |
| `namespace_filtered_total` | Counter | `direction`, `kind`, `namespace` | Filtered by namespace rules. |
| `selector_filtered_total` | Counter | `direction`, `kind` | Filtered by selector rules. |
| `patch_applied_total` | Counter | `direction`, `kind`, `patch_type` | Patch applications. |
| `events_emitted_total` | Counter | `direction`, `kind`, `event_type`, `reason` | Kubernetes events emitted. |
| `plugin_info` | Gauge | `version`, `git_commit`, `build_date` | Build metadata (value is 1). |
| `config_reloads_total` | Counter | `status` | Config reload results. |

### Label values

- `direction`: `toHost`, `fromHost`
- `operation`: `create`, `update`, `delete`, `sync`
- `status`: `success`, `error`, `skipped`
- `error_type`: `conflict`, `not_found`, `validation`, `timeout`, `unknown`

## Related configuration

```yaml
version: v1
log_level: info
# Emits Kubernetes Events (tracked by events_emitted_total)
events_enabled: true
syncResources:
  - apiVersion: v1
    kind: Secret
    direction: toHost
```
