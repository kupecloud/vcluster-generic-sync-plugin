package syncers

import (
	"github.com/loft-sh/vcluster/pkg/patcher"
	"github.com/loft-sh/vcluster/pkg/syncer"
	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	synctypes "github.com/loft-sh/vcluster/pkg/syncer/types"
	"github.com/loft-sh/vcluster/pkg/util/patch"
	"github.com/loft-sh/vcluster/pkg/util/translate"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
	"github.com/kupecloud/vcluster-generic-sync-plugin/metrics"
	"github.com/kupecloud/vcluster-generic-sync-plugin/patches"
)

// targetNamespaceAnnotation allows per-instance override of the target namespace
// in the virtual cluster. When set on a host object, the syncer creates the
// virtual object in this namespace instead of the config-level TargetNamespace.
const targetNamespaceAnnotation = "kupe.cloud/target-namespace"

// syncedFromAnnotation marks a virtual object as created by the fromHost syncer and
// records its host source as "{hostNamespace}/{hostName}". translate.VirtualMetadata
// strips the SDK's Name/Namespace provenance annotations, leaving no way to tell a
// syncer-created virtual object from a user-created one. We stamp this annotation in
// SyncToVirtual (and re-apply it on every Sync) and gate EVERY delete of a virtual
// object on it: orphan cleanup when the host source is gone (sync and mirror mode)
// and stale-copy cleanup when the selector no longer matches. A user's own object is
// never stamped and therefore never deleted (VGSP-3, VGSP-5).
const syncedFromAnnotation = "kupe.cloud/synced-from"

// FromHostSyncer syncs resources from host cluster to virtual cluster
type FromHostSyncer struct {
	name                 string
	gvk                  schema.GroupVersionKind
	cfg                  config.SyncerConfig
	namespaced           bool
	hasStatusSubresource bool
	targetNamespace      string
	virtualNamespace     string
	vclusterName         string
	patcher              *patches.Patcher
	log                  *logging.Logger
	tracer               *logging.ObjectTracer
	events               *logging.EventEmitter
	eventRecorder        events.EventRecorder
	metrics              *metrics.Recorder
}

// NewFromHostSyncer creates a new syncer for host to virtual synchronisation
func NewFromHostSyncer(ctx *synccontext.RegisterContext, gvk schema.GroupVersionKind, cfg config.SyncerConfig) (*FromHostSyncer, error) {
	log := logging.Log
	name := syncerName(gvk, config.FromHost)
	namespaced := true
	if resolved, err := resolveNamespaced(ctx, gvk); err != nil {
		log.Warning("Failed to resolve resource scope, defaulting to namespaced",
			"gvk", gvk.String(),
			"error", err)
	} else {
		namespaced = resolved
	}

	eventRecorder := ctx.VirtualManager.GetEventRecorder(name + "-syncer")

	// Only create EventEmitter if events are enabled
	var eventEmitter *logging.EventEmitter
	if cfg.EventsEnabled {
		eventEmitter = logging.NewEventEmitter(eventRecorder, string(config.FromHost), gvk.Kind)
	}

	s := &FromHostSyncer{
		name:                 name,
		gvk:                  gvk,
		cfg:                  cfg,
		namespaced:           namespaced,
		hasStatusSubresource: false,
		targetNamespace:      ctx.Config.HostNamespace,
		virtualNamespace:     cfg.Resource.TargetNamespace,
		vclusterName:         ctx.Config.Name,
		patcher:              patches.NewPatcher(cfg.Resource.Patches, ctx.Config.Name, ctx.Config.HostNamespace, cfg.Resource.SelectorIncludeOwnerLabels),
		log:                  log,
		tracer:               logging.NewObjectTracer(string(config.FromHost), gvk.Kind),
		events:               eventEmitter,
		eventRecorder:        eventRecorder,
		metrics:              metrics.NewRecorder(metrics.DirectionFromHost, gvk.Kind),
	}

	log.Info("FromHostSyncer created",
		"gvk", gvk.String(),
		"name", s.Name(),
		"statusSync", cfg.Resource.StatusSync,
		"patchCount", len(cfg.Resource.Patches),
		"maxConcurrentReconciles", cfg.MaxConcurrentReconciles,
		"eventFilteringEnabled", cfg.EventFilteringEnabled,
		"eventsEnabled", cfg.EventsEnabled,
		"namespaceFilterActive", cfg.NamespaceMatcher != nil && cfg.NamespaceMatcher.HasFilters(),
		"extraLabels", len(cfg.Resource.ExtraLabels))

	if cfg.NamespaceMatcher != nil && cfg.NamespaceMatcher.HasFilters() {
		log.Debug("Namespace filtering configured",
			"includes", cfg.NamespaceMatcher.GetEffectiveIncludes(),
			"excludes", cfg.NamespaceMatcher.GetEffectiveExcludes())
	}

	return s, nil
}

// Name returns the name of the syncer
func (s *FromHostSyncer) Name() string {
	return s.name
}

// Resource returns a new instance of the resource type being synced
func (s *FromHostSyncer) Resource() client.Object {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(s.gvk)
	return obj
}

// Migrate performs any necessary migrations
func (s *FromHostSyncer) Migrate(_ *synccontext.RegisterContext, _ synccontext.Mapper) error {
	return nil
}

// GroupVersionKind returns the GVK of the resource being synced
func (s *FromHostSyncer) GroupVersionKind() schema.GroupVersionKind {
	return s.gvk
}

// EventRecorder returns the event recorder
func (s *FromHostSyncer) EventRecorder() events.EventRecorder {
	return s.eventRecorder
}

// VirtualToHost translates a virtual cluster name to a host cluster name
func (s *FromHostSyncer) VirtualToHost(_ *synccontext.SyncContext, req types.NamespacedName, _ client.Object) types.NamespacedName {
	if req.Name == "" {
		return types.NamespacedName{}
	}
	if !s.namespaced {
		return types.NamespacedName{
			Name: req.Name,
		}
	}
	return types.NamespacedName{
		Name:      req.Name,
		Namespace: s.targetNamespace,
	}
}

// HostToVirtual translates a host cluster name to a virtual cluster name
func (s *FromHostSyncer) HostToVirtual(_ *synccontext.SyncContext, req types.NamespacedName, pObj client.Object) types.NamespacedName {
	if req.Name == "" {
		return types.NamespacedName{}
	}

	if pObj == nil {
		s.log.Debug("HostToVirtual: nil host object, returning empty",
			"kind", s.gvk.Kind,
			"requestedName", req.Name)
		return types.NamespacedName{}
	}

	// Only the source host namespace is a valid import source (VGSP-4); cache widening
	// can deliver objects from shared/platform namespaces that must not be imported.
	if s.namespaced && s.targetNamespace != "" && pObj.GetNamespace() != s.targetNamespace {
		return types.NamespacedName{}
	}

	// Deliberately NO selector check here: the SDK's enqueuePhysical drops host events
	// whose HostToVirtual result is empty, so filtering de-selected objects here would
	// make the "selector no longer matches" cleanup in Sync unreachable — a de-labelled
	// host object must still map to its canonical virtual location so the stale copy can
	// be deleted. Imports remain guarded: SyncToVirtual re-checks the selector before
	// creating anything.

	if !s.namespaced {
		return types.NamespacedName{
			Name: req.Name,
		}
	}

	ns := s.virtualNamespaceOrDefault()
	if ann := pObj.GetAnnotations()[targetNamespaceAnnotation]; ann != "" {
		// Reuse the config-level validator so the annotation override enforces the same
		// rules as targetNamespace/hostNamespace — RFC 1123 AND the kube-* system-namespace
		// rejection (VGSP-8), which the previous regex-only check skipped (LOW-2).
		if err := config.ValidateTargetNamespace(ann, targetNamespaceAnnotation); err != nil {
			s.log.Warning("HostToVirtual: ignoring invalid target-namespace annotation",
				"kind", s.gvk.Kind,
				"host", req.Namespace+"/"+req.Name,
				"annotation", ann,
				"reason", err.Error())
		} else {
			ns = ann
		}
	}

	return types.NamespacedName{
		Name:      req.Name,
		Namespace: ns,
	}
}

// IsManaged checks if the host object should be managed by this syncer
func (s *FromHostSyncer) IsManaged(ctx *synccontext.SyncContext, pObj client.Object) (bool, error) {
	if pObj == nil {
		return false, nil
	}

	// Pin to the source host namespace (the vCluster's own namespace). The host cache
	// is widened to ALL hostNamespace overrides (e.g. argocd, observability) by
	// modifyHostManager, so without this guard a fromHost syncer would also receive —
	// and import into the tenant vCluster — unmarked objects living in shared/platform
	// namespaces. This is independent of config filters and enforces the documented
	// "read only from the host vcluster namespace" contract (VGSP-4). targetNamespace
	// is always set from ctx.Config.HostNamespace in production; only unset in tests.
	if s.namespaced && s.targetNamespace != "" && pObj.GetNamespace() != s.targetNamespace {
		return false, nil
	}

	// Deliberately NO selector check here (mirroring ToHostSyncer.IsManaged): the SDK
	// consults IsManaged both when enqueueing host events and when pairing objects in
	// getObjects, so excluding de-selected objects here would make them invisible to
	// reconciliation and the "selector no longer matches" cleanup in Sync unreachable —
	// the previously imported copy (including credential material) would stay in the
	// tenant vCluster forever. Sync/SyncToVirtual re-check the selector themselves.

	if labels := pObj.GetLabels(); labels != nil {
		if labels[translate.MarkerLabel] != "" {
			return false, nil
		}
	}

	return true, nil
}

func (s *FromHostSyncer) matchesSelector(obj client.Object) (bool, filterReason) {
	return checkSelectorMatch(obj, s.namespaced, s.cfg, s.metrics)
}

// Syncer returns the sync implementation wrapped for generic use
func (s *FromHostSyncer) Syncer() synctypes.Sync[client.Object] {
	return syncer.ToGenericSyncer(s)
}

// Register is called during syncer registration
// Ensures the CRD exists on the virtual cluster by copying from host
func (s *FromHostSyncer) Register(ctx *synccontext.RegisterContext) error {
	s.log.Debug("Registering syncer", "gvk", s.gvk.String())

	// For CRDs, ensure the CRD exists on the virtual cluster (copy from host).
	// Skip for core API resources (Secrets, ConfigMaps, etc.) which don't have CRDs.
	if !isCoreAPIResource(s.gvk) {
		isClusterScoped, _, err := translate.EnsureCRDFromPhysicalCluster(
			ctx.Context,
			ctx.HostManager.GetConfig(),
			ctx.VirtualManager.GetConfig(),
			s.gvk,
		)
		if err != nil {
			s.log.Error(err, "Failed to ensure CRD from physical cluster", "gvk", s.gvk.String())
			// Don't fail - the CRD might already exist or be managed differently
		} else {
			s.namespaced = !isClusterScoped
		}
	}

	// Detect status subresource via API discovery for all resources.
	// Status flows host → virtual, so we need BOTH clusters to have the status subresource:
	// - Host: where we read status from
	// - Virtual: where we write status to via Status().Update()
	if s.cfg.Resource.StatusSync {
		s.hasStatusSubresource = detectStatusSubresource(ctx, s.gvk, s.cfg, s.log)
	}

	if resolved, err := resolveNamespaced(ctx, s.gvk); err == nil {
		s.namespaced = resolved
	} else {
		s.log.Warning("Failed to resolve resource scope after CRD ensure", "gvk", s.gvk.String(), "error", err)
	}

	return nil
}

var _ synctypes.Syncer = &FromHostSyncer{}
var _ synctypes.ControllerStarter = &FromHostSyncer{}
var _ synctypes.ControllerModifier = &FromHostSyncer{}

// ModifyController implements ControllerModifier to customise controller options
func (s *FromHostSyncer) ModifyController(_ *synccontext.RegisterContext, bld *builder.Builder) (*builder.Builder, error) {
	bld = bld.WithOptions(controller.Options{
		MaxConcurrentReconciles: s.cfg.MaxConcurrentReconciles,
	})

	// Add event filtering to skip no-op reconciliations if enabled
	// Note: WithEventFilter only applies to virtual cluster watches (.Watches),
	// not to host cluster watches (.WatchesRawSource). For FromHostSyncer,
	// the primary source is the host, so this mainly filters orphan detection events.
	if s.cfg.EventFilteringEnabled {
		bld = bld.WithEventFilter(s.eventFilterPredicate())
	}

	return bld, nil
}

// eventFilterPredicate returns a predicate that filters out updates where
// only metadata fields that don't affect sync behaviour have changed.
// This reduces no-op reconciliations for changes like ManagedFields updates.
func (s *FromHostSyncer) eventFilterPredicate() predicate.Predicate {
	// For FromHost, check status changes only if status sync will actually apply.
	// Pass statusEnabled as a function so it's evaluated at runtime after Register()
	// sets hasStatusSubresource, not at controller setup time.
	return buildEventFilterPredicate(s.gvk, s.log, s.statusEnabled)
}

// SyncToHost is called when a virtual object was created (orphaned virtual object)
func (s *FromHostSyncer) SyncToHost(ctx *synccontext.SyncContext, event *synccontext.SyncToHostEvent[*unstructured.Unstructured]) (ctrl.Result, error) {
	s.metrics.RecordReconcile()
	defer s.metrics.TimeReconcile()()

	vObj := event.Virtual

	if vObj == nil {
		s.log.Warning("SyncToHost: received nil virtual object", "kind", s.gvk.Kind)
		return ctrl.Result{}, nil
	}

	s.tracer.TraceIncoming("orphan", vObj)

	// Mirror-mode cleanup is gated on the provenance annotation: the mirror only ever
	// writes syncer-created copies (all stamped in SyncToVirtual/Sync), but the SDK
	// pairs ANY virtual object of this GVK in ANY namespace via VirtualToHost, so an
	// unconditional delete here would silently destroy a tenant's own Gateway or
	// GatewayClass. Non-provenance objects fall through to the user-created no-op below.
	if s.cfg.Resource.DefaultMode() == config.Mirror && vObj.GetAnnotations()[syncedFromAnnotation] != "" {
		s.log.Debug("SyncToHost: deleting virtual object (mirror mode)",
			"kind", s.gvk.Kind,
			"virtual", vObj.GetNamespace()+"/"+vObj.GetName())
		timer := s.metrics.NewOperationTimer(metrics.OperationDelete)
		result, err := patcher.DeleteVirtualObject(ctx, vObj, nil, "mirror mode is read-only")
		timer.ObserveDuration()
		if err != nil {
			syncErr := logging.NewSyncError("delete", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.FromHost), err)
			s.log.Error(syncErr, "SyncToHost: failed to delete virtual object in mirror mode",
				"errorType", syncErr.Type,
				"retryable", syncErr.Retryable)
			s.metrics.RecordOperationError(metrics.OperationDelete)
			s.metrics.RecordError(metrics.ClassifyError(err))
			s.events.EmitDeleteFailed(vObj, err)
			s.tracer.TraceResult("delete", vObj, syncErr)
			return logging.RequeueForError(syncErr)
		}
		s.metrics.RecordOperationSuccess(metrics.OperationDelete)
		s.metrics.DecResourcesManaged()
		s.events.EmitDeleted(vObj, "mirror mode is read-only")
		s.tracer.TraceResult("delete", vObj, nil)
		return result, nil
	}

	// Propagate host deletions: when the host source is gone the
	// orphaned virtual copy is stale forever (stale spec AND stale status, since the
	// host source that would clear it no longer exists). Delete it — but ONLY if it
	// carries our provenance annotation, proving the syncer created it. A user's own
	// object with the same name (never stamped) is left untouched (VGSP-3).
	if vObj.GetAnnotations()[syncedFromAnnotation] != "" {
		s.log.Info("SyncToHost: host source deleted, removing orphaned synced virtual object",
			"kind", s.gvk.Kind,
			"virtual", vObj.GetNamespace()+"/"+vObj.GetName(),
			"syncedFrom", vObj.GetAnnotations()[syncedFromAnnotation])
		timer := s.metrics.NewOperationTimer(metrics.OperationDelete)
		result, err := patcher.DeleteVirtualObject(ctx, vObj, nil, "host source object was deleted")
		timer.ObserveDuration()
		if err != nil {
			syncErr := logging.NewSyncError("delete", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.FromHost), err)
			s.log.Error(syncErr, "SyncToHost: failed to delete orphaned virtual object",
				"errorType", syncErr.Type,
				"retryable", syncErr.Retryable)
			s.metrics.RecordOperationError(metrics.OperationDelete)
			s.metrics.RecordError(metrics.ClassifyError(err))
			s.events.EmitDeleteFailed(vObj, err)
			s.tracer.TraceResult("delete", vObj, syncErr)
			return logging.RequeueForError(syncErr)
		}
		s.metrics.RecordOperationSuccess(metrics.OperationDelete)
		s.metrics.DecResourcesManaged()
		s.events.EmitDeleted(vObj, "host source object was deleted")
		s.tracer.TraceResult("delete", vObj, nil)
		return result, nil
	}

	s.log.Info("SyncToHost: orphaned virtual object (user-created, not deleting)",
		"kind", s.gvk.Kind,
		"virtual", vObj.GetNamespace()+"/"+vObj.GetName())
	return ctrl.Result{}, nil
}

// Sync is called when both virtual and host objects exist
func (s *FromHostSyncer) Sync(ctx *synccontext.SyncContext, event *synccontext.SyncEvent[*unstructured.Unstructured]) (ctrl.Result, error) {
	s.metrics.RecordReconcile()
	defer s.metrics.TimeReconcile()()

	vObj := event.Virtual
	pObj := event.Host

	if vObj == nil || pObj == nil {
		s.log.Warning("Sync: received nil object",
			"kind", s.gvk.Kind,
			"virtualNil", vObj == nil,
			"hostNil", pObj == nil)
		return ctrl.Result{}, nil
	}

	s.tracer.TraceIncoming("update-host", pObj)
	s.tracer.TraceIncoming("update-virtual", vObj)

	statusEnabled := s.statusEnabled()

	if matches, reason := s.matchesSelector(pObj); !matches {
		if reason == filterSelector {
			s.metrics.RecordSelectorFiltered()
		}
		// Only delete the virtual copy if it carries our provenance annotation, proving
		// the syncer created it. A user-created object paired by name via VirtualToHost
		// must never be deleted (VGSP-5).
		if vObj.GetAnnotations()[syncedFromAnnotation] == "" {
			s.log.Debug("Sync: selector/filter no longer matches, but virtual object has no provenance annotation, leaving untouched",
				"kind", s.gvk.Kind,
				"host", pObj.GetNamespace()+"/"+pObj.GetName(),
				"virtual", vObj.GetNamespace()+"/"+vObj.GetName())
			return ctrl.Result{}, nil
		}
		s.log.Info("Sync: selector/filter no longer matches, deleting synced virtual object",
			"kind", s.gvk.Kind,
			"host", pObj.GetNamespace()+"/"+pObj.GetName(),
			"virtual", vObj.GetNamespace()+"/"+vObj.GetName())
		timer := s.metrics.NewOperationTimer(metrics.OperationDelete)
		result, err := patcher.DeleteVirtualObject(ctx, vObj, nil, "selector/filter no longer matches")
		timer.ObserveDuration()
		if err != nil {
			syncErr := logging.NewSyncError("delete", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.FromHost), err)
			s.log.Error(syncErr, "Sync: failed to delete virtual object after selector mismatch",
				"errorType", syncErr.Type,
				"retryable", syncErr.Retryable)
			s.metrics.RecordOperationError(metrics.OperationDelete)
			s.metrics.RecordError(metrics.ClassifyError(err))
			s.tracer.TraceResult("delete", vObj, syncErr)
			return logging.RequeueForError(syncErr)
		}
		s.metrics.RecordOperationSuccess(metrics.OperationDelete)
		s.metrics.DecResourcesManaged()
		s.tracer.TraceResult("delete", vObj, nil)
		return result, nil
	}

	// Guard against hijacking a tenant's own object. VirtualToHost maps any virtual
	// name to {targetNamespace}/{name} regardless of the virtual namespace, so the SDK
	// can pair a user-created object (same name, different virtual namespace, or one not
	// at the target-namespace-annotation override) with this host object, and the update
	// below would overwrite the user's spec/labels with host content. Only proceed when
	// the virtual object sits at the canonical location HostToVirtual derives from the
	// host object, including any target-namespace annotation override (VGSP-5).
	if s.namespaced {
		canonical := s.HostToVirtual(ctx, types.NamespacedName{Name: pObj.GetName(), Namespace: pObj.GetNamespace()}, pObj)
		if canonical.Name == "" || canonical.Namespace != vObj.GetNamespace() {
			// The paired virtual object is not at the canonical import location. If it
			// carries THIS host source's provenance, it is the syncer's own copy left
			// stranded at an old location when the kupe.cloud/target-namespace annotation
			// changed (old namespace A → new canonical namespace B): the host source still
			// exists so it never reaches the orphan path, and it would otherwise sit frozen
			// with stale (for Secrets: still-live credential) data forever. Delete it — the
			// fresh copy is created at the canonical location by SyncToVirtual. A
			// user-created object (no provenance) or one synced from a different source is
			// left untouched (MEDIUM-2 / VGSP-5).
			if vObj.GetAnnotations()[syncedFromAnnotation] == provenanceSource(pObj) {
				s.log.Info("Sync: target-namespace changed, removing stale synced virtual copy at old location",
					"kind", s.gvk.Kind,
					"virtual", vObj.GetNamespace()+"/"+vObj.GetName(),
					"canonical", canonical.Namespace+"/"+canonical.Name,
					"syncedFrom", vObj.GetAnnotations()[syncedFromAnnotation])
				timer := s.metrics.NewOperationTimer(metrics.OperationDelete)
				result, err := patcher.DeleteVirtualObject(ctx, vObj, nil, "target-namespace changed; removing stale synced copy at old location")
				timer.ObserveDuration()
				if err != nil {
					syncErr := logging.NewSyncError("delete", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.FromHost), err)
					s.log.Error(syncErr, "Sync: failed to delete stale virtual copy after target-namespace change",
						"errorType", syncErr.Type,
						"retryable", syncErr.Retryable)
					s.metrics.RecordOperationError(metrics.OperationDelete)
					s.metrics.RecordError(metrics.ClassifyError(err))
					s.tracer.TraceResult("delete", vObj, syncErr)
					return logging.RequeueForError(syncErr)
				}
				s.metrics.RecordOperationSuccess(metrics.OperationDelete)
				s.metrics.DecResourcesManaged()
				s.tracer.TraceResult("delete", vObj, nil)
				return result, nil
			}
			s.log.Debug("Sync: virtual object is not at the canonical import location, treating as unrelated",
				"kind", s.gvk.Kind,
				"virtual", vObj.GetNamespace()+"/"+vObj.GetName(),
				"canonical", canonical.Namespace+"/"+canonical.Name)
			return ctrl.Result{}, nil
		}
	}

	s.log.Debug("Sync: updating virtual object",
		"kind", s.gvk.Kind,
		"host", pObj.GetNamespace()+"/"+pObj.GetName(),
		"virtual", vObj.GetNamespace()+"/"+vObj.GetName())

	updated := vObj.DeepCopy()

	copySyncableFields(pObj, updated)

	updated.SetAnnotations(translate.VirtualAnnotations(pObj, vObj))
	updated.SetLabels(translate.VirtualLabels(pObj, vObj))
	mergeExtraLabels(updated, s.cfg.Resource.ExtraLabels)
	// Re-stamp provenance: VirtualAnnotations is derived from host annotations and
	// would otherwise drop this plugin-set marker, leaving objects created before
	// this fix (or after the first update) undeletable on host deletion (VGSP-3).
	stampProvenance(updated, pObj)

	if err := s.applyPatches(ctx, pObj, updated); err != nil {
		syncErr := logging.NewSyncError("patch", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.FromHost), err)
		s.log.Error(syncErr, "Sync: failed to apply patches",
			"errorType", syncErr.Type,
			"retryable", syncErr.Retryable)
		s.metrics.RecordOperationError(metrics.OperationUpdate)
		s.metrics.RecordError(metrics.ClassifyError(err))
		s.events.EmitPatchFailed(vObj, err)
		s.tracer.TraceResult("update", updated, syncErr)
		return logging.RequeueForError(syncErr)
	}

	s.tracer.TraceDiff("update", vObj, updated)

	// Precompute the merge patch so we can detect no-op reconciles below.
	// ApplyObject recomputes internally — duplicate work is cheap and keeps
	// the existing apply path unchanged.
	objPatch, patchErr := patch.CalculateMergePatch(vObj, updated)
	if patchErr != nil {
		syncErr := logging.NewSyncError("patch", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.FromHost), patchErr)
		s.log.Error(syncErr, "Sync: failed to calculate merge patch",
			"errorType", syncErr.Type,
			"retryable", syncErr.Retryable)
		s.metrics.RecordOperationError(metrics.OperationUpdate)
		s.metrics.RecordError(metrics.ClassifyError(patchErr))
		s.tracer.TraceResult("update", updated, syncErr)
		return logging.RequeueForError(syncErr)
	}

	// Use ApplyObject with beforeObject for proper merge patch calculation
	// This is more efficient than full Update as it calculates and sends only the diff
	timer := s.metrics.NewOperationTimer(metrics.OperationUpdate)
	err := patcher.ApplyObject(ctx, vObj, updated, synccontext.SyncHostToVirtual, statusEnabled)
	timer.ObserveDuration()
	if err != nil {
		syncErr := logging.NewSyncError("update", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.FromHost), err)
		s.log.Error(syncErr, "Sync: failed to update virtual object",
			"errorType", syncErr.Type,
			"retryable", syncErr.Retryable)
		s.metrics.RecordOperationError(metrics.OperationUpdate)
		s.metrics.RecordError(metrics.ClassifyError(err))
		s.events.EmitUpdateFailed(vObj, err)
		s.tracer.TraceResult("update", updated, syncErr)
		return logging.RequeueForError(syncErr)
	}

	if patchIsEffectivelyEmpty(objPatch) {
		s.metrics.RecordOperationSkipped(metrics.OperationUpdate)
	} else {
		s.metrics.RecordOperationSuccess(metrics.OperationUpdate)
	}
	s.tracer.TraceResult("update", updated, nil)

	if statusEnabled {
		if err := syncStatusHostToVirtual(ctx, pObj, vObj, ctx.VirtualClient); err != nil {
			syncErr := logging.NewSyncError("status", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.FromHost), err)
			s.log.Error(syncErr, "Sync: failed to sync status",
				"errorType", syncErr.Type,
				"retryable", syncErr.Retryable)
			s.metrics.RecordError(metrics.ClassifyError(err))
			s.events.EmitSyncFailed(vObj, err)
			return logging.RequeueForError(syncErr)
		}
	}

	// Skip the Updated event when the patch was empty. statusSync triggers a
	// reconcile every time the host controller updates status; without this
	// guard each tick writes an Event to kine, growing the backing DB
	// unboundedly and eventually wedging the vcluster apiserver. The
	// `skipped` operation counter above lets us alert on sustained hot loops.
	if !patchIsEffectivelyEmpty(objPatch) {
		s.events.EmitUpdated(vObj)
	}

	return ctrl.Result{}, nil
}

// SyncToVirtual is called when a host object exists but virtual doesn't (import)
func (s *FromHostSyncer) SyncToVirtual(ctx *synccontext.SyncContext, event *synccontext.SyncToVirtualEvent[*unstructured.Unstructured]) (ctrl.Result, error) {
	s.metrics.RecordReconcile()
	defer s.metrics.TimeReconcile()()

	pObj := event.Host

	if pObj == nil {
		s.log.Warning("SyncToVirtual: received nil host object", "kind", s.gvk.Kind)
		return ctrl.Result{}, nil
	}

	s.tracer.TraceIncoming("create", pObj)

	if matches, reason := s.matchesSelector(pObj); !matches {
		if reason == filterSelector {
			s.metrics.RecordSelectorFiltered()
		}
		return ctrl.Result{}, nil
	}

	virtualName := s.HostToVirtual(ctx, types.NamespacedName{
		Name:      pObj.GetName(),
		Namespace: pObj.GetNamespace(),
	}, pObj)

	if virtualName.Name == "" {
		return ctrl.Result{}, nil
	}

	s.log.Info("SyncToVirtual: creating virtual object",
		"kind", s.gvk.Kind,
		"host", pObj.GetNamespace()+"/"+pObj.GetName(),
		"virtual", virtualName.Namespace+"/"+virtualName.Name)

	vObj := translate.VirtualMetadata(pObj, virtualName)
	mergeExtraLabels(vObj, s.cfg.Resource.ExtraLabels)
	stampProvenance(vObj, pObj)

	// Strip status before create when statusSync is disabled.
	// translate.VirtualMetadata deep-copies the entire object including status,
	// but we only want status set via the status subresource when enabled.
	if !s.statusEnabled() {
		stripStatus(vObj)
	}

	if err := s.applyPatches(ctx, pObj, vObj); err != nil {
		syncErr := logging.NewSyncError("patch", s.gvk.Kind, virtualName.Namespace, virtualName.Name, string(config.FromHost), err)
		s.log.Error(syncErr, "SyncToVirtual: failed to apply patches",
			"errorType", syncErr.Type,
			"retryable", syncErr.Retryable)
		s.metrics.RecordOperationError(metrics.OperationCreate)
		s.metrics.RecordError(metrics.ClassifyError(err))
		s.events.EmitPatchFailed(vObj, err)
		s.tracer.TraceResult("create", vObj, syncErr)
		return logging.RequeueForError(syncErr)
	}

	s.tracer.TracePatched("create", pObj, vObj)

	if ctx.VirtualClient == nil {
		s.log.Error(nil, "SyncToVirtual: VirtualClient is nil, cannot create virtual object")
		return ctrl.Result{}, nil
	}

	// Ensure the target virtual namespace exists. Without this, Create fails NotFound
	// forever — controller-runtime retries indefinitely, a Warning event fires per
	// attempt, and the resource never appears for the tenant (VGSP-8).
	if s.namespaced && virtualName.Namespace != "" {
		if err := s.ensureVirtualNamespace(ctx, virtualName.Namespace); err != nil {
			syncErr := logging.NewSyncError("create", s.gvk.Kind, virtualName.Namespace, virtualName.Name, string(config.FromHost), err)
			s.log.Error(syncErr, "SyncToVirtual: failed to ensure target namespace",
				"errorType", syncErr.Type,
				"retryable", syncErr.Retryable)
			s.metrics.RecordOperationError(metrics.OperationCreate)
			s.metrics.RecordError(metrics.ClassifyError(err))
			s.tracer.TraceResult("create", vObj, syncErr)
			return logging.RequeueForError(syncErr)
		}
	}

	timer := s.metrics.NewOperationTimer(metrics.OperationCreate)
	err := ctx.VirtualClient.Create(ctx, vObj)
	timer.ObserveDuration()
	if err != nil {
		syncErr := logging.NewSyncError("create", s.gvk.Kind, virtualName.Namespace, virtualName.Name, string(config.FromHost), err)
		s.log.Error(syncErr, "SyncToVirtual: failed to create virtual object",
			"errorType", syncErr.Type,
			"retryable", syncErr.Retryable)
		s.metrics.RecordOperationError(metrics.OperationCreate)
		s.metrics.RecordError(metrics.ClassifyError(err))
		s.events.EmitCreateFailed(vObj, err)
		s.tracer.TraceResult("create", vObj, syncErr)
		return logging.RequeueForError(syncErr)
	}

	s.metrics.RecordOperationSuccess(metrics.OperationCreate)
	s.metrics.IncResourcesManaged()
	// Use just the name for cluster-scoped resources, namespace/name for namespaced
	targetName := pObj.GetName()
	if ns := pObj.GetNamespace(); ns != "" {
		targetName = ns + "/" + targetName
	}
	s.events.EmitCreated(vObj, targetName)

	s.tracer.TraceResult("create", vObj, nil)

	return ctrl.Result{}, nil
}

// provenanceSource returns the "{namespace}/{name}" (or "{name}" for cluster-scoped)
// identifier of a host object, as recorded in the syncedFromAnnotation.
func provenanceSource(pObj client.Object) string {
	source := pObj.GetName()
	if ns := pObj.GetNamespace(); ns != "" {
		source = ns + "/" + source
	}
	return source
}

// stampProvenance records the host source on the virtual object so SyncToHost can
// distinguish syncer-created objects from user-created ones (VGSP-3).
func stampProvenance(vObj, pObj client.Object) {
	annotations := vObj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[syncedFromAnnotation] = provenanceSource(pObj)
	vObj.SetAnnotations(annotations)
}

func (s *FromHostSyncer) applyPatches(ctx *synccontext.SyncContext, pObj, vObj client.Object) error {
	return s.patcher.ApplyToVirtual(ctx, pObj, vObj)
}

func (s *FromHostSyncer) statusEnabled() bool {
	return s.cfg.Resource.StatusSync && s.hasStatusSubresource && s.cfg.Resource.DefaultMode() == config.Sync
}

// ensureVirtualNamespace idempotently creates the target namespace in the virtual
// cluster. A concurrent create (AlreadyExists) is treated as success (VGSP-8).
func (s *FromHostSyncer) ensureVirtualNamespace(ctx *synccontext.SyncContext, name string) error {
	ns := &unstructured.Unstructured{}
	ns.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Namespace"})
	ns.SetName(name)
	key := types.NamespacedName{Name: name}
	existing := &unstructured.Unstructured{}
	existing.SetGroupVersionKind(ns.GroupVersionKind())
	if err := ctx.VirtualClient.Get(ctx, key, existing); err == nil {
		return nil
	} else if !apierrors.IsNotFound(err) {
		return err
	}
	if err := ctx.VirtualClient.Create(ctx, ns); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	s.log.Info("SyncToVirtual: created target namespace", "namespace", name)
	return nil
}

func (s *FromHostSyncer) virtualNamespaceOrDefault() string {
	if s.virtualNamespace != "" {
		return s.virtualNamespace
	}
	return "default"
}
