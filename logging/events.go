package logging

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/tools/events"
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

// EventEmitter provides standardised Kubernetes event emission for sync operations
type EventEmitter struct {
	recorder  events.EventRecorder
	direction string
	kind      string
}

// NewEventEmitter creates a new event emitter for a syncer
func NewEventEmitter(recorder events.EventRecorder, direction, kind string) *EventEmitter {
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
	e.recorder.Eventf(obj, nil, EventTypeNormal, ReasonCreated, ReasonCreated, fmt.Sprintf("%s '%s' synced (%s)", e.kind, targetName, e.direction))
	e.recordMetric(EventTypeNormal, ReasonCreated)
}

// EmitUpdated emits an event when a resource is successfully updated
func (e *EventEmitter) EmitUpdated(obj client.Object) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Eventf(obj, nil, EventTypeNormal, ReasonUpdated, ReasonUpdated, fmt.Sprintf("%s %s updated", e.kind, e.direction))
	e.recordMetric(EventTypeNormal, ReasonUpdated)
}

// EmitDeleted emits an event when a resource is deleted
func (e *EventEmitter) EmitDeleted(obj client.Object, reason string) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Eventf(obj, nil, EventTypeNormal, ReasonDeleted, ReasonDeleted, fmt.Sprintf("%s deleted (%s): %s", e.kind, e.direction, reason))
	e.recordMetric(EventTypeNormal, ReasonDeleted)
}

// EmitCreateFailed emits a warning event when resource creation fails
func (e *EventEmitter) EmitCreateFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Eventf(obj, nil, EventTypeWarning, ReasonCreateFailed, ReasonCreateFailed, fmt.Sprintf("Failed to create %s (%s): %s", e.kind, e.direction, sanitizeError(err)))
	e.recordMetric(EventTypeWarning, ReasonCreateFailed)
}

// EmitUpdateFailed emits a warning event when resource update fails
func (e *EventEmitter) EmitUpdateFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Eventf(obj, nil, EventTypeWarning, ReasonUpdateFailed, ReasonUpdateFailed, fmt.Sprintf("Failed to update %s (%s): %s", e.kind, e.direction, sanitizeError(err)))
	e.recordMetric(EventTypeWarning, ReasonUpdateFailed)
}

// EmitDeleteFailed emits a warning event when resource deletion fails
func (e *EventEmitter) EmitDeleteFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Eventf(obj, nil, EventTypeWarning, ReasonDeleteFailed, ReasonDeleteFailed, fmt.Sprintf("Failed to delete %s (%s): %s", e.kind, e.direction, sanitizeError(err)))
	e.recordMetric(EventTypeWarning, ReasonDeleteFailed)
}

// EmitPatchFailed emits a warning event when patch application fails
func (e *EventEmitter) EmitPatchFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Eventf(obj, nil, EventTypeWarning, ReasonPatchFailed, ReasonPatchFailed, fmt.Sprintf("Failed to apply patches to %s (%s): %s", e.kind, e.direction, sanitizeError(err)))
	e.recordMetric(EventTypeWarning, ReasonPatchFailed)
}

// EmitSyncFailed emits a warning event for general sync failures
func (e *EventEmitter) EmitSyncFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Eventf(obj, nil, EventTypeWarning, ReasonSyncFailed, ReasonSyncFailed, fmt.Sprintf("Sync failed for %s (%s): %s", e.kind, e.direction, sanitizeError(err)))
	e.recordMetric(EventTypeWarning, ReasonSyncFailed)
}

// EmitFiltered emits an event when a resource is filtered out by selector
func (e *EventEmitter) EmitFiltered(obj client.Object, reason string) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Eventf(obj, nil, EventTypeNormal, ReasonFiltered, ReasonFiltered, fmt.Sprintf("%s filtered (%s): %s", e.kind, e.direction, reason))
	e.recordMetric(EventTypeNormal, ReasonFiltered)
}

// sanitizeError removes potentially sensitive content from errors used in events.
func sanitizeError(err error) string {
	if err == nil {
		return ""
	}

	errStr := err.Error()

	sensitivePatterns := []string{
		"bearer",
		"token",
		"password",
		"secret",
		"credential",
		"api-key",
		"apikey",
		"authorization",
	}

	lowerErr := strings.ToLower(errStr)
	for _, pattern := range sensitivePatterns {
		if strings.Contains(lowerErr, pattern) {
			return fmt.Sprintf("error (details redacted for security): %s",
				truncateString(redactSensitiveContent(errStr), 200))
		}
	}

	return truncateString(errStr, 500)
}

// redactSensitiveContent replaces content after sensitive keywords with [REDACTED]
func redactSensitiveContent(s string) string {
	result := s

	redactAfter := []string{"bearer ", "token=", "password=", "secret=", "apikey=", "api-key="}
	for _, pattern := range redactAfter {
		lowerResult := strings.ToLower(result)
		if idx := strings.Index(lowerResult, pattern); idx != -1 {
			endIdx := idx + len(pattern)
			valueEnd := endIdx
			for valueEnd < len(result) {
				c := result[valueEnd]
				if c == ' ' || c == '"' || c == '\'' || c == ',' || c == '}' {
					break
				}
				valueEnd++
			}
			result = result[:endIdx] + "[REDACTED]" + result[valueEnd:]
		}
	}

	return result
}

// truncateString truncates a string to maxLen characters, adding "..." if truncated
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
