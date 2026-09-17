package storage

import (
	"fmt"
	"strings"
	"unicode/utf8"
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

// ftsVectorExprPlaceholders builds to_tsvector(config, title || content)
// from SQL expressions — column names or placeholders such as $3.
// lang must be allowlisted (simple|russian).
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

// ftsPrefixExpr returns SQL for to_tsquery(config, $arg) where $arg holds
// the output of ftsPrefixQuery.
func ftsPrefixExpr(lang string, argPlaceholder string) string {
	if !isAllowedFTSLanguage(lang) {
		lang = DefaultFTSLanguage
	}
	return fmt.Sprintf("to_tsquery('%s', %s)", lang, argPlaceholder)
}

// ftsPrefixQuery renders the words of a plain query as an AND of quoted
// prefix terms in to_tsquery syntax: `постгр нов` → `'постгр':* & 'нов':*`.
// Quoting makes every word a literal, so operator characters inside it are
// data, not syntax; to_tsquery still normalises the word with the
// dictionary, drops stop words and, for words that are only punctuation,
// yields an empty query — never an error (fuzzed with 3 000 random operator
// soups on PostgreSQL 17). Words shorter than ftsPrefixMinRunes stay exact:
// a two-letter prefix matches most of the lexicon (200 000 entries: `ка`
// → 135 000 rows, 1.8 s) for no useful ranking.
func ftsPrefixQuery(q string) string {
	var b strings.Builder
	for i, w := range strings.Fields(q) {
		if i > 0 {
			b.WriteString(" & ")
		}
		b.WriteByte('\'')
		b.WriteString(tsqueryLiteralEscaper.Replace(w))
		b.WriteByte('\'')
		if utf8.RuneCountInString(w) >= ftsPrefixMinRunes {
			b.WriteString(":*")
		}
	}
	return b.String()
}

const ftsPrefixMinRunes = 3

var tsqueryLiteralEscaper = strings.NewReplacer(`\`, `\\`, `'`, `''`)

// HasSearchOperators reports whether a search query uses websearch syntax
// (a quoted phrase, a -excluded word or an OR). Such queries are taken
// literally; only plain words get the prefix match of ftsPrefixQuery.
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
