package middleware

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
)

// Compress gzip-encodes text responses (HTML, JSON, CSS, JavaScript, SVG)
// when the client accepts gzip. The decision is made on Content-Type at the
// first Write, never on the size of that write: html/template emits a page
// as dozens of small writes, and a "first chunk >= 512 B" rule left every
// UI page uncompressed (62 KB /ui/unread, 146 KB admin feeds for 200 feeds;
// gzip makes them 4.8 KB and 6.5 KB).
func Compress(enabled bool) func(http.Handler) http.Handler {
	if !enabled {
		return func(next http.Handler) http.Handler { return next }
	}
	var pool sync.Pool
	pool.New = func() any {
		return gzip.NewWriter(io.Discard)
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
				next.ServeHTTP(w, r)
				return
			}
			if isWebSocketUpgrade(r) || r.Header.Get("Range") != "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Add("Vary", "Accept-Encoding")
			gzw := &gzipResponseWriter{
				ResponseWriter: w,
				gz:             pool.Get().(*gzip.Writer),
				pool:           &pool,
			}
			defer gzw.close()
			next.ServeHTTP(gzw, r)
		})
	}
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz     *gzip.Writer
	pool   *sync.Pool
	status int
	wrote  bool
}

// Header is the outer writer's map: delayed WriteHeader still lets the
// handler overwrite middleware headers (CSP). A private map copied with Add
// on flush duplicated Content-Security-Policy and browsers AND the policies.
func (w *gzipResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.wrote = true
		h := w.ResponseWriter.Header()
		ct := h.Get("Content-Type")
		if w.status == 0 {
			w.status = http.StatusOK
		}
		if !shouldGzip(ct, w.status) {
			if w.gz != nil {
				w.pool.Put(w.gz)
				w.gz = nil
			}
			w.ResponseWriter.WriteHeader(w.status)
			return w.ResponseWriter.Write(b)
		}
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		w.gz.Reset(w.ResponseWriter)
		w.ResponseWriter.WriteHeader(w.status)
	}
	if w.gz != nil {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipResponseWriter) close() {
	if !w.wrote {
		w.flushHeaders()
	}
	if w.gz != nil {
		_ = w.gz.Close()
		w.pool.Put(w.gz)
		w.gz = nil
	}
}

func (w *gzipResponseWriter) flushHeaders() {
	if w.wrote {
		return
	}
	w.wrote = true
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(status)
}

var gzipTypes = [...]string{"text/html", "application/json", "text/css", "text/javascript", "application/javascript", "image/svg+xml"}

func shouldGzip(contentType string, status int) bool {
	if status >= 300 || status == http.StatusNoContent {
		return false
	}
	for _, t := range gzipTypes {
		if strings.HasPrefix(contentType, t) {
			return true
		}
	}
	return false
}

func isWebSocketUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func (w *gzipResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, fmt.Errorf("gzipResponseWriter: ResponseWriter does not implement http.Hijacker")
	}
	return hj.Hijack()
}

// Unwrap supports http.ResponseController.
func (w *gzipResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *gzipResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
