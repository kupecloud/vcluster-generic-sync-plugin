---
title: Metrics
description: Prometheus metrics exposed by the plugin.
---

The plugin runs as a separate process from the vcluster syncer, so its metrics are **not** exposed on the syncer's metrics port. The plugin serves its own registry (including all `generic_sync_*` metrics) on port **8082** of the vcluster pod, with two endpoints:

- `/metrics` — Prometheus metrics
- `/healthz` — liveness endpoint (returns `ok`), so a failed metrics server is detectable

## Accessing metrics

```bash
kubectl port-forward -n <namespace> <vcluster-pod> 8082:8082
curl http://localhost:8082/metrics | grep generic_sync_
```

Metrics are always enabled when the plugin is loaded; a metrics-server bind failure is fatal and the plugin exits so it can be restarted rather than run without metrics.

`events_emitted_total` is recorded only when the plugin actually emits a Kubernetes event. If `events_enabled` is set to `false`, no events are emitted and the metric does not increment.

## Metric reference

All metrics are prefixed with `generic_sync_`.

| Metric | Type | Labels | Description |
| --- | --- | --- | --- |
| `operations_total` | Counter | `direction`, `kind`, `operation`, `status` | Total sync operations. |
| `operation_duration_seconds` | Histogram | `direction`, `kind`, `operation` | Sync operation latency. |
| `errors_total` | Counter | `direction`, `kind`, `error_type` | Sync errors by class. |
| `resources_managed` | Gauge | `direction`, `kind` | Currently managed resources. Maintained by inc/dec and reset on restart; clamped at zero. |
| `last_successful_sync_timestamp_seconds` | Gauge | `direction`, `kind` | Unix time of the last successful (or no-op) reconcile. Freshness signal. |
| `reconcile_total` | Counter | `direction`, `kind` | Reconcile attempts. |
| `reconcile_duration_seconds` | Histogram | `direction`, `kind` | Reconcile duration. |
| `syncer_info` | Gauge | `direction`, `kind`, `api_version`, `mode`, `status_sync` | Registered syncers (value is 1). |
| `namespace_filtered_total` | Counter | `direction`, `kind` | Filtered by namespace rules. (No `namespace` label — namespace names are controlled from inside the vCluster and would be unbounded cardinality.) |
| `selector_filtered_total` | Counter | `direction`, `kind` | Filtered by selector rules. |
| `ownership_conflicts_total` | Counter | `direction`, `kind` | `fromHost` imports refused because the target object was not created by the syncer from that host object. See [Conflicts](../configuration/sync-resources.md#conflicts-fromhost). |
| `patch_applied_total` | Counter | `direction`, `kind`, `patch_type` | Patch applications. |
| `events_emitted_total` | Counter | `direction`, `kind`, `event_type`, `reason` | Kubernetes events emitted. |
| `plugin_info` | Gauge | `version`, `git_commit`, `build_date` | Build metadata (value is 1). |
| `config_reloads_total` | Counter | `status` | Config reload results. |

### Label values

- `direction`: `toHost`, `fromHost`
- `operation`: `create`, `update`, `delete`
- `status`: `success`, `error`, `skipped`
- `error_type`: `conflict`, `not_found`, `validation`, `forbidden`, `transient`, `unknown`

## Example alerts

```yaml
groups:
  - name: generic-sync
    rules:
      # A syncer that has not had a successful reconcile in 15m while it has
      # registered syncers is likely wedged (distinguishes "dead" from "idle").
      - alert: GenericSyncStale
        expr: |
          (time() - generic_sync_last_successful_sync_timestamp_seconds) > 900
        for: 15m
        labels:
          severity: warning
      # Sustained no-op hot loop (kine-flood class): skips climbing fast.
      - alert: GenericSyncHotLoop
        expr: |
          rate(generic_sync_operations_total{status="skipped"}[5m]) > 5
        for: 10m
        labels:
          severity: warning
```

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
