//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestWidgetStatusSync(t *testing.T) {
	feature := features.New("widget-status-sync").
		Assess("host status updates sync to vcluster", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			hostDyn, err := hostDynamicClient()
			if err != nil {
				t.Fatalf("create host dynamic client: %v", err)
			}
			vclusterDyn, vclusterKubeconfig, cleanup, err := vclusterDynamicClient(ctx)
			if err != nil {
				t.Fatalf("get vcluster dynamic client: %v", err)
			}
			defer cleanup()

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
			hostName := hostWidgetName()

			if !keepTestResources() {
				defer func() {
					cleanupCtx := context.Background()
					_ = vclusterDyn.Resource(widgetGVR).Namespace("default").Delete(cleanupCtx, "my-widget", metav1.DeleteOptions{})
					_ = hostDyn.Resource(widgetGVR).Namespace(hostNamespace).Delete(cleanupCtx, hostName, metav1.DeleteOptions{})
				}()
			}

			hostObj, err := hostDyn.Resource(widgetGVR).Namespace(hostNamespace).Get(ctx, hostName, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get host widget: %v", err)
			}

			status := map[string]interface{}{
				"state":        "serviced",
				"serialNumber": "SN-12345",
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
				if state != "serviced" {
					return false, nil
				}
				serial, _, _ := unstructured.NestedString(vObj.Object, "status", "serialNumber")
				if serial != "SN-12345" {
					return false, nil
				}
				return true, nil
			})
			if err != nil {
				t.Fatalf("status not synced to vcluster: %v", err)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
