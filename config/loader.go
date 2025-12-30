// Package config provides configuration loading and validation for the vcluster generic sync plugin.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
)

const (
	// ConfigEnvVar is the environment variable name for the configuration
	ConfigEnvVar = "PLUGIN_CONFIG"
	// LegacyConfigEnvVar is the previous environment variable name
	LegacyConfigEnvVar = "CONFIG"
)

// Load loads the configuration from PLUGIN_CONFIG (or CONFIG for backwards compatibility)
func Load() (*Config, error) {
	configStr := os.Getenv(ConfigEnvVar)
	if configStr == "" {
		configStr = os.Getenv(LegacyConfigEnvVar)
	}
	if configStr == "" {
		return nil, fmt.Errorf("environment variable %s (or %s) is not set", ConfigEnvVar, LegacyConfigEnvVar)
	}
	return Parse(configStr)
}

// Parse parses the configuration from a YAML string.
// Unknown fields in the YAML will cause an error to fail fast on typos.
func Parse(yamlStr string) (*Config, error) {
	cfg := &Config{}

	// Use yaml.Decoder with KnownFields to reject unknown fields (catches typos)
	decoder := yaml.NewDecoder(strings.NewReader(yamlStr))
	decoder.KnownFields(true)
	if err := decoder.Decode(cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if err := Validate(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

// Validate validates the configuration
func Validate(cfg *Config) error {
	if cfg.Version == "" {
		return fmt.Errorf("version is required")
	}
	if cfg.Version != "v1" {
		return fmt.Errorf("unsupported config version: %s (expected v1)", cfg.Version)
	}

	// Validate log level if specified
	if cfg.LogLevel != "" && !logging.IsValidLogLevel(cfg.LogLevel) {
		return fmt.Errorf("invalid log_level: '%s' (must be one of: error, warning, info, debug, trace)", cfg.LogLevel)
	}

	// Validate global filters
	if cfg.GlobalFilters != nil {
		if err := validateGlobalFilters(cfg.GlobalFilters); err != nil {
			return err
		}
	}

	// Track seen resources to detect duplicates
	seen := make(map[string]int)

	for i := range cfg.SyncResources {
		if err := validateSyncResource(&cfg.SyncResources[i], i); err != nil {
			return err
		}

		// Check for duplicate resource definitions
		res := &cfg.SyncResources[i]
		key := resourceKey(res.APIVersion, res.Kind, res.Direction)
		if prevIndex, exists := seen[key]; exists {
			return fmt.Errorf("duplicate syncResource: %s/%s with direction '%s' defined at index %d and %d",
				res.APIVersion, res.Kind, res.Direction, prevIndex, i)
		}
		seen[key] = i
	}

	return nil
}

// validateGlobalFilters validates the globalFilters configuration
func validateGlobalFilters(gf *GlobalFilters) error {
	for i, rule := range gf.Include {
		if err := validateNamespaceRule(&rule, fmt.Sprintf("globalFilters.include[%d]", i)); err != nil {
			return err
		}
	}
	for i, rule := range gf.Exclude {
		if err := validateNamespaceRule(&rule, fmt.Sprintf("globalFilters.exclude[%d]", i)); err != nil {
			return err
		}
	}
	return nil
}

// validateNamespaceRule validates a single namespace rule
func validateNamespaceRule(rule *NamespaceRule, prefix string) error {
	// Namespace must be non-empty
	if rule.Namespace == "" {
		return fmt.Errorf("%s.namespace is required", prefix)
	}

	// Validate glob pattern by testing it
	if err := validateGlobPattern(rule.Namespace, prefix+".namespace"); err != nil {
		return err
	}

	// Validate resource format if specified
	for j, res := range rule.Resources {
		if err := validateResourceFormat(res, fmt.Sprintf("%s.resources[%d]", prefix, j)); err != nil {
			return err
		}
	}

	return nil
}

// validateGlobPattern checks if a glob pattern is valid
func validateGlobPattern(pattern, path string) error {
	_, err := filepath.Match(pattern, "test")
	if err != nil {
		return fmt.Errorf("%s: invalid glob pattern '%s': %w", path, pattern, err)
	}
	return nil
}

// validateResourceFormat validates the format of a resource identifier
// Valid formats: "Kind", "version/Kind", "group/version/Kind"
func validateResourceFormat(resource, path string) error {
	if resource == "" {
		return fmt.Errorf("%s: resource cannot be empty", path)
	}

	parts := strings.Split(resource, "/")
	switch len(parts) {
	case 1:
		// "Kind" - must start with uppercase
		if !isValidKind(parts[0]) {
			return fmt.Errorf("%s: invalid resource format '%s' (Kind must start with uppercase letter)", path, resource)
		}
	case 2:
		// "version/Kind" - e.g., "v1/Secret"
		if !isValidVersion(parts[0]) {
			return fmt.Errorf("%s: invalid resource format '%s' (version '%s' is invalid)", path, resource, parts[0])
		}
		if !isValidKind(parts[1]) {
			return fmt.Errorf("%s: invalid resource format '%s' (Kind must start with uppercase letter)", path, resource)
		}
	case 3:
		// "group/version/Kind" - e.g., "gateway.networking.k8s.io/v1/Gateway"
		if !isValidGroup(parts[0]) {
			return fmt.Errorf("%s: invalid resource format '%s' (group '%s' is invalid)", path, resource, parts[0])
		}
		if !isValidVersion(parts[1]) {
			return fmt.Errorf("%s: invalid resource format '%s' (version '%s' is invalid)", path, resource, parts[1])
		}
		if !isValidKind(parts[2]) {
			return fmt.Errorf("%s: invalid resource format '%s' (Kind must start with uppercase letter)", path, resource)
		}
	default:
		return fmt.Errorf("%s: invalid resource format '%s' (expected 'Kind', 'version/Kind', or 'group/version/Kind')", path, resource)
	}

	return nil
}

// isValidKind checks if the kind starts with an uppercase letter
func isValidKind(kind string) bool {
	if kind == "" {
		return false
	}
	first := kind[0]
	return first >= 'A' && first <= 'Z'
}

// isValidVersion checks if the version follows k8s conventions (v1, v1beta1, v1alpha1, etc)
func isValidVersion(version string) bool {
	if version == "" {
		return false
	}
	// Must start with 'v'
	if version[0] != 'v' {
		return false
	}
	// Must have at least one digit after 'v'
	if len(version) < 2 {
		return false
	}
	return version[1] >= '0' && version[1] <= '9'
}

// isValidGroup checks if the group is a valid DNS subdomain
func isValidGroup(group string) bool {
	if group == "" {
		return false
	}
	// Basic check: must contain at least one character and consist of valid DNS characters
	for _, c := range group {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '.' && c != '-' {
			return false
		}
	}
	return true
}

// resourceKey generates a unique key for a sync resource based on apiVersion, kind, and direction
func resourceKey(apiVersion, kind string, direction SyncDirection) string {
	return fmt.Sprintf("%s:%s:%s", apiVersion, kind, direction)
}

func validateSyncResource(res *SyncResource, index int) error {
	prefix := fmt.Sprintf("syncResources[%d]", index)

	if res.APIVersion == "" {
		return fmt.Errorf("%s.apiVersion is required", prefix)
	}
	if res.Kind == "" {
		return fmt.Errorf("%s.kind is required", prefix)
	}
	if res.Direction == "" {
		return fmt.Errorf("%s.direction is required", prefix)
	}
	if res.Direction != ToHost && res.Direction != FromHost {
		return fmt.Errorf("%s.direction must be 'toHost' or 'fromHost', got '%s'", prefix, res.Direction)
	}
	if res.Mode != "" && res.Mode != Sync && res.Mode != Mirror {
		return fmt.Errorf("%s.mode must be 'sync' or 'mirror', got '%s'", prefix, res.Mode)
	}

	// Auto-disable statusSync when using mirror mode (mirror is read-only)
	if res.StatusSync && res.DefaultMode() == Mirror {
		logging.Log.Warning("statusSync auto-disabled because mirror mode is read-only",
			"resource", fmt.Sprintf("%s/%s", res.APIVersion, res.Kind),
			"direction", string(res.Direction))
		res.StatusSync = false
	}

	// Validate selector namespace patterns
	if res.Selector != nil {
		if err := validateSelector(res.Selector, prefix+".selector"); err != nil {
			return err
		}
	}

	for j, patch := range res.Patches {
		if err := validatePatch(&patch, fmt.Sprintf("%s.patches[%d]", prefix, j)); err != nil {
			return err
		}
	}

	return nil
}

// validateSelector validates the selector configuration including namespace patterns
func validateSelector(sel *Selector, prefix string) error {
	// Validate matchNamespaces patterns
	for i, ns := range sel.MatchNamespaces {
		if ns == "" {
			return fmt.Errorf("%s.matchNamespaces[%d]: namespace pattern cannot be empty", prefix, i)
		}
		if err := validateGlobPattern(ns, fmt.Sprintf("%s.matchNamespaces[%d]", prefix, i)); err != nil {
			return err
		}
	}

	// Validate excludeNamespaces patterns
	for i, ns := range sel.ExcludeNamespaces {
		if ns == "" {
			return fmt.Errorf("%s.excludeNamespaces[%d]: namespace pattern cannot be empty", prefix, i)
		}
		if err := validateGlobPattern(ns, fmt.Sprintf("%s.excludeNamespaces[%d]", prefix, i)); err != nil {
			return err
		}
	}

	return nil
}

func validatePatch(patch *Patch, prefix string) error {
	if patch.Path == "" {
		return fmt.Errorf("%s.path is required", prefix)
	}
	if patch.Type == "" {
		return fmt.Errorf("%s.type is required", prefix)
	}

	validTypes := []PatchType{
		PatchRewriteName,
		PatchRewriteNamespace,
		PatchRewriteRef,
		PatchRewriteHostRef,
		PatchRewriteLabelSelector,
		PatchNone,
	}

	valid := false
	for _, t := range validTypes {
		if patch.Type == t {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("%s.type '%s' is not valid (must be one of: %s)",
			prefix, patch.Type, strings.Join(patchTypeStrings(validTypes), ", "))
	}

	return nil
}

func patchTypeStrings(types []PatchType) []string {
	result := make([]string, len(types))
	for i, t := range types {
		result[i] = string(t)
	}
	return result
}

// PrintDebugConfig logs the full configuration using structured logging
// Only logs at debug level - won't appear unless debug is enabled
func PrintDebugConfig(cfg *Config) {
	logging.Log.Debug("Plugin configuration",
		"version", cfg.Version,
		"logLevel", string(cfg.GetLogLevel()),
		"maxConcurrentReconciles", cfg.GetMaxConcurrentReconciles(),
		"eventFilteringEnabled", cfg.IsEventFilteringEnabled(),
		"eventsEnabled", cfg.IsEventsEnabled(),
		"syncResourceCount", len(cfg.SyncResources))

	// Log global filters if configured
	if cfg.GlobalFilters != nil {
		var globalIncludes, globalExcludes []map[string]interface{}

		for _, rule := range cfg.GlobalFilters.Include {
			globalIncludes = append(globalIncludes, map[string]interface{}{
				"namespace": rule.Namespace,
				"resources": rule.Resources,
			})
		}

		for _, rule := range cfg.GlobalFilters.Exclude {
			globalExcludes = append(globalExcludes, map[string]interface{}{
				"namespace": rule.Namespace,
				"resources": rule.Resources,
			})
		}

		logging.Log.Debug("GlobalFilters",
			"includes", globalIncludes,
			"excludes", globalExcludes)
	}

	for i, res := range cfg.SyncResources {
		patches := make([]map[string]string, len(res.Patches))
		for j, p := range res.Patches {
			patches[j] = map[string]string{"path": p.Path, "type": string(p.Type)}
		}

		var matchLabels map[string]string
		var matchNamespaces, excludeNamespaces []string
		if res.Selector != nil {
			matchLabels = res.Selector.MatchLabels
			matchNamespaces = res.Selector.MatchNamespaces
			excludeNamespaces = res.Selector.ExcludeNamespaces
		}

		logging.Log.Debug("SyncResource",
			"index", i,
			"apiVersion", res.APIVersion,
			"kind", res.Kind,
			"direction", string(res.Direction),
			"mode", string(res.DefaultMode()),
			"statusSync", res.StatusSync,
			"targetNamespace", res.TargetNamespace,
			"selectorIncludeOwnerLabels", res.SelectorIncludeOwnerLabels,
			"matchLabels", matchLabels,
			"matchNamespaces", matchNamespaces,
			"excludeNamespaces", excludeNamespaces,
			"patches", patches)
	}
}
