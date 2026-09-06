package middleware

import (
	"bufio"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"rssam/internal/requestid"
)

type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

// Hijack is required for WebSocket upgrades: x/net/websocket type-asserts
// http.Hijacker on the ResponseWriter without checking.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("statusWriter: underlying ResponseWriter does not implement http.Hijacker")
	}
	w.status = http.StatusSwitchingProtocols
	return hj.Hijack()
}

// Flush forwards streaming flushes (SSE, long polls) to the real writer.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap supports http.ResponseController.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

const accessLogSlow = 200 * time.Millisecond

// AccessLog logs structured request metadata after the handler completes.
func AccessLog(log *slog.Logger) func(http.Handler) http.Handler {
	if log == nil {
		log = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			status := sw.status
			if status == 0 {
				status = http.StatusOK
			}
			dur := time.Since(start)
			attrs := []any{
				"method", r.Method,
				"path", r.URL.Path,
				"status", status,
				"duration_ms", dur.Milliseconds(),
				"bytes", sw.bytes,
				"remote_addr", ClientIP(r),
				"user_agent", r.UserAgent(),
			}
			if id := requestid.FromContext(r.Context()); id != "" {
				attrs = append(attrs, "request_id", id)
			}
			quiet := r.URL.Path == "/healthz" || r.URL.Path == "/favicon.ico" || strings.HasPrefix(r.URL.Path, "/ui/static/")
			if status >= 400 || (!quiet && dur >= accessLogSlow) {
				log.Info("http access", attrs...)
				return
			}
			log.Debug("http access", attrs...)
		})
	}
}
