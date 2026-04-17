package syncers

import (
	"testing"

	"github.com/loft-sh/vcluster/pkg/util/translate"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
)

func TestToHostSyncer_VirtualToHost(t *testing.T) {
	// Set the VClusterName for translate functions
	originalDefault := translate.Default
	originalVClusterName := translate.VClusterName
	translate.VClusterName = "my-vcluster"
	translate.Default = translate.NewSingleNamespaceTranslator("vcluster-ns")
	defer func() {
		translate.Default = originalDefault
		translate.VClusterName = originalVClusterName
	}()

	syncer := &ToHostSyncer{
		name:                  "test-syncer",
		gvk:                   schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
		hostNamespace:         "vcluster-ns",
		vclusterName:          "my-vcluster",
		vclusterHostNamespace: "vcluster-ns",
		namespaced:            true,
	}

	tests := []struct {
		name              string
		vName             string
		vNamespace        string
		expectedName      string
		expectedNamespace string
	}{
		{
			name:              "simple name translation",
			vName:             "my-config",
			vNamespace:        "default",
			expectedName:      "my-config-x-default-x-my-vcluster",
			expectedNamespace: "vcluster-ns",
		},
		{
			name:              "different namespace",
			vName:             "app-config",
			vNamespace:        "production",
			expectedName:      "app-config-x-production-x-my-vcluster",
			expectedNamespace: "vcluster-ns",
		},
		{
			name:              "empty name returns empty",
			vName:             "",
			vNamespace:        "default",
			expectedName:      "",
			expectedNamespace: "",
		},
		{
			name:              "name with hyphens",
			vName:             "my-app-config",
			vNamespace:        "my-namespace",
			expectedName:      "my-app-config-x-my-namespace-x-my-vcluster",
			expectedNamespace: "vcluster-ns",
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

func TestToHostSyncer_HostToVirtual(t *testing.T) {
	syncer := &ToHostSyncer{
		name:                  "test-syncer",
		gvk:                   schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
		hostNamespace:         "vcluster-ns",
		vclusterName:          "my-vcluster",
		vclusterHostNamespace: "vcluster-ns",
		namespaced:            true,
	}

	tests := []struct {
		name              string
		annotations       map[string]string
		expectedName      string
		expectedNamespace string
	}{
		{
			name: "valid annotations",
			annotations: map[string]string{
				translate.NameAnnotation:      "my-config",
				translate.NamespaceAnnotation: "default",
			},
			expectedName:      "my-config",
			expectedNamespace: "default",
		},
		{
			name: "different namespace",
			annotations: map[string]string{
				translate.NameAnnotation:      "app-secret",
				translate.NamespaceAnnotation: "production",
			},
			expectedName:      "app-secret",
			expectedNamespace: "production",
		},
		{
			name:              "nil annotations returns empty",
			annotations:       nil,
			expectedName:      "",
			expectedNamespace: "",
		},
		{
			name:              "missing annotations returns empty",
			annotations:       map[string]string{},
			expectedName:      "",
			expectedNamespace: "",
		},
		{
			name: "missing name annotation returns empty",
			annotations: map[string]string{
				translate.NamespaceAnnotation: "default",
			},
			expectedName:      "",
			expectedNamespace: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pObj := &unstructured.Unstructured{}
			pObj.SetAnnotations(tt.annotations)

			req := types.NamespacedName{Name: "ignored", Namespace: "ignored"}
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

func TestToHostSyncer_IsManaged(t *testing.T) {
	syncer := &ToHostSyncer{
		name:                  "test-syncer",
		gvk:                   schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
		hostNamespace:         "vcluster-ns",
		vclusterName:          "my-vcluster",
		vclusterHostNamespace: "vcluster-ns",
		namespaced:            true,
	}

	tests := []struct {
		name        string
		namespace   string
		labels      map[string]string
		annotations map[string]string
		expected    bool
	}{
		{
			name:      "managed object with correct labels and annotations",
			namespace: "vcluster-ns",
			labels: map[string]string{
				translate.MarkerLabel: "my-vcluster",
			},
			annotations: map[string]string{
				translate.NameAnnotation: "my-config",
			},
			expected: true,
		},
		{
			name:      "wrong namespace",
			namespace: "other-ns",
			labels: map[string]string{
				translate.MarkerLabel: "my-vcluster",
			},
			annotations: map[string]string{
				translate.NameAnnotation: "my-config",
			},
			expected: false,
		},
		{
			name:      "wrong vcluster marker",
			namespace: "vcluster-ns",
			labels: map[string]string{
				translate.MarkerLabel: "other-vcluster",
			},
			annotations: map[string]string{
				translate.NameAnnotation: "my-config",
			},
			expected: false,
		},
		{
			name:      "missing marker label",
			namespace: "vcluster-ns",
			labels:    map[string]string{},
			annotations: map[string]string{
				translate.NameAnnotation: "my-config",
			},
			expected: false,
		},
		{
			name:      "nil labels",
			namespace: "vcluster-ns",
			labels:    nil,
			annotations: map[string]string{
				translate.NameAnnotation: "my-config",
			},
			expected: false,
		},
		{
			name:      "missing name annotation",
			namespace: "vcluster-ns",
			labels: map[string]string{
				translate.MarkerLabel: "my-vcluster",
			},
			annotations: map[string]string{},
			expected:    false,
		},
		{
			name:      "nil annotations",
			namespace: "vcluster-ns",
			labels: map[string]string{
				translate.MarkerLabel: "my-vcluster",
			},
			annotations: nil,
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pObj := &unstructured.Unstructured{}
			pObj.SetNamespace(tt.namespace)
			pObj.SetLabels(tt.labels)
			pObj.SetAnnotations(tt.annotations)

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

func TestToHostSyncer_ExcludeControlledByLabel(t *testing.T) {
	syncer := &ToHostSyncer{}

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

func TestToHostSyncer_MatchesSelector(t *testing.T) {
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
			objLabels:    map[string]string{"app": "demo"},
			objNamespace: "default",
			expected:     true,
		},
		{
			name: "label match",
			selector: &config.Selector{
				MatchLabels: map[string]string{"app": "demo"},
			},
			objLabels:    map[string]string{"app": "demo"},
			objNamespace: "default",
			expected:     true,
		},
		{
			name: "label mismatch",
			selector: &config.Selector{
				MatchLabels: map[string]string{"app": "demo"},
			},
			objLabels:    map[string]string{"app": "other"},
			objNamespace: "default",
			expected:     false,
		},
		{
			name: "namespace match",
			selector: &config.Selector{
				MatchNamespaces: []string{"system"},
			},
			objLabels:    map[string]string{"app": "demo"},
			objNamespace: "system",
			expected:     true,
		},
		{
			name: "namespace mismatch",
			selector: &config.Selector{
				MatchNamespaces: []string{"system"},
			},
			objLabels:    map[string]string{"app": "demo"},
			objNamespace: "default",
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syncer := &ToHostSyncer{
				namespaced: true,
				cfg:        testSyncerConfigToHost(config.SyncResource{APIVersion: "v1", Kind: "ConfigMap", Selector: tt.selector}),
			}

			vObj := &unstructured.Unstructured{}
			vObj.SetLabels(tt.objLabels)
			vObj.SetNamespace(tt.objNamespace)

			result, _ := syncer.matchesSelector(vObj)
			if result != tt.expected {
				t.Errorf("matchesSelector() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// testSyncerConfigToHost creates a SyncerConfig with properly initialised NamespaceMatcher for tests
func testSyncerConfigToHost(res config.SyncResource) config.SyncerConfig {
	return config.SyncerConfig{
		Resource:                res,
		MaxConcurrentReconciles: 10,
		EventFilteringEnabled:   true,
		NamespaceMatcher:        config.NewNamespaceMatcher(res.APIVersion, res.Kind, nil, res.Selector),
	}
}

func TestToHostSyncer_Name(t *testing.T) {
	syncer := &ToHostSyncer{
		name: "generic-toHost-configmap",
	}

	if syncer.Name() != "generic-toHost-configmap" {
		t.Errorf("Name() = %q, expected %q", syncer.Name(), "generic-toHost-configmap")
	}
}

func TestToHostSyncer_Resource(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"}
	syncer := &ToHostSyncer{
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

func TestToHostSyncer_GroupVersionKind(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"}
	syncer := &ToHostSyncer{
		gvk: gvk,
	}

	result := syncer.GroupVersionKind()
	if result != gvk {
		t.Errorf("GroupVersionKind() = %v, expected %v", result, gvk)
	}
}

func TestToHostSyncer_ClusterScoped(t *testing.T) {
	originalDefault := translate.Default
	originalVClusterName := translate.VClusterName
	translate.VClusterName = "my-vcluster"
	translate.Default = translate.NewSingleNamespaceTranslator("vcluster-ns")
	defer func() {
		translate.Default = originalDefault
		translate.VClusterName = originalVClusterName
	}()

	syncer := &ToHostSyncer{
		name:                  "test-syncer",
		gvk:                   schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Node"},
		hostNamespace:         "vcluster-ns",
		vclusterName:          "my-vcluster",
		vclusterHostNamespace: "vcluster-ns",
		namespaced:            false,
	}

	req := types.NamespacedName{Name: "node-a"}
	result := syncer.VirtualToHost(nil, req, nil)
	expectedName := translate.Default.HostNameCluster("node-a")
	if result.Name != expectedName {
		t.Errorf("VirtualToHost().Name = %q, expected %q", result.Name, expectedName)
	}
	if result.Namespace != "" {
		t.Errorf("VirtualToHost().Namespace = %q, expected empty", result.Namespace)
	}

	pObj := &unstructured.Unstructured{}
	pObj.SetAnnotations(map[string]string{
		translate.NameAnnotation: "node-a",
	})
	translated := syncer.HostToVirtual(nil, types.NamespacedName{Name: "ignored"}, pObj)
	if translated.Name != "node-a" || translated.Namespace != "" {
		t.Errorf("HostToVirtual() = %v, expected name only", translated)
	}

	pObj.SetLabels(map[string]string{
		translate.MarkerLabel: translate.Default.MarkerLabelCluster(),
	})
	managed, err := syncer.IsManaged(nil, pObj)
	if err != nil {
		t.Fatalf("IsManaged() error: %v", err)
	}
	if !managed {
		t.Errorf("IsManaged() = false, expected true for cluster-scoped object")
	}
}

func TestToHostSyncer_StatusEnabled(t *testing.T) {
	syncer := &ToHostSyncer{
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

func TestToHostSyncer_CustomHostNamespace(t *testing.T) {
	originalDefault := translate.Default
	originalVClusterName := translate.VClusterName
	translate.VClusterName = "my-vcluster"
	translate.Default = translate.NewSingleNamespaceTranslator("vcluster-ns")
	defer func() {
		translate.Default = originalDefault
		translate.VClusterName = originalVClusterName
	}()

	// Syncer with custom hostNamespace (shared namespace like argocd)
	syncer := &ToHostSyncer{
		name:                  "test-syncer",
		gvk:                   schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"},
		hostNamespace:         "argocd",
		vclusterName:          "my-vcluster",
		vclusterHostNamespace: "vcluster-ns",
		namespaced:            true,
	}

	// Shared namespace should use clean {name}-{tenant}-{cluster} naming.
	// vcluster-ns doesn't follow vcluster-{tenant}--{cluster} pattern, so
	// falls back to SafeConcatName(name, hostNS).
	req := types.NamespacedName{Name: "my-app", Namespace: "argocd"}
	result := syncer.VirtualToHost(nil, req, nil)

	expectedName := translate.SafeConcatName("my-app", "vcluster-ns")
	if result.Name != expectedName {
		t.Errorf("VirtualToHost().Name = %q, expected %q", result.Name, expectedName)
	}
	if result.Namespace != "argocd" {
		t.Errorf("VirtualToHost().Namespace = %q, expected %q", result.Namespace, "argocd")
	}

	// IsManaged should check marker against vclusterHostNamespace (not vclusterName)
	pObj := &unstructured.Unstructured{}
	pObj.SetNamespace("argocd")
	pObj.SetLabels(map[string]string{
		translate.MarkerLabel: "vcluster-ns",
	})
	pObj.SetAnnotations(map[string]string{
		translate.NameAnnotation:      "my-app",
		translate.NamespaceAnnotation: "argocd",
	})

	managed, err := syncer.IsManaged(nil, pObj)
	if err != nil {
		t.Fatalf("IsManaged returned error: %v", err)
	}
	if !managed {
		t.Error("expected object in shared namespace with host-ns marker to be managed")
	}

	// Object with vclusterName marker should NOT be managed in shared namespace
	wrongMarker := &unstructured.Unstructured{}
	wrongMarker.SetNamespace("argocd")
	wrongMarker.SetLabels(map[string]string{
		translate.MarkerLabel: "my-vcluster",
	})
	wrongMarker.SetAnnotations(map[string]string{
		translate.NameAnnotation: "my-app",
	})

	managed, err = syncer.IsManaged(nil, wrongMarker)
	if err != nil {
		t.Fatalf("IsManaged returned error: %v", err)
	}
	if managed {
		t.Error("expected object with vclusterName marker to NOT be managed in shared namespace")
	}

	// Object in wrong namespace should NOT be managed
	wrongNS := &unstructured.Unstructured{}
	wrongNS.SetNamespace("default")
	wrongNS.SetLabels(map[string]string{
		translate.MarkerLabel: "vcluster-ns",
	})

	managed, err = syncer.IsManaged(nil, wrongNS)
	if err != nil {
		t.Fatalf("IsManaged returned error: %v", err)
	}
	if managed {
		t.Error("expected object in wrong namespace to NOT be managed")
	}
}

func TestToHostSyncer_SharedNamespaceCollisionPrevention(t *testing.T) {
	originalDefault := translate.Default
	originalVClusterName := translate.VClusterName
	translate.VClusterName = "deploy-test"
	translate.Default = translate.NewSingleNamespaceTranslator("vcluster-acme--deploy-test")
	defer func() {
		translate.Default = originalDefault
		translate.VClusterName = originalVClusterName
	}()

	// Two syncers from different tenants, same cluster name, same shared namespace
	syncerTenantA := &ToHostSyncer{
		name:                  "test-syncer",
		gvk:                   schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"},
		hostNamespace:         "argocd",
		vclusterName:          "deploy-test",
		vclusterHostNamespace: "vcluster-acme--deploy-test",
		namespaced:            true,
	}

	syncerTenantB := &ToHostSyncer{
		name:                  "test-syncer",
		gvk:                   schema.GroupVersionKind{Group: "argoproj.io", Version: "v1alpha1", Kind: "Application"},
		hostNamespace:         "argocd",
		vclusterName:          "deploy-test",
		vclusterHostNamespace: "vcluster-bigcorp--deploy-test",
		namespaced:            true,
	}

	req := types.NamespacedName{Name: "guestbook", Namespace: "argocd"}

	resultA := syncerTenantA.VirtualToHost(nil, req, nil)
	resultB := syncerTenantB.VirtualToHost(nil, req, nil)

	if resultA.Name == resultB.Name {
		t.Errorf("name collision: tenant A and B both produced %q", resultA.Name)
	}

	expectedA := translate.SafeConcatName("guestbook", "acme", "deploy-test")
	expectedB := translate.SafeConcatName("guestbook", "bigcorp", "deploy-test")
	if resultA.Name != expectedA {
		t.Errorf("tenant A name = %q, expected %q", resultA.Name, expectedA)
	}
	if resultB.Name != expectedB {
		t.Errorf("tenant B name = %q, expected %q", resultB.Name, expectedB)
	}

	// Each syncer should only manage its own objects
	objA := &unstructured.Unstructured{}
	objA.SetNamespace("argocd")
	objA.SetLabels(map[string]string{translate.MarkerLabel: "vcluster-acme--deploy-test"})
	objA.SetAnnotations(map[string]string{translate.NameAnnotation: "guestbook"})

	managedByA, _ := syncerTenantA.IsManaged(nil, objA)
	managedByB, _ := syncerTenantB.IsManaged(nil, objA)

	if !managedByA {
		t.Error("expected tenant A syncer to manage its own object")
	}
	if managedByB {
		t.Error("expected tenant B syncer to NOT manage tenant A's object")
	}
}

func TestParseTenantCluster(t *testing.T) {
	tests := []struct {
		hostNS          string
		expectedTenant  string
		expectedCluster string
	}{
		{"vcluster-acme--deploy-test", "acme", "deploy-test"},
		{"vcluster-bigcorp--production", "bigcorp", "production"},
		{"vcluster-my-org--k", "my-org", "k"},
		{"vcluster-ns", "", ""},                      // no -- separator
		{"other-prefix--foo", "other-prefix", "foo"}, // no vcluster- prefix but has --
		{"", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.hostNS, func(t *testing.T) {
			tenant, cluster := parseTenantCluster(tt.hostNS)
			if tenant != tt.expectedTenant {
				t.Errorf("parseTenantCluster(%q) tenant = %q, expected %q", tt.hostNS, tenant, tt.expectedTenant)
			}
			if cluster != tt.expectedCluster {
				t.Errorf("parseTenantCluster(%q) cluster = %q, expected %q", tt.hostNS, cluster, tt.expectedCluster)
			}
			// parseTenantFromNamespace should match tenant
			if got := parseTenantFromNamespace(tt.hostNS); got != tt.expectedTenant {
				t.Errorf("parseTenantFromNamespace(%q) = %q, expected %q", tt.hostNS, got, tt.expectedTenant)
			}
		})
	}
}

func TestToHostSyncer_ApplySyncLabels(t *testing.T) {
	// Shared namespace syncer — should override marker AND add tenant label
	syncer := &ToHostSyncer{
		hostNamespace:         "argocd",
		vclusterHostNamespace: "vcluster-acme--deploy-test",
	}

	obj := &unstructured.Unstructured{}
	obj.SetLabels(map[string]string{
		translate.MarkerLabel: "deploy-test", // original marker from SDK
		"existing":            "label",
	})

	syncer.applySyncLabels(obj)

	labels := obj.GetLabels()
	if labels[translate.MarkerLabel] != "vcluster-acme--deploy-test" {
		t.Errorf("MarkerLabel = %q, expected %q", labels[translate.MarkerLabel], "vcluster-acme--deploy-test")
	}
	if labels["kupe.cloud/tenant"] != "acme" {
		t.Errorf("kupe.cloud/tenant = %q, expected %q", labels["kupe.cloud/tenant"], "acme")
	}
	if labels["existing"] != "label" {
		t.Error("existing label was removed")
	}

	// Non-shared namespace syncer — should NOT modify marker but SHOULD add tenant label
	nonShared := &ToHostSyncer{
		hostNamespace:         "vcluster-acme--deploy-test",
		vclusterHostNamespace: "vcluster-acme--deploy-test",
	}

	obj2 := &unstructured.Unstructured{}
	obj2.SetLabels(map[string]string{
		translate.MarkerLabel: "my-vcluster",
	})

	nonShared.applySyncLabels(obj2)

	labels2 := obj2.GetLabels()
	if labels2[translate.MarkerLabel] != "my-vcluster" {
		t.Error("non-shared syncer should not modify marker label")
	}
	if labels2["kupe.cloud/tenant"] != "acme" {
		t.Errorf("non-shared syncer should still add tenant label, got %q", labels2["kupe.cloud/tenant"])
	}
}

func TestToHostSyncer_NonSharedNamespaceUnchanged(t *testing.T) {
	originalDefault := translate.Default
	originalVClusterName := translate.VClusterName
	translate.VClusterName = "my-vcluster"
	translate.Default = translate.NewSingleNamespaceTranslator("vcluster-ns")
	defer func() {
		translate.Default = originalDefault
		translate.VClusterName = originalVClusterName
	}()

	// Syncer targeting its own host namespace (not shared)
	syncer := &ToHostSyncer{
		name:                  "test-syncer",
		gvk:                   schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
		hostNamespace:         "vcluster-ns",
		vclusterName:          "my-vcluster",
		vclusterHostNamespace: "vcluster-ns",
		namespaced:            true,
	}

	req := types.NamespacedName{Name: "my-config", Namespace: "default"}
	result := syncer.VirtualToHost(nil, req, nil)

	// Should use standard SDK naming, not shared namespace naming
	expectedName := "my-config-x-default-x-my-vcluster"
	if result.Name != expectedName {
		t.Errorf("VirtualToHost().Name = %q, expected %q", result.Name, expectedName)
	}

	// IsManaged should check vclusterName marker (not host namespace)
	pObj := &unstructured.Unstructured{}
	pObj.SetNamespace("vcluster-ns")
	pObj.SetLabels(map[string]string{
		translate.MarkerLabel: "my-vcluster",
	})
	pObj.SetAnnotations(map[string]string{
		translate.NameAnnotation: "my-config",
	})

	managed, err := syncer.IsManaged(nil, pObj)
	if err != nil {
		t.Fatalf("IsManaged error: %v", err)
	}
	if !managed {
		t.Error("expected non-shared namespace object to be managed with vclusterName marker")
	}
}
