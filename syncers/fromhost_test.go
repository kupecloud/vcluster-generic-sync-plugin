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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
)

// testSyncerConfig creates a SyncerConfig with properly initialized NamespaceMatcher for tests
func testSyncerConfig(res config.SyncResource) config.SyncerConfig {
	return config.SyncerConfig{
		Resource:                res,
		MaxConcurrentReconciles: 10,
		EventFilteringEnabled:   true,
		NamespaceMatcher:        config.NewNamespaceMatcher(res.APIVersion, res.Kind, nil, res.Selector),
	}
}

func TestFromHostSyncer_VirtualToHost(t *testing.T) {
	syncer := &FromHostSyncer{
		name:            "test-syncer",
		gvk:             schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"},
		targetNamespace: "host-ns",
		vclusterName:    "my-vcluster",
		namespaced:      true,
	}

	tests := []struct {
		name              string
		vName             string
		vNamespace        string
		expectedName      string
		expectedNamespace string
	}{
		{
			name:              "simple name passthrough",
			vName:             "shared-gateway",
			vNamespace:        "default",
			expectedName:      "shared-gateway",
			expectedNamespace: "host-ns",
		},
		{
			name:              "different virtual namespace still uses host ns",
			vName:             "prod-gateway",
			vNamespace:        "production",
			expectedName:      "prod-gateway",
			expectedNamespace: "host-ns",
		},
		{
			name:              "empty name returns empty",
			vName:             "",
			vNamespace:        "default",
			expectedName:      "",
			expectedNamespace: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := types.NamespacedName{Name: tt.vName, Namespace: tt.vNamespace}
			result := syncer.VirtualToHost(nil, req, nil)

			if result.Name != tt.expectedName {
				t.Errorf("VirtualToHost().Name = %q, expected %q", result.Name, tt.expectedName)
			}
			if result.Namespace != tt.expectedNamespace {
				t.Errorf("VirtualToHost().Namespace = %q, expected %q", result.Namespace, tt.expectedNamespace)
			}
		})
	}
}

func TestFromHostSyncer_HostToVirtual(t *testing.T) {
	tests := []struct {
		name              string
		selector          *config.Selector
		objLabels         map[string]string
		reqName           string
		targetNamespace   string
		expectedName      string
		expectedNamespace string
	}{
		{
			name:              "no selector - passes through",
			selector:          nil,
			objLabels:         nil,
			reqName:           "shared-gateway",
			targetNamespace:   "",
			expectedName:      "shared-gateway",
			expectedNamespace: "default",
		},
		{
			name: "matching selector",
			selector: &config.Selector{
				MatchLabels: map[string]string{"app": "gateway"},
			},
			objLabels:         map[string]string{"app": "gateway"},
			reqName:           "my-gateway",
			targetNamespace:   "",
			expectedName:      "my-gateway",
			expectedNamespace: "default",
		},
		{
			name: "non-matching selector returns empty",
			selector: &config.Selector{
				MatchLabels: map[string]string{"app": "gateway"},
			},
			objLabels:         map[string]string{"app": "other"},
			reqName:           "my-gateway",
			targetNamespace:   "",
			expectedName:      "",
			expectedNamespace: "",
		},
		{
			name: "missing label returns empty",
			selector: &config.Selector{
				MatchLabels: map[string]string{"app": "gateway"},
			},
			objLabels:         map[string]string{},
			reqName:           "my-gateway",
			targetNamespace:   "",
			expectedName:      "",
			expectedNamespace: "",
		},
		{
			name:              "target namespace override",
			selector:          nil,
			objLabels:         nil,
			reqName:           "my-gateway",
			targetNamespace:   "system",
			expectedName:      "my-gateway",
			expectedNamespace: "system",
		},
		{
			name:              "empty name returns empty",
			selector:          nil,
			objLabels:         nil,
			reqName:           "",
			targetNamespace:   "",
			expectedName:      "",
			expectedNamespace: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := &FromHostSyncer{
				name:             "test-syncer",
				gvk:              schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"},
				cfg:              testSyncerConfig(config.SyncResource{APIVersion: "gateway.networking.k8s.io/v1", Kind: "Gateway", Selector: tt.selector}),
				targetNamespace:  "host-ns",
				vclusterName:     "my-vcluster",
				namespaced:       true,
				virtualNamespace: tt.targetNamespace,
			}

			pObj := &unstructured.Unstructured{}
			pObj.SetLabels(tt.objLabels)
			pObj.SetNamespace("host-ns")

			req := types.NamespacedName{Name: tt.reqName, Namespace: "host-ns"}
			result := syncer.HostToVirtual(nil, req, pObj)

			if result.Name != tt.expectedName {
				t.Errorf("HostToVirtual().Name = %q, expected %q", result.Name, tt.expectedName)
			}
			if result.Namespace != tt.expectedNamespace {
				t.Errorf("HostToVirtual().Namespace = %q, expected %q", result.Namespace, tt.expectedNamespace)
			}
		})
	}
}

func TestFromHostSyncer_IsManaged(t *testing.T) {
	tests := []struct {
		name      string
		selector  *config.Selector
		objLabels map[string]string
		expected  bool
	}{
		{
			name:      "no selector - managed",
			selector:  nil,
			objLabels: nil,
			expected:  true,
		},
		{
			name: "matching selector - managed",
			selector: &config.Selector{
				MatchLabels: map[string]string{"shared": "true"},
			},
			objLabels: map[string]string{"shared": "true"},
			expected:  true,
		},
		{
			name: "non-matching selector - not managed",
			selector: &config.Selector{
				MatchLabels: map[string]string{"shared": "true"},
			},
			objLabels: map[string]string{"shared": "false"},
			expected:  false,
		},
		{
			name: "missing label - not managed",
			selector: &config.Selector{
				MatchLabels: map[string]string{"shared": "true"},
			},
			objLabels: map[string]string{},
			expected:  false,
		},
		{
			name:      "has vcluster marker - not managed (already owned)",
			selector:  nil,
			objLabels: map[string]string{translate.MarkerLabel: "some-vcluster"},
			expected:  false,
		},
		{
			name: "matching selector but has marker - not managed",
			selector: &config.Selector{
				MatchLabels: map[string]string{"shared": "true"},
			},
			objLabels: map[string]string{
				"shared":              "true",
				translate.MarkerLabel: "some-vcluster",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := &FromHostSyncer{
				name:            "test-syncer",
				gvk:             schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"},
				cfg:             testSyncerConfig(config.SyncResource{APIVersion: "gateway.networking.k8s.io/v1", Kind: "Gateway", Selector: tt.selector}),
				targetNamespace: "host-ns",
				vclusterName:    "my-vcluster",
				namespaced:      true,
			}

			pObj := &unstructured.Unstructured{}
			pObj.SetLabels(tt.objLabels)
			pObj.SetNamespace("host-ns")

			result, err := syncer.IsManaged(nil, pObj)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
			if result != tt.expected {
				t.Errorf("IsManaged() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestFromHostSyncer_ExcludeControlledByLabel(t *testing.T) {
	syncer := &FromHostSyncer{}

	tests := []struct {
		name     string
		labels   map[string]string
		expected bool // true means exclude
	}{
		{
			name:     "generic-sync label allows sync",
			labels:   map[string]string{translate.ControllerLabel: "generic-sync"},
			expected: false,
		},
		{
			name:     "empty label allows sync",
			labels:   map[string]string{},
			expected: false,
		},
		{
			name:     "nil labels allows sync",
			labels:   nil,
			expected: false,
		},
		{
			name:     "other-controller label excludes",
			labels:   map[string]string{translate.ControllerLabel: "other-controller"},
			expected: true,
		},
		{
			name:     "different-plugin label excludes",
			labels:   map[string]string{translate.ControllerLabel: "different-plugin"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := &unstructured.Unstructured{}
			obj.SetLabels(tt.labels)

			if result := syncer.ExcludeVirtual(obj); result != tt.expected {
				t.Errorf("ExcludeVirtual() = %v, expected %v", result, tt.expected)
			}
			if result := syncer.ExcludePhysical(obj); result != tt.expected {
				t.Errorf("ExcludePhysical() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestFromHostSyncer_MatchesSelectorNamespaces(t *testing.T) {
	syncer := &FromHostSyncer{
		namespaced: true,
		cfg: testSyncerConfig(config.SyncResource{
			APIVersion: "v1",
			Kind:       "ConfigMap",
			Selector: &config.Selector{
				MatchNamespaces: []string{"host-ns"},
			},
		}),
	}

	obj := &unstructured.Unstructured{}
	obj.SetNamespace("host-ns")
	if matches, _ := syncer.matchesSelector(obj); !matches {
		t.Fatalf("expected selector to match host-ns")
	}

	obj.SetNamespace("other")
	if matches, _ := syncer.matchesSelector(obj); matches {
		t.Fatalf("expected selector to reject other namespace")
	}
}

func TestFromHostSyncer_MatchesSelector(t *testing.T) {
	tests := []struct {
		name         string
		selector     *config.Selector
		objLabels    map[string]string
		objNamespace string
		expected     bool
	}{
		{
			name:         "nil selector matches all",
			selector:     nil,
			objLabels:    map[string]string{"foo": "bar"},
			objNamespace: "default",
			expected:     true,
		},
		{
			name:         "empty matchLabels matches all",
			selector:     &config.Selector{MatchLabels: map[string]string{}},
			objLabels:    map[string]string{"foo": "bar"},
			objNamespace: "default",
			expected:     true,
		},
		{
			name: "single label match",
			selector: &config.Selector{
				MatchLabels: map[string]string{"env": "prod"},
			},
			objLabels:    map[string]string{"env": "prod"},
			objNamespace: "default",
			expected:     true,
		},
		{
			name: "multiple labels all match",
			selector: &config.Selector{
				MatchLabels: map[string]string{
					"env":  "prod",
					"tier": "frontend",
				},
			},
			objLabels: map[string]string{
				"env":     "prod",
				"tier":    "frontend",
				"version": "v1",
			},
			objNamespace: "default",
			expected:     true,
		},
		{
			name: "one label mismatch",
			selector: &config.Selector{
				MatchLabels: map[string]string{
					"env":  "prod",
					"tier": "frontend",
				},
			},
			objLabels: map[string]string{
				"env":  "prod",
				"tier": "backend",
			},
			objNamespace: "default",
			expected:     false,
		},
		{
			name: "missing required label",
			selector: &config.Selector{
				MatchLabels: map[string]string{
					"env":  "prod",
					"tier": "frontend",
				},
			},
			objLabels: map[string]string{
				"env": "prod",
			},
			objNamespace: "default",
			expected:     false,
		},
		{
			name: "nil labels on object",
			selector: &config.Selector{
				MatchLabels: map[string]string{"env": "prod"},
			},
			objLabels:    nil,
			objNamespace: "default",
			expected:     false,
		},
		{
			name: "namespace match",
			selector: &config.Selector{
				MatchNamespaces: []string{"system", "default"},
			},
			objLabels:    map[string]string{"env": "prod"},
			objNamespace: "system",
			expected:     true,
		},
		{
			name: "namespace mismatch",
			selector: &config.Selector{
				MatchNamespaces: []string{"system"},
			},
			objLabels:    map[string]string{"env": "prod"},
			objNamespace: "default",
			expected:     false,
		},
		{
			name: "namespace and label match",
			selector: &config.Selector{
				MatchLabels:     map[string]string{"env": "prod"},
				MatchNamespaces: []string{"default"},
			},
			objLabels:    map[string]string{"env": "prod"},
			objNamespace: "default",
			expected:     true,
		},
		{
			name: "namespace match but label mismatch",
			selector: &config.Selector{
				MatchLabels:     map[string]string{"env": "prod"},
				MatchNamespaces: []string{"default"},
			},
			objLabels:    map[string]string{"env": "dev"},
			objNamespace: "default",
			expected:     false,
		},
		{
			name: "namespace set but object cluster-scoped",
			selector: &config.Selector{
				MatchNamespaces: []string{"default"},
			},
			objLabels:    map[string]string{"env": "prod"},
			objNamespace: "",
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := &FromHostSyncer{
				namespaced: true,
				cfg:        testSyncerConfig(config.SyncResource{APIVersion: "v1", Kind: "ConfigMap", Selector: tt.selector}),
			}

			pObj := &unstructured.Unstructured{}
			pObj.SetLabels(tt.objLabels)
			pObj.SetNamespace(tt.objNamespace)

			result, _ := syncer.matchesSelector(pObj)
			if result != tt.expected {
				t.Errorf("matchesSelector() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestFromHostSyncer_Name(t *testing.T) {
	syncer := &FromHostSyncer{
		name: "generic-fromHost-gateway.networking.k8s.io-gateway",
	}

	if syncer.Name() != "generic-fromHost-gateway.networking.k8s.io-gateway" {
		t.Errorf("Name() = %q, expected %q", syncer.Name(), "generic-fromHost-gateway.networking.k8s.io-gateway")
	}
}

func TestFromHostSyncer_Resource(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}
	syncer := &FromHostSyncer{
		name: "test-syncer",
		gvk:  gvk,
	}

	obj := syncer.Resource()
	u, ok := obj.(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("Resource() returned %T, expected *unstructured.Unstructured", obj)
	}

	objGVK := u.GetObjectKind().GroupVersionKind()
	if objGVK != gvk {
		t.Errorf("Resource().GVK = %v, expected %v", objGVK, gvk)
	}
}

func TestFromHostSyncer_GroupVersionKind(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1beta1", Kind: "HTTPRoute"}
	syncer := &FromHostSyncer{
		gvk: gvk,
	}

	result := syncer.GroupVersionKind()
	if result != gvk {
		t.Errorf("GroupVersionKind() = %v, expected %v", result, gvk)
	}
}

func TestFromHostSyncer_ClusterScoped(t *testing.T) {
	syncer := &FromHostSyncer{
		name:            "test-syncer",
		gvk:             schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Node"},
		targetNamespace: "host-ns",
		vclusterName:    "my-vcluster",
		namespaced:      false,
	}

	req := types.NamespacedName{Name: "node-a"}
	result := syncer.VirtualToHost(nil, req, nil)
	if result.Name != "node-a" || result.Namespace != "" {
		t.Errorf("VirtualToHost() = %v, expected name-only", result)
	}

	pObj := &unstructured.Unstructured{}
	pObj.SetLabels(map[string]string{"shared": "true"})
	translated := syncer.HostToVirtual(nil, types.NamespacedName{Name: "node-a"}, pObj)
	if translated.Name != "node-a" || translated.Namespace != "" {
		t.Errorf("HostToVirtual() = %v, expected name-only", translated)
	}
}

func TestFromHostSyncer_Sync_DeletesVirtualOnSelectorMismatch(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("widget-a")
	vObj.SetNamespace("default")

	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("widget-a")
	pObj.SetNamespace("host-ns")
	pObj.SetLabels(map[string]string{"sync": "false"})

	scheme := runtime.NewScheme()
	vClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vObj).Build()

	syncer := &FromHostSyncer{
		gvk:        gvk,
		namespaced: true,
		cfg: config.SyncerConfig{
			Resource: config.SyncResource{
				Selector: &config.Selector{
					MatchLabels: map[string]string{"sync": "true"},
				},
			},
		},
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: vClient,
		Log:           loghelper.New("test"),
	}

	event := &synccontext.SyncEvent[*unstructured.Unstructured]{
		Virtual: vObj,
		Host:    pObj,
	}

	if _, err := syncer.Sync(syncCtx, event); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(gvk)
	err := vClient.Get(context.Background(), client.ObjectKeyFromObject(vObj), fetched)
	if !errors.IsNotFound(err) {
		t.Fatalf("expected virtual object to be deleted, got err=%v", err)
	}
}

func TestFromHostSyncer_SyncToHost_MirrorDeletesVirtual(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("widget-a")
	vObj.SetNamespace("default")

	vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(vObj).Build()

	syncer := &FromHostSyncer{
		gvk:        gvk,
		namespaced: true,
		cfg:        config.SyncerConfig{Resource: config.SyncResource{Mode: config.Mirror}},
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: vClient,
		Log:           loghelper.New("test"),
	}

	event := &synccontext.SyncToHostEvent[*unstructured.Unstructured]{
		Virtual: vObj,
	}

	if _, err := syncer.SyncToHost(syncCtx, event); err != nil {
		t.Fatalf("SyncToHost() error: %v", err)
	}

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(gvk)
	err := vClient.Get(context.Background(), client.ObjectKeyFromObject(vObj), fetched)
	if !errors.IsNotFound(err) {
		t.Fatalf("expected virtual object to be deleted, got err=%v", err)
	}
}

func TestFromHostSyncer_StatusEnabled(t *testing.T) {
	syncer := &FromHostSyncer{
		cfg:                  config.SyncerConfig{Resource: config.SyncResource{Mode: config.Sync, StatusSync: true}},
		hasStatusSubresource: true,
	}
	if !syncer.statusEnabled() {
		t.Fatalf("expected statusEnabled to be true")
	}

	syncer.cfg.Resource.Mode = config.Mirror
	if syncer.statusEnabled() {
		t.Fatalf("expected statusEnabled to be false in mirror mode")
	}

	syncer.cfg.Resource.Mode = config.Sync
	syncer.hasStatusSubresource = false
	if syncer.statusEnabled() {
		t.Fatalf("expected statusEnabled to be false without status subresource")
	}
}
