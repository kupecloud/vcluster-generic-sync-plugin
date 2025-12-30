//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/util/retry"
)

func TestSecretSyncToHost(t *testing.T) {
	feature := features.New("secret-sync-tohost").
		Assess("secret created in vcluster is synced to host", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			hostClientset, err := buildClientsetFromEnv()
			if err != nil {
				t.Fatalf("build host clientset: %v", err)
			}
			vclusterClientset, cleanup, err := buildVClusterClientset(ctx)
			if err != nil {
				t.Fatalf("build vcluster clientset: %v", err)
			}
			defer cleanup()

			secretName := "e2e-secret"
			vclusterNamespace := "default"
			hostNamespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
			logStart := time.Now()

			if !keepTestResources() {
				// Cleanup: best-effort delete of test resources
				defer func() {
					cleanupCtx := context.Background()
					_ = vclusterClientset.CoreV1().Secrets(vclusterNamespace).Delete(cleanupCtx, secretName, metav1.DeleteOptions{})
					// Host secret is cleaned up automatically when vcluster secret is deleted
				}()
			}

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      secretName,
					Namespace: vclusterNamespace,
					Labels:    e2eLabels(),
				},
				Type: corev1.SecretTypeOpaque,
				StringData: map[string]string{
					"token": "s3cr3t",
				},
			}
			if err := upsertSecret(ctx, vclusterClientset, secret); err != nil {
				t.Fatalf("upsert vcluster secret: %v", err)
			}

			// Find the synced secret on the host by label rather than reconstructing the name
			// This is more robust as it works regardless of the translator implementation
			var hostSecret *corev1.Secret
			err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
				secrets, err := hostClientset.CoreV1().Secrets(hostNamespace).List(ctx, metav1.ListOptions{
					LabelSelector: e2eSyncLabelKey + "=" + e2eSyncLabelValue,
				})
				if err != nil {
					return false, err
				}
				for _, hSecret := range secrets.Items {
					if string(hSecret.Data["token"]) == "s3cr3t" {
						hostSecret = hSecret.DeepCopy()
						return true, nil
					}
				}
				return false, nil
			})
			if err != nil {
				t.Fatalf("secret not synced to host: %v", err)
			}
			if hostSecret == nil {
				t.Fatalf("secret not synced to host: unexpected empty result")
			}

			if err := ensureSecretStable(ctx, hostClientset, hostNamespace, hostSecret.Name, hostSecret.UID, 10*time.Second); err != nil {
				t.Fatalf("host secret not stable: %v", err)
			}
			if err := ensureNoSecretDeleteLogs(ctx, logStart, secretName); err != nil {
				t.Fatalf("unexpected secret delete logs: %v", err)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

func TestConfigMapSyncFromHost(t *testing.T) {
	feature := features.New("configmap-sync-fromhost").
		Assess("host configmap is synced to vcluster", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
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
			configName := "e2e-host-config"

			if !keepTestResources() {
				// Cleanup: best-effort delete of test resources
				defer func() {
					cleanupCtx := context.Background()
					_ = hostClientset.CoreV1().ConfigMaps(hostNamespace).Delete(cleanupCtx, configName, metav1.DeleteOptions{})
					// vCluster configmap is cleaned up automatically when host configmap is deleted
				}()
			}

			config := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      configName,
					Namespace: hostNamespace,
					Labels:    e2eLabels(),
				},
				Data: map[string]string{
					"mode": "sync",
				},
			}
			if err := upsertConfigMap(ctx, hostClientset, config); err != nil {
				t.Fatalf("upsert host configmap: %v", err)
			}

			err = wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
				vConfig, err := vclusterClientset.CoreV1().ConfigMaps("default").Get(ctx, configName, metav1.GetOptions{})
				if errors.IsNotFound(err) {
					return false, nil
				}
				if err != nil {
					return false, err
				}
				if vConfig.Data["mode"] != "sync" {
					return false, nil
				}
				return true, nil
			})
			if err != nil {
				t.Fatalf("configmap not synced to vcluster: %v", err)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}

func upsertSecret(ctx context.Context, clientset *kubernetes.Clientset, secret *corev1.Secret) error {
	if secret == nil {
		return nil
	}
	_, err := clientset.CoreV1().Secrets(secret.Namespace).Create(ctx, secret, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		return err
	}
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current, err := clientset.CoreV1().Secrets(secret.Namespace).Get(ctx, secret.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		secret.ResourceVersion = current.ResourceVersion
		_, err = clientset.CoreV1().Secrets(secret.Namespace).Update(ctx, secret, metav1.UpdateOptions{})
		return err
	})
}

func upsertConfigMap(ctx context.Context, clientset *kubernetes.Clientset, config *corev1.ConfigMap) error {
	if config == nil {
		return nil
	}
	_, err := clientset.CoreV1().ConfigMaps(config.Namespace).Create(ctx, config, metav1.CreateOptions{})
	if !errors.IsAlreadyExists(err) {
		return err
	}
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		current, err := clientset.CoreV1().ConfigMaps(config.Namespace).Get(ctx, config.Name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		config.ResourceVersion = current.ResourceVersion
		_, err = clientset.CoreV1().ConfigMaps(config.Namespace).Update(ctx, config, metav1.UpdateOptions{})
		return err
	})
}

func ensureSecretStable(ctx context.Context, clientset *kubernetes.Clientset, namespace, name string, uid types.UID, duration time.Duration) error {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	timer := time.NewTimer(duration)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-ticker.C:
			secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				return fmt.Errorf("host secret %s/%s disappeared", namespace, name)
			}
			if err != nil {
				return err
			}
			if secret.UID != uid {
				return fmt.Errorf("host secret %s/%s uid changed from %s to %s", namespace, name, uid, secret.UID)
			}
		}
	}
}

func ensureNoSecretDeleteLogs(ctx context.Context, since time.Time, secretName string) error {
	clientset, err := buildClientsetFromEnv()
	if err != nil {
		return err
	}

	namespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
	labelSelector := envOrDefault("E2E_VCLUSTER_LABEL_SELECTOR", "app=vcluster")
	container := envOrDefault("E2E_VCLUSTER_CONTAINER", "syncer")

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
	if err != nil || len(pods.Items) == 0 {
		return fmt.Errorf("no vcluster pod found for logs")
	}

	logs, err := fetchPodLogsSince(ctx, clientset, namespace, pods.Items[0].Name, container, since)
	if err != nil {
		return err
	}

	if strings.Contains(logs, "secret."+secretName) && strings.Contains(logs, "not used anymore") {
		return fmt.Errorf("found delete loop log for %q", secretName)
	}
	return nil
}
