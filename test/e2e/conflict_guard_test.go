//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const (
	e2eConflictTarget = "e2e-conflict-target"
	e2eConflictSrcA   = "e2e-conflict-src-a"
	e2eConflictSrcB   = "e2e-conflict-src-b"

	e2eSyncConflictAnnotation = "kupe.cloud/sync-conflict"
	e2eSyncedFromAnnotation   = "kupe.cloud/synced-from"
	e2eTargetNameAnnotation   = "kupe.cloud/target-name"

	// e2eConflictImportWindow bounds how long a refused import may take to land once its
	// virtual location frees up. It is below the plugin's 2-minute periodic retry of a
	// refused import, so the import has to come from the event that frees the location.
	e2eConflictImportWindow = 60 * time.Second
)

// TestFromHostConflictGuard pre-creates a virtual ConfigMap at the location two host
// ConfigMaps import into (through kupe.cloud/target-name). Both imports are refused: the
// virtual object is never written, and each host object carries the sync-conflict
// annotation. Once the virtual object is deleted, one of the refused imports lands
// straight away; the other stays refused, now against that copy.
func TestFromHostConflictGuard(t *testing.T) {
	feature := features.New("fromhost-conflict-guard").
		Assess("refused imports leave the virtual object alone and land once it is gone", func(ctx context.Context, t *testing.T, _ *envconf.Config) context.Context {
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
			const virtualNamespace = "default"
			hostCMs := hostClientset.CoreV1().ConfigMaps(hostNamespace)
			virtualCMs := vclusterClientset.CoreV1().ConfigMaps(virtualNamespace)

			if !keepTestResources() {
				defer func() {
					cleanupCtx := context.Background()
					_ = hostCMs.Delete(cleanupCtx, e2eConflictSrcA, metav1.DeleteOptions{})
					_ = hostCMs.Delete(cleanupCtx, e2eConflictSrcB, metav1.DeleteOptions{})
					_ = virtualCMs.Delete(cleanupCtx, e2eConflictTarget, metav1.DeleteOptions{})
				}()
			}

			// The virtual object in the way: created in the vcluster, not by the plugin.
			existing, err := virtualCMs.Create(ctx, &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: e2eConflictTarget, Namespace: virtualNamespace},
				Data:       map[string]string{"owner": "virtual"},
			}, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("create virtual configmap: %v", err)
			}

			sources := []string{e2eConflictSrcA, e2eConflictSrcB}
			for _, name := range sources {
				if _, err := hostCMs.Create(ctx, &corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:        name,
						Namespace:   hostNamespace,
						Labels:      e2eLabels(),
						Annotations: map[string]string{e2eTargetNameAnnotation: e2eConflictTarget},
					},
					Data: map[string]string{"owner": name},
				}, metav1.CreateOptions{}); err != nil {
					t.Fatalf("create host configmap %s: %v", name, err)
				}
				// Wait for each refusal in turn, so both imports are known to be refused.
				if err := waitForHostConflictAnnotation(ctx, hostClientset, hostNamespace, name, true, 2*time.Minute); err != nil {
					t.Fatalf("import of %s was not refused: %v", name, err)
				}
			}

			current, err := virtualCMs.Get(ctx, e2eConflictTarget, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get virtual configmap: %v", err)
			}
			if current.ResourceVersion != existing.ResourceVersion {
				t.Errorf("virtual configmap was written while refused: resourceVersion %s, created as %s (data %v, annotations %v)",
					current.ResourceVersion, existing.ResourceVersion, current.Data, current.Annotations)
			}

			if err := virtualCMs.Delete(ctx, e2eConflictTarget, metav1.DeleteOptions{}); err != nil {
				t.Fatalf("delete virtual configmap: %v", err)
			}

			// One refused import lands at the freed location.
			var imported *corev1.ConfigMap
			err = wait.PollUntilContextTimeout(ctx, 2*time.Second, e2eConflictImportWindow, true, func(ctx context.Context) (bool, error) {
				cm, err := virtualCMs.Get(ctx, e2eConflictTarget, metav1.GetOptions{})
				if errors.IsNotFound(err) {
					return false, nil
				}
				if err != nil {
					return false, fmt.Errorf("get virtual configmap: %w", err)
				}
				if cm.UID == existing.UID || cm.Annotations[e2eSyncedFromAnnotation] == "" {
					return false, nil
				}
				imported = cm
				return true, nil
			})
			if err != nil {
				t.Fatalf("no refused import landed within %v of the location freeing up: %v", e2eConflictImportWindow, err)
			}

			winner, loser := "", ""
			for _, name := range sources {
				if imported.Annotations[e2eSyncedFromAnnotation] == hostNamespace+"/"+name {
					winner = name
				} else {
					loser = name
				}
			}
			if winner == "" {
				t.Fatalf("imported copy has provenance %q, want one of %v in %s",
					imported.Annotations[e2eSyncedFromAnnotation], sources, hostNamespace)
			}
			if imported.Data["owner"] != winner {
				t.Errorf("imported copy data = %v, want the data of %s", imported.Data, winner)
			}
			if _, copied := imported.Annotations[e2eSyncConflictAnnotation]; copied {
				t.Errorf("imported copy carries the host-only %s annotation", e2eSyncConflictAnnotation)
			}
			if err := waitForHostConflictAnnotation(ctx, hostClientset, hostNamespace, winner, false, time.Minute); err != nil {
				t.Errorf("%s still marked as in conflict after its import: %v", winner, err)
			}
			// The other host object now conflicts with that copy and stays refused.
			loserObj, err := hostCMs.Get(ctx, loser, metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get host configmap %s: %v", loser, err)
			}
			if _, refused := loserObj.Annotations[e2eSyncConflictAnnotation]; !refused {
				t.Errorf("%s lost its %s annotation although its import is still refused", loser, e2eSyncConflictAnnotation)
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

// waitForHostConflictAnnotation polls until the host ConfigMap carries the sync-conflict
// annotation (want) or no longer does (!want). Any Get error ends the wait.
func waitForHostConflictAnnotation(ctx context.Context, clientset kubernetes.Interface, namespace, name string, want bool, timeout time.Duration) error {
	var last map[string]string
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		cm, err := clientset.CoreV1().ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, fmt.Errorf("get configmap %s/%s: %w", namespace, name, err)
		}
		last = cm.Annotations
		_, present := cm.Annotations[e2eSyncConflictAnnotation]
		return present == want, nil
	})
	if err != nil {
		return fmt.Errorf("wait for %s on %s/%s to be present=%v (last annotations %v): %w",
			e2eSyncConflictAnnotation, namespace, name, want, last, err)
	}
	return nil
}
