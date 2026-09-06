package httpserver

import (
	"net/http"
	"net/url"
	"strings"

	"rssam/internal/http/middleware"
)

// sameOriginRequest reports whether a browser-originated request came from
// this application's own origin. It is used as the CSRF check for requests
// that are authenticated by the session cookie only (API-token requests are
// not forgeable cross-site because the attacker cannot set the header).
//
// Order of evidence:
//  1. Sec-Fetch-Site (all evergreen browsers since 2020/2023): "same-origin"
//     or "none" (direct navigation, bookmark) pass; "cross-site"/"same-site"
//     fail.
//  2. Origin header: its host must equal the request Host.
//  3. Referer header: same rule (older browsers send Referer on form posts).
//  4. Nothing at all → rejected. Non-browser clients must use an API token.
func sameOriginRequest(r *http.Request) bool {
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "same-origin", "none":
		return true
	case "cross-site", "same-site":
		return false
	}
	host := middleware.RequestHost(r)
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return originMatchesHost(origin, host)
	}
	if ref := strings.TrimSpace(r.Header.Get("Referer")); ref != "" {
		return originMatchesHost(ref, host)
	}
	return false
}

func originMatchesHost(rawURL, host string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return false
	}
	return strings.EqualFold(u.Host, host)
}

// isSafeMethod: methods that must not change state and are therefore exempt
// from the origin check (the SameSite=Strict cookie already protects reads).
func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}
