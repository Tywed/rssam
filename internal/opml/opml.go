package opml

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Outline is one OPML outline node (folder or RSS feed).
type Outline struct {
	Text     string    `xml:"text,attr"`
	Title    string    `xml:"title,attr"`
	Type     string    `xml:"type,attr"`
	XMLURL   string    `xml:"xmlUrl,attr"`
	URL      string    `xml:"url,attr"`
	HTMLURL  string    `xml:"htmlUrl,attr"`
	Outlines []Outline `xml:"outline"`
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
		*out = append(*out, FeedEntry{
			Title:         outlineTitle(o),
			FeedURL:       feedURL,
			CategoryTitle: categoryTitle,
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
	fmt.Fprintf(&buf, `<opml version="2.0">
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
	fmt.Fprintf(b, `%s<outline type="rss" text=%s title=%s xmlUrl=%s htmlUrl=%s/>
`, indent, xmlAttr(title), xmlAttr(title), xmlAttr(f.FeedURL), xmlAttr(siteURL))
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
