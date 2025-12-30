package metrics

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestRecorder_RecordOperation(t *testing.T) {
	// Reset metrics for testing
	SyncOperationsTotal.Reset()

	recorder := NewRecorder(DirectionToHost, "ConfigMap")

	recorder.RecordOperationSuccess(OperationCreate)
	recorder.RecordOperationError(OperationUpdate)
	recorder.RecordOperationSkipped(OperationDelete)

	// Verify counters
	if got := testutil.ToFloat64(SyncOperationsTotal.WithLabelValues(DirectionToHost, "ConfigMap", OperationCreate, StatusSuccess)); got != 1 {
		t.Errorf("RecordOperationSuccess() = %v, want 1", got)
	}
	if got := testutil.ToFloat64(SyncOperationsTotal.WithLabelValues(DirectionToHost, "ConfigMap", OperationUpdate, StatusError)); got != 1 {
		t.Errorf("RecordOperationError() = %v, want 1", got)
	}
	if got := testutil.ToFloat64(SyncOperationsTotal.WithLabelValues(DirectionToHost, "ConfigMap", OperationDelete, StatusSkipped)); got != 1 {
		t.Errorf("RecordOperationSkipped() = %v, want 1", got)
	}
}

func TestRecorder_RecordError(t *testing.T) {
	SyncErrorsTotal.Reset()

	recorder := NewRecorder(DirectionFromHost, "Secret")

	recorder.RecordError(ErrorTypeConflict)
	recorder.RecordError(ErrorTypeNotFound)
	recorder.RecordError(ErrorTypeConflict) // Second conflict

	if got := testutil.ToFloat64(SyncErrorsTotal.WithLabelValues(DirectionFromHost, "Secret", ErrorTypeConflict)); got != 2 {
		t.Errorf("RecordError(conflict) = %v, want 2", got)
	}
	if got := testutil.ToFloat64(SyncErrorsTotal.WithLabelValues(DirectionFromHost, "Secret", ErrorTypeNotFound)); got != 1 {
		t.Errorf("RecordError(not_found) = %v, want 1", got)
	}
}

func TestRecorder_ResourcesManaged(t *testing.T) {
	ResourcesManaged.Reset()

	recorder := NewRecorder(DirectionToHost, "Gateway")

	recorder.SetResourcesManaged(5)
	if got := testutil.ToFloat64(ResourcesManaged.WithLabelValues(DirectionToHost, "Gateway")); got != 5 {
		t.Errorf("SetResourcesManaged(5) = %v, want 5", got)
	}

	recorder.IncResourcesManaged()
	if got := testutil.ToFloat64(ResourcesManaged.WithLabelValues(DirectionToHost, "Gateway")); got != 6 {
		t.Errorf("IncResourcesManaged() = %v, want 6", got)
	}

	recorder.DecResourcesManaged()
	recorder.DecResourcesManaged()
	if got := testutil.ToFloat64(ResourcesManaged.WithLabelValues(DirectionToHost, "Gateway")); got != 4 {
		t.Errorf("DecResourcesManaged() = %v, want 4", got)
	}
}

func TestRecorder_TimeOperation(t *testing.T) {
	SyncOperationDuration.Reset()

	recorder := NewRecorder(DirectionToHost, "ConfigMap")

	// Use TimeOperation
	done := recorder.TimeOperation(OperationCreate)
	time.Sleep(10 * time.Millisecond)
	done()

	// Verify histogram has observations by checking the count via testutil
	count := testutil.CollectAndCount(SyncOperationDuration)
	if count == 0 {
		t.Errorf("TimeOperation() should have recorded observations, got count = %v", count)
	}
}

func TestRecorder_RecordReconcile(t *testing.T) {
	ReconcileTotal.Reset()

	recorder := NewRecorder(DirectionFromHost, "Deployment")

	recorder.RecordReconcile()
	recorder.RecordReconcile()
	recorder.RecordReconcile()

	if got := testutil.ToFloat64(ReconcileTotal.WithLabelValues(DirectionFromHost, "Deployment")); got != 3 {
		t.Errorf("RecordReconcile() = %v, want 3", got)
	}
}

func TestRecorder_RecordNamespaceFiltered(t *testing.T) {
	NamespaceFilteredTotal.Reset()

	recorder := NewRecorder(DirectionToHost, "Secret")

	recorder.RecordNamespaceFiltered("kube-system")
	recorder.RecordNamespaceFiltered("kube-system")
	recorder.RecordNamespaceFiltered("default")

	if got := testutil.ToFloat64(NamespaceFilteredTotal.WithLabelValues(DirectionToHost, "Secret", "kube-system")); got != 2 {
		t.Errorf("RecordNamespaceFiltered(kube-system) = %v, want 2", got)
	}
	if got := testutil.ToFloat64(NamespaceFilteredTotal.WithLabelValues(DirectionToHost, "Secret", "default")); got != 1 {
		t.Errorf("RecordNamespaceFiltered(default) = %v, want 1", got)
	}
}

func TestRecorder_RecordPatchApplied(t *testing.T) {
	PatchAppliedTotal.Reset()

	recorder := NewRecorder(DirectionToHost, "HTTPRoute")

	recorder.RecordPatchApplied("rewriteName")
	recorder.RecordPatchApplied("rewriteRef")
	recorder.RecordPatchApplied("rewriteName")

	if got := testutil.ToFloat64(PatchAppliedTotal.WithLabelValues(DirectionToHost, "HTTPRoute", "rewriteName")); got != 2 {
		t.Errorf("RecordPatchApplied(rewriteName) = %v, want 2", got)
	}
}

func TestEventsEmittedCounter_WiredToLoggingPackage(t *testing.T) {
	// Verify that the EventsEmittedTotal counter is wired to the logging package
	// The actual event emission is tested in logging/events_test.go
	// This test just verifies the wiring is in place
	if EventsEmittedTotal == nil {
		t.Error("EventsEmittedTotal counter is nil")
	}

	// The init() function should have wired this up
	// We can't easily test the logging package integration here without
	// creating a circular dependency, but we verify the counter exists
}

func TestOperationTimer_ObserveWithStatus(t *testing.T) {
	SyncOperationDuration.Reset()
	SyncOperationsTotal.Reset()

	recorder := NewRecorder(DirectionToHost, "Service")

	// Test successful operation
	timer := recorder.NewOperationTimer(OperationCreate)
	time.Sleep(5 * time.Millisecond)
	timer.ObserveWithStatus(nil)

	if got := testutil.ToFloat64(SyncOperationsTotal.WithLabelValues(DirectionToHost, "Service", OperationCreate, StatusSuccess)); got != 1 {
		t.Errorf("ObserveWithStatus(nil) success count = %v, want 1", got)
	}

	// Test failed operation
	timer2 := recorder.NewOperationTimer(OperationUpdate)
	time.Sleep(5 * time.Millisecond)
	timer2.ObserveWithStatus(errors.New("test error"))

	if got := testutil.ToFloat64(SyncOperationsTotal.WithLabelValues(DirectionToHost, "Service", OperationUpdate, StatusError)); got != 1 {
		t.Errorf("ObserveWithStatus(error) error count = %v, want 1", got)
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"nil error", nil, ""},
		{"conflict error", errors.New("Operation cannot be fulfilled: conflict"), ErrorTypeConflict},
		{"already exists", errors.New("resource already exists"), ErrorTypeConflict},
		{"optimistic lock", errors.New("optimistic lock error"), ErrorTypeConflict},
		{"not found", errors.New("resource not found"), ErrorTypeNotFound},
		{"NotFound", errors.New("the server returned NotFound"), ErrorTypeNotFound},
		{"invalid", errors.New("invalid spec field"), ErrorTypeValidation},
		{"validation", errors.New("validation failed"), ErrorTypeValidation},
		{"timeout", errors.New("request timeout"), ErrorTypeTimeout},
		{"deadline exceeded", errors.New("context deadline exceeded"), ErrorTypeTimeout},
		{"context canceled", errors.New("context canceled"), ErrorTypeTimeout},
		{"unknown error", errors.New("something went wrong"), ErrorTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ClassifyError(tt.err)
			if result != tt.expected {
				t.Errorf("ClassifyError(%v) = %q, want %q", tt.err, result, tt.expected)
			}
		})
	}
}

func TestSetPluginInfo(t *testing.T) {
	PluginInfo.Reset()

	SetPluginInfo("1.0.0", "abc123", "2024-01-01")

	if got := testutil.ToFloat64(PluginInfo.WithLabelValues("1.0.0", "abc123", "2024-01-01")); got != 1 {
		t.Errorf("SetPluginInfo() gauge = %v, want 1", got)
	}
}

func TestRegisterSyncer(t *testing.T) {
	SyncerInfo.Reset()

	RegisterSyncer(DirectionToHost, "Gateway", "gateway.networking.k8s.io/v1", "sync", true)
	RegisterSyncer(DirectionFromHost, "ConfigMap", "v1", "mirror", false)

	if got := testutil.ToFloat64(SyncerInfo.WithLabelValues(DirectionToHost, "Gateway", "gateway.networking.k8s.io/v1", "sync", "true")); got != 1 {
		t.Errorf("RegisterSyncer(toHost, Gateway) = %v, want 1", got)
	}
	if got := testutil.ToFloat64(SyncerInfo.WithLabelValues(DirectionFromHost, "ConfigMap", "v1", "mirror", "false")); got != 1 {
		t.Errorf("RegisterSyncer(fromHost, ConfigMap) = %v, want 1", got)
	}
}

func TestRecordConfigReload(t *testing.T) {
	ConfigReloadsTotal.Reset()

	RecordConfigReload(true)
	RecordConfigReload(true)
	RecordConfigReload(false)

	if got := testutil.ToFloat64(ConfigReloadsTotal.WithLabelValues(StatusSuccess)); got != 2 {
		t.Errorf("RecordConfigReload(success) = %v, want 2", got)
	}
	if got := testutil.ToFloat64(ConfigReloadsTotal.WithLabelValues(StatusError)); got != 1 {
		t.Errorf("RecordConfigReload(error) = %v, want 1", got)
	}
}

// For proper histogram testing, we use prometheus testutil
func init() {
	// Ensure metrics are available for testing
	_ = GetMetricsForTesting()
}
