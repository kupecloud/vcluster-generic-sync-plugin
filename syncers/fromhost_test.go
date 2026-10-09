package syncers

import (
	"context"
	"strings"
	"testing"

	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	"github.com/loft-sh/vcluster/pkg/util/loghelper"
	"github.com/loft-sh/vcluster/pkg/util/translate"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
	"github.com/kupecloud/vcluster-generic-sync-plugin/metrics"
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
			name:              "kube-system annotation is ignored",
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

// TestFromHostSyncer_Sync_KeepsUserObjectOnSelectorMismatch: a virtual
// object WITHOUT the provenance annotation paired with a de-selected host object is a
// user's own object (paired by name via VirtualToHost) and must never be deleted.
func TestFromHostSyncer_Sync_KeepsUserObjectOnSelectorMismatch(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	userObj := &unstructured.Unstructured{}
	userObj.SetGroupVersionKind(gvk)
	userObj.SetName("widget-a")
	userObj.SetNamespace("default")
	// No provenance annotation: this is a user's own object.

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
		t.Fatalf("expected user object to be preserved on selector mismatch, got err=%v", err)
	}
}

// TestFromHostSyncer_Sync_IgnoresNonCanonicalVirtualObject: VirtualToHost
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
		t.Errorf("user object spec was hijacked: spec.source = %q, want \"user-owned\"", src)
	}
}

// TestFromHostSyncer_Sync_DeletesStaleCopyOnTargetNamespaceChange: when
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
		t.Fatalf("expected stale copy at old location to be deleted, got err=%v", err)
	}
}

// TestFromHostSyncer_Sync_KeepsUserObjectAtNonCanonicalLocation:
// a user's own object at a non-canonical location (no provenance annotation) paired by
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
		t.Fatalf("expected tenant object without provenance to be preserved, got err=%v", err)
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

// TestFromHostSyncer_SyncToHost_MirrorKeepsTenantObject: a user's own object of a
// mirrored GVK (e.g. their own Gateway in their own namespace, no provenance
// annotation) must NOT be deleted by mirror mode.
func TestFromHostSyncer_SyncToHost_MirrorKeepsTenantObject(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "Gateway"}

	tenantObj := &unstructured.Unstructured{}
	tenantObj.SetGroupVersionKind(gvk)
	tenantObj.SetName("my-gateway")
	tenantObj.SetNamespace("myapp")
	// No provenance annotation: this is the user's own Gateway.

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

// TestFromHostSyncer_SyncToHost_SyncDeletesStampedOrphan: in default
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
	// No provenance annotation: this is a user's own object.

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

// TestFromHostSyncer_SyncToHost_SyncKeepsTenantSelfCopy: a user copies a
// synced object to a NEW name inside their vCluster (kubectl preserves the provenance
// annotation, which still points at the ORIGINAL host source). The copy has no host
// counterpart of its own, so it reaches the orphan path — but its annotation
// ("host-ns/original") does not equal its own mapped source ("host-ns/renamed-copy"), and
// the claimed source still exists, so it must NOT be deleted. Gating on mere annotation
// non-emptiness would wrongly delete the user's copy.
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
		t.Fatalf("expected tenant self-copy (annotation points at a different, still-existing source) to be preserved, got err=%v", err)
	}
}

// TestFromHostSyncer_SyncToHost_MirrorKeepsTenantSelfCopy is the mirror-mode analogue of
// the case above: mirror cleanup must likewise refuse to delete a renamed user copy
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
		t.Fatalf("expected tenant self-copy to be preserved in mirror mode, got err=%v", err)
	}
}

// TestFromHostSyncer_IsManaged_PinsToSourceNamespace: cache widening
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
		t.Error("expected object in source namespace to be managed")
	}

	shared := &unstructured.Unstructured{}
	shared.SetGroupVersionKind(gvk)
	shared.SetNamespace("argocd")
	shared.SetName("mysecret")
	if managed, _ := s.IsManaged(nil, shared); managed {
		t.Error("expected object in shared namespace to NOT be managed")
	}
}

// TestFromHostSyncer_SyncToVirtual_EnsuresTargetNamespace: the target
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

// testHostObject builds an unstructured object of gvk at namespace/name with the given
// annotations (nil for none).
func testHostObject(gvk schema.GroupVersionKind, namespace, name string, annotations map[string]string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetNamespace(namespace)
	obj.SetName(name)
	if annotations != nil {
		obj.SetAnnotations(annotations)
	}
	return obj
}

// newTargetNameTestSyncer returns a namespaced fromHost syncer reading from host-ns and
// importing into "default".
func newTargetNameTestSyncer(gvk schema.GroupVersionKind) *FromHostSyncer {
	return &FromHostSyncer{
		gvk:             gvk,
		namespaced:      true,
		targetNamespace: "host-ns",
		cfg:             testSyncerConfig(config.SyncResource{APIVersion: gvk.GroupVersion().String(), Kind: gvk.Kind}),
		patcher:         patches.NewPatcher(nil, "my-vcluster", "host-ns", false),
		log:             logging.Log,
	}
}

func TestFromHostSyncer_HostToVirtual_TargetName(t *testing.T) {
	secretGVK := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
	serviceGVK := schema.GroupVersionKind{Version: "v1", Kind: "Service"}

	tests := []struct {
		name        string
		gvk         schema.GroupVersionKind
		namespaced  bool
		annotations map[string]string
		want        types.NamespacedName
	}{
		{
			name:        "valid target name renames the copy",
			gvk:         secretGVK,
			namespaced:  true,
			annotations: map[string]string{targetNameAnnotation: "db-creds"},
			want:        types.NamespacedName{Namespace: "default", Name: "db-creds"},
		},
		{
			name:        "target name combines with target namespace",
			gvk:         secretGVK,
			namespaced:  true,
			annotations: map[string]string{targetNameAnnotation: "db-creds", targetNamespaceAnnotation: "app"},
			want:        types.NamespacedName{Namespace: "app", Name: "db-creds"},
		},
		{
			name:        "empty target name keeps the host name",
			gvk:         secretGVK,
			namespaced:  true,
			annotations: map[string]string{targetNameAnnotation: ""},
			want:        types.NamespacedName{Namespace: "default", Name: "host-obj"},
		},
		{
			name:        "invalid target name skips the object instead of falling back to the host name",
			gvk:         secretGVK,
			namespaced:  true,
			annotations: map[string]string{targetNameAnnotation: "Not_Valid"},
			want:        types.NamespacedName{},
		},
		{
			name:        "invalid target name skips the object even with a valid target namespace",
			gvk:         secretGVK,
			namespaced:  true,
			annotations: map[string]string{targetNameAnnotation: "a/b", targetNamespaceAnnotation: "app"},
			want:        types.NamespacedName{},
		},
		{
			name:        "service accepts a dns label",
			gvk:         serviceGVK,
			namespaced:  true,
			annotations: map[string]string{targetNameAnnotation: "orders"},
			want:        types.NamespacedName{Namespace: "default", Name: "orders"},
		},
		{
			name:        "service rejects a subdomain that is not a dns label",
			gvk:         serviceGVK,
			namespaced:  true,
			annotations: map[string]string{targetNameAnnotation: "orders.db"},
			want:        types.NamespacedName{},
		},
		{
			name:        "cluster-scoped object is renamed",
			gvk:         schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "GatewayClass"},
			namespaced:  false,
			annotations: map[string]string{targetNameAnnotation: "shared"},
			want:        types.NamespacedName{Name: "shared"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTargetNameTestSyncer(tt.gvk)
			s.namespaced = tt.namespaced
			ns := "host-ns"
			if !tt.namespaced {
				ns = ""
			}
			pObj := testHostObject(tt.gvk, ns, "host-obj", tt.annotations)

			got := s.HostToVirtual(nil, client.ObjectKeyFromObject(pObj), pObj)
			if got != tt.want {
				t.Errorf("HostToVirtual() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFromHostSyncer_VirtualToHost_PairsRenamedCopyWithItsSource(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}

	tests := []struct {
		name        string
		vName       string
		annotations map[string]string
		want        types.NamespacedName
	}{
		{
			name:        "renamed copy maps to the host source it was synced from",
			vName:       "db-creds",
			annotations: map[string]string{targetNameAnnotation: "db-creds", syncedFromAnnotation: "host-ns/mdb-1a2b"},
			want:        types.NamespacedName{Namespace: "host-ns", Name: "mdb-1a2b"},
		},
		{
			name:        "copy under a different name than its target name keeps the name mapping",
			vName:       "my-copy",
			annotations: map[string]string{targetNameAnnotation: "db-creds", syncedFromAnnotation: "host-ns/mdb-1a2b"},
			want:        types.NamespacedName{Namespace: "host-ns", Name: "my-copy"},
		},
		{
			name:        "provenance outside the source namespace keeps the name mapping",
			vName:       "db-creds",
			annotations: map[string]string{targetNameAnnotation: "db-creds", syncedFromAnnotation: "argocd/mdb-1a2b"},
			want:        types.NamespacedName{Namespace: "host-ns", Name: "db-creds"},
		},
		{
			name:        "malformed provenance keeps the name mapping",
			vName:       "db-creds",
			annotations: map[string]string{targetNameAnnotation: "db-creds", syncedFromAnnotation: "mdb-1a2b"},
			want:        types.NamespacedName{Namespace: "host-ns", Name: "db-creds"},
		},
		{
			name:        "no provenance keeps the name mapping",
			vName:       "db-creds",
			annotations: map[string]string{targetNameAnnotation: "db-creds"},
			want:        types.NamespacedName{Namespace: "host-ns", Name: "db-creds"},
		},
		{
			name:  "object without annotations keeps the name mapping",
			vName: "db-creds",
			want:  types.NamespacedName{Namespace: "host-ns", Name: "db-creds"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTargetNameTestSyncer(gvk)
			vObj := testHostObject(gvk, "app", tt.vName, tt.annotations)

			got := s.VirtualToHost(nil, client.ObjectKeyFromObject(vObj), vObj)
			if got != tt.want {
				t.Errorf("VirtualToHost() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFromHostSyncer_SyncToVirtual_TargetName(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}

	tests := []struct {
		name        string
		annotations map[string]string
		wantCreated *types.NamespacedName
	}{
		{
			name:        "creates the copy under the target name in the target namespace",
			annotations: map[string]string{targetNameAnnotation: "db-creds", targetNamespaceAnnotation: "app"},
			wantCreated: &types.NamespacedName{Namespace: "app", Name: "db-creds"},
		},
		{
			name:        "creates nothing when the target name is invalid",
			annotations: map[string]string{targetNameAnnotation: "DB_CREDS", targetNamespaceAnnotation: "app"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pObj := testHostObject(gvk, "host-ns", "mdb-1a2b", tt.annotations)
			vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()
			syncCtx := &synccontext.SyncContext{Context: context.Background(), VirtualClient: vClient, Log: loghelper.New("test")}

			if _, err := newTargetNameTestSyncer(gvk).SyncToVirtual(syncCtx, &synccontext.SyncToVirtualEvent[*unstructured.Unstructured]{Host: pObj}); err != nil {
				t.Fatalf("SyncToVirtual() error: %v", err)
			}

			list := &unstructured.UnstructuredList{}
			list.SetGroupVersionKind(gvk.GroupVersion().WithKind("SecretList"))
			if err := vClient.List(context.Background(), list); err != nil {
				t.Fatalf("list virtual objects: %v", err)
			}
			if tt.wantCreated == nil {
				if len(list.Items) != 0 {
					t.Fatalf("expected no virtual object, got %d (first: %s/%s)", len(list.Items), list.Items[0].GetNamespace(), list.Items[0].GetName())
				}
				return
			}
			if len(list.Items) != 1 {
				t.Fatalf("expected exactly one virtual object, got %d", len(list.Items))
			}
			got := list.Items[0]
			if client.ObjectKeyFromObject(&got) != *tt.wantCreated {
				t.Errorf("virtual object created at %s, want %s", client.ObjectKeyFromObject(&got), *tt.wantCreated)
			}
			if src := got.GetAnnotations()[syncedFromAnnotation]; src != "host-ns/mdb-1a2b" {
				t.Errorf("provenance = %q, want host-ns/mdb-1a2b", src)
			}
		})
	}
}

// TestFromHostSyncer_Sync_RemovesStaleCopyWhenTargetLocationChanges: when the host
// object's target-name annotation changes, is removed, or becomes invalid, the copy the
// syncer created at the previous location is deleted — gated on provenance, so a
// tenant's own object at that location is never touched.
func TestFromHostSyncer_Sync_RemovesStaleCopyWhenTargetLocationChanges(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
	staleCopyAnnotations := map[string]string{
		targetNameAnnotation:      "old-name",
		targetNamespaceAnnotation: "app",
		syncedFromAnnotation:      "host-ns/mdb-1a2b",
	}

	tests := []struct {
		name            string
		hostAnnotations map[string]string
		virtualAnnots   map[string]string
		wantDeleted     bool
	}{
		{
			name:            "target name changed",
			hostAnnotations: map[string]string{targetNameAnnotation: "new-name", targetNamespaceAnnotation: "app"},
			virtualAnnots:   staleCopyAnnotations,
			wantDeleted:     true,
		},
		{
			name:            "target name removed",
			hostAnnotations: map[string]string{targetNamespaceAnnotation: "app"},
			virtualAnnots:   staleCopyAnnotations,
			wantDeleted:     true,
		},
		{
			name:            "target name became invalid",
			hostAnnotations: map[string]string{targetNameAnnotation: "Not_Valid", targetNamespaceAnnotation: "app"},
			virtualAnnots:   staleCopyAnnotations,
			wantDeleted:     true,
		},
		{
			name:            "target namespace changed while the name stayed",
			hostAnnotations: map[string]string{targetNameAnnotation: "old-name", targetNamespaceAnnotation: "other"},
			virtualAnnots:   staleCopyAnnotations,
			wantDeleted:     true,
		},
		{
			name:            "tenant object without provenance at the old location is kept",
			hostAnnotations: map[string]string{targetNameAnnotation: "new-name", targetNamespaceAnnotation: "app"},
			virtualAnnots:   map[string]string{targetNameAnnotation: "old-name"},
			wantDeleted:     false,
		},
		{
			name:            "copy synced from another host object is kept",
			hostAnnotations: map[string]string{targetNameAnnotation: "new-name", targetNamespaceAnnotation: "app"},
			virtualAnnots:   map[string]string{targetNameAnnotation: "old-name", syncedFromAnnotation: "host-ns/other"},
			wantDeleted:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pObj := testHostObject(gvk, "host-ns", "mdb-1a2b", tt.hostAnnotations)
			vObj := testHostObject(gvk, "app", "old-name", tt.virtualAnnots)
			vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(vObj).Build()
			syncCtx := &synccontext.SyncContext{Context: context.Background(), VirtualClient: vClient, Log: loghelper.New("test")}

			if _, err := newTargetNameTestSyncer(gvk).Sync(syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{Virtual: vObj, Host: pObj}); err != nil {
				t.Fatalf("Sync() error: %v", err)
			}

			err := vClient.Get(context.Background(), client.ObjectKeyFromObject(vObj), testHostObject(gvk, "", "", nil))
			if tt.wantDeleted && !errors.IsNotFound(err) {
				t.Fatalf("expected the stale copy to be deleted, got err=%v", err)
			}
			if !tt.wantDeleted && err != nil {
				t.Fatalf("expected the virtual object to be kept, got err=%v", err)
			}
		})
	}
}

// TestFromHostSyncer_SyncToHost_RenamedCopyDeletionPropagation: when the host source of
// a renamed copy is deleted the copy is deleted too, while a tenant's own copy of it
// under yet another name (inheriting both annotations) is kept.
func TestFromHostSyncer_SyncToHost_RenamedCopyDeletionPropagation(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}
	inherited := map[string]string{targetNameAnnotation: "db-creds", syncedFromAnnotation: "host-ns/mdb-1a2b"}

	tests := []struct {
		name        string
		vName       string
		wantDeleted bool
	}{
		{name: "renamed copy is deleted with its host source", vName: "db-creds", wantDeleted: true},
		{name: "tenant copy under another name is kept", vName: "my-copy", wantDeleted: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vObj := testHostObject(gvk, "app", tt.vName, inherited)
			vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(vObj).Build()
			syncCtx := &synccontext.SyncContext{Context: context.Background(), VirtualClient: vClient, Log: loghelper.New("test")}

			if _, err := newTargetNameTestSyncer(gvk).SyncToHost(syncCtx, &synccontext.SyncToHostEvent[*unstructured.Unstructured]{Virtual: vObj}); err != nil {
				t.Fatalf("SyncToHost() error: %v", err)
			}

			err := vClient.Get(context.Background(), client.ObjectKeyFromObject(vObj), testHostObject(gvk, "", "", nil))
			if tt.wantDeleted && !errors.IsNotFound(err) {
				t.Fatalf("expected the renamed copy to be deleted, got err=%v", err)
			}
			if !tt.wantDeleted && err != nil {
				t.Fatalf("expected the tenant copy to be kept, got err=%v", err)
			}
		})
	}
}

func TestFromHostSyncer_StaleLocation_EnqueuesPreviousLocation(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}

	tests := []struct {
		name      string
		hostNS    string
		oldAnnots map[string]string
		newAnnots map[string]string
		want      *types.NamespacedName
	}{
		{
			name:      "target name changed",
			hostNS:    "host-ns",
			oldAnnots: map[string]string{targetNameAnnotation: "old-name"},
			newAnnots: map[string]string{targetNameAnnotation: "new-name"},
			want:      &types.NamespacedName{Namespace: "default", Name: "old-name"},
		},
		{
			name:      "target name became invalid",
			hostNS:    "host-ns",
			oldAnnots: map[string]string{targetNameAnnotation: "old-name"},
			newAnnots: map[string]string{targetNameAnnotation: "Not_Valid"},
			want:      &types.NamespacedName{Namespace: "default", Name: "old-name"},
		},
		{
			name:      "target name added",
			hostNS:    "host-ns",
			newAnnots: map[string]string{targetNameAnnotation: "new-name"},
			want:      &types.NamespacedName{Namespace: "default", Name: "mdb-1a2b"},
		},
		{
			name:      "target namespace changed",
			hostNS:    "host-ns",
			oldAnnots: map[string]string{targetNamespaceAnnotation: "a"},
			newAnnots: map[string]string{targetNamespaceAnnotation: "b"},
			want:      &types.NamespacedName{Namespace: "a", Name: "mdb-1a2b"},
		},
		{
			name:      "location unchanged",
			hostNS:    "host-ns",
			oldAnnots: map[string]string{targetNameAnnotation: "same"},
			newAnnots: map[string]string{targetNameAnnotation: "same"},
		},
		{
			name:      "invalid before and after",
			hostNS:    "host-ns",
			oldAnnots: map[string]string{targetNameAnnotation: "Bad_1"},
			newAnnots: map[string]string{targetNameAnnotation: "Bad_2"},
		},
		{
			name:      "object outside the source namespace",
			hostNS:    "argocd",
			oldAnnots: map[string]string{targetNameAnnotation: "old-name"},
			newAnnots: map[string]string{targetNameAnnotation: "new-name"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldObj := testHostObject(gvk, tt.hostNS, "mdb-1a2b", tt.oldAnnots)
			newObj := testHostObject(gvk, tt.hostNS, "mdb-1a2b", tt.newAnnots)

			got, ok := newTargetNameTestSyncer(gvk).staleLocation(oldObj, newObj)
			if tt.want == nil {
				if ok {
					t.Errorf("staleLocation() enqueued %v, want nothing", got)
				}
				return
			}
			if !ok || got.NamespacedName != *tt.want {
				t.Errorf("staleLocation() = %v (ok=%v), want %v", got, ok, *tt.want)
			}
		})
	}
}

func TestFromHostSyncer_WarnInvalidTargetName_RecordsHostEvent(t *testing.T) {
	gvk := schema.GroupVersionKind{Version: "v1", Kind: "Secret"}

	tests := []struct {
		name      string
		labels    map[string]string
		annots    map[string]string
		wantEvent bool
	}{
		{name: "invalid target name on a selected object", labels: map[string]string{"sync": "true"}, annots: map[string]string{targetNameAnnotation: "Not_Valid"}, wantEvent: true},
		{name: "valid target name", labels: map[string]string{"sync": "true"}, annots: map[string]string{targetNameAnnotation: "db-creds"}, wantEvent: false},
		{name: "invalid target name on an object the selector excludes", labels: map[string]string{"sync": "false"}, annots: map[string]string{targetNameAnnotation: "Not_Valid"}, wantEvent: false},
		{name: "no target name", labels: map[string]string{"sync": "true"}, wantEvent: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := events.NewFakeRecorder(10)
			s := newTargetNameTestSyncer(gvk)
			s.cfg = testSyncerConfig(config.SyncResource{APIVersion: "v1", Kind: "Secret", Selector: &config.Selector{MatchLabels: map[string]string{"sync": "true"}}})
			s.hostEvents = logging.NewEventEmitter(recorder, string(config.FromHost), gvk.Kind)
			pObj := testHostObject(gvk, "host-ns", "mdb-1a2b", tt.annots)
			pObj.SetLabels(tt.labels)

			s.warnInvalidTargetName(pObj)

			select {
			case e := <-recorder.Events:
				if !tt.wantEvent {
					t.Fatalf("unexpected event: %s", e)
				}
				if !strings.Contains(e, logging.ReasonInvalidTargetName) {
					t.Errorf("event %q does not carry reason %s", e, logging.ReasonInvalidTargetName)
				}
			default:
				if tt.wantEvent {
					t.Fatal("expected an InvalidTargetName event on the host object")
				}
			}
		})
	}
}

func TestFromHostSyncer_VirtualControlledBy_StampsCopies(t *testing.T) {
	// A CRD kind: the fake client cannot apply merge patches to core kinds built as
	// unstructured objects without a scheme, and stamping does not depend on the kind.
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	tests := []struct {
		name          string
		enabled       bool
		existingLabel bool // the virtual copy already exists and carries the label
		wantLabel     bool
	}{
		{name: "create stamps the label when enabled", enabled: true, wantLabel: true},
		{name: "create leaves the label off by default", enabled: false, wantLabel: false},
		{name: "update keeps the label when enabled", enabled: true, existingLabel: true, wantLabel: true},
		{name: "update removes the label once disabled", enabled: false, existingLabel: true, wantLabel: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTargetNameTestSyncer(gvk)
			s.cfg = testSyncerConfig(config.SyncResource{APIVersion: "example.com/v1", Kind: "Widget", VirtualControlledBy: tt.enabled})

			pObj := testHostObject(gvk, "host-ns", "mdb-1a2b", map[string]string{targetNameAnnotation: "orders"})
			// A host-side controlled-by label must never leak through: only the option sets it.
			pObj.SetLabels(map[string]string{"app": "orders", translate.ControllerLabel: "host-controller"})

			builder := fake.NewClientBuilder().WithScheme(runtime.NewScheme())
			var existing *unstructured.Unstructured
			if tt.existingLabel {
				existing = testHostObject(gvk, "default", "orders", map[string]string{
					targetNameAnnotation: "orders",
					syncedFromAnnotation: "host-ns/mdb-1a2b",
				})
				existing.SetLabels(map[string]string{translate.ControllerLabel: controlledByLabelValue})
				builder = builder.WithObjects(existing)
			}
			vClient := builder.Build()
			syncCtx := &synccontext.SyncContext{Context: context.Background(), VirtualClient: vClient, Log: loghelper.New("test")}

			if existing == nil {
				if _, err := s.SyncToVirtual(syncCtx, &synccontext.SyncToVirtualEvent[*unstructured.Unstructured]{Host: pObj}); err != nil {
					t.Fatalf("SyncToVirtual() error: %v", err)
				}
			} else {
				if _, err := s.Sync(syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{Virtual: existing, Host: pObj}); err != nil {
					t.Fatalf("Sync() error: %v", err)
				}
			}

			got := testHostObject(gvk, "", "", nil)
			if err := vClient.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "orders"}, got); err != nil {
				t.Fatalf("get virtual copy: %v", err)
			}
			value, has := got.GetLabels()[translate.ControllerLabel]
			if has != tt.wantLabel || (tt.wantLabel && value != controlledByLabelValue) {
				t.Errorf("controlled-by label = %q (present=%v), want present=%v with value %q", value, has, tt.wantLabel, controlledByLabelValue)
			}
			if got.GetLabels()["app"] != "orders" {
				t.Errorf("host labels were not carried over: %v", got.GetLabels())
			}
		})
	}
}

// TestFromHostSyncer_VirtualControlledBy_CopyStaysManaged: a copy stamped
// controlled-by: generic-sync is still the plugin's own — not excluded, updated from the
// host, and deleted when its host source goes away.
func TestFromHostSyncer_VirtualControlledBy_CopyStaysManaged(t *testing.T) {
	// A CRD kind: the fake client cannot apply merge patches to core kinds built as
	// unstructured objects without a scheme, and stamping does not depend on the kind.
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	stampedCopy := func() *unstructured.Unstructured {
		obj := testHostObject(gvk, "default", "orders", map[string]string{
			targetNameAnnotation: "orders",
			syncedFromAnnotation: "host-ns/mdb-1a2b",
		})
		obj.SetLabels(map[string]string{translate.ControllerLabel: controlledByLabelValue})
		return obj
	}
	newSyncer := func() *FromHostSyncer {
		s := newTargetNameTestSyncer(gvk)
		s.cfg = testSyncerConfig(config.SyncResource{APIVersion: "example.com/v1", Kind: "Widget", VirtualControlledBy: true})
		return s
	}

	t.Run("not excluded by the plugin", func(t *testing.T) {
		if newSyncer().ExcludeVirtual(stampedCopy()) {
			t.Fatal("ExcludeVirtual() = true for a copy stamped controlled-by: generic-sync")
		}
	})

	t.Run("updated from the host", func(t *testing.T) {
		vObj := stampedCopy()
		vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(vObj).Build()
		syncCtx := &synccontext.SyncContext{Context: context.Background(), VirtualClient: vClient, Log: loghelper.New("test")}
		pObj := testHostObject(gvk, "host-ns", "mdb-1a2b", map[string]string{targetNameAnnotation: "orders"})
		_ = unstructured.SetNestedField(pObj.Object, "from-host", "spec", "source")

		if _, err := newSyncer().Sync(syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{Virtual: vObj, Host: pObj}); err != nil {
			t.Fatalf("Sync() error: %v", err)
		}
		got := testHostObject(gvk, "", "", nil)
		if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(vObj), got); err != nil {
			t.Fatalf("get virtual copy: %v", err)
		}
		if src, _, _ := unstructured.NestedString(got.Object, "spec", "source"); src != "from-host" {
			t.Errorf("spec.source = %q, want the host value from-host", src)
		}
	})

	t.Run("deleted with its host source", func(t *testing.T) {
		vObj := stampedCopy()
		vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(vObj).Build()
		syncCtx := &synccontext.SyncContext{Context: context.Background(), VirtualClient: vClient, Log: loghelper.New("test")}

		if _, err := newSyncer().SyncToHost(syncCtx, &synccontext.SyncToHostEvent[*unstructured.Unstructured]{Virtual: vObj}); err != nil {
			t.Fatalf("SyncToHost() error: %v", err)
		}
		if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(vObj), testHostObject(gvk, "", "", nil)); !errors.IsNotFound(err) {
			t.Fatalf("expected the stamped copy to be deleted, got err=%v", err)
		}
	})
}

// conflictTestEnv wires a fromHost Widget syncer to fake host and virtual clients and
// fake event recorders for the host and virtual sides.
type conflictTestEnv struct {
	syncer       *FromHostSyncer
	syncCtx      *synccontext.SyncContext
	hostClient   client.Client
	virtual      client.Client
	hostRec      *events.FakeRecorder
	virtualRec   *events.FakeRecorder
	conflictsMet func() float64
}

func newConflictTestEnv(t *testing.T, pObj *unstructured.Unstructured, hostFuncs *interceptor.Funcs, virtualObjs ...client.Object) *conflictTestEnv {
	t.Helper()
	gvk := pObj.GroupVersionKind()
	hostBuilder := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(pObj.DeepCopy())
	if hostFuncs != nil {
		hostBuilder = hostBuilder.WithInterceptorFuncs(*hostFuncs)
	}
	env := &conflictTestEnv{
		hostClient: hostBuilder.Build(),
		virtual:    fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(virtualObjs...).Build(),
		hostRec:    events.NewFakeRecorder(10),
		virtualRec: events.NewFakeRecorder(10),
	}
	env.syncer = newTargetNameTestSyncer(gvk)
	env.syncer.metrics = metrics.NewRecorder(metrics.DirectionFromHost, gvk.Kind)
	env.syncer.events = logging.NewEventEmitter(env.virtualRec, string(config.FromHost), gvk.Kind)
	env.syncer.hostEvents = logging.NewEventEmitter(env.hostRec, string(config.FromHost), gvk.Kind)
	env.syncCtx = &synccontext.SyncContext{Context: context.Background(), HostClient: env.hostClient, VirtualClient: env.virtual, Log: loghelper.New("test")}
	env.conflictsMet = func() float64 {
		return testutil.ToFloat64(metrics.SyncConflictsTotal.WithLabelValues(metrics.DirectionFromHost, gvk.Kind))
	}
	return env
}

func (e *conflictTestEnv) hostAnnotation(t *testing.T, pObj *unstructured.Unstructured) (string, bool) {
	t.Helper()
	got := testHostObject(pObj.GroupVersionKind(), "", "", nil)
	if err := e.hostClient.Get(context.Background(), client.ObjectKeyFromObject(pObj), got); err != nil {
		t.Fatalf("get host object: %v", err)
	}
	v, ok := got.GetAnnotations()[syncConflictAnnotation]
	return v, ok
}

func countEvents(r *events.FakeRecorder, reason string) int {
	n := 0
	for {
		select {
		case e := <-r.Events:
			if strings.Contains(e, reason) {
				n++
			}
		default:
			return n
		}
	}
}

func TestFromHostSyncer_Sync_RefusesToOverwriteObjectsItDidNotCreate(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ConflictWidget"}

	tests := []struct {
		name             string
		virtualAnnots    map[string]string
		hostAnnots       map[string]string
		wantOverwritten  bool
		wantHostConflict bool
		wantConflictEvts int
	}{
		{
			name:             "tenant object without provenance is left untouched",
			virtualAnnots:    nil,
			wantOverwritten:  false,
			wantHostConflict: true,
			wantConflictEvts: 1,
		},
		{
			name:             "copy synced from another host object is left untouched",
			virtualAnnots:    map[string]string{syncedFromAnnotation: "host-ns/other-widget"},
			wantOverwritten:  false,
			wantHostConflict: true,
			wantConflictEvts: 1,
		},
		{
			name:             "copy synced from this host object is updated",
			virtualAnnots:    map[string]string{syncedFromAnnotation: "host-ns/widget-a"},
			wantOverwritten:  true,
			wantHostConflict: false,
		},
		{
			name:             "cleared conflict removes the host annotation",
			virtualAnnots:    map[string]string{syncedFromAnnotation: "host-ns/widget-a"},
			hostAnnots:       map[string]string{syncConflictAnnotation: "stale"},
			wantOverwritten:  true,
			wantHostConflict: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pObj := testHostObject(gvk, "host-ns", "widget-a", tt.hostAnnots)
			_ = unstructured.SetNestedField(pObj.Object, "from-host", "spec", "source")
			vObj := testHostObject(gvk, "default", "widget-a", tt.virtualAnnots)
			_ = unstructured.SetNestedField(vObj.Object, "tenant", "spec", "source")
			env := newConflictTestEnv(t, pObj, nil, vObj)
			before := env.conflictsMet()

			result, err := env.syncer.Sync(env.syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{Virtual: vObj, Host: pObj})
			if err != nil {
				t.Fatalf("Sync() error: %v", err)
			}

			got := testHostObject(gvk, "", "", nil)
			if err := env.virtual.Get(context.Background(), client.ObjectKeyFromObject(vObj), got); err != nil {
				t.Fatalf("get virtual object: %v", err)
			}
			src, _, _ := unstructured.NestedString(got.Object, "spec", "source")
			if overwritten := src == "from-host"; overwritten != tt.wantOverwritten {
				t.Errorf("virtual spec.source = %q, overwritten=%v, want overwritten=%v", src, overwritten, tt.wantOverwritten)
			}
			if _, copied := got.GetAnnotations()[syncConflictAnnotation]; copied {
				t.Error("the host's sync-conflict annotation was copied to the virtual object")
			}

			value, annotated := env.hostAnnotation(t, pObj)
			if annotated != tt.wantHostConflict {
				t.Errorf("host sync-conflict annotation present=%v (%q), want %v", annotated, value, tt.wantHostConflict)
			}
			wantMetric := 0.0
			if tt.wantHostConflict {
				wantMetric = 1
				if !strings.Contains(value, "default/widget-a") {
					t.Errorf("sync-conflict reason %q does not name the virtual object", value)
				}
				if result.RequeueAfter != conflictRequeueInterval {
					t.Errorf("RequeueAfter = %v, want %v so the import is retried once the conflict clears", result.RequeueAfter, conflictRequeueInterval)
				}
			}
			if delta := env.conflictsMet() - before; delta != wantMetric {
				t.Errorf("sync_conflicts_total increased by %v, want %v", delta, wantMetric)
			}
			if n := countEvents(env.hostRec, logging.ReasonSyncConflict); n != tt.wantConflictEvts {
				t.Errorf("host SyncConflict events = %d, want %d", n, tt.wantConflictEvts)
			}
			if n := countEvents(env.virtualRec, logging.ReasonSyncConflict); n != tt.wantConflictEvts {
				t.Errorf("virtual SyncConflict events = %d, want %d", n, tt.wantConflictEvts)
			}
		})
	}
}

// TestFromHostSyncer_Sync_ConflictEventsOncePerConflict: retries of a conflict that is
// already recorded on the host object do not emit new events.
func TestFromHostSyncer_Sync_ConflictEventsOncePerConflict(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ConflictWidget"}
	pObj := testHostObject(gvk, "host-ns", "widget-a", nil)
	vObj := testHostObject(gvk, "default", "widget-a", nil)
	env := newConflictTestEnv(t, pObj, nil, vObj)

	if _, err := env.syncer.Sync(env.syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{Virtual: vObj, Host: pObj}); err != nil {
		t.Fatalf("first Sync() error: %v", err)
	}
	if n := countEvents(env.hostRec, logging.ReasonSyncConflict) + countEvents(env.virtualRec, logging.ReasonSyncConflict); n != 2 {
		t.Fatalf("first conflict emitted %d events, want one on each side", n)
	}

	annotated := testHostObject(gvk, "", "", nil)
	if err := env.hostClient.Get(context.Background(), client.ObjectKeyFromObject(pObj), annotated); err != nil {
		t.Fatalf("get host object: %v", err)
	}
	if _, err := env.syncer.Sync(env.syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{Virtual: vObj, Host: annotated}); err != nil {
		t.Fatalf("second Sync() error: %v", err)
	}
	if n := countEvents(env.hostRec, logging.ReasonSyncConflict) + countEvents(env.virtualRec, logging.ReasonSyncConflict); n != 0 {
		t.Errorf("retry of an already-recorded conflict emitted %d events, want 0", n)
	}
}

// TestFromHostSyncer_Sync_ConflictWithoutHostPatchPermission: when the plugin may not
// patch the host object, the conflict is still reported through events and the metric.
func TestFromHostSyncer_Sync_ConflictWithoutHostPatchPermission(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ConflictWidget"}
	pObj := testHostObject(gvk, "host-ns", "widget-a", nil)
	vObj := testHostObject(gvk, "default", "widget-a", nil)
	forbidden := &interceptor.Funcs{
		Patch: func(_ context.Context, _ client.WithWatch, obj client.Object, _ client.Patch, _ ...client.PatchOption) error {
			return errors.NewForbidden(schema.GroupResource{Group: gvk.Group, Resource: "conflictwidgets"}, obj.GetName(), nil)
		},
	}
	env := newConflictTestEnv(t, pObj, forbidden, vObj)
	before := env.conflictsMet()

	result, err := env.syncer.Sync(env.syncCtx, &synccontext.SyncEvent[*unstructured.Unstructured]{Virtual: vObj, Host: pObj})
	if err != nil {
		t.Fatalf("Sync() error: %v", err)
	}
	if result.RequeueAfter != conflictRequeueInterval {
		t.Errorf("RequeueAfter = %v, want %v", result.RequeueAfter, conflictRequeueInterval)
	}
	if delta := env.conflictsMet() - before; delta != 1 {
		t.Errorf("sync_conflicts_total increased by %v, want 1", delta)
	}
	if n := countEvents(env.hostRec, logging.ReasonSyncConflict); n != 1 {
		t.Errorf("host SyncConflict events = %d, want 1", n)
	}
	if n := countEvents(env.virtualRec, logging.ReasonSyncConflict); n != 1 {
		t.Errorf("virtual SyncConflict events = %d, want 1", n)
	}
}

// TestFromHostSyncer_SyncToVirtual_RefusesExistingObjectItDidNotCreate covers an object
// the SDK did not pair with the host object (Create returns AlreadyExists).
func TestFromHostSyncer_SyncToVirtual_RefusesExistingObjectItDidNotCreate(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ConflictWidget"}

	tests := []struct {
		name          string
		virtualAnnots map[string]string
	}{
		{name: "tenant object without provenance", virtualAnnots: nil},
		{name: "copy synced from another host object", virtualAnnots: map[string]string{syncedFromAnnotation: "host-ns/other-widget"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pObj := testHostObject(gvk, "host-ns", "widget-a", nil)
			_ = unstructured.SetNestedField(pObj.Object, "from-host", "spec", "source")
			existing := testHostObject(gvk, "default", "widget-a", tt.virtualAnnots)
			_ = unstructured.SetNestedField(existing.Object, "tenant", "spec", "source")
			env := newConflictTestEnv(t, pObj, nil, existing)

			result, err := env.syncer.SyncToVirtual(env.syncCtx, &synccontext.SyncToVirtualEvent[*unstructured.Unstructured]{Host: pObj})
			if err != nil {
				t.Fatalf("SyncToVirtual() error: %v", err)
			}
			if result.RequeueAfter != conflictRequeueInterval {
				t.Errorf("RequeueAfter = %v, want %v", result.RequeueAfter, conflictRequeueInterval)
			}
			got := testHostObject(gvk, "", "", nil)
			if err := env.virtual.Get(context.Background(), client.ObjectKeyFromObject(existing), got); err != nil {
				t.Fatalf("get virtual object: %v", err)
			}
			if src, _, _ := unstructured.NestedString(got.Object, "spec", "source"); src != "tenant" {
				t.Errorf("existing object was overwritten: spec.source = %q", src)
			}
			if _, annotated := env.hostAnnotation(t, pObj); !annotated {
				t.Error("expected the sync-conflict annotation on the host object")
			}
		})
	}
}

// TestFromHostSyncer_SyncToHost_KeepsObjectsItDidNotCreate: when the host source is
// deleted, objects at its location that the syncer did not create from it stay.
func TestFromHostSyncer_SyncToHost_KeepsObjectsItDidNotCreate(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "ConflictWidget"}

	tests := []struct {
		name          string
		virtualAnnots map[string]string
	}{
		{name: "tenant object without provenance", virtualAnnots: nil},
		{name: "copy synced from another host object", virtualAnnots: map[string]string{syncedFromAnnotation: "host-ns/other-widget"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vObj := testHostObject(gvk, "default", "widget-a", tt.virtualAnnots)
			vClient := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(vObj).Build()
			syncCtx := &synccontext.SyncContext{Context: context.Background(), VirtualClient: vClient, Log: loghelper.New("test")}

			if _, err := newTargetNameTestSyncer(gvk).SyncToHost(syncCtx, &synccontext.SyncToHostEvent[*unstructured.Unstructured]{Virtual: vObj}); err != nil {
				t.Fatalf("SyncToHost() error: %v", err)
			}
			if err := vClient.Get(context.Background(), client.ObjectKeyFromObject(vObj), testHostObject(gvk, "", "", nil)); err != nil {
				t.Fatalf("expected the object to be kept, got err=%v", err)
			}
		})
	}
}
