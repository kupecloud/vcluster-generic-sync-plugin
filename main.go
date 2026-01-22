// Package main is the entry point for the vcluster generic sync plugin.
package main

import (
	"github.com/loft-sh/vcluster-sdk/plugin"
	ctrlmanager "sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/kupecloud/vcluster-generic-sync-plugin/syncers"
)

func main() {
	// Use InitWithOptions to configure the host manager to watch all namespaces.
	// By default, the vCluster SDK only watches the vCluster's own namespace,
	// but we need to watch all namespaces for fromHost syncers to work properly.
	ctx := plugin.MustInitWithOptions(plugin.Options{
		ModifyHostManager: func(options *ctrlmanager.Options) {
			// Setting DefaultNamespaces to nil tells controller-runtime to watch
			// all namespaces instead of just the vCluster's namespace.
			options.Cache.DefaultNamespaces = nil
		},
	})
	syncers.RegisterAll(ctx)
	plugin.MustStart()
}
