package goquota

// Field represents a structured log field.
type Field struct {
	Key   string
	Value interface{}
}

// Logger defines the interface for structured logging.
type Logger interface {
	// Debug logs a debug message with fields.
	Debug(msg string, fields ...Field)

	// Info logs an info message with fields.
	Info(msg string, fields ...Field)

	// Warn logs a warning message with fields.
	Warn(msg string, fields ...Field)

	// Error logs an error message with fields.
	Error(msg string, fields ...Field)
}

// NoopLogger is a no-op implementation of the Logger interface.
type NoopLogger struct{}

func (n *NoopLogger) Debug(msg string, fields ...Field) {}
func (n *NoopLogger) Info(msg string, fields ...Field)  {}
func (n *NoopLogger) Warn(msg string, fields ...Field)  {}
func (n *NoopLogger) Error(msg string, fields ...Field) {}
