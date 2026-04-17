package config

import (
	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
)

// Config is the root configuration for the plugin
type Config struct {
	Version string `yaml:"version"`
	// LogLevel controls the minimum severity/verbosity to emit.
	// Supported values: error, warning, info, debug, trace (default: info).
	// Info/Warning/Error are gated by this value; Debug/Trace require debug/trace respectively.
	LogLevel logging.LogLevel `yaml:"log_level,omitempty"`
	// MaxConcurrentReconciles controls how many resources can be reconciled in parallel per syncer.
	// Higher values increase throughput but also memory/CPU usage.
	// Default is 10 if not specified. Maximum is 100.
	MaxConcurrentReconciles int `yaml:"max_concurrent_reconciles,omitempty"`
	// DisableEventFiltering disables the optimization that skips reconciliation
	// when only metadata like ManagedFields changes. Set to true for debugging.
	// Default is false (filtering enabled).
	DisableEventFiltering bool `yaml:"disable_event_filtering,omitempty"`
	// EventsEnabled controls whether Kubernetes events are emitted for sync operations.
	// When enabled, events are created for create, update, delete, and error operations.
	// These events are visible via "kubectl get events" and provide observability.
	// Default is true (events enabled).
	EventsEnabled *bool `yaml:"events_enabled,omitempty"`
	// GlobalFilters defines global filtering rules applied to all resources.
	// Currently supports namespace filtering; extensible for future filter types.
	GlobalFilters *GlobalFilters `yaml:"globalFilters,omitempty"`
	// GlobalExtraLabels are labels merged onto all synced target objects.
	// Per-resource ExtraLabels take precedence over global labels on key conflict.
	// Useful for injecting tenant or environment labels via Helm values overlay.
	GlobalExtraLabels map[string]string `yaml:"globalExtraLabels,omitempty"`
	SyncResources     []SyncResource    `yaml:"syncResources"`
}

// GlobalFilters defines global filtering rules applied to all resources.
// Currently supports namespace filtering; extensible for future filter types.
type GlobalFilters struct {
	// Include defines namespaces that are allowed for syncing.
	// If empty, all namespaces are allowed (subject to exclude rules).
	// Supports glob patterns (e.g., "app-*", "team-*").
	Include []NamespaceRule `yaml:"include,omitempty"`
	// Exclude defines namespaces that are never synced.
	// These rules are enforced and cannot be overridden by resource-level config.
	// Supports glob patterns (e.g., "kube-*", "*-system").
	Exclude []NamespaceRule `yaml:"exclude,omitempty"`
}

// NamespaceRule defines a namespace filter rule with optional resource scoping.
type NamespaceRule struct {
	// Namespace is the namespace name or glob pattern to match.
	// Supports glob patterns: "*" matches any sequence, "?" matches single char.
	// Examples: "kube-system", "kube-*", "team-*-staging"
	Namespace string `yaml:"namespace"`
	// Resources limits this rule to specific resource types.
	// If empty, the rule applies to all resources.
	// Format: "Kind" or "apiVersion/Kind" (e.g., "Secret", "v1/Secret", "gateway.networking.k8s.io/v1/Gateway")
	Resources []string `yaml:"resources,omitempty"`
}

// GetMaxConcurrentReconciles returns the configured max concurrent reconciles, defaulting to 10
func (c *Config) GetMaxConcurrentReconciles() int {
	if c.MaxConcurrentReconciles <= 0 {
		return 10
	}
	if c.MaxConcurrentReconciles > 100 {
		return 100
	}
	return c.MaxConcurrentReconciles
}

// IsEventFilteringEnabled returns true if event filtering optimization is enabled
func (c *Config) IsEventFilteringEnabled() bool {
	return !c.DisableEventFiltering
}

// IsEventsEnabled returns true if Kubernetes events should be emitted (default: true)
func (c *Config) IsEventsEnabled() bool {
	if c.EventsEnabled == nil {
		return true // Default to enabled
	}
	return *c.EventsEnabled
}

// GetLogLevel returns the configured log level, defaulting to info
func (c *Config) GetLogLevel() logging.LogLevel {
	if c.LogLevel == "" {
		return logging.LogLevelInfo
	}
	return c.LogLevel
}

// SyncResource defines a single resource type to sync
type SyncResource struct {
	APIVersion string        `yaml:"apiVersion"`
	Kind       string        `yaml:"kind"`
	Direction  SyncDirection `yaml:"direction"`
	Mode       SyncMode      `yaml:"mode,omitempty"`
	// TargetNamespace controls where fromHost resources are created in the virtual cluster.
	// Defaults to "default" if not set.
	TargetNamespace string `yaml:"targetNamespace,omitempty"`
	// HostNamespace overrides where toHost resources are created on the host cluster.
	// Defaults to the vCluster's host namespace if empty. Only applies to toHost direction.
	HostNamespace string `yaml:"hostNamespace,omitempty"`
	// ExtraLabels are additional labels merged onto target objects during sync.
	// For toHost: applied to host objects. For fromHost: applied to virtual objects.
	// Applied after vCluster's standard label translation.
	ExtraLabels map[string]string `yaml:"extraLabels,omitempty"`
	Selector    *Selector         `yaml:"selector,omitempty"`
	// SelectorIncludeOwnerLabels controls whether selector translation adds marker/namespace labels.
	// Default is false to preserve original selector semantics.
	SelectorIncludeOwnerLabels bool    `yaml:"selectorIncludeOwnerLabels,omitempty"`
	Patches                    []Patch `yaml:"patches,omitempty"`
	StatusSync                 bool    `yaml:"statusSync,omitempty"`
}

// SyncDirection indicates the direction of sync
type SyncDirection string

const (
	// ToHost syncs resources from vcluster to host cluster
	ToHost SyncDirection = "toHost"
	// FromHost syncs resources from host cluster to vcluster
	FromHost SyncDirection = "fromHost"
)

// SyncMode indicates the sync behaviour for a resource.
type SyncMode string

const (
	// Sync mode reconciles desired state; status (if enabled) flows host -> virtual.
	Sync SyncMode = "sync"
	// Mirror mode provides one-way read-only copy
	Mirror SyncMode = "mirror"
)

// Selector defines which resources to sync based on labels and namespaces
type Selector struct {
	MatchLabels map[string]string `yaml:"matchLabels,omitempty"`
	// MatchNamespaces limits syncing to these namespaces (include list).
	// If empty, inherits from globalFilters.include or allows all.
	// Supports glob patterns (e.g., "app-*", "team-*").
	MatchNamespaces []string `yaml:"matchNamespaces,omitempty"`
	// ExcludeNamespaces excludes these namespaces from syncing for this resource.
	// These are added to (not replacing) globalFilters.exclude rules.
	// Supports glob patterns (e.g., "kube-*", "*-system").
	ExcludeNamespaces []string `yaml:"excludeNamespaces,omitempty"`
}

// Patch defines a single patch operation for reference translation
type Patch struct {
	Path string    `yaml:"path"`
	Type PatchType `yaml:"type"`
}

// PatchType defines the type of patch operation
type PatchType string

const (
	// PatchRewriteName translates a name field
	// e.g., "my-secret" -> "my-secret-x-ns-x-vcluster"
	PatchRewriteName PatchType = "rewriteName"

	// PatchRewriteNamespace translates a namespace field
	// e.g., "default" -> "vcluster-my-vcluster"
	PatchRewriteNamespace PatchType = "rewriteNamespace"

	// PatchRewriteRef translates both name and namespace in a reference object
	// For references to vcluster-native resources (Services, Secrets, etc.)
	PatchRewriteRef PatchType = "rewriteRef"

	// PatchRewriteHostRef translates namespace only, keeps name as-is
	// For references to host-native resources (shared Gateways, ClusterIssuers, etc.)
	PatchRewriteHostRef PatchType = "rewriteHostRef"

	// PatchRewriteLabelSelector translates labels in a selector
	PatchRewriteLabelSelector PatchType = "rewriteLabelSelector"

	// PatchNone explicitly skips translation (for documentation)
	PatchNone PatchType = "none"
)

// DefaultMode returns the default sync mode if not specified
func (r *SyncResource) DefaultMode() SyncMode {
	if r.Mode == "" {
		return Sync
	}
	return r.Mode
}

// SyncerConfig contains all configuration a syncer needs, combining
// resource-specific settings with plugin-wide performance settings
type SyncerConfig struct {
	// Resource contains the per-resource sync configuration
	Resource SyncResource
	// MaxConcurrentReconciles is the max parallel reconciliations for this syncer
	MaxConcurrentReconciles int
	// EventFilteringEnabled controls whether to filter no-op events
	EventFilteringEnabled bool
	// EventsEnabled controls whether Kubernetes events are emitted
	EventsEnabled bool
	// NamespaceMatcher provides namespace filtering for this resource
	NamespaceMatcher *NamespaceMatcher
}

// NewSyncerConfig creates a SyncerConfig for a resource from the plugin config.
// Global extra labels are merged into each resource's ExtraLabels, with per-resource
// labels taking precedence on key conflict.
func NewSyncerConfig(pluginCfg *Config, res SyncResource) SyncerConfig {
	// Merge global labels into resource labels (resource wins on conflict)
	if len(pluginCfg.GlobalExtraLabels) > 0 {
		merged := make(map[string]string, len(pluginCfg.GlobalExtraLabels)+len(res.ExtraLabels))
		for k, v := range pluginCfg.GlobalExtraLabels {
			merged[k] = v
		}
		for k, v := range res.ExtraLabels {
			merged[k] = v // Per-resource overrides global
		}
		res.ExtraLabels = merged
	}

	return SyncerConfig{
		Resource:                res,
		MaxConcurrentReconciles: pluginCfg.GetMaxConcurrentReconciles(),
		EventFilteringEnabled:   pluginCfg.IsEventFilteringEnabled(),
		EventsEnabled:           pluginCfg.IsEventsEnabled(),
		NamespaceMatcher:        NewNamespaceMatcher(res.APIVersion, res.Kind, pluginCfg.GlobalFilters, res.Selector),
	}
}
