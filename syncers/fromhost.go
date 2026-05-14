package syncers

import (
	"regexp"

	"github.com/loft-sh/vcluster/pkg/patcher"
	"github.com/loft-sh/vcluster/pkg/syncer"
	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	synctypes "github.com/loft-sh/vcluster/pkg/syncer/types"
	"github.com/loft-sh/vcluster/pkg/util/patch"
	"github.com/loft-sh/vcluster/pkg/util/translate"
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

// validNamespaceRe matches valid Kubernetes namespace names (RFC 1123 DNS label).
var validNamespaceRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

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

	if matches, _ := s.matchesSelector(pObj); !matches {
		return types.NamespacedName{}
	}

	if !s.namespaced {
		return types.NamespacedName{
			Name: req.Name,
		}
	}

	ns := s.virtualNamespaceOrDefault()
	if ann := pObj.GetAnnotations()[targetNamespaceAnnotation]; ann != "" {
		if !validNamespaceRe.MatchString(ann) {
			s.log.Warning("HostToVirtual: ignoring invalid target-namespace annotation",
				"kind", s.gvk.Kind,
				"host", req.Namespace+"/"+req.Name,
				"annotation", ann)
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

	if matches, _ := s.matchesSelector(pObj); !matches {
		return false, nil
	}

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

	if s.cfg.Resource.DefaultMode() == config.Mirror {
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
			return logging.RequeueResult(syncErr), syncErr
		}
		s.metrics.RecordOperationSuccess(metrics.OperationDelete)
		s.metrics.DecResourcesManaged()
		s.events.EmitDeleted(vObj, "mirror mode is read-only")
		s.tracer.TraceResult("delete", vObj, nil)
		return result, nil
	}

	s.log.Info("SyncToHost: orphaned virtual object",
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
		s.log.Debug("Sync: selector/filter no longer matches, deleting virtual object",
			"kind", s.gvk.Kind,
			"host", pObj.GetNamespace()+"/"+pObj.GetName(),
			"virtual", vObj.GetNamespace()+"/"+vObj.GetName())
		if reason == filterSelector {
			s.metrics.RecordSelectorFiltered()
		}
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
			return logging.RequeueResult(syncErr), syncErr
		}
		s.metrics.RecordOperationSuccess(metrics.OperationDelete)
		s.metrics.DecResourcesManaged()
		s.tracer.TraceResult("delete", vObj, nil)
		return result, nil
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

	if err := s.applyPatches(ctx, pObj, updated); err != nil {
		syncErr := logging.NewSyncError("patch", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.FromHost), err)
		s.log.Error(syncErr, "Sync: failed to apply patches",
			"errorType", syncErr.Type,
			"retryable", syncErr.Retryable)
		s.metrics.RecordOperationError(metrics.OperationUpdate)
		s.metrics.RecordError(metrics.ClassifyError(err))
		s.events.EmitPatchFailed(vObj, err)
		s.tracer.TraceResult("update", updated, syncErr)
		return logging.RequeueResult(syncErr), syncErr
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
		return logging.RequeueResult(syncErr), syncErr
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
		return logging.RequeueResult(syncErr), syncErr
	}

	s.metrics.RecordOperationSuccess(metrics.OperationUpdate)
	s.tracer.TraceResult("update", updated, nil)

	if statusEnabled {
		if err := syncStatusHostToVirtual(ctx, pObj, vObj, ctx.VirtualClient); err != nil {
			syncErr := logging.NewSyncError("status", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.FromHost), err)
			s.log.Error(syncErr, "Sync: failed to sync status",
				"errorType", syncErr.Type,
				"retryable", syncErr.Retryable)
			s.metrics.RecordError(metrics.ClassifyError(err))
			s.events.EmitSyncFailed(vObj, err)
			return logging.RequeueResult(syncErr), syncErr
		}
	}

	// Skip the Updated event when the patch was empty. statusSync triggers a
	// reconcile every time the host controller updates status; without this
	// guard each tick writes an Event to kine, growing the backing DB
	// unboundedly and eventually wedging the vcluster apiserver.
	if !objPatch.IsEmpty() {
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
		return logging.RequeueResult(syncErr), syncErr
	}

	s.tracer.TracePatched("create", pObj, vObj)

	if ctx.VirtualClient == nil {
		s.log.Error(nil, "SyncToVirtual: VirtualClient is nil, cannot create virtual object")
		return ctrl.Result{}, nil
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
		return logging.RequeueResult(syncErr), syncErr
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

func (s *FromHostSyncer) applyPatches(ctx *synccontext.SyncContext, pObj, vObj client.Object) error {
	return s.patcher.ApplyToVirtual(ctx, pObj, vObj)
}

func (s *FromHostSyncer) statusEnabled() bool {
	return s.cfg.Resource.StatusSync && s.hasStatusSubresource && s.cfg.Resource.DefaultMode() == config.Sync
}

func (s *FromHostSyncer) virtualNamespaceOrDefault() string {
	if s.virtualNamespace != "" {
		return s.virtualNamespace
	}
	return "default"
}
