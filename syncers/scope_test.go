package syncers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-logr/logr"
	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
)

type fakeManager struct {
	mapper meta.RESTMapper
	config *rest.Config
}

func (f *fakeManager) GetHTTPClient() *http.Client { return nil }
func (f *fakeManager) GetConfig() *rest.Config     { return f.config }
func (f *fakeManager) GetCache() cache.Cache       { return nil }
func (f *fakeManager) GetScheme() *runtime.Scheme  { return runtime.NewScheme() }
func (f *fakeManager) GetClient() client.Client    { return nil }
func (f *fakeManager) GetFieldIndexer() client.FieldIndexer {
	return nil
}
func (f *fakeManager) GetEventRecorderFor(string) record.EventRecorder { return nil }
func (f *fakeManager) GetRESTMapper() meta.RESTMapper                  { return f.mapper }
func (f *fakeManager) GetAPIReader() client.Reader                     { return nil }
func (f *fakeManager) Start(context.Context) error                     { return nil }
func (f *fakeManager) Add(manager.Runnable) error                      { return nil }
func (f *fakeManager) Elected() <-chan struct{}                        { return make(chan struct{}) }
func (f *fakeManager) AddMetricsServerExtraHandler(string, http.Handler) error {
	return nil
}
func (f *fakeManager) AddHealthzCheck(string, healthz.Checker) error { return nil }
func (f *fakeManager) AddReadyzCheck(string, healthz.Checker) error  { return nil }
func (f *fakeManager) GetWebhookServer() webhook.Server              { return nil }
func (f *fakeManager) GetLogger() logr.Logger                        { return log.Log }
func (f *fakeManager) GetControllerOptions() ctrlconfig.Controller   { return ctrlconfig.Controller{} }

func newMapper(gvk schema.GroupVersionKind, namespaced bool) meta.RESTMapper {
	rm := meta.NewDefaultRESTMapper([]schema.GroupVersion{gvk.GroupVersion()})
	if namespaced {
		rm.Add(gvk, meta.RESTScopeNamespace)
	} else {
		rm.Add(gvk, meta.RESTScopeRoot)
	}
	return rm
}

func newDiscoveryServer(t *testing.T, groupVersion string, resources []metav1.APIResource) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("/api/"+groupVersion, func(w http.ResponseWriter, r *http.Request) {
		list := metav1.APIResourceList{
			GroupVersion: groupVersion,
			APIResources: resources,
		}
		data, err := json.Marshal(list)
		if err != nil {
			t.Fatalf("marshal discovery response: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	})

	return httptest.NewServer(mux)
}

func newDiscoveryConfig(server *httptest.Server, groupVersion string) *rest.Config {
	return &rest.Config{
		Host:    server.URL,
		APIPath: "",
		ContentConfig: rest.ContentConfig{
			GroupVersion:         &schema.GroupVersion{Version: groupVersion},
			NegotiatedSerializer: clientgoscheme.Codecs.WithoutConversion(),
		},
	}
}

func TestResolveNamespacedPrefersVirtualManager(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	ctx := &synccontext.RegisterContext{
		VirtualManager: &fakeManager{mapper: newMapper(gvk, true)},
		HostManager:    &fakeManager{mapper: newMapper(gvk, false)},
	}

	namespaced, err := resolveNamespaced(ctx, gvk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !namespaced {
		t.Fatal("expected namespaced scope from virtual manager")
	}
}

func TestResolveNamespacedFallsBackToHost(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	virtualMapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{gvk.GroupVersion()})
	ctx := &synccontext.RegisterContext{
		VirtualManager: &fakeManager{mapper: virtualMapper},
		HostManager:    &fakeManager{mapper: newMapper(gvk, false)},
	}

	namespaced, err := resolveNamespaced(ctx, gvk)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if namespaced {
		t.Fatal("expected cluster-scoped result from host manager")
	}
}

func TestResolveNamespacedDefaultsOnError(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	virtualMapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{gvk.GroupVersion()})
	hostMapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{gvk.GroupVersion()})
	ctx := &synccontext.RegisterContext{
		VirtualManager: &fakeManager{mapper: virtualMapper},
		HostManager:    &fakeManager{mapper: hostMapper},
	}

	namespaced, err := resolveNamespaced(ctx, gvk)
	if err == nil {
		t.Fatal("expected error when no REST mappings exist")
	}
	if !namespaced {
		t.Fatal("expected namespaced default when resolution fails")
	}
}

func TestHasStatusSubresourceCoreResources(t *testing.T) {
	resources := []metav1.APIResource{
		{Name: "configmaps", Namespaced: true, Kind: "ConfigMap", Verbs: []string{"get", "list"}},
		{Name: "pods", Namespaced: true, Kind: "Pod", Verbs: []string{"get", "list"}},
		{Name: "pods/status", Namespaced: true, Kind: "Pod", Verbs: []string{"get", "update", "patch"}},
	}
	server := newDiscoveryServer(t, "v1", resources)
	defer server.Close()

	cfg := newDiscoveryConfig(server, "v1")
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		t.Fatalf("failed to create discovery client: %v", err)
	}

	configMapGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	hasStatus, err := hasStatusSubresource(discoveryClient, configMapGVK)
	if err != nil {
		t.Fatalf("unexpected error for ConfigMap: %v", err)
	}
	if hasStatus {
		t.Fatal("expected ConfigMap to have no status subresource")
	}

	podGVK := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}
	hasStatus, err = hasStatusSubresource(discoveryClient, podGVK)
	if err != nil {
		t.Fatalf("unexpected error for Pod: %v", err)
	}
	if !hasStatus {
		t.Fatal("expected Pod to have status subresource")
	}
}

func TestRegisterCoreResourceSkipsCRDEnsureAndDisablesStatusSync(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"}
	resources := []metav1.APIResource{
		{Name: "configmaps", Namespaced: true, Kind: "ConfigMap", Verbs: []string{"get", "list"}},
	}
	server := newDiscoveryServer(t, "v1", resources)
	defer server.Close()

	cfg := newDiscoveryConfig(server, "v1")
	hostMgr := &fakeManager{
		mapper: newMapper(gvk, true),
		config: cfg,
	}
	ctx := &synccontext.RegisterContext{
		Context:     context.Background(),
		HostManager: hostMgr,
	}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Register panicked for core resource: %v", r)
		}
	}()

	toHost := &ToHostSyncer{
		gvk: gvk,
		cfg: config.SyncerConfig{
			Resource: config.SyncResource{StatusSync: true},
		},
		log: logging.Log,
	}
	if err := toHost.Register(ctx); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if toHost.hasStatusSubresource {
		t.Fatal("expected status subresource to be disabled for ConfigMap")
	}
	if toHost.statusEnabled() {
		t.Fatal("expected statusEnabled to be false for ConfigMap")
	}

	fromHost := &FromHostSyncer{
		gvk: gvk,
		cfg: config.SyncerConfig{
			Resource: config.SyncResource{StatusSync: true},
		},
		log: logging.Log,
	}
	if err := fromHost.Register(ctx); err != nil {
		t.Fatalf("Register failed: %v", err)
	}
	if fromHost.hasStatusSubresource {
		t.Fatal("expected status subresource to be disabled for ConfigMap")
	}
	if fromHost.statusEnabled() {
		t.Fatal("expected statusEnabled to be false for ConfigMap")
	}
}

func TestStatusSyncDisabledWhenVirtualLacksStatus(t *testing.T) {
	// Test that status sync is disabled when host has status but virtual doesn't.
	// This tests the fix for the host/virtual status mismatch issue.
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}

	// Host cluster has pods WITH status subresource
	hostResources := []metav1.APIResource{
		{Name: "pods", Namespaced: true, Kind: "Pod", Verbs: []string{"get", "list"}},
		{Name: "pods/status", Namespaced: true, Kind: "Pod", Verbs: []string{"get", "update", "patch"}},
	}
	hostServer := newDiscoveryServer(t, "v1", hostResources)
	defer hostServer.Close()

	// Virtual cluster has pods WITHOUT status subresource
	virtualResources := []metav1.APIResource{
		{Name: "pods", Namespaced: true, Kind: "Pod", Verbs: []string{"get", "list"}},
	}
	virtualServer := newDiscoveryServer(t, "v1", virtualResources)
	defer virtualServer.Close()

	hostCfg := newDiscoveryConfig(hostServer, "v1")
	virtualCfg := newDiscoveryConfig(virtualServer, "v1")

	hostMgr := &fakeManager{
		mapper: newMapper(gvk, true),
		config: hostCfg,
	}
	virtualMgr := &fakeManager{
		mapper: newMapper(gvk, true),
		config: virtualCfg,
	}

	ctx := &synccontext.RegisterContext{
		Context:        context.Background(),
		HostManager:    hostMgr,
		VirtualManager: virtualMgr,
	}

	// ToHostSyncer: status flows host -> virtual, so need virtual to have status subresource
	toHost := &ToHostSyncer{
		gvk: gvk,
		cfg: config.SyncerConfig{
			Resource: config.SyncResource{StatusSync: true},
		},
		log: logging.Log,
	}
	if err := toHost.Register(ctx); err != nil {
		t.Fatalf("ToHostSyncer.Register failed: %v", err)
	}
	if toHost.hasStatusSubresource {
		t.Fatal("ToHostSyncer: expected status subresource disabled when virtual lacks status")
	}
	if toHost.statusEnabled() {
		t.Fatal("ToHostSyncer: expected statusEnabled false when virtual lacks status")
	}

	// FromHostSyncer: status also flows host -> virtual
	fromHost := &FromHostSyncer{
		gvk: gvk,
		cfg: config.SyncerConfig{
			Resource: config.SyncResource{StatusSync: true},
		},
		log: logging.Log,
	}
	if err := fromHost.Register(ctx); err != nil {
		t.Fatalf("FromHostSyncer.Register failed: %v", err)
	}
	if fromHost.hasStatusSubresource {
		t.Fatal("FromHostSyncer: expected status subresource disabled when virtual lacks status")
	}
	if fromHost.statusEnabled() {
		t.Fatal("FromHostSyncer: expected statusEnabled false when virtual lacks status")
	}
}

func TestStatusSyncEnabledWhenBothHaveStatus(t *testing.T) {
	// Test that status sync is enabled when both host and virtual have status subresource.
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"}

	// Both clusters have pods WITH status subresource
	resources := []metav1.APIResource{
		{Name: "pods", Namespaced: true, Kind: "Pod", Verbs: []string{"get", "list"}},
		{Name: "pods/status", Namespaced: true, Kind: "Pod", Verbs: []string{"get", "update", "patch"}},
	}
	hostServer := newDiscoveryServer(t, "v1", resources)
	defer hostServer.Close()
	virtualServer := newDiscoveryServer(t, "v1", resources)
	defer virtualServer.Close()

	hostCfg := newDiscoveryConfig(hostServer, "v1")
	virtualCfg := newDiscoveryConfig(virtualServer, "v1")

	hostMgr := &fakeManager{
		mapper: newMapper(gvk, true),
		config: hostCfg,
	}
	virtualMgr := &fakeManager{
		mapper: newMapper(gvk, true),
		config: virtualCfg,
	}

	ctx := &synccontext.RegisterContext{
		Context:        context.Background(),
		HostManager:    hostMgr,
		VirtualManager: virtualMgr,
	}

	toHost := &ToHostSyncer{
		gvk: gvk,
		cfg: config.SyncerConfig{
			Resource: config.SyncResource{StatusSync: true},
		},
		log: logging.Log,
	}
	if err := toHost.Register(ctx); err != nil {
		t.Fatalf("ToHostSyncer.Register failed: %v", err)
	}
	if !toHost.hasStatusSubresource {
		t.Fatal("ToHostSyncer: expected status subresource enabled when both have status")
	}
	if !toHost.statusEnabled() {
		t.Fatal("ToHostSyncer: expected statusEnabled true when both have status")
	}

	fromHost := &FromHostSyncer{
		gvk: gvk,
		cfg: config.SyncerConfig{
			Resource: config.SyncResource{StatusSync: true},
		},
		log: logging.Log,
	}
	if err := fromHost.Register(ctx); err != nil {
		t.Fatalf("FromHostSyncer.Register failed: %v", err)
	}
	if !fromHost.hasStatusSubresource {
		t.Fatal("FromHostSyncer: expected status subresource enabled when both have status")
	}
	if !fromHost.statusEnabled() {
		t.Fatal("FromHostSyncer: expected statusEnabled true when both have status")
	}
}
