package logging

import (
	"encoding/json"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ObjectTracer provides trace-level logging for Kubernetes objects.
// All methods check IsTraceEnabled() to avoid expensive operations when trace is disabled.
type ObjectTracer struct {
	log       *Logger
	direction string
	kind      string
}

// NewObjectTracer creates a new tracer for sync operations
func NewObjectTracer(direction, kind string) *ObjectTracer {
	return &ObjectTracer{
		log:       Log,
		direction: direction,
		kind:      kind,
	}
}

// TraceIncoming logs the full content of an incoming object (source of sync)
func (t *ObjectTracer) TraceIncoming(operation string, obj client.Object) {
	if t == nil || !IsTraceEnabled() || obj == nil {
		return
	}

	t.log.Trace("Incoming object",
		"direction", t.direction,
		"kind", t.kind,
		"operation", operation,
		"name", obj.GetName(),
		"namespace", obj.GetNamespace(),
		"resourceVersion", obj.GetResourceVersion(),
		"generation", obj.GetGeneration(),
		"content", t.serializeObject(obj))
}

// TraceOutgoing logs the full content of an outgoing object (result of sync)
func (t *ObjectTracer) TraceOutgoing(operation string, obj client.Object) {
	if t == nil || !IsTraceEnabled() || obj == nil {
		return
	}

	t.log.Trace("Outgoing object",
		"direction", t.direction,
		"kind", t.kind,
		"operation", operation,
		"name", obj.GetName(),
		"namespace", obj.GetNamespace(),
		"resourceVersion", obj.GetResourceVersion(),
		"generation", obj.GetGeneration(),
		"content", t.serializeObject(obj))
}

// TraceDiff logs the before and after state for update operations
func (t *ObjectTracer) TraceDiff(operation string, before, after client.Object) {
	if t == nil || !IsTraceEnabled() {
		return
	}

	t.log.Trace("Object diff",
		"direction", t.direction,
		"kind", t.kind,
		"operation", operation,
		"name", t.getName(after),
		"namespace", t.getNamespace(after),
		"before", t.serializeObject(before),
		"after", t.serializeObject(after))
}

// TracePatched logs the object state after patches are applied
func (t *ObjectTracer) TracePatched(operation string, original, patched client.Object) {
	if t == nil || !IsTraceEnabled() {
		return
	}

	t.log.Trace("Object after patches",
		"direction", t.direction,
		"kind", t.kind,
		"operation", operation,
		"name", t.getName(patched),
		"namespace", t.getNamespace(patched),
		"original", t.serializeObject(original),
		"patched", t.serializeObject(patched))
}

// TraceResult logs the result of an operation
func (t *ObjectTracer) TraceResult(operation string, obj client.Object, err error) {
	if t == nil || !IsTraceEnabled() {
		return
	}

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	t.log.Trace("Operation result",
		"direction", t.direction,
		"kind", t.kind,
		"operation", operation,
		"name", t.getName(obj),
		"namespace", t.getNamespace(obj),
		"success", err == nil,
		"error", errStr)
}

// serialiseObject converts an object to a JSON string for logging
func (t *ObjectTracer) serializeObject(obj client.Object) string {
	if obj == nil {
		return "null"
	}

	// For unstructured objects, serialise the full object
	if u, ok := obj.(*unstructured.Unstructured); ok {
		data, err := json.Marshal(u.Object)
		if err != nil {
			return "error: " + err.Error()
		}
		return string(data)
	}

	// For typed objects, use standard JSON serialisation
	data, err := json.Marshal(obj)
	if err != nil {
		return "error: " + err.Error()
	}
	return string(data)
}

func (t *ObjectTracer) getName(obj client.Object) string {
	if obj == nil {
		return ""
	}
	return obj.GetName()
}

func (t *ObjectTracer) getNamespace(obj client.Object) string {
	if obj == nil {
		return ""
	}
	return obj.GetNamespace()
}
