package logging

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// TestSerializeObject_RedactsSecretData: Secret data/stringData must
// never appear verbatim in trace output.
func TestSerializeObject_RedactsSecretData(t *testing.T) {
	tracer := NewObjectTracer("toHost", "Secret")

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("Secret")
	u.SetName("creds")
	u.Object["data"] = map[string]interface{}{
		"password": "c3VwZXJzZWNyZXQ=",
	}
	u.Object["stringData"] = map[string]interface{}{
		"token": "plaintexttoken",
	}

	out := tracer.serializeObject(u)

	if strings.Contains(out, "c3VwZXJzZWNyZXQ=") || strings.Contains(out, "plaintexttoken") {
		t.Fatalf("secret material leaked into trace output: %s", out)
	}
	if !strings.Contains(out, "[REDACTED-") {
		t.Fatalf("expected redaction placeholder, got: %s", out)
	}

	// Original object must be unmodified (we redact a deep copy).
	if got := u.Object["data"].(map[string]interface{})["password"]; got != "c3VwZXJzZWNyZXQ=" {
		t.Fatalf("original object was mutated: %v", got)
	}
}

// TestSerializeObject_NonSecretUnchanged ensures non-Secret objects still serialise fully.
func TestSerializeObject_NonSecretUnchanged(t *testing.T) {
	tracer := NewObjectTracer("toHost", "ConfigMap")

	u := &unstructured.Unstructured{}
	u.SetAPIVersion("v1")
	u.SetKind("ConfigMap")
	u.Object["data"] = map[string]interface{}{"key": "value"}

	out := tracer.serializeObject(u)
	if !strings.Contains(out, "value") {
		t.Fatalf("expected ConfigMap data to be serialised, got: %s", out)
	}
}
