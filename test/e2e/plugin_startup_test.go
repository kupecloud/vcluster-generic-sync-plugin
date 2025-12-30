//go:build e2e

package e2e

import (
	"context"
	"testing"

	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestPluginStartupLogs(t *testing.T) {
	feature := features.New("plugin-startup-logs").
		Assess("plugin logs indicate startup", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			if err := waitForPluginStartupLogs(ctx); err != nil {
				t.Fatalf("plugin startup logs not found: %v", err)
			}

			return ctx
		}).
		Feature()

	testEnv.Test(t, feature)
}
