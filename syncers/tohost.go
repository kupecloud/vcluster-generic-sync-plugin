package syncers

import (
	"strings"

	"github.com/loft-sh/vcluster/pkg/patcher"
	"github.com/loft-sh/vcluster/pkg/syncer"
	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	synctypes "github.com/loft-sh/vcluster/pkg/syncer/types"
	"github.com/loft-sh/vcluster/pkg/util/translate"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
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

// ToHostSyncer syncs resources from virtual cluster to host cluster
type ToHostSyncer struct {
	name                  string
	gvk                   schema.GroupVersionKind
	cfg                   config.SyncerConfig
	namespaced            bool
	hasStatusSubresource  bool
	patcherFn             *patches.Patcher
	log                   *logging.Logger
	tracer                *logging.ObjectTracer
	events                *logging.EventEmitter
	eventRecorder         record.EventRecorder
	hostNamespace         string
	vclusterName          string
	vclusterHostNamespace string
	metrics               *metrics.Recorder
}

// NewToHostSyncer creates a new syncer for virtual to host synchronization
func NewToHostSyncer(ctx *synccontext.RegisterContext, gvk schema.GroupVersionKind, cfg config.SyncerConfig) (*ToHostSyncer, error) {
	log := logging.Log

	name := syncerName(gvk, config.ToHost)
	namespaced := true
	if resolved, err := resolveNamespaced(ctx, gvk); err != nil {
		log.Warning("Failed to resolve resource scope, defaulting to namespaced",
			"gvk", gvk.String(),
			"error", err)
	} else {
		namespaced = resolved
	}

	eventRecorder := ctx.VirtualManager.GetEventRecorderFor(name + "-syncer")

	// Only create EventEmitter if events are enabled
	var events *logging.EventEmitter
	if cfg.EventsEnabled {
		events = logging.NewEventEmitter(eventRecorder, string(config.ToHost), gvk.Kind)
	}

	s := &ToHostSyncer{
		name:                  name,
		gvk:                   gvk,
		cfg:                   cfg,
		namespaced:            namespaced,
		hasStatusSubresource:  false,
		patcherFn:             patches.NewPatcher(cfg.Resource.Patches, ctx.Config.Name, ctx.Config.HostNamespace, cfg.Resource.SelectorIncludeOwnerLabels),
		log:                   log,
		tracer:                logging.NewObjectTracer(string(config.ToHost), gvk.Kind),
		events:                events,
		eventRecorder:         eventRecorder,
		hostNamespace:         firstNonEmpty(cfg.Resource.HostNamespace, ctx.Config.HostNamespace),
		vclusterName:          ctx.Config.Name,
		vclusterHostNamespace: ctx.Config.HostNamespace,
		metrics:               metrics.NewRecorder(metrics.DirectionToHost, gvk.Kind),
	}

	log.Info("ToHostSyncer created",
		"gvk", gvk.String(),
		"name", s.Name(),
		"statusSync", cfg.Resource.StatusSync,
		"patchCount", len(cfg.Resource.Patches),
		"maxConcurrentReconciles", cfg.MaxConcurrentReconciles,
		"eventFilteringEnabled", cfg.EventFilteringEnabled,
		"eventsEnabled", cfg.EventsEnabled,
		"namespaceFilterActive", cfg.NamespaceMatcher != nil && cfg.NamespaceMatcher.HasFilters(),
		"hostNamespace", s.hostNamespace,
		"extraLabels", len(cfg.Resource.ExtraLabels))

	if cfg.NamespaceMatcher != nil && cfg.NamespaceMatcher.HasFilters() {
		log.Debug("Namespace filtering configured",
			"includes", cfg.NamespaceMatcher.GetEffectiveIncludes(),
			"excludes", cfg.NamespaceMatcher.GetEffectiveExcludes())
	}

	return s, nil
}

// Name returns the syncer name
func (s *ToHostSyncer) Name() string {
	return s.name
}

// Resource returns a new instance of the synced object
func (s *ToHostSyncer) Resource() client.Object {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(s.gvk)
	return obj
}

// EventRecorder returns the event recorder
func (s *ToHostSyncer) EventRecorder() record.EventRecorder {
	return s.eventRecorder
}

// GroupVersionKind returns the GVK
func (s *ToHostSyncer) GroupVersionKind() schema.GroupVersionKind {
	return s.gvk
}

// Migrate is called during startup
func (s *ToHostSyncer) Migrate(ctx *synccontext.RegisterContext, mapper synccontext.Mapper) error {
	return nil
}

// VirtualToHost translates virtual name to host name.
// For shared namespaces (hostNamespace differs from the vCluster's own namespace),
// the host namespace is used as the name suffix instead of VClusterName to prevent
// collisions when multiple tenants share a target namespace (e.g. argocd).
func (s *ToHostSyncer) VirtualToHost(ctx *synccontext.SyncContext, req types.NamespacedName, vObj client.Object) types.NamespacedName {
	if req.Name == "" {
		return types.NamespacedName{}
	}
	if !s.namespaced {
		return types.NamespacedName{
			Name: translate.Default.HostNameCluster(req.Name),
		}
	}
	if s.isSharedNamespace() {
		return types.NamespacedName{
			Name:      s.sharedNamespaceName(req.Name),
			Namespace: s.hostNamespace,
		}
	}
	hostName := translate.Default.HostName(ctx, req.Name, req.Namespace)
	return types.NamespacedName{
		Name:      hostName.Name,
		Namespace: s.hostNamespace,
	}
}

// isSharedNamespace returns true when the target host namespace differs from the
// vCluster's own host namespace. In shared namespaces, naming and ownership markers
// must include tenant identity to prevent cross-tenant collisions.
func (s *ToHostSyncer) isSharedNamespace() bool {
	return s.hostNamespace != s.vclusterHostNamespace
}

// markerValue returns the value to use for the vCluster marker label.
// For shared namespaces, uses the host namespace (tenant-unique) instead of VClusterName.
func (s *ToHostSyncer) markerValue() string {
	if s.isSharedNamespace() {
		return s.vclusterHostNamespace
	}
	return s.vclusterName
}

// parseTenantCluster extracts tenant and cluster from a vCluster host namespace.
// The namespace follows the pattern vcluster-{tenant}--{cluster}.
func parseTenantCluster(hostNS string) (tenant, cluster string) {
	trimmed := strings.TrimPrefix(hostNS, "vcluster-")
	if idx := strings.Index(trimmed, "--"); idx > 0 {
		return trimmed[:idx], trimmed[idx+2:]
	}
	return "", ""
}

// parseTenantFromNamespace extracts just the tenant name (convenience wrapper).
func parseTenantFromNamespace(hostNS string) string {
	tenant, _ := parseTenantCluster(hostNS)
	return tenant
}

// sharedNamespaceName builds a clean, user-visible host name for resources in shared
// namespaces: {name}-{tenant}-{cluster}. Falls back to SafeConcatName with the full
// host namespace if the namespace doesn't follow the vcluster-{tenant}--{cluster} pattern.
func (s *ToHostSyncer) sharedNamespaceName(name string) string {
	tenant, cluster := parseTenantCluster(s.vclusterHostNamespace)
	if tenant != "" && cluster != "" {
		return translate.SafeConcatName(name, tenant, cluster)
	}
	// Fallback for non-standard namespace patterns
	return translate.SafeConcatName(name, s.vclusterHostNamespace)
}

// HostToVirtual translates host name to virtual name
func (s *ToHostSyncer) HostToVirtual(ctx *synccontext.SyncContext, req types.NamespacedName, pObj client.Object) types.NamespacedName {
	if pObj == nil {
		return types.NamespacedName{}
	}

	annotations := pObj.GetAnnotations()
	if annotations == nil {
		return types.NamespacedName{}
	}

	vName := annotations[translate.NameAnnotation]
	vNamespace := annotations[translate.NamespaceAnnotation]
	if vName == "" {
		return types.NamespacedName{}
	}

	if !s.namespaced {
		return types.NamespacedName{
			Name: vName,
		}
	}

	return types.NamespacedName{
		Name:      vName,
		Namespace: vNamespace,
	}
}

// IsManaged checks if host object is managed by this syncer
func (s *ToHostSyncer) IsManaged(ctx *synccontext.SyncContext, pObj client.Object) (bool, error) {
	if pObj == nil {
		return false, nil
	}

	if s.namespaced {
		if pObj.GetNamespace() != s.hostNamespace {
			return false, nil
		}
		labels := pObj.GetLabels()
		if labels == nil || labels[translate.MarkerLabel] != s.markerValue() {
			return false, nil
		}
	} else {
		if pObj.GetNamespace() != "" {
			return false, nil
		}
		labels := pObj.GetLabels()
		if labels == nil || labels[translate.MarkerLabel] != translate.Default.MarkerLabelCluster() {
			return false, nil
		}
	}

	annotations := pObj.GetAnnotations()
	if annotations == nil || annotations[translate.NameAnnotation] == "" {
		return false, nil
	}

	return true, nil
}

// Syncer returns the sync implementation wrapped for generic use
func (s *ToHostSyncer) Syncer() synctypes.Sync[client.Object] {
	return syncer.ToGenericSyncer(s)
}

// Register is called during syncer registration
// Can be used to ensure CRD exists on host cluster
func (s *ToHostSyncer) Register(ctx *synccontext.RegisterContext) error {
	s.log.Debug("Registering syncer", "gvk", s.gvk.String())

	// Ensure the CRD exists on the host cluster (toHost requires host CRD)
	apiResource, err := translate.KindExists(ctx.HostManager.GetConfig(), s.gvk)
	if err != nil {
		s.log.Error(err, "CRD not found on host cluster", "gvk", s.gvk.String())
		return err
	}
	s.namespaced = apiResource.Namespaced

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

var _ synctypes.Syncer = &ToHostSyncer{}
var _ synctypes.ControllerStarter = &ToHostSyncer{}
var _ synctypes.ControllerModifier = &ToHostSyncer{}

// ModifyController implements ControllerModifier to customize controller options
func (s *ToHostSyncer) ModifyController(_ *synccontext.RegisterContext, bld *builder.Builder) (*builder.Builder, error) {
	bld = bld.WithOptions(controller.Options{
		MaxConcurrentReconciles: s.cfg.MaxConcurrentReconciles,
	})

	// Add event filtering to skip no-op reconciliations if enabled
	// Note: WithEventFilter applies to virtual cluster watches (.Watches).
	// For ToHostSyncer, the primary source is the virtual cluster, so this
	// effectively filters the main event stream.
	if s.cfg.EventFilteringEnabled {
		bld = bld.WithEventFilter(s.eventFilterPredicate())
	}

	return bld, nil
}

// eventFilterPredicate returns a predicate that filters out updates where
// only metadata fields that don't affect sync behavior have changed.
// This reduces no-op reconciliations for changes like ManagedFields updates.
func (s *ToHostSyncer) eventFilterPredicate() predicate.Predicate {
	// For ToHost, status flows host→virtual, so we don't check status changes on virtual objects
	return buildEventFilterPredicate(s.gvk, s.log, func() bool { return false })
}

// SyncToHost is called when a virtual object was created and needs to be synced to the host
func (s *ToHostSyncer) SyncToHost(ctx *synccontext.SyncContext, event *synccontext.SyncToHostEvent[*unstructured.Unstructured]) (ctrl.Result, error) {
	s.metrics.RecordReconcile()
	defer s.metrics.TimeReconcile()()

	vObj := event.Virtual

	if vObj == nil {
		s.log.Warning("SyncToHost: received nil virtual object", "kind", s.gvk.Kind)
		return ctrl.Result{}, nil
	}

	s.tracer.TraceIncoming("create", vObj)

	statusEnabled := s.statusEnabled()

	if matches, reason := s.matchesSelector(vObj); !matches {
		s.log.Debug("SyncToHost: skipping virtual object (selector/filter mismatch)",
			"kind", s.gvk.Kind,
			"virtual", vObj.GetNamespace()+"/"+vObj.GetName())
		if reason == filterSelector {
			s.metrics.RecordSelectorFiltered()
		}
		return ctrl.Result{}, nil
	}

	hostName := s.VirtualToHost(ctx, types.NamespacedName{
		Name:      vObj.GetName(),
		Namespace: vObj.GetNamespace(),
	}, vObj)

	pObj := translate.HostMetadata(vObj, hostName)
	// Strip ownerRefs for shared namespaces. The SDK sets an ownerRef to the
	// vCluster Service, but that Service lives in vclusterHostNamespace, not
	// in hostNamespace. Kubernetes GC treats cross-namespace ownerRefs as
	// dangling and immediately deletes the object.
	if s.isSharedNamespace() {
		pObj.SetOwnerReferences(nil)
	}
	s.applySyncLabels(pObj)
	mergeExtraLabels(pObj, s.cfg.Resource.ExtraLabels)

	// Strip status before create when statusSync is disabled.
	// translate.HostMetadata deep-copies the entire object including status,
	// but we only want status set via the status subresource when enabled.
	if !statusEnabled {
		stripStatus(pObj)
	}

	if err := s.applyPatches(ctx, vObj, pObj); err != nil {
		syncErr := logging.NewSyncError("patch", s.gvk.Kind, hostName.Namespace, hostName.Name, string(config.ToHost), err)
		s.log.Error(syncErr, "SyncToHost: failed to apply patches",
			"virtual", vObj.GetNamespace()+"/"+vObj.GetName(),
			"errorType", syncErr.Type,
			"retryable", syncErr.Retryable)
		s.metrics.RecordOperationError(metrics.OperationCreate)
		s.metrics.RecordError(metrics.ClassifyError(err))
		s.events.EmitPatchFailed(vObj, err)
		s.tracer.TraceResult("create", pObj, syncErr)
		return logging.RequeueResult(syncErr), syncErr
	}

	s.tracer.TracePatched("create", vObj, pObj)

	// Use vCluster's patcher to create the host object (SDK logs "Create host object")
	timer := s.metrics.NewOperationTimer(metrics.OperationCreate)
	result, err := patcher.CreateHostObject(ctx, vObj, pObj, s.EventRecorder(), statusEnabled)
	timer.ObserveDuration()
	if err != nil {
		syncErr := logging.NewSyncError("create", s.gvk.Kind, hostName.Namespace, hostName.Name, string(config.ToHost), err)
		s.log.Error(syncErr, "SyncToHost: failed to create host object",
			"virtual", vObj.GetNamespace()+"/"+vObj.GetName(),
			"host", hostName.Namespace+"/"+hostName.Name,
			"errorType", syncErr.Type,
			"retryable", syncErr.Retryable)
		s.metrics.RecordOperationError(metrics.OperationCreate)
		s.metrics.RecordError(metrics.ClassifyError(err))
		s.events.EmitCreateFailed(vObj, err)
		s.tracer.TraceResult("create", pObj, syncErr)
		return logging.RequeueResult(syncErr), syncErr
	}

	s.metrics.RecordOperationSuccess(metrics.OperationCreate)
	s.metrics.IncResourcesManaged()
	// Use just the name for cluster-scoped resources, namespace/name for namespaced
	targetName := hostName.Name
	if hostName.Namespace != "" {
		targetName = hostName.Namespace + "/" + targetName
	}
	s.events.EmitCreated(vObj, targetName)

	s.log.Info("SyncToHost: created",
		"kind", s.gvk.Kind,
		"virtual", vObj.GetNamespace()+"/"+vObj.GetName(),
		"host", hostName.Namespace+"/"+hostName.Name)

	s.tracer.TraceResult("create", pObj, nil)

	return result, nil
}

// Sync is called when both virtual and host objects exist and need to be synchronized
func (s *ToHostSyncer) Sync(ctx *synccontext.SyncContext, event *synccontext.SyncEvent[*unstructured.Unstructured]) (ctrl.Result, error) {
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

	s.tracer.TraceIncoming("update-virtual", vObj)
	s.tracer.TraceIncoming("update-host", pObj)

	statusEnabled := s.statusEnabled()

	if matches, reason := s.matchesSelector(vObj); !matches {
		s.log.Debug("Sync: skipping virtual object (selector/filter mismatch)",
			"kind", s.gvk.Kind,
			"virtual", vObj.GetNamespace()+"/"+vObj.GetName())
		if reason == filterSelector {
			s.metrics.RecordSelectorFiltered()
		}
		if managed, err := s.IsManaged(ctx, pObj); err != nil {
			return ctrl.Result{}, err
		} else if managed {
			result, err := patcher.DeleteHostObject(ctx, pObj, nil, "selector/filter no longer matches")
			if err != nil {
				syncErr := logging.NewSyncError("delete", s.gvk.Kind, pObj.GetNamespace(), pObj.GetName(), string(config.ToHost), err)
				s.log.Error(syncErr, "Sync: failed to delete host object after selector mismatch",
					"host", pObj.GetNamespace()+"/"+pObj.GetName(),
					"errorType", syncErr.Type,
					"retryable", syncErr.Retryable)
				s.metrics.RecordOperationError(metrics.OperationDelete)
				s.metrics.RecordError(metrics.ClassifyError(err))
				s.tracer.TraceResult("delete", pObj, syncErr)
				return logging.RequeueResult(syncErr), syncErr
			}
			s.metrics.RecordOperationSuccess(metrics.OperationDelete)
			s.metrics.DecResourcesManaged()
			s.tracer.TraceResult("delete", pObj, nil)
			return result, nil
		}
		return ctrl.Result{}, nil
	}

	s.log.Debug("Sync: syncing virtual to host",
		"kind", s.gvk.Kind,
		"virtual", vObj.GetNamespace()+"/"+vObj.GetName(),
		"host", pObj.GetNamespace()+"/"+pObj.GetName())

	updated := pObj.DeepCopy()

	// Strip stale cross-namespace ownerRefs for shared namespaces.
	// Self-heals objects created by pre-fix builds where the SDK set an
	// ownerRef to the vCluster Service in the wrong namespace.
	if s.isSharedNamespace() {
		updated.SetOwnerReferences(nil)
	}

	copySyncableFields(vObj, updated)

	updated.SetAnnotations(translate.HostAnnotations(vObj, pObj))
	updated.SetLabels(translate.HostLabels(vObj, pObj))
	s.applySyncLabels(updated)
	mergeExtraLabels(updated, s.cfg.Resource.ExtraLabels)

	if err := s.applyPatches(ctx, vObj, updated); err != nil {
		syncErr := logging.NewSyncError("patch", s.gvk.Kind, pObj.GetNamespace(), pObj.GetName(), string(config.ToHost), err)
		s.log.Error(syncErr, "Sync: failed to apply patches",
			"errorType", syncErr.Type,
			"retryable", syncErr.Retryable)
		s.metrics.RecordOperationError(metrics.OperationUpdate)
		s.metrics.RecordError(metrics.ClassifyError(err))
		s.events.EmitPatchFailed(vObj, err)
		s.tracer.TraceResult("update", updated, syncErr)
		return logging.RequeueResult(syncErr), syncErr
	}

	s.tracer.TraceDiff("update", pObj, updated)

	// Use ApplyObject with beforeObject for proper merge patch calculation
	// This is more efficient than CreateHostObject as it calculates the diff
	timer := s.metrics.NewOperationTimer(metrics.OperationUpdate)
	err := patcher.ApplyObject(ctx, pObj, updated, synccontext.SyncVirtualToHost, statusEnabled)
	timer.ObserveDuration()
	if err != nil {
		syncErr := logging.NewSyncError("update", s.gvk.Kind, pObj.GetNamespace(), pObj.GetName(), string(config.ToHost), err)
		s.log.Error(syncErr, "Sync: failed to update host object",
			"host", pObj.GetNamespace()+"/"+pObj.GetName(),
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
			syncErr := logging.NewSyncError("status", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.ToHost), err)
			s.log.Error(syncErr, "Sync: failed to sync status",
				"errorType", syncErr.Type,
				"retryable", syncErr.Retryable)
			s.metrics.RecordError(metrics.ClassifyError(err))
			s.events.EmitSyncFailed(vObj, err)
			return logging.RequeueResult(syncErr), syncErr
		}
	}

	s.events.EmitUpdated(vObj)

	return ctrl.Result{}, nil
}

// SyncToVirtual is called when a host object exists but virtual doesn't (orphaned)
func (s *ToHostSyncer) SyncToVirtual(ctx *synccontext.SyncContext, event *synccontext.SyncToVirtualEvent[*unstructured.Unstructured]) (ctrl.Result, error) {
	s.metrics.RecordReconcile()
	defer s.metrics.TimeReconcile()()

	pObj := event.Host

	if pObj == nil {
		s.log.Warning("SyncToVirtual: received nil host object", "kind", s.gvk.Kind)
		return ctrl.Result{}, nil
	}

	s.tracer.TraceIncoming("delete", pObj)

	// If virtual object was deleted, delete the host object
	if event.VirtualOld != nil || translate.ShouldDeleteHostObject(pObj) {
		vName := ""
		vNamespace := ""
		if annotations := pObj.GetAnnotations(); annotations != nil {
			vName = annotations[translate.NameAnnotation]
			vNamespace = annotations[translate.NamespaceAnnotation]
		}

		// SDK logs "delete host..." so we just confirm after
		timer := s.metrics.NewOperationTimer(metrics.OperationDelete)
		result, err := patcher.DeleteHostObject(ctx, pObj, event.VirtualOld, "virtual object was deleted")
		timer.ObserveDuration()
		if err != nil {
			syncErr := logging.NewSyncError("delete", s.gvk.Kind, pObj.GetNamespace(), pObj.GetName(), string(config.ToHost), err)
			s.log.Error(syncErr, "SyncToVirtual: failed to delete host object",
				"virtual", vNamespace+"/"+vName,
				"host", pObj.GetNamespace()+"/"+pObj.GetName(),
				"errorType", syncErr.Type,
				"retryable", syncErr.Retryable)
			s.metrics.RecordOperationError(metrics.OperationDelete)
			s.metrics.RecordError(metrics.ClassifyError(err))
			if event.VirtualOld != nil {
				s.events.EmitDeleteFailed(event.VirtualOld, err)
			}
			s.tracer.TraceResult("delete", pObj, syncErr)
			return logging.RequeueResult(syncErr), syncErr
		}

		s.metrics.RecordOperationSuccess(metrics.OperationDelete)
		s.metrics.DecResourcesManaged()
		if event.VirtualOld != nil {
			s.events.EmitDeleted(event.VirtualOld, "virtual object was deleted")
		}

		s.log.Info("SyncToVirtual: deleted",
			"kind", s.gvk.Kind,
			"virtual", vNamespace+"/"+vName,
			"host", pObj.GetNamespace()+"/"+pObj.GetName())

		s.tracer.TraceResult("delete", pObj, nil)

		return result, nil
	}

	return ctrl.Result{}, nil
}

func (s *ToHostSyncer) applyPatches(ctx *synccontext.SyncContext, vObj, pObj client.Object) error {
	return s.patcherFn.ApplyToHost(ctx, vObj, pObj)
}

func (s *ToHostSyncer) matchesSelector(obj client.Object) (bool, filterReason) {
	return checkSelectorMatch(obj, s.namespaced, s.cfg, s.metrics)
}

func (s *ToHostSyncer) statusEnabled() bool {
	return s.cfg.Resource.StatusSync && s.hasStatusSubresource && s.cfg.Resource.DefaultMode() == config.Sync
}

// applySyncLabels adds a tenant label to all synced resources and, for shared
// namespaces, overrides the marker label to be tenant-unique.
func (s *ToHostSyncer) applySyncLabels(obj client.Object) {
	labels := obj.GetLabels()
	if labels == nil {
		labels = map[string]string{}
	}
	// Tenant label on ALL synced resources so ownership is always visible
	if tenant := parseTenantFromNamespace(s.vclusterHostNamespace); tenant != "" {
		labels["kupe.cloud/tenant"] = tenant
	}
	// In shared namespaces, override the marker label so each tenant's syncer
	// only manages its own resources (prevents cross-tenant collisions)
	if s.isSharedNamespace() {
		labels[translate.MarkerLabel] = s.vclusterHostNamespace
	}
	obj.SetLabels(labels)
}
