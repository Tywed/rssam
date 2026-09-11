package reader

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
	"golang.org/x/net/html"

	"rssam/internal/model"
	"rssam/internal/storage"
)

var htmlSanitizer = newEntryPolicy()

// newEntryPolicy is bluemonday's UGC policy plus <video controls poster>
// with <source src type>, which the Telegram bridge emits for channel videos.
// Only http(s) URLs are accepted for poster/src, so javascript: and data:
// payloads are dropped like they are for <a href> and <img src>.
//
// Entry HTML is rendered inside rssam and forwarded to webhook receivers, so
// the policy also handles what the page cannot: every http(s) URL loses its
// tracking parameters (same rule as the dedup key), and links open in a new
// tab with rel="noopener noreferrer" so the target learns neither the
// reader's address nor gets a window.opener.
func newEntryPolicy() *bluemonday.Policy {
	httpURL := regexp.MustCompile(`^https?://[^\s"'<>]+$`)
	p := bluemonday.UGCPolicy()
	p.AllowElements("video")
	p.AllowAttrs("controls").Matching(regexp.MustCompile(`^(controls)?$`)).OnElements("video")
	p.AllowAttrs("poster").Matching(httpURL).OnElements("video")
	p.AllowAttrs("src").Matching(httpURL).OnElements("source")
	p.AllowAttrs("type").Matching(regexp.MustCompile(`^video/[a-z0-9.+-]+$`)).OnElements("source")
	p.AllowURLSchemeWithCustomPolicy("http", stripTrackingURL)
	p.AllowURLSchemeWithCustomPolicy("https", stripTrackingURL)
	p.AllowAttrs("loading").Matching(regexp.MustCompile(`^(lazy|eager)$`)).OnElements("img")
	p.RequireNoReferrerOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)
	return p
}

// stripTrackingURL is a bluemonday URL policy: it never rejects, it edits.
// Relative URLs do not reach scheme policies; they are left as they are.
func stripTrackingURL(u *url.URL) bool {
	if q, stripped := model.StripTrackingParams(u.RawQuery); stripped {
		u.RawQuery = q
		u.ForceQuery = false
	}
	return true
}

// blockedImageHosts are known tracking-pixel and feed-analytics endpoints;
// an <img> pointing at them carries no content.
var blockedImageHosts = []string{
	"feeds.feedburner.com/~r/",
	"feedsportal.com",
	"stats.wordpress.com",
	"pixel.wp.com",
	"api.flattr.com",
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
	out := strings.TrimSpace(htmlSanitizer.Sanitize(html))
	if strings.Contains(out, "<img") {
		out = rewriteImages(out)
	}
	return out
}

// rewriteImages runs over already sanitized HTML: drops 1×1/0×0 pixel
// trackers and images from blockedImageHosts, and adds loading="lazy" so a
// long entry does not fetch every picture at once. Everything that is not an
// <img> tag is copied byte for byte.
func rewriteImages(in string) string {
	z := html.NewTokenizer(strings.NewReader(in))
	var b strings.Builder
	b.Grow(len(in))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return b.String()
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "img" || !hasAttr {
				b.Write(z.Raw())
				continue
			}
			var attrs [][2]string
			drop, lazy := false, false
			for {
				k, v, more := z.TagAttr()
				key, val := string(k), string(v)
				switch key {
				case "width", "height":
					if val == "0" || val == "1" {
						drop = true
					}
				case "src":
					for _, h := range blockedImageHosts {
						if strings.Contains(val, h) {
							drop = true
						}
					}
				case "loading":
					lazy = true
				}
				attrs = append(attrs, [2]string{key, val})
				if !more {
					break
				}
			}
			if drop {
				continue
			}
			b.WriteString("<img")
			for _, a := range attrs {
				b.WriteString(" " + a[0] + `="` + html.EscapeString(a[1]) + `"`)
			}
			if !lazy {
				b.WriteString(` loading="lazy"`)
			}
			b.WriteString(">")
		default:
			b.Write(z.Raw())
		}
	}
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
