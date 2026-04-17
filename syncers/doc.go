// Package syncers provides the core synchronisation logic for the vcluster-generic-sync-plugin.
//
// # Overview
//
// This package implements bidirectional resource synchronisation between virtual and host
// Kubernetes clusters. It provides two main syncer implementations:
//
//   - ToHostSyncer: Syncs resources from the virtual cluster to the host cluster
//   - FromHostSyncer: Syncs resources from the host cluster to the virtual cluster
//
// # Architecture
//
// Syncers are created by the Factory based on the plugin configuration. Each syncer
// implements the vCluster SDK's syncer interface and handles the full lifecycle of
// resource synchronisation including:
//
//   - Name and namespace translation between clusters
//   - Reference patching using the patches package
//   - Status synchronisation (when enabled)
//   - Namespace and label selector filtering
//   - Kubernetes event emission for observability
//   - Prometheus metrics recording
//
// # Key Components
//
//   - ToHostSyncer: Implements virtual-to-host synchronisation
//   - FromHostSyncer: Implements host-to-virtual synchronisation
//   - Factory: Creates syncers from configuration
//   - helpers.go: Shared utility functions for both syncers
//   - scope.go: API discovery for resource scope detection
//   - excluder.go: Logic to exclude resources managed by other components
//
// # Status Synchronisation
//
// Status always flows from host to virtual cluster (host → virtual). When statusSync
// is enabled for a resource, the syncer:
//
//  1. Detects if the resource has a status subresource on both clusters
//  2. Reads status from the host object
//  3. Updates the virtual object's status via the status subresource
//
// Status sync is automatically disabled for mirror mode (read-only) and when the
// resource doesn't have a status subresource.
//
// # Event Filtering
//
// By default, syncers filter out updates that only change metadata fields that don't
// affect sync behaviour (like ManagedFields). This optimisation reduces unnecessary
// reconciliations and can be disabled via configuration for debugging.
package syncers
