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

// Compress optionally gzip-encodes JSON and large HTML responses when the client accepts gzip.
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
			if isWebSocketUpgrade(r) {
				next.ServeHTTP(w, r)
				return
			}
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
	header http.Header
	status int
	wrote  bool
}

func (w *gzipResponseWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	w.status = status
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.wrote = true
		h := w.ResponseWriter.Header()
		for k, vv := range w.header {
			for _, v := range vv {
				h.Add(k, v)
			}
		}
		ct := h.Get("Content-Type")
		if w.status == 0 {
			w.status = http.StatusOK
		}
		if !shouldGzip(ct, w.status, len(b)) {
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
	h := w.ResponseWriter.Header()
	for k, vv := range w.header {
		for _, v := range vv {
			h.Add(k, v)
		}
	}
	status := w.status
	if status == 0 {
		status = http.StatusOK
	}
	w.ResponseWriter.WriteHeader(status)
}

const gzipHTMLMinBytes = 512

func shouldGzip(contentType string, status, firstLen int) bool {
	if status >= 300 {
		return false
	}
	if strings.Contains(contentType, "application/json") {
		return true
	}
	if strings.Contains(contentType, "text/html") && firstLen >= gzipHTMLMinBytes {
		return true
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

func (w *gzipResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
