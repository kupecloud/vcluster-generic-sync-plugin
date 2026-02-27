//go:build go1.18
// +build go1.18

package config

import (
	"testing"
)

// FuzzParse fuzzes the YAML config parser to find panics or unexpected behavior
func FuzzParse(f *testing.F) {
	// Seed with valid and invalid configurations
	seeds := []string{
		// Valid minimal config
		`version: v1
syncResources:
  - apiVersion: v1
    kind: Secret
    direction: toHost`,

		// Valid with all options
		`version: v1
log_level: debug
max_concurrent_reconciles: 50
syncResources:
  - apiVersion: gateway.networking.k8s.io/v1
    kind: HTTPRoute
    direction: toHost
    mode: sync
    statusSync: true
    selector:
      matchLabels:
        app: test
      matchNamespaces:
        - "app-*"
    patches:
      - path: spec.rules[*].backendRefs[*]
        type: rewriteRef`,

		// Invalid version
		`version: v2
syncResources: []`,

		// Missing required fields
		`version: v1
syncResources:
  - kind: Secret`,

		// Empty config
		``,

		// Malformed YAML
		`version: v1
syncResources:
  - apiVersion: v1
    kind: [invalid`,

		// Unknown fields (should fail with strict parsing)
		`version: v1
unknownField: true
syncResources: []`,

		// Unicode in values
		`version: v1
syncResources:
  - apiVersion: v1
    kind: Secret
    direction: toHost
    selector:
      matchLabels:
        "日本語": "値"`,

		// Very long strings
		`version: v1
syncResources:
  - apiVersion: ` + string(make([]byte, 1000)) + `
    kind: Secret
    direction: toHost`,

		// Special characters in paths
		`version: v1
syncResources:
  - apiVersion: v1
    kind: ConfigMap
    direction: toHost
    patches:
      - path: "spec.data[*].key"
        type: none`,
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, yamlStr string) {
		// Parse should not panic
		_, _ = Parse(yamlStr)
	})
}

// FuzzValidatePatchPath fuzzes the patch path validator
func FuzzValidatePatchPath(f *testing.F) {
	seeds := []string{
		// Valid paths
		"spec.name",
		"spec.rules[*].backendRefs[*].name",
		"spec.items[0].ref",
		"metadata.annotations",

		// Invalid paths
		"",
		".",
		".spec",
		"spec.",
		"spec..name",
		"spec[]",
		"spec[abc]",
		"spec[-1]",
		"[*].name",

		// Edge cases
		"a",
		"a.b.c.d.e.f.g.h.i.j",
		"spec[0][1][2]",
		"spec[*][*][*]",
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, path string) {
		// Should not panic
		_ = validatePatchPath(path, "test")
	})
}

// FuzzValidateGlobPattern fuzzes the glob pattern validator
func FuzzValidateGlobPattern(f *testing.F) {
	seeds := []string{
		// Valid patterns
		"default",
		"kube-*",
		"*-system",
		"team-?-prod",
		"*",
		"app-[a-z]*",

		// Invalid patterns
		"[",
		"[invalid",
		"\\",

		// Edge cases
		"",
		string(make([]byte, 1000)),
	}

	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, pattern string) {
		// Should not panic
		_ = validateGlobPattern(pattern, "test")
	})
}
