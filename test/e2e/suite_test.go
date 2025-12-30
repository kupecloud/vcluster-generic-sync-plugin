//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/kind/pkg/cluster"
)

var (
	testEnv      env.Environment
	kindProvider *cluster.Provider
	tempDir      string
	keepCluster  bool
	pfStop       func()
	vkCleanup    func()
)

func TestMain(m *testing.M) {
	useExisting := parseBool(os.Getenv("USE_EXISTING_CLUSTER"))
	installVCluster := parseBoolWithDefault(os.Getenv("E2E_INSTALL_VCLUSTER"), !useExisting)
	buildImage := parseBoolWithDefault(os.Getenv("E2E_BUILD_IMAGE"), !useExisting)
	keepCluster = parseBool(os.Getenv("E2E_KEEP_CLUSTER"))
	kubeconfigOut := strings.TrimSpace(os.Getenv("E2E_KUBECONFIG_OUT"))
	kindClusterName = envOrDefault("KIND_CLUSTER_NAME", "vcluster-generic-sync-e2e")

	fmt.Printf("E2E Test Configuration:\n")
	fmt.Printf("  USE_EXISTING_CLUSTER: %v\n", useExisting)
	fmt.Printf("  E2E_INSTALL_VCLUSTER: %v\n", installVCluster)
	fmt.Printf("  E2E_BUILD_IMAGE: %v\n", buildImage)
	fmt.Printf("  E2E_KEEP_CLUSTER: %v\n", keepCluster)
	fmt.Printf("  KIND_CLUSTER_NAME: %s\n", kindClusterName)

	if useExisting {
		path, err := resolveKubeconfigPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: resolve kubeconfig: %v\n", err)
			os.Exit(1)
		}
		kubeconfigPath = path
		fmt.Printf("  Using existing cluster with kubeconfig: %s\n", kubeconfigPath)
	} else {
		dir, err := os.MkdirTemp("", "vcluster-e2e-*")
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: create temp dir: %v\n", err)
			os.Exit(1)
		}
		tempDir = dir
		kubeconfigPath = filepath.Join(tempDir, "kubeconfig")

		fmt.Printf("  Creating Kind cluster: %s\n", kindClusterName)
		fmt.Printf("  Kubeconfig path: %s\n", kubeconfigPath)

		kindProvider = cluster.NewProvider()

		opts := []cluster.CreateOption{
			cluster.CreateWithKubeconfigPath(kubeconfigPath),
			cluster.CreateWithWaitForReady(5 * time.Minute),
		}
		if nodeImage := os.Getenv("KIND_NODE_IMAGE"); nodeImage != "" {
			opts = append(opts, cluster.CreateWithNodeImage(nodeImage))
			fmt.Printf("  KIND_NODE_IMAGE: %s\n", nodeImage)
		}

		fmt.Println("  Creating Kind cluster (this may take a few minutes)...")
		if err := kindProvider.Create(kindClusterName, opts...); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: create kind cluster: %v\n", err)
			// Cleanup on failure
			if tempDir != "" {
				_ = os.RemoveAll(tempDir)
			}
			os.Exit(1)
		}
		fmt.Println("  Kind cluster created successfully")
	}

	// Set KUBECONFIG environment variable
	if err := os.Setenv("KUBECONFIG", kubeconfigPath); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: set KUBECONFIG: %v\n", err)
		os.Exit(1)
	}
	if kubeconfigOut != "" {
		if err := os.WriteFile(kubeconfigOut, []byte(kubeconfigPath+"\n"), 0o600); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: write kubeconfig out: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("  Wrote kubeconfig path to: %s\n", kubeconfigOut)
	}

	// Create environment config from kubeconfig path
	// envconf.New() creates an empty config; we need to point to our kubeconfig
	cfg := envconf.NewWithKubeConfig(kubeconfigPath)
	testEnv = env.NewWithConfig(cfg)

	if err := ensureHostWidgetCRD(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: install widget CRD on host: %v\n", err)
		cleanup()
		os.Exit(1)
	}
	if err := ensureHostGatewayAPICRDs(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: install Gateway API CRDs on host: %v\n", err)
		cleanup()
		os.Exit(1)
	}

	// Build and load plugin image if needed
	if !useExisting && buildImage {
		imageTag := envOrDefault("E2E_IMAGE_TAG", "vcluster-generic-sync-plugin:e2e")
		fmt.Printf("  Building plugin image: %s\n", imageTag)
		if err := buildPluginImage(imageTag); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: build plugin image: %v\n", err)
			cleanup()
			os.Exit(1)
		}
		fmt.Println("  Loading image into Kind cluster...")
		if err := loadImageIntoKind(imageTag); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: load image into kind: %v\n", err)
			cleanup()
			os.Exit(1)
		}
		fmt.Println("  Image loaded successfully")
	}

	// Install vCluster if needed
	if installVCluster {
		fmt.Println("  Installing vCluster...")
		if err := installVClusterChart(); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: install vcluster chart: %v\n", err)
			cleanup()
			os.Exit(1)
		}
		fmt.Println("  vCluster installed and ready")

		if err := setupVClusterAccess(); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: setup vcluster access: %v\n", err)
			cleanup()
			os.Exit(1)
		}

		if err := ensureVClusterGatewayAPICRDs(); err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: install Gateway API CRDs in vcluster: %v\n", err)
			cleanup()
			os.Exit(1)
		}
	}

	if err := runPreflightChecks(); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: preflight checks failed: %v\n", err)
		cleanup()
		os.Exit(1)
	}

	// Register cleanup for non-existing clusters
	if !useExisting {
		testEnv.Finish(func(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
			if keepCluster {
				fmt.Printf("  Keeping Kind cluster: %s\n", kindClusterName)
				fmt.Printf("  Kubeconfig path: %s\n", kubeconfigPath)
				return ctx, nil
			}
			fmt.Println("  Cleaning up Kind cluster...")
			cleanup()
			return ctx, nil
		})
	}

	fmt.Println("  Running tests...")
	code := testEnv.Run(m)
	fmt.Printf("  Tests completed with exit code: %d\n", code)
	os.Exit(code)
}

func runPreflightChecks() error {
	if os.Getenv("E2E_VCLUSTER_KUBECONFIG") == "" {
		return fmt.Errorf("E2E_VCLUSTER_KUBECONFIG is not set; vcluster access is not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	if err := waitForPluginStartupLogs(ctx); err != nil {
		return fmt.Errorf("plugin startup logs not detected: %w", err)
	}
	return nil
}

func ensureHostWidgetCRD() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return applyWidgetCRD(ctx)
}

func ensureHostGatewayAPICRDs() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return applyGatewayAPICRDs(ctx, kubeconfigPath)
}

func ensureVClusterGatewayAPICRDs() error {
	kubeconfig := strings.TrimSpace(os.Getenv("E2E_VCLUSTER_KUBECONFIG"))
	if kubeconfig == "" {
		return fmt.Errorf("E2E_VCLUSTER_KUBECONFIG is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return applyGatewayAPICRDs(ctx, kubeconfig)
}

func cleanup() {
	if keepCluster {
		if pfStop != nil {
			pfStop()
		}
		if vkCleanup != nil {
			vkCleanup()
		}
		return
	}
	if pfStop != nil {
		pfStop()
	}
	if vkCleanup != nil {
		vkCleanup()
	}
	if kindProvider != nil && kindClusterName != "" {
		fmt.Printf("  Deleting Kind cluster: %s\n", kindClusterName)
		_ = kindProvider.Delete(kindClusterName, kubeconfigPath)
	}
	if tempDir != "" {
		_ = os.RemoveAll(tempDir)
	}
}

func setupVClusterAccess() error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	clientset, err := buildClientsetFromEnv()
	if err != nil {
		return err
	}

	namespace := envOrDefault("E2E_VCLUSTER_NAMESPACE", "vcluster")
	labelSelector := envOrDefault("E2E_VCLUSTER_LABEL_SELECTOR", "app=vcluster")
	releaseName := envOrDefault("E2E_VCLUSTER_NAME", "vcluster")

	pods, selectorUsed, err := listVClusterPods(ctx, clientset, namespace, labelSelector, releaseName)
	if err != nil {
		return err
	}
	if len(pods) == 0 {
		return fmt.Errorf("no vcluster pods found for selector %q", selectorUsed)
	}

	fmt.Printf("  Starting port-forward to vcluster pod: %s\n", pods[0].Name)
	pfCtx, pfCancel := context.WithCancel(context.Background())
	port, stop, err := startVClusterPortForward(pfCtx, kubeconfigPath, namespace, pods[0].Name)
	if err != nil {
		pfCancel()
		return err
	}
	pfStop = func() {
		pfCancel()
		stop()
	}

	server := fmt.Sprintf("https://127.0.0.1:%d", port)
	if err := os.Setenv("E2E_VCLUSTER_SERVER", server); err != nil {
		return err
	}

	path, cleanup, err := vclusterKubeconfigPath(ctx, clientset)
	if err != nil {
		return err
	}
	vkCleanup = cleanup
	if err := os.Setenv("E2E_VCLUSTER_KUBECONFIG", path); err != nil {
		return err
	}

	fmt.Printf("  Vcluster server: %s\n", server)
	fmt.Printf("  Vcluster kubeconfig: %s\n", path)
	if err := waitForVClusterAPI(ctx, path); err != nil {
		return fmt.Errorf("vcluster API not reachable: %w", err)
	}
	return nil
}

func resolveKubeconfigPath() (string, error) {
	if value := os.Getenv("KUBECONFIG"); value != "" {
		parts := strings.Split(value, string(os.PathListSeparator))
		if len(parts) > 0 && parts[0] != "" {
			return parts[0], nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kube", "config"), nil
}
