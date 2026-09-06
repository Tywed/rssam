package logger

import (
	"context"
	"log/slog"

	"rssam/internal/requestid"
)

// requestIDHandler adds request_id=<id> to every record logged with a
// context that carries one (log.ErrorContext(r.Context(), ...)). Records
// logged without a request context are passed through unchanged.
type requestIDHandler struct{ slog.Handler }

func (h requestIDHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := requestid.FromContext(ctx); id != "" {
		r.AddAttrs(slog.String("request_id", id))
	}
	return h.Handler.Handle(ctx, r)
}

func (h requestIDHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return requestIDHandler{h.Handler.WithAttrs(attrs)}
}

func (h requestIDHandler) WithGroup(name string) slog.Handler {
	return requestIDHandler{h.Handler.WithGroup(name)}
}
