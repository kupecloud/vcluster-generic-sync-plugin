//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestStatusSubresourceDetection(t *testing.T) {
	feature := features.New("status-subresource-detection").
		Assess("status sync disabled for resources without status subresource", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// The plugin now checks both host and virtual clusters for status subresource.
			// ConfigMap has statusSync: true but no status subresource, so we expect the warning.
			// Look for either host or virtual missing status - both indicate detection is working.
			if err := waitForAnyPluginLogToken(ctx, []string{
				"Status sync disabled because host resource has no status subresource",
				"Status sync disabled because virtual resource has no status subresource",
			}); err != nil {
				t.Fatalf("status subresource detection log not found: %v", err)
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

func TestWidgetStatusSubresourceEnabled(t *testing.T) {
	// Widget CRD has status subresource defined, so status sync should be enabled.
	// This tests that the discovery-based detection correctly identifies resources WITH status.
	feature := features.New("widget-status-subresource-enabled").
		Assess("Widget status sync is enabled when CRD has status subresource", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			// Wait for Widget syncer to be registered
			if err := waitForPluginLogTokens(ctx, []string{
				"All syncers registered successfully",
			}); err != nil {
				t.Fatalf("plugin not ready: %v", err)
			}

			// Verify we can do status updates on Widget (which proves status sync is enabled)
			hostDyn, err := hostDynamicClient()
			if err != nil {
				t.Fatalf("create host dynamic client: %v", err)
			}
			vclusterDyn, vclusterKubeconfig, cleanup, err := vclusterDynamicClient(ctx)
			if err != nil {
				t.Fatalf("get vcluster dynamic client: %v", err)
			}
			defer cleanup()

			// Ensure Widget CRD exists on both clusters
			if err := applyWidgetCRD(ctx); err != nil {
				t.Fatalf("apply widget CRD to host: %v", err)
			}
			if err := applyWidgetToVCluster(ctx, vclusterKubeconfig); err != nil {
				t.Fatalf("apply widget to vcluster: %v", err)
			}
			if err := waitForHostWidget(ctx, hostDyn); err != nil {
				t.Fatalf("wait for host widget: %v", err)
			}
			if err := waitForVClusterWidget(ctx, vclusterDyn); err != nil {
				t.Fatalf("wait for vcluster widget: %v", err)
			}

			hostNamespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")

			hostObj, err := hostDyn.Resource(widgetGVR).Namespace(hostNamespace).Get(ctx, hostWidgetName(), metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get host widget: %v", err)
			}

			status := map[string]interface{}{
				"state":        "status-enabled",
				"serialNumber": "SN-STATUS-OK",
				"lastServiced": time.Now().UTC().Format(time.RFC3339),
			}
			if err := unstructured.SetNestedMap(hostObj.Object, status, "status"); err != nil {
				t.Fatalf("set host status: %v", err)
			}
			if _, err := hostDyn.Resource(widgetGVR).Namespace(hostNamespace).UpdateStatus(ctx, hostObj, metav1.UpdateOptions{}); err != nil {
				t.Fatalf("update host status: %v", err)
			}

			err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
				vObj, err := vclusterDyn.Resource(widgetGVR).Namespace("default").Get(ctx, "my-widget", metav1.GetOptions{})
				if errors.IsNotFound(err) {
					return false, nil
				}
				if err != nil {
					return false, err
				}
				state, _, _ := unstructured.NestedString(vObj.Object, "status", "state")
				if state != "status-enabled" {
					return false, nil
				}
				serial, _, _ := unstructured.NestedString(vObj.Object, "status", "serialNumber")
				if serial != "SN-STATUS-OK" {
					return false, nil
				}
				return true, nil
			})
			if err != nil {
				t.Fatalf("status not synced to vcluster: %v", err)
			}

			// Clean up
			if !keepTestResources() {
				defer func() {
					cleanupCtx := context.Background()
					_ = vclusterDyn.Resource(widgetGVR).Namespace("default").Delete(cleanupCtx, "my-widget", metav1.DeleteOptions{})
					_ = hostDyn.Resource(widgetGVR).Namespace(hostNamespace).Delete(cleanupCtx, hostWidgetName(), metav1.DeleteOptions{})
				}()
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// waitForAnyPluginLogToken waits for ANY of the given tokens to appear in plugin logs.
// This is useful when multiple log messages could indicate success.
func waitForAnyPluginLogToken(ctx context.Context, tokens []string) error {
	clientset, err := buildClientsetFromEnv()
	if err != nil {
		return err
	}

	namespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
	labelSelector := envOrDefault("E2E_VCLUSTER_LABEL_SELECTOR", "app=vcluster")
	container := envOrDefault("E2E_VCLUSTER_CONTAINER", "syncer")
	explicitContainer := strings.TrimSpace(envOrDefault("E2E_VCLUSTER_CONTAINER", "")) != ""

	timeout := 3 * time.Minute
	pollInterval := 5 * time.Second
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil || len(pods.Items) == 0 {
			time.Sleep(pollInterval)
			continue
		}
		for _, pod := range pods.Items {
			containers := []string{container}
			if !explicitContainer {
				for _, c := range pod.Spec.Containers {
					if c.Name != container {
						containers = append(containers, c.Name)
					}
				}
			}
			for _, candidate := range containers {
				logs, err := fetchPodLogs(ctx, clientset, namespace, pod.Name, candidate)
				if err != nil {
					continue
				}
				// Check if ANY token is present
				for _, token := range tokens {
					if strings.Contains(logs, token) {
						return nil
					}
				}
			}
		}
		time.Sleep(pollInterval)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return fmt.Errorf("timeout waiting for any log token: %v", tokens)
}
