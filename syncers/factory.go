package syncers

import (
	"fmt"
	"strings"

	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	syncertypes "github.com/loft-sh/vcluster/pkg/syncer/types"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
	"github.com/kupecloud/vcluster-generic-sync-plugin/metrics"
)

// Factory creates syncers from configuration
type Factory struct {
	ctx *synccontext.RegisterContext
	log *logging.Logger
}

// NewFactory creates a new syncer factory
func NewFactory(ctx *synccontext.RegisterContext) *Factory {
	return &Factory{ctx: ctx, log: logging.Log}
}

// CreateSyncers creates all syncers from the given configuration
func (f *Factory) CreateSyncers(cfg *config.Config) ([]syncertypes.Base, error) {
	var syncers []syncertypes.Base

	f.log.Debug("Creating syncers",
		"count", len(cfg.SyncResources),
		"maxConcurrentReconciles", cfg.GetMaxConcurrentReconciles(),
		"eventFilteringEnabled", cfg.IsEventFilteringEnabled())

	for i, res := range cfg.SyncResources {
		f.log.Debug("Creating syncer",
			"index", i,
			"apiVersion", res.APIVersion,
			"kind", res.Kind,
			"direction", res.Direction)

		syncer, err := f.createSyncer(res, cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create syncer for %s/%s: %w", res.APIVersion, res.Kind, err)
		}

		f.log.Debug("Syncer created", "name", syncer.Name())
		syncers = append(syncers, syncer)
	}

	return syncers, nil
}

// createSyncer creates a single syncer for the given resource configuration
func (f *Factory) createSyncer(res config.SyncResource, pluginCfg *config.Config) (syncertypes.Base, error) {
	gvk, err := parseGVK(res.APIVersion, res.Kind)
	if err != nil {
		return nil, err
	}

	// Build unified syncer config from resource + plugin-wide settings
	cfg := config.NewSyncerConfig(pluginCfg, res)

	f.log.Info("Creating syncer",
		"gvk", gvk.String(),
		"direction", res.Direction,
		"mode", res.DefaultMode(),
		"maxConcurrentReconciles", cfg.MaxConcurrentReconciles)

	var syncer syncertypes.Base
	var syncerErr error

	switch res.Direction {
	case config.ToHost:
		syncer, syncerErr = NewToHostSyncer(f.ctx, gvk, cfg)
	case config.FromHost:
		syncer, syncerErr = NewFromHostSyncer(f.ctx, gvk, cfg)
	default:
		return nil, fmt.Errorf("unknown sync direction: %s", res.Direction)
	}

	if syncerErr != nil {
		return nil, syncerErr
	}

	// Register syncer info metric
	metrics.RegisterSyncer(
		string(res.Direction),
		gvk.Kind,
		res.APIVersion,
		string(res.DefaultMode()),
		res.StatusSync,
	)

	return syncer, nil
}

// parseGVK parses apiVersion and kind into a GroupVersionKind
func parseGVK(apiVersion, kind string) (schema.GroupVersionKind, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionKind{}, fmt.Errorf("invalid apiVersion %q: %w", apiVersion, err)
	}
	return gv.WithKind(kind), nil
}

// syncerName generates a unique name for the syncer
func syncerName(gvk schema.GroupVersionKind, direction config.SyncDirection) string {
	name := strings.ToLower(gvk.Kind)
	if gvk.Group != "" {
		name = strings.ToLower(gvk.Group) + "-" + name
	}
	return fmt.Sprintf("generic-%s-%s", direction, name)
}
