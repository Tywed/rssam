package opml

import (
	"os"
	"strings"
	"testing"
)

// FuzzParseBytes: arbitrary bytes never panic; a successful parse yields a
// document whose collected feeds all have a non-empty URL, and generating
// OPML from those feeds parses back to the same feed URLs (round-trip).
func FuzzParseBytes(f *testing.F) {
	sample, err := os.ReadFile("testdata/sample.opml")
	if err != nil {
		f.Fatal(err)
	}
	f.Add(sample)
	f.Add([]byte(`<opml version="2.0"><body><outline text="a" xmlUrl="http://x/a.xml"/></body></opml>`))
	f.Add([]byte(`<opml><body><outline text="folder"><outline type="rss" url="http://x/b"/></outline></body></opml>`))
	f.Add([]byte(`<opml><body></body></opml>`))
	f.Add([]byte(`<?xml version="1.0"?><opml><body><outline text="&lt;&amp;" xmlUrl="http://x/?a=1&amp;b=2"/></body></opml>`))
	f.Add([]byte(`<opml><body><outline xmlUrl="   "/></body></opml>`))
	f.Add([]byte("\xff\xfe<opml>"))
	f.Add([]byte(`<!DOCTYPE x [<!ENTITY e "e">]><opml><body><outline text="&e;" xmlUrl="http://x"/></body></opml>`))

	f.Fuzz(func(t *testing.T, data []byte) {
		doc, err := ParseBytes(data)
		if err != nil {
			if doc != nil {
				t.Fatalf("error with non-nil doc: %v", err)
			}
			return
		}
		if doc == nil || len(doc.Body) == 0 {
			t.Fatalf("nil/empty document without error")
		}
		feeds := doc.CollectFeeds()
		in := ExportInput{Title: doc.Title}
		for _, fe := range feeds {
			if strings.TrimSpace(fe.FeedURL) == "" {
				t.Fatalf("collected feed with empty URL: %+v", fe)
			}
			in.Feeds = append(in.Feeds, ExportFeed{Title: fe.Title, FeedURL: fe.FeedURL})
		}
		if len(feeds) == 0 {
			return
		}
		out, err := Generate(in)
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		back, err := ParseBytes(out)
		if err != nil {
			t.Fatalf("re-parse generated OPML: %v\n%s", err, out)
		}
		got := back.CollectFeeds()
		if len(got) != len(feeds) {
			t.Fatalf("round-trip feed count %d != %d\n%s", len(got), len(feeds), out)
		}
		for i := range feeds {
			if strings.TrimSpace(got[i].FeedURL) != strings.TrimSpace(feeds[i].FeedURL) {
				t.Fatalf("round-trip feed %d: %q != %q", i, got[i].FeedURL, feeds[i].FeedURL)
			}
		}
	})
}
