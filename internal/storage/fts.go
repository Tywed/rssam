package storage

import (
	"fmt"
	"strings"
)

// DefaultFTSLanguage is the PostgreSQL text search config used when FTS_LANGUAGE is unset.
const DefaultFTSLanguage = "simple"

// NormalizeFTSLanguage maps env/user input to a PostgreSQL text search configuration name.
// Supported: simple (default), russian (ru).
func NormalizeFTSLanguage(lang string) string {
	switch strings.ToLower(strings.TrimSpace(lang)) {
	case "", "simple":
		return "simple"
	case "russian", "ru":
		return "russian"
	default:
		return DefaultFTSLanguage
	}
}

func isAllowedFTSLanguage(lang string) bool {
	switch lang {
	case "simple", "russian":
		return true
	default:
		return false
	}
}

// ftsIndexedChars caps the text handed to to_tsvector. A tsvector must stay
// under 1 MiB (PostgreSQL raises 54000 otherwise), and one oversized item
// would fail the whole batch INSERT — every entry of that poll — and drive
// the feed into error backoff. Measured worst case (unique hyphenated
// tokens, which the parser indexes both whole and in parts): 150 000 chars
// → ~580 KB of tsvector; ordinary prose is ~2.3 bytes per char.
const ftsIndexedChars = 150000

// ftsVectorExpr returns SQL for to_tsvector(config, title || content) using table columns.
// Use in UPDATE/SELECT only — not inside INSERT VALUES.
// lang must be allowlisted (simple|russian).
func ftsVectorExpr(lang string) string {
	return ftsVectorExprPlaceholders(lang, "title", "content")
}

// ftsVectorExprPlaceholders builds to_tsvector from SQL placeholders (e.g. $3, $5) for INSERT VALUES.
func ftsVectorExprPlaceholders(lang, titlePlaceholder, contentPlaceholder string) string {
	if !isAllowedFTSLanguage(lang) {
		lang = DefaultFTSLanguage
	}
	return fmt.Sprintf("to_tsvector('%s', left(coalesce(%s, '') || ' ' || coalesce(%s, ''), %d))", lang, titlePlaceholder, contentPlaceholder, ftsIndexedChars)
}

// ftsWebsearchExpr returns SQL for websearch_to_tsquery(config, $arg):
// "quoted phrase", -excluded, OR. Unlike to_tsquery it never raises on
// malformed input (unbalanced quotes, stray operators) — verified on
// PostgreSQL 17 with 2 000 random operator soups; garbage degrades to a
// NOTICE and an empty query.
func ftsWebsearchExpr(lang string, argPlaceholder string) string {
	if !isAllowedFTSLanguage(lang) {
		lang = DefaultFTSLanguage
	}
	return fmt.Sprintf("websearch_to_tsquery('%s', %s)", lang, argPlaceholder)
}

// HasSearchOperators reports whether a search query uses websearch syntax
// (a quoted phrase, a -excluded word or an OR). Such queries are answered by
// the tsvector index alone: a substring fallback over the raw text would be
// meaningless for them.
func HasSearchOperators(q string) bool {
	if strings.Contains(q, `"`) {
		return true
	}
	for _, w := range strings.Fields(q) {
		if len(w) > 1 && w[0] == '-' {
			return true
		}
		if strings.EqualFold(w, "or") {
			return true
		}
	}
	return false
}
