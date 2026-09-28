// Package config provides configuration loading and validation for the vcluster generic sync plugin.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
)

// rfc1123NamespaceRe matches valid Kubernetes namespace names (RFC 1123 DNS label).
var rfc1123NamespaceRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidateTargetNamespace validates a target namespace value. It enforces RFC 1123
// and rejects Kubernetes system namespaces (kube-*), which a syncer must never target —
// these are operator-controlled defence-in-depth checks. It does not
// reject other vClusters' vcluster-* namespaces because the plugin's own namespace is only
// known at runtime, not at config-load time; that check is left to RBAC.
//
// It is the single source of truth for both the config-level targetNamespace/hostNamespace
// values (fail-fast at load) and the per-object kupe.cloud/target-namespace annotation
// override (log-and-ignore at runtime), so the two paths cannot drift.
func ValidateTargetNamespace(ns, field string) error {
	if !rfc1123NamespaceRe.MatchString(ns) {
		return fmt.Errorf("%s: %q is not a valid RFC 1123 namespace name", field, ns)
	}
	if strings.HasPrefix(ns, "kube-") {
		return fmt.Errorf("%s: %q targets a Kubernetes system namespace, which is not allowed", field, ns)
	}
	return nil
}

const (
	// ConfigEnvVar is the environment variable name for the configuration
	ConfigEnvVar = "PLUGIN_CONFIG"
	// LegacyConfigEnvVar is the previous environment variable name
	LegacyConfigEnvVar = "CONFIG"
)

// Load loads the configuration from PLUGIN_CONFIG (or CONFIG for backwards compatibility)
func Load() (*Config, error) {
	configStr := os.Getenv(ConfigEnvVar)
	usedEnvVar := ConfigEnvVar

	if configStr == "" {
		configStr = os.Getenv(LegacyConfigEnvVar)
		if configStr != "" {
			usedEnvVar = LegacyConfigEnvVar
			logging.Log.Warning("CONFIG environment variable is deprecated, use PLUGIN_CONFIG instead")
		}
	}

	if configStr == "" {
		return nil, fmt.Errorf("environment variable %s (or %s) is not set", ConfigEnvVar, LegacyConfigEnvVar)
	}

	logging.Log.Debug("Loading configuration", "envVar", usedEnvVar)
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

	if err := ValidateAndNormalize(cfg); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

// ValidateAndNormalize validates the configuration and applies normalisation.
// This function may modify cfg to apply defaults or disable conflicting settings.
// For example, statusSync is disabled when mirror mode is used.
func ValidateAndNormalize(cfg *Config) error {
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

	// Warn if max_concurrent_reconciles exceeds the cap
	if cfg.MaxConcurrentReconciles > 100 {
		logging.Log.Warning("max_concurrent_reconciles exceeds maximum of 100, will be capped",
			"requested", cfg.MaxConcurrentReconciles,
			"effective", 100)
	}

	// Warn if extraLabels use vCluster reserved prefixes
	warnReservedLabels(cfg.GlobalExtraLabels, "globalExtraLabels")

	// Validate global filters
	if cfg.GlobalFilters != nil {
		if err := validateGlobalFilters(cfg.GlobalFilters); err != nil {
			return err
		}
		// Check for conflicting namespace patterns
		checkConflictingNamespacePatterns(cfg.GlobalFilters)
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

// checkConflictingNamespacePatterns warns if the same namespace pattern appears
// in both include and exclude lists, which could indicate a configuration error.
func checkConflictingNamespacePatterns(gf *GlobalFilters) {
	if gf == nil {
		return
	}

	// Build map of exclude patterns for quick lookup
	excludePatterns := make(map[string]bool)
	for _, rule := range gf.Exclude {
		excludePatterns[rule.Namespace] = true
	}

	// Check if any include pattern is also in exclude
	for _, rule := range gf.Include {
		if excludePatterns[rule.Namespace] {
			logging.Log.Warning("Namespace pattern appears in both include and exclude (exclude takes precedence)",
				"pattern", rule.Namespace)
		}
	}
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
// reservedLabelPrefix is the label domain used internally by vCluster for
// tracking managed objects, namespaces, and controller ownership.
const reservedLabelPrefix = "vcluster.loft.sh/"

// warnReservedLabels logs a warning if any label keys use the vCluster reserved prefix.
// Overwriting these labels can break vCluster's internal object tracking.
func warnReservedLabels(labels map[string]string, context string) {
	for k := range labels {
		if strings.HasPrefix(k, reservedLabelPrefix) {
			logging.Log.Warning("extraLabels key uses vCluster reserved prefix — this may break object tracking",
				"context", context,
				"key", k,
				"reservedPrefix", reservedLabelPrefix)
		}
	}
}

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

	// Validate hostNamespace target (toHost shared-namespace override) — RFC 1123 and
	// no system namespaces.
	if res.HostNamespace != "" {
		if err := ValidateTargetNamespace(res.HostNamespace, prefix+".hostNamespace"); err != nil {
			return err
		}
	}

	// Validate config-level targetNamespace (fromHost import target) — same rules as the
	// per-object annotation override, applied at startup so a bad value fails fast rather
	// than retrying NotFound forever.
	if res.TargetNamespace != "" {
		if err := ValidateTargetNamespace(res.TargetNamespace, prefix+".targetNamespace"); err != nil {
			return err
		}
	}

	// Warn if hostNamespace is used with fromHost (it only applies to toHost)
	if res.HostNamespace != "" && res.Direction == FromHost {
		logging.Log.Warning("hostNamespace is ignored for fromHost direction",
			"resource", fmt.Sprintf("%s/%s", res.APIVersion, res.Kind))
	}

	// Warn if extraLabels use vCluster reserved prefixes
	warnReservedLabels(res.ExtraLabels, fmt.Sprintf("%s.extraLabels", prefix))

	for j, patch := range res.Patches {
		if err := validatePatch(&patch, fmt.Sprintf("%s.patches[%d]", prefix, j)); err != nil {
			return err
		}
	}

	// hostOwnedFields name top-level keys of the host object. The
	// system-managed keys are never synced anyway, so listing one is a
	// misunderstanding worth failing on; spec is the thing being synced.
	for j, f := range res.HostOwnedFields {
		switch f {
		case "":
			return fmt.Errorf("%s.hostOwnedFields[%d] is empty", prefix, j)
		case "apiVersion", "kind", "metadata", "status", "spec":
			return fmt.Errorf("%s.hostOwnedFields[%d]: %q cannot be host-owned", prefix, j, f)
		}
		if strings.Contains(f, ".") {
			return fmt.Errorf("%s.hostOwnedFields[%d]: %q must be a top-level field, not a path", prefix, j, f)
		}
	}
	if len(res.HostOwnedFields) > 0 && res.Direction == FromHost {
		logging.Log.Warning("hostOwnedFields is ignored for fromHost direction",
			"resource", fmt.Sprintf("%s/%s", res.APIVersion, res.Kind))
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

	// Validate path format
	if err := validatePatchPath(patch.Path, prefix+".path"); err != nil {
		return err
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

// validatePatchPath validates that a patch path is well-formed.
// Valid paths: "spec.name", "spec.rules[*].name", "spec.items[0].ref"
// Invalid paths: "", ".foo", "foo.", "foo..bar", "foo[]", "foo[abc]"
func validatePatchPath(path, prefix string) error {
	if path == "" {
		return fmt.Errorf("%s: path cannot be empty", prefix)
	}

	// Path cannot start or end with a dot
	if strings.HasPrefix(path, ".") {
		return fmt.Errorf("%s: path cannot start with '.' (got '%s')", prefix, path)
	}
	if strings.HasSuffix(path, ".") {
		return fmt.Errorf("%s: path cannot end with '.' (got '%s')", prefix, path)
	}

	// Check for consecutive dots
	if strings.Contains(path, "..") {
		return fmt.Errorf("%s: path cannot contain consecutive dots (got '%s')", prefix, path)
	}

	// Validate each segment
	segments := strings.Split(path, ".")
	for i, seg := range segments {
		if seg == "" {
			return fmt.Errorf("%s: path segment %d is empty (got '%s')", prefix, i, path)
		}

		// Check for array notation
		if strings.Contains(seg, "[") {
			// Must have matching brackets
			if !strings.Contains(seg, "]") {
				return fmt.Errorf("%s: unclosed bracket in segment '%s' (path: '%s')", prefix, seg, path)
			}
			// Bracket must be at the end
			bracketIdx := strings.Index(seg, "[")
			closeIdx := strings.Index(seg, "]")
			if closeIdx != len(seg)-1 {
				return fmt.Errorf("%s: bracket must be at end of segment '%s' (path: '%s')", prefix, seg, path)
			}
			if bracketIdx >= closeIdx {
				return fmt.Errorf("%s: invalid bracket notation in segment '%s' (path: '%s')", prefix, seg, path)
			}

			// Check index content - must be * or a number
			indexContent := seg[bracketIdx+1 : closeIdx]
			if indexContent == "" {
				return fmt.Errorf("%s: empty array index in segment '%s' (use [*] for wildcard or [0] for specific index)", prefix, seg)
			}
			if indexContent != "*" {
				// Must be a valid non-negative integer
				for _, c := range indexContent {
					if c < '0' || c > '9' {
						return fmt.Errorf("%s: invalid array index '%s' in segment '%s' (must be * or a number)", prefix, indexContent, seg)
					}
				}
			}

			// Field name before bracket must not be empty
			fieldName := seg[:bracketIdx]
			if fieldName == "" {
				return fmt.Errorf("%s: field name before bracket is empty in segment '%s' (path: '%s')", prefix, seg, path)
			}
		}
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

	// Log global extra labels if configured
	if len(cfg.GlobalExtraLabels) > 0 {
		logging.Log.Debug("GlobalExtraLabels", "labels", cfg.GlobalExtraLabels)
	}

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
			"hostNamespace", res.HostNamespace,
			"extraLabels", res.ExtraLabels,
			"selectorIncludeOwnerLabels", res.SelectorIncludeOwnerLabels,
			"matchLabels", matchLabels,
			"matchNamespaces", matchNamespaces,
			"excludeNamespaces", excludeNamespaces,
			"patches", patches)
	}
}
