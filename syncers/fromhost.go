package syncers

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

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
	"k8s.io/client-go/util/workqueue"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	ctrlevent "sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	ctrlsource "sigs.k8s.io/controller-runtime/pkg/source"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
	"github.com/kupecloud/vcluster-generic-sync-plugin/metrics"
	"github.com/kupecloud/vcluster-generic-sync-plugin/patches"
)

// targetNamespaceAnnotation allows per-instance override of the target namespace
// in the virtual cluster. When set on a host object, the syncer creates the
// virtual object in this namespace instead of the config-level TargetNamespace.
const targetNamespaceAnnotation = "kupe.cloud/target-namespace"

// targetNameAnnotation allows per-instance override of the virtual object's name. When
// set on a host object, the virtual copy takes this name instead of the host object's
// name. The value must be a valid name for the kind (see config.ValidateTargetName); an
// invalid value means the object is not imported at all — it never falls back to the
// host name, which would publish the object under a name nobody asked for.
//
// The annotation is copied to the virtual object together with the other host
// annotations, so a virtual object whose own name equals its target-name annotation
// identifies itself as a renamed copy; VirtualToHost uses that (together with the
// provenance annotation) to pair it back with its host source.
const targetNameAnnotation = "kupe.cloud/target-name"

// syncedFromAnnotation marks a virtual object as created by the fromHost syncer and
// records its host source as "{hostNamespace}/{hostName}". translate.VirtualMetadata
// strips the SDK's Name/Namespace provenance annotations, leaving no way to tell a
// syncer-created virtual object from a user-created one. We stamp this annotation in
// SyncToVirtual (and re-apply it on every Sync) and gate EVERY delete of a virtual
// object on it: orphan cleanup when the host source is gone (sync and mirror mode)
// and stale-copy cleanup when the selector no longer matches. A user's own object is
// never stamped and therefore never deleted.
const syncedFromAnnotation = "kupe.cloud/synced-from"

// syncConflictAnnotation is set on a namespaced HOST object whose import is refused
// because its virtual location already holds an object the syncer did not create from
// it — a tenant's own object, or a copy synced from a different host object. The value
// says why, so a platform controller can surface the conflict. It is removed once the
// host object syncs again, and never copied to the virtual object. Cluster-scoped host
// objects are shared by every virtual cluster on the host, so the plugin never writes
// to them: their conflicts are reported through the log, the metric and an event on the
// virtual object only.
const syncConflictAnnotation = "kupe.cloud/sync-conflict"

// conflictRequeueInterval is how often a refused import is retried. A refused host object
// is normally imported as soon as its location frees up, because VirtualToHost pairs the
// location with it (see FromHostSyncer.refused). The periodic retry is the backstop for a
// location freed without an event reaching this syncer, and re-reports a conflict that
// persists.
const conflictRequeueInterval = 2 * time.Minute

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
	hostEvents           *logging.EventEmitter
	eventRecorder        events.EventRecorder
	metrics              *metrics.Recorder

	// refused maps a virtual location to the host object whose import there was last
	// refused, so the location's next reconcile is paired with that host object. See
	// VirtualToHost.
	refused   map[types.NamespacedName]types.NamespacedName
	refusedMu sync.Mutex
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

	// Only create EventEmitters if events are enabled. Events about a host object
	// (e.g. an import that was refused) are recorded on the host, where the platform
	// operator that owns the object can see them.
	var eventEmitter, hostEventEmitter *logging.EventEmitter
	if cfg.EventsEnabled {
		eventEmitter = logging.NewEventEmitter(eventRecorder, string(config.FromHost), gvk.Kind)
		if ctx.HostManager != nil {
			hostEventEmitter = logging.NewEventEmitter(ctx.HostManager.GetEventRecorder(name+"-syncer"), string(config.FromHost), gvk.Kind)
		}
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
		hostEvents:           hostEventEmitter,
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
		"extraLabels", len(cfg.Resource.ExtraLabels),
		"virtualControlledBy", cfg.Resource.VirtualControlledBy)

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
func (s *FromHostSyncer) VirtualToHost(_ *synccontext.SyncContext, req types.NamespacedName, vObj client.Object) types.NamespacedName {
	if req.Name == "" {
		return types.NamespacedName{}
	}
	// A copy imported under a kupe.cloud/target-name override does not share its host
	// source's name, so the name alone cannot find the source. Without this, the
	// SDK would pair the copy with a missing (or unrelated) host object: host deletions
	// would never propagate, and a stale copy left behind by a changed or invalid
	// annotation would never be cleaned up.
	if src, ok := s.renamedCopySource(req, vObj); ok {
		return src
	}
	byName := types.NamespacedName{Name: req.Name}
	if s.namespaced {
		byName.Namespace = s.targetNamespace
	}
	// The SDK pairs a virtual location with the host object whose event enqueued it
	// (hostNameRequestLookup in its pkg/syncer/syncer.go), and an event for another host
	// object mapping to the same location replaces that pairing. A refused import is
	// retried under its virtual location, so without this the retry — and the virtual
	// event that frees the location — would be paired by name, and the refused host object
	// would never be imported. The syncer's own copy at the location keeps its pairing.
	if vObj == nil || !syncedFromMatches(vObj, namespacedNameSource(byName)) {
		if host, ok := s.refusedAt(req); ok {
			return host
		}
	}
	return byName
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

	// Only the source host namespace is a valid import source; cache widening
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

	name := req.Name
	if ann := pObj.GetAnnotations()[targetNameAnnotation]; ann != "" {
		// Unlike an invalid target-namespace (which falls back to the configured
		// namespace), an invalid target name is NOT imported: falling back to the host
		// name would publish the object under a name nobody asked for. Returning an
		// empty name makes the SDK drop the host event; the stale copy at the previous
		// location is enqueued separately by hostLocationHandler. The warning and event
		// are emitted there too, once per change, rather than on every call.
		if err := config.ValidateTargetName(s.gvk.Group, s.gvk.Kind, ann, targetNameAnnotation); err != nil {
			s.log.Debug("HostToVirtual: invalid target-name annotation, not importing",
				"kind", s.gvk.Kind,
				"host", req.Namespace+"/"+req.Name,
				"annotation", ann,
				"reason", err.Error())
			return types.NamespacedName{}
		}
		name = ann
	}

	if !s.namespaced {
		return types.NamespacedName{
			Name: name,
		}
	}

	ns := s.virtualNamespaceOrDefault()
	if ann := pObj.GetAnnotations()[targetNamespaceAnnotation]; ann != "" {
		// Reuse the config-level validator so the annotation override enforces the same
		// rules as targetNamespace/hostNamespace — RFC 1123 AND the kube-* system-namespace
		// rejection.
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
		Name:      name,
		Namespace: ns,
	}
}

// renamedCopySource returns the host source of a virtual object imported under a
// kupe.cloud/target-name override. Such a copy carries the target-name annotation equal
// to its own name (copied from its host source) and the provenance annotation naming the
// source. Both must hold: a user's copy of a synced object under yet another name still
// carries the original's annotations, but its name no longer equals its target-name
// annotation, so it keeps the default name-based mapping (and is never mistaken for the
// syncer's copy). The source must also be in this syncer's source namespace — the only
// place fromHost objects are ever read from.
func (s *FromHostSyncer) renamedCopySource(req types.NamespacedName, vObj client.Object) (types.NamespacedName, bool) {
	if vObj == nil {
		return types.NamespacedName{}, false
	}
	annotations := vObj.GetAnnotations()
	if annotations[targetNameAnnotation] != req.Name {
		return types.NamespacedName{}, false
	}
	syncedFrom := annotations[syncedFromAnnotation]
	if syncedFrom == "" {
		return types.NamespacedName{}, false
	}
	if !s.namespaced {
		if strings.Contains(syncedFrom, "/") {
			return types.NamespacedName{}, false
		}
		return types.NamespacedName{Name: syncedFrom}, true
	}
	ns, name, found := strings.Cut(syncedFrom, "/")
	if !found || ns == "" || name == "" || strings.Contains(name, "/") {
		return types.NamespacedName{}, false
	}
	if s.targetNamespace != "" && ns != s.targetNamespace {
		return types.NamespacedName{}, false
	}
	return types.NamespacedName{Namespace: ns, Name: name}, true
}

// rememberRefused records that host's import at location was refused, replacing any other
// location recorded for host. It reports whether this refusal is new.
func (s *FromHostSyncer) rememberRefused(location, host types.NamespacedName) bool {
	s.refusedMu.Lock()
	defer s.refusedMu.Unlock()
	if s.refused == nil {
		s.refused = map[types.NamespacedName]types.NamespacedName{}
	}
	if current, ok := s.refused[location]; ok && current == host {
		return false
	}
	s.deleteRefusalsLocked(host)
	s.refused[location] = host
	return true
}

// forgetRefused drops the refusal recorded for host, once host synced, no longer
// wants importing, or is gone.
func (s *FromHostSyncer) forgetRefused(host types.NamespacedName) {
	s.refusedMu.Lock()
	defer s.refusedMu.Unlock()
	s.deleteRefusalsLocked(host)
}

// deleteRefusalsLocked drops every location recorded for host. refusedMu must be held.
func (s *FromHostSyncer) deleteRefusalsLocked(host types.NamespacedName) {
	for loc, h := range s.refused {
		if h == host {
			delete(s.refused, loc)
		}
	}
}

// forgetRefusedAt drops the refusal recorded at location if it is host's: host no longer
// maps there.
func (s *FromHostSyncer) forgetRefusedAt(location, host types.NamespacedName) {
	s.refusedMu.Lock()
	defer s.refusedMu.Unlock()
	if s.refused[location] == host {
		delete(s.refused, location)
	}
}

// refusedAt returns the host object whose import at location was last refused.
func (s *FromHostSyncer) refusedAt(location types.NamespacedName) (types.NamespacedName, bool) {
	s.refusedMu.Lock()
	defer s.refusedMu.Unlock()
	host, ok := s.refused[location]
	return host, ok
}

// IsManaged checks if the host object should be managed by this syncer
func (s *FromHostSyncer) IsManaged(_ *synccontext.SyncContext, pObj client.Object) (bool, error) {
	return s.managesHostObject(pObj), nil
}

// managesHostObject reports whether pObj is a host object this syncer may import.
func (s *FromHostSyncer) managesHostObject(pObj client.Object) bool {
	if pObj == nil {
		return false
	}

	// Pin to the source host namespace (the vCluster's own namespace). The host cache
	// is widened to ALL hostNamespace overrides (e.g. argocd, observability) by
	// modifyHostManager, so without this guard a fromHost syncer would also receive —
	// and import into the vCluster — unmarked objects living in shared/platform
	// namespaces. This is independent of config filters and enforces the documented
	// "read only from the host vcluster namespace" contract. targetNamespace
	// is always set from ctx.Config.HostNamespace in production; only unset in tests.
	if s.namespaced && s.targetNamespace != "" && pObj.GetNamespace() != s.targetNamespace {
		return false
	}

	// Deliberately NO selector check here (mirroring ToHostSyncer.IsManaged): the SDK
	// consults IsManaged both when enqueueing host events and when pairing objects in
	// getObjects, so excluding de-selected objects here would make them invisible to
	// reconciliation and the "selector no longer matches" cleanup in Sync unreachable —
	// the previously imported copy (including credential material) would stay in the
	// vCluster forever. Sync/SyncToVirtual re-check the selector themselves.

	if labels := pObj.GetLabels(); labels != nil {
		if labels[translate.MarkerLabel] != "" {
			return false
		}
	}

	return true
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
func (s *FromHostSyncer) ModifyController(ctx *synccontext.RegisterContext, bld *builder.Builder) (*builder.Builder, error) {
	bld = bld.WithOptions(controller.Options{
		MaxConcurrentReconciles: s.cfg.MaxConcurrentReconciles,
	})

	// The SDK enqueues a host event only at the host object's CURRENT virtual location.
	// When a target-namespace/target-name annotation changes (or becomes invalid), the
	// copy at the previous location would only be revisited on the next virtual-side
	// event. This second watch on the same host informer enqueues the previous location
	// as well, so the stale copy is removed straight away (Sync deletes it; the delete is
	// gated on provenance like every other delete).
	if ctx != nil && ctx.HostManager != nil {
		bld = bld.WatchesRawSource(ctrlsource.Kind(ctx.HostManager.GetCache(), s.Resource(), s.hostLocationHandler()))
	}

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
	return buildEventFilterPredicate(s.gvk, s.log, s.statusEnabled, nil)
}

// hostLocationHandler handles host events alongside the SDK's own handler: it enqueues
// the previous virtual location when a host object's target location changes, warns
// (log + event on the host object) when a host object carries an invalid target name,
// and forgets a deleted host object's refused import.
func (s *FromHostSyncer) hostLocationHandler() handler.TypedEventHandler[client.Object, reconcile.Request] {
	return handler.TypedFuncs[client.Object, reconcile.Request]{
		CreateFunc: func(_ context.Context, e ctrlevent.TypedCreateEvent[client.Object], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			s.warnInvalidTargetName(e.Object)
		},
		UpdateFunc: func(_ context.Context, e ctrlevent.TypedUpdateEvent[client.Object], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			if req, ok := s.staleLocation(e.ObjectOld, e.ObjectNew); ok {
				q.Add(req)
			}
			if e.ObjectOld == nil || e.ObjectNew == nil ||
				e.ObjectOld.GetAnnotations()[targetNameAnnotation] != e.ObjectNew.GetAnnotations()[targetNameAnnotation] {
				s.warnInvalidTargetName(e.ObjectNew)
			}
		},
		DeleteFunc: func(_ context.Context, e ctrlevent.TypedDeleteEvent[client.Object], _ workqueue.TypedRateLimitingInterface[reconcile.Request]) {
			if e.Object != nil {
				s.forgetRefused(client.ObjectKeyFromObject(e.Object))
			}
		},
	}
}

// staleLocation returns the virtual location a host object mapped to before an update,
// when that differs from where it maps now (target-namespace or target-name annotation
// changed, removed, or became invalid).
func (s *FromHostSyncer) staleLocation(oldObj, newObj client.Object) (reconcile.Request, bool) {
	if oldObj == nil || newObj == nil {
		return reconcile.Request{}, false
	}
	if !s.managesHostObject(newObj) {
		return reconcile.Request{}, false
	}
	oldLoc := s.HostToVirtual(nil, client.ObjectKeyFromObject(oldObj), oldObj)
	newLoc := s.HostToVirtual(nil, client.ObjectKeyFromObject(newObj), newObj)
	if oldLoc.Name == "" || oldLoc == newLoc {
		return reconcile.Request{}, false
	}
	return reconcile.Request{NamespacedName: oldLoc}, true
}

// warnInvalidTargetName logs a warning and records an event on the host object when it
// would be imported but its target-name annotation is invalid, so it is skipped.
func (s *FromHostSyncer) warnInvalidTargetName(pObj client.Object) {
	if pObj == nil {
		return
	}
	ann := pObj.GetAnnotations()[targetNameAnnotation]
	if ann == "" {
		return
	}
	err := config.ValidateTargetName(s.gvk.Group, s.gvk.Kind, ann, targetNameAnnotation)
	if err == nil {
		return
	}
	if !s.managesHostObject(pObj) {
		return
	}
	if matches, _ := checkSelectorMatch(pObj, s.namespaced, s.cfg, nil); !matches {
		return
	}
	s.log.Warning("Not importing host object: invalid target-name annotation",
		"kind", s.gvk.Kind,
		"host", provenanceSource(pObj),
		"annotation", ann,
		"reason", err.Error())
	s.hostEvents.EmitInvalidTargetName(pObj, ann, err)
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
	// unconditional delete here would silently destroy a user's own Gateway or
	// GatewayClass. Objects that don't match this virtual location's mapped source —
	// including a user's own copy under a new name that inherited the annotation —
	// fall through to the user-created no-op below.
	if s.cfg.Resource.DefaultMode() == config.Mirror && syncedFromMatches(vObj, s.mappedSource(vObj)) {
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
	// host source that would clear it no longer exists). Delete it — but ONLY if its
	// provenance annotation equals THIS virtual location's mapped source, proving the
	// syncer created it here. A user's own object with the same name (never stamped), or a
	// user's own copy under a new name that inherited another object's annotation whose
	// claimed source still exists (maps elsewhere), is left untouched.
	if syncedFromMatches(vObj, s.mappedSource(vObj)) {
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
		s.forgetRefused(client.ObjectKeyFromObject(pObj))
		// Only delete the virtual copy if its provenance annotation equals THIS host
		// source, proving the syncer created it from this object. A user-created object
		// paired by name via VirtualToHost (never stamped), or a user's own copy that
		// inherited a different source's annotation, must never be deleted.
		if !syncedFromMatches(vObj, provenanceSource(pObj)) {
			s.log.Debug("Sync: selector/filter no longer matches, but virtual object was not synced from this host source, leaving untouched",
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

	// Guard against hijacking a user's own object. VirtualToHost maps any virtual
	// name to {targetNamespace}/{name} regardless of the virtual namespace, so the SDK
	// can pair a user-created object (same name, different virtual namespace, or one not
	// at the target-namespace/target-name annotation override) with this host object, and
	// the update below would overwrite the user's spec/labels with host content. Only
	// proceed when the virtual object sits at the canonical location HostToVirtual derives
	// from the host object, including any target-namespace and target-name overrides.
	{
		canonical := s.HostToVirtual(ctx, types.NamespacedName{Name: pObj.GetName(), Namespace: pObj.GetNamespace()}, pObj)
		if canonical.Name == "" || canonical.Name != vObj.GetName() || (s.namespaced && canonical.Namespace != vObj.GetNamespace()) {
			s.forgetRefusedAt(client.ObjectKeyFromObject(vObj), client.ObjectKeyFromObject(pObj))
			// The paired virtual object is not at the canonical import location (or the
			// host object no longer has one: its target-name annotation is invalid). If it
			// carries THIS host source's provenance, it is the syncer's own copy left
			// stranded at an old location when the kupe.cloud/target-namespace or
			// kupe.cloud/target-name annotation changed: the host source still exists so it
			// never reaches the orphan path, and it would otherwise sit frozen with stale
			// (for Secrets: still-live credential) data forever. Delete it — the fresh copy
			// is created at the canonical location by SyncToVirtual. A user-created object
			// (no provenance) or one synced from a different source is left untouched.
			if syncedFromMatches(vObj, provenanceSource(pObj)) {
				s.log.Info("Sync: target location changed, removing stale synced virtual copy at old location",
					"kind", s.gvk.Kind,
					"virtual", vObj.GetNamespace()+"/"+vObj.GetName(),
					"canonical", canonical.Namespace+"/"+canonical.Name,
					"syncedFrom", vObj.GetAnnotations()[syncedFromAnnotation])
				timer := s.metrics.NewOperationTimer(metrics.OperationDelete)
				result, err := patcher.DeleteVirtualObject(ctx, vObj, nil, "target location changed; removing stale synced copy at old location")
				timer.ObserveDuration()
				if err != nil {
					syncErr := logging.NewSyncError("delete", s.gvk.Kind, vObj.GetNamespace(), vObj.GetName(), string(config.FromHost), err)
					s.log.Error(syncErr, "Sync: failed to delete stale virtual copy after target location change",
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

	// Never overwrite a virtual object the syncer did not create from THIS host object:
	// a tenant's own object that happens to sit at the import location, or a copy synced
	// from another host object that targets the same location. Report it instead.
	if !syncedFromMatches(vObj, provenanceSource(pObj)) {
		return s.refuseConflict(ctx, pObj, vObj), nil
	}

	s.log.Debug("Sync: updating virtual object",
		"kind", s.gvk.Kind,
		"host", pObj.GetNamespace()+"/"+pObj.GetName(),
		"virtual", vObj.GetNamespace()+"/"+vObj.GetName())

	updated := vObj.DeepCopy()

	copySyncableFields(pObj, updated, nil)

	updated.SetAnnotations(translate.VirtualAnnotations(pObj, vObj, syncConflictAnnotation))
	updated.SetLabels(translate.VirtualLabels(pObj, vObj))
	mergeExtraLabels(updated, s.cfg.Resource.ExtraLabels)
	s.syncControlledByLabel(updated)
	// Re-stamp provenance: VirtualAnnotations is derived from host annotations and
	// would otherwise drop this plugin-set marker on update, leaving the object
	// undeletable on host deletion.
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

	s.forgetRefused(client.ObjectKeyFromObject(pObj))
	if err := s.clearSyncConflict(ctx, pObj, "update"); err != nil {
		return logging.RequeueForError(err)
	}

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
		s.forgetRefused(client.ObjectKeyFromObject(pObj))
		return ctrl.Result{}, nil
	}

	virtualName := s.HostToVirtual(ctx, types.NamespacedName{
		Name:      pObj.GetName(),
		Namespace: pObj.GetNamespace(),
	}, pObj)

	if virtualName.Name == "" {
		s.forgetRefused(client.ObjectKeyFromObject(pObj))
		return ctrl.Result{}, nil
	}

	s.log.Info("SyncToVirtual: creating virtual object",
		"kind", s.gvk.Kind,
		"host", pObj.GetNamespace()+"/"+pObj.GetName(),
		"virtual", virtualName.Namespace+"/"+virtualName.Name)

	vObj := translate.VirtualMetadata(pObj, virtualName, syncConflictAnnotation)
	mergeExtraLabels(vObj, s.cfg.Resource.ExtraLabels)
	s.syncControlledByLabel(vObj)
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
	// attempt, and the resource never appears in the vCluster.
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
	if apierrors.IsAlreadyExists(err) {
		// An object appeared at the location after the SDK read it. Refuse it if the
		// syncer did not create it from this host object.
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(s.gvk)
		getErr := ctx.VirtualClient.Get(ctx, virtualName, existing)
		switch {
		case getErr != nil:
			err = errors.Join(err, fmt.Errorf("get existing virtual object: %w", getErr))
		case !syncedFromMatches(existing, provenanceSource(pObj)):
			return s.refuseConflict(ctx, pObj, existing), nil
		default:
			// This host object's own copy, created by a concurrent reconcile of it. Nothing
			// failed: retry shortly, when the SDK pairs the copy and Sync updates it.
			s.log.Debug("SyncToVirtual: copy already created by a concurrent reconcile, retrying",
				"kind", s.gvk.Kind,
				"host", provenanceSource(pObj),
				"virtual", provenanceSource(existing))
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
	}
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

	s.forgetRefused(client.ObjectKeyFromObject(pObj))
	if err := s.clearSyncConflict(ctx, pObj, "create"); err != nil {
		return logging.RequeueForError(err)
	}

	return ctrl.Result{}, nil
}

// provenanceSource returns the "{namespace}/{name}" (or "{name}" for cluster-scoped)
// identifier of a host object, as recorded in the syncedFromAnnotation.
func provenanceSource(pObj client.Object) string {
	return namespacedNameSource(client.ObjectKeyFromObject(pObj))
}

// mappedSource returns the host provenance identifier a syncer-created copy of vObj
// would carry — provenanceSource of vObj's VirtualToHost mapping. When only the virtual
// object is in hand (SyncToHost has no paired host object), a delete is gated on the
// annotation equalling THIS value rather than merely being non-empty, so a user's own
// copy of a synced object under a new name (which inherits the original's annotation, but
// maps to a different host source) is never mistaken for the syncer's own copy.
func (s *FromHostSyncer) mappedSource(vObj client.Object) string {
	return namespacedNameSource(s.VirtualToHost(nil, types.NamespacedName{Name: vObj.GetName(), Namespace: vObj.GetNamespace()}, vObj))
}

// namespacedNameSource returns the provenance identifier of the host object at key, or
// "" for an empty key.
func namespacedNameSource(key types.NamespacedName) string {
	if key.Name == "" {
		return ""
	}
	if key.Namespace == "" {
		return key.Name
	}
	return key.Namespace + "/" + key.Name
}

// syncedFromMatches reports whether vObj carries this syncer's provenance annotation
// pointing at the given (non-empty) host source. Every virtual-object delete is gated on
// this — never on mere annotation non-emptiness — so the four delete branches stay
// consistent: only a copy the syncer created FROM the source being evaluated is ever
// deleted, and an object lacking the annotation is always refused.
func syncedFromMatches(vObj client.Object, source string) bool {
	return source != "" && vObj.GetAnnotations()[syncedFromAnnotation] == source
}

// stampProvenance records the host source on the virtual object so SyncToHost can
// distinguish syncer-created objects from user-created ones.
func stampProvenance(vObj, pObj client.Object) {
	annotations := vObj.GetAnnotations()
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[syncedFromAnnotation] = provenanceSource(pObj)
	vObj.SetAnnotations(annotations)
}

// refuseConflict leaves vObj untouched, records the refusal so the location's next
// reconcile is paired with pObj, and reports the conflict: a warning log and the
// sync-conflicts metric on every attempt; for a namespaced kind, the sync-conflict
// annotation on the host object, with warning events on both objects when the annotation
// is newly set (or cannot be set, so the conflict is still visible); for a cluster-scoped
// kind, a warning event on the virtual object when the refusal is new.
func (s *FromHostSyncer) refuseConflict(ctx *synccontext.SyncContext, pObj *unstructured.Unstructured, vObj client.Object) ctrl.Result {
	reason := fmt.Sprintf("%s %s already exists in the virtual cluster and was not created by the syncer from this object",
		s.gvk.Kind, provenanceSource(vObj))
	s.log.Warning("Refusing to overwrite virtual object not created by the syncer from this host object",
		"kind", s.gvk.Kind,
		"host", provenanceSource(pObj),
		"virtual", provenanceSource(vObj),
		"syncedFrom", vObj.GetAnnotations()[syncedFromAnnotation])
	s.metrics.RecordSyncConflict()
	newlyRefused := s.rememberRefused(client.ObjectKeyFromObject(vObj), client.ObjectKeyFromObject(pObj))
	result := ctrl.Result{RequeueAfter: conflictRequeueInterval}

	if !s.namespaced {
		if newlyRefused {
			s.events.EmitSyncConflict(vObj, reason)
		}
		return result
	}

	recorded, err := s.setHostAnnotation(ctx, pObj, syncConflictAnnotation, &reason)
	if err != nil {
		s.log.Warning("Could not record the sync conflict on the host object; reporting it through events and metrics only",
			"kind", s.gvk.Kind,
			"host", provenanceSource(pObj),
			"error", err.Error())
	}
	if recorded || err != nil {
		s.hostEvents.EmitSyncConflict(pObj, reason)
		s.events.EmitSyncConflict(vObj, reason)
	}
	return result
}

// clearSyncConflict removes the sync-conflict annotation from a namespaced host object
// that has just synced; cluster-scoped host objects are never written. A failure is logged
// and recorded here and returned as a SyncError for the caller to requeue on; operation
// names the sync step that succeeded.
func (s *FromHostSyncer) clearSyncConflict(ctx *synccontext.SyncContext, pObj *unstructured.Unstructured, operation string) error {
	if !s.namespaced {
		return nil
	}
	if _, err := s.setHostAnnotation(ctx, pObj, syncConflictAnnotation, nil); err != nil {
		syncErr := logging.NewSyncError(operation, s.gvk.Kind, pObj.GetNamespace(), pObj.GetName(), string(config.FromHost), err)
		s.log.Error(syncErr, "Failed to clear the sync-conflict annotation on the host object",
			"errorType", syncErr.Type,
			"retryable", syncErr.Retryable)
		s.metrics.RecordError(metrics.ClassifyError(err))
		return syncErr
	}
	return nil
}

// setHostAnnotation sets the annotation key on the host object to *value, or removes it
// when value is nil, with a merge patch. It reports whether the object changed: an
// annotation already in the wanted state is not written again.
func (s *FromHostSyncer) setHostAnnotation(ctx *synccontext.SyncContext, pObj *unstructured.Unstructured, key string, value *string) (bool, error) {
	current, present := pObj.GetAnnotations()[key]
	if (value == nil && !present) || (value != nil && present && current == *value) {
		return false, nil
	}
	if ctx.HostClient == nil {
		return false, errors.New("no host client")
	}
	updated := pObj.DeepCopy()
	annotations := updated.GetAnnotations()
	if value == nil {
		delete(annotations, key)
	} else {
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[key] = *value
	}
	updated.SetAnnotations(annotations)
	if err := ctx.HostClient.Patch(ctx, updated, client.MergeFrom(pObj)); err != nil {
		return false, fmt.Errorf("patch annotation %s on host %s %s: %w", key, s.gvk.Kind, provenanceSource(pObj), err)
	}
	return true, nil
}

// syncControlledByLabel applies the virtualControlledBy option to a virtual copy.
// translate.VirtualLabels drops a controlled-by label coming from the host but keeps the
// one already on the virtual object, so the label is set here on every create and
// update when the option is on, and the plugin's own value is removed when it is off —
// turning the option off hands the copy back to vCluster's syncers.
func (s *FromHostSyncer) syncControlledByLabel(vObj client.Object) {
	labels := vObj.GetLabels()
	if !s.cfg.Resource.VirtualControlledBy {
		if labels[translate.ControllerLabel] == controlledByLabelValue {
			delete(labels, translate.ControllerLabel)
			vObj.SetLabels(labels)
		}
		return
	}
	if labels == nil {
		labels = map[string]string{}
	}
	labels[translate.ControllerLabel] = controlledByLabelValue
	vObj.SetLabels(labels)
}

func (s *FromHostSyncer) applyPatches(ctx *synccontext.SyncContext, pObj, vObj client.Object) error {
	return s.patcher.ApplyToVirtual(ctx, pObj, vObj)
}

func (s *FromHostSyncer) statusEnabled() bool {
	return s.cfg.Resource.StatusSync && s.hasStatusSubresource && s.cfg.Resource.DefaultMode() == config.Sync
}

// ensureVirtualNamespace idempotently creates the target namespace in the virtual
// cluster. A concurrent create (AlreadyExists) is treated as success.
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
