package httpserver

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"sync"

	"rssam/internal/version"
)

//go:embed openapi.json
var openAPISpec []byte

//go:embed docs.html
var docsPage []byte

// openAPIVersionPlaceholder is what openapi.json carries in the repository;
// the served document reports the running binary's version instead, so the
// spec a client downloads always matches the server it talks to.
const openAPIVersionPlaceholder = `"version": "0.0.0-dev"`

var (
	openAPIOnce    sync.Once
	openAPIServed  []byte
	openAPIETag    string
	docsETag       string
	docsScriptHash string
	docsStyleHash  string
)

func renderedOpenAPISpec() ([]byte, string) {
	openAPIOnce.Do(func() {
		v := version.Version
		if v == "" {
			v = "dev"
		}
		openAPIServed = bytes.Replace(openAPISpec, []byte(openAPIVersionPlaceholder), []byte(`"version": `+strconv.Quote(v)), 1)
		openAPIETag = weakETag(openAPIServed)
		docsETag = weakETag(docsPage)
		docsScriptHash = inlineHash(docsPage, "<script>", "</script>")
		docsStyleHash = inlineHash(docsPage, "<style>", "</style>")
	})
	return openAPIServed, openAPIETag
}

// inlineHash is the CSP hash-source of the single <script>/<style> block in
// docs.html, so the page needs neither 'unsafe-inline' nor a nonce.
func inlineHash(page []byte, open, close string) string {
	start := bytes.Index(page, []byte(open))
	end := bytes.LastIndex(page, []byte(close))
	if start < 0 || end < start {
		return ""
	}
	sum := sha256.Sum256(page[start+len(open) : end])
	return base64.StdEncoding.EncodeToString(sum[:])
}

func weakETag(b []byte) string {
	sum := sha256.Sum256(b)
	return `W/"` + hex.EncodeToString(sum[:8]) + `"`
}

// handleOpenAPISpec and handleDocs are public: the document describes the
// API without revealing any deployment state, and API consumers need it
// before they have credentials.
func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	spec, etag := renderedOpenAPISpec()
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(spec)
}

func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	renderedOpenAPISpec()
	// The page is a static renderer of /openapi.json: its own script and
	// styles are inline, it loads nothing from anywhere else.
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'sha256-"+docsScriptHash+"'; style-src 'sha256-"+docsStyleHash+"'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("ETag", docsETag)
	w.Header().Set("X-Robots-Tag", "noindex")
	if r.Header.Get("If-None-Match") == docsETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(docsPage)
}
