// Package dzenchannel is the bridge for Dzen channels (dzen.ru/<name>,
// dzen.ru/id/<hex>): articles, posts, videos and shorts of one author via the
// public export API the channel page itself loads. Channels have no RSS.
package dzenchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"rssam/internal/bridge/v1"
	"rssam/internal/model"
)

// Handler implements bridge.Handler and bridge.TitleDiscoverer.
type Handler struct {
	client *Client
}

func NewHandler(client *Client) *Handler {
	return &Handler{client: client}
}

func (h *Handler) Name() string { return feedType }

func (h *Handler) DetectFeedType(feedURL string) string {
	if Detect(feedURL) {
		return feedType
	}
	return ""
}

// Fetch polls every content tab the channel has (or only the ones named in
// ?types=): one request per tab, the first one also tells which tabs exist.
func (h *Handler) Fetch(ctx context.Context, req bridge.Request) (bridge.Response, error) {
	if h == nil || h.client == nil {
		return bridge.Response{}, fmt.Errorf("dzen_channel: handler is not configured")
	}
	ch, ok := Parse(req.FeedURL)
	if !ok {
		return bridge.Response{}, errInvalidFeedURL
	}
	first := TypeArticle
	if len(ch.Types) > 0 {
		first = ch.Types[0]
	}
	page, err := h.client.Export(ctx, ch, first)
	if err != nil {
		return bridge.Response{}, err
	}
	author := strings.TrimSpace(page.Channel.Source.Title)
	entries := entriesFromPage(page, author)

	want := ch.Types
	if len(want) == 0 {
		want = page.tabTypes()
	}
	for _, t := range want {
		if t == first {
			continue
		}
		if len(ch.Types) > 0 && len(page.Tabs) > 0 && !slices.Contains(page.tabTypes(), t) {
			continue
		}
		more, err := h.client.Export(ctx, ch, t)
		if err != nil {
			return bridge.Response{}, err
		}
		entries = append(entries, entriesFromPage(more, author)...)
	}
	return bridge.Response{Entries: entries}, nil
}

// DiscoverTitle returns the channel's display name for an empty title field.
func (h *Handler) DiscoverTitle(ctx context.Context, feedURL string) (string, error) {
	if h == nil || h.client == nil {
		return "", fmt.Errorf("dzen_channel: handler is not configured")
	}
	ch, ok := Parse(feedURL)
	if !ok {
		return "", errInvalidFeedURL
	}
	page, err := h.client.Export(ctx, ch, TypeArticle)
	if err != nil {
		return "", err
	}
	if title := strings.TrimSpace(page.Channel.Source.Title); title != "" {
		return title, nil
	}
	return ch.String(), nil
}

// publicationPaths are the dzen.ru paths of a single publication; feed items
// with other links (channel cards, collections) are not entries.
var publicationPaths = []string{"/a/", "/b/", "/video/watch/", "/shorts/"}

func entriesFromPage(page *exportPage, author string) []bridge.Entry {
	out := make([]bridge.Entry, 0, len(page.FeedData.Items))
	for _, it := range page.FeedData.Items {
		if e, ok := entryFromItem(it, author); ok {
			out = append(out, e)
		}
	}
	return out
}

func entryFromItem(it exportItem, author string) (bridge.Entry, bool) {
	id := strings.TrimSpace(it.ID)
	link := publicationURL(it)
	if id == "" || link == "" {
		return bridge.Entry{}, false
	}
	title := strings.TrimSpace(it.Title)
	text := strings.TrimSpace(it.Text)
	if title == "" {
		title = firstWords(text, 80)
	}
	if title == "" {
		title = "Публикация"
	}
	var authorPtr *string
	if author != "" {
		a := author
		authorPtr = &a
	}
	return bridge.Entry{
		Title:       title,
		URL:         link,
		Content:     contentHTML(link, imageURL(it), text),
		Author:      authorPtr,
		PublishedAt: publishedAt(it.PublicationDate),
		Hash:        model.DedupHashFromString(id),
	}, true
}

// publicationURL is the share link, or the item link without its tracking
// query; "" when the link is not a publication page.
func publicationURL(it exportItem) string {
	raw := strings.TrimSpace(it.ShareLink)
	if raw == "" {
		raw = strings.TrimSpace(it.Link)
	}
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	ok := false
	for _, p := range publicationPaths {
		if strings.HasPrefix(u.Path, p) {
			ok = true
			break
		}
	}
	if !ok {
		return ""
	}
	u.RawQuery = ""
	u.Fragment = ""
	return model.NormalizeURL(u.String())
}

func imageURL(it exportItem) string {
	if it.Image == nil || it.Image.URLTemplate == "" {
		return ""
	}
	// scale_1200 is the size the channel page shows (~280 KB); orig is the
	// upload itself (4–5 MB for a news photo).
	size := it.Image.SizeName
	if size == "" {
		size = "scale_1200"
	}
	ns := it.Image.Namespace
	if ns == "" {
		ns = "zen_doc"
	}
	u := strings.ReplaceAll(it.Image.URLTemplate, "{namespace}", ns)
	u = strings.ReplaceAll(u, "{size}", size)
	if !strings.HasPrefix(u, "https://") {
		return ""
	}
	return u
}

func publishedAt(n json.Number) *time.Time {
	s := strings.TrimSpace(n.String())
	if s == "" {
		return nil
	}
	v, err := n.Int64()
	if err != nil {
		return nil
	}
	if v <= 0 {
		return nil
	}
	if v > 1e12 {
		v /= 1000
	}
	t := time.Unix(v, 0).UTC()
	return &t
}

func contentHTML(link, image, text string) string {
	var b strings.Builder
	if image != "" {
		fmt.Fprintf(&b, `<p><a href="%s"><img src="%s" alt="" loading="lazy"></a></p>`, html.EscapeString(link), html.EscapeString(image))
	}
	if text != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("<p>" + strings.ReplaceAll(html.EscapeString(text), "\n", "<br>\n") + "</p>")
	}
	return b.String()
}

// firstWords cuts text to at most limit runes on a word boundary.
func firstWords(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	cut := string(runes[:limit])
	if i := strings.LastIndex(cut, " "); i > limit/2 {
		cut = cut[:i]
	}
	return cut + "…"
}
