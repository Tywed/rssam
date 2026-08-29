package dzen

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
)

type NewsItem struct {
	Title       string
	URL         string
	Summary     string
	Author      string
	TimeText    string
	DocID       string
	PubDateUnix int64
}

type neoPayload struct {
	DataSource struct {
		News struct {
			Stories []struct {
				Docs []neoDoc `json:"docs"`
			} `json:"stories"`
		} `json:"news"`
	} `json:"dataSource"`
}

type neoDoc struct {
	URL                 string                  `json:"url"`
	Time                string                  `json:"time"`
	SourceName          string                  `json:"sourceName"`
	DocID               string                  `json:"docId"`
	Title               []textFragment          `json:"title"`
	Text                []textFragment          `json:"text"`
	StudioDocumentStats *studioDocumentStats     `json:"studioDocumentStats"`
}

type studioDocumentStats struct {
	DocURL     string `json:"docUrl"`
	DocPubDate int64  `json:"docPubDate"`
}

type textFragment struct {
	Text string `json:"text"`
}

func ParseSearchHTML(body []byte) ([]NewsItem, error) {
	raw, err := extractNeoJSON(string(body))
	if err != nil {
		return nil, err
	}
	var payload neoPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("dzen: decode payload: %w", err)
	}

	out := make([]NewsItem, 0)
	for _, story := range payload.DataSource.News.Stories {
		for _, doc := range story.Docs {
			item := itemFromDoc(doc)
			if item.Title == "" || item.URL == "" {
				continue
			}
			out = append(out, item)
		}
	}
	if len(out) == 0 {
		return nil, errEmptyResults
	}
	return out, nil
}

func itemFromDoc(doc neoDoc) NewsItem {
	title := joinFragments(doc.Title)
	summary := joinFragments(doc.Text)
	entryURL := strings.TrimSpace(doc.URL)
	var pubDate int64
	if st := doc.StudioDocumentStats; st != nil {
		if u := strings.TrimSpace(st.DocURL); u != "" {
			entryURL = u
		}
		if st.DocPubDate > 0 {
			pubDate = st.DocPubDate
		}
	}
	return NewsItem{
		Title:       strings.TrimSpace(title),
		URL:         entryURL,
		Summary:     strings.TrimSpace(summary),
		Author:      strings.TrimSpace(doc.SourceName),
		TimeText:    strings.TrimSpace(doc.Time),
		DocID:       strings.TrimSpace(doc.DocID),
		PubDateUnix: pubDate,
	}
}

func joinFragments(parts []textFragment) string {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}

func BuildContentHTML(item NewsItem) string {
	var b strings.Builder
	if u := strings.TrimSpace(item.URL); u != "" {
		label := "Читать оригинал"
		if src := strings.TrimSpace(item.Author); src != "" {
			label = "Читать на " + src
		}
		b.WriteString(`<p><a href="`)
		b.WriteString(html.EscapeString(u))
		b.WriteString(`" rel="noopener noreferrer" target="_blank">`)
		b.WriteString(html.EscapeString(label))
		b.WriteString(` ↗</a></p>`)
	}
	if summary := strings.TrimSpace(item.Summary); summary != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("<p>")
		b.WriteString(html.EscapeString(summary))
		b.WriteString("</p>")
	}
	return b.String()
}

func extractNeoJSON(page string) ([]byte, error) {
	const marker = "window.Ya.Neo=window.Ya.Neo||{};window.Ya.Neo="
	idx := strings.Index(page, marker)
	if idx < 0 {
		return nil, errNeoPayloadNotFound
	}
	start := idx + len(marker)
	if start >= len(page) || page[start] != '{' {
		return nil, errNeoPayloadNotFound
	}

	level := 0
	inStr := false
	esc := false
	for i := start; i < len(page); i++ {
		c := page[i]
		if inStr {
			if esc {
				esc = false
				continue
			}
			if c == '\\' {
				esc = true
				continue
			}
			if c == '"' {
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			level++
		case '}':
			level--
			if level == 0 {
				return []byte(page[start : i+1]), nil
			}
		}
	}
	return nil, errNeoPayloadNotFound
}
