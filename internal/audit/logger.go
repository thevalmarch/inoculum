package audit

import (
	"log/slog"
	"os"
	"sync"
)

var (
	mu     sync.RWMutex
	logger *slog.Logger
	file   *os.File
)

// InitLogger initializes the global JSON audit logger to a file.
func InitLogger(logFile string) error {
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		_ = file.Close()
		file = nil
		logger = nil
	}
	if logFile == "" {
		return nil
	}

	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}

	handler := slog.NewJSONHandler(f, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	file = f
	logger = slog.New(handler)
	return nil
}

// Close flushes and closes an explicitly enabled audit log.
func Close() error {
	mu.Lock()
	defer mu.Unlock()
	logger = nil
	if file == nil {
		return nil
	}
	err := file.Close()
	file = nil
	return err
}

// LogEvent logs a structured JSON audit event.
func LogEvent(eventType, sourceIP, status, msg string, extra map[string]any) {
	mu.RLock()
	defer mu.RUnlock()
	if logger == nil {
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

	logger.Info(msg, args...)
}
