package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"rssam/internal/requestid"
)

func TestRequestID(t *testing.T) {
	var seen string
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = requestid.FromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))
	do := func(remote, hdr string) (string, string) {
		seen = ""
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remote
		if hdr != "" {
			req.Header.Set(requestid.Header, hdr)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return seen, rec.Header().Get(requestid.Header)
	}

	// Generated when absent; context and response header agree.
	ctxID, respID := do("203.0.113.5:1234", "")
	if ctxID == "" || ctxID != respID || !requestid.Valid(ctxID) {
		t.Fatalf("generated: ctx=%q resp=%q", ctxID, respID)
	}
	// Untrusted peer cannot choose the id.
	if ctxID, _ := do("203.0.113.5:1234", "attacker-chosen"); ctxID == "attacker-chosen" {
		t.Fatal("id from untrusted peer must be replaced")
	}
	// Trusted proxy (loopback is in DefaultTrustedProxies) may.
	if ctxID, respID := do("127.0.0.1:1234", "nginx-42"); ctxID != "nginx-42" || respID != "nginx-42" {
		t.Fatalf("trusted proxy id not kept: ctx=%q resp=%q", ctxID, respID)
	}
	// ...but only when it is well-formed.
	if ctxID, _ := do("127.0.0.1:1234", "bad id\n"); ctxID == "bad id\n" || ctxID == "" {
		t.Fatalf("malformed id must be replaced, got %q", ctxID)
	}
	// Two requests never share a generated id.
	a, _ := do("203.0.113.5:1", "")
	b, _ := do("203.0.113.5:1", "")
	if a == b {
		t.Fatal("ids must be unique")
	}
}
