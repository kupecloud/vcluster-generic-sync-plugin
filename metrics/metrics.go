// Package metrics provides Prometheus metrics for the vcluster generic sync plugin.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	"github.com/kupecloud/vcluster-generic-sync-plugin/logging"
)

// Metric constants for labels and values.
const (
	// Namespace is the prefix for all generic sync metrics.
	Namespace = "generic_sync"

	// LabelDirection is the metric label for sync direction.
	LabelDirection = "direction"
	// LabelKind is the metric label for resource kind.
	LabelKind = "kind"
	// LabelOperation is the metric label for operation type.
	LabelOperation = "operation"
	// LabelStatus is the metric label for operation status.
	LabelStatus = "status"
	// LabelErrorType is the metric label for error classification.
	LabelErrorType = "error_type"

	// DirectionToHost indicates syncing from virtual to host cluster.
	DirectionToHost = "toHost"
	// DirectionFromHost indicates syncing from host to virtual cluster.
	DirectionFromHost = "fromHost"

	// OperationCreate indicates a create operation.
	OperationCreate = "create"
	// OperationUpdate indicates an update operation.
	OperationUpdate = "update"
	// OperationDelete indicates a delete operation.
	OperationDelete = "delete"
	// OperationSync indicates a generic sync operation.
	OperationSync = "sync"

	// StatusSuccess indicates successful completion.
	StatusSuccess = "success"
	// StatusError indicates an error occurred.
	StatusError = "error"
	// StatusSkipped indicates the operation was skipped.
	StatusSkipped = "skipped"

	// ErrorTypeConflict indicates a resource conflict error.
	ErrorTypeConflict = "conflict"
	// ErrorTypeNotFound indicates a resource not found error.
	ErrorTypeNotFound = "not_found"
	// ErrorTypeValidation indicates a validation error.
	ErrorTypeValidation = "validation"
	// ErrorTypeTimeout indicates a timeout error.
	ErrorTypeTimeout = "timeout"
	// ErrorTypeUnknown indicates an unknown error type.
	ErrorTypeUnknown = "unknown"
)

var (
	// SyncOperationsTotal counts sync operations by direction, kind, operation, and status
	SyncOperationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "operations_total",
			Help:      "Total number of sync operations performed",
		},
		[]string{LabelDirection, LabelKind, LabelOperation, LabelStatus},
	)

	// SyncOperationDuration measures the duration of sync operations
	SyncOperationDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "operation_duration_seconds",
			Help:      "Duration of sync operations in seconds",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{LabelDirection, LabelKind, LabelOperation},
	)

	// SyncErrorsTotal counts sync errors by direction, kind, and error type
	SyncErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "errors_total",
			Help:      "Total number of sync errors",
		},
		[]string{LabelDirection, LabelKind, LabelErrorType},
	)

	// ResourcesManaged tracks the number of resources currently being managed
	ResourcesManaged = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "resources_managed",
			Help:      "Number of resources currently being managed by the syncer",
		},
		[]string{LabelDirection, LabelKind},
	)

	// ReconcileTotal counts total reconciliation attempts
	ReconcileTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "reconcile_total",
			Help:      "Total number of reconciliation attempts",
		},
		[]string{LabelDirection, LabelKind},
	)

	// ReconcileDuration measures the duration of reconciliation loops
	ReconcileDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: Namespace,
			Name:      "reconcile_duration_seconds",
			Help:      "Duration of reconciliation loops in seconds",
			Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{LabelDirection, LabelKind},
	)

	// SyncerInfo provides information about registered syncers
	SyncerInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "syncer_info",
			Help:      "Information about registered syncers (value is always 1)",
		},
		[]string{LabelDirection, LabelKind, "api_version", "mode", "status_sync"},
	)

	// NamespaceFilteredTotal counts resources filtered by namespace rules
	NamespaceFilteredTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "namespace_filtered_total",
			Help:      "Total number of resources filtered by namespace rules",
		},
		[]string{LabelDirection, LabelKind, "namespace"},
	)

	// SelectorFilteredTotal counts resources filtered by selector rules
	SelectorFilteredTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "selector_filtered_total",
			Help:      "Total number of resources filtered by label selector rules",
		},
		[]string{LabelDirection, LabelKind},
	)

	// PatchAppliedTotal counts patches applied to resources
	PatchAppliedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "patch_applied_total",
			Help:      "Total number of patches applied to resources",
		},
		[]string{LabelDirection, LabelKind, "patch_type"},
	)

	// EventsEmittedTotal counts Kubernetes events emitted
	EventsEmittedTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "events_emitted_total",
			Help:      "Total number of Kubernetes events emitted",
		},
		[]string{LabelDirection, LabelKind, "event_type", "reason"},
	)

	// PluginInfo provides build information about the plugin
	PluginInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: Namespace,
			Name:      "plugin_info",
			Help:      "Plugin build information (value is always 1)",
		},
		[]string{"version", "git_commit", "build_date"},
	)

	// ConfigReloadsTotal counts configuration reloads
	ConfigReloadsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: Namespace,
			Name:      "config_reloads_total",
			Help:      "Total number of configuration reloads",
		},
		[]string{"status"},
	)
)

// init registers all metrics with the controller-runtime metrics registry
func init() {
	// Register all metrics with controller-runtime's registry
	// This ensures they're exposed on the same port as vcluster's metrics
	metrics.Registry.MustRegister(
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
	)

	// Wire up the events emitted counter to the logging package
	// This allows EventEmitter to record metrics without circular imports
	logging.EventsEmittedCounter = EventsEmittedTotal
}

// SetPluginInfo sets the plugin build information metric
func SetPluginInfo(version, gitCommit, buildDate string) {
	PluginInfo.WithLabelValues(version, gitCommit, buildDate).Set(1)
}

// RegisterSyncer records information about a registered syncer
func RegisterSyncer(direction, kind, apiVersion, mode string, statusSync bool) {
	statusSyncStr := "false"
	if statusSync {
		statusSyncStr = "true"
	}
	SyncerInfo.WithLabelValues(direction, kind, apiVersion, mode, statusSyncStr).Set(1)
}

// RecordConfigReload records a configuration reload attempt
func RecordConfigReload(success bool) {
	status := StatusSuccess
	if !success {
		status = StatusError
	}
	ConfigReloadsTotal.WithLabelValues(status).Inc()
}
