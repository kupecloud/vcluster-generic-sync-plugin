package syncers

import (
	"context"
	"testing"

	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	"github.com/loft-sh/vcluster/pkg/util/loghelper"
	"github.com/loft-sh/vcluster/pkg/util/translate"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
	"github.com/kupecloud/vcluster-generic-sync-plugin/patches"
)

func TestToHostSyncer_SyncToHost_CreatesHostObject(t *testing.T) {
	originalDefault := translate.Default
	originalVClusterName := translate.VClusterName
	translate.VClusterName = "my-vcluster"
	translate.Default = translate.NewSingleNamespaceTranslator("vcluster-ns")
	defer func() {
		translate.Default = originalDefault
		translate.VClusterName = originalVClusterName
	}()

	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("widget-a")
	vObj.SetNamespace("default")
	vObj.Object["spec"] = map[string]interface{}{"size": "large"}

	hostClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	virtualClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()

	syncer := &ToHostSyncer{
		gvk:                   gvk,
		cfg:                   config.SyncerConfig{Resource: config.SyncResource{StatusSync: false}},
		namespaced:            true,
		hostNamespace:         "vcluster-ns",
		vclusterName:          "my-vcluster",
		vclusterHostNamespace: "vcluster-ns",
		patcherFn:             patches.NewPatcher(nil, "my-vcluster", "vcluster-ns", false),
		log:                   logging.Log,
		eventRecorder:         events.NewFakeRecorder(10),
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		HostClient:    hostClient,
		VirtualClient: virtualClient,
		Log:           loghelper.New("test"),
	}

	event := &synccontext.SyncToHostEvent[*unstructured.Unstructured]{
		Virtual: vObj,
	}

	if _, err := syncer.SyncToHost(syncCtx, event); err != nil {
		t.Fatalf("SyncToHost() error: %v", err)
	}

	expected := translate.Default.HostName(syncCtx, vObj.GetName(), vObj.GetNamespace())
	hostObj := &unstructured.Unstructured{}
	hostObj.SetGroupVersionKind(gvk)
	if err := hostClient.Get(context.Background(), expected, hostObj); err != nil {
		t.Fatalf("expected host object to be created: %v", err)
	}
}

// TestToHostSyncer_SyncToHost_StripsTenantStatus: even with statusSync
// enabled, a tenant-authored .status must NOT be written to the host object on create —
// status flows host→virtual only. The real host controller populates status and the Sync
// cycle propagates it back.
func TestToHostSyncer_SyncToHost_StripsTenantStatus(t *testing.T) {
	originalDefault := translate.Default
	originalVClusterName := translate.VClusterName
	translate.VClusterName = "my-vcluster"
	translate.Default = translate.NewSingleNamespaceTranslator("vcluster-ns")
	defer func() {
		translate.Default = originalDefault
		translate.VClusterName = originalVClusterName
	}()

	gvk := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"}
	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("route-a")
	vObj.SetNamespace("default")
	vObj.Object["spec"] = map[string]interface{}{"hostnames": []interface{}{"example.com"}}
	// Tenant fabricates an Accepted=True status that must never reach the host object.
	vObj.Object["status"] = map[string]interface{}{
		"parents": []interface{}{
			map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{"type": "Accepted", "status": "True"},
				},
			},
		},
	}

	// Register the status subresource so the fake client mimics the real API server:
	// Create ignores .status and only Status().Update persists it. Without this the
	// fake client silently drops the SDK's status subresource write and the test can't
	// observe the regression.
	statusTmpl := &unstructured.Unstructured{}
	statusTmpl.SetGroupVersionKind(gvk)
	hostClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithStatusSubresource(statusTmpl).Build()
	virtualClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()

	syncer := &ToHostSyncer{
		gvk:                   gvk,
		cfg:                   config.SyncerConfig{Resource: config.SyncResource{StatusSync: true}},
		namespaced:            true,
		hasStatusSubresource:  true, // statusEnabled() => true
		hostNamespace:         "vcluster-ns",
		vclusterName:          "my-vcluster",
		vclusterHostNamespace: "vcluster-ns",
		patcherFn:             patches.NewPatcher(nil, "my-vcluster", "vcluster-ns", false),
		log:                   logging.Log,
		eventRecorder:         events.NewFakeRecorder(10),
	}
	if !syncer.statusEnabled() {
		t.Fatalf("test precondition: expected statusEnabled() to be true")
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		HostClient:    hostClient,
		VirtualClient: virtualClient,
		Log:           loghelper.New("test"),
	}

	if _, err := syncer.SyncToHost(syncCtx, &synccontext.SyncToHostEvent[*unstructured.Unstructured]{Virtual: vObj}); err != nil {
		t.Fatalf("SyncToHost() error: %v", err)
	}

	expected := translate.Default.HostName(syncCtx, vObj.GetName(), vObj.GetNamespace())
	hostObj := &unstructured.Unstructured{}
	hostObj.SetGroupVersionKind(gvk)
	if err := hostClient.Get(context.Background(), expected, hostObj); err != nil {
		t.Fatalf("expected host object to be created: %v", err)
	}
	if _, found, _ := unstructured.NestedMap(hostObj.Object, "status"); found {
		t.Errorf("tenant-supplied status was written to host object on create: %v", hostObj.Object["status"])
	}
}

func TestFromHostSyncer_SyncToVirtual_CreatesVirtualObject(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("widget-a")
	pObj.SetNamespace("host-ns")
	pObj.Object["spec"] = map[string]interface{}{"size": "large"}

	virtualClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()

	syncer := &FromHostSyncer{
		gvk:        gvk,
		namespaced: true,
		cfg:        config.SyncerConfig{Resource: config.SyncResource{Mode: config.Mirror}},
		patcher:    patches.NewPatcher(nil, "my-vcluster", "host-ns", false),
		log:        logging.Log,
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: virtualClient,
		Log:           loghelper.New("test"),
	}

	event := &synccontext.SyncToVirtualEvent[*unstructured.Unstructured]{
		Host: pObj,
	}

	if _, err := syncer.SyncToVirtual(syncCtx, event); err != nil {
		t.Fatalf("SyncToVirtual() error: %v", err)
	}

	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	key := client.ObjectKey{Namespace: "default", Name: pObj.GetName()}
	if err := virtualClient.Get(context.Background(), key, vObj); err != nil {
		t.Fatalf("expected virtual object to be created: %v", err)
	}
}

func TestFromHostSyncer_SyncToVirtual_TargetNamespace(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("widget-a")
	pObj.SetNamespace("host-ns")

	virtualClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()

	syncer := &FromHostSyncer{
		gvk:              gvk,
		namespaced:       true,
		cfg:              config.SyncerConfig{Resource: config.SyncResource{Mode: config.Mirror}},
		virtualNamespace: "system",
		patcher:          patches.NewPatcher(nil, "my-vcluster", "host-ns", false),
		log:              logging.Log,
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: virtualClient,
		Log:           loghelper.New("test"),
	}

	event := &synccontext.SyncToVirtualEvent[*unstructured.Unstructured]{
		Host: pObj,
	}

	if _, err := syncer.SyncToVirtual(syncCtx, event); err != nil {
		t.Fatalf("SyncToVirtual() error: %v", err)
	}

	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	key := client.ObjectKey{Namespace: "system", Name: pObj.GetName()}
	if err := virtualClient.Get(context.Background(), key, vObj); err != nil {
		t.Fatalf("expected virtual object in target namespace: %v", err)
	}
}

func TestFromHostSyncer_SyncToVirtual_RespectsMatchNamespaces(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("widget-a")
	pObj.SetNamespace("other")

	virtualClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()

	res := config.SyncResource{
		APIVersion: "example.com/v1",
		Kind:       "Widget",
		Selector:   &config.Selector{MatchNamespaces: []string{"system"}},
	}
	syncer := &FromHostSyncer{
		gvk:        gvk,
		namespaced: true,
		cfg: config.SyncerConfig{
			Resource:         res,
			NamespaceMatcher: config.NewNamespaceMatcher(res.APIVersion, res.Kind, nil, res.Selector),
		},
		patcher: patches.NewPatcher(nil, "my-vcluster", "host-ns", false),
		log:     logging.Log,
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: virtualClient,
		Log:           loghelper.New("test"),
	}

	event := &synccontext.SyncToVirtualEvent[*unstructured.Unstructured]{
		Host: pObj,
	}

	if _, err := syncer.SyncToVirtual(syncCtx, event); err != nil {
		t.Fatalf("SyncToVirtual() error: %v", err)
	}

	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	key := client.ObjectKey{Namespace: "default", Name: pObj.GetName()}
	if err := virtualClient.Get(context.Background(), key, vObj); !errors.IsNotFound(err) {
		t.Fatalf("expected no virtual object to be created, got err=%v", err)
	}
}

func TestToHostSyncer_SyncToHost_RespectsMatchNamespaces(t *testing.T) {
	originalDefault := translate.Default
	originalVClusterName := translate.VClusterName
	translate.VClusterName = "my-vcluster"
	translate.Default = translate.NewSingleNamespaceTranslator("vcluster-ns")
	defer func() {
		translate.Default = originalDefault
		translate.VClusterName = originalVClusterName
	}()

	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("widget-a")
	vObj.SetNamespace("default")
	vObj.Object["spec"] = map[string]interface{}{"size": "large"}

	hostClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	virtualClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()

	res := config.SyncResource{
		APIVersion: "example.com/v1",
		Kind:       "Widget",
		Selector:   &config.Selector{MatchNamespaces: []string{"system"}},
	}
	syncer := &ToHostSyncer{
		gvk: gvk,
		cfg: config.SyncerConfig{
			Resource:         res,
			NamespaceMatcher: config.NewNamespaceMatcher(res.APIVersion, res.Kind, nil, res.Selector),
		},
		namespaced:            true,
		hostNamespace:         "vcluster-ns",
		vclusterName:          "my-vcluster",
		vclusterHostNamespace: "vcluster-ns",
		patcherFn:             patches.NewPatcher(nil, "my-vcluster", "vcluster-ns", false),
		log:                   logging.Log,
		eventRecorder:         events.NewFakeRecorder(10),
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		HostClient:    hostClient,
		VirtualClient: virtualClient,
		Log:           loghelper.New("test"),
	}

	event := &synccontext.SyncToHostEvent[*unstructured.Unstructured]{
		Virtual: vObj,
	}

	if _, err := syncer.SyncToHost(syncCtx, event); err != nil {
		t.Fatalf("SyncToHost() error: %v", err)
	}

	expected := translate.Default.HostName(syncCtx, vObj.GetName(), vObj.GetNamespace())
	hostObj := &unstructured.Unstructured{}
	hostObj.SetGroupVersionKind(gvk)
	if err := hostClient.Get(context.Background(), expected, hostObj); !errors.IsNotFound(err) {
		t.Fatalf("expected no host object to be created, got err=%v", err)
	}
}

// TestToHostSyncer_Sync_NoEventOnNoOp guards the EmitUpdated gate in tohost.Sync.
// statusSync-driven reconciles tick whenever the host controller writes status
// to a synced object; if the merge patch is empty (steady state), the syncer
// must NOT emit an Updated event — those events accumulate in kine and have
// previously bloated a single vcluster's SQLite state.db to >500 MB in 2 days.
func TestToHostSyncer_Sync_NoEventOnNoOp(t *testing.T) {
	originalDefault := translate.Default
	originalVClusterName := translate.VClusterName
	translate.VClusterName = "my-vcluster"
	translate.Default = translate.NewSingleNamespaceTranslator("vcluster-ns")
	defer func() {
		translate.Default = originalDefault
		translate.VClusterName = originalVClusterName
	}()

	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("widget-a")
	vObj.SetNamespace("default")
	vObj.Object["spec"] = map[string]interface{}{"size": "large"}

	hostClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	virtualClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	recorder := events.NewFakeRecorder(10)

	syncer := &ToHostSyncer{
		gvk:                   gvk,
		cfg:                   config.SyncerConfig{Resource: config.SyncResource{StatusSync: false}},
		namespaced:            true,
		hostNamespace:         "vcluster-ns",
		vclusterName:          "my-vcluster",
		vclusterHostNamespace: "vcluster-ns",
		patcherFn:             patches.NewPatcher(nil, "my-vcluster", "vcluster-ns", false),
		log:                   logging.Log,
		eventRecorder:         recorder,
		events:                logging.NewEventEmitter(recorder, "toHost", gvk.Kind),
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		HostClient:    hostClient,
		VirtualClient: virtualClient,
		Log:           loghelper.New("test"),
	}

	// Create the host object first via SyncToHost (this emits a Created event we drain below).
	if _, err := syncer.SyncToHost(syncCtx, &synccontext.SyncToHostEvent[*unstructured.Unstructured]{Virtual: vObj}); err != nil {
		t.Fatalf("SyncToHost() error: %v", err)
	}
	drainEvents(recorder)

	// Read back the freshly created host object — this is the steady-state pObj.
	hostName := translate.Default.HostName(syncCtx, vObj.GetName(), vObj.GetNamespace())
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	if err := hostClient.Get(context.Background(), hostName, pObj); err != nil {
		t.Fatalf("get host object: %v", err)
	}

	// Sync() with both sides matching should be a no-op. No Updated event.
	if _, err := syncer.Sync(syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{Virtual: vObj, Host: pObj}); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	select {
	case e := <-recorder.Events:
		t.Fatalf("expected no event for steady-state Sync, got: %s", e)
	default:
	}
}

// TestFromHostSyncer_Sync_NoEventOnNoOp mirrors the above for the FromHost direction.
func TestFromHostSyncer_Sync_NoEventOnNoOp(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("widget-a")
	pObj.SetNamespace("host-ns")
	pObj.Object["spec"] = map[string]interface{}{"size": "large"}

	virtualClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
	hostClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(pObj).Build()
	recorder := events.NewFakeRecorder(10)

	syncer := &FromHostSyncer{
		gvk:           gvk,
		cfg:           config.SyncerConfig{Resource: config.SyncResource{StatusSync: false}},
		namespaced:    true,
		patcher:       patches.NewPatcher(nil, "my-vcluster", "vcluster-ns", false),
		log:           logging.Log,
		eventRecorder: recorder,
		events:        logging.NewEventEmitter(recorder, "fromHost", gvk.Kind),
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		HostClient:    hostClient,
		VirtualClient: virtualClient,
		Log:           loghelper.New("test"),
	}

	// Create the virtual object via SyncToVirtual (emits Created; we drain).
	if _, err := syncer.SyncToVirtual(syncCtx, &synccontext.SyncToVirtualEvent[*unstructured.Unstructured]{Host: pObj}); err != nil {
		t.Fatalf("SyncToVirtual() error: %v", err)
	}
	drainEvents(recorder)

	// Read back the freshly created virtual object — SyncToVirtual creates it
	// in the syncer's virtualNamespace (default "default" when not configured).
	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	if err := virtualClient.Get(context.Background(), client.ObjectKey{Namespace: "default", Name: pObj.GetName()}, vObj); err != nil {
		t.Fatalf("get virtual object: %v", err)
	}

	// Sync() with both sides matching should be a no-op.
	if _, err := syncer.Sync(syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{Virtual: vObj, Host: pObj}); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	select {
	case e := <-recorder.Events:
		t.Fatalf("expected no event for steady-state Sync, got: %s", e)
	default:
	}
}

// drainEvents pulls every queued event off a FakeRecorder so subsequent
// assertions can check whether *new* events were emitted.
func drainEvents(r *events.FakeRecorder) {
	for {
		select {
		case <-r.Events:
		default:
			return
		}
	}
}
