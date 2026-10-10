//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/loft-sh/vcluster/pkg/util/translate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// Shared variables accessible across all e2e files
// These are set in TestMain (suite_test.go) and used by other files
var (
	// kindClusterName is the name of the Kind cluster
	kindClusterName string
	// kubeconfigPath is the path to the kubeconfig file for the host cluster
	kubeconfigPath string
)

const (
	e2eSyncLabelKey           = "e2e.kupecloud.io/sync"
	e2eSyncLabelValue         = "true"
	e2eControlledByLabelKey   = "vcluster.loft.sh/controlled-by"
	e2eControlledByLabelValue = "generic-sync"
)

// Widget GVR for testing
var widgetGVR = schema.GroupVersionResource{Group: "example.com", Version: "v1", Resource: "widgets"}

var gatewayClassGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "gatewayclasses",
}

var gatewayGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "gateways",
}

var httpRouteGVR = schema.GroupVersionResource{
	Group:    "gateway.networking.k8s.io",
	Version:  "v1",
	Resource: "httproutes",
}

// =============================================================================
// Environment helpers
// =============================================================================

// envOrDefault returns the value of an environment variable or a fallback
func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

// parseBool parses a boolean from a string
func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "y":
		return true
	default:
		return false
	}
}

// parseBoolWithDefault parses a boolean with a default fallback
func parseBoolWithDefault(value string, fallback bool) bool {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return parseBool(value)
}

func keepTestResources() bool {
	return parseBool(os.Getenv("E2E_KEEP_RESOURCES"))
}

func e2eLabels() map[string]string {
	return map[string]string{
		e2eSyncLabelKey:         e2eSyncLabelValue,
		e2eControlledByLabelKey: e2eControlledByLabelValue,
	}
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

// repoRoot returns the root directory of the repository
func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(wd, "..", ".."))
}

// =============================================================================
// Kubernetes client helpers
// =============================================================================

// buildClientsetFromPath creates a Kubernetes clientset from a kubeconfig path
func buildClientsetFromPath(path string) (*kubernetes.Clientset, error) {
	if path == "" {
		return nil, fmt.Errorf("kubeconfig path is empty")
	}
	restCfg, err := clientcmd.BuildConfigFromFlags("", path)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(restCfg)
}

// buildClientsetFromEnv creates a Kubernetes clientset from KUBECONFIG env var
func buildClientsetFromEnv() (*kubernetes.Clientset, error) {
	return buildClientsetFromPath(os.Getenv("KUBECONFIG"))
}

// buildVClusterClientset creates a clientset for the vCluster
func buildVClusterClientset(ctx context.Context) (*kubernetes.Clientset, func(), error) {
	hostClientset, err := buildClientsetFromEnv()
	if err != nil {
		return nil, nil, err
	}
	kubeconfig, cleanup, err := vclusterKubeconfigPath(ctx, hostClientset)
	if err != nil {
		return nil, nil, err
	}
	vclusterClientset, err := buildClientsetFromPath(kubeconfig)
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return vclusterClientset, cleanup, nil
}

// hostDynamicClient creates a dynamic client for the host cluster
func hostDynamicClient() (dynamic.Interface, error) {
	hostConfig, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return nil, err
	}
	return dynamic.NewForConfig(hostConfig)
}

// vclusterDynamicClient creates a dynamic client for the vCluster
func vclusterDynamicClient(ctx context.Context) (dynamic.Interface, string, func(), error) {
	clientset, err := buildClientsetFromEnv()
	if err != nil {
		return nil, "", nil, err
	}
	vclusterKubeconfig, cleanup, err := vclusterKubeconfigPath(ctx, clientset)
	if err != nil {
		return nil, "", nil, err
	}
	restCfg, err := clientcmd.BuildConfigFromFlags("", vclusterKubeconfig)
	if err != nil {
		cleanup()
		return nil, "", nil, err
	}
	client, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		cleanup()
		return nil, "", nil, err
	}
	return client, vclusterKubeconfig, cleanup, nil
}

// =============================================================================
// vCluster kubeconfig helpers
// =============================================================================

// vclusterKubeconfigPath finds and returns the path to the vCluster kubeconfig
func vclusterKubeconfigPath(ctx context.Context, clientset kubernetes.Interface) (string, func(), error) {
	if path := os.Getenv("E2E_VCLUSTER_KUBECONFIG"); path != "" {
		return path, func() {}, nil
	}

	namespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
	releaseName := envOrDefault("E2E_VCLUSTER_NAME", "vcluster")
	serverOverride := strings.TrimSpace(os.Getenv("E2E_VCLUSTER_SERVER"))

	secretNames := []string{
		fmt.Sprintf("%s-kubeconfig", releaseName),
		fmt.Sprintf("%s-kube-config", releaseName),
		fmt.Sprintf("vc-%s", releaseName),
		fmt.Sprintf("vcluster-%s", releaseName),
		"vcluster-kubeconfig",
	}

	// The exported kubeconfig secret is written by the syncer some time after the pod
	// reports Ready, so wait for it. Only the named secrets are accepted: any other secret
	// holding a kubeconfig belongs to a control-plane component (e.g. the scheduler) and
	// lacks the permissions the suite needs.
	var data []byte
	pollErr := wait.PollUntilContextTimeout(ctx, 2*time.Second, 90*time.Second, true, func(ctx context.Context) (bool, error) {
		for _, name := range secretNames {
			secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
			if errors.IsNotFound(err) {
				continue
			}
			if err != nil {
				return false, fmt.Errorf("get secret %s/%s: %w", namespace, name, err)
			}
			if kubeconfig, ok := kubeconfigFromSecret(secret); ok {
				data = kubeconfig
				return true, nil
			}
		}
		return false, nil
	})
	if pollErr != nil {
		return "", nil, fmt.Errorf("wait for the vcluster kubeconfig secret (one of %v) in namespace %s: %w", secretNames, namespace, pollErr)
	}
	if serverOverride != "" {
		var err error
		data, err = rewriteKubeconfigServer(data, serverOverride)
		if err != nil {
			return "", nil, err
		}
	}
	return writeTempKubeconfig(data)
}

func kubeconfigFromSecret(secret *corev1.Secret) ([]byte, bool) {
	if secret == nil {
		return nil, false
	}
	if data, ok := secret.Data["config"]; ok && looksLikeKubeconfig(data) {
		return data, true
	}
	if data, ok := secret.Data["kubeconfig"]; ok && looksLikeKubeconfig(data) {
		return data, true
	}
	for _, data := range secret.Data {
		if looksLikeKubeconfig(data) {
			return data, true
		}
	}
	return nil, false
}

func looksLikeKubeconfig(data []byte) bool {
	text := string(data)
	return strings.Contains(text, "apiVersion: v1") &&
		strings.Contains(text, "clusters:") &&
		strings.Contains(text, "users:")
}

func writeTempKubeconfig(data []byte) (string, func(), error) {
	dir, err := os.MkdirTemp("", "vcluster-kubeconfig-*")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return path, func() { _ = os.RemoveAll(dir) }, nil
}

func rewriteKubeconfigServer(data []byte, server string) ([]byte, error) {
	config, err := clientcmd.Load(data)
	if err != nil {
		return nil, err
	}
	for _, cluster := range config.Clusters {
		cluster.Server = server
	}
	return clientcmd.Write(*config)
}

func waitForVClusterAPI(ctx context.Context, kubeconfig string) error {
	clientset, err := buildClientsetFromPath(kubeconfig)
	if err != nil {
		return err
	}
	return wait.PollUntilContextTimeout(ctx, 2*time.Second, 90*time.Second, true, func(ctx context.Context) (bool, error) {
		_, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return false, nil
		}
		return true, nil
	})
}

func waitForPluginStartupLogs(ctx context.Context) error {
	required := []string{
		envOrDefault("E2E_PLUGIN_LOG_START", "Plugin starting"),
		envOrDefault("E2E_PLUGIN_LOG_READY", "All syncers registered successfully"),
	}
	return waitForPluginLogTokens(ctx, required)
}

func waitForPluginLogTokens(ctx context.Context, required []string) error {
	clientset, err := buildClientsetFromEnv()
	if err != nil {
		return err
	}

	namespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
	labelSelector := envOrDefault("E2E_VCLUSTER_LABEL_SELECTOR", "app=vcluster")
	container := envOrDefault("E2E_VCLUSTER_CONTAINER", "syncer")
	explicitContainer := strings.TrimSpace(os.Getenv("E2E_VCLUSTER_CONTAINER")) != ""

	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 3*time.Minute, true, func(ctx context.Context) (bool, error) {
		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil || len(pods.Items) == 0 {
			return false, nil
		}
		for _, pod := range pods.Items {
			containers := []string{container}
			if !explicitContainer {
				for _, c := range pod.Spec.Containers {
					if c.Name != container {
						containers = append(containers, c.Name)
					}
				}
			}
			for _, candidate := range containers {
				logs, err := fetchPodLogs(ctx, clientset, namespace, pod.Name, candidate)
				if err != nil {
					continue
				}
				found := true
				for _, token := range required {
					if !strings.Contains(logs, token) {
						found = false
						break
					}
				}
				if found {
					return true, nil
				}
			}
		}
		return false, nil
	})
}

func fetchPodLogs(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName, container string) (string, error) {
	opts := podLogOptions(container)
	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, &opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func fetchPodLogsSince(ctx context.Context, clientset *kubernetes.Clientset, namespace, podName, container string, since time.Time) (string, error) {
	sinceTime := metav1.NewTime(since)
	opts := corev1.PodLogOptions{
		Container: container,
		SinceTime: &sinceTime,
	}
	req := clientset.CoreV1().Pods(namespace).GetLogs(podName, &opts)
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()

	data, err := io.ReadAll(stream)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func podLogOptions(container string) corev1.PodLogOptions {
	return corev1.PodLogOptions{
		Container: container,
	}
}

// =============================================================================
// kubectl helpers
// =============================================================================

// kubectlApply applies a manifest file using kubectl
func kubectlApply(ctx context.Context, kubeconfigPath, manifestPath string) error {
	return runCmd(ctx, "", []string{
		"kubectl",
		"--kubeconfig", kubeconfigPath,
		"apply",
		"-f", manifestPath,
	})
}

// =============================================================================
// Widget test helpers
// =============================================================================

// applyWidgetCRD applies the Widget CRD to the host cluster
func applyWidgetCRD(ctx context.Context) error {
	return applyWidgetCRDToCluster(ctx, kubeconfigPath)
}

func applyWidgetCRDToCluster(ctx context.Context, kubeconfig string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	crdPath := filepath.Join(root, "test", "testdata", "widget-crd.yaml")
	return kubectlApply(ctx, kubeconfig, crdPath)
}

func applyGatewayAPICRDs(ctx context.Context, kubeconfig string) error {
	// Vendored render of the upstream kustomization at ref=v1.4.1 — see the
	// header in the testdata file for provenance and the regeneration command.
	// Applied from disk rather than `apply -k github.com/...` because the
	// runtime git fetch is not hermetic: CI runners share a NAT IP and GitHub
	// intermittently challenges the anonymous fetch for credentials.
	root, err := repoRoot()
	if err != nil {
		return err
	}
	crdPath := filepath.Join(root, "test", "testdata", "gateway-api-crds-v1.4.1.yaml")

	// `kubectl apply` is GET-then-CREATE per object, so it races anything else
	// creating the same CRD in that window (in the vCluster, the syncer can
	// create the Gateway API CRDs concurrently), failing with AlreadyExists
	// (main run 36421277413, 2026-09-28). A re-apply patches the now-existing
	// object, so retry only that race; any other error still fails at once.
	const attempts = 5
	for i := 1; ; i++ {
		err = kubectlApply(ctx, kubeconfig, crdPath)
		if err == nil || i == attempts || !strings.Contains(err.Error(), "AlreadyExists") {
			return err
		}
		fmt.Printf("[e2e] Gateway API CRD apply raced a concurrent create (attempt %d/%d), retrying\n", i, attempts)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(3 * time.Second):
		}
	}
}

// applyWidgetToVCluster applies the example Widget to the vCluster
func applyWidgetToVCluster(ctx context.Context, kubeconfig string) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	widgetPath := filepath.Join(root, "test", "testdata", "widget.yaml")
	if err := applyWidgetCRDToCluster(ctx, kubeconfig); err != nil {
		return err
	}
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		if err := kubectlApply(ctx, kubeconfig, widgetPath); err != nil {
			return false, nil
		}
		return true, nil
	})
}

// waitForHostWidget waits for the Widget to appear on the host cluster
func waitForHostWidget(ctx context.Context, hostDyn dynamic.Interface) error {
	hostNamespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
	hostName := hostWidgetName()

	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		_, err := hostDyn.Resource(widgetGVR).Namespace(hostNamespace).Get(ctx, hostName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	})
}

// waitForVClusterWidget waits for the Widget to appear in the vCluster
func waitForVClusterWidget(ctx context.Context, vclusterDyn dynamic.Interface) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		_, err := vclusterDyn.Resource(widgetGVR).Namespace("default").Get(ctx, "my-widget", metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, nil
	})
}

// hostWidgetName returns the translated name of the Widget on the host cluster
func hostWidgetName() string {
	vclusterName := envOrDefault("E2E_VCLUSTER_NAME", "vcluster")
	return translate.SingleNamespaceHostName("my-widget", "default", vclusterName)
}
