---
title: E2E Testing
description: End-to-end suite with kind, vcluster Helm install, and sync assertions.
---

The E2E suite validates sync behavior against a real API server. It provisions a Kind cluster, installs vcluster via Helm, loads the plugin image, and runs sync tests.

## Quick start

```bash
# Create kind + install vcluster + run tests
make e2e
```

```bash
# Use an existing cluster
USE_EXISTING_CLUSTER=true go test -tags=e2e ./test/e2e -v
```

## Environment variables

### Cluster lifecycle

- `USE_EXISTING_CLUSTER` (bool): use existing `KUBECONFIG` instead of creating kind.
- `KIND_CLUSTER_NAME` (string): kind cluster name (default `vcluster-generic-sync-e2e`).
- `KIND_NODE_IMAGE` (string): optional kind node image override.
- `KUBECONFIG` (string): kubeconfig path for host cluster access.
- `E2E_KEEP_CLUSTER` (bool): keep kind cluster after tests.
- `E2E_KUBECONFIG_OUT` (string): write host kubeconfig path to this file.
- `E2E_KEEP_RESOURCES` (bool): skip test resource cleanup.

### Image build and install

- `E2E_BUILD_IMAGE` (bool): build the plugin image (defaults to true when kind is created).
- `E2E_IMAGE_TAG` (string): image tag for the plugin image (default `vcluster-generic-sync-plugin:e2e`).
- `E2E_INSTALL_VCLUSTER` (bool): install vcluster via Helm (defaults to true when kind is created).
- `E2E_LOG_CMD_OUTPUT` (bool): stream docker/kind/helm output to stdout.
- `E2E_VCLUSTER_HELM_REPO` (string): Helm repo (default `https://charts.loft.sh`).
- `E2E_VCLUSTER_VERSION` (string): vcluster chart version (default `v0.30.4`).
- `E2E_VCLUSTER_INSTALL_TIMEOUT` (duration): Helm install + readiness timeout (default `15m`).

### Vcluster access and log checks

- `E2E_VCLUSTER_NAMESPACE` (string): host namespace for vcluster (default `vcluster`).
- `E2E_VCLUSTER_NAME` (string): Helm release name (default `vcluster`).
- `E2E_VCLUSTER_LABEL_SELECTOR` (string): label selector for vcluster pod lookup (default `app=vcluster`).
- `E2E_VCLUSTER_CONTAINER` (string): container name for plugin logs (default `syncer`).
- `E2E_VCLUSTER_READY_TIMEOUT` (duration): wait for vcluster pod Ready (default `10m`).
- `E2E_VCLUSTER_STATUS_INTERVAL` (duration): status log interval (default `30s`).
- `E2E_PLUGIN_LOG_START` (string): startup log token (default `Plugin starting`).
- `E2E_PLUGIN_LOG_READY` (string): ready log token (default `All syncers registered successfully`).
- `E2E_VCLUSTER_KUBECONFIG` (string): optional explicit vcluster kubeconfig path.
- `E2E_VCLUSTER_SERVER` (string): override vcluster kubeconfig server (used with port-forward).
- `E2E_GATEWAY_HOST_NAMESPACE` (string): host namespace used by Gateway fromHost sync.

## Test coverage

The E2E suite covers the following scenarios:

### Plugin lifecycle

| Test | Description |
|------|-------------|
| `TestPluginStartup` | Verifies plugin starts and logs expected messages |
| `TestVClusterReady` | Validates vcluster API is accessible |

### Widget CRD (custom resource sync)

| Test | Description |
|------|-------------|
| `TestWidgetSyncToHost` | Widget created in vcluster syncs to host |
| `TestWidgetDeletePropagation` | Deleting widget in vcluster removes it from host |
| `TestWidgetStatusSync` | Host status updates propagate to vcluster |

### Status subresource detection

| Test | Description |
|------|-------------|
| `TestStatusSubresourceDetection` | Verifies status sync is disabled for resources without status subresource (ConfigMap) |
| `TestWidgetStatusSubresourceEnabled` | Verifies status sync is enabled for CRDs with status subresource (Widget) |

### Core resources

| Test | Description |
|------|-------------|
| `TestSecretSyncToHost` | Secret with sync label in vcluster syncs to host |
| `TestConfigMapSyncFromHost` | ConfigMap with sync label on host syncs to vcluster |

### Gateway API

| Test | Description |
|------|-------------|
| `TestGatewayClassSyncFromHost` | GatewayClass on host syncs to vcluster |
| `TestGatewaySyncFromHost` | Gateway on host syncs to vcluster |
| `TestHTTPRouteSyncToHost` | HTTPRoute in vcluster syncs to host |

## Test fixtures

Test fixtures are located in `test/testdata/`:

- `widget-crd.yaml` - Widget CRD with status subresource
- `widget.yaml` - Example Widget resource for sync tests

Gateway API CRDs are installed directly from the official repo:

```
kubectl apply -k github.com/kubernetes-sigs/gateway-api/config/crd?ref=v1.4.1
```

## Adding new tests

1. Create a new test file in `test/e2e/` with the `//go:build e2e` tag
2. Use the e2e-framework features pattern:
   ```go
   func TestMyFeature(t *testing.T) {
       feature := features.New("my-feature").
           Assess("description", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
               // Test logic here
               return ctx
           }).
           Feature()
       testEnv.Test(t, feature)
   }
   ```
3. Use helper functions from `helpers.go` for common operations
4. Clean up test resources unless `E2E_KEEP_RESOURCES=true`
