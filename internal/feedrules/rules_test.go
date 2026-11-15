package feedrules

import (
	"errors"
	"strings"
	"testing"
)

func TestCompileRewrites_PairsCRLFAndEmptyReplacement(t *testing.T) {
	// CRLF from a textarea; an empty replacement deletes the match; the
	// trailing newline of the textarea is not a line.
	rw, err := CompileRewrites("\\?utm_source=\\w+\r\n\r\n/r/(\\d+)\r\n/article/$1\r\n")
	if err != nil || len(rw) != 2 {
		t.Fatalf("rw=%v err=%v", rw, err)
	}
	r := &Rules{Rewrites: rw}
	if got := r.Apply("https://x/r/42?utm_source=rss"); got != "https://x/article/42" {
		t.Fatalf("got %q", got)
	}
}

func TestCompileRewrites_Errors(t *testing.T) {
	cases := map[string]string{
		"a\nb\nc":   "rewrite_rules line 3: pattern without a replacement line",
		"(\nx":      "rewrite_rules line 1: error parsing regexp",
		"\nrepl\na": "rewrite_rules line 1: empty pattern",
	}
	for in, want := range cases {
		_, err := CompileRewrites(in)
		if err == nil || !strings.HasPrefix(err.Error(), want) {
			t.Errorf("%q: err=%v want prefix %q", in, err, want)
		}
		var le *LineError
		if !errors.As(err, &le) {
			t.Errorf("%q: not a LineError", in)
		}
	}
	if rw, err := CompileRewrites("\n\n"); err != nil || len(rw) != 0 {
		t.Fatalf("blank text: rw=%v err=%v", rw, err)
	}
}

func TestCompileMatch(t *testing.T) {
	re, err := CompileMatch("blocked_rules", "spam\r\n\r\n(?i)promo\n")
	if err != nil || re == nil || !re.MatchString("PROMO") || re.MatchString("news") {
		t.Fatalf("re=%v err=%v", re, err)
	}
	if _, err := CompileMatch("keep_rules", "ok\n[\n"); err == nil || err.Error() != "keep_rules line 2: error parsing regexp: missing closing ]: `[`" {
		t.Fatalf("err=%v", err)
	}
	if re, err := CompileMatch("keep_rules", "  \n"); err != nil || re != nil {
		t.Fatalf("blank: re=%v err=%v", re, err)
	}
	if _, err := CompileMatch("keep_rules", strings.Repeat("a", MaxRulesBytes+1)); err == nil {
		t.Fatal("length limit")
	}
}

func TestValidateScraper(t *testing.T) {
	if err := ValidateScraper("content=article .body\r\ndiv#main\n"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateScraper("article\ncontent=div[\n"); err == nil || !strings.HasPrefix(err.Error(), "scraper_rules line 2:") {
		t.Fatalf("err=%v", err)
	}
	if err := ValidateScraper("content="); err == nil {
		t.Fatal("empty selector")
	}
}

// An invalid text saved before validation stays inert, the others apply;
// the compiled set is reused.
func TestCached_PartialAndReused(t *testing.T) {
	a := Cached("(", "keep", "x\ny")
	if a.Blocked != nil || a.Keep == nil || len(a.Rewrites) != 1 {
		t.Fatalf("%+v", a)
	}
	if b := Cached("(", "keep", "x\ny"); b != a {
		t.Fatal("not cached")
	}
	if Cached("", "", "") != nil {
		t.Fatal("empty texts must be nil")
	}
	if (*Rules)(nil).Apply("s") != "s" || !(*Rules)(nil).Empty() {
		t.Fatal("nil rules")
	}
}
