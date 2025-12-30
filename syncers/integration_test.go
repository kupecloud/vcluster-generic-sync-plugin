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
	"k8s.io/client-go/tools/record"
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
		gvk:           gvk,
		cfg:           config.SyncerConfig{Resource: config.SyncResource{StatusSync: false}},
		namespaced:    true,
		hostNamespace: "vcluster-ns",
		vclusterName:  "my-vcluster",
		patcherFn:     patches.NewPatcher(nil, "my-vcluster", "vcluster-ns", false),
		log:           logging.Log,
		eventRecorder: record.NewFakeRecorder(10),
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
		namespaced:    true,
		hostNamespace: "vcluster-ns",
		vclusterName:  "my-vcluster",
		patcherFn:     patches.NewPatcher(nil, "my-vcluster", "vcluster-ns", false),
		log:           logging.Log,
		eventRecorder: record.NewFakeRecorder(10),
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
