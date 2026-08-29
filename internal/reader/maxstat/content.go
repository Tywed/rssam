package maxstat

import (
	"fmt"
	"html"
	"regexp"
	"strings"
	"unicode/utf8"

	maxbridge "rssam/internal/reader/max"
)

var linkifyRE = regexp.MustCompile(`https?://[^\s<>"']+`)

var typeLabels = map[string]string{
	"photo":           "📷 Фото",
	"video":           "🎬 Видео",
	"audio":           "🎵 Аудио",
	"file":            "📎 Файл",
	"share":           "🔗 Ссылка",
	"sticker":         "😊 Стикер",
	"contact":         "👤 Контакт",
	"location":        "📍 Геолокация",
	"inline_keyboard": "⌨️ Кнопки",
}

func TitleFromPost(p post) string {
	raw := strings.TrimSpace(p.Text)
	if raw != "" {
		line := spaceCollapse.ReplaceAllString(raw, " ")
		if utf8.RuneCountInString(line) > 120 {
			return maxbridge.TitleFromText(line, 120)
		}
		return line
	}
	if label, ok := typeLabels[p.Type]; ok {
		return label
	}
	if p.Type != "" {
		return strings.ToUpper(p.Type[:1]) + p.Type[1:]
	}
	return "MaxStat"
}

func BuildContentHTML(p post) string {
	var b strings.Builder

	rawText := strings.TrimSpace(p.Text)
	if rawText != "" {
		escaped := html.EscapeString(rawText)
		escaped = strings.ReplaceAll(escaped, "\n", "<br>")
		escaped = linkifyRE.ReplaceAllStringFunc(escaped, func(s string) string {
			return `<a href="` + html.EscapeString(s) + `">` + html.EscapeString(s) + `</a>`
		})
		b.WriteString("<p>")
		b.WriteString(escaped)
		b.WriteString("</p>")
	}

	for _, att := range p.Attachments {
		attURL := strings.TrimSpace(att.URL)
		if attURL == "" {
			continue
		}
		attType := strings.TrimSpace(att.Type)
		escURL := html.EscapeString(attURL)
		switch attType {
		case "image", "photo":
			b.WriteString(`<p><img src="`)
			b.WriteString(escURL)
			b.WriteString(`" alt="`)
			b.WriteString(html.EscapeString(attType))
			b.WriteString(`" style="max-width:100%;"></p>`)
		default:
			label := attType
			if label == "" {
				label = "вложение"
			}
			b.WriteString(`<p>📎 <a href="`)
			b.WriteString(escURL)
			b.WriteString(`">`)
			b.WriteString(html.EscapeString(label))
			b.WriteString(`</a></p>`)
		}
	}

	var metrics []string
	if p.Views > 0 {
		metrics = append(metrics, fmt.Sprintf("👁 %s", formatInt(p.Views)))
	}
	if len(p.LikesDetail) > 0 {
		for emoji, cnt := range p.LikesDetail {
			metrics = append(metrics, html.EscapeString(emoji)+" "+formatInt(cnt))
		}
	} else if p.Likes > 0 {
		metrics = append(metrics, fmt.Sprintf("❤️ %s", formatInt(p.Likes)))
	}
	if len(metrics) > 0 {
		b.WriteString(`<p style="color:#888;font-size:0.85em;">`)
		b.WriteString(strings.Join(metrics, " &nbsp;·&nbsp; "))
		b.WriteString(`</p>`)
	}

	postURL := strings.TrimSpace(p.URL)
	if postURL != "" {
		b.WriteString(`<p><a href="`)
		b.WriteString(html.EscapeString(postURL))
		b.WriteString(`">→ Открыть в MAX</a></p>`)
	}

	return b.String()
}

func formatInt(n int) string {
	s := fmt.Sprintf("%d", n)
	if n < 1000 {
		return s
	}
	var parts []string
	for s != "" {
		if len(s) <= 3 {
			parts = append([]string{s}, parts...)
			break
		}
		parts = append([]string{s[len(s)-3:]}, parts...)
		s = s[:len(s)-3]
	}
	return strings.Join(parts, " ")
}

func DedupKey(postID string) string {
	return strings.TrimSpace(postID)
}
