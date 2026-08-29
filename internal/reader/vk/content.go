package vk

import (
	"fmt"
	"html"
	"regexp"
	"rssam/internal/reader/max"
	"strings"
)

var urlPattern = regexp.MustCompile(`(?i)(https?://[^\s<>"']+)`)

// BuildContentHTML renders post text, attachments, and reposts.
func BuildContentHTML(post wallPost, ownerNames map[int]string) string {
	var b strings.Builder
	if t := strings.TrimSpace(post.Text); t != "" {
		b.WriteString(linkify(nl2br(html.EscapeString(t))))
	}
	for _, att := range post.Attachments {
		if block := renderAttachment(att); block != "" {
			if b.Len() > 0 {
				b.WriteString("<br><br>")
			}
			b.WriteString(block)
		}
	}
	for _, repost := range post.CopyHistory {
		if b.Len() > 0 {
			b.WriteString("<br><br>")
		}
		b.WriteString(renderRepost(repost, ownerNames))
	}
	return sanitizeForXML(b.String())
}

func renderRepost(post wallPost, ownerNames map[int]string) string {
	var b strings.Builder
	b.WriteString(`<blockquote class="vk-repost">`)
	if name := ownerNames[post.OwnerID]; name != "" {
		b.WriteString(`<strong>`)
		b.WriteString(html.EscapeString(name))
		b.WriteString(`</strong><br>`)
	}
	if t := strings.TrimSpace(post.Text); t != "" {
		b.WriteString(linkify(nl2br(html.EscapeString(t))))
	}
	for _, att := range post.Attachments {
		if block := renderAttachment(att); block != "" {
			b.WriteString("<br>")
			b.WriteString(block)
		}
	}
	b.WriteString(`</blockquote>`)
	return b.String()
}

func renderAttachment(att attachment) string {
	switch strings.ToLower(att.Type) {
	case "photo":
		if att.Photo == nil {
			return ""
		}
		u := bestPhotoURL(att.Photo.Sizes)
		if u == "" {
			return ""
		}
		alt := strings.TrimSpace(att.Photo.Text)
		return fmt.Sprintf(`<img src="%s" alt="%s" />`, html.EscapeString(u), html.EscapeString(alt))
	case "video":
		if att.Video == nil {
			return ""
		}
		title := strings.TrimSpace(att.Video.Title)
		if title == "" {
			title = "Video"
		}
		link := fmt.Sprintf("https://vk.com/video%d_%d", att.Video.OwnerID, att.Video.ID)
		return fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(link), html.EscapeString(title))
	case "link":
		if att.Link == nil {
			return ""
		}
		u := strings.TrimSpace(att.Link.URL)
		title := strings.TrimSpace(att.Link.Title)
		if title == "" {
			title = u
		}
		if u == "" {
			return html.EscapeString(title)
		}
		return fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(u), html.EscapeString(title))
	default:
		return ""
	}
}

func bestPhotoURL(sizes []photoSize) string {
	best := ""
	bestW := 0
	for _, s := range sizes {
		if s.URL == "" {
			continue
		}
		if s.Width > bestW {
			bestW = s.Width
			best = s.URL
		}
	}
	if best != "" {
		return best
	}
	for _, s := range sizes {
		if s.URL != "" {
			return s.URL
		}
	}
	return ""
}

func buildOwnerNames(profiles []profile, groups []group) map[int]string {
	names := make(map[int]string, len(profiles)+len(groups))
	for _, p := range profiles {
		name := strings.TrimSpace(p.FirstName + " " + p.LastName)
		if name != "" {
			names[p.ID] = name
		}
	}
	for _, g := range groups {
		if g.Name != "" {
			names[-g.ID] = g.Name
		}
	}
	return names
}

func postAuthor(post wallPost, ownerNames map[int]string) string {
	if name := ownerNames[post.OwnerID]; name != "" {
		return name
	}
	if post.FromID != 0 {
		if name := ownerNames[post.FromID]; name != "" {
			return name
		}
	}
	return ""
}

func postURL(ownerID, postID int) string {
	return fmt.Sprintf("https://vk.com/wall%d_%d", ownerID, postID)
}

func titleFromPost(post wallPost) string {
	text := strings.TrimSpace(post.Text)
	if text == "" && len(post.CopyHistory) > 0 {
		text = strings.TrimSpace(post.CopyHistory[0].Text)
	}
	return max.TitleFromText(text, 140)
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

// DedupKey returns the stable dedup identifier owner_id_post_id.
func DedupKey(ownerID, postID int) string {
	return fmt.Sprintf("%d_%d", ownerID, postID)
}

// TitlePreview truncates to maxRunes (tests).
func TitlePreview(text string, maxRunes int) string {
	return max.TitleFromText(text, maxRunes)
}
