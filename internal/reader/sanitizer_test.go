package reader

import (
	"strings"
	"testing"
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
