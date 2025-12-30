package config

import (
	"testing"

	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
)

func TestConfig_GetLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		logLevel logging.LogLevel
		expected logging.LogLevel
	}{
		{
			name:     "empty defaults to info",
			logLevel: "",
			expected: logging.LogLevelInfo,
		},
		{
			name:     "explicit info",
			logLevel: logging.LogLevelInfo,
			expected: logging.LogLevelInfo,
		},
		{
			name:     "debug level",
			logLevel: logging.LogLevelDebug,
			expected: logging.LogLevelDebug,
		},
		{
			name:     "trace level",
			logLevel: logging.LogLevelTrace,
			expected: logging.LogLevelTrace,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{LogLevel: tt.logLevel}
			result := cfg.GetLogLevel()
			if result != tt.expected {
				t.Errorf("GetLogLevel() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestSyncResource_DefaultMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     SyncMode
		expected SyncMode
	}{
		{
			name:     "empty defaults to sync",
			mode:     "",
			expected: Sync,
		},
		{
			name:     "explicit sync",
			mode:     Sync,
			expected: Sync,
		},
		{
			name:     "mirror mode",
			mode:     Mirror,
			expected: Mirror,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := &SyncResource{Mode: tt.mode}
			result := res.DefaultMode()
			if result != tt.expected {
				t.Errorf("DefaultMode() = %q, expected %q", result, tt.expected)
			}
		})
	}
}

func TestSyncDirectionConstants(t *testing.T) {
	if ToHost != "toHost" {
		t.Errorf("ToHost = %q, expected %q", ToHost, "toHost")
	}
	if FromHost != "fromHost" {
		t.Errorf("FromHost = %q, expected %q", FromHost, "fromHost")
	}
}

func TestSyncModeConstants(t *testing.T) {
	if Sync != "sync" {
		t.Errorf("Sync = %q, expected %q", Sync, "sync")
	}
	if Mirror != "mirror" {
		t.Errorf("Mirror = %q, expected %q", Mirror, "mirror")
	}
}

func TestPatchTypeConstants(t *testing.T) {
	tests := []struct {
		patchType PatchType
		expected  string
	}{
		{PatchRewriteName, "rewriteName"},
		{PatchRewriteNamespace, "rewriteNamespace"},
		{PatchRewriteRef, "rewriteRef"},
		{PatchRewriteHostRef, "rewriteHostRef"},
		{PatchRewriteLabelSelector, "rewriteLabelSelector"},
		{PatchNone, "none"},
	}

	for _, tt := range tests {
		if string(tt.patchType) != tt.expected {
			t.Errorf("PatchType constant = %q, expected %q", tt.patchType, tt.expected)
		}
	}
}

func TestConfigStructure(t *testing.T) {
	// Test that Config can be created with all fields
	cfg := Config{
		Version:  "v1",
		LogLevel: logging.LogLevelDebug,
		SyncResources: []SyncResource{
			{
				APIVersion: "gateway.networking.k8s.io/v1",
				Kind:       "HTTPRoute",
				Direction:  ToHost,
				Mode:       Sync,
				StatusSync: true,
				Selector: &Selector{
					MatchLabels: map[string]string{"app": "test"},
				},
				Patches: []Patch{
					{Path: "spec.backendRefs[*].name", Type: PatchRewriteName},
					{Path: "spec.backendRefs[*].namespace", Type: PatchRewriteNamespace},
				},
			},
		},
	}

	if cfg.Version != "v1" {
		t.Errorf("Config.Version = %q, expected %q", cfg.Version, "v1")
	}

	if cfg.LogLevel != logging.LogLevelDebug {
		t.Errorf("Config.LogLevel = %q, expected %q", cfg.LogLevel, logging.LogLevelDebug)
	}

	if len(cfg.SyncResources) != 1 {
		t.Fatalf("Config.SyncResources has %d items, expected 1", len(cfg.SyncResources))
	}

	res := cfg.SyncResources[0]
	if res.Kind != "HTTPRoute" {
		t.Errorf("SyncResource.Kind = %q, expected %q", res.Kind, "HTTPRoute")
	}

	if res.Selector == nil {
		t.Error("SyncResource.Selector is nil")
	} else if res.Selector.MatchLabels["app"] != "test" {
		t.Errorf("Selector.MatchLabels[app] = %q, expected %q", res.Selector.MatchLabels["app"], "test")
	}

	if len(res.Patches) != 2 {
		t.Errorf("SyncResource.Patches has %d items, expected 2", len(res.Patches))
	}
}

func TestSelectorStructure(t *testing.T) {
	selector := Selector{
		MatchLabels: map[string]string{
			"env":  "production",
			"tier": "frontend",
		},
	}

	if selector.MatchLabels["env"] != "production" {
		t.Errorf("Selector.MatchLabels[env] = %q, expected %q", selector.MatchLabels["env"], "production")
	}
	if selector.MatchLabels["tier"] != "frontend" {
		t.Errorf("Selector.MatchLabels[tier] = %q, expected %q", selector.MatchLabels["tier"], "frontend")
	}
}

func TestPatchStructure(t *testing.T) {
	patch := Patch{
		Path: "spec.rules[*].backendRefs[*].name",
		Type: PatchRewriteName,
	}

	if patch.Path != "spec.rules[*].backendRefs[*].name" {
		t.Errorf("Patch.Path = %q, expected %q", patch.Path, "spec.rules[*].backendRefs[*].name")
	}
	if patch.Type != PatchRewriteName {
		t.Errorf("Patch.Type = %q, expected %q", patch.Type, PatchRewriteName)
	}
}

func TestConfig_IsEventsEnabled(t *testing.T) {
	trueVal := true
	falseVal := false

	tests := []struct {
		name          string
		eventsEnabled *bool
		expected      bool
	}{
		{
			name:          "nil defaults to true",
			eventsEnabled: nil,
			expected:      true,
		},
		{
			name:          "explicit true",
			eventsEnabled: &trueVal,
			expected:      true,
		},
		{
			name:          "explicit false",
			eventsEnabled: &falseVal,
			expected:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{EventsEnabled: tt.eventsEnabled}
			result := cfg.IsEventsEnabled()
			if result != tt.expected {
				t.Errorf("IsEventsEnabled() = %v, expected %v", result, tt.expected)
			}
		})
	}
}
