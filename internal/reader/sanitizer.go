package reader

import (
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

var htmlSanitizer = bluemonday.UGCPolicy()

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
