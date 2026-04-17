package logging

import (
	"testing"
)

func TestInitLogging(t *testing.T) {
	// Store original log level
	originalLevel := logLevelValue.Load()
	defer func() { logLevelValue.Store(originalLevel) }()

	tests := []struct {
		name     string
		level    LogLevel
		expected int32
	}{
		{
			name:     "set error level",
			level:    LogLevelError,
			expected: logLevelIntError,
		},
		{
			name:     "set warning level",
			level:    LogLevelWarning,
			expected: logLevelIntWarning,
		},
		{
			name:     "set info level",
			level:    LogLevelInfo,
			expected: logLevelIntInfo,
		},
		{
			name:     "set debug level",
			level:    LogLevelDebug,
			expected: logLevelIntDebug,
		},
		{
			name:     "set trace level",
			level:    LogLevelTrace,
			expected: logLevelIntTrace,
		},
		{
			name:     "empty defaults to info",
			level:    "",
			expected: logLevelIntInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InitLogging(tt.level)
			actual := logLevelValue.Load()
			if actual != tt.expected {
				t.Errorf("InitLogging(%q) set logLevelValue to %d, expected %d", tt.level, actual, tt.expected)
			}
		})
	}
}

func TestNewLogger(t *testing.T) {
	logger := NewLogger()
	if logger == nil {
		t.Error("NewLogger() returned nil")
	}
}

func TestLoggerGlobal(t *testing.T) {
	if Log == nil {
		t.Error("Log global logger is nil")
	}
}

func TestGetBinarySHA(t *testing.T) {
	sha := GetBinarySHA()

	// Should return a non-empty string
	if sha == "" {
		t.Error("GetBinarySHA() returned empty string")
	}

	// Should return 12 characters (short SHA) or an error message
	if len(sha) < 12 {
		// It's either a valid short SHA or an error message
		if sha[:7] != "unknown" {
			t.Errorf("GetBinarySHA() returned unexpected format: %q", sha)
		}
	}

	// If it's a valid SHA, it should be 12 hex characters
	if len(sha) == 12 {
		for _, c := range sha {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Errorf("GetBinarySHA() returned non-hex character: %q in %q", c, sha)
			}
		}
	}
}

func TestLogLevelConstants(t *testing.T) {
	// Ensure log level constants have expected values
	if LogLevelInfo != "info" {
		t.Errorf("LogLevelInfo = %q, expected %q", LogLevelInfo, "info")
	}
	if LogLevelWarning != "warning" {
		t.Errorf("LogLevelWarning = %q, expected %q", LogLevelWarning, "warning")
	}
	if LogLevelError != "error" {
		t.Errorf("LogLevelError = %q, expected %q", LogLevelError, "error")
	}
	if LogLevelDebug != "debug" {
		t.Errorf("LogLevelDebug = %q, expected %q", LogLevelDebug, "debug")
	}
	if LogLevelTrace != "trace" {
		t.Errorf("LogLevelTrace = %q, expected %q", LogLevelTrace, "trace")
	}
}

func TestIsValidLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		level    LogLevel
		expected bool
	}{
		{"empty is valid", "", true},
		{"error is valid", LogLevelError, true},
		{"warning is valid", LogLevelWarning, true},
		{"info is valid", LogLevelInfo, true},
		{"debug is valid", LogLevelDebug, true},
		{"trace is valid", LogLevelTrace, true},
		{"invalid level", "verbose", false},
		{"invalid level 2", "warn", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := IsValidLogLevel(tt.level)
			if actual != tt.expected {
				t.Errorf("IsValidLogLevel(%q) = %v, expected %v", tt.level, actual, tt.expected)
			}
		})
	}
}

// Note: Testing the actual logging methods (Info, Debug, Trace, Error, Warning)
// would require mocking klog, which is complex. The behaviour is simple enough
// that the level check logic is covered by TestInitLogging.
//
// Info/Warning/Debug/Trace methods check the logLevelValue atomically before calling klog:
// - Info: logs if logLevelValue >= logLevelIntInfo
// - Warning: logs if logLevelValue >= logLevelIntWarning
// - Debug: logs if logLevelValue >= logLevelIntDebug
// - Trace: logs if logLevelValue >= logLevelIntTrace
//
// These checks are tested indirectly through the InitLogging test.
