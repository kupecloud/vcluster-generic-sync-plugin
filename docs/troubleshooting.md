---
title: Troubleshooting
description: Common issues and solutions for the vcluster-generic-sync-plugin.
---

# Troubleshooting Guide

This guide covers common issues and their solutions when using the vcluster-generic-sync-plugin.

## Enable Debug Logging

The first step in troubleshooting is to enable debug or trace logging:

```yaml
version: v1
log_level: debug  # Options: error, warning, info, debug, trace
syncResources:
  # ...
```

- **debug**: Shows detailed sync operations and decisions
- **trace**: Shows full object content (use sparingly, very verbose)

View logs with:
```bash
kubectl logs -n <vcluster-namespace> <vcluster-pod> -c syncer | grep -i "generic-sync"
```

## Common Issues

### Resource Not Syncing

**Symptoms:** A resource exists in the source cluster but doesn't appear in the target.

**Possible causes:**

1. **Namespace filtering**: Check if the namespace is excluded
   ```bash
   kubectl logs ... | grep -i "filtered"
   ```

2. **Label selector mismatch**: If using `matchLabels`, verify the resource has matching labels
   ```yaml
   selector:
     matchLabels:
       sync: "true"  # Resource must have this label
   ```

3. **Direction mismatch**: Verify `direction` is correct
   - `toHost`: vCluster → Host cluster
   - `fromHost`: Host → vCluster

4. **CRD not installed**: For custom resources, ensure the CRD exists on both clusters

### Status Not Syncing

**Symptoms:** Resource syncs but status is empty or outdated.

**Check:**

1. **statusSync enabled?**
   ```yaml
   syncResources:
     - apiVersion: v1
       kind: Service
       direction: toHost
       statusSync: true  # Must be explicitly enabled
   ```

2. **Status subresource exists?** The resource must have a `/status` subresource. Check logs for:
   ```
   Status sync disabled because resource has no status subresource
   ```

3. **Not using mirror mode?** Status sync is disabled in mirror mode (read-only).

### Patch Application Failures

**Symptoms:** Events show "PatchFailed" or references are not translated.

**Debug steps:**

1. Enable trace logging to see full object content:
   ```yaml
   log_level: trace
   ```

2. Verify patch path exists in the resource:
   ```yaml
   patches:
     - path: spec.secretRef.name  # This path must exist in the resource
       type: rewriteName
   ```

3. Check for typos in path (paths are case-sensitive)

### "Already exists" Errors

**Symptoms:** Create operations fail with "already exists".

**Cause:** Resource with the translated name already exists on the host.

**Solutions:**

1. Delete the conflicting resource on the host
2. Check if another syncer or controller is managing the same resource
3. Verify the resource isn't manually created

### Conflict Errors

**Symptoms:** Frequent "conflict" errors in logs.

**Cause:** Multiple controllers modifying the same resource.

**Solutions:**

1. Reduce `max_concurrent_reconciles` to lower contention
2. Check if other controllers are modifying the synced resources
3. Consider adding more specific selectors to reduce scope

## Checking Metrics

The plugin exposes Prometheus metrics at the standard `/metrics` endpoint.

Key metrics to watch:

| Metric | Description |
|--------|-------------|
| `vcluster_generic_sync_operations_total` | Total sync operations by status |
| `vcluster_generic_sync_errors_total` | Errors by type |
| `vcluster_generic_sync_resources_managed` | Currently managed resources |
| `vcluster_generic_sync_reconcile_duration_seconds` | Reconciliation latency |

Example PromQL queries:

```promql
# Error rate by kind
rate(vcluster_generic_sync_errors_total[5m])

# Slow reconciliations
histogram_quantile(0.99, rate(vcluster_generic_sync_reconcile_duration_seconds_bucket[5m]))

# Resources being managed
vcluster_generic_sync_resources_managed
```

## Checking Events

Kubernetes events provide visibility into sync operations:

```bash
# View events for a specific resource
kubectl get events --field-selector involvedObject.name=<resource-name>

# View all sync-related events
kubectl get events | grep -E "(Synced|SyncFailed|Created|Updated|Deleted)"
```

Event reasons:
- `Created`: Resource successfully created
- `Updated`: Resource successfully updated
- `Deleted`: Resource deleted
- `SyncFailed`: General sync failure
- `PatchFailed`: Patch application failed
- `CreateFailed`/`UpdateFailed`/`DeleteFailed`: Specific operation failures

## Configuration Validation

The plugin validates configuration at startup. Common validation errors:

| Error | Solution |
|-------|----------|
| `version is required` | Add `version: v1` to config |
| `direction must be 'toHost' or 'fromHost'` | Fix typo in direction |
| `invalid glob pattern` | Check namespace pattern syntax |
| `duplicate syncResource` | Remove duplicate resource definitions |
| `path cannot start with '.'` | Fix patch path syntax |

## Performance Tuning

If experiencing performance issues:

1. **Reduce reconciliation scope:**
   ```yaml
   globalFilters:
     exclude:
       - namespace: "kube-*"
       - namespace: "*-system"
   ```

2. **Adjust concurrency:**
   ```yaml
   max_concurrent_reconciles: 5  # Lower for less load, higher for throughput
   ```

3. **Enable event filtering** (default):
   ```yaml
   disable_event_filtering: false
   ```

4. **Use specific selectors:**
   ```yaml
   selector:
     matchLabels:
       sync-enabled: "true"
   ```

## Getting Help

If issues persist:

1. Check the [GitHub issues](https://github.com/kupecloud/vcluster-generic-sync-plugin/issues)
2. Collect debug logs and metrics
3. Include your configuration (sanitized) when reporting issues
