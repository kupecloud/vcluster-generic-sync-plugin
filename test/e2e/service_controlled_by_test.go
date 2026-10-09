//go:build e2e

package e2e

import (
	"context"
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
	e2eHostServiceName     = "e2e-mdb-7f3a9c"
	e2eTargetServiceName   = "e2e-orders-db"
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
					Ports:     []corev1.ServicePort{{Name: "postgres", Port: 5432}},
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
			if err := waitForHostService(ctx, hostClientset, hostNamespace, controlHostName, true, 2*time.Minute); err != nil {
				t.Fatalf("control service never reached the host as %s: %v", controlHostName, err)
			}

			// The stamped copy must not appear on the host, and keeps not appearing.
			copyHostName := translate.SingleNamespaceHostName(e2eTargetServiceName, "default", vclusterName)
			if err := waitForHostService(ctx, hostClientset, hostNamespace, copyHostName, true, e2eNativeIgnoredWindow); err == nil {
				t.Fatalf("stamped copy was synced back to the host as %s", copyHostName)
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// waitForHostService polls until the named host Service exists (wantExists) and
// returns nil, or returns the poll error after timeout.
func waitForHostService(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string, wantExists bool, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		_, err := clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return !wantExists, nil
		}
		if err != nil {
			return false, err
		}
		return wantExists, nil
	})
}
