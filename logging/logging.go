package logging

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sync/atomic"

	"k8s.io/klog/v2"
)

// LogLevel defines the logging verbosity
type LogLevel string

const (
	// LogLevelError logs only errors (and fatal)
	LogLevelError LogLevel = "error"
	// LogLevelWarning logs warnings and errors
	LogLevelWarning LogLevel = "warning"
	// LogLevelInfo is the default log level
	LogLevelInfo LogLevel = "info"
	// LogLevelDebug enables verbose debug logging
	LogLevelDebug LogLevel = "debug"
	// LogLevelTrace enables very verbose trace logging
	LogLevelTrace LogLevel = "trace"
)

// logLevelInt maps LogLevel to int for atomic operations
const (
	logLevelIntError   int32 = 0
	logLevelIntWarning int32 = 1
	logLevelIntInfo    int32 = 2
	logLevelIntDebug   int32 = 3
	logLevelIntTrace   int32 = 4
)

// Logger provides structured logging helpers
// vCluster automatically adds "component": "/plugins/generic-sync" to all plugin logs
// Use this component path for filtering: kubectl logs <pod> | grep "/plugins/generic-sync"
//
// klog only supports INFO, WARNING, ERROR, FATAL severities.
// DEBUG is implemented via our own level check since vCluster resets klog verbosity.
// We prefix debug messages with [DEBUG] to make them visually distinct.
type Logger struct{}

// logLevelValue stores the configured log level atomically (set by InitLogging)
// Uses atomic operations to avoid race conditions when logging from multiple goroutines
var logLevelValue atomic.Int32

func init() {
	logLevelValue.Store(logLevelIntInfo)
}

// NewLogger creates a new logger instance
func NewLogger() *Logger {
	return &Logger{}
}

// Info logs at INFO severity when log_level is info or higher
func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	level := logLevelValue.Load()
	if level >= logLevelIntInfo {
		klog.InfoS(msg, keysAndValues...)
	}
}

// Debug logs at debug level when log_level is debug or trace
func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	level := logLevelValue.Load()
	if level >= logLevelIntDebug {
		klog.InfoS("[DEBUG] "+msg, keysAndValues...)
	}
}

// Trace logs at trace level when log_level is trace
func (l *Logger) Trace(msg string, keysAndValues ...interface{}) {
	level := logLevelValue.Load()
	if level >= logLevelIntTrace {
		klog.InfoS("[TRACE] "+msg, keysAndValues...)
	}
}

// Error logs at ERROR severity when log_level is error or higher
func (l *Logger) Error(err error, msg string, keysAndValues ...interface{}) {
	level := logLevelValue.Load()
	if level >= logLevelIntError {
		klog.ErrorS(err, msg, keysAndValues...)
	}
}

// Warning logs messages that need attention but are not errors.
// Visible when log_level is warning or higher.
//
// NOTE: klog does not provide a WarningS function for structured logging.
// Only InfoS and ErrorS support structured key-value pairs.
// See: https://github.com/kubernetes/klog/issues/248
//
// We use klog.InfoS with a [WARNING] prefix to enable structured logging while
// providing visual distinction. Log aggregation systems filtering by severity
// will see these as INFO. If true WARNING severity is required, use klog.Warning
// directly (without structured fields).
func (l *Logger) Warning(msg string, keysAndValues ...interface{}) {
	level := logLevelValue.Load()
	if level >= logLevelIntWarning {
		klog.InfoS("[WARNING] "+msg, keysAndValues...)
	}
}

// Fatal logs at ERROR severity and exits
func (l *Logger) Fatal(err error, msg string, keysAndValues ...interface{}) {
	klog.ErrorS(err, msg, keysAndValues...)
	klog.FlushAndExit(klog.ExitFlushTimeout, 1)
}

// InitLogging configures log level based on config
// Thread-safe: can be called from any goroutine
func InitLogging(level LogLevel) {
	var intLevel int32
	switch level {
	case LogLevelError:
		intLevel = logLevelIntError
	case LogLevelWarning:
		intLevel = logLevelIntWarning
	case LogLevelDebug:
		intLevel = logLevelIntDebug
	case LogLevelTrace:
		intLevel = logLevelIntTrace
	default:
		intLevel = logLevelIntInfo
	}
	logLevelValue.Store(intLevel)
}

// IsValidLogLevel checks if the given log level is valid
func IsValidLogLevel(level LogLevel) bool {
	switch level {
	case LogLevelError, LogLevelWarning, LogLevelInfo, LogLevelDebug, LogLevelTrace, "":
		return true
	default:
		return false
	}
}

// GetBinarySHA returns the SHA256 hash of the running binary
// This helps verify which version of the plugin is actually running
func GetBinarySHA() string {
	execPath, err := os.Executable()
	if err != nil {
		return "unknown:error-getting-path"
	}

	file, err := os.Open(execPath)
	if err != nil {
		return "unknown:error-opening-binary"
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "unknown:error-hashing"
	}

	// Return first 12 chars of SHA (like git short hash)
	fullHash := hex.EncodeToString(hash.Sum(nil))
	return fullHash[:12]
}

// Log is the global logger instance for the plugin
var Log = NewLogger()

// IsTraceEnabled returns true if trace logging is enabled
// Use this to avoid expensive object serialization when trace is disabled
func IsTraceEnabled() bool {
	return logLevelValue.Load() >= logLevelIntTrace
}

// IsDebugEnabled returns true if debug logging is enabled
func IsDebugEnabled() bool {
	return logLevelValue.Load() >= logLevelIntDebug
}
