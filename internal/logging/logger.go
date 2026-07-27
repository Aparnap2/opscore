package logging

import (
	"log/slog"
	"os"
)

var DefaultLogger *Logger

func init() {
	DefaultLogger = NewLogger()
}

type Logger struct {
	*slog.Logger
}

func NewLogger() *Logger {
	return &Logger{
		Logger: slog.New(slog.NewJSONHandler(os.Stdout, nil)),
	}
}

func Info(msg string, args ...any) {
	DefaultLogger.Info(msg, args...)
}

func Error(msg string, args ...any) {
	DefaultLogger.Error(msg, args...)
}

func Warn(msg string, args ...any) {
	DefaultLogger.Warn(msg, args...)
}

func Debug(msg string, args ...any) {
	DefaultLogger.Debug(msg, args...)
}
