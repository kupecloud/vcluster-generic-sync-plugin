package logging

import (
	"fmt"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NIL-RECEIVER SAFETY PATTERN
//
// All EventEmitter methods are designed to be nil-safe. Callers do not need to check
// if the emitter is nil before calling methods - the methods will silently return
// without side effects if the receiver is nil. This pattern allows callers to use:
//
//     s.events.EmitCreated(obj, name)  // Safe even if s.events is nil
//
// Instead of requiring verbose nil checks:
//
//     if s.events != nil { s.events.EmitCreated(obj, name) }
//
// This is useful because EventEmitter is only created when events are enabled,
// so the nil case is common during normal operation.

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
	// Format: "Kind 'name' synced (direction)" - cleaner and includes resource name
	e.recorder.Event(obj, EventTypeNormal, ReasonCreated,
		fmt.Sprintf("%s '%s' synced (%s)", e.kind, targetName, e.direction))
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
		fmt.Sprintf("Failed to create %s (%s): %s", e.kind, e.direction, sanitizeError(err)))
	e.recordMetric(EventTypeWarning, ReasonCreateFailed)
}

// EmitUpdateFailed emits a warning event when resource update fails
func (e *EventEmitter) EmitUpdateFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeWarning, ReasonUpdateFailed,
		fmt.Sprintf("Failed to update %s (%s): %s", e.kind, e.direction, sanitizeError(err)))
	e.recordMetric(EventTypeWarning, ReasonUpdateFailed)
}

// EmitDeleteFailed emits a warning event when resource deletion fails
func (e *EventEmitter) EmitDeleteFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeWarning, ReasonDeleteFailed,
		fmt.Sprintf("Failed to delete %s (%s): %s", e.kind, e.direction, sanitizeError(err)))
	e.recordMetric(EventTypeWarning, ReasonDeleteFailed)
}

// EmitPatchFailed emits a warning event when patch application fails
func (e *EventEmitter) EmitPatchFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeWarning, ReasonPatchFailed,
		fmt.Sprintf("Failed to apply patches to %s (%s): %s", e.kind, e.direction, sanitizeError(err)))
	e.recordMetric(EventTypeWarning, ReasonPatchFailed)
}

// EmitSyncFailed emits a warning event for general sync failures
func (e *EventEmitter) EmitSyncFailed(obj client.Object, err error) {
	if e == nil || e.recorder == nil || obj == nil {
		return
	}
	e.recorder.Event(obj, EventTypeWarning, ReasonSyncFailed,
		fmt.Sprintf("Sync failed for %s (%s): %s", e.kind, e.direction, sanitizeError(err)))
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

// sanitizeError removes potentially sensitive information from error messages
// before including them in Kubernetes events (which are visible to users).
// This helps prevent accidental exposure of secrets, tokens, or credentials.
func sanitizeError(err error) string {
	if err == nil {
		return ""
	}

	errStr := err.Error()

	// List of patterns that might indicate sensitive content
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

	// Check if error might contain sensitive data
	lowerErr := strings.ToLower(errStr)
	for _, pattern := range sensitivePatterns {
		if strings.Contains(lowerErr, pattern) {
			// Return a sanitized version without the potentially sensitive content
			return fmt.Sprintf("error (details redacted for security): %s",
				truncateString(redactSensitiveContent(errStr), 200))
		}
	}

	// Truncate very long error messages
	return truncateString(errStr, 500)
}

// redactSensitiveContent replaces content after sensitive keywords with [REDACTED]
func redactSensitiveContent(s string) string {
	// Simple redaction: if the string contains sensitive patterns,
	// we truncate after the first occurrence of common delimiters
	// This is a best-effort approach
	result := s

	// Redact anything after common patterns that might be followed by secrets
	redactAfter := []string{"bearer ", "token=", "password=", "secret=", "apikey=", "api-key="}
	for _, pattern := range redactAfter {
		lowerResult := strings.ToLower(result)
		if idx := strings.Index(lowerResult, pattern); idx != -1 {
			endIdx := idx + len(pattern)
			// Find the end of the value (space, quote, or end of string)
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
