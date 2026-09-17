//go:build integration

package storage

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestIntegration_SearchEntriesWebsearchOperators(t *testing.T) {
	store := isolatedStore(t)
	store.ftsLanguage = "russian"
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "websearch")
	feed := newFeedForUser(t, store, owner.ID, "websearch", 60)

	texts := []string{
		"Газпром объявил дивиденды",
		"Акции Газпрома выросли",
		"Северный поток остановлен",
		"Поток северный ветер",
		"Нефть подорожала",
	}
	params := make([]CreateEntryParams, 0, len(texts))
	for i, tx := range texts {
		params = append(params, CreateEntryParams{Title: tx, Content: "<p>" + tx + "</p>", URL: fmt.Sprintf("https://example.com/ws/%d", i), Hash: fmt.Sprintf("ws-%d", i)})
	}
	if _, _, err := store.CreateEntries(ctx, feed.ID, params); err != nil {
		t.Fatal(err)
	}

	search := func(q string) []string {
		t.Helper()
		rows, _, err := store.SearchEntries(ctx, owner.ID, SearchEntriesFilter{Query: q, FeedID: &feed.ID, Limit: 10})
		if err != nil {
			t.Fatalf("search %q: %v", q, err)
		}
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Title)
		}
		return out
	}
	has := func(got []string, want ...string) bool {
		if len(got) != len(want) {
			return false
		}
		set := map[string]bool{}
		for _, g := range got {
			set[g] = true
		}
		for _, w := range want {
			if !set[w] {
				return false
			}
		}
		return true
	}

	// Stemming: one word form finds all inflections.
	if got := search("газпром"); !has(got, texts[0], texts[1]) {
		t.Fatalf("stem: %v", got)
	}
	// Exclusion.
	if got := search("газпром -акции"); !has(got, texts[0]) {
		t.Fatalf("exclude: %v", got)
	}
	// Phrase: adjacent words in this order only.
	if got := search(`"северный поток"`); !has(got, texts[2]) {
		t.Fatalf("phrase: %v", got)
	}
	// OR.
	if got := search("нефть or дивиденды"); !has(got, texts[4], texts[0]) {
		t.Fatalf("or: %v", got)
	}
	// Plain words match as prefixes too: an unfinished word or a stem the
	// dictionary does not know still finds the entry; substrings inside a
	// word do not.
	if got := search("нефт"); !has(got, texts[4]) {
		t.Fatalf("prefix: %v", got)
	}
	if got := search("газпр выр"); !has(got, texts[1]) {
		t.Fatalf("multi-word prefix: %v", got)
	}
	if got := search("ефть"); len(got) != 0 {
		t.Fatalf("infix must not match: %v", got)
	}
	// Operator characters inside a plain word are data, not syntax.
	if got := search("газпром'а"); !has(got, texts[0], texts[1]) {
		t.Fatalf("quote inside word: %v", got)
	}
	// Garbage never errors, just finds nothing.
	for _, q := range []string{`"`, `"""`, "(((", "&&& ||| !!!", "OR OR", "-", `""`, "a:*", "!a", "a <-> b", `'`, `''`, `\`, `\'`, "':*", "a:*b", "x'y\\z", "в", "в на", strings.Repeat("я", 3000)} {
		if _, _, err := store.SearchEntries(ctx, owner.ID, SearchEntriesFilter{Query: q, FeedID: &feed.ID, Limit: 10, Rank: true, WithTotal: true}); err != nil {
			t.Fatalf("garbage %q: %v", q, err)
		}
	}
}
