package config

import (
	"sigs.k8s.io/controller-runtime/pkg/cache"
)

// HostNamespaces extracts the namespaces that need to be watched on the host cluster
// based on the plugin configuration. This enables efficient cache configuration
// by only watching namespaces that fromHost syncers actually need.
//
// The logic considers both globalFilters and per-syncer selectors:
//  1. If globalFilters.include exists with specific namespaces (no wildcards),
//     those are the ONLY namespaces that can ever be synced, so we only watch those.
//  2. Otherwise, we look at each fromHost syncer's matchNamespaces.
//  3. If any wildcards are found or no filters specified, we watch all namespaces.
//
// Returns a map with cache.AllNamespaces key if all namespaces should be watched.
// Returns a specific namespace map if only certain namespaces are needed.
// Returns nil to use SDK defaults (no fromHost syncers configured).
func HostNamespaces(cfg *Config, existingNamespaces map[string]cache.Config) map[string]cache.Config {
	// If no config, return nil to use SDK defaults - we don't know what to watch
	if cfg == nil {
		return nil
	}

	// Check if we have any fromHost syncers - if not, no need to modify cache
	hasFromHostSyncers := false
	for _, res := range cfg.SyncResources {
		if res.Direction == FromHost {
			hasFromHostSyncers = true
			break
		}
	}
	if !hasFromHostSyncers {
		return nil // Use SDK defaults
	}

	// Start with a copy of any namespaces already configured (e.g., by vCluster SDK)
	// We copy to avoid modifying the original map
	namespaces := copyNamespaces(existingNamespaces)

	// FIRST: Check globalFilters.include - if it has specific namespaces,
	// those are the ONLY namespaces that can be synced (global allowlist)
	if cfg.GlobalFilters != nil && len(cfg.GlobalFilters.Include) > 0 {
		globalHasWildcard := false

		for _, rule := range cfg.GlobalFilters.Include {
			if containsGlobWildcard(rule.Namespace) {
				globalHasWildcard = true
				break
			}
			namespaces[rule.Namespace] = cache.Config{}
		}

		// If global include has only specific namespaces (no wildcards),
		// return the merged namespaces - nothing else can be synced anyway
		if !globalHasWildcard {
			return namespaces
		}
	}

	// SECOND: Check each fromHost syncer's matchNamespaces
	needsAllNamespaces := false
	for _, res := range cfg.SyncResources {
		if res.Direction != FromHost {
			continue
		}

		// If no selector or no matchNamespaces, syncer could match any namespace
		// (this includes cluster-scoped resources which don't have matchNamespaces)
		if res.Selector == nil || len(res.Selector.MatchNamespaces) == 0 {
			needsAllNamespaces = true
			continue
		}

		// Check for wildcard patterns that require all namespaces
		for _, ns := range res.Selector.MatchNamespaces {
			if containsGlobWildcard(ns) {
				needsAllNamespaces = true
				break
			}
			// Add specific namespace to watch list
			namespaces[ns] = cache.Config{}
		}
	}

	// If any syncer needs all namespaces, add the AllNamespaces key
	// but preserve any existing namespace-specific configuration
	if needsAllNamespaces {
		namespaces[cache.AllNamespaces] = cache.Config{}
	}

	// If we ended up with no additional namespaces, return nil to use SDK defaults
	if len(namespaces) == 0 {
		return nil
	}

	return namespaces
}

// copyNamespaces creates a copy of the namespace map to avoid modifying the original
func copyNamespaces(existing map[string]cache.Config) map[string]cache.Config {
	result := make(map[string]cache.Config)
	for ns, cfg := range existing {
		result[ns] = cfg
	}
	return result
}

// containsGlobWildcard checks if a pattern contains glob wildcards
func containsGlobWildcard(pattern string) bool {
	for _, c := range pattern {
		if c == '*' || c == '?' || c == '[' {
			return true
		}
	}
	return false
}
