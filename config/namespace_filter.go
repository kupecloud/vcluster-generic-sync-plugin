package config

import (
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
)

// NamespaceMatcher provides namespace filtering logic for a specific resource type.
// It combines global and per-resource rules with the following precedence:
// 1. Global excludes are always enforced (cannot be overridden)
// 2. Per-resource excludes add additional exclusions
// 3. Per-resource includes override global includes (if specified)
// 4. If no includes are specified anywhere, all namespaces are allowed
type NamespaceMatcher struct {
	// globalExcludePatterns are patterns that apply to this resource from global config
	globalExcludePatterns []string
	// globalIncludePatterns are patterns that apply to this resource from global config
	globalIncludePatterns []string
	// resourceExcludePatterns are additional excludes from per-resource config
	resourceExcludePatterns []string
	// resourceIncludePatterns are includes from per-resource config (overrides global)
	resourceIncludePatterns []string
	// hasResourceIncludes indicates if resource-level includes were specified
	hasResourceIncludes bool
}

// NewNamespaceMatcher creates a NamespaceMatcher for a specific resource type.
// apiVersion is the full API version (e.g., "v1", "gateway.networking.k8s.io/v1")
// kind is the resource kind (e.g., "Secret", "Gateway")
// globalFilters is the global filters from Config (can be nil)
// selector is the per-resource selector (can be nil)
func NewNamespaceMatcher(apiVersion, kind string, globalFilters *GlobalFilters, selector *Selector) *NamespaceMatcher {
	m := &NamespaceMatcher{}

	// Build resource identifiers for matching
	resourceIdentifiers := buildResourceIdentifiers(apiVersion, kind)

	// Extract global rules that apply to this resource
	if globalFilters != nil {
		for _, rule := range globalFilters.Exclude {
			if ruleAppliesToResource(rule, resourceIdentifiers) {
				m.globalExcludePatterns = append(m.globalExcludePatterns, rule.Namespace)
			}
		}
		for _, rule := range globalFilters.Include {
			if ruleAppliesToResource(rule, resourceIdentifiers) {
				m.globalIncludePatterns = append(m.globalIncludePatterns, rule.Namespace)
			}
		}
	}

	// Extract per-resource rules
	if selector != nil {
		m.resourceExcludePatterns = selector.ExcludeNamespaces
		m.resourceIncludePatterns = selector.MatchNamespaces
		m.hasResourceIncludes = len(selector.MatchNamespaces) > 0
	}

	return m
}

// IsAllowed checks if a namespace is allowed for syncing.
// Returns true if the namespace passes all filter rules.
func (m *NamespaceMatcher) IsAllowed(namespace string) bool {
	// Step 1: Check global excludes (always enforced, cannot be overridden)
	for _, pattern := range m.globalExcludePatterns {
		if matchGlob(pattern, namespace) {
			return false
		}
	}

	// Step 2: Check resource-level excludes (additional exclusions)
	for _, pattern := range m.resourceExcludePatterns {
		if matchGlob(pattern, namespace) {
			return false
		}
	}

	// Step 3: Check includes
	// Resource-level includes override global includes if specified
	includePatterns := m.globalIncludePatterns
	if m.hasResourceIncludes {
		includePatterns = m.resourceIncludePatterns
	}

	// If no include patterns, all namespaces are allowed (that passed excludes)
	if len(includePatterns) == 0 {
		return true
	}

	// Check if namespace matches any include pattern
	for _, pattern := range includePatterns {
		if matchGlob(pattern, namespace) {
			return true
		}
	}

	return false
}

// buildResourceIdentifiers returns all valid identifiers for a resource.
// For "gateway.networking.k8s.io/v1" and "Gateway", returns:
// - "Gateway"
// - "v1/Gateway"
// - "gateway.networking.k8s.io/v1/Gateway"
func buildResourceIdentifiers(apiVersion, kind string) []string {
	identifiers := []string{kind}

	if apiVersion != "" {
		// Split apiVersion into group and version
		parts := strings.Split(apiVersion, "/")
		if len(parts) == 1 {
			// Core API: "v1" -> "v1/Kind"
			identifiers = append(identifiers, parts[0]+"/"+kind)
		} else {
			// Group API: "gateway.networking.k8s.io/v1" -> "v1/Kind" and "group/v1/Kind"
			version := parts[len(parts)-1]
			identifiers = append(identifiers, version+"/"+kind, apiVersion+"/"+kind)
		}
	}

	return identifiers
}

// ruleAppliesToResource checks if a namespace rule applies to a resource.
// If rule.Resources is empty, it applies to all resources.
// Otherwise, the resource must match one of the specified identifiers.
func ruleAppliesToResource(rule NamespaceRule, resourceIdentifiers []string) bool {
	// Empty resources means rule applies to all
	if len(rule.Resources) == 0 {
		return true
	}

	// Check if any resource identifier matches
	for _, ruleResource := range rule.Resources {
		for _, identifier := range resourceIdentifiers {
			if strings.EqualFold(ruleResource, identifier) {
				return true
			}
		}
	}

	return false
}

// globCache caches the results of glob pattern matching to avoid
// repeated filepath.Match calls for the same pattern+value combinations.
// Bounded to maxGlobCacheSize entries; cleared entirely when exceeded.
var globCache sync.Map // map[string]bool

// globCacheSize tracks the approximate number of entries in globCache.
var globCacheSize atomic.Int64

// maxGlobCacheSize is the upper bound on cached glob results.
// When exceeded the entire cache is cleared (amortised O(1)).
const maxGlobCacheSize = 1000

// matchGlob performs glob-style pattern matching with caching.
// Supports "*" (matches any sequence) and "?" (matches single character).
// Uses filepath.Match which provides standard glob semantics.
// Results are cached to improve performance for repeated checks.
func matchGlob(pattern, value string) bool {
	// Handle exact match first (most common case, no cache needed)
	if pattern == value {
		return true
	}

	// Check cache
	cacheKey := pattern + "\x00" + value // Use null byte as separator (invalid in namespace names)
	if cached, ok := globCache.Load(cacheKey); ok {
		return cached.(bool)
	}

	// Use filepath.Match for glob patterns
	matched, err := filepath.Match(pattern, value)
	if err != nil {
		// Invalid pattern - treat as literal match failure
		// Don't cache errors as patterns should be validated at config load time
		return false
	}

	// Evict entire cache if it has grown too large.
	if globCacheSize.Load() >= maxGlobCacheSize {
		globCache.Range(func(key, _ any) bool {
			globCache.Delete(key)
			return true
		})
		globCacheSize.Store(0)
	}

	// Cache and return result
	if _, loaded := globCache.LoadOrStore(cacheKey, matched); !loaded {
		globCacheSize.Add(1)
	}
	return matched
}

// GetEffectiveIncludes returns the effective include patterns for logging/debugging.
func (m *NamespaceMatcher) GetEffectiveIncludes() []string {
	if m.hasResourceIncludes {
		return m.resourceIncludePatterns
	}
	return m.globalIncludePatterns
}

// GetEffectiveExcludes returns all effective exclude patterns for logging/debugging.
func (m *NamespaceMatcher) GetEffectiveExcludes() []string {
	combined := make([]string, 0, len(m.globalExcludePatterns)+len(m.resourceExcludePatterns))
	combined = append(combined, m.globalExcludePatterns...)
	combined = append(combined, m.resourceExcludePatterns...)
	return combined
}

// HasFilters returns true if any namespace filtering is configured.
func (m *NamespaceMatcher) HasFilters() bool {
	return len(m.globalExcludePatterns) > 0 ||
		len(m.globalIncludePatterns) > 0 ||
		len(m.resourceExcludePatterns) > 0 ||
		len(m.resourceIncludePatterns) > 0
}
