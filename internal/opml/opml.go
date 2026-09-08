package opml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Namespace is the XML namespace of rssam-specific outline attributes.
// Exported files declare it as xmlns:rssam on <opml>; foreign readers ignore
// the attributes, rssam restores per-feed settings from them on import.
const Namespace = "https://github.com/Tywed/rssam/opml"

// Outline is one OPML outline node (folder or RSS feed).
//
// The rssam:* attributes carry the feed settings that plain OPML cannot
// express. They are optional on input; only the standard attributes are
// needed for a feed to be imported.
type Outline struct {
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr"`
	Type     string    `xml:"type,attr"`
	XMLURL   string    `xml:"xmlUrl,attr"`
	URL      string    `xml:"url,attr"`
	HTMLURL  string    `xml:"htmlUrl,attr"`
	Outlines []Outline `xml:"outline"`

	RssamFeedType      string `xml:"https://github.com/Tywed/rssam/opml feedType,attr"`
	RssamInterval      string `xml:"https://github.com/Tywed/rssam/opml interval,attr"`
	RssamTLSInsecure   string `xml:"https://github.com/Tywed/rssam/opml tlsInsecure,attr"`
	RssamFetchViaProxy string `xml:"https://github.com/Tywed/rssam/opml fetchViaProxy,attr"`
	RssamCrawler       string `xml:"https://github.com/Tywed/rssam/opml crawler,attr"`
	RssamHashOnly      string `xml:"https://github.com/Tywed/rssam/opml hashOnly,attr"`
	RssamRetentionDays string `xml:"https://github.com/Tywed/rssam/opml retentionDays,attr"`
	RssamUserAgent     string `xml:"https://github.com/Tywed/rssam/opml userAgent,attr"`
	RssamWebhook       string `xml:"https://github.com/Tywed/rssam/opml webhook,attr"`
	RssamScraperRules  string `xml:"https://github.com/Tywed/rssam/opml scraperRules,attr"`
	RssamRewriteRules  string `xml:"https://github.com/Tywed/rssam/opml rewriteRules,attr"`
	RssamBlockedRules  string `xml:"https://github.com/Tywed/rssam/opml blockedRules,attr"`
	RssamKeepRules     string `xml:"https://github.com/Tywed/rssam/opml keepRules,attr"`
}

// FeedSettings are the rssam-specific per-feed settings carried in OPML.
// Zero values mean "not specified": the importer keeps its defaults.
type FeedSettings struct {
	FeedType        string
	IntervalMinutes int // 0 = not specified
	TLSInsecure     bool
	FetchViaProxy   bool
	Crawler         bool
	StoreHashOnly   bool
	RetentionDays   *int // nil = not specified
	UserAgent       string
	// WebhookName is the name of the webhook the feed was bound to. Webhook
	// IDs are not portable between instances, so the binding is restored by
	// name on import (and skipped with a report entry when there is no match).
	WebhookName  string
	ScraperRules string
	RewriteRules string
	BlockedRules string
	KeepRules    string
}

// Document is a parsed OPML file.
type Document struct {
	Version string
	Title   string
	Body    []Outline
}

// FeedEntry is a feed discovered during import walk.
type FeedEntry struct {
	Title         string
	FeedURL       string
	CategoryTitle string
	// Settings restored from rssam:* attributes (zero when absent).
	Settings FeedSettings
	// SettingsErrors lists rssam:* attributes that could not be parsed. The
	// feed is still importable; the importer reports them and falls back to
	// defaults for the offending attributes.
	SettingsErrors []string
}

// Parse reads OPML XML from r.
func Parse(r io.Reader) (*Document, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return ParseBytes(data)
}

// ParseBytes parses OPML XML.
func ParseBytes(data []byte) (*Document, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errors.New("empty opml document")
	}

	var raw struct {
		Version string `xml:"version,attr"`
		Head    struct {
			Title string `xml:"title"`
		} `xml:"head"`
		Body struct {
			Outlines []Outline `xml:"outline"`
		} `xml:"body"`
	}
	if err := xml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse opml xml: %w", err)
	}
	if len(raw.Body.Outlines) == 0 {
		return nil, errors.New("opml body has no outlines")
	}
	return &Document{
		Version: strings.TrimSpace(raw.Version),
		Title:   strings.TrimSpace(raw.Head.Title),
		Body:    raw.Body.Outlines,
	}, nil
}

// CollectFeeds walks the outline tree and returns feed entries in document order.
func (d *Document) CollectFeeds() []FeedEntry {
	if d == nil {
		return nil
	}
	var out []FeedEntry
	for _, o := range d.Body {
		collectOutlineFeeds(o, "", &out)
	}
	return out
}

func collectOutlineFeeds(o Outline, categoryTitle string, out *[]FeedEntry) {
	if isRSSOutline(o) {
		feedURL := feedURLFromOutline(o)
		if feedURL == "" {
			return
		}
		settings, errs := settingsFromOutline(o)
		*out = append(*out, FeedEntry{
			Title:          outlineTitle(o),
			FeedURL:        feedURL,
			CategoryTitle:  categoryTitle,
			Settings:       settings,
			SettingsErrors: errs,
		})
		return
	}
	folderTitle := outlineTitle(o)
	if folderTitle == "" && len(o.Outlines) > 0 {
		folderTitle = categoryTitle
	}
	nextCategory := categoryTitle
	if folderTitle != "" {
		nextCategory = folderTitle
	}
	for _, child := range o.Outlines {
		collectOutlineFeeds(child, nextCategory, out)
	}
}

func isRSSOutline(o Outline) bool {
	if strings.EqualFold(strings.TrimSpace(o.Type), "rss") {
		return true
	}
	if strings.TrimSpace(o.XMLURL) != "" {
		return true
	}
	return false
}

func feedURLFromOutline(o Outline) string {
	if u := strings.TrimSpace(o.XMLURL); u != "" {
		return u
	}
	return strings.TrimSpace(o.URL)
}

func settingsFromOutline(o Outline) (FeedSettings, []string) {
	var (
		fs   FeedSettings
		errs []string
	)
	fs.FeedType = strings.ToLower(strings.TrimSpace(o.RssamFeedType))
	if v := strings.TrimSpace(o.RssamInterval); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			errs = append(errs, "rssam:interval: expected a positive integer, got "+strconv.Quote(v))
		} else {
			fs.IntervalMinutes = n
		}
	}
	if v := strings.TrimSpace(o.RssamRetentionDays); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			errs = append(errs, "rssam:retentionDays: expected a non-negative integer, got "+strconv.Quote(v))
		} else if n > 0 {
			fs.RetentionDays = &n
		}
	}
	boolAttr := func(name, raw string, dst *bool) {
		v := strings.TrimSpace(raw)
		if v == "" {
			return
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			errs = append(errs, "rssam:"+name+": expected true/false, got "+strconv.Quote(v))
			return
		}
		*dst = b
	}
	boolAttr("tlsInsecure", o.RssamTLSInsecure, &fs.TLSInsecure)
	boolAttr("fetchViaProxy", o.RssamFetchViaProxy, &fs.FetchViaProxy)
	boolAttr("crawler", o.RssamCrawler, &fs.Crawler)
	boolAttr("hashOnly", o.RssamHashOnly, &fs.StoreHashOnly)
	fs.UserAgent = strings.TrimSpace(o.RssamUserAgent)
	fs.WebhookName = strings.TrimSpace(o.RssamWebhook)
	fs.ScraperRules = strings.TrimSpace(o.RssamScraperRules)
	fs.RewriteRules = strings.TrimSpace(o.RssamRewriteRules)
	fs.BlockedRules = strings.TrimSpace(o.RssamBlockedRules)
	fs.KeepRules = strings.TrimSpace(o.RssamKeepRules)
	return fs, errs
}

func outlineTitle(o Outline) string {
	if t := strings.TrimSpace(o.Title); t != "" {
		return t
	}
	return strings.TrimSpace(o.Text)
}

// ExportInput is data for OPML generation.
type ExportInput struct {
	Title      string
	Categories []ExportCategory
	Feeds      []ExportFeed
}

// ExportCategory is a category folder in export.
type ExportCategory struct {
	ID    int64
	Title string
}

// ExportFeed is a feed row in export.
type ExportFeed struct {
	Title      string
	FeedURL    string
	CategoryID *int64
	// Settings are written as rssam:* attributes; zero values are omitted so
	// a default feed exports as a plain OPML outline.
	Settings FeedSettings
}

// Generate builds OPML 2.0 XML for subscriptions export.
func Generate(in ExportInput) ([]byte, error) {
	title := strings.TrimSpace(in.Title)
	if title == "" {
		title = "rssam subscriptions"
	}

	byCategory := make(map[int64][]ExportFeed)
	var uncategorized []ExportFeed
	for _, f := range in.Feeds {
		if f.CategoryID != nil {
			byCategory[*f.CategoryID] = append(byCategory[*f.CategoryID], f)
			continue
		}
		uncategorized = append(uncategorized, f)
	}

	var body bytes.Buffer
	body.WriteString("  <body>\n")
	for _, c := range in.Categories {
		feeds := byCategory[c.ID]
		if len(feeds) == 0 {
			continue
		}
		fmt.Fprintf(&body, "    <outline text=%s title=%s>\n", xmlAttr(c.Title), xmlAttr(c.Title))
		for _, f := range feeds {
			writeFeedOutline(&body, f, "      ")
		}
		body.WriteString("    </outline>\n")
		delete(byCategory, c.ID)
	}
	for _, feeds := range byCategory {
		for _, f := range feeds {
			writeFeedOutline(&body, f, "    ")
		}
	}
	for _, f := range uncategorized {
		writeFeedOutline(&body, f, "    ")
	}
	body.WriteString("  </body>\n")

	now := time.Now().UTC().Format(time.RFC1123)
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	fmt.Fprintf(&buf, `<opml version="2.0" xmlns:rssam="`+Namespace+`">
  <head>
    <title>%s</title>
    <dateCreated>%s</dateCreated>
  </head>
`, xmlEscape(title), xmlEscape(now))
	buf.Write(body.Bytes())
	buf.WriteString("</opml>\n")
	return buf.Bytes(), nil
}

func writeFeedOutline(b *bytes.Buffer, f ExportFeed, indent string) {
	title := strings.TrimSpace(f.Title)
	if title == "" {
		title = f.FeedURL
	}
	siteURL := f.FeedURL
	fmt.Fprintf(b, `%s<outline type="rss" text=%s title=%s xmlUrl=%s htmlUrl=%s`, indent, xmlAttr(title), xmlAttr(title), xmlAttr(f.FeedURL), xmlAttr(siteURL))
	writeSettingsAttrs(b, f.Settings)
	b.WriteString("/>\n")
}

// writeSettingsAttrs appends rssam:* attributes for every non-default
// setting. "rss" is the default feed type and is omitted like the other
// zero values.
func writeSettingsAttrs(b *bytes.Buffer, s FeedSettings) {
	attr := func(name, value string) {
		if value == "" {
			return
		}
		fmt.Fprintf(b, " rssam:%s=%s", name, xmlAttr(value))
	}
	if ft := strings.ToLower(strings.TrimSpace(s.FeedType)); ft != "" && ft != "rss" {
		attr("feedType", ft)
	}
	if s.IntervalMinutes > 0 {
		attr("interval", strconv.Itoa(s.IntervalMinutes))
	}
	if s.TLSInsecure {
		attr("tlsInsecure", "true")
	}
	if s.FetchViaProxy {
		attr("fetchViaProxy", "true")
	}
	if s.Crawler {
		attr("crawler", "true")
	}
	if s.StoreHashOnly {
		attr("hashOnly", "true")
	}
	if s.RetentionDays != nil && *s.RetentionDays > 0 {
		attr("retentionDays", strconv.Itoa(*s.RetentionDays))
	}
	attr("userAgent", s.UserAgent)
	attr("webhook", s.WebhookName)
	attr("scraperRules", s.ScraperRules)
	attr("rewriteRules", s.RewriteRules)
	attr("blockedRules", s.BlockedRules)
	attr("keepRules", s.KeepRules)
}

func xmlAttr(s string) string {
	return `"` + xmlEscape(strings.TrimSpace(s)) + `"`
}

func xmlEscape(s string) string {
	var buf strings.Builder
	if err := xml.EscapeText(&buf, []byte(s)); err != nil {
		return s
	}
	return buf.String()
}
