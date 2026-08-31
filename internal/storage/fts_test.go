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
	expr := ftsVectorExpr("simple")
	if expr != "to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, ''))" {
		t.Fatalf("unexpected expr: %s", expr)
	}
	expr = ftsVectorExpr("'; DROP TABLE entries; --")
	if expr != "to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(content, ''))" {
		t.Fatalf("unexpected sanitized expr: %s", expr)
	}
}

func TestFTSWebsearchExpr_allowlisted(t *testing.T) {
	expr := ftsWebsearchExpr("russian", "$1")
	if expr != "plainto_tsquery('russian', $1)" {
		t.Fatalf("unexpected expr: %s", expr)
	}
}
