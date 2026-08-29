package scraper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rssam/internal/ssrf"
)

const sampleHTML = `<!DOCTYPE html>
<html><head><title>Sample</title></head>
<body>
<nav>Menu</nav>
<article class="post">
  <h1>Hello World</h1>
  <div class="entry-content"><p>First paragraph.</p><p>Second paragraph.</p></div>
</article>
<footer>Footer</footer>
</body></html>`

func TestApplyRewriteRules(t *testing.T) {
	in := "<p>Visit https://track.example.com/click?id=1 for details.</p>"
	rules := `https://track\.example\.com/[^\s"]+
https://example.com/article`
	got := ApplyRewriteRules(in, rules)
	want := "<p>Visit https://example.com/article for details.</p>"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestApplyRewriteRules_InvalidPairsIgnored(t *testing.T) {
	in := "abc"
	got := ApplyRewriteRules(in, "only-one-line")
	if got != in {
		t.Fatalf("got %q", got)
	}
}

func TestExtractContent_ScraperRulesMiniflux(t *testing.T) {
	rules := "content=.entry-content"
	got, err := ExtractContent(sampleHTML, rules)
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(got, "First paragraph", "Second paragraph") {
		t.Fatalf("unexpected content: %s", got)
	}
	if containsAll(got, "Menu", "Footer") {
		t.Fatalf("should not include nav/footer: %s", got)
	}
}

func TestExtractContent_PlainSelector(t *testing.T) {
	got, err := ExtractContent(sampleHTML, "article.post")
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(got, "Hello World", "First paragraph") {
		t.Fatalf("unexpected: %s", got)
	}
}

func TestExtractContent_FixtureFile(t *testing.T) {
	path := filepath.Join("testdata", "sample.html")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skip("testdata not found:", err)
	}
	got, err := ExtractContent(string(data), "content=#main")
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(got, "Fixture article body") {
		t.Fatalf("unexpected: %s", got)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !contains(s, p) {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return strings.Contains(s, sub)
}

func TestScrapePage_StripsMaliciousScript(t *testing.T) {
	page := `<html><body><div class="entry-content"><p>Article</p><script>alert(1)</script><img src=x onerror=alert(1)></div></body></html>`
	pageSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(page))
	}))
	t.Cleanup(pageSrv.Close)

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	f := NewFetcher(Options{
		HTTPClient:   &http.Client{Timeout: 2 * time.Second},
		UserAgent:    "rssam-test",
		MaxBodyBytes: 1 << 20,
		SSRFGuard:    guard,
	})

	got, err := f.ScrapePage(context.Background(), pageSrv.URL, "content=.entry-content", "", "rssam-test", false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "<script") || strings.Contains(got, "onerror") {
		t.Fatalf("unsafe markup remained: %q", got)
	}
	if !contains(got, "Article") {
		t.Fatalf("expected safe content, got %q", got)
	}
}
