package patches

import (
	"testing"

	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	"github.com/loft-sh/vcluster/pkg/util/translate"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
)

const testVClusterName = "my-vcluster"

func setTranslateDefaults(t *testing.T, hostNamespace string) {
	t.Helper()
	originalDefault := translate.Default
	originalVClusterName := translate.VClusterName
	translate.VClusterName = testVClusterName
	translate.Default = translate.NewSingleNamespaceTranslator(hostNamespace)
	t.Cleanup(func() {
		translate.Default = originalDefault
		translate.VClusterName = originalVClusterName
	})
}

func TestParsePath(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected []PathSegment
	}{
		{
			name: "simple field",
			path: "spec",
			expected: []PathSegment{
				{Field: "spec", IsArray: false, ArrayIndex: 0},
			},
		},
		{
			name: "nested fields",
			path: "spec.template.spec.containers",
			expected: []PathSegment{
				{Field: "spec", IsArray: false, ArrayIndex: 0},
				{Field: "template", IsArray: false, ArrayIndex: 0},
				{Field: "spec", IsArray: false, ArrayIndex: 0},
				{Field: "containers", IsArray: false, ArrayIndex: 0},
			},
		},
		{
			name: "array wildcard",
			path: "spec.rules[*]",
			expected: []PathSegment{
				{Field: "spec", IsArray: false, ArrayIndex: 0},
				{Field: "rules", IsArray: true, ArrayIndex: -1},
			},
		},
		{
			name: "array with index",
			path: "spec.rules[0]",
			expected: []PathSegment{
				{Field: "spec", IsArray: false, ArrayIndex: 0},
				{Field: "rules", IsArray: true, ArrayIndex: 0},
			},
		},
		{
			name: "array wildcard with nested field",
			path: "spec.rules[*].name",
			expected: []PathSegment{
				{Field: "spec", IsArray: false, ArrayIndex: 0},
				{Field: "rules", IsArray: true, ArrayIndex: -1},
				{Field: "name", IsArray: false, ArrayIndex: 0},
			},
		},
		{
			name: "multiple arrays",
			path: "spec.rules[*].backendRefs[*].name",
			expected: []PathSegment{
				{Field: "spec", IsArray: false, ArrayIndex: 0},
				{Field: "rules", IsArray: true, ArrayIndex: -1},
				{Field: "backendRefs", IsArray: true, ArrayIndex: -1},
				{Field: "name", IsArray: false, ArrayIndex: 0},
			},
		},
		{
			name: "deep nesting with arrays",
			path: "spec.a[*].b[*].c[*].d",
			expected: []PathSegment{
				{Field: "spec", IsArray: false, ArrayIndex: 0},
				{Field: "a", IsArray: true, ArrayIndex: -1},
				{Field: "b", IsArray: true, ArrayIndex: -1},
				{Field: "c", IsArray: true, ArrayIndex: -1},
				{Field: "d", IsArray: false, ArrayIndex: 0},
			},
		},
		{
			name: "mixed index types",
			path: "spec.rules[0].backendRefs[*].name",
			expected: []PathSegment{
				{Field: "spec", IsArray: false, ArrayIndex: 0},
				{Field: "rules", IsArray: true, ArrayIndex: 0},
				{Field: "backendRefs", IsArray: true, ArrayIndex: -1},
				{Field: "name", IsArray: false, ArrayIndex: 0},
			},
		},
		{
			name: "gateway tls certificate refs",
			path: "spec.listeners[*].tls.certificateRefs[*].name",
			expected: []PathSegment{
				{Field: "spec", IsArray: false, ArrayIndex: 0},
				{Field: "listeners", IsArray: true, ArrayIndex: -1},
				{Field: "tls", IsArray: false, ArrayIndex: 0},
				{Field: "certificateRefs", IsArray: true, ArrayIndex: -1},
				{Field: "name", IsArray: false, ArrayIndex: 0},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parsePath(tt.path)
			if len(result) != len(tt.expected) {
				t.Errorf("parsePath(%q) returned %d segments, expected %d", tt.path, len(result), len(tt.expected))
				return
			}
			for i, seg := range result {
				if seg.Field != tt.expected[i].Field {
					t.Errorf("segment[%d].Field = %q, expected %q", i, seg.Field, tt.expected[i].Field)
				}
				if seg.IsArray != tt.expected[i].IsArray {
					t.Errorf("segment[%d].IsArray = %v, expected %v", i, seg.IsArray, tt.expected[i].IsArray)
				}
				if seg.IsArray && seg.ArrayIndex != tt.expected[i].ArrayIndex {
					t.Errorf("segment[%d].ArrayIndex = %d, expected %d", i, seg.ArrayIndex, tt.expected[i].ArrayIndex)
				}
			}
		})
	}
}

func TestReverseTranslateName(t *testing.T) {
	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	tests := []struct {
		name     string
		hostName string
		expected string
	}{
		{
			name:     "simple name",
			hostName: "my-secret-x-default-x-my-vcluster",
			expected: "my-secret",
		},
		{
			name:     "name with hyphen",
			hostName: "my-app-secret-x-default-x-my-vcluster",
			expected: "my-app-secret",
		},
		{
			name:     "name containing -x-",
			hostName: "my-x-app-x-default-x-my-vcluster",
			expected: "my-x-app",
		},
		{
			name:     "namespace containing -x-",
			hostName: "my-secret-x-prod-x-ns-x-my-vcluster",
			expected: "my-secret-x-prod", // Known limitation: can't distinguish
		},
		{
			name:     "not a translated name",
			hostName: "regular-name",
			expected: "regular-name",
		},
		{
			name:     "different vcluster suffix",
			hostName: "my-secret-x-default-x-other-vcluster",
			expected: "my-secret-x-default-x-other-vcluster",
		},
		{
			name:     "empty string",
			hostName: "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := patcher.reverseTranslateName(tt.hostName)
			if result != tt.expected {
				t.Errorf("reverseTranslateName(%q) = %q, expected %q", tt.hostName, result, tt.expected)
			}
		})
	}
}

func TestReverseTranslateNameAndNamespace(t *testing.T) {
	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	tests := []struct {
		name              string
		hostName          string
		expectedName      string
		expectedNamespace string
	}{
		{
			name:              "simple name and namespace",
			hostName:          "my-secret-x-default-x-my-vcluster",
			expectedName:      "my-secret",
			expectedNamespace: "default",
		},
		{
			name:              "name with hyphens",
			hostName:          "my-app-secret-x-production-x-my-vcluster",
			expectedName:      "my-app-secret",
			expectedNamespace: "production",
		},
		{
			name:              "name containing -x-",
			hostName:          "my-x-app-x-default-x-my-vcluster",
			expectedName:      "my-x-app",
			expectedNamespace: "default",
		},
		{
			name:              "not a translated name",
			hostName:          "regular-name",
			expectedName:      "",
			expectedNamespace: "",
		},
		{
			name:              "different vcluster",
			hostName:          "secret-x-ns-x-other-vc",
			expectedName:      "",
			expectedNamespace: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, namespace := patcher.reverseTranslateNameAndNamespace(tt.hostName)
			if name != tt.expectedName {
				t.Errorf("name = %q, expected %q", name, tt.expectedName)
			}
			if namespace != tt.expectedNamespace {
				t.Errorf("namespace = %q, expected %q", namespace, tt.expectedNamespace)
			}
		})
	}
}

func TestRewriteLabelSelectorToVirtual_DropsHostOnlyLabels(t *testing.T) {
	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	src := map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				translate.MarkerLabel: "my-vcluster",
				"app":                 "demo",
			},
			"matchExpressions": []interface{}{
				map[string]interface{}{
					"key":      translate.MarkerLabel,
					"operator": "In",
					"values":   []interface{}{"my-vcluster"},
				},
			},
		},
	}
	dst := map[string]interface{}{
		"selector": map[string]interface{}{},
	}

	if err := patcher.rewriteLabelSelectorToVirtualAtPath(src, dst, "selector"); err != nil {
		t.Fatalf("rewriteLabelSelectorToVirtualAtPath error: %v", err)
	}

	selector, ok := dst["selector"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected selector to be map")
	}

	matchLabels, ok := selector["matchLabels"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected matchLabels to be map")
	}
	if _, exists := matchLabels[translate.MarkerLabel]; exists {
		t.Fatalf("expected host-only label to be dropped")
	}
	if matchLabels["app"] != "demo" {
		t.Fatalf("expected app label to remain")
	}

	matchExpressions, ok := selector["matchExpressions"].([]interface{})
	if !ok {
		t.Fatalf("expected matchExpressions to be slice")
	}
	if len(matchExpressions) != 0 {
		t.Fatalf("expected host-only matchExpression to be dropped")
	}
}

func TestRewriteLabelSelector_TranslatesMatchExpressions(t *testing.T) {
	originalDefault := translate.Default
	originalVClusterName := translate.VClusterName
	translate.VClusterName = "my-vcluster"
	translate.Default = translate.NewSingleNamespaceTranslator("vcluster-ns")
	defer func() {
		translate.Default = originalDefault
		translate.VClusterName = originalVClusterName
	}()

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	src := map[string]interface{}{
		"selector": map[string]interface{}{
			"matchExpressions": []interface{}{
				map[string]interface{}{
					"key":      translate.NamespaceLabel,
					"operator": "In",
					"values":   []interface{}{"default"},
				},
			},
		},
	}
	dst := map[string]interface{}{
		"selector": map[string]interface{}{},
	}

	if err := patcher.rewriteLabelSelectorToHostAtPath(&synccontext.SyncContext{}, src, dst, "selector", nil); err != nil {
		t.Fatalf("rewriteLabelSelectorToHostAtPath error: %v", err)
	}

	selector, ok := dst["selector"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected selector to be map")
	}

	matchExpressions, ok := selector["matchExpressions"].([]interface{})
	if !ok || len(matchExpressions) != 1 {
		t.Fatalf("expected matchExpressions to be translated")
	}

	expr, ok := matchExpressions[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected matchExpression to be map")
	}

	expectedKey := translate.HostLabel(translate.NamespaceLabel)
	if expr["key"] != expectedKey {
		t.Fatalf("expected key %q, got %q", expectedKey, expr["key"])
	}
}

func TestRewriteLabelSelector_TranslatesMatchLabelsOnly(t *testing.T) {
	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	src := map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"app": "demo",
			},
		},
	}
	dst := map[string]interface{}{
		"selector": map[string]interface{}{},
	}

	if err := patcher.rewriteLabelSelectorToHostAtPath(&synccontext.SyncContext{}, src, dst, "selector", nil); err != nil {
		t.Fatalf("rewriteLabelSelectorToHostAtPath error: %v", err)
	}

	selector := dst["selector"].(map[string]interface{})
	matchLabels := selector["matchLabels"].(map[string]interface{})
	if _, exists := matchLabels[translate.MarkerLabel]; exists {
		t.Fatalf("expected marker label not to be added")
	}
	expectedKey := translate.HostLabel("app")
	if matchLabels[expectedKey] != "demo" {
		t.Fatalf("expected %q to be translated, got %v", expectedKey, matchLabels)
	}
}

func TestRewriteLabelSelector_IncludesOwnerLabels(t *testing.T) {
	setTranslateDefaults(t, "vcluster-ns")

	patcher := &Patcher{
		vclusterName:               "my-vcluster",
		hostNs:                     "vcluster-ns",
		includeSelectorOwnerLabels: true,
	}

	srcObj := &unstructured.Unstructured{}
	srcObj.SetNamespace("default")

	src := map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"app": "demo",
			},
		},
	}
	dst := map[string]interface{}{
		"selector": map[string]interface{}{},
	}

	if err := patcher.rewriteLabelSelectorToHostAtPath(&synccontext.SyncContext{}, src, dst, "selector", srcObj); err != nil {
		t.Fatalf("rewriteLabelSelectorToHostAtPath error: %v", err)
	}

	selector := dst["selector"].(map[string]interface{})
	matchLabels := selector["matchLabels"].(map[string]interface{})
	if matchLabels[translate.MarkerLabel] != "my-vcluster" {
		t.Fatalf("expected marker label %q, got %v", "my-vcluster", matchLabels[translate.MarkerLabel])
	}
	if matchLabels[translate.NamespaceLabel] != "default" {
		t.Fatalf("expected namespace label %q, got %v", "default", matchLabels[translate.NamespaceLabel])
	}
}

func TestRewriteLabelSelector_IncludesClusterMarker(t *testing.T) {
	setTranslateDefaults(t, "vcluster-ns")

	patcher := &Patcher{
		vclusterName:               "my-vcluster",
		hostNs:                     "vcluster-ns",
		includeSelectorOwnerLabels: true,
	}

	srcObj := &unstructured.Unstructured{}
	srcObj.SetNamespace("")

	src := map[string]interface{}{
		"selector": map[string]interface{}{
			"matchLabels": map[string]interface{}{
				"app": "demo",
			},
		},
	}
	dst := map[string]interface{}{
		"selector": map[string]interface{}{},
	}

	if err := patcher.rewriteLabelSelectorToHostAtPath(&synccontext.SyncContext{}, src, dst, "selector", srcObj); err != nil {
		t.Fatalf("rewriteLabelSelectorToHostAtPath error: %v", err)
	}

	selector := dst["selector"].(map[string]interface{})
	matchLabels := selector["matchLabels"].(map[string]interface{})
	expectedMarker := translate.Default.MarkerLabelCluster()
	if matchLabels[translate.MarkerLabel] != expectedMarker {
		t.Fatalf("expected marker label %q, got %v", expectedMarker, matchLabels[translate.MarkerLabel])
	}
	if _, exists := matchLabels[translate.NamespaceLabel]; exists {
		t.Fatalf("did not expect namespace label on cluster-scoped selector")
	}
}

func TestOriginalRefsAnnotation_NameOverride(t *testing.T) {
	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	hostObj := &unstructured.Unstructured{}
	patcher.recordOriginalRef(hostObj, "opaque-host-name", "original-name", "orig-ns")

	src := map[string]interface{}{
		"name": "opaque-host-name",
	}
	dst := map[string]interface{}{}

	if err := patcher.rewriteNameToVirtualAtPath(src, dst, "name", hostObj); err != nil {
		t.Fatalf("rewriteNameToVirtualAtPath error: %v", err)
	}

	if dst["name"] != "original-name" {
		t.Fatalf("expected original name, got %v", dst["name"])
	}
}

func TestOriginalRefsAnnotation_RefOverride(t *testing.T) {
	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	hostObj := &unstructured.Unstructured{}
	patcher.recordOriginalRef(hostObj, "opaque-host-name", "original-name", "orig-ns")

	src := map[string]interface{}{
		"ref": map[string]interface{}{
			"name":      "opaque-host-name",
			"namespace": "vcluster-ns",
		},
	}
	dst := map[string]interface{}{
		"ref": map[string]interface{}{},
	}

	if err := patcher.rewriteRefToVirtualAtPath(src, dst, "ref", hostObj); err != nil {
		t.Fatalf("rewriteRefToVirtualAtPath error: %v", err)
	}

	ref := dst["ref"].(map[string]interface{})
	if ref["name"] != "original-name" || ref["namespace"] != "orig-ns" {
		t.Fatalf("expected original ref, got %#v", ref)
	}
}

func TestRewriteNamespaceScalarArray(t *testing.T) {
	setTranslateDefaults(t, "vcluster-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	vObj := &unstructured.Unstructured{}
	vObj.Object = map[string]interface{}{
		"spec": map[string]interface{}{
			"namespaces": []interface{}{"default", "other"},
		},
	}
	vObj.SetNamespace("default")
	pObj := vObj.DeepCopy()

	patch := config.Patch{Path: "spec.namespaces[*]", Type: config.PatchRewriteNamespace}
	compiled := compiledPatch{patch: patch, segments: parsePath(patch.Path)}
	if err := patcher.applyPatchToHost(&synccontext.SyncContext{}, vObj, pObj, compiled); err != nil {
		t.Fatalf("applyPatchToHost error: %v", err)
	}

	translated := pObj.Object["spec"].(map[string]interface{})["namespaces"].([]interface{})
	if translated[0] != "vcluster-ns" || translated[1] != "vcluster-ns" {
		t.Fatalf("expected host namespace, got %#v", translated)
	}

	vDest := pObj.DeepCopy()
	if err := patcher.applyPatchToVirtual(&synccontext.SyncContext{}, pObj, vDest, compiled); err != nil {
		t.Fatalf("applyPatchToVirtual error: %v", err)
	}
	roundTrip := vDest.Object["spec"].(map[string]interface{})["namespaces"].([]interface{})
	if roundTrip[0] != "default" || roundTrip[1] != "default" {
		t.Fatalf("expected virtual namespace, got %#v", roundTrip)
	}
}

func TestRewriteNameScalarArray(t *testing.T) {
	setTranslateDefaults(t, "vcluster-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	vObj := &unstructured.Unstructured{}
	vObj.Object = map[string]interface{}{
		"spec": map[string]interface{}{
			"secretNames": []interface{}{"alpha", "beta"},
		},
	}
	vObj.SetNamespace("default")
	pObj := vObj.DeepCopy()

	patch := config.Patch{Path: "spec.secretNames[*]", Type: config.PatchRewriteName}
	compiled := compiledPatch{patch: patch, segments: parsePath(patch.Path)}
	if err := patcher.applyPatchToHost(&synccontext.SyncContext{}, vObj, pObj, compiled); err != nil {
		t.Fatalf("applyPatchToHost error: %v", err)
	}

	translated := pObj.Object["spec"].(map[string]interface{})["secretNames"].([]interface{})
	if translated[0] == "alpha" || translated[1] == "beta" {
		t.Fatalf("expected names to be translated")
	}

	vDest := pObj.DeepCopy()
	if err := patcher.applyPatchToVirtual(&synccontext.SyncContext{}, pObj, vDest, compiled); err != nil {
		t.Fatalf("applyPatchToVirtual error: %v", err)
	}
	roundTrip := vDest.Object["spec"].(map[string]interface{})["secretNames"].([]interface{})
	if roundTrip[0] != "alpha" || roundTrip[1] != "beta" {
		t.Fatalf("expected round-trip names, got %#v", roundTrip)
	}
}

func TestRewriteNameScalarArray_SpecificIndex(t *testing.T) {
	setTranslateDefaults(t, "vcluster-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	vObj := &unstructured.Unstructured{}
	vObj.Object = map[string]interface{}{
		"spec": map[string]interface{}{
			"secretNames": []interface{}{"alpha", "beta", "gamma"},
		},
	}
	vObj.SetNamespace("default")
	pObj := vObj.DeepCopy()

	patch := config.Patch{Path: "spec.secretNames[1]", Type: config.PatchRewriteName}
	compiled := compiledPatch{patch: patch, segments: parsePath(patch.Path)}
	if err := patcher.applyPatchToHost(&synccontext.SyncContext{}, vObj, pObj, compiled); err != nil {
		t.Fatalf("applyPatchToHost error: %v", err)
	}

	translated := pObj.Object["spec"].(map[string]interface{})["secretNames"].([]interface{})
	if translated[0] != "alpha" {
		t.Fatalf("expected index 0 unchanged, got %#v", translated[0])
	}
	if translated[1] == "beta" {
		t.Fatalf("expected index 1 to be translated")
	}
	if translated[2] != "gamma" {
		t.Fatalf("expected index 2 unchanged, got %#v", translated[2])
	}
}

func TestRewriteNameToHostAtPath(t *testing.T) {
	setTranslateDefaults(t, "vcluster-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	srcObj := &unstructured.Unstructured{}
	srcObj.SetNamespace("default")

	tests := []struct {
		name     string
		src      map[string]interface{}
		field    string
		expected string
	}{
		{
			name:     "simple name translation",
			src:      map[string]interface{}{"name": "my-secret"},
			field:    "name",
			expected: "my-secret-x-default-x-my-vcluster",
		},
		{
			name:     "name with sibling namespace",
			src:      map[string]interface{}{"name": "my-secret", "namespace": "production"},
			field:    "name",
			expected: "my-secret-x-production-x-my-vcluster",
		},
		{
			name:     "empty name",
			src:      map[string]interface{}{"name": ""},
			field:    "name",
			expected: "",
		},
		{
			name:     "missing field",
			src:      map[string]interface{}{},
			field:    "name",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := make(map[string]interface{})
			for k, v := range tt.src {
				dst[k] = v
			}

			err := patcher.rewriteNameToHostAtPath(&synccontext.SyncContext{}, tt.src, dst, tt.field, srcObj, nil)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			result, _ := dst[tt.field].(string)
			if result != tt.expected {
				t.Errorf("dst[%q] = %q, expected %q", tt.field, result, tt.expected)
			}
		})
	}
}

func TestTraverseAndApply_SingleArray(t *testing.T) {
	setTranslateDefaults(t, "vcluster-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	srcObj := &unstructured.Unstructured{}
	srcObj.SetNamespace("default")

	// Simulate HTTPRoute with rules[*].backendRefs[*].name
	src := map[string]interface{}{
		"spec": map[string]interface{}{
			"rules": []interface{}{
				map[string]interface{}{
					"backendRefs": []interface{}{
						map[string]interface{}{"name": "service1", "namespace": "default"},
						map[string]interface{}{"name": "service2", "namespace": "default"},
					},
				},
				map[string]interface{}{
					"backendRefs": []interface{}{
						map[string]interface{}{"name": "service3", "namespace": "other"},
					},
				},
			},
		},
	}

	// Deep copy for dst
	dst := map[string]interface{}{
		"spec": map[string]interface{}{
			"rules": []interface{}{
				map[string]interface{}{
					"backendRefs": []interface{}{
						map[string]interface{}{"name": "service1", "namespace": "default"},
						map[string]interface{}{"name": "service2", "namespace": "default"},
					},
				},
				map[string]interface{}{
					"backendRefs": []interface{}{
						map[string]interface{}{"name": "service3", "namespace": "other"},
					},
				},
			},
		},
	}

	patchPath := "spec.rules[*].backendRefs[*].name"

	segments := parsePath(patchPath)
	err := patcher.traverseAndApply(&synccontext.SyncContext{}, src, dst, segments, func(ctx *synccontext.SyncContext, srcMap, dstMap map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error {
		return patcher.rewriteNameToHostAtPath(ctx, srcMap, dstMap, field, srcObj, nil)
	}, nil, srcObj, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify translations
	rules := dst["spec"].(map[string]interface{})["rules"].([]interface{})

	// First rule, first backend
	rule0 := rules[0].(map[string]interface{})
	backends0 := rule0["backendRefs"].([]interface{})
	backend00 := backends0[0].(map[string]interface{})
	if backend00["name"] != "service1-x-default-x-my-vcluster" {
		t.Errorf("backend[0][0].name = %v, expected service1-x-default-x-my-vcluster", backend00["name"])
	}

	// First rule, second backend
	backend01 := backends0[1].(map[string]interface{})
	if backend01["name"] != "service2-x-default-x-my-vcluster" {
		t.Errorf("backend[0][1].name = %v, expected service2-x-default-x-my-vcluster", backend01["name"])
	}

	// Second rule, first backend
	rule1 := rules[1].(map[string]interface{})
	backends1 := rule1["backendRefs"].([]interface{})
	backend10 := backends1[0].(map[string]interface{})
	if backend10["name"] != "service3-x-other-x-my-vcluster" {
		t.Errorf("backend[1][0].name = %v, expected service3-x-other-x-my-vcluster", backend10["name"])
	}
}

func TestTraverseAndApply_EmptyArray(t *testing.T) {
	setTranslateDefaults(t, "vcluster-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	srcObj := &unstructured.Unstructured{}
	srcObj.SetNamespace("default")

	src := map[string]interface{}{
		"spec": map[string]interface{}{
			"rules": []interface{}{}, // Empty array
		},
	}

	dst := map[string]interface{}{
		"spec": map[string]interface{}{
			"rules": []interface{}{},
		},
	}

	segments := parsePath("spec.rules[*].name")
	err := patcher.traverseAndApply(&synccontext.SyncContext{}, src, dst, segments, func(ctx *synccontext.SyncContext, srcMap, dstMap map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error {
		return patcher.rewriteNameToHostAtPath(ctx, srcMap, dstMap, field, srcObj, nil)
	}, nil, srcObj, nil)

	if err != nil {
		t.Fatalf("unexpected error with empty array: %v", err)
	}
}

func TestTraverseAndApply_MissingPath(t *testing.T) {
	setTranslateDefaults(t, "vcluster-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	srcObj := &unstructured.Unstructured{}
	srcObj.SetNamespace("default")

	src := map[string]interface{}{
		"spec": map[string]interface{}{
			// No "rules" field
		},
	}

	dst := map[string]interface{}{
		"spec": map[string]interface{}{},
	}

	segments := parsePath("spec.rules[*].name")
	err := patcher.traverseAndApply(&synccontext.SyncContext{}, src, dst, segments, func(ctx *synccontext.SyncContext, srcMap, dstMap map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error {
		return patcher.rewriteNameToHostAtPath(ctx, srcMap, dstMap, field, srcObj, nil)
	}, nil, srcObj, nil)

	// Should not error on missing path
	if err != nil {
		t.Fatalf("unexpected error with missing path: %v", err)
	}
}

func TestTraverseAndApply_SpecificIndex(t *testing.T) {
	setTranslateDefaults(t, "vcluster-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	srcObj := &unstructured.Unstructured{}
	srcObj.SetNamespace("default")

	src := map[string]interface{}{
		"spec": map[string]interface{}{
			"rules": []interface{}{
				map[string]interface{}{"name": "rule0"},
				map[string]interface{}{"name": "rule1"},
				map[string]interface{}{"name": "rule2"},
			},
		},
	}

	dst := map[string]interface{}{
		"spec": map[string]interface{}{
			"rules": []interface{}{
				map[string]interface{}{"name": "rule0"},
				map[string]interface{}{"name": "rule1"},
				map[string]interface{}{"name": "rule2"},
			},
		},
	}

	// Only translate rules[1].name
	segments := parsePath("spec.rules[1].name")
	err := patcher.traverseAndApply(&synccontext.SyncContext{}, src, dst, segments, func(ctx *synccontext.SyncContext, srcMap, dstMap map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error {
		return patcher.rewriteNameToHostAtPath(ctx, srcMap, dstMap, field, srcObj, nil)
	}, nil, srcObj, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rules := dst["spec"].(map[string]interface{})["rules"].([]interface{})

	// rule[0] should be unchanged
	rule0 := rules[0].(map[string]interface{})
	if rule0["name"] != "rule0" {
		t.Errorf("rules[0].name should be unchanged, got %v", rule0["name"])
	}

	// rule[1] should be translated
	rule1 := rules[1].(map[string]interface{})
	if rule1["name"] != "rule1-x-default-x-my-vcluster" {
		t.Errorf("rules[1].name = %v, expected rule1-x-default-x-my-vcluster", rule1["name"])
	}

	// rule[2] should be unchanged
	rule2 := rules[2].(map[string]interface{})
	if rule2["name"] != "rule2" {
		t.Errorf("rules[2].name should be unchanged, got %v", rule2["name"])
	}
}

func TestCopyValueAtPath(t *testing.T) {
	patcher := &Patcher{}

	tests := []struct {
		name     string
		src      map[string]interface{}
		field    string
		expected interface{}
	}{
		{
			name:     "copy string value",
			src:      map[string]interface{}{"value": "test"},
			field:    "value",
			expected: "test",
		},
		{
			name:     "copy integer value",
			src:      map[string]interface{}{"port": 8080},
			field:    "port",
			expected: 8080,
		},
		{
			name:     "copy map value",
			src:      map[string]interface{}{"config": map[string]interface{}{"key": "val"}},
			field:    "config",
			expected: map[string]interface{}{"key": "val"},
		},
		{
			name:     "missing field",
			src:      map[string]interface{}{},
			field:    "missing",
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := make(map[string]interface{})
			err := patcher.copyValueAtPath(tt.src, dst, tt.field)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			result := dst[tt.field]
			if tt.expected == nil {
				if result != nil {
					t.Errorf("expected nil, got %v", result)
				}
				return
			}

			// For maps, just check they're not nil
			if _, ok := tt.expected.(map[string]interface{}); ok {
				if result == nil {
					t.Errorf("expected map, got nil")
				}
				return
			}

			if result != tt.expected {
				t.Errorf("got %v, expected %v", result, tt.expected)
			}
		})
	}
}

// Test rewriteNameToVirtualAtPath - reverse translation of names
func TestRewriteNameToVirtualAtPath(t *testing.T) {
	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-ns",
	}

	tests := []struct {
		name     string
		src      map[string]interface{}
		field    string
		expected string
	}{
		{
			name:     "simple translated name",
			src:      map[string]interface{}{"name": "my-secret-x-default-x-my-vcluster"},
			field:    "name",
			expected: "my-secret",
		},
		{
			name:     "name with hyphens",
			src:      map[string]interface{}{"name": "my-app-secret-x-production-x-my-vcluster"},
			field:    "name",
			expected: "my-app-secret",
		},
		{
			name:     "name containing -x-",
			src:      map[string]interface{}{"name": "my-x-app-x-default-x-my-vcluster"},
			field:    "name",
			expected: "my-x-app",
		},
		{
			name:     "not a translated name",
			src:      map[string]interface{}{"name": "regular-name"},
			field:    "name",
			expected: "regular-name",
		},
		{
			name:     "empty name",
			src:      map[string]interface{}{"name": ""},
			field:    "name",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := make(map[string]interface{})
			for k, v := range tt.src {
				dst[k] = v
			}

			err := patcher.rewriteNameToVirtualAtPath(tt.src, dst, tt.field, nil)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			result, _ := dst[tt.field].(string)
			if result != tt.expected {
				t.Errorf("dst[%q] = %q, expected %q", tt.field, result, tt.expected)
			}
		})
	}
}

// Test rewriteNamespaceToHostAtPath
func TestRewriteNamespaceToHostAtPath(t *testing.T) {
	setTranslateDefaults(t, "vcluster-host-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-host-ns",
	}

	tests := []struct {
		name     string
		src      map[string]interface{}
		field    string
		expected string
	}{
		{
			name:     "translate namespace to host",
			src:      map[string]interface{}{"namespace": "default"},
			field:    "namespace",
			expected: "vcluster-host-ns",
		},
		{
			name:     "any namespace becomes host namespace",
			src:      map[string]interface{}{"namespace": "production"},
			field:    "namespace",
			expected: "vcluster-host-ns",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := make(map[string]interface{})
			for k, v := range tt.src {
				dst[k] = v
			}

			err := patcher.rewriteNamespaceToHostAtPath(&synccontext.SyncContext{}, tt.src, dst, tt.field)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			result, _ := dst[tt.field].(string)
			if result != tt.expected {
				t.Errorf("dst[%q] = %q, expected %q", tt.field, result, tt.expected)
			}
		})
	}
}

// Test rewriteNamespaceToVirtualAtPath
func TestRewriteNamespaceToVirtualAtPath(t *testing.T) {
	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-host-ns",
	}

	tests := []struct {
		name     string
		src      map[string]interface{}
		vObjNs   string
		field    string
		expected string
	}{
		{
			name:     "translate to virtual namespace",
			src:      map[string]interface{}{"namespace": "vcluster-host-ns"},
			vObjNs:   "default",
			field:    "namespace",
			expected: "default",
		},
		{
			name:     "use virtual object namespace",
			src:      map[string]interface{}{"namespace": "vcluster-host-ns"},
			vObjNs:   "production",
			field:    "namespace",
			expected: "production",
		},
		{
			name:     "empty virtual namespace defaults to default",
			src:      map[string]interface{}{"namespace": "vcluster-host-ns"},
			vObjNs:   "",
			field:    "namespace",
			expected: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vObj := &unstructured.Unstructured{}
			vObj.SetNamespace(tt.vObjNs)

			dst := make(map[string]interface{})
			for k, v := range tt.src {
				dst[k] = v
			}

			err := patcher.rewriteNamespaceToVirtualAtPath(tt.src, dst, tt.field, vObj)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			result, _ := dst[tt.field].(string)
			if result != tt.expected {
				t.Errorf("dst[%q] = %q, expected %q", tt.field, result, tt.expected)
			}
		})
	}
}

// Test rewriteRefToHostAtPath - full reference translation
func TestRewriteRefToHostAtPath(t *testing.T) {
	setTranslateDefaults(t, "vcluster-host-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-host-ns",
	}

	srcObj := &unstructured.Unstructured{}
	srcObj.SetNamespace("default")

	tests := []struct {
		name              string
		src               map[string]interface{}
		field             string
		expectedName      string
		expectedNamespace string
	}{
		{
			name: "translate full reference",
			src: map[string]interface{}{
				"secretRef": map[string]interface{}{
					"name":      "my-secret",
					"namespace": "default",
				},
			},
			field:             "secretRef",
			expectedName:      "my-secret-x-default-x-my-vcluster",
			expectedNamespace: "vcluster-host-ns",
		},
		{
			name: "reference without namespace uses object namespace",
			src: map[string]interface{}{
				"secretRef": map[string]interface{}{
					"name": "my-secret",
				},
			},
			field:             "secretRef",
			expectedName:      "my-secret-x-default-x-my-vcluster",
			expectedNamespace: "vcluster-host-ns",
		},
		{
			name: "reference with different namespace",
			src: map[string]interface{}{
				"secretRef": map[string]interface{}{
					"name":      "my-secret",
					"namespace": "other-ns",
				},
			},
			field:             "secretRef",
			expectedName:      "my-secret-x-other-ns-x-my-vcluster",
			expectedNamespace: "vcluster-host-ns",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := make(map[string]interface{})
			// Deep copy the ref
			if ref, ok := tt.src[tt.field].(map[string]interface{}); ok {
				dstRef := make(map[string]interface{})
				for k, v := range ref {
					dstRef[k] = v
				}
				dst[tt.field] = dstRef
			}

			err := patcher.rewriteRefToHostAtPath(&synccontext.SyncContext{}, tt.src, dst, tt.field, srcObj, nil)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			ref := dst[tt.field].(map[string]interface{})
			if ref["name"] != tt.expectedName {
				t.Errorf("ref.name = %q, expected %q", ref["name"], tt.expectedName)
			}
			if ref["namespace"] != tt.expectedNamespace {
				t.Errorf("ref.namespace = %q, expected %q", ref["namespace"], tt.expectedNamespace)
			}
		})
	}
}

// Test rewriteRefToVirtualAtPath - reverse reference translation
func TestRewriteRefToVirtualAtPath(t *testing.T) {
	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-host-ns",
	}

	tests := []struct {
		name              string
		src               map[string]interface{}
		field             string
		expectedName      string
		expectedNamespace string
	}{
		{
			name: "reverse translate full reference",
			src: map[string]interface{}{
				"secretRef": map[string]interface{}{
					"name":      "my-secret-x-default-x-my-vcluster",
					"namespace": "vcluster-host-ns",
				},
			},
			field:             "secretRef",
			expectedName:      "my-secret",
			expectedNamespace: "default",
		},
		{
			name: "reverse translate with different namespace",
			src: map[string]interface{}{
				"secretRef": map[string]interface{}{
					"name":      "my-secret-x-production-x-my-vcluster",
					"namespace": "vcluster-host-ns",
				},
			},
			field:             "secretRef",
			expectedName:      "my-secret",
			expectedNamespace: "production",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := make(map[string]interface{})
			// Deep copy the ref
			if ref, ok := tt.src[tt.field].(map[string]interface{}); ok {
				dstRef := make(map[string]interface{})
				for k, v := range ref {
					dstRef[k] = v
				}
				dst[tt.field] = dstRef
			}

			err := patcher.rewriteRefToVirtualAtPath(tt.src, dst, tt.field, nil)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			ref := dst[tt.field].(map[string]interface{})
			if ref["name"] != tt.expectedName {
				t.Errorf("ref.name = %q, expected %q", ref["name"], tt.expectedName)
			}
			if ref["namespace"] != tt.expectedNamespace {
				t.Errorf("ref.namespace = %q, expected %q", ref["namespace"], tt.expectedNamespace)
			}
		})
	}
}

func TestRewriteRefArrayElementRoundTrip(t *testing.T) {
	setTranslateDefaults(t, "vcluster-host-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-host-ns",
	}

	vObj := &unstructured.Unstructured{}
	vObj.Object = map[string]interface{}{
		"spec": map[string]interface{}{
			"backendRefs": []interface{}{
				map[string]interface{}{"name": "api", "namespace": "default"},
				map[string]interface{}{"name": "web", "namespace": "other"},
			},
		},
	}
	vObj.SetNamespace("default")
	pObj := vObj.DeepCopy()

	patch := config.Patch{Path: "spec.backendRefs[*]", Type: config.PatchRewriteRef}
	compiled := compiledPatch{patch: patch, segments: parsePath(patch.Path)}

	if err := patcher.applyPatchToHost(&synccontext.SyncContext{}, vObj, pObj, compiled); err != nil {
		t.Fatalf("applyPatchToHost error: %v", err)
	}

	translated := pObj.Object["spec"].(map[string]interface{})["backendRefs"].([]interface{})
	ref0 := translated[0].(map[string]interface{})
	if ref0["name"] != "api-x-default-x-my-vcluster" || ref0["namespace"] != "vcluster-host-ns" {
		t.Fatalf("unexpected ref0 translation: %#v", ref0)
	}
	ref1 := translated[1].(map[string]interface{})
	if ref1["name"] != "web-x-other-x-my-vcluster" || ref1["namespace"] != "vcluster-host-ns" {
		t.Fatalf("unexpected ref1 translation: %#v", ref1)
	}

	vDest := pObj.DeepCopy()
	if err := patcher.applyPatchToVirtual(&synccontext.SyncContext{}, pObj, vDest, compiled); err != nil {
		t.Fatalf("applyPatchToVirtual error: %v", err)
	}

	roundTrip := vDest.Object["spec"].(map[string]interface{})["backendRefs"].([]interface{})
	rt0 := roundTrip[0].(map[string]interface{})
	if rt0["name"] != "api" || rt0["namespace"] != "default" {
		t.Fatalf("unexpected ref0 round-trip: %#v", rt0)
	}
	rt1 := roundTrip[1].(map[string]interface{})
	if rt1["name"] != "web" || rt1["namespace"] != "other" {
		t.Fatalf("unexpected ref1 round-trip: %#v", rt1)
	}
}

// TestApplyToHostPrunesStaleRefs verifies the original-refs annotation is rebuilt from
// scratch on each ApplyToHost pass, so mappings for refs that churn out (e.g. blue/green
// backendRefs) are pruned rather than accumulating indefinitely.
func TestApplyToHostPrunesStaleRefs(t *testing.T) {
	setTranslateDefaults(t, "vcluster-host-ns")

	patcher := NewPatcher(
		[]config.Patch{{Path: "spec.backendRefs[*]", Type: config.PatchRewriteRef}},
		"my-vcluster", "vcluster-host-ns", false,
	)

	// Pass 1: two backendRefs -> two recorded mappings.
	vObj := &unstructured.Unstructured{}
	vObj.Object = map[string]interface{}{
		"spec": map[string]interface{}{
			"backendRefs": []interface{}{
				map[string]interface{}{"name": "api", "namespace": "default"},
				map[string]interface{}{"name": "web", "namespace": "other"},
			},
		},
	}
	vObj.SetNamespace("default")
	pObj := vObj.DeepCopy()

	if err := patcher.ApplyToHost(&synccontext.SyncContext{}, vObj, pObj); err != nil {
		t.Fatalf("ApplyToHost (pass 1) error: %v", err)
	}
	refs := getRefMap(pObj)
	if len(refs) != 2 {
		t.Fatalf("expected 2 recorded refs after pass 1, got %d (%#v)", len(refs), refs)
	}

	// Pass 2: the "web" backendRef churns out; the host object still carries the pass-1
	// annotation (as it would when re-read from the cache on update).
	vObj2 := &unstructured.Unstructured{}
	vObj2.Object = map[string]interface{}{
		"spec": map[string]interface{}{
			"backendRefs": []interface{}{
				map[string]interface{}{"name": "api", "namespace": "default"},
			},
		},
	}
	vObj2.SetNamespace("default")
	pObj2 := vObj2.DeepCopy()
	pObj2.SetAnnotations(pObj.GetAnnotations())

	if err := patcher.ApplyToHost(&synccontext.SyncContext{}, vObj2, pObj2); err != nil {
		t.Fatalf("ApplyToHost (pass 2) error: %v", err)
	}
	refs = getRefMap(pObj2)
	if len(refs) != 1 {
		t.Fatalf("expected stale ref pruned to 1 entry after pass 2, got %d (%#v)", len(refs), refs)
	}
	if _, ok := refs["api-x-default-x-my-vcluster"]; !ok {
		t.Fatalf("expected surviving ref for api, got %#v", refs)
	}
	if _, ok := refs["web-x-other-x-my-vcluster"]; ok {
		t.Fatalf("expected stale web ref to be pruned, got %#v", refs)
	}
}

// Test rewriteHostRefToHostAtPath - host-native reference (only namespace changes)
func TestRewriteHostRefToHostAtPath(t *testing.T) {
	setTranslateDefaults(t, "vcluster-host-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-host-ns",
	}

	tests := []struct {
		name              string
		src               map[string]interface{}
		field             string
		expectedName      string
		expectedNamespace string
	}{
		{
			name: "host ref keeps name, translates namespace",
			src: map[string]interface{}{
				"gatewayRef": map[string]interface{}{
					"name":      "shared-gateway",
					"namespace": "gateway-ns",
				},
			},
			field:             "gatewayRef",
			expectedName:      "shared-gateway",
			expectedNamespace: "vcluster-host-ns",
		},
		{
			name: "host ref without namespace",
			src: map[string]interface{}{
				"gatewayRef": map[string]interface{}{
					"name": "shared-gateway",
				},
			},
			field:             "gatewayRef",
			expectedName:      "shared-gateway",
			expectedNamespace: "", // No namespace to translate
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dst := make(map[string]interface{})
			// Deep copy the ref
			if ref, ok := tt.src[tt.field].(map[string]interface{}); ok {
				dstRef := make(map[string]interface{})
				for k, v := range ref {
					dstRef[k] = v
				}
				dst[tt.field] = dstRef
			}

			err := patcher.rewriteHostRefToHostAtPath(&synccontext.SyncContext{}, tt.src, dst, tt.field)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			ref := dst[tt.field].(map[string]interface{})
			if ref["name"] != tt.expectedName {
				t.Errorf("ref.name = %q, expected %q", ref["name"], tt.expectedName)
			}
			if tt.expectedNamespace != "" {
				if ref["namespace"] != tt.expectedNamespace {
					t.Errorf("ref.namespace = %q, expected %q", ref["namespace"], tt.expectedNamespace)
				}
			}
		})
	}
}

func TestRewriteHostRefArrayElementRoundTrip(t *testing.T) {
	setTranslateDefaults(t, "vcluster-host-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-host-ns",
	}

	vObj := &unstructured.Unstructured{}
	vObj.Object = map[string]interface{}{
		"spec": map[string]interface{}{
			"parentRefs": []interface{}{
				map[string]interface{}{"name": "gateway-a", "namespace": "shared"},
				map[string]interface{}{"name": "gateway-b", "namespace": "external"},
			},
		},
	}
	vObj.SetNamespace("default")
	pObj := vObj.DeepCopy()

	patch := config.Patch{Path: "spec.parentRefs[*]", Type: config.PatchRewriteHostRef}
	compiled := compiledPatch{patch: patch, segments: parsePath(patch.Path)}

	if err := patcher.applyPatchToHost(&synccontext.SyncContext{}, vObj, pObj, compiled); err != nil {
		t.Fatalf("applyPatchToHost error: %v", err)
	}

	translated := pObj.Object["spec"].(map[string]interface{})["parentRefs"].([]interface{})
	ref0 := translated[0].(map[string]interface{})
	if ref0["name"] != "gateway-a" || ref0["namespace"] != "vcluster-host-ns" {
		t.Fatalf("unexpected ref0 translation: %#v", ref0)
	}
	ref1 := translated[1].(map[string]interface{})
	if ref1["name"] != "gateway-b" || ref1["namespace"] != "vcluster-host-ns" {
		t.Fatalf("unexpected ref1 translation: %#v", ref1)
	}

	vDest := pObj.DeepCopy()
	if err := patcher.applyPatchToVirtual(&synccontext.SyncContext{}, pObj, vDest, compiled); err != nil {
		t.Fatalf("applyPatchToVirtual error: %v", err)
	}

	roundTrip := vDest.Object["spec"].(map[string]interface{})["parentRefs"].([]interface{})
	rt0 := roundTrip[0].(map[string]interface{})
	if rt0["name"] != "gateway-a" || rt0["namespace"] != "default" {
		t.Fatalf("unexpected ref0 round-trip: %#v", rt0)
	}
	rt1 := roundTrip[1].(map[string]interface{})
	if rt1["name"] != "gateway-b" || rt1["namespace"] != "default" {
		t.Fatalf("unexpected ref1 round-trip: %#v", rt1)
	}
}

// Test rewriteHostRefToVirtualAtPath - reverse host-native reference
func TestRewriteHostRefToVirtualAtPath(t *testing.T) {
	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-host-ns",
	}

	tests := []struct {
		name              string
		src               map[string]interface{}
		vObjNs            string
		field             string
		expectedName      string
		expectedNamespace string
	}{
		{
			name: "host ref keeps name, sets virtual namespace",
			src: map[string]interface{}{
				"gatewayRef": map[string]interface{}{
					"name":      "shared-gateway",
					"namespace": "vcluster-host-ns",
				},
			},
			vObjNs:            "default",
			field:             "gatewayRef",
			expectedName:      "shared-gateway",
			expectedNamespace: "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vObj := &unstructured.Unstructured{}
			vObj.SetNamespace(tt.vObjNs)

			dst := make(map[string]interface{})
			// Deep copy the ref
			if ref, ok := tt.src[tt.field].(map[string]interface{}); ok {
				dstRef := make(map[string]interface{})
				for k, v := range ref {
					dstRef[k] = v
				}
				dst[tt.field] = dstRef
			}

			err := patcher.rewriteHostRefToVirtualAtPath(tt.src, dst, tt.field, vObj)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			ref := dst[tt.field].(map[string]interface{})
			if ref["name"] != tt.expectedName {
				t.Errorf("ref.name = %q, expected %q", ref["name"], tt.expectedName)
			}
			if ref["namespace"] != tt.expectedNamespace {
				t.Errorf("ref.namespace = %q, expected %q", ref["namespace"], tt.expectedNamespace)
			}
		})
	}
}

// Test Gateway TLS certificate refs - real-world complex array path
func TestGatewayTLSCertificateRefs(t *testing.T) {
	setTranslateDefaults(t, "vcluster-host-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-host-ns",
	}

	srcObj := &unstructured.Unstructured{}
	srcObj.SetNamespace("default")

	// Simulate Gateway with listeners[*].tls.certificateRefs[*]
	src := map[string]interface{}{
		"spec": map[string]interface{}{
			"listeners": []interface{}{
				map[string]interface{}{
					"name": "https",
					"tls": map[string]interface{}{
						"certificateRefs": []interface{}{
							map[string]interface{}{"name": "tls-secret", "namespace": "default"},
						},
					},
				},
				map[string]interface{}{
					"name": "https-alt",
					"tls": map[string]interface{}{
						"certificateRefs": []interface{}{
							map[string]interface{}{"name": "alt-tls-secret", "namespace": "certs"},
						},
					},
				},
			},
		},
	}

	// Deep copy for dst
	dst := map[string]interface{}{
		"spec": map[string]interface{}{
			"listeners": []interface{}{
				map[string]interface{}{
					"name": "https",
					"tls": map[string]interface{}{
						"certificateRefs": []interface{}{
							map[string]interface{}{"name": "tls-secret", "namespace": "default"},
						},
					},
				},
				map[string]interface{}{
					"name": "https-alt",
					"tls": map[string]interface{}{
						"certificateRefs": []interface{}{
							map[string]interface{}{"name": "alt-tls-secret", "namespace": "certs"},
						},
					},
				},
			},
		},
	}

	// Apply name translation
	segments := parsePath("spec.listeners[*].tls.certificateRefs[*].name")
	err := patcher.traverseAndApply(&synccontext.SyncContext{}, src, dst, segments, func(ctx *synccontext.SyncContext, srcMap, dstMap map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error {
		return patcher.rewriteNameToHostAtPath(ctx, srcMap, dstMap, field, srcObj, nil)
	}, nil, srcObj, nil)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify first listener's certificate ref
	listeners := dst["spec"].(map[string]interface{})["listeners"].([]interface{})
	listener0 := listeners[0].(map[string]interface{})
	tls0 := listener0["tls"].(map[string]interface{})
	certRefs0 := tls0["certificateRefs"].([]interface{})
	cert0 := certRefs0[0].(map[string]interface{})

	expectedName0 := "tls-secret-x-default-x-my-vcluster"
	if cert0["name"] != expectedName0 {
		t.Errorf("listener[0].tls.certificateRefs[0].name = %v, expected %v", cert0["name"], expectedName0)
	}

	// Verify second listener's certificate ref
	listener1 := listeners[1].(map[string]interface{})
	tls1 := listener1["tls"].(map[string]interface{})
	certRefs1 := tls1["certificateRefs"].([]interface{})
	cert1 := certRefs1[0].(map[string]interface{})

	expectedName1 := "alt-tls-secret-x-certs-x-my-vcluster"
	if cert1["name"] != expectedName1 {
		t.Errorf("listener[1].tls.certificateRefs[0].name = %v, expected %v", cert1["name"], expectedName1)
	}
}

// Test HTTPRoute with multiple nested arrays - end-to-end
func TestHTTPRouteEndToEnd(t *testing.T) {
	setTranslateDefaults(t, "vcluster-host-ns")

	patcher := &Patcher{
		vclusterName: "my-vcluster",
		hostNs:       "vcluster-host-ns",
	}

	srcObj := &unstructured.Unstructured{}
	srcObj.SetNamespace("default")

	// Simulate HTTPRoute
	src := map[string]interface{}{
		"spec": map[string]interface{}{
			"rules": []interface{}{
				map[string]interface{}{
					"matches": []interface{}{
						map[string]interface{}{"path": map[string]interface{}{"value": "/api"}},
					},
					"backendRefs": []interface{}{
						map[string]interface{}{"name": "api-service", "namespace": "default", "port": 8080},
						map[string]interface{}{"name": "api-service-v2", "namespace": "default", "port": 8080},
					},
				},
				map[string]interface{}{
					"matches": []interface{}{
						map[string]interface{}{"path": map[string]interface{}{"value": "/web"}},
					},
					"backendRefs": []interface{}{
						map[string]interface{}{"name": "web-service", "namespace": "frontend", "port": 80},
					},
				},
			},
		},
	}

	// Deep copy for dst
	dst := map[string]interface{}{
		"spec": map[string]interface{}{
			"rules": []interface{}{
				map[string]interface{}{
					"matches": []interface{}{
						map[string]interface{}{"path": map[string]interface{}{"value": "/api"}},
					},
					"backendRefs": []interface{}{
						map[string]interface{}{"name": "api-service", "namespace": "default", "port": 8080},
						map[string]interface{}{"name": "api-service-v2", "namespace": "default", "port": 8080},
					},
				},
				map[string]interface{}{
					"matches": []interface{}{
						map[string]interface{}{"path": map[string]interface{}{"value": "/web"}},
					},
					"backendRefs": []interface{}{
						map[string]interface{}{"name": "web-service", "namespace": "frontend", "port": 80},
					},
				},
			},
		},
	}

	// Apply name translation for backendRefs[*].name
	nameSegments := parsePath("spec.rules[*].backendRefs[*].name")
	err := patcher.traverseAndApply(&synccontext.SyncContext{}, src, dst, nameSegments, func(ctx *synccontext.SyncContext, srcMap, dstMap map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error {
		return patcher.rewriteNameToHostAtPath(ctx, srcMap, dstMap, field, srcObj, nil)
	}, nil, srcObj, nil)
	if err != nil {
		t.Fatalf("name translation error: %v", err)
	}

	// Apply namespace translation for backendRefs[*].namespace
	nsSegments := parsePath("spec.rules[*].backendRefs[*].namespace")
	err = patcher.traverseAndApply(&synccontext.SyncContext{}, src, dst, nsSegments, func(ctx *synccontext.SyncContext, srcMap, dstMap map[string]interface{}, field string, srcObj, dstObj *unstructured.Unstructured) error {
		return patcher.rewriteNamespaceToHostAtPath(ctx, srcMap, dstMap, field)
	}, nil, srcObj, nil)
	if err != nil {
		t.Fatalf("namespace translation error: %v", err)
	}

	// Verify translations
	rules := dst["spec"].(map[string]interface{})["rules"].([]interface{})

	// Rule 0, Backend 0
	rule0 := rules[0].(map[string]interface{})
	backends0 := rule0["backendRefs"].([]interface{})
	backend00 := backends0[0].(map[string]interface{})
	if backend00["name"] != "api-service-x-default-x-my-vcluster" {
		t.Errorf("rule[0].backendRefs[0].name = %v, expected api-service-x-default-x-my-vcluster", backend00["name"])
	}
	if backend00["namespace"] != "vcluster-host-ns" {
		t.Errorf("rule[0].backendRefs[0].namespace = %v, expected vcluster-host-ns", backend00["namespace"])
	}

	// Rule 0, Backend 1
	backend01 := backends0[1].(map[string]interface{})
	if backend01["name"] != "api-service-v2-x-default-x-my-vcluster" {
		t.Errorf("rule[0].backendRefs[1].name = %v, expected api-service-v2-x-default-x-my-vcluster", backend01["name"])
	}

	// Rule 1, Backend 0
	rule1 := rules[1].(map[string]interface{})
	backends1 := rule1["backendRefs"].([]interface{})
	backend10 := backends1[0].(map[string]interface{})
	if backend10["name"] != "web-service-x-frontend-x-my-vcluster" {
		t.Errorf("rule[1].backendRefs[0].name = %v, expected web-service-x-frontend-x-my-vcluster", backend10["name"])
	}
	if backend10["namespace"] != "vcluster-host-ns" {
		t.Errorf("rule[1].backendRefs[0].namespace = %v, expected vcluster-host-ns", backend10["namespace"])
	}

	// Port should be unchanged
	if backend00["port"] != 8080 {
		t.Errorf("port should be unchanged, got %v", backend00["port"])
	}
}
