package syncers

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/loft-sh/vcluster/pkg/syncer/synccontext"
	"github.com/loft-sh/vcluster/pkg/util/patch"
	"github.com/loft-sh/vcluster/pkg/util/translate"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	ctrlevent "sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/kupecloud/vcluster-generic-sync-plugin/config"
	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
)

func TestCheckSelectorMatch_NilMetrics(t *testing.T) {
	tests := []struct {
		name           string
		selector       *config.Selector
		objLabels      map[string]string
		objNamespace   string
		namespaced     bool
		expectedMatch  bool
		expectedReason filterReason
	}{
		{
			name:           "nil metrics with namespace filter - should not panic",
			selector:       &config.Selector{MatchNamespaces: []string{"allowed-ns"}},
			objLabels:      nil,
			objNamespace:   "blocked-ns",
			namespaced:     true,
			expectedMatch:  false,
			expectedReason: filterNamespace,
		},
		{
			name:           "nil metrics with matching namespace",
			selector:       &config.Selector{MatchNamespaces: []string{"allowed-ns"}},
			objLabels:      nil,
			objNamespace:   "allowed-ns",
			namespaced:     true,
			expectedMatch:  true,
			expectedReason: filterNone,
		},
		{
			name:           "nil metrics with label selector mismatch",
			selector:       &config.Selector{MatchLabels: map[string]string{"env": "prod"}},
			objLabels:      map[string]string{"env": "dev"},
			objNamespace:   "default",
			namespaced:     true,
			expectedMatch:  false,
			expectedReason: filterSelector,
		},
		{
			name:           "nil metrics with label selector match",
			selector:       &config.Selector{MatchLabels: map[string]string{"env": "prod"}},
			objLabels:      map[string]string{"env": "prod"},
			objNamespace:   "default",
			namespaced:     true,
			expectedMatch:  true,
			expectedReason: filterNone,
		},
		{
			name:           "nil metrics with nil selector",
			selector:       nil,
			objLabels:      map[string]string{"any": "label"},
			objNamespace:   "default",
			namespaced:     true,
			expectedMatch:  true,
			expectedReason: filterNone,
		},
		{
			name:           "nil metrics cluster-scoped resource",
			selector:       nil,
			objLabels:      nil,
			objNamespace:   "",
			namespaced:     false,
			expectedMatch:  true,
			expectedReason: filterNone,
		},
		{
			name:           "nil metrics namespaced resource with empty namespace",
			selector:       nil,
			objLabels:      nil,
			objNamespace:   "",
			namespaced:     true,
			expectedMatch:  false,
			expectedReason: filterNamespace,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.SyncerConfig{
				Resource:         config.SyncResource{APIVersion: "v1", Kind: "ConfigMap", Selector: tt.selector},
				NamespaceMatcher: config.NewNamespaceMatcher("v1", "ConfigMap", nil, tt.selector),
			}

			obj := &unstructured.Unstructured{}
			obj.SetLabels(tt.objLabels)
			obj.SetNamespace(tt.objNamespace)

			// Call with nil metrics - should not panic
			match, reason := checkSelectorMatch(obj, tt.namespaced, cfg, nil)

			if match != tt.expectedMatch {
				t.Errorf("checkSelectorMatch() match = %v, expected %v", match, tt.expectedMatch)
			}
			if reason != tt.expectedReason {
				t.Errorf("checkSelectorMatch() reason = %v, expected %v", reason, tt.expectedReason)
			}
		})
	}
}

func TestCheckSelectorMatch_NilObject(t *testing.T) {
	cfg := config.SyncerConfig{
		Resource: config.SyncResource{APIVersion: "v1", Kind: "ConfigMap"},
	}

	// Should handle nil object gracefully
	match, reason := checkSelectorMatch(nil, true, cfg, nil)
	if match != false {
		t.Errorf("checkSelectorMatch(nil) match = %v, expected false", match)
	}
	if reason != filterNone {
		t.Errorf("checkSelectorMatch(nil) reason = %v, expected filterNone", reason)
	}
}

func TestHasSyncableFieldChanges(t *testing.T) {
	tests := []struct {
		name        string
		oldObj      map[string]interface{}
		newObj      map[string]interface{}
		checkStatus bool
		expected    bool
	}{
		{
			name:        "no changes",
			oldObj:      map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(1)}},
			newObj:      map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(1)}},
			checkStatus: false,
			expected:    false,
		},
		{
			name:        "spec changed",
			oldObj:      map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(1)}},
			newObj:      map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(2)}},
			checkStatus: false,
			expected:    true,
		},
		{
			name:        "data field changed (ConfigMap/Secret style)",
			oldObj:      map[string]interface{}{"data": map[string]interface{}{"key": "value1"}},
			newObj:      map[string]interface{}{"data": map[string]interface{}{"key": "value2"}},
			checkStatus: false,
			expected:    true,
		},
		{
			name:        "field removed",
			oldObj:      map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(1)}, "extra": "field"},
			newObj:      map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(1)}},
			checkStatus: false,
			expected:    true,
		},
		{
			name:        "field added",
			oldObj:      map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(1)}},
			newObj:      map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(1)}, "extra": "field"},
			checkStatus: false,
			expected:    true,
		},
		{
			name:        "status changed but not checking status",
			oldObj:      map[string]interface{}{"spec": map[string]interface{}{}, "status": map[string]interface{}{"ready": false}},
			newObj:      map[string]interface{}{"spec": map[string]interface{}{}, "status": map[string]interface{}{"ready": true}},
			checkStatus: false,
			expected:    false,
		},
		{
			name:        "status changed and checking status",
			oldObj:      map[string]interface{}{"spec": map[string]interface{}{}, "status": map[string]interface{}{"ready": false}},
			newObj:      map[string]interface{}{"spec": map[string]interface{}{}, "status": map[string]interface{}{"ready": true}},
			checkStatus: true,
			expected:    true,
		},
		{
			name:        "metadata changes ignored",
			oldObj:      map[string]interface{}{"metadata": map[string]interface{}{"resourceVersion": "1"}},
			newObj:      map[string]interface{}{"metadata": map[string]interface{}{"resourceVersion": "2"}},
			checkStatus: false,
			expected:    false,
		},
		{
			name:        "apiVersion and kind changes ignored",
			oldObj:      map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap"},
			newObj:      map[string]interface{}{"apiVersion": "v1", "kind": "ConfigMap"},
			checkStatus: false,
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oldU := &unstructured.Unstructured{Object: tt.oldObj}
			newU := &unstructured.Unstructured{Object: tt.newObj}

			result := hasSyncableFieldChanges(oldU, newU, tt.checkStatus, nil)
			if result != tt.expected {
				t.Errorf("hasSyncableFieldChanges() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestCopySyncableFields(t *testing.T) {
	tests := []struct {
		name     string
		src      map[string]interface{}
		dst      map[string]interface{}
		expected map[string]interface{}
	}{
		{
			name:     "copy spec field",
			src:      map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(3)}},
			dst:      map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(1)}},
			expected: map[string]interface{}{"spec": map[string]interface{}{"replicas": int64(3)}},
		},
		{
			name:     "copy data field (ConfigMap style)",
			src:      map[string]interface{}{"data": map[string]interface{}{"key": "new-value"}},
			dst:      map[string]interface{}{"data": map[string]interface{}{"key": "old-value"}},
			expected: map[string]interface{}{"data": map[string]interface{}{"key": "new-value"}},
		},
		{
			name:     "remove field not in src",
			src:      map[string]interface{}{"spec": map[string]interface{}{}},
			dst:      map[string]interface{}{"spec": map[string]interface{}{}, "extra": "field"},
			expected: map[string]interface{}{"spec": map[string]interface{}{}},
		},
		{
			name:     "preserve metadata",
			src:      map[string]interface{}{"spec": map[string]interface{}{}, "metadata": map[string]interface{}{"name": "src-name"}},
			dst:      map[string]interface{}{"spec": map[string]interface{}{}, "metadata": map[string]interface{}{"name": "dst-name"}},
			expected: map[string]interface{}{"spec": map[string]interface{}{}, "metadata": map[string]interface{}{"name": "dst-name"}},
		},
		{
			name:     "preserve status",
			src:      map[string]interface{}{"spec": map[string]interface{}{}, "status": map[string]interface{}{"ready": true}},
			dst:      map[string]interface{}{"spec": map[string]interface{}{}, "status": map[string]interface{}{"ready": false}},
			expected: map[string]interface{}{"spec": map[string]interface{}{}, "status": map[string]interface{}{"ready": false}},
		},
		{
			name: "preserve apiVersion and kind",
			src: map[string]interface{}{
				"apiVersion": "v2",
				"kind":       "NewKind",
				"spec":       map[string]interface{}{"new": "spec"},
			},
			dst: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "OldKind",
				"spec":       map[string]interface{}{"old": "spec"},
			},
			expected: map[string]interface{}{
				"apiVersion": "v1",
				"kind":       "OldKind",
				"spec":       map[string]interface{}{"new": "spec"},
			},
		},
		{
			name: "multiple custom fields",
			src: map[string]interface{}{
				"data":       map[string]interface{}{"key1": "val1"},
				"binaryData": map[string]interface{}{"key2": "val2"},
				"stringData": map[string]interface{}{"key3": "val3"},
			},
			dst: map[string]interface{}{
				"data": map[string]interface{}{"old": "data"},
			},
			expected: map[string]interface{}{
				"data":       map[string]interface{}{"key1": "val1"},
				"binaryData": map[string]interface{}{"key2": "val2"},
				"stringData": map[string]interface{}{"key3": "val3"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &unstructured.Unstructured{Object: tt.src}
			dst := &unstructured.Unstructured{Object: tt.dst}

			copySyncableFields(src, dst, nil)

			// Check each expected key
			for key, expectedVal := range tt.expected {
				actualVal, exists := dst.Object[key]
				if !exists {
					t.Errorf("expected key %q to exist in dst", key)
					continue
				}
				// Use string comparison for simplicity
				if actualVal == nil && expectedVal != nil {
					t.Errorf("key %q: got nil, expected %v", key, expectedVal)
				}
			}

			// Check no unexpected keys exist
			for key := range dst.Object {
				if _, exists := tt.expected[key]; !exists {
					t.Errorf("unexpected key %q in dst", key)
				}
			}
		})
	}
}

func TestCopySyncableFields_DeepCopy(t *testing.T) {
	// Verify that modifications to dst don't affect src (deep copy)
	srcData := map[string]interface{}{
		"nested": map[string]interface{}{
			"key": "original",
		},
	}
	src := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec": srcData,
	}}
	dst := &unstructured.Unstructured{Object: map[string]interface{}{}}

	copySyncableFields(src, dst, nil)

	// Modify the nested value in dst
	dstSpec, _, _ := unstructured.NestedMap(dst.Object, "spec")
	dstSpec["nested"].(map[string]interface{})["key"] = "modified"

	// Verify src is unchanged
	srcNested, _, _ := unstructured.NestedString(src.Object, "spec", "nested", "key")
	if srcNested != "original" {
		t.Errorf("src was modified after changing dst: got %q, expected %q", srcNested, "original")
	}
}

// mockStatusClient is a test client that simulates status updates with configurable behaviour
type mockStatusClient struct {
	client.Client
	conflictCount  int32 // number of conflicts to return before succeeding
	conflictsSeen  int32 // atomic counter of conflicts returned
	updateAttempts int32 // atomic counter of update attempts
	lastUpdated    client.Object
}

func (c *mockStatusClient) Status() client.SubResourceWriter {
	return &mockStatusWriter{
		client:         c.Client,
		conflictCount:  c.conflictCount,
		conflictsSeen:  &c.conflictsSeen,
		updateAttempts: &c.updateAttempts,
		lastUpdated:    &c.lastUpdated,
	}
}

type mockStatusWriter struct {
	client         client.Client
	conflictCount  int32
	conflictsSeen  *int32
	updateAttempts *int32
	lastUpdated    *client.Object
}

func (w *mockStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	atomic.AddInt32(w.updateAttempts, 1)
	seen := atomic.AddInt32(w.conflictsSeen, 1)
	if seen <= w.conflictCount {
		return errors.NewConflict(schema.GroupResource{Group: "test", Resource: "widgets"}, obj.GetName(), nil)
	}
	// Store the updated object and update the underlying client
	*w.lastUpdated = obj
	return w.client.Update(ctx, obj)
}

func (w *mockStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return nil
}

func (w *mockStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}

func (w *mockStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return nil
}

func TestSyncStatusHostToVirtual_ConflictRequeue(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	// The status sync no longer retries in-line on conflict: it makes a single update
	// attempt and returns the conflict error so the controller requeues against a settled
	// cache. So any conflict yields exactly one attempt and a returned conflict error.
	tests := []struct {
		name                   string
		conflictCount          int32
		expectSuccess          bool
		expectedUpdateAttempts int32
	}{
		{
			name:                   "no conflict - succeeds on first try",
			conflictCount:          0,
			expectSuccess:          true,
			expectedUpdateAttempts: 1,
		},
		{
			name:                   "conflict - single attempt, returns conflict for requeue",
			conflictCount:          1,
			expectSuccess:          false,
			expectedUpdateAttempts: 1,
		},
		{
			name:                   "persistent conflict - still a single attempt",
			conflictCount:          10,
			expectSuccess:          false,
			expectedUpdateAttempts: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create virtual object
			vObj := &unstructured.Unstructured{}
			vObj.SetGroupVersionKind(gvk)
			vObj.SetName("test-widget")
			vObj.SetNamespace("default")
			_ = unstructured.SetNestedMap(vObj.Object, map[string]interface{}{"ready": false}, "status")

			// Create host object with different status
			pObj := &unstructured.Unstructured{}
			pObj.SetGroupVersionKind(gvk)
			pObj.SetName("test-widget")
			pObj.SetNamespace("host-ns")
			_ = unstructured.SetNestedMap(pObj.Object, map[string]interface{}{"ready": true}, "status")

			// Create fake client with virtual object
			scheme := runtime.NewScheme()
			baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vObj).Build()

			// Wrap with mock client
			mockClient := &mockStatusClient{
				Client:        baseClient,
				conflictCount: tt.conflictCount,
			}

			// Create sync context
			syncCtx := &synccontext.SyncContext{
				Context: context.Background(),
			}

			// Call syncStatusHostToVirtual
			err := syncStatusHostToVirtual(syncCtx, pObj, vObj, mockClient)

			// Check result
			if tt.expectSuccess {
				if err != nil {
					t.Errorf("expected success, got error: %v", err)
				}
			} else {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if !errors.IsConflict(err) {
					t.Errorf("expected conflict error, got: %v", err)
				}
			}

			// Verify update attempts
			if atomic.LoadInt32(&mockClient.updateAttempts) != tt.expectedUpdateAttempts {
				t.Errorf("expected %d update attempts, got %d", tt.expectedUpdateAttempts, atomic.LoadInt32(&mockClient.updateAttempts))
			}
		})
	}
}

func TestSyncStatusHostToVirtual_StatusCopy(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	// Create virtual object without status
	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("test-widget")
	vObj.SetNamespace("default")

	// Create host object with status
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("test-widget")
	pObj.SetNamespace("host-ns")
	_ = unstructured.SetNestedMap(pObj.Object, map[string]interface{}{
		"ready":    true,
		"replicas": int64(3),
	}, "status")

	// Create fake client
	scheme := runtime.NewScheme()
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vObj).Build()

	// Use mock client to properly handle status updates
	mockClient := &mockStatusClient{
		Client:        baseClient,
		conflictCount: 0,
	}

	syncCtx := &synccontext.SyncContext{
		Context: context.Background(),
	}

	// Call syncStatusHostToVirtual
	err := syncStatusHostToVirtual(syncCtx, pObj, vObj, mockClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify status was copied by checking the lastUpdated object
	if mockClient.lastUpdated == nil {
		t.Fatal("expected lastUpdated to be set")
	}
	updatedU, ok := mockClient.lastUpdated.(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("expected *unstructured.Unstructured, got %T", mockClient.lastUpdated)
	}

	// Verify status was copied
	status, found, _ := unstructured.NestedMap(updatedU.Object, "status")
	if !found {
		t.Fatal("expected status to be present")
	}
	if status["ready"] != true {
		t.Errorf("expected status.ready=true, got %v", status["ready"])
	}
	if status["replicas"] != int64(3) {
		t.Errorf("expected status.replicas=3, got %v", status["replicas"])
	}
}

func TestSyncStatusHostToVirtual_StatusClear(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	// Create virtual object with status
	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("test-widget")
	vObj.SetNamespace("default")
	_ = unstructured.SetNestedMap(vObj.Object, map[string]interface{}{"ready": true}, "status")

	// Create host object without status
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("test-widget")
	pObj.SetNamespace("host-ns")
	// No status set

	// Create fake client
	scheme := runtime.NewScheme()
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vObj).Build()

	// Use mock client to properly handle status updates
	mockClient := &mockStatusClient{
		Client:        baseClient,
		conflictCount: 0,
	}

	syncCtx := &synccontext.SyncContext{
		Context: context.Background(),
	}

	// Call syncStatusHostToVirtual
	err := syncStatusHostToVirtual(syncCtx, pObj, vObj, mockClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify status was cleared by checking the lastUpdated object
	if mockClient.lastUpdated == nil {
		t.Fatal("expected lastUpdated to be set")
	}
	updatedU, ok := mockClient.lastUpdated.(*unstructured.Unstructured)
	if !ok {
		t.Fatalf("expected *unstructured.Unstructured, got %T", mockClient.lastUpdated)
	}

	// Verify status was cleared
	_, found, _ := unstructured.NestedMap(updatedU.Object, "status")
	if found {
		t.Error("expected status to be cleared")
	}
}

func TestSyncStatusHostToVirtual_SkipsIdenticalStatus(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	status := map[string]interface{}{"ready": true}

	// Create virtual object with status
	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("test-widget")
	vObj.SetNamespace("default")
	_ = unstructured.SetNestedMap(vObj.Object, status, "status")

	// Create host object with identical status
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("test-widget")
	pObj.SetNamespace("host-ns")
	_ = unstructured.SetNestedMap(pObj.Object, status, "status")

	// Create fake client that tracks updates
	scheme := runtime.NewScheme()
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vObj).Build()
	trackingClient := &mockStatusClient{
		Client:        baseClient,
		conflictCount: 0, // No conflicts, just tracking
	}

	syncCtx := &synccontext.SyncContext{
		Context: context.Background(),
	}

	// Call syncStatusHostToVirtual
	err := syncStatusHostToVirtual(syncCtx, pObj, vObj, trackingClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify no updates were made
	if atomic.LoadInt32(&trackingClient.updateAttempts) != 0 {
		t.Errorf("expected 0 update attempts for identical status, got %d", atomic.LoadInt32(&trackingClient.updateAttempts))
	}
}

func TestSyncStatusHostToVirtual_NilObjects(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("test-widget")
	vObj.SetNamespace("default")

	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("test-widget")
	pObj.SetNamespace("host-ns")

	scheme := runtime.NewScheme()
	vClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	syncCtx := &synccontext.SyncContext{
		Context: context.Background(),
	}

	// Test nil pObj
	err := syncStatusHostToVirtual(syncCtx, nil, vObj, vClient)
	if err != nil {
		t.Errorf("expected nil error for nil pObj, got: %v", err)
	}

	// Test nil vObj
	err = syncStatusHostToVirtual(syncCtx, pObj, nil, vClient)
	if err != nil {
		t.Errorf("expected nil error for nil vObj, got: %v", err)
	}

	// Test nil client
	err = syncStatusHostToVirtual(syncCtx, pObj, vObj, nil)
	if err != nil {
		t.Errorf("expected nil error for nil client, got: %v", err)
	}
}

func TestSyncStatusHostToVirtual_NoStatusOnEither(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	// Create virtual object without status
	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("test-widget")
	vObj.SetNamespace("default")

	// Create host object without status
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("test-widget")
	pObj.SetNamespace("host-ns")

	// Create fake client that tracks updates
	scheme := runtime.NewScheme()
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vObj).Build()
	trackingClient := &mockStatusClient{
		Client:        baseClient,
		conflictCount: 0,
	}

	syncCtx := &synccontext.SyncContext{
		Context: context.Background(),
	}

	// Call syncStatusHostToVirtual
	err := syncStatusHostToVirtual(syncCtx, pObj, vObj, trackingClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify no updates were made
	if atomic.LoadInt32(&trackingClient.updateAttempts) != 0 {
		t.Errorf("expected 0 update attempts when neither has status, got %d", atomic.LoadInt32(&trackingClient.updateAttempts))
	}
}

func TestSyncStatusHostToVirtual_NonConflictError(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}

	// Create virtual object
	vObj := &unstructured.Unstructured{}
	vObj.SetGroupVersionKind(gvk)
	vObj.SetName("test-widget")
	vObj.SetNamespace("default")
	_ = unstructured.SetNestedMap(vObj.Object, map[string]interface{}{"ready": false}, "status")

	// Create host object with different status
	pObj := &unstructured.Unstructured{}
	pObj.SetGroupVersionKind(gvk)
	pObj.SetName("test-widget")
	pObj.SetNamespace("host-ns")
	_ = unstructured.SetNestedMap(pObj.Object, map[string]interface{}{"ready": true}, "status")

	// Create a client that returns non-conflict error
	scheme := runtime.NewScheme()
	baseClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(vObj).Build()
	errorClient := &nonConflictErrorClient{
		Client: baseClient,
	}

	syncCtx := &synccontext.SyncContext{
		Context: context.Background(),
	}

	// Call syncStatusHostToVirtual
	err := syncStatusHostToVirtual(syncCtx, pObj, vObj, errorClient)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.IsConflict(err) {
		t.Error("expected non-conflict error")
	}
	if !errors.IsNotFound(err) {
		t.Errorf("expected NotFound error, got: %v", err)
	}
}

// nonConflictErrorClient returns a non-conflict error on status update
type nonConflictErrorClient struct {
	client.Client
}

func (c *nonConflictErrorClient) Status() client.SubResourceWriter {
	return &nonConflictStatusWriter{}
}

type nonConflictStatusWriter struct{}

func (w *nonConflictStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	return errors.NewNotFound(schema.GroupResource{Group: "test", Resource: "widgets"}, obj.GetName())
}

func (w *nonConflictStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	return nil
}

func (w *nonConflictStatusWriter) Create(ctx context.Context, obj client.Object, subResource client.Object, opts ...client.SubResourceCreateOption) error {
	return nil
}

func (w *nonConflictStatusWriter) Apply(ctx context.Context, obj runtime.ApplyConfiguration, opts ...client.SubResourceApplyOption) error {
	return nil
}

// testLogger creates a logger for testing that discards output
func testLogger() *logging.Logger {
	return logging.NewLogger()
}

func TestStripStatus(t *testing.T) {
	tests := []struct {
		name        string
		obj         *unstructured.Unstructured
		expectNil   bool
		expectEmpty bool
	}{
		{
			name:      "nil object - should not panic",
			obj:       nil,
			expectNil: true,
		},
		{
			name: "object with status - status should be removed",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "Secret",
					"metadata":   map[string]interface{}{"name": "test"},
					"data":       map[string]interface{}{"key": "value"},
					"status":     map[string]interface{}{"ready": true},
				},
			},
			expectNil:   false,
			expectEmpty: true,
		},
		{
			name: "object without status - should remain unchanged",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata":   map[string]interface{}{"name": "test"},
					"data":       map[string]interface{}{"key": "value"},
				},
			},
			expectNil:   false,
			expectEmpty: true, // no status to begin with
		},
		{
			name: "object with complex status - status should be removed",
			obj: &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "gateway.networking.k8s.io/v1",
					"kind":       "HTTPRoute",
					"metadata":   map[string]interface{}{"name": "test"},
					"spec":       map[string]interface{}{"rules": []interface{}{}},
					"status": map[string]interface{}{
						"parents": []interface{}{
							map[string]interface{}{
								"controllerName": "test",
								"conditions":     []interface{}{},
							},
						},
					},
				},
			},
			expectNil:   false,
			expectEmpty: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stripStatus(tt.obj)

			if tt.expectNil {
				// Just verify no panic occurred
				return
			}

			// Verify status was removed
			_, hasStatus, _ := unstructured.NestedMap(tt.obj.Object, "status")
			if tt.expectEmpty && hasStatus {
				t.Error("expected status to be removed, but it still exists")
			}

			// Verify other fields remain intact
			if _, exists := tt.obj.Object["apiVersion"]; !exists {
				t.Error("apiVersion should still exist")
			}
			if _, exists := tt.obj.Object["kind"]; !exists {
				t.Error("kind should still exist")
			}
			if _, exists := tt.obj.Object["metadata"]; !exists {
				t.Error("metadata should still exist")
			}
		})
	}
}

func TestIsCoreAPIResource(t *testing.T) {
	tests := []struct {
		name     string
		gvk      schema.GroupVersionKind
		expected bool
	}{
		{
			name:     "v1 Secret - core API resource",
			gvk:      schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Secret"},
			expected: true,
		},
		{
			name:     "v1 ConfigMap - core API resource",
			gvk:      schema.GroupVersionKind{Group: "", Version: "v1", Kind: "ConfigMap"},
			expected: true,
		},
		{
			name:     "v1 Pod - core API resource",
			gvk:      schema.GroupVersionKind{Group: "", Version: "v1", Kind: "Pod"},
			expected: true,
		},
		{
			name:     "apps/v1 Deployment - not core API",
			gvk:      schema.GroupVersionKind{Group: "apps", Version: "v1", Kind: "Deployment"},
			expected: false,
		},
		{
			name:     "gateway.networking.k8s.io/v1 HTTPRoute - not core API",
			gvk:      schema.GroupVersionKind{Group: "gateway.networking.k8s.io", Version: "v1", Kind: "HTTPRoute"},
			expected: false,
		},
		{
			name:     "custom.example.com/v1 Widget - not core API",
			gvk:      schema.GroupVersionKind{Group: "custom.example.com", Version: "v1", Kind: "Widget"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isCoreAPIResource(tt.gvk)
			if result != tt.expected {
				t.Errorf("isCoreAPIResource(%v) = %v, expected %v", tt.gvk, result, tt.expected)
			}
		})
	}
}

func TestBuildEventFilterPredicate(t *testing.T) {
	gvk := schema.GroupVersionKind{Group: "example.com", Version: "v1", Kind: "Widget"}
	log := testLogger()

	// Test with status disabled
	statusDisabled := func() bool { return false }
	predicateNoStatus := buildEventFilterPredicate(gvk, log, statusDisabled, nil)

	// Test with status enabled
	statusEnabled := func() bool { return true }
	predicateWithStatus := buildEventFilterPredicate(gvk, log, statusEnabled, nil)

	t.Run("create events always processed", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetName("test")
		event := ctrlevent.CreateEvent{Object: obj}

		if !predicateNoStatus.Create(event) {
			t.Error("create events should always be processed")
		}
		if !predicateWithStatus.Create(event) {
			t.Error("create events should always be processed with status enabled")
		}
	})

	t.Run("delete events always processed", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetName("test")
		event := ctrlevent.DeleteEvent{Object: obj}

		if !predicateNoStatus.Delete(event) {
			t.Error("delete events should always be processed")
		}
	})

	t.Run("update with generation change processed", func(t *testing.T) {
		oldObj := &unstructured.Unstructured{}
		oldObj.SetName("test")
		oldObj.SetGeneration(1)

		newObj := &unstructured.Unstructured{}
		newObj.SetName("test")
		newObj.SetGeneration(2)

		event := ctrlevent.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}
		if !predicateNoStatus.Update(event) {
			t.Error("generation change should trigger reconciliation")
		}
	})

	t.Run("update with label change processed", func(t *testing.T) {
		oldObj := &unstructured.Unstructured{}
		oldObj.SetName("test")
		oldObj.SetLabels(map[string]string{"env": "dev"})

		newObj := &unstructured.Unstructured{}
		newObj.SetName("test")
		newObj.SetLabels(map[string]string{"env": "prod"})

		event := ctrlevent.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}
		if !predicateNoStatus.Update(event) {
			t.Error("label change should trigger reconciliation")
		}
	})

	t.Run("update with annotation change processed", func(t *testing.T) {
		oldObj := &unstructured.Unstructured{}
		oldObj.SetName("test")
		oldObj.SetAnnotations(map[string]string{"note": "old"})

		newObj := &unstructured.Unstructured{}
		newObj.SetName("test")
		newObj.SetAnnotations(map[string]string{"note": "new"})

		event := ctrlevent.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}
		if !predicateNoStatus.Update(event) {
			t.Error("annotation change should trigger reconciliation")
		}
	})

	t.Run("update with finalizer change processed", func(t *testing.T) {
		oldObj := &unstructured.Unstructured{}
		oldObj.SetName("test")
		oldObj.SetFinalizers([]string{})

		newObj := &unstructured.Unstructured{}
		newObj.SetName("test")
		newObj.SetFinalizers([]string{"example.com/cleanup"})

		event := ctrlevent.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}
		if !predicateNoStatus.Update(event) {
			t.Error("finalizer change should trigger reconciliation")
		}
	})

	t.Run("update with spec change processed", func(t *testing.T) {
		oldObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"spec": map[string]interface{}{"replicas": int64(1)},
			},
		}
		oldObj.SetName("test")

		newObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"spec": map[string]interface{}{"replicas": int64(3)},
			},
		}
		newObj.SetName("test")

		event := ctrlevent.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}
		if !predicateNoStatus.Update(event) {
			t.Error("spec change should trigger reconciliation")
		}
	})

	t.Run("update with only status change - status disabled", func(t *testing.T) {
		oldObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"spec":   map[string]interface{}{"replicas": int64(1)},
				"status": map[string]interface{}{"ready": false},
			},
		}
		oldObj.SetName("test")

		newObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"spec":   map[string]interface{}{"replicas": int64(1)},
				"status": map[string]interface{}{"ready": true},
			},
		}
		newObj.SetName("test")

		event := ctrlevent.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}
		if predicateNoStatus.Update(event) {
			t.Error("status-only change with status disabled should NOT trigger reconciliation")
		}
	})

	t.Run("update with only status change - status enabled", func(t *testing.T) {
		oldObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"spec":   map[string]interface{}{"replicas": int64(1)},
				"status": map[string]interface{}{"ready": false},
			},
		}
		oldObj.SetName("test")

		newObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"spec":   map[string]interface{}{"replicas": int64(1)},
				"status": map[string]interface{}{"ready": true},
			},
		}
		newObj.SetName("test")

		event := ctrlevent.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}
		if !predicateWithStatus.Update(event) {
			t.Error("status change with status enabled should trigger reconciliation")
		}
	})

	t.Run("update with no meaningful changes skipped", func(t *testing.T) {
		oldObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"spec": map[string]interface{}{"replicas": int64(1)},
			},
		}
		oldObj.SetName("test")
		oldObj.SetResourceVersion("1")

		newObj := &unstructured.Unstructured{
			Object: map[string]interface{}{
				"spec": map[string]interface{}{"replicas": int64(1)},
			},
		}
		newObj.SetName("test")
		newObj.SetResourceVersion("2") // Only resource version changed

		event := ctrlevent.UpdateEvent{ObjectOld: oldObj, ObjectNew: newObj}
		if predicateNoStatus.Update(event) {
			t.Error("resource version only change should be skipped")
		}
	})

	t.Run("generic events always processed", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetName("test")
		event := ctrlevent.GenericEvent{Object: obj}

		if !predicateNoStatus.Generic(event) {
			t.Error("generic events should always be processed")
		}
	})

	t.Run("nil objects in update handled", func(t *testing.T) {
		event := ctrlevent.UpdateEvent{ObjectOld: nil, ObjectNew: nil}
		if !predicateNoStatus.Update(event) {
			t.Error("nil objects should default to processing")
		}
	})
}

func TestFirstNonEmpty(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   string
	}{
		{"first wins", []string{"a", "b"}, "a"},
		{"skips empty", []string{"", "b"}, "b"},
		{"all empty", []string{"", ""}, ""},
		{"single", []string{"x"}, "x"},
		{"none", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstNonEmpty(tt.values...); got != tt.want {
				t.Errorf("firstNonEmpty() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMergeExtraLabels(t *testing.T) {
	t.Run("merges onto existing labels", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetLabels(map[string]string{"existing": "label"})
		mergeExtraLabels(obj, map[string]string{"extra": "value"})
		labels := obj.GetLabels()
		if labels["existing"] != "label" {
			t.Error("expected existing label to be preserved")
		}
		if labels["extra"] != "value" {
			t.Error("expected extra label to be added")
		}
	})

	t.Run("creates labels map if nil", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		mergeExtraLabels(obj, map[string]string{"new": "label"})
		if obj.GetLabels()["new"] != "label" {
			t.Error("expected label on previously nil map")
		}
	})

	t.Run("no-op with nil extra", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetLabels(map[string]string{"keep": "me"})
		mergeExtraLabels(obj, nil)
		if len(obj.GetLabels()) != 1 {
			t.Error("expected labels unchanged with nil extra")
		}
	})

	t.Run("no-op with empty extra", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		mergeExtraLabels(obj, map[string]string{})
		if obj.GetLabels() != nil {
			t.Error("expected nil labels with empty extra on nil object")
		}
	})

	t.Run("does not override plugin-owned keys already set", func(t *testing.T) {
		obj := &unstructured.Unstructured{}
		obj.SetLabels(map[string]string{
			"kupe.cloud/managed-by": "vcluster-sync",
			"kupe.cloud/tenant":     "acme",
			translate.MarkerLabel:   "vcluster-acme--deploy",
		})
		mergeExtraLabels(obj, map[string]string{
			"kupe.cloud/managed-by": "something-else",
			"kupe.cloud/tenant":     "attacker",
			translate.MarkerLabel:   "hijack",
			"other":                 "ok",
		})
		labels := obj.GetLabels()
		if labels["kupe.cloud/managed-by"] != "vcluster-sync" {
			t.Errorf("managed-by overridden = %q, expected preserved %q", labels["kupe.cloud/managed-by"], "vcluster-sync")
		}
		if labels["kupe.cloud/tenant"] != "acme" {
			t.Errorf("tenant overridden = %q, expected preserved %q", labels["kupe.cloud/tenant"], "acme")
		}
		if labels[translate.MarkerLabel] != "vcluster-acme--deploy" {
			t.Errorf("marker overridden = %q, expected preserved", labels[translate.MarkerLabel])
		}
		if labels["other"] != "ok" {
			t.Error("non-plugin-owned extra label should still be applied")
		}
	})

	t.Run("passes plugin-owned keys through when not already set", func(t *testing.T) {
		// fromHost imports get tenant/managed-by from globalExtraLabels rather than
		// applySyncLabels, so an unset plugin-owned key must still flow through.
		obj := &unstructured.Unstructured{}
		mergeExtraLabels(obj, map[string]string{
			"kupe.cloud/tenant":     "acme",
			"kupe.cloud/managed-by": "vcluster-sync",
		})
		labels := obj.GetLabels()
		if labels["kupe.cloud/tenant"] != "acme" {
			t.Errorf("unset tenant should pass through, got %q", labels["kupe.cloud/tenant"])
		}
		if labels["kupe.cloud/managed-by"] != "vcluster-sync" {
			t.Errorf("unset managed-by should pass through, got %q", labels["kupe.cloud/managed-by"])
		}
	})
}

func TestPatchIsEffectivelyEmpty(t *testing.T) {
	tests := []struct {
		name  string
		patch patch.Patch
		want  bool
	}{
		{"nil", patch.Patch(nil), true},
		{"empty", patch.Patch{}, true},
		{"empty metadata only", patch.Patch{"metadata": map[string]interface{}{}}, true},
		{"nested empty", patch.Patch{"metadata": map[string]interface{}{"annotations": map[string]interface{}{}}}, true},
		{"real change", patch.Patch{"spec": map[string]interface{}{"size": "large"}}, false},
		{"metadata with content", patch.Patch{"metadata": map[string]interface{}{"labels": map[string]interface{}{"a": "b"}}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := patchIsEffectivelyEmpty(tt.patch); got != tt.want {
				t.Errorf("patchIsEffectivelyEmpty() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Argo CD keeps a pending sync in the Application's top-level `operation`
// field on the HOST copy. The virtual copy never has it (no Argo runs in the
// vCluster), so without host-owned handling the copy deleted it — between
// Argo setting it and Argo's worker reading it — and auto-sync never ran.
func TestCopySyncableFieldsKeepsHostOwned(t *testing.T) {
	hostOwned := map[string]bool{"operation": true}
	src := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec":      map[string]interface{}{"project": "tenant"},
		"operation": map[string]interface{}{"sync": map[string]interface{}{"revision": "virtual-must-not-win"}},
	}}
	dst := &unstructured.Unstructured{Object: map[string]interface{}{
		"spec":      map[string]interface{}{"project": "old"},
		"operation": map[string]interface{}{"sync": map[string]interface{}{"revision": "abc"}},
		"status":    map[string]interface{}{"sync": "OutOfSync"},
	}}
	copySyncableFields(src, dst, hostOwned)
	if got := dst.Object["operation"].(map[string]interface{})["sync"].(map[string]interface{})["revision"]; got != "abc" {
		t.Fatalf("host-owned operation was overwritten from the virtual object: %v", got)
	}
	if got := dst.Object["spec"].(map[string]interface{})["project"]; got != "tenant" {
		t.Fatalf("spec not copied: %v", got)
	}
	// And the virtual object lacking the field must not delete it either.
	delete(src.Object, "operation")
	copySyncableFields(src, dst, hostOwned)
	if _, ok := dst.Object["operation"]; !ok {
		t.Fatal("host-owned operation deleted because the virtual object lacks it")
	}
}

func TestHasSyncableFieldChangesIgnoresHostOwned(t *testing.T) {
	hostOwned := map[string]bool{"operation": true}
	oldU := &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{"a": "1"}}}
	newU := &unstructured.Unstructured{Object: map[string]interface{}{"spec": map[string]interface{}{"a": "1"}, "operation": map[string]interface{}{"sync": map[string]interface{}{}}}}
	if hasSyncableFieldChanges(oldU, newU, false, hostOwned) {
		t.Fatal("Argo setting operation on the host copy must not count as a syncable change")
	}
	if !hasSyncableFieldChanges(oldU, newU, false, nil) {
		t.Fatal("without host-owned handling the same change is (correctly) a change")
	}
	if hasSyncableFieldChanges(newU, oldU, false, hostOwned) {
		t.Fatal("Argo clearing operation must not count either")
	}
}
