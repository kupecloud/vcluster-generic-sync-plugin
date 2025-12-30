//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

func TestVClusterReady(t *testing.T) {
	feature := features.New("vcluster-ready").
		Assess("vcluster pod is ready", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			clientset, err := buildClientsetFromEnv()
			if err != nil {
				t.Fatalf("build clientset: %v", err)
			}

			namespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
			labelSelector := envOrDefault("E2E_VCLUSTER_LABEL_SELECTOR", "app=vcluster")

			err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
				pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
				if err != nil || len(pods.Items) == 0 {
					return false, nil
				}
				for _, pod := range pods.Items {
					for _, cond := range pod.Status.Conditions {
						if cond.Type == "Ready" && cond.Status == "True" {
							return true, nil
						}
					}
				}
				return false, nil
			})
			if err != nil {
				t.Fatalf("vcluster pod not ready: %v", err)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
