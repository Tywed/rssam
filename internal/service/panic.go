package service

import (
	"fmt"
	"log/slog"
	"runtime/debug"

	"rssam/internal/metrics"
)

// recoveredPanic logs a panic recovered in a poll with its stack and counts
// it under rssam_panics_total{where="poll"}.
func recoveredPanic(log *slog.Logger, v any, attrs ...any) error {
	metrics.PanicsTotal.WithLabelValues("poll").Inc()
	attrs = append([]any{"panic", fmt.Sprint(v), "stack", string(debug.Stack())}, attrs...)
	log.Error("panic recovered in poll", attrs...)
	return fmt.Errorf("panic: %v", v)
}
