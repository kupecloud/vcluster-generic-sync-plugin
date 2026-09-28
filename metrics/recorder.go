package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
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

// RecordOperationSuccess records a successful sync operation and refreshes the
// last-successful-sync timestamp.
func (r *Recorder) RecordOperationSuccess(operation string) {
	if r == nil {
		return
	}
	r.RecordOperation(operation, StatusSuccess)
	r.RecordSyncSuccess()
}

// RecordOperationError records a failed sync operation
func (r *Recorder) RecordOperationError(operation string) {
	if r == nil {
		return
	}
	r.RecordOperation(operation, StatusError)
}

// RecordOperationSkipped records a skipped (no-op) sync operation and refreshes the
// last-successful-sync timestamp — a no-op reconcile still proves the syncer is live
// and converged.
func (r *Recorder) RecordOperationSkipped(operation string) {
	if r == nil {
		return
	}
	r.RecordOperation(operation, StatusSkipped)
	r.RecordSyncSuccess()
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

// DecResourcesManaged decrements the managed resources count.
// The gauge is maintained purely by Inc/Dec and is not seeded from existing managed
// objects at startup, so a restart resets it to 0 while real synced resources persist;
// subsequent deletes can therefore transiently drive it negative. The gauge is treated
// as an approximation (no read-then-decrement clamp, which is racy under concurrent
// reconciles) until a census-based count is added.
func (r *Recorder) DecResourcesManaged() {
	if r == nil {
		return
	}
	ResourcesManaged.WithLabelValues(r.direction, r.kind).Dec()
}

// RecordSyncSuccess stamps the last-successful-sync timestamp for this syncer.
// Call on every successful create/update/delete/no-op reconcile so a freshness
// alert can detect a silently-wedged syncer.
func (r *Recorder) RecordSyncSuccess() {
	if r == nil {
		return
	}
	LastSuccessfulSyncTimestamp.WithLabelValues(r.direction, r.kind).SetToCurrentTime()
}

// RecordNamespaceFiltered records a resource filtered by namespace rules.
// The filtered namespace is intentionally not recorded as a label — it is
// tenant-controlled and would be an unbounded-cardinality vector.
func (r *Recorder) RecordNamespaceFiltered() {
	if r == nil {
		return
	}
	NamespaceFilteredTotal.WithLabelValues(r.direction, r.kind).Inc()
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

// ClassifyError returns the error_type metric label for a given error.
//
// It delegates to logging.ClassifyError (typed apierrors checks) and maps the result
// to a metric label, so the errors_total label, the structured logs, and the requeue
// policy always agree on the same failure.
func ClassifyError(err error) string {
	if err == nil {
		return ""
	}
	switch logging.ClassifyError(err) {
	case logging.ErrorTypeConflict:
		return ErrorTypeConflict
	case logging.ErrorTypeNotFound:
		return ErrorTypeNotFound
	case logging.ErrorTypeValidation:
		return ErrorTypeValidation
	case logging.ErrorTypeForbidden:
		return ErrorTypeForbidden
	case logging.ErrorTypeTransient:
		return ErrorTypeTransient
	default:
		return ErrorTypeUnknown
	}
}

// GetMetricsForTesting returns all metric collectors for testing purposes
func GetMetricsForTesting() []prometheus.Collector {
	return []prometheus.Collector{
		SyncOperationsTotal,
		SyncOperationDuration,
		SyncErrorsTotal,
		ResourcesManaged,
		LastSuccessfulSyncTimestamp,
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
