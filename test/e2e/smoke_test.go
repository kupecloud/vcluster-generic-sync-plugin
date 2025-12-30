//go:build e2e

package e2e

import (
	"context"
	"os"
	"testing"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

func TestClusterConnectivity(t *testing.T) {
	feature := features.New("cluster-connectivity").
		Assess("list namespaces", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			kubeconfig := os.Getenv("KUBECONFIG")
			if kubeconfig == "" {
				t.Fatalf("KUBECONFIG is not set")
			}
			restCfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
			if err != nil {
				t.Fatalf("build kubeconfig: %v", err)
			}
			clientset, err := kubernetes.NewForConfig(restCfg)
			if err != nil {
				t.Fatalf("create clientset: %v", err)
			}
			namespaces, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
			if err != nil {
				t.Fatalf("list namespaces: %v", err)
			}
			if len(namespaces.Items) == 0 {
				t.Fatalf("expected at least one namespace")
			}
			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
