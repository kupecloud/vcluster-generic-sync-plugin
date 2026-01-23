// Package main is the entry point for the vcluster generic sync plugin.
package main

import (
	"github.com/loft-sh/vcluster-sdk/plugin"

	"github.com/kupecloud/vcluster-generic-sync-plugin/syncers"
)

func main() {
	ctx := plugin.MustInit()

	syncers.RegisterAll(ctx)
	plugin.MustStart()
}
