// Package patches provides reference translation and patching for synced resources.
//
// # Overview
//
// When resources are synced between virtual and host clusters, references to other
// resources (like Services, Secrets, or ConfigMaps) need to be translated to the
// appropriate names in the target cluster. This package handles that translation.
//
// # Name Translation
//
// vCluster uses a naming convention for translated resources:
//
//	{name}-x-{namespace}-x-{vcluster}
//
// For example, a Secret named "my-secret" in namespace "default" in vcluster "dev"
// becomes "my-secret-x-default-x-dev" on the host cluster.
//
// # Patch Types
//
// The package supports several patch types for different use cases:
//
//   - rewriteName: Translates a name field (e.g., secretRef.name)
//   - rewriteNamespace: Translates a namespace field
//   - rewriteRef: Translates both name and namespace in a reference object
//   - rewriteHostRef: Translates namespace only (for host-native resources)
//   - rewriteLabelSelector: Translates label keys in selectors
//   - none: Copies value as-is (for documentation purposes)
//
// # Path Syntax
//
// Patches use dot-notation paths with array support:
//
//	spec.secretRef.name           // Simple field path
//	spec.rules[*].backendRefs[*]  // Array wildcard - applies to all elements
//	spec.rules[0].name            // Specific array index
//
// # Original Reference Tracking
//
// The patcher stores original reference values in annotations to enable accurate
// reverse translation. This is important because the name translation format is
// ambiguous when names or namespaces contain "-x-".
//
// See docs/configuration/patches.md for detailed documentation on limitations.
package patches
