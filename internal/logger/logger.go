package logger

import (
	"io"
	"log/slog"
	"os"
	"strings"
)

type Option struct {
	Format string // json|text
	Level  string // debug|info|warn|error
}

func New(level, format string) *slog.Logger {
	return NewWithWriter(level, format, os.Stdout)
}

// NewWithWriter is New with an explicit destination (tests capture output).
func NewWithWriter(level, format string, w io.Writer) *slog.Logger {
	opts := Option{
		Format: strings.ToLower(strings.TrimSpace(format)),
		Level:  strings.ToLower(strings.TrimSpace(level)),
	}

	var h slog.Handler
	handlerOpts := &slog.HandlerOptions{Level: parseLevel(opts.Level)}

	switch opts.Format {
	case "text":
		h = slog.NewTextHandler(w, handlerOpts)
	default:
		h = slog.NewJSONHandler(w, handlerOpts)
	}

	return slog.New(requestIDHandler{h})
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
