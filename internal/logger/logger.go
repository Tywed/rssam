package logger

import (
	"log/slog"
	"os"
	"strings"
)

type Option struct {
	Format string // json|text
	Level  string // debug|info|warn|error
}

func New(level, format string) *slog.Logger {
	opts := Option{
		Format: strings.ToLower(strings.TrimSpace(format)),
		Level:  strings.ToLower(strings.TrimSpace(level)),
	}

	var h slog.Handler
	handlerOpts := &slog.HandlerOptions{Level: parseLevel(opts.Level)}

	switch opts.Format {
	case "text":
		h = slog.NewTextHandler(os.Stdout, handlerOpts)
	default:
		h = slog.NewJSONHandler(os.Stdout, handlerOpts)
	}

	return slog.New(h)
}

func parseLevel(v string) slog.Level {
	switch v {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
