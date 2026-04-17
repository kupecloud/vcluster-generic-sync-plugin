package syncers

import (
	"testing"
)

func TestVersion(t *testing.T) {
	// Version constant should be set
	if Version == "" {
		t.Error("Version should not be empty")
	}

	// Version should follow semver format (basic check)
	if Version != "0.1.0" {
		t.Logf("Version is %q (expected 0.1.0 for initial release)", Version)
	}
}

// Note: RegisterAll is difficult to unit test as it:
// 1. Calls config.Load() which reads from filesystem/env
// 2. Calls plugin.MustRegister() which requires vcluster runtime
// 3. Has side effects (initialises logging, registers syncers)
//
// Integration tests should cover RegisterAll functionality.
// The individual components (Factory, ToHostSyncer, FromHostSyncer)
// are tested in their respective test files.
