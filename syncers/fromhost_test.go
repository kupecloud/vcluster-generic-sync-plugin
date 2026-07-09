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
	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
	"github.com/kupecloud/vcluster-generic-sync-plugin/patches"
)

// testSyncerConfig creates a SyncerConfig with properly initialised NamespaceMatcher for tests
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
		objAnnotations    map[string]string
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
			// De-selected host objects must still map to their canonical virtual
			// location so the SDK enqueues them and Sync can clean up the stale copy.
			name: "non-matching selector still maps to canonical location",
			selector: &config.Selector{
				MatchLabels: map[string]string{"app": "gateway"},
			},
			objLabels:         map[string]string{"app": "other"},
			reqName:           "my-gateway",
			targetNamespace:   "",
			expectedName:      "my-gateway",
			expectedNamespace: "default",
		},
		{
			name: "missing label still maps to canonical location",
			selector: &config.Selector{
				MatchLabels: map[string]string{"app": "gateway"},
			},
			objLabels:         map[string]string{},
			reqName:           "my-gateway",
			targetNamespace:   "",
			expectedName:      "my-gateway",
			expectedNamespace: "default",
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
		{
			name:              "annotation overrides default namespace",
			selector:          nil,
			objLabels:         nil,
			objAnnotations:    map[string]string{targetNamespaceAnnotation: "custom-ns"},
			reqName:           "my-secret",
			targetNamespace:   "",
			expectedName:      "my-secret",
			expectedNamespace: "custom-ns",
		},
		{
			name:              "annotation overrides config namespace",
			selector:          nil,
			objLabels:         nil,
			objAnnotations:    map[string]string{targetNamespaceAnnotation: "override-ns"},
			reqName:           "my-secret",
			targetNamespace:   "config-ns",
			expectedName:      "my-secret",
			expectedNamespace: "override-ns",
		},
		{
			name:              "empty annotation falls back to config namespace",
			selector:          nil,
			objLabels:         nil,
			objAnnotations:    map[string]string{targetNamespaceAnnotation: ""},
			reqName:           "my-secret",
			targetNamespace:   "config-ns",
			expectedName:      "my-secret",
			expectedNamespace: "config-ns",
		},
		{
			name:              "invalid annotation with slash is ignored",
			selector:          nil,
			objLabels:         nil,
			objAnnotations:    map[string]string{targetNamespaceAnnotation: "../../etc"},
			reqName:           "my-secret",
			targetNamespace:   "config-ns",
			expectedName:      "my-secret",
			expectedNamespace: "config-ns",
		},
		{
			name:              "invalid annotation with uppercase is ignored",
			selector:          nil,
			objLabels:         nil,
			objAnnotations:    map[string]string{targetNamespaceAnnotation: "InvalidNS"},
			reqName:           "my-secret",
			targetNamespace:   "config-ns",
			expectedName:      "my-secret",
			expectedNamespace: "config-ns",
		},
		{
			name:              "invalid annotation with dots is ignored",
			selector:          nil,
			objLabels:         nil,
			objAnnotations:    map[string]string{targetNamespaceAnnotation: "ns.with.dots"},
			reqName:           "my-secret",
			targetNamespace:   "",
			expectedName:      "my-secret",
			expectedNamespace: "default",
		},
		{
			name:              "kube-system annotation is ignored (VGSP-8)",
			selector:          nil,
			objLabels:         nil,
			objAnnotations:    map[string]string{targetNamespaceAnnotation: "kube-system"},
			reqName:           "my-secret",
			targetNamespace:   "config-ns",
			expectedName:      "my-secret",
			expectedNamespace: "config-ns",
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
				log:              logging.Log,
			}

			pObj := &unstructured.Unstructured{}
			pObj.SetLabels(tt.objLabels)
			if tt.objAnnotations != nil {
				pObj.SetAnnotations(tt.objAnnotations)
			}
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
			// De-selected objects stay managed so they keep reconciling and Sync can
			// delete the previously imported copy (selector-mismatch cleanup).
			name: "non-matching selector - still managed for cleanup",
			selector: &config.Selector{
				MatchLabels: map[string]string{"shared": "true"},
			},
			objLabels: map[string]string{"shared": "false"},
			expected:  true,
		},
		{
			name: "missing label - still managed for cleanup",
			selector: &config.Selector{
				MatchLabels: map[string]string{"shared": "true"},
			},
			objLabels: map[string]string{},
			expected:  true,
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

// TestFromHostSyncer_Sync_DeletesVirtualOnSelectorMismatch covers the de-labelling
// cleanup path: when the host object no longer matches the selector (e.g. the platform
// removed the sync label to stop sharing it), the previously imported virtual copy —
// identified by the provenance annotation — must be deleted from the vCluster.
func TestFromHostSyncer_Sync_DeletesVirtualOnSelectorMismatch(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("widget-a")
	vObj.SetNamespace("default")
	vObj.SetAnnotations(map[string]string{syncedFromAnnotation: "host-ns/widget-a"})

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

// TestFromHostSyncer_Sync_KeepsUserObjectOnSelectorMismatch covers VGSP-5: a virtual
// object WITHOUT the provenance annotation paired with a de-selected host object is a
// user's own object (paired by name via VirtualToHost) and must never be deleted.
func TestFromHostSyncer_Sync_KeepsUserObjectOnSelectorMismatch(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	userObj := &unstructured.Unstructured{}
	userObj.SetGroupVersionKind(gvk)
	userObj.SetName("widget-a")
	userObj.SetNamespace("default")
	// No provenance annotation: this is a tenant's own object.

	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("widget-a")
	pObj.SetNamespace("host-ns")
	pObj.SetLabels(map[string]string{"sync": "false"})

	vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(userObj).Build()

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

	if _, err := syncer.Sync(syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{
		Virtual: userObj,
		Host:    pObj,
	}); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(gvk)
	if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(userObj), fetched); err != nil {
		t.Fatalf("VGSP-5: expected user object to be preserved on selector mismatch, got err=%v", err)
	}
}

// TestFromHostSyncer_Sync_IgnoresNonCanonicalVirtualObject covers VGSP-5: VirtualToHost
// maps any virtual name to {targetNamespace}/{name} regardless of the virtual namespace,
// so the SDK can pair a user-created object (same name, different virtual namespace) with
// a host object. Sync must treat that as unrelated and leave the user's spec untouched,
// rather than overwriting it with host content.
func TestFromHostSyncer_Sync_IgnoresNonCanonicalVirtualObject(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	// Host object lives in the source namespace; its canonical virtual location is the
	// configured target namespace "imported".
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("widget-a")
	pObj.SetNamespace("host-ns")
	_ = unstructured.SetNestedField(pObj.Object, "from-host", "spec", "source")

	// User's own object: same name, but in a DIFFERENT virtual namespace than the
	// canonical import location, with its own spec that must not be clobbered.
	userObj := &unstructured.Unstructured{}
	userObj.SetGroupVersionKind(gvk)
	userObj.SetName("widget-a")
	userObj.SetNamespace("user-ns")
	_ = unstructured.SetNestedField(userObj.Object, "user-owned", "spec", "source")

	vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(userObj).Build()

	syncer := &FromHostSyncer{
		gvk:              gvk,
		namespaced:       true,
		targetNamespace:  "host-ns",
		virtualNamespace: "imported",
		cfg:              testSyncerConfig(config.SyncResource{}),
		patcher:          patches.NewPatcher(nil, "my-vcluster", "host-ns", false),
		log:              logging.Log,
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: vClient,
		Log:           loghelper.New("test"),
	}

	if _, err := syncer.Sync(syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{
		Virtual: userObj,
		Host:    pObj,
	}); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(gvk)
	if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(userObj), fetched); err != nil {
		t.Fatalf("expected user object to still exist, got err=%v", err)
	}
	if src, _, _ := unstructured.NestedString(fetched.Object, "spec", "source"); src != "user-owned" {
		t.Errorf("VGSP-5: user object spec was hijacked: spec.source = %q, want \"user-owned\"", src)
	}
}

// TestFromHostSyncer_Sync_DeletesStaleCopyOnTargetNamespaceChange covers MEDIUM-2: when
// a host object's kupe.cloud/target-namespace annotation changes (old location A → new
// canonical location B), the syncer's own stale copy stranded in A — identified by THIS
// host source's provenance — must be deleted rather than left frozen with stale data.
func TestFromHostSyncer_Sync_DeletesStaleCopyOnTargetNamespaceChange(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}

	// Host object now points at namespace-b via the target-namespace annotation.
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("my-secret")
	pObj.SetNamespace("host-ns")
	pObj.SetAnnotations(map[string]string{targetNamespaceAnnotation: "namespace-b"})

	// Old imported copy left behind in namespace-a, stamped with this host source.
	staleObj := &unstructured.Unstructured{}
	staleObj.SetGroupVersionKind(gvk)
	staleObj.SetName("my-secret")
	staleObj.SetNamespace("namespace-a")
	staleObj.SetAnnotations(map[string]string{syncedFromAnnotation: "host-ns/my-secret"})

	vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(staleObj).Build()

	syncer := &FromHostSyncer{
		gvk:              gvk,
		namespaced:       true,
		targetNamespace:  "host-ns",
		virtualNamespace: "namespace-a",
		cfg:              testSyncerConfig(config.SyncResource{}),
		patcher:          patches.NewPatcher(nil, "my-vcluster", "host-ns", false),
		log:              logging.Log,
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: vClient,
		Log:           loghelper.New("test"),
	}

	if _, err := syncer.Sync(syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{
		Virtual: staleObj,
		Host:    pObj,
	}); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(gvk)
	if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(staleObj), fetched); !errors.IsNotFound(err) {
		t.Fatalf("MEDIUM-2: expected stale copy at old location to be deleted, got err=%v", err)
	}
}

// TestFromHostSyncer_Sync_KeepsUserObjectAtNonCanonicalLocation covers MEDIUM-2/VGSP-5:
// a tenant's own object at a non-canonical location (no provenance annotation) paired by
// name with a host object must NOT be deleted by the canonical guard.
func TestFromHostSyncer_Sync_KeepsUserObjectAtNonCanonicalLocation(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}

	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("my-secret")
	pObj.SetNamespace("host-ns")
	pObj.SetAnnotations(map[string]string{targetNamespaceAnnotation: "namespace-b"})

	// Tenant's own object, same name, sitting in namespace-a with NO provenance.
	userObj := &unstructured.Unstructured{}
	userObj.SetGroupVersionKind(gvk)
	userObj.SetName("my-secret")
	userObj.SetNamespace("namespace-a")

	vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(userObj).Build()

	syncer := &FromHostSyncer{
		gvk:              gvk,
		namespaced:       true,
		targetNamespace:  "host-ns",
		virtualNamespace: "namespace-a",
		cfg:              testSyncerConfig(config.SyncResource{}),
		patcher:          patches.NewPatcher(nil, "my-vcluster", "host-ns", false),
		log:              logging.Log,
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: vClient,
		Log:           loghelper.New("test"),
	}

	if _, err := syncer.Sync(syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{
		Virtual: userObj,
		Host:    pObj,
	}); err != nil {
		t.Fatalf("Sync() error: %v", err)
	}

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(gvk)
	if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(userObj), fetched); err != nil {
		t.Fatalf("MEDIUM-2: expected tenant object without provenance to be preserved, got err=%v", err)
	}
}

// TestFromHostSyncer_SyncToHost_MirrorDeletesVirtual: a stale mirror copy — created by
// the syncer (provenance annotation) whose host source is gone — is deleted.
func TestFromHostSyncer_SyncToHost_MirrorDeletesVirtual(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("widget-a")
	vObj.SetNamespace("default")
	vObj.SetAnnotations(map[string]string{syncedFromAnnotation: "host-ns/widget-a"})

	vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(vObj).Build()

	syncer := &FromHostSyncer{
		gvk:             gvk,
		namespaced:      true,
		targetNamespace: "host-ns",
		cfg:             config.SyncerConfig{Resource: config.SyncResource{Mode: config.Mirror}},
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

// TestFromHostSyncer_SyncToHost_MirrorKeepsTenantObject: a tenant's own object of a
// mirrored GVK (e.g. their own Gateway in their own namespace, no provenance
// annotation) must NOT be deleted by mirror mode.
func TestFromHostSyncer_SyncToHost_MirrorKeepsTenantObject(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}

	tenantObj := &unstructured.Unstructured{}
	tenantObj.SetGroupVersionKind(gvk)
	tenantObj.SetName("my-gateway")
	tenantObj.SetNamespace("myapp")
	// No provenance annotation: this is the tenant's own Gateway.

	vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(tenantObj).Build()

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

	if _, err := syncer.SyncToHost(syncCtx, &synccontext.SyncToHostEvent[*unstructured.Unstructured]{
		Virtual: tenantObj,
	}); err != nil {
		t.Fatalf("SyncToHost() error: %v", err)
	}

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(gvk)
	if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(tenantObj), fetched); err != nil {
		t.Fatalf("expected tenant-owned object to be preserved in mirror mode, got err=%v", err)
	}
}

// TestFromHostSyncer_SyncToHost_SyncDeletesStampedOrphan covers VGSP-3: in default
// sync mode, when the host source is deleted the syncer-created virtual copy (carrying
// the provenance annotation) is deleted, while a user-created object (no annotation) is
// left untouched.
func TestFromHostSyncer_SyncToHost_SyncDeletesStampedOrphan(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	stamped := &unstructured.Unstructured{}
	stamped.SetGroupVersionKind(gvk)
	stamped.SetName("synced-widget")
	stamped.SetNamespace("default")
	stamped.SetAnnotations(map[string]string{syncedFromAnnotation: "host-ns/synced-widget"})

	vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(stamped).Build()

	syncer := &FromHostSyncer{
		gvk:             gvk,
		namespaced:      true,
		targetNamespace: "host-ns",
		cfg:             config.SyncerConfig{Resource: config.SyncResource{Mode: config.Sync}},
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: vClient,
		Log:           loghelper.New("test"),
	}

	if _, err := syncer.SyncToHost(syncCtx, &synccontext.SyncToHostEvent[*unstructured.Unstructured]{Virtual: stamped}); err != nil {
		t.Fatalf("SyncToHost() error: %v", err)
	}

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(gvk)
	if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(stamped), fetched); !errors.IsNotFound(err) {
		t.Fatalf("expected stamped orphan to be deleted, got err=%v", err)
	}
}

func TestFromHostSyncer_SyncToHost_SyncKeepsUserObject(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	userObj := &unstructured.Unstructured{}
	userObj.SetGroupVersionKind(gvk)
	userObj.SetName("user-widget")
	userObj.SetNamespace("default")
	// No provenance annotation: this is a tenant's own object.

	vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(userObj).Build()

	syncer := &FromHostSyncer{
		gvk:        gvk,
		namespaced: true,
		cfg:        config.SyncerConfig{Resource: config.SyncResource{Mode: config.Sync}},
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: vClient,
		Log:           loghelper.New("test"),
	}

	if _, err := syncer.SyncToHost(syncCtx, &synccontext.SyncToHostEvent[*unstructured.Unstructured]{Virtual: userObj}); err != nil {
		t.Fatalf("SyncToHost() error: %v", err)
	}

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(gvk)
	if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(userObj), fetched); err != nil {
		t.Fatalf("expected user object to be preserved, got err=%v", err)
	}
}

// TestFromHostSyncer_SyncToHost_SyncKeepsTenantSelfCopy covers C7: a tenant copies a
// synced object to a NEW name inside their vCluster (kubectl preserves the provenance
// annotation, which still points at the ORIGINAL host source). The copy has no host
// counterpart of its own, so it reaches the orphan path — but its annotation
// ("host-ns/original") does not equal its own mapped source ("host-ns/renamed-copy"), and
// the claimed source still exists, so it must NOT be deleted. Gating on mere annotation
// non-emptiness (the pre-C7 behaviour) would wrongly delete the tenant's copy.
func TestFromHostSyncer_SyncToHost_SyncKeepsTenantSelfCopy(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}

	selfCopy := &unstructured.Unstructured{}
	selfCopy.SetGroupVersionKind(gvk)
	selfCopy.SetName("renamed-copy")
	selfCopy.SetNamespace("default")
	// Annotation inherited from the original synced object, not this copy's own source.
	selfCopy.SetAnnotations(map[string]string{syncedFromAnnotation: "host-ns/original"})

	vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(selfCopy).Build()

	syncer := &FromHostSyncer{
		gvk:             gvk,
		namespaced:      true,
		targetNamespace: "host-ns",
		cfg:             config.SyncerConfig{Resource: config.SyncResource{Mode: config.Sync}},
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: vClient,
		Log:           loghelper.New("test"),
	}

	if _, err := syncer.SyncToHost(syncCtx, &synccontext.SyncToHostEvent[*unstructured.Unstructured]{Virtual: selfCopy}); err != nil {
		t.Fatalf("SyncToHost() error: %v", err)
	}

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(gvk)
	if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(selfCopy), fetched); err != nil {
		t.Fatalf("C7: expected tenant self-copy (annotation points at a different, still-existing source) to be preserved, got err=%v", err)
	}
}

// TestFromHostSyncer_SyncToHost_MirrorKeepsTenantSelfCopy is the mirror-mode analogue of
// the C7 case above: mirror cleanup must likewise refuse to delete a renamed tenant copy
// whose inherited annotation maps to a different host source.
func TestFromHostSyncer_SyncToHost_MirrorKeepsTenantSelfCopy(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}

	selfCopy := &unstructured.Unstructured{}
	selfCopy.SetGroupVersionKind(gvk)
	selfCopy.SetName("renamed-gateway")
	selfCopy.SetNamespace("default")
	selfCopy.SetAnnotations(map[string]string{syncedFromAnnotation: "host-ns/original-gateway"})

	vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(selfCopy).Build()

	syncer := &FromHostSyncer{
		gvk:             gvk,
		namespaced:      true,
		targetNamespace: "host-ns",
		cfg:             config.SyncerConfig{Resource: config.SyncResource{Mode: config.Mirror}},
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: vClient,
		Log:           loghelper.New("test"),
	}

	if _, err := syncer.SyncToHost(syncCtx, &synccontext.SyncToHostEvent[*unstructured.Unstructured]{Virtual: selfCopy}); err != nil {
		t.Fatalf("SyncToHost() error: %v", err)
	}

	fetched := &unstructured.Unstructured{}
	fetched.SetGroupVersionKind(gvk)
	if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(selfCopy), fetched); err != nil {
		t.Fatalf("C7: expected tenant self-copy to be preserved in mirror mode, got err=%v", err)
	}
}

// TestFromHostSyncer_IsManaged_PinsToSourceNamespace covers VGSP-4: cache widening
// can deliver host objects from shared namespaces; the syncer must only manage objects
// in its own source (target) namespace.
func TestFromHostSyncer_IsManaged_PinsToSourceNamespace(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"}
	s := &FromHostSyncer{
		gvk:             gvk,
		namespaced:      true,
		targetNamespace: "vcluster-acme--prod",
		cfg:             testSyncerConfig(config.SyncResource{}),
		metrics:         nil,
	}

	inSource := &unstructured.Unstructured{}
	inSource.SetGroupVersionKind(gvk)
	inSource.SetNamespace("vcluster-acme--prod")
	inSource.SetName("mysecret")
	if managed, _ := s.IsManaged(nil, inSource); !managed {
		t.Error("VGSP-4: expected object in source namespace to be managed")
	}

	shared := &unstructured.Unstructured{}
	shared.SetGroupVersionKind(gvk)
	shared.SetNamespace("argocd")
	shared.SetName("mysecret")
	if managed, _ := s.IsManaged(nil, shared); managed {
		t.Error("VGSP-4: expected object in shared namespace to NOT be managed")
	}
}

// TestFromHostSyncer_SyncToVirtual_EnsuresTargetNamespace covers VGSP-8: the target
// virtual namespace is created if it doesn't exist, so Create doesn't fail NotFound
// forever.
func TestFromHostSyncer_SyncToVirtual_EnsuresTargetNamespace(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("widget-a")
	pObj.SetNamespace("host-ns")

	virtualClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()

	syncer := &FromHostSyncer{
		gvk:              gvk,
		namespaced:       true,
		virtualNamespace: "imported",
		cfg:              config.SyncerConfig{Resource: config.SyncResource{Mode: config.Mirror}},
		patcher:          patches.NewPatcher(nil, "my-vcluster", "host-ns", false),
		log:              logging.Log,
	}

	syncCtx := &synccontext.SyncContext{
		Context:       context.Background(),
		VirtualClient: virtualClient,
		Log:           loghelper.New("test"),
	}

	if _, err := syncer.SyncToVirtual(syncCtx, &synccontext.SyncToVirtualEvent[*unstructured.Unstructured]{Host: pObj}); err != nil {
		t.Fatalf("SyncToVirtual() error: %v", err)
	}

	createdNS := &unstructured.Unstructured{}
	createdNS.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "Namespace"})
	if err := virtualClient.Get(context.Background(), types.NamespacedName{Name: "imported"}, createdNS); err != nil {
		t.Fatalf("expected target namespace to be created: %v", err)
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
