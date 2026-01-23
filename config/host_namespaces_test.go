package config

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/cache"
)

func TestHostNamespaces(t *testing.T) {
	tests := []struct {
		name               string
		cfg                *Config
		existingNamespaces map[string]cache.Config
		wantAllNamespaces  bool
		wantNamespaces     []string
		wantNil            bool
	}{
		{
			name:    "nil config returns nil (SDK defaults)",
			cfg:     nil,
			wantNil: true,
		},
		{
			name: "no fromHost syncers returns nil (SDK defaults)",
			cfg: &Config{
				Version: "v1",
				SyncResources: []SyncResource{
					{APIVersion: "v1", Kind: "Secret", Direction: ToHost},
				},
			},
			wantNil: true,
		},
		{
			name: "fromHost with no selector returns all namespaces",
			cfg: &Config{
				Version: "v1",
				SyncResources: []SyncResource{
					{APIVersion: "v1", Kind: "ConfigMap", Direction: FromHost},
				},
			},
			wantAllNamespaces: true,
		},
		{
			name: "fromHost with empty matchNamespaces returns all namespaces",
			cfg: &Config{
				Version: "v1",
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{}},
					},
				},
			},
			wantAllNamespaces: true,
		},
		{
			name: "fromHost with wildcard matchNamespaces returns all namespaces",
			cfg: &Config{
				Version: "v1",
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"team-*"}},
					},
				},
			},
			wantAllNamespaces: true,
		},
		{
			name: "fromHost with specific matchNamespaces returns those namespaces",
			cfg: &Config{
				Version: "v1",
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"team-alpha", "team-beta"}},
					},
				},
			},
			wantNamespaces: []string{"team-alpha", "team-beta"},
		},
		{
			name: "multiple fromHost syncers with specific namespaces returns union",
			cfg: &Config{
				Version: "v1",
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"ns-a"}},
					},
					{
						APIVersion: "v1",
						Kind:       "Secret",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"ns-b"}},
					},
				},
			},
			wantNamespaces: []string{"ns-a", "ns-b"},
		},
		{
			name: "one fromHost with wildcard among specific returns all namespaces",
			cfg: &Config{
				Version: "v1",
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"ns-a"}},
					},
					{
						APIVersion: "v1",
						Kind:       "Secret",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"team-*"}},
					},
				},
			},
			wantAllNamespaces: true,
		},
		{
			name: "globalFilters.include with specific namespaces returns those only",
			cfg: &Config{
				Version: "v1",
				GlobalFilters: &GlobalFilters{
					Include: []NamespaceRule{
						{Namespace: "allowed-ns-1"},
						{Namespace: "allowed-ns-2"},
					},
				},
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"allowed-ns-1", "other-ns"}},
					},
				},
			},
			wantNamespaces: []string{"allowed-ns-1", "allowed-ns-2"},
		},
		{
			name: "globalFilters.include with wildcard but syncer has specific ns uses syncer ns",
			cfg: &Config{
				Version: "v1",
				GlobalFilters: &GlobalFilters{
					Include: []NamespaceRule{
						{Namespace: "team-*"},
					},
				},
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"team-alpha"}},
					},
				},
			},
			// Syncer is more restrictive than global filter, so we only watch syncer's namespaces
			wantNamespaces: []string{"team-alpha"},
		},
		{
			name: "globalFilters.include with wildcard and syncer with wildcard returns all",
			cfg: &Config{
				Version: "v1",
				GlobalFilters: &GlobalFilters{
					Include: []NamespaceRule{
						{Namespace: "team-*"},
					},
				},
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"team-*"}},
					},
				},
			},
			wantAllNamespaces: true,
		},
		{
			name: "globalFilters.exclude does not affect watched namespaces",
			cfg: &Config{
				Version: "v1",
				GlobalFilters: &GlobalFilters{
					Exclude: []NamespaceRule{
						{Namespace: "kube-*"},
					},
				},
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"my-ns"}},
					},
				},
			},
			wantNamespaces: []string{"my-ns"},
		},
		{
			name: "preserves existing namespaces from SDK",
			cfg: &Config{
				Version: "v1",
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"my-ns"}},
					},
				},
			},
			existingNamespaces: map[string]cache.Config{"vcluster": {}},
			wantNamespaces:     []string{"vcluster", "my-ns"},
		},
		{
			name: "question mark wildcard triggers all namespaces",
			cfg: &Config{
				Version: "v1",
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"ns-?"}},
					},
				},
			},
			wantAllNamespaces: true,
		},
		{
			name: "bracket wildcard triggers all namespaces",
			cfg: &Config{
				Version: "v1",
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"ns-[abc]"}},
					},
				},
			},
			wantAllNamespaces: true,
		},
		{
			name: "toHost syncers are ignored",
			cfg: &Config{
				Version: "v1",
				SyncResources: []SyncResource{
					{
						APIVersion: "v1",
						Kind:       "Secret",
						Direction:  ToHost,
						Selector:   &Selector{MatchNamespaces: []string{"any-ns"}},
					},
					{
						APIVersion: "v1",
						Kind:       "ConfigMap",
						Direction:  FromHost,
						Selector:   &Selector{MatchNamespaces: []string{"specific-ns"}},
					},
				},
			},
			wantNamespaces: []string{"specific-ns"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HostNamespaces(tt.cfg, tt.existingNamespaces)

			if tt.wantNil {
				if got != nil {
					t.Errorf("HostNamespaces() = %v, want nil", got)
				}
				return
			}

			if tt.wantAllNamespaces {
				if got == nil {
					t.Errorf("HostNamespaces() = nil, want all namespaces")
					return
				}
				if _, ok := got[cache.AllNamespaces]; !ok {
					t.Errorf("HostNamespaces() = %v, want cache.AllNamespaces key", got)
				}
				return
			}

			if tt.wantNamespaces != nil {
				if got == nil {
					t.Errorf("HostNamespaces() = nil, want namespaces %v", tt.wantNamespaces)
					return
				}
				// Check all wanted namespaces are present
				for _, ns := range tt.wantNamespaces {
					if _, ok := got[ns]; !ok {
						t.Errorf("HostNamespaces() missing namespace %q, got %v", ns, got)
					}
				}
				// Check no extra namespaces (except AllNamespaces shouldn't be there)
				if _, ok := got[cache.AllNamespaces]; ok {
					t.Errorf("HostNamespaces() has AllNamespaces key but want specific namespaces %v", tt.wantNamespaces)
				}
			}
		})
	}
}

func TestContainsGlobWildcard(t *testing.T) {
	tests := []struct {
		pattern string
		want    bool
	}{
		{"team-alpha", false},
		{"team-*", true},
		{"*", true},
		{"ns-?", true},
		{"ns-[abc]", true},
		{"ns-[a-z]", true},
		{"simple", false},
		{"with-dash", false},
		{"with.dot", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			if got := containsGlobWildcard(tt.pattern); got != tt.want {
				t.Errorf("containsGlobWildcard(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}
}
