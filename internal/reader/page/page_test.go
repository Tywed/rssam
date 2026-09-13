package page

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestParseFeedURL(t *testing.T) {
	cases := []struct {
		in       string
		page     string
		selector string
		ok       bool
	}{
		{"page+https://example.com/prices#.price-table", "https://example.com/prices", ".price-table", true},
		{"page+http://example.com/a?x=1##main > .t", "http://example.com/a?x=1", "#main > .t", true},
		{"page+https://example.com/", "https://example.com/", defaultSelector, true},
		{"PAGE+HTTPS://example.com/x#h1", "https://example.com/x", "h1", true},
		{"https://example.com/#x", "", "", false},
		{"page+ftp://example.com/", "", "", false},
		{"page+https:///nohost#x", "", "", false},
	}
	for _, c := range cases {
		got, ok := ParseFeedURL(c.in)
		if ok != c.ok || got.PageURL != c.page || got.Selector != c.selector {
			t.Errorf("%q → %+v ok=%v, want %q %q %v", c.in, got, ok, c.page, c.selector, c.ok)
		}
	}
}

func TestHandler_FetchDetectsChanges(t *testing.T) {
	var version atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		v := version.Load()
		fmt.Fprintf(w, `<html><head><title>Тарифы</title><script>var t=%d</script></head><body>
<nav>menu %d</nav>
<div id="prices"><h2>Тарифы</h2><ul><li>Базовый — 100 ₽</li><li>Про — %d ₽</li></ul><script>track(%d)</script></div>
<footer>© %d</footer></body></html>`, v, v, 200+int(v)*50, v, v)
	}))
	defer ts.Close()
	h := NewHandler(ts.Client(), nil, "test")
	feedURL := "page+" + ts.URL + "/tarify##prices"

	res, err := h.Fetch(context.Background(), feedURL, "", State{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Modified || len(res.Entries) != 1 || res.Title != "Тарифы" {
		t.Fatalf("first poll: %+v", res)
	}
	first := res.Entries[0]
	if !strings.Contains(first.Title, "начальный снимок") || first.URL != ts.URL+"/tarify" || strings.Contains(first.Content, "track(") || strings.Contains(first.Content, "page-diff") {
		t.Fatalf("baseline entry: %+v", first)
	}
	if res.State.Text != "Тарифы\nБазовый — 100 ₽\nПро — 200 ₽" {
		t.Fatalf("snapshot text %q", res.State.Text)
	}

	// Noise outside the selector (nav, footer, scripts) is not a change.
	version.Store(0)
	again, err := h.Fetch(context.Background(), feedURL, "", res.State, false)
	if err != nil || again.Modified || len(again.Entries) != 0 || again.State != res.State {
		t.Fatalf("unchanged: %+v err=%v", again, err)
	}

	version.Store(1)
	changed, err := h.Fetch(context.Background(), feedURL, "", res.State, false)
	if err != nil || !changed.Modified || len(changed.Entries) != 1 {
		t.Fatalf("changed: %+v err=%v", changed, err)
	}
	e := changed.Entries[0]
	if e.Hash == first.Hash || e.Hash == "" {
		t.Fatalf("hash must differ per snapshot: %q vs %q", e.Hash, first.Hash)
	}
	if !strings.Contains(e.Content, "<ins>Про — 250 ₽</ins>") || !strings.Contains(e.Content, "<del>Про — 200 ₽</del>") || strings.Contains(e.Content, "<ins>Базовый") {
		t.Fatalf("diff block: %s", e.Content)
	}
	if !strings.Contains(e.Content, "<li>Про — 250 ₽</li>") {
		t.Fatalf("fragment html must follow the diff: %s", e.Content)
	}

	// Selector without matches is an error (feed goes into backoff, user sees it).
	if _, err := h.Fetch(context.Background(), "page+"+ts.URL+"/tarify#.nope", "", State{}, false); !errors.Is(err, ErrSelectorNoMatch) {
		t.Fatalf("err=%v", err)
	}
	if _, err := h.DiscoverTitle(context.Background(), "page+"+ts.URL+"/tarify#.nope", false); !errors.Is(err, ErrSelectorNoMatch) {
		t.Fatalf("discover err=%v", err)
	}
	if title, err := h.DiscoverTitle(context.Background(), feedURL, false); err != nil || title != "Тарифы" {
		t.Fatalf("title=%q err=%v", title, err)
	}

	bad := "page+" + ts.URL + "/tarify#["
	if _, err := h.Fetch(context.Background(), bad, "", State{}, false); err == nil || !strings.Contains(err.Error(), "invalid CSS selector") {
		t.Fatalf("invalid selector fetch err=%v", err)
	}
	if _, err := h.DiscoverTitle(context.Background(), bad, false); err == nil || !strings.Contains(err.Error(), "invalid CSS selector") {
		t.Fatalf("invalid selector discover err=%v", err)
	}
}

func TestDiffHTML(t *testing.T) {
	if got := diffHTML("a\nb\nc", "a\nb\nc"); got != "" {
		t.Fatalf("no diff expected, got %q", got)
	}
	got := diffHTML("a\nb\nc", "a\nx\nc\nd")
	for _, want := range []string{"<ins>x</ins>", "<ins>d</ins>", "<del>b</del>"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	if strings.Contains(got, "<ins>a</ins>") || strings.Contains(got, "<del>c</del>") {
		t.Errorf("unchanged lines must not appear: %s", got)
	}
	if !strings.Contains(diffHTML("", "<b>&"), "&lt;b&gt;&amp;") {
		t.Error("lines must be escaped")
	}
}

func TestTruncateKeepsUTF8(t *testing.T) {
	s := strings.Repeat("я", 10)
	out := truncate(s, 11)
	if out != strings.Repeat("я", 5) {
		t.Fatalf("got %q", out)
	}
}
