package logger

import (
	"log/slog"
	"os"
)

func Init(serviceName string) {
	hostname, _ := os.Hostname()

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				a.Key = "timestamp"
			}
			if a.Key == slog.MessageKey {
				a.Key = "message"
			}
			return a
		},
	})

	logger := slog.New(handler).With(
		slog.String("service", serviceName),
		slog.String("hostname", hostname),
	)

	slog.SetDefault(logger)
}

func LogError(action string, msg string, err error, reqID string) {
	attrs := []any{
		slog.String("action", action),
	}
	if reqID != "" {
		attrs = append(attrs, slog.String("request_id", reqID))
	}
	
	errorObj := slog.Group("error",
		slog.String("msg", err.Error()),
		slog.String("stack", "stack trace placeholder"), 
	)
	attrs = append(attrs, errorObj)

	slog.Error(msg, attrs...)
}

func LogInfo(action string, msg string, reqID string, args ...any) {
	attrs := []any{
		slog.String("action", action),
	}
	if reqID != "" {
		attrs = append(attrs, slog.String("request_id", reqID))
	}
	attrs = append(attrs, args...)
	
	slog.Info(msg, attrs...)
}