package audit

import (
	"log/slog"
	"os"
)

var Logger *slog.Logger

// InitLogger initializes the global JSON audit logger to a file.
func InitLogger(logFile string) error {
	if logFile == "" {
		return nil // No auditing if no file specified
	}

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}

	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	Logger = slog.New(handler)
	return nil
}

// LogEvent logs a structured JSON audit event.
func LogEvent(eventType, sourceIP, status, msg string, extra map[string]any) {
	if Logger == nil {
		return
	}

	args := []any{
		"event_type", eventType,
		"source_ip", sourceIP,
		"status", status,
	}

	for k, v := range extra {
		args = append(args, k, v)
	}

	Logger.Info(msg, args...)
}
