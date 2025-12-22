package zerolog

import (
	"github.com/mihaimyh/goquota/pkg/goquota"
	"github.com/rs/zerolog"
)

// Logger implements goquota.Logger using zerolog.
type Logger struct {
	logger zerolog.Logger
}

// NewLogger creates a new zerolog logger adapter.
func NewLogger(logger zerolog.Logger) *Logger {
	return &Logger{logger: logger}
}

func (l *Logger) Debug(msg string, fields ...goquota.Field) {
	l.log(l.logger.Debug(), msg, fields)
}

func (l *Logger) Info(msg string, fields ...goquota.Field) {
	l.log(l.logger.Info(), msg, fields)
}

func (l *Logger) Warn(msg string, fields ...goquota.Field) {
	l.log(l.logger.Warn(), msg, fields)
}

func (l *Logger) Error(msg string, fields ...goquota.Field) {
	l.log(l.logger.Error(), msg, fields)
}

func (l *Logger) log(event *zerolog.Event, msg string, fields []goquota.Field) {
	if event == nil {
		return
	}
	for _, f := range fields {
		event = event.Interface(f.Key, f.Value)
	}
	event.Msg(msg)
}
