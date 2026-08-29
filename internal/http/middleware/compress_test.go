package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompress_HTMLSmallNotGzipped(t *testing.T) {
	body := strings.Repeat("x", 200)
	h := Compress(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))

	req := httptest.NewRequest(http.MethodGet, "/ui/login", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("small html must not be gzip-encoded")
	}
	if got := rec.Body.String(); got != body {
		t.Fatalf("body len=%d want %d", len(got), len(body))
	}
}

func TestCompress_HTMLGzippedWhenLarge(t *testing.T) {
	body := strings.Repeat("x", 2000)
	h := Compress(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))

	req := httptest.NewRequest(http.MethodGet, "/ui/unread", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("expected gzip content-encoding for large html")
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	got, err := io.ReadAll(zr)
	_ = zr.Close()
	if err != nil {
		t.Fatalf("read gzip: %v", err)
	}
	if string(got) != body {
		t.Fatalf("decoded len=%d want %d", len(got), len(body))
	}
}

func TestCompress_RedirectWithoutBody(t *testing.T) {
	h := Compress(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/unread", http.StatusFound)
	}))

	req := httptest.NewRequest(http.MethodPost, "/ui/login", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status=%d want %d", rec.Code, http.StatusFound)
	}
	if got := rec.Header().Get("Location"); got != "/ui/unread" {
		t.Fatalf("location=%q", got)
	}
}

func TestCompress_WebSocketUpgradeBypassesGzip(t *testing.T) {
	var gotGzipWrapper bool
	h := Compress(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, gotGzipWrapper = w.(*gzipResponseWriter)
		w.WriteHeader(http.StatusSwitchingProtocols)
	}))

	req := httptest.NewRequest(http.MethodGet, "/ws/v1", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if gotGzipWrapper {
		t.Fatal("websocket upgrade must bypass gzip wrapper")
	}
	if rec.Code != http.StatusSwitchingProtocols {
		t.Fatalf("status=%d want %d", rec.Code, http.StatusSwitchingProtocols)
	}
}

func TestCompress_JSONGzippedWhenAccepted(t *testing.T) {
	h := Compress(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("expected gzip content-encoding for json")
	}
}
