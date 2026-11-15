package worker

import (
	"context"
	"fmt"
	"log/slog"
	"runtime/debug"
	"time"

	"rssam/internal/metrics"
)

// loopRestartDelay keeps a loop that panics on every pass from spinning.
const loopRestartDelay = time.Second

// recovered logs a recovered panic with its stack and counts it.
func recovered(log *slog.Logger, where string, v any, attrs ...any) {
	metrics.PanicsTotal.WithLabelValues(where).Inc()
	attrs = append([]any{"panic", fmt.Sprint(v), "stack", string(debug.Stack())}, attrs...)
	log.Error("panic recovered in "+where, attrs...)
}

// runLoop runs loop until it returns on its own (ctx done); a panic inside
// is logged and the loop started again — one bug in one pass must not
// stop polling, deliveries or cleanup for good.
func runLoop(ctx context.Context, log *slog.Logger, where string, loop func()) {
	for {
		exited := func() (exited bool) {
			defer func() {
				if v := recover(); v != nil {
					recovered(log, where, v)
				}
			}()
			loop()
			return true
		}()
		if exited || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(loopRestartDelay):
		}
	}
}

// statusContext is the context for writing the outcome of a finished job:
// the job's own ctx while it lives, else a short grace so that a message
// already sent gets its row even when the drain budget ran out meanwhile.
func statusContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), statusWriteGrace)
}

const statusWriteGrace = 5 * time.Second
