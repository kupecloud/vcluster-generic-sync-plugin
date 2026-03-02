package metrics_test

import (
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	metricspkg "github.com/kupecloud/vcluster-generic-sync-plugin/metrics"
)

func TestRecorder_RecordOperation(t *testing.T) {
	// Reset metrics for testing
	metricspkg.SyncOperationsTotal.Reset()

	recorder := metricspkg.NewRecorder(metricspkg.DirectionToHost, "ConfigMap")

	recorder.RecordOperationSuccess(metricspkg.OperationCreate)
	recorder.RecordOperationError(metricspkg.OperationUpdate)
	recorder.RecordOperationSkipped(metricspkg.OperationDelete)

	// Verify counters
	if got := testutil.ToFloat64(metricspkg.SyncOperationsTotal.WithLabelValues(metricspkg.DirectionToHost, "ConfigMap", metricspkg.OperationCreate, metricspkg.StatusSuccess)); got != 1 {
		t.Errorf("RecordOperationSuccess() = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metricspkg.SyncOperationsTotal.WithLabelValues(metricspkg.DirectionToHost, "ConfigMap", metricspkg.OperationUpdate, metricspkg.StatusError)); got != 1 {
		t.Errorf("RecordOperationError() = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metricspkg.SyncOperationsTotal.WithLabelValues(metricspkg.DirectionToHost, "ConfigMap", metricspkg.OperationDelete, metricspkg.StatusSkipped)); got != 1 {
		t.Errorf("RecordOperationSkipped() = %v, want 1", got)
	}
}

func TestRecorder_RecordError(t *testing.T) {
	metricspkg.SyncErrorsTotal.Reset()

	recorder := metricspkg.NewRecorder(metricspkg.DirectionFromHost, "Secret")

	recorder.RecordError(metricspkg.ErrorTypeConflict)
	recorder.RecordError(metricspkg.ErrorTypeNotFound)
	recorder.RecordError(metricspkg.ErrorTypeConflict) // Second conflict

	if got := testutil.ToFloat64(metricspkg.SyncErrorsTotal.WithLabelValues(metricspkg.DirectionFromHost, "Secret", metricspkg.ErrorTypeConflict)); got != 2 {
		t.Errorf("RecordError(conflict) = %v, want 2", got)
	}
	if got := testutil.ToFloat64(metricspkg.SyncErrorsTotal.WithLabelValues(metricspkg.DirectionFromHost, "Secret", metricspkg.ErrorTypeNotFound)); got != 1 {
		t.Errorf("RecordError(not_found) = %v, want 1", got)
	}
}

func TestRecorder_ResourcesManaged(t *testing.T) {
	metricspkg.ResourcesManaged.Reset()

	recorder := metricspkg.NewRecorder(metricspkg.DirectionToHost, "Gateway")

	recorder.SetResourcesManaged(5)
	if got := testutil.ToFloat64(metricspkg.ResourcesManaged.WithLabelValues(metricspkg.DirectionToHost, "Gateway")); got != 5 {
		t.Errorf("SetResourcesManaged(5) = %v, want 5", got)
	}

	recorder.IncResourcesManaged()
	if got := testutil.ToFloat64(metricspkg.ResourcesManaged.WithLabelValues(metricspkg.DirectionToHost, "Gateway")); got != 6 {
		t.Errorf("IncResourcesManaged() = %v, want 6", got)
	}

	recorder.DecResourcesManaged()
	recorder.DecResourcesManaged()
	if got := testutil.ToFloat64(metricspkg.ResourcesManaged.WithLabelValues(metricspkg.DirectionToHost, "Gateway")); got != 4 {
		t.Errorf("DecResourcesManaged() = %v, want 4", got)
	}
}

func TestRecorder_TimeOperation(t *testing.T) {
	metricspkg.SyncOperationDuration.Reset()

	recorder := metricspkg.NewRecorder(metricspkg.DirectionToHost, "ConfigMap")

	// Use TimeOperation
	done := recorder.TimeOperation(metricspkg.OperationCreate)
	time.Sleep(10 * time.Millisecond)
	done()

	// Verify histogram has observations by checking the count via testutil
	count := testutil.CollectAndCount(metricspkg.SyncOperationDuration)
	if count == 0 {
		t.Errorf("TimeOperation() should have recorded observations, got count = %v", count)
	}
}

func TestRecorder_RecordReconcile(t *testing.T) {
	metricspkg.ReconcileTotal.Reset()

	recorder := metricspkg.NewRecorder(metricspkg.DirectionFromHost, "Deployment")

	recorder.RecordReconcile()
	recorder.RecordReconcile()
	recorder.RecordReconcile()

	if got := testutil.ToFloat64(metricspkg.ReconcileTotal.WithLabelValues(metricspkg.DirectionFromHost, "Deployment")); got != 3 {
		t.Errorf("RecordReconcile() = %v, want 3", got)
	}
}

func TestRecorder_RecordNamespaceFiltered(t *testing.T) {
	metricspkg.NamespaceFilteredTotal.Reset()

	recorder := metricspkg.NewRecorder(metricspkg.DirectionToHost, "Secret")

	recorder.RecordNamespaceFiltered("kube-system")
	recorder.RecordNamespaceFiltered("kube-system")
	recorder.RecordNamespaceFiltered("default")

	if got := testutil.ToFloat64(metricspkg.NamespaceFilteredTotal.WithLabelValues(metricspkg.DirectionToHost, "Secret", "kube-system")); got != 2 {
		t.Errorf("RecordNamespaceFiltered(kube-system) = %v, want 2", got)
	}
	if got := testutil.ToFloat64(metricspkg.NamespaceFilteredTotal.WithLabelValues(metricspkg.DirectionToHost, "Secret", "default")); got != 1 {
		t.Errorf("RecordNamespaceFiltered(default) = %v, want 1", got)
	}
}

func TestRecorder_RecordPatchApplied(t *testing.T) {
	metricspkg.PatchAppliedTotal.Reset()

	recorder := metricspkg.NewRecorder(metricspkg.DirectionToHost, "HTTPRoute")

	recorder.RecordPatchApplied("rewriteName")
	recorder.RecordPatchApplied("rewriteRef")
	recorder.RecordPatchApplied("rewriteName")

	if got := testutil.ToFloat64(metricspkg.PatchAppliedTotal.WithLabelValues(metricspkg.DirectionToHost, "HTTPRoute", "rewriteName")); got != 2 {
		t.Errorf("RecordPatchApplied(rewriteName) = %v, want 2", got)
	}
}

func TestEventsEmittedCounter_WiredToLoggingPackage(t *testing.T) {
	// Verify that the metricspkg.EventsEmittedTotal counter is wired to the logging package
	// The actual event emission is tested in logging/events_test.go
	// This test just verifies the wiring is in place
	if metricspkg.EventsEmittedTotal == nil {
		t.Error("metricspkg.EventsEmittedTotal counter is nil")
	}

	// The init() function should have wired this up
	// We can't easily test the logging package integration here without
	// creating a circular dependency, but we verify the counter exists
}

func TestOperationTimer_ObserveWithStatus(t *testing.T) {
	metricspkg.SyncOperationDuration.Reset()
	metricspkg.SyncOperationsTotal.Reset()

	recorder := metricspkg.NewRecorder(metricspkg.DirectionToHost, "Service")

	// Test successful operation
	timer := recorder.NewOperationTimer(metricspkg.OperationCreate)
	time.Sleep(5 * time.Millisecond)
	timer.ObserveWithStatus(nil)

	if got := testutil.ToFloat64(metricspkg.SyncOperationsTotal.WithLabelValues(metricspkg.DirectionToHost, "Service", metricspkg.OperationCreate, metricspkg.StatusSuccess)); got != 1 {
		t.Errorf("ObserveWithStatus(nil) success count = %v, want 1", got)
	}

	// Test failed operation
	timer2 := recorder.NewOperationTimer(metricspkg.OperationUpdate)
	time.Sleep(5 * time.Millisecond)
	timer2.ObserveWithStatus(errors.New("test error"))

	if got := testutil.ToFloat64(metricspkg.SyncOperationsTotal.WithLabelValues(metricspkg.DirectionToHost, "Service", metricspkg.OperationUpdate, metricspkg.StatusError)); got != 1 {
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
		{"conflict error", errors.New("Operation cannot be fulfilled: conflict"), metricspkg.ErrorTypeConflict},
		{"already exists", errors.New("resource already exists"), metricspkg.ErrorTypeConflict},
		{"optimistic lock", errors.New("optimistic lock error"), metricspkg.ErrorTypeConflict},
		{"not found", errors.New("resource not found"), metricspkg.ErrorTypeNotFound},
		{"NotFound", errors.New("the server returned NotFound"), metricspkg.ErrorTypeNotFound},
		{"invalid", errors.New("invalid spec field"), metricspkg.ErrorTypeValidation},
		{"validation", errors.New("validation failed"), metricspkg.ErrorTypeValidation},
		{"timeout", errors.New("request timeout"), metricspkg.ErrorTypeTimeout},
		{"deadline exceeded", errors.New("context deadline exceeded"), metricspkg.ErrorTypeTimeout},
		{"context canceled", errors.New("context canceled"), metricspkg.ErrorTypeTimeout},
		{"unknown error", errors.New("something went wrong"), metricspkg.ErrorTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := metricspkg.ClassifyError(tt.err)
			if result != tt.expected {
				t.Errorf("metricspkg.ClassifyError(%v) = %q, want %q", tt.err, result, tt.expected)
			}
		})
	}
}

func TestSetPluginInfo(t *testing.T) {
	metricspkg.PluginInfo.Reset()

	metricspkg.SetPluginInfo("1.0.0", "abc123", "2024-01-01")

	if got := testutil.ToFloat64(metricspkg.PluginInfo.WithLabelValues("1.0.0", "abc123", "2024-01-01")); got != 1 {
		t.Errorf("metricspkg.SetPluginInfo() gauge = %v, want 1", got)
	}
}

func TestRegisterSyncer(t *testing.T) {
	metricspkg.SyncerInfo.Reset()

	metricspkg.RegisterSyncer(metricspkg.DirectionToHost, "Gateway", "gateway.networking.k8s.io/v1", "sync", true)
	metricspkg.RegisterSyncer(metricspkg.DirectionFromHost, "ConfigMap", "v1", "mirror", false)

	if got := testutil.ToFloat64(metricspkg.SyncerInfo.WithLabelValues(metricspkg.DirectionToHost, "Gateway", "gateway.networking.k8s.io/v1", "sync", "true")); got != 1 {
		t.Errorf("metricspkg.RegisterSyncer(toHost, Gateway) = %v, want 1", got)
	}
	if got := testutil.ToFloat64(metricspkg.SyncerInfo.WithLabelValues(metricspkg.DirectionFromHost, "ConfigMap", "v1", "mirror", "false")); got != 1 {
		t.Errorf("metricspkg.RegisterSyncer(fromHost, ConfigMap) = %v, want 1", got)
	}
}

func TestRecordConfigReload(t *testing.T) {
	metricspkg.ConfigReloadsTotal.Reset()

	metricspkg.RecordConfigReload(true)
	metricspkg.RecordConfigReload(true)
	metricspkg.RecordConfigReload(false)

	if got := testutil.ToFloat64(metricspkg.ConfigReloadsTotal.WithLabelValues(metricspkg.StatusSuccess)); got != 2 {
		t.Errorf("metricspkg.RecordConfigReload(success) = %v, want 2", got)
	}
	if got := testutil.ToFloat64(metricspkg.ConfigReloadsTotal.WithLabelValues(metricspkg.StatusError)); got != 1 {
		t.Errorf("metricspkg.RecordConfigReload(error) = %v, want 1", got)
	}
}

// For proper histogram testing, we use prometheus testutil
func init() {
	// Ensure metrics are available for testing
	_ = metricspkg.GetMetricsForTesting()
}
