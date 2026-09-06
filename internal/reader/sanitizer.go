package reader

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"

	"rssam/internal/storage"
)

var htmlSanitizer = newEntryPolicy()

// newEntryPolicy is bluemonday's UGC policy plus <video controls poster>
// with <source src type>, which the Telegram bridge emits for channel videos.
// Only http(s) URLs are accepted for poster/src, so javascript: and data:
// payloads are dropped like they are for <a href> and <img src>.
func newEntryPolicy() *bluemonday.Policy {
	httpURL := regexp.MustCompile(`^https?://[^\s"'<>]+$`)
	p := bluemonday.UGCPolicy()
	p.AllowElements("video")
	p.AllowAttrs("controls").Matching(regexp.MustCompile(`^(controls)?$`)).OnElements("video")
	p.AllowAttrs("poster").Matching(httpURL).OnElements("video")
	p.AllowAttrs("src").Matching(httpURL).OnElements("source")
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^video/[a-z0-9.+-]+$`)).OnElements("source")
	return p
}

// SanitizeHTML strips unsafe markup from feed entry HTML (scripts, event handlers, etc.).
// Plain text is returned unchanged aside from trimming.
func SanitizeHTML(html string) string {
	html = strings.TrimSpace(html)
	if html == "" {
		return ""
	}
	if !strings.Contains(html, "<") {
		return html
	}
	return strings.TrimSpace(htmlSanitizer.Sanitize(html))
}

// SanitizeEntries sanitizes Content of every entry in place. Entry content is
// rendered as trusted HTML by the web UI and forwarded to webhook receivers,
// so everything a handler returns must pass through here regardless of the
// source type.
func SanitizeEntries(entries []storage.CreateEntryParams) {
	for i := range entries {
		entries[i].Content = SanitizeHTML(entries[i].Content)
	}
}
