package middleware

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
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
			quiet := r.URL.Path == "/healthz" || r.URL.Path == "/favicon.ico" || strings.HasPrefix(r.URL.Path, "/ui/static/")
			if status >= 400 || (!quiet && dur >= accessLogSlow) {
				log.Info("http access", attrs...)
				return
			}
			log.Debug("http access", attrs...)
		})
	}
}
