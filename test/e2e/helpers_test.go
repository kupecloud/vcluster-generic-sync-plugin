//go:build e2e

package e2e

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

const testKubeconfig = "apiVersion: v1\nclusters: []\nusers: []\n"

func kubeconfigSecret(name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "vcluster"},
		Data:       map[string][]byte{"config": []byte(testKubeconfig)},
	}
}

// forbidGets makes every Get of resource fail with Forbidden.
func forbidGets(clientset *fake.Clientset, resource string) {
	clientset.PrependReactor("get", resource, func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: resource}, "x", nil)
	})
}

func TestVClusterKubeconfigPath(t *testing.T) {
	t.Setenv("E2E_VCLUSTER_KUBECONFIG", "")
	t.Setenv("E2E_VCLUSTER_NAMESPACE", "vcluster")
	t.Setenv("E2E_VCLUSTER_NAME", "vcluster")
	t.Setenv("E2E_VCLUSTER_SERVER", "")

	tests := []struct {
		name      string
		secrets   []*corev1.Secret
		forbidden bool
		wantErr   func(error) bool
	}{
		{name: "named secret is used", secrets: []*corev1.Secret{kubeconfigSecret("vc-vcluster")}},
		{
			name:    "a component's kubeconfig is never used",
			secrets: []*corev1.Secret{kubeconfigSecret("vc-config-scheduler")},
			wantErr: func(err error) bool {
				return strings.Contains(err.Error(), "one of [") && errors.Is(err, context.DeadlineExceeded)
			},
		},
		{name: "a failing Get ends the wait with its error", forbidden: true, wantErr: apierrors.IsForbidden},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewClientset()
			for _, secret := range tt.secrets {
				if _, err := clientset.CoreV1().Secrets(secret.Namespace).Create(context.Background(), secret, metav1.CreateOptions{}); err != nil {
					t.Fatalf("create secret: %v", err)
				}
			}
			if tt.forbidden {
				forbidGets(clientset, "secrets")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			path, cleanup, err := vclusterKubeconfigPath(ctx, clientset)
			if tt.wantErr != nil {
				if err == nil {
					cleanup()
					t.Fatalf("vclusterKubeconfigPath() returned %s, want an error", path)
				}
				if !tt.wantErr(err) {
					t.Fatalf("vclusterKubeconfigPath() error = %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("vclusterKubeconfigPath() error: %v", err)
			}
			defer cleanup()
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read kubeconfig: %v", err)
			}
			if string(data) != testKubeconfig {
				t.Errorf("kubeconfig = %q, want the named secret's", data)
			}
		})
	}
}

func TestAssertHostServiceAbsentFor(t *testing.T) {
	tests := []struct {
		name      string
		exists    bool
		forbidden bool
		wantErr   bool
	}{
		{name: "absent for the whole window", wantErr: false},
		{name: "present", exists: true, wantErr: true},
		{name: "a failing Get is not absence", forbidden: true, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clientset := fake.NewClientset()
			if tt.exists {
				svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "copy", Namespace: "vcluster"}}
				if _, err := clientset.CoreV1().Services("vcluster").Create(context.Background(), svc, metav1.CreateOptions{}); err != nil {
					t.Fatalf("create service: %v", err)
				}
			}
			if tt.forbidden {
				forbidGets(clientset, "services")
			}

			err := assertHostServiceAbsentFor(context.Background(), clientset, "vcluster", "copy", time.Second)
			if (err != nil) != tt.wantErr {
				t.Errorf("assertHostServiceAbsentFor() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWaitForHostServiceExists_FailsOnGetError(t *testing.T) {
	clientset := fake.NewClientset()
	forbidGets(clientset, "services")

	err := waitForHostServiceExists(context.Background(), clientset, "vcluster", "svc", 30*time.Second)
	if !apierrors.IsForbidden(err) {
		t.Errorf("waitForHostServiceExists() error = %v, want the Forbidden Get error", err)
	}
}
