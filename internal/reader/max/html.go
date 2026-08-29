package max

import (
	"html"
	"regexp"
	"strings"
	"unicode/utf8"
)

var urlPattern = regexp.MustCompile(`(?i)(https?://[^\s<>"']+)`)

// TitleFromText returns the first line of plain text, truncated to maxRunes.
func TitleFromText(text string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 140
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "Message"
	}
	line := text
	if idx := strings.IndexAny(text, "\r\n"); idx >= 0 {
		line = text[:idx]
	}
	line = strings.TrimSpace(line)
	if line == "" {
		line = text
	}
	if utf8.RuneCountInString(line) <= maxRunes {
		return sanitizeForXML(line)
	}
	runes := []rune(line)
	return sanitizeForXML(string(runes[:maxRunes]))
}

// BuildContentHTML renders message body HTML (text + forwarded block).
func BuildContentHTML(text string, fwd *ForwardedMessage) string {
	var b strings.Builder
	if t := strings.TrimSpace(text); t != "" {
		b.WriteString(linkify(nl2br(html.EscapeString(t))))
	}
	if fwd != nil {
		if b.Len() > 0 {
			b.WriteString("<br><br>")
		}
		b.WriteString(`<blockquote class="max-forwarded">`)
		if name := strings.TrimSpace(fwd.ChatName); name != "" {
			if link := strings.TrimSpace(fwd.ChatLink); link != "" {
				b.WriteString(`<strong><a href="`)
				b.WriteString(html.EscapeString(link))
				b.WriteString(`">`)
				b.WriteString(html.EscapeString(name))
				b.WriteString(`</a></strong><br>`)
			} else {
				b.WriteString(`<strong>`)
				b.WriteString(html.EscapeString(name))
				b.WriteString(`</strong><br>`)
			}
		}
		if mt := strings.TrimSpace(fwd.MessageText); mt != "" {
			b.WriteString(linkify(nl2br(html.EscapeString(mt))))
		}
		b.WriteString(`</blockquote>`)
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

// sanitizeForXML removes characters invalid in XML 1.0 (like PHP sanitizeForXml).
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
