package ui

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

// localUIPath reduces a user- or browser-supplied location (Referer, a
// "next"/"redirect" form field) to a path inside the UI. Anything that is
// not under /ui/ — another origin, a protocol-relative URL, a path outside
// the UI, garbage — yields fallback. The scheme and host are always dropped,
// so the result is safe to hand to http.Redirect.
func localUIPath(raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.HasPrefix(u.Path, "/ui/") {
		return fallback
	}
	// Reject dot segments that would escape the UI after normalisation.
	if p := path.Clean(u.Path); p != u.Path && !strings.HasPrefix(p, "/ui/") {
		return fallback
	}
	// url.Parse("//evil/ui/x") gives Host=evil, Path=/ui/x — dropping the
	// host is exactly what we want; RequestURI keeps only path and query.
	return (&url.URL{Path: u.Path, RawQuery: u.RawQuery}).RequestURI()
}

// refererOr returns the Referer reduced to a UI path, or fallback.
func refererOr(r *http.Request, fallback string) string {
	return localUIPath(r.Header.Get("Referer"), fallback)
}
