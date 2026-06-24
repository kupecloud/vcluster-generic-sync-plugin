package logging

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func TestSyncError_Error(t *testing.T) {
	tests := []struct {
		name     string
		syncErr  *SyncError
		wantPart string
	}{
		{
			name: "with namespace",
			syncErr: &SyncError{
				Operation: "create",
				Kind:      "HTTPRoute",
				Namespace: "default",
				Name:      "my-route",
				Direction: "toHost",
				Cause:     errors.New("connection refused"),
			},
			wantPart: "create HTTPRoute default/my-route (toHost)",
		},
		{
			name: "without namespace",
			syncErr: &SyncError{
				Operation: "delete",
				Kind:      "ClusterIssuer",
				Namespace: "",
				Name:      "letsencrypt",
				Direction: "fromHost",
				Cause:     errors.New("not found"),
			},
			wantPart: "delete ClusterIssuer letsencrypt (fromHost)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.syncErr.Error()
			if got == "" {
				t.Error("Error() returned empty string")
			}
			// Just verify it contains the expected parts
			if tt.wantPart != "" && !containsSubstring(got, tt.wantPart) {
				t.Errorf("Error() = %q, want to contain %q", got, tt.wantPart)
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || substr == "" ||
		(s != "" && substr != "" && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestSyncError_Unwrap(t *testing.T) {
	cause := errors.New("underlying error")
	syncErr := &SyncError{Cause: cause}

	if !errors.Is(syncErr.Unwrap(), cause) {
		t.Errorf("Unwrap() = %v, want %v", syncErr.Unwrap(), cause)
	}
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected ErrorType
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: ErrorTypePermanent,
		},
		{
			name:     "conflict error",
			err:      apierrors.NewConflict(schema.GroupResource{Group: "", Resource: "pods"}, "test", errors.New("conflict")),
			expected: ErrorTypeConflict,
		},
		{
			name:     "not found error",
			err:      apierrors.NewNotFound(schema.GroupResource{Group: "", Resource: "pods"}, "test"),
			expected: ErrorTypeNotFound,
		},
		{
			name:     "invalid error",
			err:      apierrors.NewInvalid(schema.GroupKind{Group: "", Kind: "Pod"}, "test", nil),
			expected: ErrorTypeValidation,
		},
		{
			name:     "bad request error",
			err:      apierrors.NewBadRequest("bad request"),
			expected: ErrorTypeValidation,
		},
		{
			name:     "timeout error",
			err:      apierrors.NewTimeoutError("timeout", 5),
			expected: ErrorTypeTransient,
		},
		{
			name:     "too many requests error",
			err:      apierrors.NewTooManyRequests("rate limited", 5),
			expected: ErrorTypeTransient,
		},
		{
			name:     "service unavailable error",
			err:      apierrors.NewServiceUnavailable("unavailable"),
			expected: ErrorTypeTransient,
		},
		{
			name:     "internal error",
			err:      apierrors.NewInternalError(errors.New("internal")),
			expected: ErrorTypeTransient,
		},
		{
			name:     "forbidden error",
			err:      apierrors.NewForbidden(schema.GroupResource{Group: "", Resource: "pods"}, "test", errors.New("forbidden")),
			expected: ErrorTypeForbidden,
		},
		{
			name:     "unauthorised error",
			err:      apierrors.NewUnauthorized("unauthorised"),
			expected: ErrorTypeForbidden,
		},
		{
			name:     "context deadline exceeded",
			err:      context.DeadlineExceeded,
			expected: ErrorTypeTransient,
		},
		{
			name:     "context canceled",
			err:      context.Canceled,
			expected: ErrorTypeTransient,
		},
		{
			name:     "generic error",
			err:      errors.New("some error"),
			expected: ErrorTypePermanent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ClassifyError(tt.err)
			if got != tt.expected {
				t.Errorf("ClassifyError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name: "transient sync error",
			err: &SyncError{
				Type:      ErrorTypeTransient,
				Retryable: true,
			},
			expected: true,
		},
		{
			name: "conflict sync error",
			err: &SyncError{
				Type:      ErrorTypeConflict,
				Retryable: true,
			},
			expected: true,
		},
		{
			name: "permanent sync error",
			err: &SyncError{
				Type:      ErrorTypePermanent,
				Retryable: false,
			},
			expected: false,
		},
		{
			name:     "raw conflict error",
			err:      apierrors.NewConflict(schema.GroupResource{}, "test", errors.New("conflict")),
			expected: true,
		},
		{
			name:     "raw timeout error",
			err:      apierrors.NewTimeoutError("timeout", 1),
			expected: true,
		},
		{
			name:     "raw not found error",
			err:      apierrors.NewNotFound(schema.GroupResource{}, "test"),
			expected: false,
		},
		{
			name:     "generic error",
			err:      errors.New("some error"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsRetryable(tt.err)
			if got != tt.expected {
				t.Errorf("IsRetryable() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNewSyncError(t *testing.T) {
	cause := errors.New("test error")

	t.Run("create operation", func(t *testing.T) {
		err := NewSyncError("create", "HTTPRoute", "default", "my-route", "toHost", cause)
		if err.Operation != "create" {
			t.Errorf("Operation = %q, want %q", err.Operation, "create")
		}
		if err.Kind != "HTTPRoute" {
			t.Errorf("Kind = %q, want %q", err.Kind, "HTTPRoute")
		}
		if err.Namespace != "default" {
			t.Errorf("Namespace = %q, want %q", err.Namespace, "default")
		}
		if err.Name != "my-route" {
			t.Errorf("Name = %q, want %q", err.Name, "my-route")
		}
		if err.Direction != "toHost" {
			t.Errorf("Direction = %q, want %q", err.Direction, "toHost")
		}
	})

	t.Run("update operation", func(t *testing.T) {
		err := NewSyncError("update", "Gateway", "prod", "main-gw", "fromHost", cause)
		if err.Operation != "update" {
			t.Errorf("Operation = %q, want %q", err.Operation, "update")
		}
	})

	t.Run("delete operation", func(t *testing.T) {
		err := NewSyncError("delete", "Secret", "default", "tls-secret", "toHost", cause)
		if err.Operation != "delete" {
			t.Errorf("Operation = %q, want %q", err.Operation, "delete")
		}
	})

	t.Run("patch operation", func(t *testing.T) {
		err := NewSyncError("patch", "HTTPRoute", "default", "route", "toHost", cause)
		if err.Operation != "patch" {
			t.Errorf("Operation = %q, want %q", err.Operation, "patch")
		}
	})

	t.Run("status-sync operation", func(t *testing.T) {
		err := NewSyncError("status-sync", "HTTPRoute", "default", "route", "toHost", cause)
		if err.Operation != "status-sync" {
			t.Errorf("Operation = %q, want %q", err.Operation, "status-sync")
		}
	})

	t.Run("classifies transient error", func(t *testing.T) {
		transientCause := apierrors.NewTimeoutError("timeout", 5)
		err := NewSyncError("create", "HTTPRoute", "default", "route", "toHost", transientCause)
		if err.Type != ErrorTypeTransient {
			t.Errorf("Type = %v, want %v", err.Type, ErrorTypeTransient)
		}
		if !err.Retryable {
			t.Error("expected Retryable to be true for transient error")
		}
	})

	t.Run("classifies conflict error", func(t *testing.T) {
		conflictCause := apierrors.NewConflict(schema.GroupResource{}, "test", errors.New("conflict"))
		err := NewSyncError("update", "HTTPRoute", "default", "route", "toHost", conflictCause)
		if err.Type != ErrorTypeConflict {
			t.Errorf("Type = %v, want %v", err.Type, ErrorTypeConflict)
		}
		if !err.Retryable {
			t.Error("expected Retryable to be true for conflict error")
		}
	})
}

func TestDefaultRetryConfig(t *testing.T) {
	cfg := DefaultRetryConfig()

	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want %d", cfg.MaxRetries, 3)
	}
	if cfg.InitialDelay != 100*time.Millisecond {
		t.Errorf("InitialDelay = %v, want %v", cfg.InitialDelay, 100*time.Millisecond)
	}
	if cfg.MaxDelay != 5*time.Second {
		t.Errorf("MaxDelay = %v, want %v", cfg.MaxDelay, 5*time.Second)
	}
	if cfg.Multiplier != 2.0 {
		t.Errorf("Multiplier = %v, want %v", cfg.Multiplier, 2.0)
	}
}

func TestCalculateBackoff(t *testing.T) {
	cfg := RetryConfig{
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
	}

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond}, // First attempt: initial delay
		{1, 200 * time.Millisecond}, // Second: 100 * 2
		{2, 400 * time.Millisecond}, // Third: 100 * 4
		{3, 800 * time.Millisecond}, // Fourth: 100 * 8
		{4, 1 * time.Second},        // Fifth: capped at max
		{10, 1 * time.Second},       // Always capped at max
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("attempt_%d", tt.attempt), func(t *testing.T) {
			got := cfg.CalculateBackoff(tt.attempt)
			if got != tt.expected {
				t.Errorf("CalculateBackoff(%d) = %v, want %v", tt.attempt, got, tt.expected)
			}
		})
	}
}

func TestRetryWithBackoff_Success(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:   3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	attempts := 0
	result := RetryWithBackoff(context.Background(), cfg, func() (bool, error) {
		attempts++
		return false, nil // Success on first try
	})

	if !result.Succeeded {
		t.Error("Expected success")
	}
	if result.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", result.Attempts)
	}
	if result.FinalError != nil {
		t.Errorf("FinalError = %v, want nil", result.FinalError)
	}
}

func TestRetryWithBackoff_SuccessAfterRetries(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:   3,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	attempts := 0
	result := RetryWithBackoff(context.Background(), cfg, func() (bool, error) {
		attempts++
		if attempts < 3 {
			return true, errors.New("temporary error")
		}
		return false, nil // Success on third try
	})

	if !result.Succeeded {
		t.Error("Expected success")
	}
	if result.Attempts != 3 {
		t.Errorf("Attempts = %d, want 3", result.Attempts)
	}
}

func TestRetryWithBackoff_ExhaustedRetries(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:   2,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	result := RetryWithBackoff(context.Background(), cfg, func() (bool, error) {
		return true, errors.New("always fails")
	})

	if result.Succeeded {
		t.Error("Expected failure")
	}
	if result.Attempts != 3 { // Initial + 2 retries
		t.Errorf("Attempts = %d, want 3", result.Attempts)
	}
	if result.FinalError == nil {
		t.Error("Expected error")
	}
}

func TestRetryWithBackoff_PermanentFailure(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:   5,
		InitialDelay: 1 * time.Millisecond,
		MaxDelay:     10 * time.Millisecond,
		Multiplier:   2.0,
	}

	result := RetryWithBackoff(context.Background(), cfg, func() (bool, error) {
		return false, errors.New("permanent error") // Don't retry
	})

	if result.Succeeded {
		t.Error("Expected failure")
	}
	if result.Attempts != 1 {
		t.Errorf("Attempts = %d, want 1", result.Attempts)
	}
}

func TestRetryWithBackoff_ContextCanceled(t *testing.T) {
	cfg := RetryConfig{
		MaxRetries:   10,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     1 * time.Second,
		Multiplier:   2.0,
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	result := RetryWithBackoff(ctx, cfg, func() (bool, error) {
		return true, errors.New("should not reach here many times")
	})

	if result.Succeeded {
		t.Error("Expected failure due to context cancellation")
	}
	if !errors.Is(result.FinalError, context.Canceled) {
		t.Errorf("FinalError = %v, want context.Canceled", result.FinalError)
	}
}

func TestRequeueResult(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		wantRequeue bool
		wantAfter   bool
	}{
		{
			name:        "nil error",
			err:         nil,
			wantRequeue: false,
			wantAfter:   false,
		},
		{
			name: "conflict error - near-immediate requeue",
			err: &SyncError{
				Type: ErrorTypeConflict,
			},
			wantRequeue: false,
			wantAfter:   true,
		},
		{
			name: "transient error - delayed requeue",
			err: &SyncError{
				Type: ErrorTypeTransient,
			},
			wantRequeue: false,
			wantAfter:   true,
		},
		{
			name: "not found error - no requeue",
			err: &SyncError{
				Type: ErrorTypeNotFound,
			},
			wantRequeue: false,
			wantAfter:   false,
		},
		{
			name: "permanent error - delayed requeue",
			err: &SyncError{
				Type: ErrorTypePermanent,
			},
			wantRequeue: false,
			wantAfter:   true,
		},
		{
			name: "validation error - delayed requeue",
			err: &SyncError{
				Type: ErrorTypeValidation,
			},
			wantRequeue: false,
			wantAfter:   true,
		},
		{
			name: "forbidden error - delayed requeue",
			err: &SyncError{
				Type: ErrorTypeForbidden,
			},
			wantRequeue: false,
			wantAfter:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := RequeueResult(tt.err)
			// Check for immediate requeue (Requeue=true with no delay)
			// The deprecated Requeue field is still set by controller-runtime for immediate requeues
			immediateRequeue := result.RequeueAfter == 0 && !result.IsZero()
			if tt.wantRequeue && !immediateRequeue {
				t.Errorf("Expected immediate requeue, got result=%+v", result)
			}
			hasAfter := result.RequeueAfter > 0
			if hasAfter != tt.wantAfter {
				t.Errorf("RequeueAfter > 0 = %v, want %v", hasAfter, tt.wantAfter)
			}
		})
	}
}

func TestErrorTypeConstants(t *testing.T) {
	// Ensure error type constants have expected values
	tests := []struct {
		errType  ErrorType
		expected string
	}{
		{ErrorTypeTransient, "transient"},
		{ErrorTypePermanent, "permanent"},
		{ErrorTypeConflict, "conflict"},
		{ErrorTypeNotFound, "not_found"},
		{ErrorTypeValidation, "validation"},
		{ErrorTypeForbidden, "forbidden"},
	}

	for _, tt := range tests {
		if string(tt.errType) != tt.expected {
			t.Errorf("ErrorType = %q, expected %q", tt.errType, tt.expected)
		}
	}
}
