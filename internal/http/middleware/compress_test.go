package middleware

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A template page arrives as many small writes; the first one decides
// nothing but the Content-Type.
func TestCompress_HTMLChunkedWritesGzipped(t *testing.T) {
	h := Compress(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<!DOCTYPE html>"))
		for range 100 {
			_, _ = w.Write([]byte("<p>chunk</p>\n"))
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/ui/unread", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Fatalf("headers: %v", rec.Header())
	}
	zr, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if !strings.HasPrefix(string(got), "<!DOCTYPE html>") || strings.Count(string(got), "<p>chunk</p>") != 100 {
		t.Fatalf("decoded: %d bytes", len(got))
	}
}

func TestCompress_StaticTypesAndSkips(t *testing.T) {
	for _, tc := range []struct {
		ct     string
		status int
		rng    string
		want   bool
	}{
		{"text/css; charset=utf-8", 200, "", true},
		{"text/javascript; charset=utf-8", 200, "", true},
		{"image/svg+xml", 200, "", true},
		{"image/png", 200, "", false},
		{"text/plain; charset=utf-8", 200, "", false},
		{"text/html; charset=utf-8", 304, "", false},
		{"text/css", 200, "bytes=0-10", false},
	} {
		h := Compress(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", tc.ct)
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(strings.Repeat("y", 100)))
		}))
		req := httptest.NewRequest(http.MethodGet, "/ui/static/app.css", nil)
		req.Header.Set("Accept-Encoding", "gzip")
		if tc.rng != "" {
			req.Header.Set("Range", tc.rng)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if got := rec.Header().Get("Content-Encoding") == "gzip"; got != tc.want {
			t.Fatalf("%s status=%d range=%q: gzip=%v want %v", tc.ct, tc.status, tc.rng, got, tc.want)
		}
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

func TestCompress_HandlerCSPReplacesMiddleware(t *testing.T) {
	inner := Compress(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(strings.Repeat("x", 2000)))
	}))
	h := SecurityHeaders(SecurityConfig{})(inner)

	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	csps := rec.Header().Values("Content-Security-Policy")
	if len(csps) != 1 {
		t.Fatalf("want 1 CSP, got %q", csps)
	}
	if !strings.Contains(csps[0], "script-src 'self'") {
		t.Fatalf("handler CSP lost under gzip: %q", csps[0])
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
