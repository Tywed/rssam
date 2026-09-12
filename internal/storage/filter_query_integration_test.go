//go:build integration

package storage

import (
	"context"
	"fmt"
	"testing"
)

func TestIntegration_MatchEntryQueries(t *testing.T) {
	store := isolatedStore(t)
	store.ftsLanguage = "russian"
	ctx := context.Background()
	owner := newIntegrationUser(t, store, "fq")
	feed := newFeedForUser(t, store, owner.ID, "fq", 60)

	texts := []string{
		"Банк объявил дивиденды",
		"Акции банка выросли на прогнозе",
		"Нефть подорожала",
	}
	params := make([]CreateEntryParams, 0, len(texts))
	for i, tx := range texts {
		params = append(params, CreateEntryParams{Title: tx, Content: "<p>тело</p>", URL: fmt.Sprintf("https://example.com/fq/%d", i), Hash: fmt.Sprintf("fq-%d", i)})
	}
	_, created, err := store.CreateEntries(ctx, feed.ID, params)
	if err != nil || len(created) != 3 {
		t.Fatalf("create: %d %v", len(created), err)
	}
	byTitle := map[string]Entry{}
	for _, e := range created {
		byTitle[e.Title] = e
	}

	// Stored rows go through search_vector (ID > 0, no text needed);
	// unsaved items (ID 0) are vectorised on the fly from title+content.
	items := []QueryMatchItem{
		{EntryID: byTitle["Банк объявил дивиденды"].ID},
		{EntryID: byTitle["Акции банка выросли на прогнозе"].ID},
		{EntryID: byTitle["Нефть подорожала"].ID},
		{Title: "Свежая новость", Content: "банки снова в заголовках"},
		{Title: "", Content: ""},
	}
	queries := []string{"банк", "банк -прогноз", `"нефть подорожала" or дивиденды`, "(unbalanced \"quote"}
	hits, err := store.MatchEntryQueries(ctx, items, queries)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]bool{
		{true, true, true, false},
		{true, false, false, false},
		{false, false, true, false},
		{true, true, false, false},
		{false, false, false, false},
	}
	for i := range want {
		for j := range want[i] {
			if hits[i][j] != want[i][j] {
				t.Errorf("item %d query %q: got %v want %v", i, queries[j], hits[i][j], want[i][j])
			}
		}
	}

	empty, err := store.MatchEntryQueries(ctx, nil, queries)
	if err != nil || len(empty) != 0 {
		t.Fatalf("no items: %v %v", empty, err)
	}
	none, err := store.MatchEntryQueries(ctx, items[:1], nil)
	if err != nil || len(none) != 1 || len(none[0]) != 0 {
		t.Fatalf("no queries: %v %v", none, err)
	}
}
