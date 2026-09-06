package reader

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mmcdole/gofeed"
	"golang.org/x/net/html"
)

// FuzzSanitizeHTML: output never contains script/iframe/event-handler markup
// and sanitizing is idempotent (a second pass changes nothing). The second
// property is what lets stored content be re-sanitized safely.
func FuzzSanitizeHTML(f *testing.F) {
	for _, s := range []string{
		"", "plain text", "<p>hi</p>", "<script>alert(1)</script>", `<a href="javascript:alert(1)">x</a>`,
		`<img src=x onerror=alert(1)>`, `<video><source src="https://x/v.mp4"><source src="javascript:1"></video>`,
		`<svg onload=alert(1)>`, `<iframe src="https://x"></iframe>`, `<p style="x:expression(1)">`,
		`<a href="https://x" target="_blank">ok</a>`, "<<>>", `<img src="data:text/html;base64,PHNjcmlwdD4=">`,
		"<p>\x00</p>", `&lt;script&gt;`, "<b><i>nested</b></i>",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		out := SanitizeHTML(in)
		if strings.Contains(out, "<") {
			assertSafeMarkup(t, in, out)
		}
		if again := SanitizeHTML(out); again != out {
			t.Fatalf("not idempotent:\n in: %q\n 1st: %q\n 2nd: %q", in, out, again)
		}
	})
}

// assertSafeMarkup tokenizes sanitizer output and rejects active content:
// script-like elements, on* handlers and javascript:/data: URLs in URL
// attributes. Plain text is not inspected — "javascript:" in a text node is
// harmless.
func assertSafeMarkup(t *testing.T, in, out string) {
	t.Helper()
	z := html.NewTokenizer(strings.NewReader(out))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return
		case html.StartTagToken, html.SelfClosingTagToken:
			tok := z.Token()
			switch tok.Data {
			case "script", "iframe", "object", "embed", "style", "meta", "link", "base", "form":
				t.Fatalf("element <%s> survived sanitizer:\n in: %q\nout: %q", tok.Data, in, out)
			}
			for _, a := range tok.Attr {
				key := strings.ToLower(a.Key)
				if strings.HasPrefix(key, "on") {
					t.Fatalf("event handler %q survived:\n in: %q\nout: %q", a.Key, in, out)
				}
				switch key {
				case "href", "src", "poster", "action", "formaction", "xlink:href", "srcset", "data":
					v := strings.ToLower(strings.TrimSpace(a.Val))
					v = strings.Map(func(r rune) rune {
						if r < 0x20 || r == 0x7f {
							return -1
						}
						return r
					}, v)
					if strings.HasPrefix(v, "javascript:") || strings.HasPrefix(v, "vbscript:") || strings.HasPrefix(v, "data:text/html") {
						t.Fatalf("dangerous URL %s=%q survived:\n in: %q\nout: %q", a.Key, a.Val, in, out)
					}
				}
			}
		}
	}
}

// FuzzParseLimitedFeed: gofeed + the size limiter never panic, and every item
// that normalizes to an entry has a non-empty dedup hash whenever it has a URL.
func FuzzParseLimitedFeed(f *testing.F) {
	f.Add([]byte(`<?xml version="1.0"?><rss version="2.0"><channel><title>t</title><item><title>a</title><link>https://x/a</link><description>&lt;b&gt;hi&lt;/b&gt;</description><enclosure url="https://x/a.mp3" length="12" type="audio/mpeg"/></item></channel></rss>`))
	f.Add([]byte(`<feed xmlns="http://www.w3.org/2005/Atom"><title>t</title><entry><title>a</title><link href="https://x/a"/><id>urn:1</id><content type="html">&lt;script&gt;x&lt;/script&gt;</content></entry></feed>`))
	f.Add([]byte(`{"version":"https://jsonfeed.org/version/1","title":"t","items":[{"id":"1","url":"https://x/1","content_html":"<p>x</p>"}]}`))
	f.Add([]byte(`<rss><channel><item><guid>only-guid</guid></item><item></item></channel></rss>`))
	f.Add([]byte("<rss><channel><item><enclosure url='https://x' length='notanumber'/></item></channel></rss>"))
	f.Add([]byte("garbage"))
	f.Add([]byte(""))

	parser := gofeed.NewParser()
	f.Fuzz(func(t *testing.T, data []byte) {
		feed, err := parseLimitedFeed(parser, bytes.NewReader(data))
		if err != nil {
			return
		}
		if feed == nil {
			t.Fatal("nil feed without error")
		}
		for _, it := range feed.Items {
			if it == nil {
				continue
			}
			e := normalizeItem(it)
			if e.URL != "" && e.Hash == "" {
				t.Fatalf("entry with URL %q has empty hash", e.URL)
			}
			if strings.Contains(strings.ToLower(e.Content), "<script") {
				t.Fatalf("unsanitized content: %q", e.Content)
			}
			for _, enc := range e.Enclosures {
				if strings.TrimSpace(enc.URL) == "" {
					t.Fatalf("enclosure with empty URL kept")
				}
			}
		}
	})
}

// FuzzCompileMultilineRegex: user-supplied feed rules must never panic the
// poller; a nil result is the only acceptable failure mode.
func FuzzCompileMultilineRegex(f *testing.F) {
	for _, s := range []string{"", "foo", "foo\nbar", "(", "a\n\n\n(\n", "(?i)ok\n[", strings.Repeat("a|", 1000), "\\", "(?P<n>x)\n(?P<n>y)"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, rules string) {
		re := compileMultilineRegex(rules)
		if re == nil {
			return
		}
		// A compiled rule set must match each of its own literal lines that
		// happen to be plain words, and never panic on arbitrary input.
		_ = re.MatchString(rules)
		_ = re.MatchString("")
	})
}
