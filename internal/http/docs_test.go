package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"rssam/internal/version"
)

// The served spec reports the binary's version, never the repository
// placeholder, and the placeholder is the only version the repository holds
// (otherwise the substitution would silently stop).
func TestOpenAPI_VersionIsTheBinaryVersion(t *testing.T) {
	if !strings.Contains(string(openAPISpec), openAPIVersionPlaceholder) {
		t.Fatalf("openapi.json must carry %s in info.version", openAPIVersionPlaceholder)
	}
	spec, _ := renderedOpenAPISpec()
	var doc struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := json.Unmarshal(spec, &doc); err != nil {
		t.Fatal(err)
	}
	want := version.Version
	if want == "" {
		want = "dev"
	}
	if doc.Info.Version != want {
		t.Fatalf("info.version=%q, want %q", doc.Info.Version, want)
	}
	if strings.Contains(string(spec), openAPIVersionPlaceholder) {
		t.Fatal("placeholder leaked into the served document")
	}
}

func TestOpenAPI_ETagRoundTrip(t *testing.T) {
	s := New(Dependencies{})
	h := s.Handler()
	for _, path := range []string{"/openapi.json", "/docs"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		etag := rec.Header().Get("ETag")
		if !strings.HasPrefix(etag, `W/"`) {
			t.Fatalf("%s: etag %q", path, etag)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("If-None-Match", etag)
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
			t.Fatalf("%s: revalidation got %d with %d bytes", path, rec.Code, rec.Body.Len())
		}
	}
}

// /docs is public, self-contained and locked down: one inline script and
// one inline style whose CSP hashes match the served bytes, no external
// URLs besides the spec it renders, no session/auth involved.
func TestDocs_PublicSelfContainedCSP(t *testing.T) {
	s := New(Dependencies{})
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/docs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type %q", ct)
	}
	body := rec.Body.String()
	if strings.Count(body, "<script>") != 1 || strings.Count(body, "<style>") != 1 {
		t.Fatalf("docs.html must have exactly one <script> and one <style> block")
	}
	if strings.Contains(body, "<script src") || strings.Contains(body, "<link ") || strings.Contains(body, "@import") {
		t.Fatal("docs.html must not load external resources")
	}
	for _, m := range regexp.MustCompile(`(?:src|href|url)\s*[=(]\s*["']?(?://|https?:)`).FindAllString(body, -1) {
		t.Errorf("external reference in docs.html: %s", m)
	}
	csp := rec.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'none'", "script-src 'sha256-" + docsScriptHash + "'", "style-src 'sha256-" + docsStyleHash + "'", "connect-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP %q lacks %q", csp, want)
		}
	}
	if docsScriptHash == "" || docsStyleHash == "" || docsScriptHash == docsStyleHash {
		t.Fatalf("hashes script=%q style=%q", docsScriptHash, docsStyleHash)
	}
	if rec.Header().Get("X-Robots-Tag") != "noindex" {
		t.Fatal("docs must ask crawlers to stay away")
	}
	if strings.Contains(body, "/ui/") || strings.Contains(body, "session") {
		t.Fatal("docs must not depend on the UI or a session")
	}
}

func TestDocs_MethodNotAllowed(t *testing.T) {
	s := New(Dependencies{})
	for _, path := range []string{"/docs", "/openapi.json"} {
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("POST %s: %d", path, rec.Code)
		}
	}
}
