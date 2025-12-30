// Package logging provides structured logging, error handling, and event emission for the sync plugin.
package logging

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
)

// ErrorType categorizes sync errors for metrics and handling.
// These types cover all error scenarios in a vCluster sync plugin:
// - API server errors (transient, conflict, not_found)
// - Configuration/data errors (validation, permanent)
// - Authorization errors (forbidden)
type ErrorType string

const (
	// ErrorTypeTransient indicates a temporary error that may succeed on retry.
	// Examples: network timeouts, API server overload, rate limiting
	ErrorTypeTransient ErrorType = "transient"

	// ErrorTypePermanent indicates an error that won't be fixed by retrying.
	// Examples: unknown errors, logic errors, unrecoverable states
	ErrorTypePermanent ErrorType = "permanent"

	// ErrorTypeConflict indicates a resource version conflict (optimistic locking).
	// Should be retried immediately as another controller modified the resource.
	ErrorTypeConflict ErrorType = "conflict"

	// ErrorTypeNotFound indicates the resource was not found.
	// Often expected during deletion - typically don't retry.
	ErrorTypeNotFound ErrorType = "not_found"

	// ErrorTypeValidation indicates invalid configuration or data.
	// Examples: invalid spec, missing required fields, schema violations
	ErrorTypeValidation ErrorType = "validation"

	// ErrorTypeForbidden indicates authorization/permission errors.
	// Examples: RBAC denies access, namespace restrictions
	ErrorTypeForbidden ErrorType = "forbidden"
)

// SyncError provides structured error information for sync operations
type SyncError struct {
	// Type categorizes the error for handling decisions
	Type ErrorType
	// Operation describes what was being attempted (create, update, delete, patch)
	Operation string
	// Kind is the Kubernetes resource kind
	Kind string
	// Name is the resource name
	Name string
	// Namespace is the resource namespace
	Namespace string
	// Direction is the sync direction (toHost, fromHost)
	Direction string
	// Cause is the underlying error
	Cause error
	// Retryable indicates if the operation should be retried
	Retryable bool
}

// Error implements the error interface
func (e *SyncError) Error() string {
	if e.Namespace != "" {
		return fmt.Sprintf("%s %s %s/%s (%s): %v",
			e.Operation, e.Kind, e.Namespace, e.Name, e.Direction, e.Cause)
	}
	return fmt.Sprintf("%s %s %s (%s): %v",
		e.Operation, e.Kind, e.Name, e.Direction, e.Cause)
}

// Unwrap returns the underlying error
func (e *SyncError) Unwrap() error {
	return e.Cause
}

// Is checks if the target error matches this error's type
func (e *SyncError) Is(target error) bool {
	var syncErr *SyncError
	if errors.As(target, &syncErr) {
		return e.Type == syncErr.Type
	}
	return false
}

// NewSyncError creates a new structured sync error.
// It automatically classifies the error type based on the underlying cause
// and determines if it's retryable.
//
// Usage:
//
//	err := logging.NewSyncError("create", "HTTPRoute", "default", "my-route", "toHost", cause)
//	err := logging.NewSyncError("update", "Gateway", "prod", "main-gw", "fromHost", cause)
//	err := logging.NewSyncError("delete", "Secret", "default", "tls-secret", "toHost", cause)
func NewSyncError(operation, kind, namespace, name, direction string, cause error) *SyncError {
	errType := ClassifyError(cause)
	return &SyncError{
		Type:      errType,
		Operation: operation,
		Kind:      kind,
		Name:      name,
		Namespace: namespace,
		Direction: direction,
		Cause:     cause,
		Retryable: errType == ErrorTypeTransient || errType == ErrorTypeConflict,
	}
}

// ClassifyError determines the error type from the underlying error.
// This classification is used to decide retry behavior and requeue timing.
func ClassifyError(err error) ErrorType {
	if err == nil {
		return ErrorTypePermanent
	}

	// Check for Kubernetes API errors (most common in sync operations)
	if apierrors.IsConflict(err) {
		return ErrorTypeConflict
	}
	if apierrors.IsNotFound(err) {
		return ErrorTypeNotFound
	}
	if apierrors.IsInvalid(err) || apierrors.IsBadRequest(err) {
		return ErrorTypeValidation
	}
	if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
		return ErrorTypeForbidden
	}
	if apierrors.IsServerTimeout(err) || apierrors.IsTimeout(err) || apierrors.IsTooManyRequests(err) {
		return ErrorTypeTransient
	}
	if apierrors.IsServiceUnavailable(err) || apierrors.IsInternalError(err) {
		return ErrorTypeTransient
	}

	// Check for network errors (common in distributed systems)
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return ErrorTypeTransient
		}
	}

	// Check for context errors (cancellation, deadline)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return ErrorTypeTransient
	}

	// Default to permanent for unknown errors
	return ErrorTypePermanent
}

// IsRetryable checks if an error should be retried
func IsRetryable(err error) bool {
	var syncErr *SyncError
	if errors.As(err, &syncErr) {
		return syncErr.Retryable
	}

	errType := ClassifyError(err)
	return errType == ErrorTypeTransient || errType == ErrorTypeConflict
}

// RetryConfig configures retry behavior
type RetryConfig struct {
	// MaxRetries is the maximum number of retry attempts (0 = no retries)
	MaxRetries int
	// InitialDelay is the delay before the first retry
	InitialDelay time.Duration
	// MaxDelay is the maximum delay between retries
	MaxDelay time.Duration
	// Multiplier is the factor by which delay increases after each retry
	Multiplier float64
}

// DefaultRetryConfig returns sensible defaults for retry configuration
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		MaxRetries:   3,
		InitialDelay: 100 * time.Millisecond,
		MaxDelay:     5 * time.Second,
		Multiplier:   2.0,
	}
}

// CalculateBackoff calculates the backoff delay for a given attempt
func (c RetryConfig) CalculateBackoff(attempt int) time.Duration {
	if attempt <= 0 {
		return c.InitialDelay
	}

	delay := float64(c.InitialDelay) * math.Pow(c.Multiplier, float64(attempt))
	if delay > float64(c.MaxDelay) {
		delay = float64(c.MaxDelay)
	}

	return time.Duration(delay)
}

// RetryResult wraps the result of a retry operation
type RetryResult struct {
	// Attempts is the number of attempts made
	Attempts int
	// FinalError is the last error encountered (nil if successful)
	FinalError error
	// Succeeded indicates if the operation ultimately succeeded
	Succeeded bool
}

// RetryWithBackoff executes a function with exponential backoff retry
// The function should return (shouldRetry bool, err error)
// - shouldRetry=true, err=nil: unexpected, treated as success
// - shouldRetry=true, err!=nil: retry after backoff
// - shouldRetry=false, err=nil: success, stop
// - shouldRetry=false, err!=nil: permanent failure, stop
func RetryWithBackoff(ctx context.Context, cfg RetryConfig, fn func() (bool, error)) RetryResult {
	result := RetryResult{Attempts: 0}

	for attempt := 0; attempt <= cfg.MaxRetries; attempt++ {
		result.Attempts = attempt + 1

		// Check context before attempting
		if ctx.Err() != nil {
			result.FinalError = ctx.Err()
			return result
		}

		shouldRetry, err := fn()

		if err == nil {
			result.Succeeded = true
			return result
		}

		result.FinalError = err

		// Don't retry if function says not to or if this was the last attempt
		if !shouldRetry || attempt >= cfg.MaxRetries {
			return result
		}

		// Calculate backoff and wait
		delay := cfg.CalculateBackoff(attempt)

		select {
		case <-ctx.Done():
			result.FinalError = ctx.Err()
			return result
		case <-time.After(delay):
			// Continue to next attempt
		}
	}

	return result
}

// RequeueResult returns an appropriate controller-runtime Result based on error type.
// This determines how quickly the controller will retry after an error.
func RequeueResult(err error) ctrl.Result {
	if err == nil {
		return ctrl.Result{}
	}

	var syncErr *SyncError
	if errors.As(err, &syncErr) {
		return requeueForErrorType(syncErr.Type)
	}

	return requeueForErrorType(ClassifyError(err))
}

// requeueForErrorType returns the appropriate requeue result for an error type
func requeueForErrorType(errType ErrorType) ctrl.Result {
	switch errType {
	case ErrorTypeConflict:
		// Immediate requeue for conflicts - another update happened
		return ctrl.Result{Requeue: true}
	case ErrorTypeTransient:
		// Requeue with short backoff for transient errors
		return ctrl.Result{RequeueAfter: 5 * time.Second}
	case ErrorTypeNotFound:
		// No requeue for not found - resource may have been deleted
		return ctrl.Result{}
	case ErrorTypeValidation:
		// Longer backoff for validation - unlikely to change quickly
		return ctrl.Result{RequeueAfter: 1 * time.Minute}
	case ErrorTypeForbidden:
		// Long backoff for permission errors - requires manual intervention
		return ctrl.Result{RequeueAfter: 5 * time.Minute}
	default:
		// Default backoff for permanent/unknown errors
		return ctrl.Result{RequeueAfter: 30 * time.Second}
	}
}
