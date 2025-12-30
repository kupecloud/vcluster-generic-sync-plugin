package logging

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Event types for Kubernetes events
const (
	EventTypeNormal  = "Normal"
	EventTypeWarning = "Warning"
)

// Event reasons for sync operations
const (
	// Sync lifecycle events
	ReasonSynced        = "Synced"
	ReasonCreated       = "Created"
	ReasonUpdated       = "Updated"
	ReasonDeleted       = "Deleted"
	ReasonSyncFailed    = "SyncFailed"
	ReasonCreateFailed  = "CreateFailed"
	ReasonUpdateFailed  = "UpdateFailed"
	ReasonDeleteFailed  = "DeleteFailed"
	ReasonPatchFailed   = "PatchFailed"
	ReasonSelectorMatch = "SelectorMatch"
	ReasonFiltered      = "Filtered"
)

// EventsEmittedCounter is set by the metrics package to allow recording event emissions
// This avoids a circular import between logging and metrics packages
var EventsEmittedCounter *prometheus.CounterVec

// EventEmitter provides standardized Kubernetes event emission for sync operations
type EventEmitter struct {
	recorder  record.EventRecorder
	direction string
	kind      string
}

// NewEventEmitter creates a new event emitter for a syncer
func NewEventEmitter(recorder record.EventRecorder, direction, kind string) *EventEmitter {
	return &EventEmitter{
		recorder:  recorder,
		direction: direction,
		kind:      kind,
	}
}

// recordMetric records the event emission metric if the counter is configured
func (e *EventEmitter) recordMetric(eventType, reason string) {
	if EventsEmittedCounter != nil {
		EventsEmittedCounter.WithLabelValues(e.direction, e.kind, eventType, reason).Inc()
	}
}

// EmitCreated emits an event when a resource is successfully created
func (e *EventEmitter) EmitCreated(obj client.Object, targetName string) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeNormal, ReasonCreated,
		fmt.Sprintf("%s %s synced to %s", e.kind, e.direction, targetName))
	e.recordMetric(EventTypeNormal, ReasonCreated)
}

// EmitUpdated emits an event when a resource is successfully updated
func (e *EventEmitter) EmitUpdated(obj client.Object) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeNormal, ReasonUpdated,
		fmt.Sprintf("%s %s updated", e.kind, e.direction))
	e.recordMetric(EventTypeNormal, ReasonUpdated)
}

// EmitDeleted emits an event when a resource is deleted
func (e *EventEmitter) EmitDeleted(obj client.Object, reason string) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeNormal, ReasonDeleted,
		fmt.Sprintf("%s deleted (%s): %s", e.kind, e.direction, reason))
	e.recordMetric(EventTypeNormal, ReasonDeleted)
}

// EmitCreateFailed emits a warning event when resource creation fails
func (e *EventEmitter) EmitCreateFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeWarning, ReasonCreateFailed,
		fmt.Sprintf("Failed to create %s (%s): %v", e.kind, e.direction, err))
	e.recordMetric(EventTypeWarning, ReasonCreateFailed)
}

// EmitUpdateFailed emits a warning event when resource update fails
func (e *EventEmitter) EmitUpdateFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeWarning, ReasonUpdateFailed,
		fmt.Sprintf("Failed to update %s (%s): %v", e.kind, e.direction, err))
	e.recordMetric(EventTypeWarning, ReasonUpdateFailed)
}

// EmitDeleteFailed emits a warning event when resource deletion fails
func (e *EventEmitter) EmitDeleteFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeWarning, ReasonDeleteFailed,
		fmt.Sprintf("Failed to delete %s (%s): %v", e.kind, e.direction, err))
	e.recordMetric(EventTypeWarning, ReasonDeleteFailed)
}

// EmitPatchFailed emits a warning event when patch application fails
func (e *EventEmitter) EmitPatchFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeWarning, ReasonPatchFailed,
		fmt.Sprintf("Failed to apply patches to %s (%s): %v", e.kind, e.direction, err))
	e.recordMetric(EventTypeWarning, ReasonPatchFailed)
}

// EmitSyncFailed emits a warning event for general sync failures
func (e *EventEmitter) EmitSyncFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeWarning, ReasonSyncFailed,
		fmt.Sprintf("Sync failed for %s (%s): %v", e.kind, e.direction, err))
	e.recordMetric(EventTypeWarning, ReasonSyncFailed)
}

// EmitFiltered emits an event when a resource is filtered out by selector
func (e *EventEmitter) EmitFiltered(obj client.Object, reason string) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeNormal, ReasonFiltered,
		fmt.Sprintf("%s filtered (%s): %s", e.kind, e.direction, reason))
	e.recordMetric(EventTypeNormal, ReasonFiltered)
}
