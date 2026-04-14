// Package main is the entry point for the vcluster generic sync plugin.
package main

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/ghodss/yaml"
	"github.com/loft-sh/vcluster-sdk/plugin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlmanager "sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
	_ "github.com/kupecloud/vcluster-generic-sync-plugin/metrics" // registers generic_sync_* metrics
	"github.com/kupecloud/vcluster-generic-sync-plugin/syncers"
)

func main() {
	// Serve plugin metrics on port 8082 (syncer uses 8080/8081).
	// The plugin runs as a separate process, so its controller-runtime
	// registry (which includes generic_sync_* metrics) isn't exposed
	// by the syncer's metrics server.
	go func() {
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}))
		srv := &http.Server{
			Addr:              "0.0.0.0:8082",
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       30 * time.Second,
		}
		if err := srv.ListenAndServe(); err != nil {
			fmt.Fprintf(os.Stderr, "metrics server error: %v\n", err)
		}
	}()

	ctx := plugin.MustInitWithOptions(plugin.Options{
		ModifyHostManager: modifyHostManager,
	})

	syncers.RegisterAll(ctx)
	plugin.MustStart()
}

// modifyHostManager extends the host-side cache to include any additional
// namespaces required by hostNamespace overrides in the sync config.
//
// This adds namespaces to DefaultNamespaces which means ALL resource type
// informers will watch those namespaces. The SA must have list/watch RBAC
// for all synced resource types in each additional namespace. The overhead
// is negligible — informers for types with zero objects in the namespace
// consume only an idle watch connection.
//
// We cannot use Cache.ByObject to scope per-resource because
// controller-runtime resolves ByObject keys via scheme.ObjectKinds, which
// fails for unstructured CRD types not registered in the scheme.
func modifyHostManager(options *ctrlmanager.Options) {
	if options.Cache.DefaultNamespaces == nil {
		return
	}
	for _, ns := range collectHostNamespaces() {
		options.Cache.DefaultNamespaces[ns] = cache.Config{}
	}
}

// collectHostNamespaces parses the plugin config from the PLUGIN_CONFIG env var
// and returns all unique hostNamespace values from sync resource entries.
func collectHostNamespaces() []string {
	raw := os.Getenv(config.ConfigEnvVar)
	if raw == "" {
		raw = os.Getenv(config.LegacyConfigEnvVar)
	}
	if raw == "" {
		return nil
	}
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: failed to parse PLUGIN_CONFIG for host namespace discovery: %v\n", err)
		return nil
	}
	seen := map[string]bool{}
	for _, r := range cfg.SyncResources {
		if r.HostNamespace != "" {
			seen[r.HostNamespace] = true
		}
	}
	result := make([]string, 0, len(seen))
	for ns := range seen {
		result = append(result, ns)
	}
	return result
}
