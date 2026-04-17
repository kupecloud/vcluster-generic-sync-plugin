package syncers

import (
	"github.com/loft-sh/vcluster-sdk/plugin"
	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
	"github.com/kupecloud/vcluster-generic-sync-plugin/metrics"
)

// Version information - set via ldflags at build time
var (
	// Version is the plugin version
	Version = "dev"
	// GitCommit is the git commit SHA
	GitCommit = "unknown"
	// BuildDate is the build date
	BuildDate = "unknown"
)

// RegisterAll loads configuration and registers all syncers
func RegisterAll(ctx *synccontext.RegisterContext) {
	log := logging.Log

	// Load configuration first (needed to get log level)
	cfg, err := config.Load()
	if err != nil {
		metrics.RecordConfigReload(false)
		log.Fatal(err, "Failed to load configuration")
	}
	metrics.RecordConfigReload(true)

	// Initialise logging based on config
	logging.InitLogging(cfg.GetLogLevel())

	// Set plugin info metric
	metrics.SetPluginInfo(Version, GitCommit, BuildDate)

	// Log startup info
	log.Info("Plugin starting",
		"version", Version,
		"gitCommit", GitCommit,
		"buildDate", BuildDate,
		"logLevel", cfg.GetLogLevel(),
		"binarySHA", logging.GetBinarySHA(),
		"syncResourceCount", len(cfg.SyncResources))

	// Log detailed config at debug level
	config.PrintDebugConfig(cfg)

	if len(cfg.SyncResources) == 0 {
		log.Warning("No sync resources configured, plugin will run but not sync anything")
		return
	}

	// Create and register syncers
	factory := NewFactory(ctx)
	syncers, err := factory.CreateSyncers(cfg)
	if err != nil {
		log.Fatal(err, "Failed to create syncers")
	}

	for _, s := range syncers {
		log.Info("Registering syncer", "name", s.Name())
		plugin.MustRegister(s)
	}

	log.Info("All syncers registered successfully", "count", len(syncers))
}
