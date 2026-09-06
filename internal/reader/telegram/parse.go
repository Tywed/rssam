package telegram

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

var backgroundImageRegex = regexp.MustCompile(`background-image:\s*url\('([^']+)'\)`)

// ParsedMessage is one Telegram channel post from t.me/s preview HTML.
type ParsedMessage struct {
	URI            string
	Title          string
	Content        string
	Timestamp      time.Time
	Author         string
	Enclosures     []string
	HasViews       bool // false for non-last items in a legacy split album
	IsGroupedAlbum bool // data-view p ends with "g" or js-message_grouped_wrap present
}

// ParseHTML extracts messages from a t.me/s channel page.
func ParseHTML(body []byte, channelUsername string) ([]ParsedMessage, error) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("telegram: parse html: %w", err)
	}
	channelUsername = NormalizeUsername(channelUsername)

	var out []ParsedMessage
	doc.Find("div.tgme_widget_message_wrap.js-widget_message_wrap").Each(func(_ int, wrap *goquery.Selection) {
		msg := wrap.Find("div.tgme_widget_message").First()
		if msg.Length() == 0 {
			return
		}
		parsed := parseMessage(msg, channelUsername)
		if parsed.URI != "" {
			out = append(out, parsed)
		}
	})
	if len(out) == 0 {
		title := strings.TrimSpace(doc.Find("div.tgme_channel_info_header_title span").First().Text())
		if title == "" {
			return nil, fmt.Errorf("telegram: no messages found (channel may be private or missing)")
		}
	}
	return mergeGroupedMessages(out), nil
}

// mergeGroupedMessages combines consecutive album parts into one entry.
// On t.me/s only the last item in a grouped album shows view count.
func mergeGroupedMessages(msgs []ParsedMessage) []ParsedMessage {
	if len(msgs) == 0 {
		return msgs
	}
	var out []ParsedMessage
	var group []ParsedMessage

	flush := func() {
		if len(group) == 0 {
			return
		}
		if len(group) == 1 {
			out = append(out, group[0])
		} else {
			out = append(out, mergeMessageGroup(group))
		}
		group = nil
	}

	for _, m := range msgs {
		if m.IsGroupedAlbum {
			flush()
			out = append(out, m)
			continue
		}
		if m.HasViews {
			if len(group) > 0 {
				group = append(group, m)
				flush()
			} else {
				out = append(out, m)
			}
		} else {
			group = append(group, m)
		}
	}
	flush()
	return out
}

func mergeMessageGroup(group []ParsedMessage) ParsedMessage {
	last := group[len(group)-1]
	merged := ParsedMessage{
		URI:       last.URI,
		Timestamp: last.Timestamp,
		Author:    last.Author,
		Title:     last.Title,
		HasViews:  true,
	}
	if merged.Author == "" {
		for _, m := range group {
			if m.Author != "" {
				merged.Author = m.Author
				break
			}
		}
	}
	if merged.Timestamp.IsZero() {
		for _, m := range group {
			if !m.Timestamp.IsZero() {
				merged.Timestamp = m.Timestamp
				break
			}
		}
	}
	var content strings.Builder
	for _, m := range group {
		if s := strings.TrimSpace(m.Content); s != "" {
			if content.Len() > 0 {
				content.WriteString("\n")
			}
			content.WriteString(s)
		}
		merged.Enclosures = append(merged.Enclosures, m.Enclosures...)
	}
	merged.Enclosures = uniqueStrings(merged.Enclosures)
	merged.Content = content.String()
	if merged.Title == "" || isGenericMediaTitle(merged.Title) {
		for i := len(group) - 1; i >= 0; i-- {
			if t := strings.TrimSpace(group[i].Title); t != "" && !isGenericMediaTitle(t) {
				merged.Title = t
				break
			}
		}
	}
	if merged.Title == "" {
		merged.Title = ellipsisTitle(stripTags(merged.Content), 100)
	}
	if merged.Title == "" {
		merged.Title = "Telegram post"
	}
	return merged
}

// ChannelTitleFromHTML extracts public channel title from t.me/s preview page.
func ChannelTitleFromHTML(body []byte) string {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return ""
	}
	title := strings.TrimSpace(doc.Find("div.tgme_channel_info_header_title span").First().Text())
	if title == "" {
		return ""
	}
	return html.UnescapeString(title)
}

// NextPageURL returns pagination link when present.
func NextPageURL(body []byte) (string, bool) {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return "", false
	}
	link, ok := doc.Find("div.tgme_widget_message_centered.js-messages_more_wrap a").First().Attr("href")
	if !ok || link == "" || !strings.Contains(link, "before") {
		return "", false
	}
	if strings.HasPrefix(link, "http") {
		return link, true
	}
	return "https://t.me/" + strings.TrimPrefix(link, "/"), true
}

func parseMessage(msg *goquery.Selection, channelUsername string) ParsedMessage {
	var p ParsedMessage
	if href, ok := msg.Find("a.tgme_widget_message_date").First().Attr("href"); ok {
		p.URI = strings.TrimSpace(href)
	}
	if dt, ok := msg.Find("span.tgme_widget_message_meta time").First().Attr("datetime"); ok {
		if t, err := time.Parse(time.RFC3339, dt); err == nil {
			p.Timestamp = t.UTC()
		}
	}
	if owner := msg.Find("a.tgme_widget_message_owner_name").First(); owner.Length() > 0 {
		p.Author = strings.TrimSpace(owner.Text())
	} else if channelUsername != "" {
		p.Author = "@" + channelUsername
	}
	if views := msg.Find("span.tgme_widget_message_views").First(); views.Length() > 0 {
		p.HasViews = strings.TrimSpace(views.Text()) != ""
	}
	p.IsGroupedAlbum = isGroupedAlbumMessage(msg)
	if p.IsGroupedAlbum {
		p.HasViews = true
	}

	content, title, enclosures := buildContent(msg, channelUsername)
	p.Content = content
	p.Title = title
	p.Enclosures = enclosures
	if p.Title == "" {
		p.Title = ellipsisTitle(stripTags(content), 100)
	}
	if p.Title == "" {
		p.Title = "Telegram post"
	}
	return p
}

func buildContent(msg *goquery.Selection, channelUsername string) (content, title string, enclosures []string) {
	var b strings.Builder

	if ns := msg.Find("div.message_media_not_supported_wrap").First(); ns.Length() > 0 {
		b.WriteString(innerHTML(ns))
	}

	if fwd := msg.Find("div.tgme_widget_message_forwarded_from").First(); fwd.Length() > 0 {
		b.WriteString(innerHTML(fwd))
		b.WriteString("<br><br>")
	}

	if reply := msg.Find("a.tgme_widget_message_reply").First(); reply.Length() > 0 {
		b.WriteString(processReply(reply))
	}

	if msg.Find("div.tgme_widget_message_sticker_wrap").Length() > 0 {
		part, t := processSticker(msg, channelUsername)
		b.WriteString(part)
		if title == "" {
			title = t
		}
	}

	if grouped := msg.Find("div.tgme_widget_message_grouped_wrap.js-message_grouped_wrap"); grouped.Length() > 0 {
		part, t, enc := processGroupedMedia(grouped, channelUsername)
		b.WriteString(part)
		enclosures = append(enclosures, enc...)
		if title == "" || isGenericMediaTitle(title) {
			if t != "" {
				title = t
			}
		}
	} else {
		if msg.Find("video").Length() > 0 {
			part, t, enc := processVideo(msg, channelUsername)
			b.WriteString(part)
			enclosures = append(enclosures, enc...)
			if title == "" {
				title = t
			}
		}

		if msg.Find("a.tgme_widget_message_photo_wrap").Length() > 0 {
			part, t, enc := processPhoto(msg, channelUsername)
			b.WriteString(part)
			enclosures = append(enclosures, enc...)
			if title == "" {
				title = t
			}
		}
	}

	if text := msg.Find("div.tgme_widget_message_text.js-message_text").First(); text.Length() > 0 {
		b.WriteString(innerHTML(text))
		title = ellipsisTitle(strings.TrimSpace(text.Text()), 100)
	}

	if msg.Find("a.tgme_widget_message_link_preview").Length() > 0 {
		b.WriteString(processLinkPreview(msg.Find("a.tgme_widget_message_link_preview").First()))
	}

	return b.String(), title, uniqueStrings(enclosures)
}

func processReply(reply *goquery.Selection) string {
	author := strings.TrimSpace(reply.Find("span.tgme_widget_message_author_name").First().Text())
	var text string
	if meta := reply.Find("div.tgme_widget_message_metatext").First(); meta.Length() > 0 {
		text = innerHTML(meta)
	}
	if body := reply.Find("div.tgme_widget_message_text").First(); body.Length() > 0 {
		text = innerHTML(body)
	}
	href, _ := reply.Attr("href")
	return fmt.Sprintf("<blockquote>%s<br>%s<br><a href=%q>%s</a></blockquote><hr>",
		html.EscapeString(author), text, href, html.EscapeString(href))
}

func processSticker(msg *goquery.Selection, channelUsername string) (string, string) {
	title := ""
	if channelUsername != "" {
		title = "@" + channelUsername + " posted a sticker"
	}
	wrap := msg.Find("div.tgme_widget_message_sticker_wrap").First()
	if wrap.Length() == 0 {
		return "", title
	}
	if wrap.Find("picture").Length() > 0 {
		return innerHTML(wrap), title
	}
	if style, ok := wrap.Find("i").First().Attr("style"); ok {
		if m := backgroundImageRegex.FindStringSubmatch(style); len(m) > 1 {
			return fmt.Sprintf(`<img src=%q>`, m[1]), title
		}
	}
	return "", title
}

func processPhoto(msg *goquery.Selection, channelUsername string) (string, string, []string) {
	title := ""
	if channelUsername != "" {
		title = "@" + channelUsername + " posted a photo"
	}
	var b strings.Builder
	var enc []string
	msg.Find("a.tgme_widget_message_photo_wrap").Each(func(_ int, wrap *goquery.Selection) {
		style, _ := wrap.Attr("style")
		m := backgroundImageRegex.FindStringSubmatch(style)
		if len(m) < 2 {
			return
		}
		enc = append(enc, m[1])
		href, _ := wrap.Attr("href")
		fmt.Fprintf(&b, `<a href=%q><img src=%q></a><br>`, href, m[1])
	})
	return b.String(), title, enc
}

func processVideo(msg *goquery.Selection, channelUsername string) (string, string, []string) {
	title := ""
	if channelUsername != "" {
		title = "@" + channelUsername + " posted a video"
	}
	var poster string
	for _, sel := range []string{
		"i.tgme_widget_message_video_thumb",
		"i.link_preview_video_thumb",
		"i.tgme_widget_message_roundvideo_thumb",
	} {
		if style, ok := msg.Find(sel).First().Attr("style"); ok {
			if m := backgroundImageRegex.FindStringSubmatch(style); len(m) > 1 {
				poster = m[1]
				break
			}
		}
	}
	var enc []string
	if poster != "" {
		enc = append(enc, poster)
	}
	src, _ := msg.Find("video").First().Attr("src")
	if src != "" {
		enc = append(enc, src)
	}
	htmlOut := fmt.Sprintf(`<video controls poster=%q style="max-width:100%%"><source src=%q type="video/mp4"></video>`,
		poster, src)
	return htmlOut, title, enc
}

func isGroupedAlbumMessage(msg *goquery.Selection) bool {
	if msg.Find("div.tgme_widget_message_grouped_wrap.js-message_grouped_wrap").Length() > 0 {
		return true
	}
	return dataViewGrouped(msg)
}

func dataViewGrouped(msg *goquery.Selection) bool {
	raw, ok := msg.Attr("data-view")
	if !ok || strings.TrimSpace(raw) == "" {
		return false
	}
	raw = strings.TrimSpace(raw)
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		if pad := len(raw) % 4; pad != 0 {
			raw += strings.Repeat("=", 4-pad)
		}
		data, err = base64.StdEncoding.DecodeString(raw)
	}
	if err != nil {
		return false
	}
	var view struct {
		P any `json:"p"`
	}
	if err := json.Unmarshal(data, &view); err != nil {
		return false
	}
	switch p := view.P.(type) {
	case string:
		return strings.HasSuffix(p, "g")
	default:
		return false
	}
}

func processGroupedMedia(grouped *goquery.Selection, channelUsername string) (string, string, []string) {
	title := ""
	if channelUsername != "" {
		title = "@" + channelUsername + " posted media"
	}
	var b strings.Builder
	var enc []string

	grouped.Find("a.tgme_widget_message_photo_wrap").Each(func(_ int, wrap *goquery.Selection) {
		style, _ := wrap.Attr("style")
		m := backgroundImageRegex.FindStringSubmatch(style)
		if len(m) < 2 {
			return
		}
		enc = append(enc, m[1])
		href, _ := wrap.Attr("href")
		fmt.Fprintf(&b, `<a href=%q><img src=%q></a><br>`, href, m[1])
	})

	grouped.Find("a.tgme_widget_message_video_player").Each(func(_ int, player *goquery.Selection) {
		var poster string
		if style, ok := player.Find("i.tgme_widget_message_video_thumb").First().Attr("style"); ok {
			if m := backgroundImageRegex.FindStringSubmatch(style); len(m) > 1 {
				poster = m[1]
			}
		}
		src, _ := player.Find("video").First().Attr("src")
		if poster != "" {
			enc = append(enc, poster)
		}
		if src != "" {
			enc = append(enc, src)
		}
		fmt.Fprintf(&b, `<video controls poster=%q style="max-width:100%%"><source src=%q type="video/mp4"></video><br>`,
			poster, src)
	})

	return b.String(), title, uniqueStrings(enc)
}

func processLinkPreview(preview *goquery.Selection) string {
	if strings.TrimSpace(preview.Text()) == "" {
		return ""
	}
	var image, title, site, desc string
	if style, ok := preview.Find("i").First().Attr("style"); ok {
		if m := backgroundImageRegex.FindStringSubmatch(style); len(m) > 1 {
			image = fmt.Sprintf(`<img src=%q>`, m[1])
		}
	}
	if el := preview.Find("div.link_preview_title").First(); el.Length() > 0 {
		title = html.EscapeString(strings.TrimSpace(el.Text()))
	}
	if el := preview.Find("div.link_preview_site_name").First(); el.Length() > 0 {
		site = html.EscapeString(strings.TrimSpace(el.Text()))
	}
	if el := preview.Find("div.link_preview_description").First(); el.Length() > 0 {
		desc = html.EscapeString(strings.TrimSpace(el.Text()))
	}
	href, _ := preview.Attr("href")
	return fmt.Sprintf(`<blockquote><a href=%q>%s</a><br><a href=%q>%s - %s</a><br>%s</blockquote>`,
		href, image, href, title, site, desc)
}

func innerHTML(sel *goquery.Selection) string {
	h, err := sel.Html()
	if err != nil {
		return sel.Text()
	}
	return h
}

func ellipsisTitle(text string, maxLen int) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return string(runes[:maxLen]) + "..."
}

func stripTags(s string) string {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader("<div>" + s + "</div>"))
	if err != nil {
		return s
	}
	return strings.TrimSpace(doc.Find("div").First().Text())
}

func isGenericMediaTitle(title string) bool {
	return strings.Contains(title, " posted a photo") ||
		strings.Contains(title, " posted a video") ||
		strings.Contains(title, " posted a sticker")
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
