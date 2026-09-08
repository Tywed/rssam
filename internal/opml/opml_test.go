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
			{Title: "Example", FeedURL: "https://example.com/feed.xml", CategoryID: new(int64(1))},
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

//go:fix inline

func TestSettingsRoundtrip(t *testing.T) {
	retention := 14
	in := ExportInput{
		Title: "settings",
		Feeds: []ExportFeed{
			{
				Title:   "Telegram",
				FeedURL: "https://t.me/s/golang",
				Settings: FeedSettings{
					FeedType:        "telegram",
					IntervalMinutes: 15,
					TLSInsecure:     true,
					FetchViaProxy:   true,
					Crawler:         true,
					StoreHashOnly:   true,
					RetentionDays:   &retention,
					UserAgent:       "custom-ua/1.0",
					WebhookName:     `alerts "prod" & staging`,
					ScraperRules:    "article",
					RewriteRules:    "rewrite-rule(\"a\",\"b\")",
					BlockedRules:    "spam|ads\nline two",
					KeepRules:       "go",
				},
			},
			{Title: "Plain", FeedURL: "https://example.com/rss", Settings: FeedSettings{FeedType: "rss", IntervalMinutes: 0}},
		},
	}
	xml, err := Generate(in)
	if err != nil {
		t.Fatal(err)
	}
	out := string(xml)
	if !strings.Contains(out, `xmlns:rssam="`+Namespace+`"`) {
		t.Fatalf("namespace declaration missing:\n%s", out)
	}
	// A default feed must stay a plain OPML outline.
	plainLine := out[strings.Index(out, `title="Plain"`):]
	plainLine = plainLine[:strings.Index(plainLine, "\n")]
	if strings.Contains(plainLine, "rssam:") {
		t.Fatalf("default feed exported with rssam attributes: %s", plainLine)
	}

	doc, err := ParseBytes(xml)
	if err != nil {
		t.Fatalf("re-parse: %v\n%s", err, out)
	}
	feeds := doc.CollectFeeds()
	if len(feeds) != 2 {
		t.Fatalf("feeds = %d", len(feeds))
	}
	got := feeds[0]
	if len(got.SettingsErrors) != 0 {
		t.Fatalf("settings errors: %v", got.SettingsErrors)
	}
	want := in.Feeds[0].Settings
	if got.Settings.FeedType != want.FeedType || got.Settings.IntervalMinutes != want.IntervalMinutes ||
		!got.Settings.TLSInsecure || !got.Settings.FetchViaProxy || !got.Settings.Crawler || !got.Settings.StoreHashOnly ||
		got.Settings.RetentionDays == nil || *got.Settings.RetentionDays != retention ||
		got.Settings.UserAgent != want.UserAgent || got.Settings.WebhookName != want.WebhookName ||
		got.Settings.ScraperRules != want.ScraperRules || got.Settings.RewriteRules != want.RewriteRules ||
		got.Settings.BlockedRules != want.BlockedRules || got.Settings.KeepRules != want.KeepRules {
		t.Fatalf("settings mismatch:\n got %+v\nwant %+v\n%s", got.Settings, want, out)
	}
	if p := feeds[1].Settings; p != (FeedSettings{}) {
		t.Fatalf("plain feed settings should be zero: %+v", p)
	}
}

func TestSettingsInvalidAttributesReported(t *testing.T) {
	doc, err := ParseBytes([]byte(`<opml version="2.0" xmlns:rssam="` + Namespace + `"><body>
<outline type="rss" text="a" xmlUrl="https://example.com/a" rssam:interval="abc" rssam:tlsInsecure="maybe" rssam:retentionDays="-1" rssam:hashOnly="1"/>
<outline type="rss" text="b" xmlUrl="https://example.com/b" interval="15" tlsInsecure="true"/>
</body></opml>`))
	if err != nil {
		t.Fatal(err)
	}
	feeds := doc.CollectFeeds()
	if len(feeds) != 2 {
		t.Fatalf("feeds = %d", len(feeds))
	}
	a := feeds[0]
	if len(a.SettingsErrors) != 3 {
		t.Fatalf("expected 3 attribute errors, got %v", a.SettingsErrors)
	}
	if a.Settings.IntervalMinutes != 0 || a.Settings.TLSInsecure || a.Settings.RetentionDays != nil || !a.Settings.StoreHashOnly {
		t.Fatalf("invalid attributes must fall back to zero values: %+v", a.Settings)
	}
	// Attributes outside the rssam namespace are foreign and ignored.
	if b := feeds[1]; b.Settings != (FeedSettings{}) || len(b.SettingsErrors) != 0 {
		t.Fatalf("un-namespaced attributes must be ignored: %+v %v", b.Settings, b.SettingsErrors)
	}
}
