package config

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		check   func(*Config) error
	}{
		{
			name: "valid minimal config",
			yaml: `
version: v1
syncResources: []
`,
			wantErr: false,
		},
		{
			name: "valid toHost config",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    mode: sync
    selector:
      matchLabels:
        sync-to-host: "true"
      matchNamespaces:
        - default
        - prod
    selectorIncludeOwnerLabels: true
    patches:
      - path: spec.secretRef.name
        type: rewriteName
    statusSync: true
`,
			wantErr: false,
			check: func(cfg *Config) error {
				if len(cfg.SyncResources) != 1 {
					t.Errorf("expected 1 sync resource, got %d", len(cfg.SyncResources))
				}
				res := cfg.SyncResources[0]
				if res.APIVersion != "example.com/v1" {
					t.Errorf("expected apiVersion 'example.com/v1', got '%s'", res.APIVersion)
				}
				if res.Kind != "MyResource" {
					t.Errorf("expected kind 'MyResource', got '%s'", res.Kind)
				}
				if res.Direction != ToHost {
					t.Errorf("expected direction 'toHost', got '%s'", res.Direction)
				}
				if res.Mode != Sync {
					t.Errorf("expected mode 'sync', got '%s'", res.Mode)
				}
				if !res.StatusSync {
					t.Errorf("expected statusSync true")
				}
				if !res.SelectorIncludeOwnerLabels {
					t.Errorf("expected selectorIncludeOwnerLabels true")
				}
				if res.Selector == nil || len(res.Selector.MatchNamespaces) != 2 {
					t.Errorf("expected 2 matchNamespaces entries")
				}
				if len(res.Patches) != 1 {
					t.Errorf("expected 1 patch, got %d", len(res.Patches))
				}
				return nil
			},
		},
		{
			name: "valid fromHost config",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: ClusterConfig
    direction: fromHost
    mode: mirror
    targetNamespace: system
`,
			wantErr: false,
			check: func(cfg *Config) error {
				if len(cfg.SyncResources) != 1 {
					t.Errorf("expected 1 sync resource, got %d", len(cfg.SyncResources))
				}
				res := cfg.SyncResources[0]
				if res.Direction != FromHost {
					t.Errorf("expected direction 'fromHost', got '%s'", res.Direction)
				}
				if res.Mode != Mirror {
					t.Errorf("expected mode 'mirror', got '%s'", res.Mode)
				}
				if res.TargetNamespace != "system" {
					t.Errorf("expected targetNamespace 'system', got '%s'", res.TargetNamespace)
				}
				return nil
			},
		},
		{
			name: "valid config with hostNamespace and extraLabels",
			yaml: `
version: v1
globalExtraLabels:
  kupe.cloud/tenant: acme
syncResources:
  - apiVersion: argoproj.io/v1alpha1
    kind: Application
    direction: toHost
    hostNamespace: argocd
    extraLabels:
      kupe.cloud/managed-by: vcluster-sync
`,
			wantErr: false,
			check: func(cfg *Config) error {
				if cfg.GlobalExtraLabels["kupe.cloud/tenant"] != "acme" {
					t.Errorf("expected globalExtraLabels[kupe.cloud/tenant] = 'acme', got '%s'", cfg.GlobalExtraLabels["kupe.cloud/tenant"])
				}
				res := cfg.SyncResources[0]
				if res.HostNamespace != "argocd" {
					t.Errorf("expected hostNamespace 'argocd', got '%s'", res.HostNamespace)
				}
				if res.ExtraLabels["kupe.cloud/managed-by"] != "vcluster-sync" {
					t.Errorf("expected extraLabels[kupe.cloud/managed-by] = 'vcluster-sync', got '%s'", res.ExtraLabels["kupe.cloud/managed-by"])
				}
				return nil
			},
		},
		{
			name: "valid config with globalExtraLabels only",
			yaml: `
version: v1
globalExtraLabels:
  env: dev
  team: platform
syncResources: []
`,
			wantErr: false,
			check: func(cfg *Config) error {
				if len(cfg.GlobalExtraLabels) != 2 {
					t.Errorf("expected 2 globalExtraLabels, got %d", len(cfg.GlobalExtraLabels))
				}
				if cfg.GlobalExtraLabels["env"] != "dev" {
					t.Errorf("expected globalExtraLabels[env] = 'dev', got '%s'", cfg.GlobalExtraLabels["env"])
				}
				return nil
			},
		},
		{
			name: "hostNamespace with fromHost produces no error",
			yaml: `
version: v1
syncResources:
  - apiVersion: v1
    kind: ConfigMap
    direction: fromHost
    hostNamespace: ignored-ns
`,
			wantErr: false,
			check: func(cfg *Config) error {
				// hostNamespace is accepted but ignored for fromHost (warning only)
				if cfg.SyncResources[0].HostNamespace != "ignored-ns" {
					t.Errorf("expected hostNamespace to be preserved in struct, got '%s'", cfg.SyncResources[0].HostNamespace)
				}
				return nil
			},
		},
		{
			name: "valid config with all patch types",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    patches:
      - path: spec.secretRef.name
        type: rewriteName
      - path: spec.namespace
        type: rewriteNamespace
      - path: spec.backendRefs[*]
        type: rewriteRef
      - path: spec.parentRefs[*]
        type: rewriteHostRef
      - path: spec.selector
        type: rewriteLabelSelector
      - path: spec.external
        type: none
`,
			wantErr: false,
			check: func(cfg *Config) error {
				if len(cfg.SyncResources[0].Patches) != 6 {
					t.Errorf("expected 6 patches, got %d", len(cfg.SyncResources[0].Patches))
				}
				return nil
			},
		},
		{
			name: "missing version",
			yaml: `
syncResources: []
`,
			wantErr: true,
		},
		{
			name: "invalid version",
			yaml: `
version: v2
syncResources: []
`,
			wantErr: true,
		},
		{
			name: "missing apiVersion",
			yaml: `
version: v1
syncResources:
  - kind: MyResource
    direction: toHost
`,
			wantErr: true,
		},
		{
			name: "missing kind",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    direction: toHost
`,
			wantErr: true,
		},
		{
			name: "missing direction",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
`,
			wantErr: true,
		},
		{
			name: "invalid direction",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: invalid
`,
			wantErr: true,
		},
		{
			name: "invalid mode",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    mode: invalid
`,
			wantErr: true,
		},
		{
			name: "invalid patch type",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    patches:
      - path: spec.name
        type: invalidType
`,
			wantErr: true,
		},
		{
			name: "missing patch path",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    patches:
      - type: rewriteName
`,
			wantErr: true,
		},
		{
			name: "missing patch type",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    patches:
      - path: spec.name
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse(tt.yaml)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.check != nil && err == nil {
				if err := tt.check(cfg); err != nil {
					t.Errorf("check failed: %v", err)
				}
			}
		})
	}
}

func TestDefaultMode(t *testing.T) {
	res := &SyncResource{}
	if res.DefaultMode() != Sync {
		t.Errorf("expected default mode 'sync', got '%s'", res.DefaultMode())
	}

	res.Mode = Mirror
	if res.DefaultMode() != Mirror {
		t.Errorf("expected mode 'mirror', got '%s'", res.DefaultMode())
	}
}

func TestStatusSyncAutoDisabledWithMirrorMode(t *testing.T) {
	tests := []struct {
		name                string
		yaml                string
		expectStatusSync    bool
		expectWarningLogged bool
	}{
		{
			name: "statusSync enabled with sync mode - remains enabled",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    mode: sync
    statusSync: true
`,
			expectStatusSync: true,
		},
		{
			name: "statusSync enabled with mirror mode - auto-disabled",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    mode: mirror
    statusSync: true
`,
			expectStatusSync: false,
		},
		{
			name: "statusSync disabled with mirror mode - stays disabled",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    mode: mirror
    statusSync: false
`,
			expectStatusSync: false,
		},
		{
			name: "statusSync enabled with default mode (sync) - remains enabled",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    statusSync: true
`,
			expectStatusSync: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse(tt.yaml)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if len(cfg.SyncResources) != 1 {
				t.Fatalf("expected 1 sync resource, got %d", len(cfg.SyncResources))
			}
			if cfg.SyncResources[0].StatusSync != tt.expectStatusSync {
				t.Errorf("StatusSync = %v, expected %v", cfg.SyncResources[0].StatusSync, tt.expectStatusSync)
			}
		})
	}
}

func TestGlobalFiltersValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid global filters with simple namespaces",
			yaml: `
version: v1
globalFilters:
  include:
    - namespace: app-*
    - namespace: prod
  exclude:
    - namespace: kube-system
    - namespace: kube-*
syncResources: []
`,
			wantErr: false,
		},
		{
			name: "valid global filters with resource scoping",
			yaml: `
version: v1
globalFilters:
  include:
    - namespace: team-*
      resources:
        - Secret
        - v1/ConfigMap
        - gateway.networking.k8s.io/v1/Gateway
  exclude:
    - namespace: "*-system"
      resources:
        - Secret
syncResources: []
`,
			wantErr: false,
		},
		{
			name: "empty namespace in global filter include",
			yaml: `
version: v1
globalFilters:
  include:
    - namespace: ""
syncResources: []
`,
			wantErr: true,
			errMsg:  "globalFilters.include[0].namespace is required",
		},
		{
			name: "empty namespace in global filter exclude",
			yaml: `
version: v1
globalFilters:
  exclude:
    - namespace: ""
syncResources: []
`,
			wantErr: true,
			errMsg:  "globalFilters.exclude[0].namespace is required",
		},
		{
			name: "invalid glob pattern in global filter",
			yaml: `
version: v1
globalFilters:
  include:
    - namespace: "[invalid"
syncResources: []
`,
			wantErr: true,
			errMsg:  "invalid glob pattern",
		},
		{
			name: "invalid resource format - lowercase kind",
			yaml: `
version: v1
globalFilters:
  include:
    - namespace: prod
      resources:
        - secret
syncResources: []
`,
			wantErr: true,
			errMsg:  "Kind must start with uppercase letter",
		},
		{
			name: "invalid resource format - bad version",
			yaml: `
version: v1
globalFilters:
  include:
    - namespace: prod
      resources:
        - x1/Secret
syncResources: []
`,
			wantErr: true,
			errMsg:  "version 'x1' is invalid",
		},
		{
			name: "invalid resource format - too many parts",
			yaml: `
version: v1
globalFilters:
  include:
    - namespace: prod
      resources:
        - a/b/c/d/Secret
syncResources: []
`,
			wantErr: true,
			errMsg:  "expected 'Kind', 'version/Kind', or 'group/version/Kind'",
		},
		{
			name: "invalid resource format - empty string",
			yaml: `
version: v1
globalFilters:
  include:
    - namespace: prod
      resources:
        - ""
syncResources: []
`,
			wantErr: true,
			errMsg:  "resource cannot be empty",
		},
		{
			name: "invalid resource format - bad group characters",
			yaml: `
version: v1
globalFilters:
  include:
    - namespace: prod
      resources:
        - UPPER.group/v1/Secret
syncResources: []
`,
			wantErr: true,
			errMsg:  "group 'UPPER.group' is invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.yaml)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsg != "" {
				if err == nil || !contains(err.Error(), tt.errMsg) {
					t.Errorf("Parse() error = %v, expected to contain %q", err, tt.errMsg)
				}
			}
		})
	}
}

func TestSelectorNamespaceValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid selector with namespace patterns",
			yaml: `
version: v1
syncResources:
  - apiVersion: v1
    kind: Secret
    direction: toHost
    selector:
      matchNamespaces:
        - app-*
        - prod
      excludeNamespaces:
        - "*-system"
        - temp-*
`,
			wantErr: false,
		},
		{
			name: "empty matchNamespaces pattern",
			yaml: `
version: v1
syncResources:
  - apiVersion: v1
    kind: Secret
    direction: toHost
    selector:
      matchNamespaces:
        - ""
`,
			wantErr: true,
			errMsg:  "syncResources[0].selector.matchNamespaces[0]: namespace pattern cannot be empty",
		},
		{
			name: "empty excludeNamespaces pattern",
			yaml: `
version: v1
syncResources:
  - apiVersion: v1
    kind: Secret
    direction: toHost
    selector:
      excludeNamespaces:
        - app-*
        - ""
`,
			wantErr: true,
			errMsg:  "syncResources[0].selector.excludeNamespaces[1]: namespace pattern cannot be empty",
		},
		{
			name: "invalid glob in matchNamespaces",
			yaml: `
version: v1
syncResources:
  - apiVersion: v1
    kind: Secret
    direction: toHost
    selector:
      matchNamespaces:
        - "[unclosed"
`,
			wantErr: true,
			errMsg:  "invalid glob pattern",
		},
		{
			name: "invalid glob in excludeNamespaces",
			yaml: `
version: v1
syncResources:
  - apiVersion: v1
    kind: Secret
    direction: toHost
    selector:
      excludeNamespaces:
        - "[unclosed"
`,
			wantErr: true,
			errMsg:  "invalid glob pattern",
		},
		{
			name: "valid complex glob patterns",
			yaml: `
version: v1
syncResources:
  - apiVersion: v1
    kind: Secret
    direction: toHost
    selector:
      matchNamespaces:
        - "team-?-*"
        - "app-[abc]-*"
      excludeNamespaces:
        - "*-staging"
`,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.yaml)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr && tt.errMsg != "" {
				if err == nil || !contains(err.Error(), tt.errMsg) {
					t.Errorf("Parse() error = %v, expected to contain %q", err, tt.errMsg)
				}
			}
		})
	}
}

func TestResourceFormatValidation(t *testing.T) {
	tests := []struct {
		name     string
		resource string
		wantErr  bool
	}{
		// Valid formats
		{"valid kind only", "Secret", false},
		{"valid kind with version", "v1/Secret", false},
		{"valid full path", "gateway.networking.k8s.io/v1/Gateway", false},
		{"valid beta version", "v1beta1/CustomResource", false},
		{"valid alpha version", "v2alpha1/Resource", false},

		// Invalid formats
		{"lowercase kind", "secret", true},
		{"empty string", "", true},
		{"invalid version - no v prefix", "1/Secret", true},
		{"invalid version - no number", "v/Secret", true},
		{"too many slashes", "a/b/c/d/Secret", true},
		{"uppercase in group", "Example.COM/v1/Resource", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateResourceFormat(tt.resource, "test")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateResourceFormat(%q) error = %v, wantErr %v", tt.resource, err, tt.wantErr)
			}
		})
	}
}

func TestGlobPatternValidation(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		wantErr bool
	}{
		// Valid patterns
		{"exact match", "kube-system", false},
		{"star wildcard", "app-*", false},
		{"question wildcard", "app-?", false},
		{"mixed wildcards", "team-*-staging", false},
		{"character class", "[abc]-ns", false},

		// Invalid patterns
		{"unclosed bracket", "[abc", true},
		{"bad escape", "test\\", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGlobPattern(tt.pattern, "test")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateGlobPattern(%q) error = %v, wantErr %v", tt.pattern, err, tt.wantErr)
			}
		})
	}
}

// contains checks if s contains substr
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || (s != "" && containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestUnknownFieldsRejected(t *testing.T) {
	tests := []struct {
		name   string
		yaml   string
		errMsg string
	}{
		{
			name: "unknown top-level field",
			yaml: `
version: v1
syncResources: []
unknownField: value
`,
			errMsg: "unknown",
		},
		{
			name: "typo in syncResources field",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    statuSync: true
`,
			errMsg: "not found in type",
		},
		{
			name: "typo in selector field",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    selector:
      matchLabel:
        foo: bar
`,
			errMsg: "not found in type",
		},
		{
			name: "unknown field in globalFilters",
			yaml: `
version: v1
globalFilters:
  includeNamespaces:
    - default
  unknownFilter: value
syncResources: []
`,
			errMsg: "not found in type",
		},
		{
			name: "typo in patches field",
			yaml: `
version: v1
syncResources:
  - apiVersion: example.com/v1
    kind: MyResource
    direction: toHost
    patches:
      - pth: spec.name
        type: rewriteName
`,
			errMsg: "not found in type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.yaml)
			if err == nil {
				t.Errorf("Parse() expected error for unknown field, got nil")
				return
			}
			if !contains(err.Error(), tt.errMsg) {
				t.Errorf("Parse() error = %v, expected to contain %q", err, tt.errMsg)
			}
		})
	}
}

func TestValidateTargetNamespace(t *testing.T) {
	tests := []struct {
		ns      string
		wantErr bool
	}{
		{"argocd", false},
		{"vcluster-acme--prod", false},
		{"observability", false},
		{"kube-system", true},     // VGSP-9: system namespace rejected
		{"kube-public", true},     // VGSP-9
		{"Invalid_NS", true},      // not RFC 1123
		{"-leading-hyphen", true}, // not RFC 1123
		{"", true},                // empty not valid
	}
	for _, tt := range tests {
		t.Run(tt.ns, func(t *testing.T) {
			err := validateTargetNamespace(tt.ns, "test")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateTargetNamespace(%q) err=%v, wantErr=%v", tt.ns, err, tt.wantErr)
			}
		})
	}
}

func TestValidateSyncResource_RejectsSystemHostNamespace(t *testing.T) {
	res := &SyncResource{
		APIVersion:    "v1",
		Kind:          "Secret",
		Direction:     ToHost,
		HostNamespace: "kube-system",
	}
	if err := validateSyncResource(res, 0); err == nil {
		t.Error("VGSP-9: expected error for kube-system hostNamespace")
	}
}
