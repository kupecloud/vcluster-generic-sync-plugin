package config

import (
	"testing"
)

func TestMatchGlob(t *testing.T) {
	tests := []struct {
		pattern  string
		value    string
		expected bool
	}{
		// Exact matches
		{"default", "default", true},
		{"default", "kube-system", false},

		// Wildcard patterns
		{"kube-*", "kube-system", true},
		{"kube-*", "kube-public", true},
		{"kube-*", "default", false},
		{"*-system", "kube-system", true},
		{"*-system", "monitoring-system", true},
		{"*-system", "default", false},

		// Single character wildcard
		{"team-?", "team-a", true},
		{"team-?", "team-ab", false},

		// Complex patterns
		{"team-*-staging", "team-frontend-staging", true},
		{"team-*-staging", "team-backend-staging", true},
		{"team-*-staging", "team-frontend-prod", false},

		// Edge cases
		{"*", "anything", true},
		{"", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.value, func(t *testing.T) {
			result := matchGlob(tt.pattern, tt.value)
			if result != tt.expected {
				t.Errorf("matchGlob(%q, %q) = %v, want %v", tt.pattern, tt.value, result, tt.expected)
			}
		})
	}
}

func TestBuildResourceIdentifiers(t *testing.T) {
	tests := []struct {
		apiVersion string
		kind       string
		expected   []string
	}{
		// Core API
		{"v1", "Secret", []string{"Secret", "v1/Secret"}},
		{"v1", "ConfigMap", []string{"ConfigMap", "v1/ConfigMap"}},

		// Group API
		{"gateway.networking.k8s.io/v1", "Gateway", []string{"Gateway", "v1/Gateway", "gateway.networking.k8s.io/v1/Gateway"}},
		{"apps/v1", "Deployment", []string{"Deployment", "v1/Deployment", "apps/v1/Deployment"}},

		// Empty apiVersion
		{"", "Secret", []string{"Secret"}},
	}

	for _, tt := range tests {
		t.Run(tt.apiVersion+"/"+tt.kind, func(t *testing.T) {
			result := buildResourceIdentifiers(tt.apiVersion, tt.kind)
			if len(result) != len(tt.expected) {
				t.Errorf("buildResourceIdentifiers(%q, %q) = %v, want %v", tt.apiVersion, tt.kind, result, tt.expected)
				return
			}
			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("buildResourceIdentifiers(%q, %q)[%d] = %q, want %q", tt.apiVersion, tt.kind, i, v, tt.expected[i])
				}
			}
		})
	}
}

func TestRuleAppliesToResource(t *testing.T) {
	tests := []struct {
		name        string
		rule        NamespaceRule
		identifiers []string
		expected    bool
	}{
		{
			name:        "empty resources matches all",
			rule:        NamespaceRule{Namespace: "kube-system", Resources: nil},
			identifiers: []string{"Secret", "v1/Secret"},
			expected:    true,
		},
		{
			name:        "kind match",
			rule:        NamespaceRule{Namespace: "kube-system", Resources: []string{"Secret"}},
			identifiers: []string{"Secret", "v1/Secret"},
			expected:    true,
		},
		{
			name:        "full path match",
			rule:        NamespaceRule{Namespace: "kube-system", Resources: []string{"v1/Secret"}},
			identifiers: []string{"Secret", "v1/Secret"},
			expected:    true,
		},
		{
			name:        "no match",
			rule:        NamespaceRule{Namespace: "kube-system", Resources: []string{"ConfigMap"}},
			identifiers: []string{"Secret", "v1/Secret"},
			expected:    false,
		},
		{
			name:        "case insensitive match",
			rule:        NamespaceRule{Namespace: "kube-system", Resources: []string{"secret"}},
			identifiers: []string{"Secret", "v1/Secret"},
			expected:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ruleAppliesToResource(tt.rule, tt.identifiers)
			if result != tt.expected {
				t.Errorf("ruleAppliesToResource() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNamespaceMatcher_IsAllowed(t *testing.T) {
	tests := []struct {
		name          string
		globalFilters *GlobalFilters
		selector      *Selector
		apiVersion    string
		kind          string
		namespace     string
		expected      bool
	}{
		// No filters - everything allowed
		{
			name:       "no filters allows all",
			namespace:  "default",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   true,
		},

		// Global excludes
		{
			name: "global exclude blocks namespace",
			globalFilters: &GlobalFilters{
				Exclude: []NamespaceRule{{Namespace: "kube-system"}},
			},
			namespace:  "kube-system",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   false,
		},
		{
			name: "global exclude with glob pattern",
			globalFilters: &GlobalFilters{
				Exclude: []NamespaceRule{{Namespace: "kube-*"}},
			},
			namespace:  "kube-public",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   false,
		},
		{
			name: "global exclude allows non-matching namespace",
			globalFilters: &GlobalFilters{
				Exclude: []NamespaceRule{{Namespace: "kube-system"}},
			},
			namespace:  "default",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   true,
		},

		// Global excludes with resource scoping
		{
			name: "global exclude for specific resource blocks",
			globalFilters: &GlobalFilters{
				Exclude: []NamespaceRule{{Namespace: "kube-system", Resources: []string{"Secret"}}},
			},
			namespace:  "kube-system",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   false,
		},
		{
			name: "global exclude for different resource allows",
			globalFilters: &GlobalFilters{
				Exclude: []NamespaceRule{{Namespace: "kube-system", Resources: []string{"Secret"}}},
			},
			namespace:  "kube-system",
			apiVersion: "v1",
			kind:       "ConfigMap",
			expected:   true,
		},

		// Global includes
		{
			name: "global include allows matching namespace",
			globalFilters: &GlobalFilters{
				Include: []NamespaceRule{{Namespace: "default"}},
			},
			namespace:  "default",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   true,
		},
		{
			name: "global include blocks non-matching namespace",
			globalFilters: &GlobalFilters{
				Include: []NamespaceRule{{Namespace: "default"}},
			},
			namespace:  "production",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   false,
		},

		// Resource-level excludes (additional to global)
		{
			name: "resource exclude adds to global",
			globalFilters: &GlobalFilters{
				Exclude: []NamespaceRule{{Namespace: "kube-system"}},
			},
			selector: &Selector{
				ExcludeNamespaces: []string{"staging"},
			},
			namespace:  "staging",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   false,
		},

		// Resource-level includes override global includes
		{
			name: "resource include overrides global include",
			globalFilters: &GlobalFilters{
				Include: []NamespaceRule{{Namespace: "default"}},
			},
			selector: &Selector{
				MatchNamespaces: []string{"production"},
			},
			namespace:  "production",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   true,
		},
		{
			name: "resource include does not override global exclude",
			globalFilters: &GlobalFilters{
				Exclude: []NamespaceRule{{Namespace: "kube-system"}},
			},
			selector: &Selector{
				MatchNamespaces: []string{"kube-system"},
			},
			namespace:  "kube-system",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   false, // Global exclude is enforced
		},

		// Complex scenario
		{
			name: "complex: global exclude enforced, resource include for rest",
			globalFilters: &GlobalFilters{
				Exclude: []NamespaceRule{{Namespace: "kube-*"}},
			},
			selector: &Selector{
				MatchNamespaces: []string{"app-*"},
			},
			namespace:  "app-frontend",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   true,
		},
		{
			name: "complex: global exclude enforced even with resource include",
			globalFilters: &GlobalFilters{
				Exclude: []NamespaceRule{{Namespace: "kube-*"}},
			},
			selector: &Selector{
				MatchNamespaces: []string{"kube-system", "app-*"},
			},
			namespace:  "kube-system",
			apiVersion: "v1",
			kind:       "Secret",
			expected:   false, // Global exclude wins
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := NewNamespaceMatcher(tt.apiVersion, tt.kind, tt.globalFilters, tt.selector)
			result := matcher.IsAllowed(tt.namespace)
			if result != tt.expected {
				t.Errorf("IsAllowed(%q) = %v, want %v", tt.namespace, result, tt.expected)
			}
		})
	}
}

func TestNamespaceMatcher_HasFilters(t *testing.T) {
	tests := []struct {
		name          string
		globalFilters *GlobalFilters
		selector      *Selector
		expected      bool
	}{
		{
			name:     "no filters",
			expected: false,
		},
		{
			name: "global exclude only",
			globalFilters: &GlobalFilters{
				Exclude: []NamespaceRule{{Namespace: "kube-system"}},
			},
			expected: true,
		},
		{
			name: "global include only",
			globalFilters: &GlobalFilters{
				Include: []NamespaceRule{{Namespace: "default"}},
			},
			expected: true,
		},
		{
			name: "resource exclude only",
			selector: &Selector{
				ExcludeNamespaces: []string{"staging"},
			},
			expected: true,
		},
		{
			name: "resource include only",
			selector: &Selector{
				MatchNamespaces: []string{"production"},
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			matcher := NewNamespaceMatcher("v1", "Secret", tt.globalFilters, tt.selector)
			result := matcher.HasFilters()
			if result != tt.expected {
				t.Errorf("HasFilters() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestNamespaceMatcher_GetEffective(t *testing.T) {
	globalFilters := &GlobalFilters{
		Include: []NamespaceRule{{Namespace: "default"}},
		Exclude: []NamespaceRule{{Namespace: "kube-system"}},
	}
	selector := &Selector{
		MatchNamespaces:   []string{"production"},
		ExcludeNamespaces: []string{"staging"},
	}

	matcher := NewNamespaceMatcher("v1", "Secret", globalFilters, selector)

	// Resource includes override global includes
	includes := matcher.GetEffectiveIncludes()
	if len(includes) != 1 || includes[0] != "production" {
		t.Errorf("GetEffectiveIncludes() = %v, want [production]", includes)
	}

	// Excludes are combined
	excludes := matcher.GetEffectiveExcludes()
	if len(excludes) != 2 {
		t.Errorf("GetEffectiveExcludes() = %v, want [kube-system, staging]", excludes)
	}
}
