---
title: Trace Logging
description: Trace-level logging for full object visibility during sync.
---

The plugin supports **trace-level logging** (not distributed tracing). When enabled, it logs full JSON content for objects at key points in the sync pipeline.

## Enable trace logging

```yaml
version: v1
log_level: trace
syncResources:
  - apiVersion: v1
    kind: Secret
    direction: toHost
```

### Log levels

| Level | Description |
| --- | --- |
| `error` | Only errors and fatal messages |
| `warning` | Warnings and errors |
| `info` | Standard operational logging (default) |
| `debug` | Verbose debugging information |
| `trace` | Full object content logging |

Trace includes all lower levels.

## What gets logged

- Incoming source objects
- Outgoing target objects
- Before/after diffs for updates
- Patched object output
- Operation results (success or error)

## Output format

Trace logs are standard klog logs with a `[TRACE]` prefix:

```
I1228 15:30:45.123456  12345 trace.go:33] "[TRACE] Incoming object" direction="toHost" kind="Secret" operation="create" name="my-secret" namespace="default" resourceVersion="12345" generation=1 content="{...}"
```

## Security considerations

**Warning:** Trace logging serializes full object content, including sensitive data such as:
- Secret `data` and `stringData` fields
- ConfigMap values that may contain sensitive configuration
- Any sensitive fields in custom resources

Only enable trace logging in secure environments during debugging. Avoid trace logging in production or ensure logs are not exposed to unauthorized users.

## Performance considerations

Trace logging is expensive because it serializes full objects. Only enable it when debugging specific issues.

## Distributed tracing

There is **no OpenTelemetry tracer** wired into the plugin today. Trace output goes to normal logs (stdout/stderr). If you want OTLP traces, the plugin will need additional instrumentation and exporter configuration.
