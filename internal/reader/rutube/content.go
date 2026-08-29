package rutube

import (
	"fmt"
	"html"
	"regexp"
	"strings"
)

var urlPattern = regexp.MustCompile(`(?i)(https?://[^\s<>"']+)`)

// BuildContentHTML renders thumbnail + description (nl2br + linkify).
func BuildContentHTML(thumbnailURL, description string) string {
	var b strings.Builder
	if thumb := strings.TrimSpace(thumbnailURL); thumb != "" {
		b.WriteString(fmt.Sprintf(`<p><a href="%s"><img src="%s" alt="" /></a></p>`,
			html.EscapeString(thumb), html.EscapeString(thumb)))
	}
	if desc := strings.TrimSpace(description); desc != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(linkify(nl2br(html.EscapeString(desc))))
	}
	return sanitizeForXML(b.String())
}

func nl2br(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.ReplaceAll(s, "\n", "<br>\n")
}

func linkify(s string) string {
	return urlPattern.ReplaceAllStringFunc(s, func(match string) string {
		m := strings.TrimRight(match, ".,;:!?)")
		suffix := match[len(m):]
		return `<a href="` + html.EscapeString(m) + `" rel="nofollow noopener noreferrer">` + html.EscapeString(m) + `</a>` + suffix
	})
}

func sanitizeForXML(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if isValidXMLChar(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isValidXMLChar(r rune) bool {
	return r == 0x9 || r == 0xA || r == 0xD ||
		(r >= 0x20 && r <= 0xD7FF) ||
		(r >= 0xE000 && r <= 0xFFFD) ||
		(r >= 0x10000 && r <= 0x10FFFF)
}

// FeedTitle returns "{author} - Rutube" for feed metadata.
func FeedTitle(authorName string) string {
	authorName = strings.TrimSpace(authorName)
	if authorName == "" {
		return "Rutube"
	}
	return authorName + " - Rutube"
}
