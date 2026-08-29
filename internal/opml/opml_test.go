package opml

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSampleFixture(t *testing.T) {
	path := filepath.Join("testdata", "sample.opml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseBytes(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	feeds := doc.CollectFeeds()
	if len(feeds) != 3 {
		t.Fatalf("expected 3 feeds, got %d: %+v", len(feeds), feeds)
	}

	byURL := make(map[string]FeedEntry, len(feeds))
	for _, f := range feeds {
		byURL[f.FeedURL] = f
	}

	goFeed, ok := byURL["https://go.dev/blog/.atom"]
	if !ok {
		t.Fatal("missing go blog feed")
	}
	if goFeed.CategoryTitle != "Go" {
		t.Fatalf("go feed category: got %q want Go", goFeed.CategoryTitle)
	}
	if goFeed.Title != "Go Blog" {
		t.Fatalf("go feed title: got %q", goFeed.Title)
	}

	ex, ok := byURL["https://example.com/feed.xml"]
	if !ok {
		t.Fatal("missing example feed")
	}
	if ex.CategoryTitle != "Tech" {
		t.Fatalf("example category: got %q want Tech", ex.CategoryTitle)
	}

	root, ok := byURL["https://news.example.com/rss"]
	if !ok {
		t.Fatal("missing root feed")
	}
	if root.CategoryTitle != "" {
		t.Fatalf("root feed category should be empty, got %q", root.CategoryTitle)
	}
}

func TestGenerateRoundtripStructure(t *testing.T) {
	xml, err := Generate(ExportInput{
		Title: "export",
		Categories: []ExportCategory{
			{ID: 1, Title: "Tech"},
		},
		Feeds: []ExportFeed{
			{Title: "Example", FeedURL: "https://example.com/feed.xml", CategoryID: int64Ptr(1)},
			{Title: "Root", FeedURL: "https://news.example.com/rss"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(xml), `type="rss"`) {
		t.Fatalf("expected rss outline in export: %s", xml)
	}
	doc, err := ParseBytes(xml)
	if err != nil {
		t.Fatalf("re-parse export: %v", err)
	}
	feeds := doc.CollectFeeds()
	if len(feeds) != 2 {
		t.Fatalf("expected 2 feeds after export parse, got %d", len(feeds))
	}
}

func int64Ptr(v int64) *int64 { return &v }
