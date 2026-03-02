//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
)

const vclusterValuesTemplate = `
sync:
  toHost:
    secrets:
      enabled: false
controlPlane:
  distro:
    k8s:
      enabled: true
  advanced:
    serviceAccount:
      enabled: false
      name: default
  statefulSet:
    image:
      repository: loft-sh/vcluster-oss
    probes:
      livenessProbe:
        enabled: false
      readinessProbe:
        enabled: false
rbac:
  clusterRole:
    enabled: true
    extraRules:
      - apiGroups: ["apiextensions.k8s.io"]
        resources: ["customresourcedefinitions"]
        verbs: ["get", "list", "watch"]
plugin:
  generic-sync:
    version: v2
    image: %s
    rbac:
      role:
        extraRules:
          - apiGroups: ["example.com"]
            resources: ["widgets"]
            verbs: ["create", "delete", "patch", "update", "get", "list", "watch"]
          - apiGroups: [""]
            resources: ["secrets", "configmaps"]
            verbs: ["create", "delete", "patch", "update", "get", "list", "watch"]
          - apiGroups: ["gateway.networking.k8s.io"]
            resources: ["gateways", "httproutes"]
            verbs: ["create", "delete", "patch", "update", "get", "list", "watch"]
      clusterRole:
        extraRules:
          - apiGroups: ["apiextensions.k8s.io"]
            resources: ["customresourcedefinitions"]
            verbs: ["get", "list", "watch"]
          - apiGroups: [""]
            resources: ["secrets", "configmaps"]
            verbs: ["create", "delete", "patch", "update", "get", "list", "watch"]
          - apiGroups: ["gateway.networking.k8s.io"]
            resources: ["gatewayclasses", "gateways", "httproutes"]
            verbs: ["create", "delete", "patch", "update", "get", "list", "watch"]
    config:
      version: v1
      log_level: "debug"
      syncResources:
        - apiVersion: example.com/v1
          kind: Widget
          direction: toHost
          mode: sync
          statusSync: true
        - apiVersion: v1
          kind: Secret
          direction: toHost
          mode: sync
          selector:
            matchLabels:
              e2e.kupecloud.io/sync: "true"
            matchNamespaces:
              - default
        - apiVersion: v1
          kind: ConfigMap
          direction: fromHost
          mode: sync
          statusSync: true
          targetNamespace: default
          selector:
            matchLabels:
              e2e.kupecloud.io/sync: "true"
            matchNamespaces:
              - "%s"
        - apiVersion: gateway.networking.k8s.io/v1
          kind: GatewayClass
          direction: fromHost
          mode: sync
          selector:
            matchLabels:
              e2e.kupecloud.io/sync: "true"
        - apiVersion: gateway.networking.k8s.io/v1
          kind: Gateway
          direction: fromHost
          mode: sync
          targetNamespace: default
          selector:
            matchLabels:
              e2e.kupecloud.io/sync: "true"
            matchNamespaces:
              - "%s"
        - apiVersion: gateway.networking.k8s.io/v1
          kind: HTTPRoute
          direction: toHost
          mode: sync
          selector:
            matchLabels:
              e2e.kupecloud.io/sync: "true"
            matchNamespaces:
              - default
`

func buildPluginImage(tag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	repoRoot, err := repoRoot()
	if err != nil {
		return err
	}

	return runCmd(ctx, repoRoot, []string{
		"docker", "build", "-t", tag, ".",
	})
}

func loadImageIntoKind(tag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	return runCmd(ctx, "", []string{
		"kind", "load", "docker-image", tag,
		"--name", kindClusterName,
	})
}

func installVClusterChart() error {
	installTimeout := parseDurationEnv("E2E_VCLUSTER_INSTALL_TIMEOUT", 15*time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), installTimeout)
	defer cancel()

	namespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
	releaseName := envOrDefault("E2E_VCLUSTER_NAME", "vcluster")
	chartRepo := envOrDefault("E2E_VCLUSTER_HELM_REPO", "https://charts.loft.sh")
	chartVersion := envOrDefault("E2E_VCLUSTER_VERSION", "v0.30.4")
	imageTag := envOrDefault("E2E_IMAGE_TAG", "vcluster-generic-sync-plugin:e2e")

	valuesFile, cleanup, err := writeValuesFile(imageTag, namespace)
	if err != nil {
		return err
	}
	defer cleanup()

	if err := runCmd(ctx, "", []string{"helm", "repo", "add", "loft", chartRepo}); err != nil {
		return err
	}
	if err := runCmd(ctx, "", []string{"helm", "repo", "update"}); err != nil {
		return err
	}

	args := []string{
		"helm", "upgrade", "--install", releaseName, "loft/vcluster",
		"--namespace", namespace,
		"--create-namespace",
		"--version", chartVersion,
		"--values", valuesFile,
	}
	if err := runCmd(ctx, "", args); err != nil {
		return err
	}
	return waitForVClusterReady(ctx)
}

func waitForVClusterReady(ctx context.Context) error {
	clientset, err := buildClientsetFromEnv()
	if err != nil {
		return err
	}

	readyTimeout := parseDurationEnv("E2E_VCLUSTER_READY_TIMEOUT", 10*time.Minute)
	namespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
	labelSelector := envOrDefault("E2E_VCLUSTER_LABEL_SELECTOR", "app=vcluster")
	releaseName := envOrDefault("E2E_VCLUSTER_NAME", "vcluster")
	reportInterval := parseDurationEnv("E2E_VCLUSTER_STATUS_INTERVAL", 30*time.Second)
	lastReport := time.Now().Add(-reportInterval)

	return wait.PollUntilContextTimeout(ctx, 5*time.Second, readyTimeout, true, func(ctx context.Context) (bool, error) {
		pods, selectorUsed, err := listVClusterPods(ctx, clientset, namespace, labelSelector, releaseName)
		if err != nil {
			if time.Since(lastReport) >= reportInterval {
				fmt.Printf("[e2e] vcluster pod lookup error: %v\n", err)
				lastReport = time.Now()
			}
			return false, nil
		}
		if len(pods) == 0 {
			if time.Since(lastReport) >= reportInterval {
				fmt.Printf("[e2e] No vcluster pods found (selector: %q). Check label selector or namespace.\n", selectorUsed)
				logNamespacePods(ctx, clientset, namespace)
				lastReport = time.Now()
			}
			return false, nil
		}
		for _, pod := range pods {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == "Ready" && cond.Status == "True" {
					return true, nil
				}
			}
		}
		if time.Since(lastReport) >= reportInterval {
			fmt.Printf("[e2e] vcluster pods not ready yet (selector: %q):\n", selectorUsed)
			logPodStatuses(pods)
			lastReport = time.Now()
		}
		return false, nil
	})
}

func listVClusterPods(ctx context.Context, clientset *kubernetes.Clientset, namespace, labelSelector, releaseName string) ([]corev1.Pod, string, error) {
	if labelSelector != "" {
		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			return nil, labelSelector, err
		}
		if len(pods.Items) > 0 {
			return pods.Items, labelSelector, nil
		}
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, labelSelector, err
	}

	var matched []corev1.Pod
	for _, pod := range pods.Items {
		if isLikelyVClusterPod(pod, releaseName) {
			matched = append(matched, pod)
		}
	}
	if len(matched) > 0 {
		return matched, "auto", nil
	}

	return nil, labelSelector, nil
}

func isLikelyVClusterPod(pod corev1.Pod, releaseName string) bool {
	if pod.Labels == nil {
		return strings.Contains(pod.Name, "vcluster")
	}
	if pod.Labels["app"] == "vcluster" {
		return true
	}
	if pod.Labels["app.kubernetes.io/name"] == "vcluster" {
		return true
	}
	if releaseName != "" {
		if pod.Labels["app.kubernetes.io/instance"] == releaseName {
			return true
		}
		if pod.Labels["release"] == releaseName {
			return true
		}
	}
	return strings.Contains(pod.Name, "vcluster")
}

func logNamespacePods(ctx context.Context, clientset *kubernetes.Clientset, namespace string) {
	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Printf("[e2e] Failed to list pods in namespace %q: %v\n", namespace, err)
		return
	}
	if len(pods.Items) == 0 {
		fmt.Printf("[e2e] No pods found in namespace %q\n", namespace)
		return
	}
	logPodStatuses(pods.Items)
}

func logPodStatuses(pods []corev1.Pod) {
	for _, pod := range pods {
		ready := podReadyCondition(pod)
		fmt.Printf("  - %s: phase=%s ready=%s restarts=%d\n", pod.Name, pod.Status.Phase, ready, totalRestarts(pod.Status.ContainerStatuses))
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.State.Waiting != nil {
				fmt.Printf("    - %s: waiting=%s (%s)\n", cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message)
			}
			if cs.State.Terminated != nil {
				fmt.Printf("    - %s: terminated=%s (%s)\n", cs.Name, cs.State.Terminated.Reason, cs.State.Terminated.Message)
			}
		}
	}
}

func podReadyCondition(pod corev1.Pod) string {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return string(cond.Status)
		}
	}
	return "unknown"
}

func totalRestarts(statuses []corev1.ContainerStatus) int32 {
	var total int32
	for _, cs := range statuses {
		total += cs.RestartCount
	}
	return total
}

func writeValuesFile(imageTag, hostNamespace string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "vcluster-generic-sync-values-*")
	if err != nil {
		return "", nil, err
	}
	path := filepath.Join(dir, "values.yaml")
	gatewayHostNamespace := envOrDefault("E2E_GATEWAY_HOST_NAMESPACE", hostNamespace)
	data := fmt.Sprintf(vclusterValuesTemplate, imageTag, hostNamespace, gatewayHostNamespace)
	if err := os.WriteFile(path, []byte(strings.TrimSpace(data)+"\n"), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	return path, func() { _ = os.RemoveAll(dir) }, nil
}

// runCmdVerbose controls whether command output is streamed to stdout/stderr
// Set E2E_LOG_CMD_OUTPUT=true to enable streaming for debugging long-running commands
var runCmdVerbose = parseBool(os.Getenv("E2E_LOG_CMD_OUTPUT"))

func runCmd(ctx context.Context, dir string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("no command provided")
	}
	cmdLine := strings.Join(args, " ")

	// Log command start for visibility during long-running operations
	fmt.Printf("[e2e] Running: %s\n", cmdLine)
	start := time.Now()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	if dir != "" {
		cmd.Dir = dir
	}

	if runCmdVerbose {
		// Stream output directly - useful for debugging long operations
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("run %q: %w", cmdLine, err)
		}
		fmt.Printf("[e2e] Completed (%.1fs): %s\n", time.Since(start).Seconds(), cmdLine)
		return nil
	}

	// Buffer output and only show on error
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %q: %w\nstdout:\n%s\nstderr:\n%s", cmdLine, err, stdout.String(), stderr.String())
	}
	fmt.Printf("[e2e] Completed (%.1fs): %s\n", time.Since(start).Seconds(), cmdLine)
	return nil
}
