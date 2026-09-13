// Package page watches a fragment of an HTML page (chosen by CSS selector)
// and turns each change into one entry: RSS for sites that have no feed.
package page

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"

	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

const (
	FeedType = "page"

	// maxBodyBytes bounds the page download; maxTextBytes bounds the text
	// snapshot kept in bridge_state for the next diff; maxContentBytes bounds
	// the fragment HTML stored with an entry.
	maxBodyBytes    = 2 << 20
	maxTextBytes    = 32 << 10
	maxContentBytes = 128 << 10
	defaultSelector = "main, article, body"
)

var ErrSelectorNoMatch = errors.New("page: selector matched nothing")

// Options parsed from a feed URL: page+https://host/path#selector.
type Options struct {
	PageURL  string
	Selector string
}

// ParseFeedURL accepts page+http(s)://…#selector; an empty fragment means
// the default selector. The fragment "#id" is written as "##id".
func ParseFeedURL(feedURL string) (Options, bool) {
	feedURL = strings.TrimSpace(feedURL)
	u, err := url.Parse(feedURL)
	if err != nil || u.Host == "" {
		return Options{}, false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "page+http" && scheme != "page+https" {
		return Options{}, false
	}
	sel := strings.TrimSpace(u.Fragment)
	if sel == "" {
		sel = defaultSelector
	}
	u.Scheme = strings.TrimPrefix(scheme, "page+")
	u.Fragment = ""
	u.RawFragment = ""
	return Options{PageURL: u.String(), Selector: sel}, true
}

// CompileSelector reports whether sel is a valid CSS selector. goquery.Find
// panics on a compile error, so callers must check this first.
func CompileSelector(sel string) error {
	_, err := cascadia.Compile(sel)
	return err
}

func DetectFeedURL(feedURL string) bool {
	_, ok := ParseFeedURL(feedURL)
	return ok
}

// State is what survives between polls (feeds.bridge_state).
type State struct {
	Hash string
	Text string
}

type Result struct {
	Entries  []storage.CreateEntryParams
	State    State
	Title    string
	Modified bool
}

type Handler struct {
	client    *http.Client
	guard     *ssrf.Guard
	userAgent string
}

func NewHandler(client *http.Client, guard *ssrf.Guard, userAgent string) *Handler {
	if client == nil {
		client = http.DefaultClient
	}
	return &Handler{client: client, guard: guard, userAgent: strings.TrimSpace(userAgent)}
}

func (h *Handler) Name() string { return FeedType }

func (h *Handler) DetectFeedType(feedURL string) string {
	if DetectFeedURL(feedURL) {
		return FeedType
	}
	return ""
}

// Fetch downloads the page, extracts the selected fragment and compares it
// with the previous snapshot. Unchanged → Modified=false and no entries; the
// first poll produces a baseline entry so the user can verify the selector.
func (h *Handler) Fetch(ctx context.Context, feedURL, userAgent string, st State, tlsInsecure bool) (Result, error) {
	opts, ok := ParseFeedURL(feedURL)
	if !ok {
		return Result{State: st}, fmt.Errorf("page: invalid feed url %q", feedURL)
	}
	if err := CompileSelector(opts.Selector); err != nil {
		return Result{State: st}, fmt.Errorf("page: invalid CSS selector: %w", err)
	}
	doc, err := h.fetchDocument(ctx, opts.PageURL, userAgent, tlsInsecure)
	if err != nil {
		return Result{State: st}, err
	}
	sel := doc.Find(opts.Selector).First()
	if sel.Length() == 0 {
		return Result{State: st}, fmt.Errorf("%w: %s", ErrSelectorNoMatch, opts.Selector)
	}
	text := truncate(blockText(sel), maxTextBytes)
	hash := hashText(text)
	title := strings.TrimSpace(doc.Find("title").First().Text())
	if title == "" {
		title = opts.PageURL
	}
	if hash == st.Hash {
		return Result{State: st, Title: title}, nil
	}
	sel.Find("script,style,noscript,template").Remove()
	fragment, _ := sel.Html()
	fragment = truncate(strings.TrimSpace(fragment), maxContentBytes)
	now := time.Now().UTC()
	first := st.Hash == ""
	entryTitle := title + ": изменение " + now.Local().Format("02.01.2006 15:04")
	if first {
		entryTitle = title + ": начальный снимок"
	}
	var content strings.Builder
	if !first {
		content.WriteString(diffHTML(st.Text, text))
	}
	content.WriteString(fragment)
	entry := storage.CreateEntryParams{
		Title:       entryTitle,
		URL:         opts.PageURL,
		Content:     content.String(),
		PublishedAt: &now,
		Hash:        hashText(opts.PageURL + "\n" + hash),
	}
	return Result{
		Entries:  []storage.CreateEntryParams{entry},
		State:    State{Hash: hash, Text: text},
		Title:    title,
		Modified: true,
	}, nil
}

// DiscoverTitle fetches the page once for the feed form (title suggestion
// and selector check).
func (h *Handler) DiscoverTitle(ctx context.Context, feedURL string, tlsInsecure bool) (string, error) {
	opts, ok := ParseFeedURL(feedURL)
	if !ok {
		return "", fmt.Errorf("page: invalid feed url %q", feedURL)
	}
	if err := CompileSelector(opts.Selector); err != nil {
		return "", fmt.Errorf("page: invalid CSS selector: %w", err)
	}
	doc, err := h.fetchDocument(ctx, opts.PageURL, "", tlsInsecure)
	if err != nil {
		return "", err
	}
	if doc.Find(opts.Selector).First().Length() == 0 {
		return "", fmt.Errorf("%w: %s", ErrSelectorNoMatch, opts.Selector)
	}
	title := strings.TrimSpace(doc.Find("title").First().Text())
	if title == "" {
		title = opts.PageURL
	}
	return title, nil
}

func (h *Handler) fetchDocument(ctx context.Context, pageURL, userAgent string, tlsInsecure bool) (*goquery.Document, error) {
	if h.guard != nil {
		if err := h.guard.ValidateURL(pageURL); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	ua := strings.TrimSpace(userAgent)
	if ua == "" {
		ua = h.userAgent
	}
	if ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml;q=0.9,*/*;q=0.8")
	client := h.client
	if h.guard != nil {
		timeout := 15 * time.Second
		if h.client != nil && h.client.Timeout > 0 {
			timeout = h.client.Timeout
		}
		client = h.guard.HTTPClientForFetch(timeout, tlsInsecure)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
	lr := &io.LimitedReader{R: resp.Body, N: maxBodyBytes + 1}
	doc, err := goquery.NewDocumentFromReader(lr)
	if err != nil {
		return nil, fmt.Errorf("parse html: %w", err)
	}
	if lr.N == 0 {
		return nil, fmt.Errorf("page body exceeds %d bytes", maxBodyBytes)
	}
	return doc, nil
}

var blockTags = map[string]bool{
	"p": true, "div": true, "li": true, "tr": true, "br": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "section": true, "article": true, "header": true, "footer": true,
	"table": true, "ul": true, "ol": true, "dd": true, "dt": true, "blockquote": true, "pre": true, "hr": true,
}

// blockText renders the selection as lines: one per block element, inline
// whitespace collapsed, script/style ignored. The diff works on these lines.
func blockText(sel *goquery.Selection) string {
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
		case html.ElementNode:
			if n.Data == "script" || n.Data == "style" || n.Data == "noscript" || n.Data == "template" {
				return
			}
			if blockTags[n.Data] {
				b.WriteByte('\n')
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if n.Type == html.ElementNode && blockTags[n.Data] {
			b.WriteByte('\n')
		}
	}
	for _, n := range sel.Nodes {
		walk(n)
	}
	lines := make([]string, 0, 64)
	for line := range strings.SplitSeq(b.String(), "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

func hashText(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := s[:n]
	for i := 0; i < utf8.UTFMax && len(cut) > 0; i++ {
		if r, size := utf8.DecodeLastRuneInString(cut); r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return cut
}
