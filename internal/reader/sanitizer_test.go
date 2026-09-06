package reader

import (
	"context"
	"strings"
	"testing"

	"rssam/internal/storage"
)

func TestSanitizeHTML_StripsScripts(t *testing.T) {
	in := `<p>ok</p><script>alert(1)</script><img src=x onerror=alert(1)>`
	got := SanitizeHTML(in)
	if strings.Contains(got, "<script") || strings.Contains(got, "onerror") {
		t.Fatalf("unsafe markup remained: %q", got)
	}
	if !strings.Contains(got, "ok") {
		t.Fatalf("expected safe content, got %q", got)
	}
}

func TestSanitizeHTML_PlainText(t *testing.T) {
	if got := SanitizeHTML("  hello  "); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestSanitizeHTML_VideoKeptWithHTTPSourcesOnly(t *testing.T) {
	in := `<video controls poster="https://cdn.example/p.jpg" style="max-width:100%"><source src="https://cdn.example/v.mp4" type="video/mp4"></video>`
	got := SanitizeHTML(in)
	for _, want := range []string{`<video controls=""`, `poster="https://cdn.example/p.jpg"`, `<source src="https://cdn.example/v.mp4" type="video/mp4">`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in %q", want, got)
		}
	}
	if strings.Contains(got, "style=") {
		t.Fatalf("style must be dropped: %q", got)
	}

	hostile := `<video controls poster="javascript:alert(1)" onplay="alert(2)"><source src="data:text/html,x" type="text/html"></video>`
	got = SanitizeHTML(hostile)
	for _, bad := range []string{"javascript:", "onplay", "data:", "text/html"} {
		if strings.Contains(got, bad) {
			t.Fatalf("%q leaked through: %q", bad, got)
		}
	}
}

// Every handler's output goes through the registry, so content produced by a
// bridge (Telegram HTML is copied verbatim from t.me) is sanitized the same
// way as RSS descriptions.
func TestHandlerRegistry_FetchSanitizesAllHandlers(t *testing.T) {
	h := &staticHandler{content: `<p>ok</p><script>alert(1)</script><img src="https://x/y.png" onerror="alert(2)"><a href="javascript:alert(3)">l</a>`}
	reg := NewHandlerRegistry(h)
	res, err := reg.Fetch(context.Background(), FetchRequest{FeedURL: "https://example.com/feed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("entries=%d", len(res.Entries))
	}
	c := res.Entries[0].Content
	for _, bad := range []string{"<script", "onerror", "javascript:"} {
		if strings.Contains(c, bad) {
			t.Fatalf("%q leaked through registry: %q", bad, c)
		}
	}
	if !strings.Contains(c, "<p>ok</p>") || !strings.Contains(c, `<img src="https://x/y.png"`) {
		t.Fatalf("safe markup lost: %q", c)
	}
}

type staticHandler struct{ content string }

func (s *staticHandler) Name() string                 { return "static" }
func (s *staticHandler) DetectFeedType(string) string { return "static" }
func (s *staticHandler) Fetch(context.Context, FetchRequest) (FetchResponse, error) {
	return FetchResponse{Entries: []storage.CreateEntryParams{{Title: "t", URL: "https://example.com/1", Content: s.content}}}, nil
}
