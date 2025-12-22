package zerolog

import (
	"bytes"
	"testing"

	"github.com/rs/zerolog"

	"github.com/mihaimyh/goquota/pkg/goquota"
)

// Phase 8.1: Logger Tests

func TestZerologLogger_NewLogger(t *testing.T) {
	output := bytes.Buffer{}
	zlog := zerolog.New(&output)
	logger := NewLogger(&zlog)

	if logger == nil {
		t.Fatal("NewLogger returned nil")
	}
}

func TestZerologLogger_Debug(t *testing.T) {
	output := bytes.Buffer{}
	zlog := zerolog.New(&output)
	logger := NewLogger(&zlog)

	logger.Debug("test debug message", goquota.Field{Key: "key", Value: "value"})

	// Verify log was written
	if output.Len() == 0 {
		t.Error("Expected debug log to be written")
	}
}

func TestZerologLogger_Info(t *testing.T) {
	output := bytes.Buffer{}
	zlog := zerolog.New(&output)
	logger := NewLogger(&zlog)

	logger.Info("test info message", goquota.Field{Key: "key", Value: "value"})

	// Verify log was written
	if output.Len() == 0 {
		t.Error("Expected info log to be written")
	}
}

func TestZerologLogger_Warn(t *testing.T) {
	output := bytes.Buffer{}
	zlog := zerolog.New(&output)
	logger := NewLogger(&zlog)

	logger.Warn("test warn message", goquota.Field{Key: "key", Value: "value"})

	// Verify log was written
	if output.Len() == 0 {
		t.Error("Expected warn log to be written")
	}
}

func TestZerologLogger_Error(t *testing.T) {
	output := bytes.Buffer{}
	zlog := zerolog.New(&output)
	logger := NewLogger(&zlog)

	logger.Error("test error message", goquota.Field{Key: "key", Value: "value"})

	// Verify log was written
	if output.Len() == 0 {
		t.Error("Expected error log to be written")
	}
}

func TestZerologLogger_LogLevelFiltering(t *testing.T) {
	output := bytes.Buffer{}
	zlog := zerolog.New(&output).Level(zerolog.WarnLevel)
	logger := NewLogger(&zlog)

	// Debug and Info should be filtered out
	logger.Debug("debug message")
	logger.Info("info message")

	if output.Len() != 0 {
		t.Error("Expected debug and info to be filtered out")
	}

	// Warn and Error should be logged
	logger.Warn("warn message")
	logger.Error("error message")

	if output.Len() == 0 {
		t.Error("Expected warn and error to be logged")
	}
}

func TestZerologLogger_MultipleFields(t *testing.T) {
	output := bytes.Buffer{}
	zlog := zerolog.New(&output)
	logger := NewLogger(&zlog)

	logger.Info("test message",
		goquota.Field{Key: "key1", Value: "value1"},
		goquota.Field{Key: "key2", Value: "value2"},
		goquota.Field{Key: "key3", Value: 123},
	)

	// Verify log was written
	if output.Len() == 0 {
		t.Error("Expected log with multiple fields to be written")
	}
}
