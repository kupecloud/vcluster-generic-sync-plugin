package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Recorder provides convenient methods for recording sync metrics.
// All methods are nil-safe and will be no-ops if the receiver is nil.
type Recorder struct {
	direction string
	kind      string
}

// NewRecorder creates a new metrics recorder for a specific syncer
func NewRecorder(direction, kind string) *Recorder {
	return &Recorder{
		direction: direction,
		kind:      kind,
	}
}

// RecordOperation records a sync operation with its status
func (r *Recorder) RecordOperation(operation, status string) {
	if r == nil {
		return
	}
	SyncOperationsTotal.WithLabelValues(r.direction, r.kind, operation, status).Inc()
}

// RecordOperationSuccess records a successful sync operation
func (r *Recorder) RecordOperationSuccess(operation string) {
	if r == nil {
		return
	}
	r.RecordOperation(operation, StatusSuccess)
}

// RecordOperationError records a failed sync operation
func (r *Recorder) RecordOperationError(operation string) {
	if r == nil {
		return
	}
	r.RecordOperation(operation, StatusError)
}

// RecordOperationSkipped records a skipped sync operation
func (r *Recorder) RecordOperationSkipped(operation string) {
	if r == nil {
		return
	}
	r.RecordOperation(operation, StatusSkipped)
}

// RecordOperationDuration records the duration of a sync operation
func (r *Recorder) RecordOperationDuration(operation string, duration time.Duration) {
	if r == nil {
		return
	}
	SyncOperationDuration.WithLabelValues(r.direction, r.kind, operation).Observe(duration.Seconds())
}

// TimeOperation returns a function that records the duration when called
// Usage: defer recorder.TimeOperation("create")()
func (r *Recorder) TimeOperation(operation string) func() {
	if r == nil {
		return func() {} // no-op
	}
	start := time.Now()
	return func() {
		r.RecordOperationDuration(operation, time.Since(start))
	}
}

// RecordError records a sync error with its type
func (r *Recorder) RecordError(errorType string) {
	if r == nil {
		return
	}
	SyncErrorsTotal.WithLabelValues(r.direction, r.kind, errorType).Inc()
}

// RecordReconcile records a reconciliation attempt
func (r *Recorder) RecordReconcile() {
	if r == nil {
		return
	}
	ReconcileTotal.WithLabelValues(r.direction, r.kind).Inc()
}

// RecordReconcileDuration records the duration of a reconciliation
func (r *Recorder) RecordReconcileDuration(duration time.Duration) {
	if r == nil {
		return
	}
	ReconcileDuration.WithLabelValues(r.direction, r.kind).Observe(duration.Seconds())
}

// TimeReconcile returns a function that records reconciliation duration when called
// Usage: defer recorder.TimeReconcile()()
func (r *Recorder) TimeReconcile() func() {
	if r == nil {
		return func() {} // no-op
	}
	start := time.Now()
	return func() {
		r.RecordReconcileDuration(time.Since(start))
	}
}

// SetResourcesManaged sets the gauge for managed resources
func (r *Recorder) SetResourcesManaged(count float64) {
	if r == nil {
		return
	}
	ResourcesManaged.WithLabelValues(r.direction, r.kind).Set(count)
}

// IncResourcesManaged increments the managed resources count
func (r *Recorder) IncResourcesManaged() {
	if r == nil {
		return
	}
	ResourcesManaged.WithLabelValues(r.direction, r.kind).Inc()
}

// DecResourcesManaged decrements the managed resources count
func (r *Recorder) DecResourcesManaged() {
	if r == nil {
		return
	}
	ResourcesManaged.WithLabelValues(r.direction, r.kind).Dec()
}

// RecordNamespaceFiltered records a resource filtered by namespace rules
func (r *Recorder) RecordNamespaceFiltered(namespace string) {
	if r == nil {
		return
	}
	NamespaceFilteredTotal.WithLabelValues(r.direction, r.kind, namespace).Inc()
}

// RecordSelectorFiltered records a resource filtered by selector rules
func (r *Recorder) RecordSelectorFiltered() {
	if r == nil {
		return
	}
	SelectorFilteredTotal.WithLabelValues(r.direction, r.kind).Inc()
}

// RecordPatchApplied records a patch applied to a resource
func (r *Recorder) RecordPatchApplied(patchType string) {
	if r == nil {
		return
	}
	PatchAppliedTotal.WithLabelValues(r.direction, r.kind, patchType).Inc()
}

// OperationTimer helps time operations with automatic recording
type OperationTimer struct {
	recorder  *Recorder
	operation string
	start     time.Time
}

// NewOperationTimer creates a timer for an operation
func (r *Recorder) NewOperationTimer(operation string) *OperationTimer {
	return &OperationTimer{
		recorder:  r,
		operation: operation,
		start:     time.Now(),
	}
}

// ObserveDuration records the duration and returns it
func (t *OperationTimer) ObserveDuration() time.Duration {
	duration := time.Since(t.start)
	t.recorder.RecordOperationDuration(t.operation, duration)
	return duration
}

// ObserveWithStatus records the duration and operation status
func (t *OperationTimer) ObserveWithStatus(err error) time.Duration {
	duration := time.Since(t.start)
	t.recorder.RecordOperationDuration(t.operation, duration)
	if err != nil {
		t.recorder.RecordOperationError(t.operation)
	} else {
		t.recorder.RecordOperationSuccess(t.operation)
	}
	return duration
}

// ClassifyError returns the appropriate error type label for a given error
func ClassifyError(err error) string {
	if err == nil {
		return ""
	}

	errStr := err.Error()

	// Check for common error patterns
	switch {
	case contains(errStr, "conflict", "already exists", "optimistic lock"):
		return ErrorTypeConflict
	case contains(errStr, "not found", "NotFound"):
		return ErrorTypeNotFound
	case contains(errStr, "invalid", "validation", "spec"):
		return ErrorTypeValidation
	case contains(errStr, "timeout", "deadline exceeded", "context canceled"):
		return ErrorTypeTimeout
	default:
		return ErrorTypeUnknown
	}
}

// contains checks if the string contains any of the substrings (case insensitive)
func contains(s string, substrs ...string) bool {
	sLower := toLower(s)
	for _, sub := range substrs {
		if containsSubstring(sLower, toLower(sub)) {
			return true
		}
	}
	return false
}

// toLower is a simple lowercase conversion without importing strings
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// containsSubstring checks if s contains sub
func containsSubstring(s, sub string) bool {
	if sub == "" {
		return true
	}
	if len(sub) > len(s) {
		return false
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// GetMetricsForTesting returns all metric collectors for testing purposes
func GetMetricsForTesting() []prometheus.Collector {
	return []prometheus.Collector{
		SyncOperationsTotal,
		SyncOperationDuration,
		SyncErrorsTotal,
		ResourcesManaged,
		ReconcileTotal,
		ReconcileDuration,
		SyncerInfo,
		NamespaceFilteredTotal,
		SelectorFilteredTotal,
		PatchAppliedTotal,
		EventsEmittedTotal,
		PluginInfo,
		ConfigReloadsTotal,
	}
}
