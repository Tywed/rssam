package max

import (
	"strings"
	"testing"
)

func TestTitleFromText(t *testing.T) {
	if got := TitleFromText("First line\nSecond", 140); got != "First line" {
		t.Fatalf("got %q", got)
	}
	title := TitleFromText(strings.Repeat("А", 20), 10)
	if len([]rune(title)) > 10 {
		t.Fatalf("title too long: %d runes", len([]rune(title)))
	}
}

func TestSanitizeForXML_StripsInvalid(t *testing.T) {
	s := sanitizeForXML("ok\x00bad")
	if s != "okbad" {
		t.Fatalf("got %q", s)
	}
}
