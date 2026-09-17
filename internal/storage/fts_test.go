package storage

import "testing"

func TestNormalizeFTSLanguage(t *testing.T) {
	cases := map[string]string{
		"":        "simple",
		"simple":  "simple",
		"SIMPLE":  "simple",
		"russian": "russian",
		"ru":      "russian",
		"english": "simple",
		"unknown": "simple",
	}
	for in, want := range cases {
		if got := NormalizeFTSLanguage(in); got != want {
			t.Fatalf("NormalizeFTSLanguage(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestFTSVectorExpr_allowlisted(t *testing.T) {
	const want = "to_tsvector('simple', left(coalesce(title, '') || ' ' || coalesce(content, ''), 150000))"
	expr := ftsVectorExprPlaceholders("simple", "title", "content")
	if expr != want {
		t.Fatalf("unexpected expr: %s", expr)
	}
	expr = ftsVectorExprPlaceholders("'; DROP TABLE entries; --", "title", "content")
	if expr != want {
		t.Fatalf("unexpected sanitized expr: %s", expr)
	}
}

func TestFTSWebsearchExpr_allowlisted(t *testing.T) {
	expr := ftsWebsearchExpr("russian", "$1")
	if expr != "websearch_to_tsquery('russian', $1)" {
		t.Fatalf("unexpected expr: %s", expr)
	}
}

func TestHasSearchOperators(t *testing.T) {
	cases := map[string]bool{
		"газпром":          false,
		"северный поток":   false,
		"a-b":              false,
		"-":                false,
		"orange":           false,
		"corridor":         false,
		`"северный поток"`: true,
		"газпром -акции":   true,
		"нефть or газ":     true,
		"нефть OR газ":     true,
		"a -b":             true,
	}
	for q, want := range cases {
		if got := HasSearchOperators(q); got != want {
			t.Errorf("HasSearchOperators(%q) = %v, want %v", q, got, want)
		}
	}
}
