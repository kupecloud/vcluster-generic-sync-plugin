//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	"github.com/loft-sh/vcluster/pkg/util/translate"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
)

const (
	e2eGatewayClassName = "e2e-gatewayclass"
	e2eGatewayName      = "e2e-gateway"
	e2eHTTPRouteName    = "e2e-httproute"
)

func TestGatewayClassSyncFromHost(t *testing.T) {
	feature := features.New("gatewayclass-sync-fromhost").
		Assess("host GatewayClass is synced to vcluster", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			hostDyn, err := hostDynamicClient()
			if err != nil {
				t.Fatalf("create host dynamic client: %v", err)
			}
			vclusterDyn, _, cleanup, err := vclusterDynamicClient(ctx)
			if err != nil {
				t.Fatalf("create vcluster dynamic client: %v", err)
			}
			defer cleanup()

			if !keepTestResources() {
				defer func() {
					cleanupCtx := context.Background()
					_ = hostDyn.Resource(gatewayClassGVR).Delete(cleanupCtx, e2eGatewayClassName, metav1.DeleteOptions{})
					_ = vclusterDyn.Resource(gatewayClassGVR).Delete(cleanupCtx, e2eGatewayClassName, metav1.DeleteOptions{})
				}()
			}

			if err := upsertGatewayClass(ctx, hostDyn); err != nil {
				t.Fatalf("create host gatewayclass: %v", err)
			}

			if err := waitForGatewayClass(ctx, vclusterDyn); err != nil {
				t.Fatalf("gatewayclass not synced to vcluster: %v", err)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

func TestGatewaySyncFromHost(t *testing.T) {
	feature := features.New("gateway-sync-fromhost").
		Assess("host Gateway is synced to vcluster", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			hostDyn, err := hostDynamicClient()
			if err != nil {
				t.Fatalf("create host dynamic client: %v", err)
			}
			vclusterDyn, _, cleanup, err := vclusterDynamicClient(ctx)
			if err != nil {
				t.Fatalf("create vcluster dynamic client: %v", err)
			}
			defer cleanup()

			if !keepTestResources() {
				defer func() {
					cleanupCtx := context.Background()
					hostNamespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
					_ = hostDyn.Resource(gatewayGVR).Namespace(hostNamespace).Delete(cleanupCtx, e2eGatewayName, metav1.DeleteOptions{})
					_ = vclusterDyn.Resource(gatewayGVR).Namespace("default").Delete(cleanupCtx, e2eGatewayName, metav1.DeleteOptions{})
				}()
			}

			if err := upsertGatewayClass(ctx, hostDyn); err != nil {
				t.Fatalf("create host gatewayclass: %v", err)
			}
			if err := upsertGateway(ctx, hostDyn); err != nil {
				t.Fatalf("create host gateway: %v", err)
			}

			if err := waitForGateway(ctx, vclusterDyn); err != nil {
				t.Fatalf("gateway not synced to vcluster: %v", err)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

func TestHTTPRouteSyncToHost(t *testing.T) {
	feature := features.New("httproute-sync-tohost").
		Assess("vcluster HTTPRoute is synced to host", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			hostDyn, err := hostDynamicClient()
			if err != nil {
				t.Fatalf("create host dynamic client: %v", err)
			}
			vclusterDyn, _, cleanup, err := vclusterDynamicClient(ctx)
			if err != nil {
				t.Fatalf("create vcluster dynamic client: %v", err)
			}
			defer cleanup()

			if !keepTestResources() {
				defer func() {
					cleanupCtx := context.Background()
					hostNamespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
					_ = vclusterDyn.Resource(httpRouteGVR).Namespace("default").Delete(cleanupCtx, e2eHTTPRouteName, metav1.DeleteOptions{})
					_ = hostDyn.Resource(httpRouteGVR).Namespace(hostNamespace).Delete(cleanupCtx, hostHTTPRouteName(), metav1.DeleteOptions{})
				}()
			}

			if err := upsertGatewayClass(ctx, hostDyn); err != nil {
				t.Fatalf("create host gatewayclass: %v", err)
			}
			if err := upsertGateway(ctx, hostDyn); err != nil {
				t.Fatalf("create host gateway: %v", err)
			}
			if err := waitForGateway(ctx, vclusterDyn); err != nil {
				t.Fatalf("gateway not synced to vcluster: %v", err)
			}

			if err := upsertHTTPRoute(ctx, vclusterDyn); err != nil {
				t.Fatalf("create vcluster httproute: %v", err)
			}
			if err := waitForHTTPRoute(ctx, hostDyn); err != nil {
				t.Fatalf("httproute not synced to host: %v", err)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

func upsertGatewayClass(ctx context.Context, dyn dynamic.Interface) error {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "GatewayClass",
			"metadata": map[string]interface{}{
				"name":   e2eGatewayClassName,
				"labels": e2eLabels(),
			},
			"spec": map[string]interface{}{
				"controllerName": "kupe.cloud/controller",
			},
		},
	}
	return upsertUnstructured(ctx, dyn, gatewayClassGVR, obj)
}

func upsertGateway(ctx context.Context, dyn dynamic.Interface) error {
	hostNamespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "Gateway",
			"metadata": map[string]interface{}{
				"name":      e2eGatewayName,
				"namespace": hostNamespace,
				"labels":    e2eLabels(),
			},
			"spec": map[string]interface{}{
				"gatewayClassName": e2eGatewayClassName,
				"listeners": []interface{}{
					map[string]interface{}{
						"name":     "http",
						"protocol": "HTTP",
						"port":     int64(80),
					},
				},
			},
		},
	}
	return upsertUnstructured(ctx, dyn, gatewayGVR, obj)
}

func upsertHTTPRoute(ctx context.Context, dyn dynamic.Interface) error {
	obj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "gateway.networking.k8s.io/v1",
			"kind":       "HTTPRoute",
			"metadata": map[string]interface{}{
				"name":      e2eHTTPRouteName,
				"namespace": "default",
				"labels":    e2eLabels(),
			},
			"spec": map[string]interface{}{
				"parentRefs": []interface{}{
					map[string]interface{}{
						"name": e2eGatewayName,
					},
				},
				"rules": []interface{}{
					map[string]interface{}{
						"backendRefs": []interface{}{
							map[string]interface{}{
								"name": "example-backend",
								"port": int64(80),
							},
						},
					},
				},
			},
		},
	}
	return upsertUnstructured(ctx, dyn, httpRouteGVR, obj)
}

func upsertUnstructured(ctx context.Context, dyn dynamic.Interface, gvr schema.GroupVersionResource, obj *unstructured.Unstructured) error {
	if obj == nil {
		return nil
	}

	resource := dyn.Resource(gvr)
	var namespaced dynamic.ResourceInterface = resource
	if ns := obj.GetNamespace(); ns != "" {
		namespaced = resource.Namespace(ns)
	}

	_, err := namespaced.Create(ctx, obj, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		return err
	}

	current, err := namespaced.Get(ctx, obj.GetName(), metav1.GetOptions{})
	if err != nil {
		return err
	}
	obj.SetResourceVersion(current.GetResourceVersion())
	_, err = namespaced.Update(ctx, obj, metav1.UpdateOptions{})
	return err
}

func waitForGatewayClass(ctx context.Context, dyn dynamic.Interface) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		_, err := dyn.Resource(gatewayClassGVR).Get(ctx, e2eGatewayClassName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		return err == nil, err
	})
}

func waitForGateway(ctx context.Context, dyn dynamic.Interface) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		_, err := dyn.Resource(gatewayGVR).Namespace("default").Get(ctx, e2eGatewayName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		return err == nil, err
	})
}

func waitForHTTPRoute(ctx context.Context, dyn dynamic.Interface) error {
	hostNamespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
	hostName := hostHTTPRouteName()
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		_, err := dyn.Resource(httpRouteGVR).Namespace(hostNamespace).Get(ctx, hostName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		return err == nil, err
	})
}

func hostHTTPRouteName() string {
	return translate.SingleNamespaceHostName(e2eHTTPRouteName, "default", envOrDefault("E2E_VCLUSTER_NAME", "vcluster"))
}
