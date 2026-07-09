package syncers

import (
	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	"github.com/loft-sh/vcluster/pkg/util/patch"
	"github.com/loft-sh/vcluster/pkg/util/translate"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlevent "sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
	"github.com/kupecloud/vcluster-generic-sync-plugin/metrics"
)

// detectStatusSubresource determines if status sync should be enabled for a resource.
// It checks if the resource has a status subresource on both host and virtual clusters,
// and respects the mirror mode restriction (mirror mode is read-only, no status sync).
//
// Status flows host → virtual, so both clusters must have the status subresource
// for sync to be enabled.
func detectStatusSubresource(
	ctx *synccontext.RegisterContext,
	gvk schema.GroupVersionKind,
	cfg config.SyncerConfig,
	log *logging.Logger,
) bool {
	// Mirror mode is read-only, no status sync allowed
	if cfg.Resource.DefaultMode() == config.Mirror {
		log.Info("Status sync disabled because mirror mode is read-only", "gvk", gvk.String())
		return false
	}

	// Check host cluster (source of status)
	if ctx.HostManager == nil {
		log.Warning("Failed to detect status subresource on host (no manager), disabling status sync",
			"gvk", gvk.String())
		return false
	}

	hostDiscovery, err := discovery.NewDiscoveryClientForConfig(ctx.HostManager.GetConfig())
	if err != nil {
		log.Warning("Failed to create host discovery client, disabling status sync",
			"gvk", gvk.String(), "error", err)
		return false
	}

	hostHasStatus, err := hasStatusSubresource(hostDiscovery, gvk)
	switch {
	case err != nil:
		log.Warning("Failed to detect status subresource on host, disabling status sync",
			"gvk", gvk.String(), "error", err)
		return false
	case !hostHasStatus:
		log.Warning("Status sync disabled because host resource has no status subresource",
			"gvk", gvk.String())
		return false
	}

	// Check virtual cluster (target for status updates)
	if ctx.VirtualManager != nil {
		virtualDiscovery, err := discovery.NewDiscoveryClientForConfig(ctx.VirtualManager.GetConfig())
		if err != nil {
			log.Warning("Failed to create virtual discovery client, disabling status sync",
				"gvk", gvk.String(), "error", err)
			return false
		}

		virtualHasStatus, err := hasStatusSubresource(virtualDiscovery, gvk)
		switch {
		case err != nil:
			log.Warning("Failed to detect status subresource on virtual, disabling status sync",
				"gvk", gvk.String(), "error", err)
			return false
		case !virtualHasStatus:
			log.Warning("Status sync disabled because virtual resource has no status subresource",
				"gvk", gvk.String())
			return false
		}
	}

	return true
}

// isCoreAPIResource returns true if the GVK is a built-in Kubernetes resource
// (core API group with empty Group). These resources don't have CRDs.
func isCoreAPIResource(gvk schema.GroupVersionKind) bool {
	return gvk.Group == ""
}

// filterReason indicates why an object was filtered out by matchesSelector
type filterReason int

const (
	filterNone      filterReason = iota // Object matches all filters
	filterNamespace                     // Object filtered due to namespace rules
	filterSelector                      // Object filtered due to label selector
)

// systemManagedFields are top-level fields managed by the Kubernetes API server
// that should not be copied during sync operations.
var systemManagedFields = map[string]bool{
	"apiVersion": true,
	"kind":       true,
	"metadata":   true,
	"status":     true,
}

// stripStatus removes the status field from an unstructured object.
// This should be called before creating objects when statusSync is disabled,
// to prevent status from being accidentally propagated via the main object create.
// Status should only be set via the status subresource when statusSync is enabled.
func stripStatus(obj *unstructured.Unstructured) {
	if obj == nil {
		return
	}
	unstructured.RemoveNestedField(obj.Object, "status")
}

// copySyncableFields replaces all syncable top-level fields in dst with those from src.
// This handles resources like Secrets (data, stringData), ConfigMaps (data, binaryData),
// and CRDs with custom top-level fields beyond just 'spec'.
// Fields present in dst but not in src are deleted to avoid stale data.
// Deep copies are used to avoid sharing references between objects.
func copySyncableFields(src, dst *unstructured.Unstructured) {
	// First, delete all syncable fields from dst that are not in src
	for key := range dst.Object {
		if systemManagedFields[key] {
			continue
		}
		if _, exists := src.Object[key]; !exists {
			delete(dst.Object, key)
		}
	}

	// Then copy all syncable fields from src to dst using deep copy
	for key := range src.Object {
		if systemManagedFields[key] {
			continue
		}
		// Use NestedFieldCopy to get a deep copy of the value
		value, found, _ := unstructured.NestedFieldCopy(src.Object, key)
		if found {
			dst.Object[key] = value
		}
	}
}

// syncStatusHostToVirtual syncs status from a host object to a virtual object.
// This is used by both toHost and fromHost syncers since status always flows host→virtual.
// If the host object has no status, the virtual object's status is cleared.
// Skips the update if the status is already identical to avoid unnecessary API writes.
// On conflict it returns the conflict error and lets the controller requeue rather than
// retrying in-line: the virtual client is cache-backed, so an immediate re-read would
// almost certainly return the same stale resourceVersion that caused the conflict. The
// SDK converts conflicts into a short (1s) requeue, by which point the informer cache has
// caught up.
func syncStatusHostToVirtual(ctx *synccontext.SyncContext, pObj, vObj *unstructured.Unstructured, virtualClient client.Client) error {
	if pObj == nil || vObj == nil {
		return nil
	}

	if virtualClient == nil {
		return nil
	}

	hostStatus, hostHasStatus, _ := unstructured.NestedMap(pObj.Object, "status")
	virtualStatus, virtualHasStatus, _ := unstructured.NestedMap(vObj.Object, "status")

	// Nothing to do if neither has status
	if !hostHasStatus && !virtualHasStatus {
		return nil
	}

	// Skip update if status is already identical.
	// Use Semantic.DeepEqual to handle mixed JSON number types (int64 vs float64).
	if hostHasStatus && virtualHasStatus && equality.Semantic.DeepEqual(hostStatus, virtualStatus) {
		return nil
	}

	vObjCopy := vObj.DeepCopy()

	if hostHasStatus {
		// Copy status from host to virtual
		_ = unstructured.SetNestedMap(vObjCopy.Object, hostStatus, "status")
	} else {
		// Host has no status, clear it from virtual
		unstructured.RemoveNestedField(vObjCopy.Object, "status")
	}

	// Return the update result directly. On conflict the controller requeues (the SDK
	// rewrites conflicts to a 1s RequeueAfter), which re-reads a settled cache instead
	// of hot-looping against the stale resourceVersion.
	return virtualClient.Status().Update(ctx, vObjCopy)
}

// hasSyncableFieldChanges checks if any syncable top-level fields changed between old and new objects.
// This is used by event filter predicates to determine if reconciliation is needed.
// If checkStatus is true, status field changes are also considered.
// This function uses direct map access instead of NestedFieldCopy to avoid allocations,
// since we only need read-only comparison. Uses Semantic.DeepEqual to handle mixed JSON
// number types (int64 vs float64) which can differ between API responses.
func hasSyncableFieldChanges(oldU, newU *unstructured.Unstructured, checkStatus bool) bool {
	// Check all syncable fields in new object
	for key := range newU.Object {
		if systemManagedFields[key] {
			continue
		}
		// Direct map access is safe here since we only read values for comparison
		oldVal := oldU.Object[key]
		newVal := newU.Object[key]
		if !equality.Semantic.DeepEqual(oldVal, newVal) {
			return true
		}
	}

	// Check for fields removed from new object
	for key := range oldU.Object {
		if systemManagedFields[key] {
			continue
		}
		if _, exists := newU.Object[key]; !exists {
			return true
		}
	}

	// Check status changes if requested
	if checkStatus {
		// Direct map access for status comparison
		oldStatus := oldU.Object["status"]
		newStatus := newU.Object["status"]
		if !equality.Semantic.DeepEqual(oldStatus, newStatus) {
			return true
		}
	}

	return false
}

// checkSelectorMatch checks if an object matches the configured selector and namespace filters.
// This shared logic is used by both ToHostSyncer and FromHostSyncer.
func checkSelectorMatch(obj client.Object, namespaced bool, cfg config.SyncerConfig, m *metrics.Recorder) (bool, filterReason) {
	if obj == nil {
		return false, filterNone
	}

	// Check namespace filtering (global + resource-level rules)
	if namespaced {
		namespace := obj.GetNamespace()
		if namespace == "" {
			return false, filterNamespace
		}
		if cfg.NamespaceMatcher != nil && !cfg.NamespaceMatcher.IsAllowed(namespace) {
			if m != nil {
				m.RecordNamespaceFiltered()
			}
			return false, filterNamespace
		}
	}

	// Check label selector
	if cfg.Resource.Selector == nil {
		return true, filterNone
	}
	if len(cfg.Resource.Selector.MatchLabels) == 0 {
		return true, filterNone
	}

	labels := obj.GetLabels()
	if labels == nil && len(cfg.Resource.Selector.MatchLabels) > 0 {
		return false, filterSelector
	}

	for k, v := range cfg.Resource.Selector.MatchLabels {
		if labels[k] != v {
			return false, filterSelector
		}
	}
	return true, filterNone
}

// buildEventFilterPredicate creates a predicate that filters out updates where
// only metadata fields that don't affect sync behaviour have changed.
// This reduces no-op reconciliations for changes like ManagedFields updates.
// The statusEnabledFn is called at runtime to determine if status changes should trigger reconciliation.
// Using a function allows deferring the check until after Register() sets hasStatusSubresource.
func buildEventFilterPredicate(gvk schema.GroupVersionKind, log *logging.Logger, statusEnabledFn func() bool) predicate.Predicate {
	return predicate.Funcs{
		CreateFunc: func(e ctrlevent.CreateEvent) bool {
			return true // Always process creates
		},
		DeleteFunc: func(e ctrlevent.DeleteEvent) bool {
			return true // Always process deletes
		},
		UpdateFunc: func(e ctrlevent.UpdateEvent) bool {
			// Skip if only resource version changed (metadata-only update)
			if e.ObjectOld == nil || e.ObjectNew == nil {
				return true
			}

			// Check if generation changed - if so, spec changed and we need to reconcile
			if e.ObjectOld.GetGeneration() != e.ObjectNew.GetGeneration() {
				return true
			}

			// Check if labels or annotations changed
			if !equality.Semantic.DeepEqual(e.ObjectOld.GetLabels(), e.ObjectNew.GetLabels()) {
				return true
			}
			if !equality.Semantic.DeepEqual(e.ObjectOld.GetAnnotations(), e.ObjectNew.GetAnnotations()) {
				return true
			}

			// Check if finalizers changed
			if !equality.Semantic.DeepEqual(e.ObjectOld.GetFinalizers(), e.ObjectNew.GetFinalizers()) {
				return true
			}

			// For unstructured objects, check if syncable fields changed
			// Call statusEnabledFn at runtime to get current status sync state
			oldU, oldOK := e.ObjectOld.(*unstructured.Unstructured)
			newU, newOK := e.ObjectNew.(*unstructured.Unstructured)
			if oldOK && newOK {
				if hasSyncableFieldChanges(oldU, newU, statusEnabledFn()) {
					return true
				}
			}

			// No meaningful changes detected, skip reconciliation
			log.Debug("Skipping reconciliation (no meaningful changes)",
				"kind", gvk.Kind,
				"name", e.ObjectNew.GetName(),
				"namespace", e.ObjectNew.GetNamespace())
			return false
		},
		GenericFunc: func(e ctrlevent.GenericEvent) bool {
			return true // Always process generic events
		},
	}
}

// firstNonEmpty returns the first non-empty string from the arguments.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// patchIsEffectivelyEmpty reports whether a merge patch contains no actual changes.
//
// patch.IsEmpty() only checks len(p) == 0, but CalculateMergePatch's DeleteAllExcept
// strips keys *inside* sub-objects (e.g. metadata) while leaving the now-empty parent —
// yielding {"metadata":{}}, which IsEmpty() reports as non-empty even though ApplyObject
// will no-op (VGSP-6). Without this, any persistent diff confined to stripped metadata
// (e.g. ownerReferences) records a success instead of a skip and re-emits an Updated
// event on every reconcile — the exact kine-growth mechanism the flood gate targets.
//
// We treat a patch as empty when every value recursively collapses to an empty map.
func patchIsEffectivelyEmpty(p patch.Patch) bool {
	if p.IsEmpty() {
		return true
	}
	return valueIsEmpty(map[string]interface{}(p))
}

func valueIsEmpty(v interface{}) bool {
	m, ok := v.(map[string]interface{})
	if !ok {
		// any non-map value (including non-empty slices/scalars) is a real change
		return false
	}
	for _, child := range m {
		if !valueIsEmpty(child) {
			return false
		}
	}
	return true
}

// pluginOwnedLabelKeys are label keys the plugin owns: the managed-by identity, the
// tenant-ownership label, and the vCluster marker. mergeExtraLabels must not let
// config-supplied extraLabels/globalExtraLabels clobber these once the plugin has
// stamped them, so plugin-owned labels always win over user-provided extra labels.
// Keys the plugin has NOT set on a given object still pass through (e.g. fromHost
// imports get tenant/managed-by from globalExtraLabels rather than applySyncLabels).
var pluginOwnedLabelKeys = map[string]bool{
	"kupe.cloud/managed-by": true,
	"kupe.cloud/tenant":     true,
	translate.MarkerLabel:   true,
}

// mergeExtraLabels merges additional labels onto an object. No-op if extra is nil or empty.
// Plugin-owned keys already present on the object are preserved (extra labels cannot
// override them); see pluginOwnedLabelKeys.
func mergeExtraLabels(obj client.Object, extra map[string]string) {
	if len(extra) == 0 {
		return
	}
	labels := obj.GetLabels()
	if labels == nil {
		labels = make(map[string]string, len(extra))
	}
	for k, v := range extra {
		if pluginOwnedLabelKeys[k] {
			if _, ok := labels[k]; ok {
				// Plugin already stamped this key — never let extra labels override it.
				continue
			}
		}
		labels[k] = v
	}
	obj.SetLabels(labels)
}
