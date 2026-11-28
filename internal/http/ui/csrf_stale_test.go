package ui

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// isStaleCSRFRedirect reports whether rec is the answer to a form whose
// token did not validate: 303 back into the UI with the stale-form flash,
// never a bare 403.
func isStaleCSRFRedirect(rec *httptest.ResponseRecorder) bool {
	loc := rec.Header().Get("Location")
	return rec.Code == http.StatusSeeOther && strings.HasPrefix(loc, "/ui/") && strings.Contains(loc, "err="+flashErrStale)
}

// A stale token sends the browser back to the page the form came from, with
// the banner; a Referer from elsewhere is ignored.
func TestCSRFRejected_RedirectsToReferer(t *testing.T) {
	h := &Handler{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	for _, tc := range []struct{ referer, want string }{
		{"http://rssam.local/ui/feeds/12/edit?tab=rules", "/ui/feeds/12/edit?err=stale&tab=rules"},
		{"http://rssam.local/ui/feeds?err=op&rid=abc", "/ui/feeds?err=stale"},
		{"https://evil.example/ui/feeds", "/ui/feeds?err=stale"},
		{"", "/ui/feeds?err=stale"},
	} {
		req := httptest.NewRequest(http.MethodPost, "/ui/feeds", nil)
		if tc.referer != "" {
			req.Header.Set("Referer", tc.referer)
		}
		rec := httptest.NewRecorder()
		h.csrfRejected(rec, req)
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != tc.want {
			t.Errorf("referer %q: %d %q, want 303 %q", tc.referer, rec.Code, rec.Header().Get("Location"), tc.want)
		}
	}
	if flashErrText(flashErrStale, "") == "" {
		t.Fatal("stale flash has no text")
	}
}
