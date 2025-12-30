//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestWidgetSyncToHost(t *testing.T) {
	feature := features.New("widget-sync-tohost").
		Assess("widget created in vcluster is synced to host", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			hostDyn, err := hostDynamicClient()
			if err != nil {
				t.Fatalf("create host dynamic client: %v", err)
			}
			vclusterDyn, vclusterKubeconfig, cleanup, err := vclusterDynamicClient(ctx)
			if err != nil {
				t.Fatalf("get vcluster dynamic client: %v", err)
			}
			defer cleanup()

			hostNamespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")

			if !keepTestResources() {
				// Cleanup: best-effort delete of test resources
				defer func() {
					cleanupCtx := context.Background()
					// Delete the widget from vcluster (host widget should be cleaned up automatically)
					_ = vclusterDyn.Resource(widgetGVR).Namespace("default").Delete(cleanupCtx, "my-widget", metav1.DeleteOptions{})
					// Also try to clean up any orphaned host widget
					_ = hostDyn.Resource(widgetGVR).Namespace(hostNamespace).Delete(cleanupCtx, hostWidgetName(), metav1.DeleteOptions{})
				}()
			}

			if err := applyWidgetCRD(ctx); err != nil {
				t.Fatalf("apply widget CRD to host: %v", err)
			}

			if err := applyWidgetToVCluster(ctx, vclusterKubeconfig); err == nil {
				if err := waitForVClusterWidget(ctx, vclusterDyn); err != nil {
					t.Fatalf("wait for vcluster widget: %v", err)
				}
			} else {
				t.Fatalf("apply widget to vcluster: %v", err)
			}

			if err := waitForHostWidget(ctx, hostDyn); err != nil {
				t.Fatalf("wait for host widget: %v", err)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

func TestWidgetDeletePropagation(t *testing.T) {
	// Test that deleting a widget from vcluster removes it from the host.
	// This verifies the delete propagation works correctly.
	feature := features.New("widget-delete-propagation").
		Assess("widget deleted from vcluster is removed from host", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			hostDyn, err := hostDynamicClient()
			if err != nil {
				t.Fatalf("create host dynamic client: %v", err)
			}
			vclusterDyn, vclusterKubeconfig, cleanup, err := vclusterDynamicClient(ctx)
			if err != nil {
				t.Fatalf("get vcluster dynamic client: %v", err)
			}
			defer cleanup()

			hostNamespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
			hostName := hostWidgetName()

			// Setup: ensure CRD and widget exist
			if err := applyWidgetCRD(ctx); err != nil {
				t.Fatalf("apply widget CRD to host: %v", err)
			}
			if err := applyWidgetToVCluster(ctx, vclusterKubeconfig); err != nil {
				t.Fatalf("apply widget to vcluster: %v", err)
			}
			if err := waitForVClusterWidget(ctx, vclusterDyn); err != nil {
				t.Fatalf("wait for vcluster widget: %v", err)
			}
			if err := waitForHostWidget(ctx, hostDyn); err != nil {
				t.Fatalf("wait for host widget: %v", err)
			}

			// Verify host widget exists before deletion
			_, err = hostDyn.Resource(widgetGVR).Namespace(hostNamespace).Get(ctx, hostName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("host widget should exist before deletion: %v", err)
			}

			// Delete the widget from vcluster
			if err := vclusterDyn.Resource(widgetGVR).Namespace("default").Delete(ctx, "my-widget", metav1.DeleteOptions{}); err != nil {
				t.Fatalf("delete widget from vcluster: %v", err)
			}

			// Wait for the host widget to be deleted
			err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
				_, err := hostDyn.Resource(widgetGVR).Namespace(hostNamespace).Get(ctx, hostName, metav1.GetOptions{})
				if errors.IsNotFound(err) {
					return true, nil // Widget is gone, success!
				}
				if err != nil {
					return false, err
				}
				return false, nil // Widget still exists, keep polling
			})
			if err != nil {
				t.Fatalf("host widget not deleted after vcluster widget deletion: %v", err)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
