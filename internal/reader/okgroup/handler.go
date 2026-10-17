// Package okgroup is the bridge for Odnoklassniki groups (ok.ru/group/<id>,
// ok.ru/<slug>): the public desktop page lists the newest ~10 posts; each
// post the bridge has not seen yet is read once from its own page for the
// full text and the exact time. Groups have no RSS.
package okgroup

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"rssam/internal/bridge/v1"
	"rssam/internal/model"
)

const (
	// maxTopicFetches bounds the per-post requests of one poll; posts beyond
	// it are emitted from the group page (day precision, cut text).
	maxTopicFetches = 10
	// seenKeep is how many post ids the state remembers; the page shows ~10.
	seenKeep = 60
)

// state is the bridge's document in feeds.bridge_state: the ids already
// turned into entries, newest first.
type state struct {
	Seen []string `json:"seen,omitempty"`
}

// Handler implements bridge.Handler and bridge.TitleDiscoverer.
type Handler struct {
	client *Client
	now    func() time.Time
}

func NewHandler(client *Client) *Handler {
	return &Handler{client: client, now: time.Now}
}

func (h *Handler) Name() string { return feedType }

func (h *Handler) DetectFeedType(feedURL string) string {
	if Detect(feedURL) {
		return feedType
	}
	return ""
}

// Fetch reads the group page and emits the posts not in State.Seen; the
// first poll (empty state) emits everything the page shows.
func (h *Handler) Fetch(ctx context.Context, req bridge.Request) (bridge.Response, error) {
	if h == nil || h.client == nil {
		return bridge.Response{}, fmt.Errorf("ok_group: handler is not configured")
	}
	g, ok := Parse(req.FeedURL)
	if !ok {
		return bridge.Response{}, errInvalidFeedURL
	}
	page, err := h.loadGroup(ctx, g)
	if err != nil {
		return bridge.Response{}, err
	}
	var st state
	if len(req.State) > 0 {
		_ = json.Unmarshal(req.State, &st)
	}
	seen := make(map[string]bool, len(st.Seen))
	for _, id := range st.Seen {
		seen[id] = true
	}
	now := h.now()
	fetched := 0
	var entries []bridge.Entry
	var newIDs []string
	for _, p := range page.Posts {
		if seen[p.ID] || slices.Contains(newIDs, p.ID) {
			continue
		}
		newIDs = append(newIDs, p.ID)
		var t *topic
		if fetched < maxTopicFetches {
			fetched++
			body, ok, err := h.client.TopicPage(ctx, page.GroupID, p.ID)
			if err != nil {
				return bridge.Response{}, err
			}
			if ok {
				t, _ = parseTopicPage(body)
			}
		}
		entries = append(entries, entry(page, p, t, now))
	}
	if len(newIDs) == 0 {
		return bridge.Response{Entries: nil}, nil
	}
	st.Seen = append(newIDs, st.Seen...)
	if len(st.Seen) > seenKeep {
		st.Seen = st.Seen[:seenKeep]
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return bridge.Response{}, err
	}
	return bridge.Response{Entries: entries, State: raw}, nil
}

// DiscoverTitle returns the group's name for an empty title field.
func (h *Handler) DiscoverTitle(ctx context.Context, feedURL string) (string, error) {
	if h == nil || h.client == nil {
		return "", fmt.Errorf("ok_group: handler is not configured")
	}
	g, ok := Parse(feedURL)
	if !ok {
		return "", errInvalidFeedURL
	}
	page, err := h.loadGroup(ctx, g)
	if err != nil {
		return "", err
	}
	if page.Title != "" {
		return page.Title, nil
	}
	return g.String(), nil
}

func (h *Handler) loadGroup(ctx context.Context, g Group) (*groupPage, error) {
	body, err := h.client.GroupPage(ctx, g)
	if err != nil {
		return nil, err
	}
	page, err := parseGroupPage(body)
	if err != nil {
		return nil, fmt.Errorf("ok_group: parse page: %w", err)
	}
	if page.Profile {
		return nil, fmt.Errorf("%w: %s", ErrNotGroup, g)
	}
	if page.GroupID == "" {
		return nil, fmt.Errorf("%w: %s", ErrGroupNotFound, g)
	}
	return page, nil
}

func entry(page *groupPage, p listPost, t *topic, now time.Time) bridge.Entry {
	title, text, images := p.Title, p.Text, []string(nil)
	if p.Image != "" {
		images = append(images, p.Image)
	}
	published := parseWhen(p.When, now)
	if t != nil {
		if t.Body != "" {
			text = t.Body
		}
		if t.Headline != "" {
			title = t.Headline
		}
		if len(t.Images) > 0 {
			images = t.Images
		}
		if !t.Published.IsZero() {
			ts := t.Published.UTC()
			published = &ts
		}
	}
	// The headline repeats the first line of the body on ok.ru.
	if title != "" && strings.HasPrefix(text, title) {
		text = strings.TrimSpace(strings.TrimPrefix(text, title))
	}
	if title == "" {
		title = firstWords(text, 80)
	}
	if title == "" {
		title = "Публикация"
	}
	var author *string
	if page.Title != "" {
		a := page.Title
		author = &a
	}
	link := TopicURL(page.GroupID, p.ID)
	return bridge.Entry{
		Title:       title,
		URL:         link,
		Content:     contentHTML(link, images, text, p.Link),
		Author:      author,
		PublishedAt: published,
		Hash:        model.DedupHashFromString(p.ID),
	}
}

func contentHTML(link string, images []string, text, external string) string {
	var b strings.Builder
	for _, img := range images {
		fmt.Fprintf(&b, `<p><a href="%s"><img src="%s" alt="" loading="lazy"></a></p>`+"\n", html.EscapeString(link), html.EscapeString(img))
	}
	if text != "" {
		b.WriteString("<p>" + strings.ReplaceAll(html.EscapeString(text), "\n", "<br>\n") + "</p>")
	}
	if external != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, `<p><a href="%s">%s</a></p>`, html.EscapeString(external), html.EscapeString(external))
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
