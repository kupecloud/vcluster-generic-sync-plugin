// Package main is the entry point for the vcluster generic sync plugin.
package main

import (
	"github.com/loft-sh/vcluster-sdk/plugin"
	ctrlmanager "sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
	"github.com/kupecloud/vcluster-generic-sync-plugin/syncers"
)

func main() {
	// Pre-load config to determine which host namespaces need to be watched.
	// If config fails to load, cfg will be nil and we'll watch all namespaces.
	cfg, _ := config.Load()

	ctx := plugin.MustInitWithOptions(plugin.Options{
		ModifyHostManager: func(options *ctrlmanager.Options) {
			if namespaces := config.HostNamespaces(cfg, options.Cache.DefaultNamespaces); namespaces != nil {
				options.Cache.DefaultNamespaces = namespaces
			}
		},
	})

	syncers.RegisterAll(ctx)
	plugin.MustStart()
}
