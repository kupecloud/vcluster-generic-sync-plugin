//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/loft-sh/vcluster/pkg/util/translate"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const (
	e2eHostServiceName     = "e2e-src-7f3a9c"
	e2eTargetServiceName   = "e2e-target-svc"
	e2eNativeControlSvc    = "e2e-native-control"
	e2eNativeIgnoredWindow = 30 * time.Second
)

// TestServiceFromHostIsIgnoredByNativeSyncer imports a host Service under a
// kupe.cloud/target-name with virtualControlledBy on, and checks that vCluster's own
// services syncer leaves the stamped copy alone (no translated Service appears on the
// host). A Service created directly in the vcluster is the control: it must reach the
// host, which proves the native syncer is running.
func TestServiceFromHostIsIgnoredByNativeSyncer(t *testing.T) {
	feature := features.New("service-fromhost-controlled-by").
		Assess("stamped copy is not synced back to the host", func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
			hostClientset, err := buildClientsetFromEnv()
			if err != nil {
				t.Fatalf("build host clientset: %v", err)
			}
			vclusterClientset, cleanup, err := buildVClusterClientset(ctx)
			if err != nil {
				t.Fatalf("build vcluster clientset: %v", err)
			}
			defer cleanup()

			hostNamespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
			vclusterName := envOrDefault("E2E_VCLUSTER_NAME", "vcluster")

			if !keepTestResources() {
				defer func() {
					cleanupCtx := context.Background()
					_ = hostClientset.CoreV1().Services(hostNamespace).Delete(cleanupCtx, e2eHostServiceName, metav1.DeleteOptions{})
					_ = vclusterClientset.CoreV1().Services("default").Delete(cleanupCtx, e2eNativeControlSvc, metav1.DeleteOptions{})
				}()
			}

			hostSvc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					Name:        e2eHostServiceName,
					Namespace:   hostNamespace,
					Labels:      map[string]string{e2eSyncLabelKey: e2eSyncLabelValue},
					Annotations: map[string]string{"kupe.cloud/target-name": e2eTargetServiceName},
				},
				Spec: corev1.ServiceSpec{
					ClusterIP: corev1.ClusterIPNone,
					Ports:     []corev1.ServicePort{{Name: "tcp", Port: 8080}},
				},
			}
			if _, err := hostClientset.CoreV1().Services(hostNamespace).Create(ctx, hostSvc, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
				t.Fatalf("create host service: %v", err)
			}

			// The copy appears under the target name, stamped as plugin-owned.
			err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
				vSvc, err := vclusterClientset.CoreV1().Services("default").Get(ctx, e2eTargetServiceName, metav1.GetOptions{})
				if errors.IsNotFound(err) {
					return false, nil
				}
				if err != nil {
					return false, err
				}
				return vSvc.Labels[e2eControlledByLabelKey] == e2eControlledByLabelValue, nil
			})
			if err != nil {
				t.Fatalf("stamped service copy %s not found in the vcluster: %v", e2eTargetServiceName, err)
			}

			// Control: an ordinary virtual Service reaches the host through vCluster's syncer.
			control := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{Name: e2eNativeControlSvc, Namespace: "default"},
				Spec: corev1.ServiceSpec{
					ClusterIP: corev1.ClusterIPNone,
					Ports:     []corev1.ServicePort{{Name: "http", Port: 80}},
				},
			}
			if _, err := vclusterClientset.CoreV1().Services("default").Create(ctx, control, metav1.CreateOptions{}); err != nil && !errors.IsAlreadyExists(err) {
				t.Fatalf("create control service: %v", err)
			}
			controlHostName := translate.SingleNamespaceHostName(e2eNativeControlSvc, "default", vclusterName)
			if err := waitForHostServiceExists(ctx, hostClientset, hostNamespace, controlHostName, 2*time.Minute); err != nil {
				t.Fatalf("control service never reached the host as %s: %v", controlHostName, err)
			}

			// The stamped copy must not appear on the host, and keeps not appearing.
			copyHostName := translate.SingleNamespaceHostName(e2eTargetServiceName, "default", vclusterName)
			if err := assertHostServiceAbsentFor(ctx, hostClientset, hostNamespace, copyHostName, e2eNativeIgnoredWindow); err != nil {
				t.Fatalf("stamped copy must not be synced back to the host: %v", err)
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// waitForHostServiceExists polls until the named host Service exists. Any error other
// than NotFound ends the wait.
func waitForHostServiceExists(ctx context.Context, clientset kubernetes.Interface, namespace, name string, timeout time.Duration) error {
	err := wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		_, err := clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, fmt.Errorf("get service %s/%s: %w", namespace, name, err)
		}
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("wait for service %s/%s: %w", namespace, name, err)
	}
	return nil
}

// assertHostServiceAbsentFor checks that the named host Service does not exist at any
// point during window. It fails as soon as the Service appears or a Get fails with
// anything other than NotFound, so an unreachable API server never passes as "absent".
func assertHostServiceAbsentFor(ctx context.Context, clientset kubernetes.Interface, namespace, name string, window time.Duration) error {
	deadline := time.Now().Add(window)
	for {
		_, err := clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		switch {
		case err == nil:
			return fmt.Errorf("service %s/%s exists", namespace, name)
		case !errors.IsNotFound(err):
			return fmt.Errorf("get service %s/%s: %w", namespace, name, err)
		}
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("check service %s/%s absent: %w", namespace, name, ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
}
