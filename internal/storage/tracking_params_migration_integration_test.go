//go:build integration

package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/migrations"
	"rssam/internal/model"
)

// Migration 0037 rewrites stored URLs/hashes with the same rule as
// model.NormalizeURL. The migration has already run on the test DB, so the
// test inserts pre-0.1.11 style rows, replays the migration body inside a
// transaction and checks the result row by row against the Go code.
func TestIntegration_TrackingParamsMigrationMatchesGo(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	pw, err := auth.HashPassword("integration-test")
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.CreateUser(ctx, CreateUserParams{Username: "int_trk_" + suffix, PasswordHash: pw})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = store.DeleteUser(context.Background(), u.ID) })
	var feedID int64
	if err := store.db.QueryRow(ctx, `
INSERT INTO feeds(user_id, feed_url, feed_type, title, interval_minutes, manual_paused)
VALUES ($1, $2, 'rss', 'trk', 60, TRUE) RETURNING id`, u.ID, "https://example.com/trk/"+suffix+".xml").Scan(&feedID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.db.Exec(context.Background(), `DELETE FROM feeds WHERE id = $1`, feedID) })

	var labelID int64
	if err := store.db.QueryRow(ctx, `
INSERT INTO labels(user_id, caption, fg_color, bg_color) VALUES ($1, $2, '#000', '#fff') RETURNING id`, u.ID, "trk"+suffix).Scan(&labelID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = store.db.Exec(context.Background(), `DELETE FROM labels WHERE id = $1`, labelID) })

	base := "https://example.com/" + suffix
	// Hash as computed before 0.1.11: sha256 of the URL with its parameters.
	legacy := func(u string) string {
		sum := sha256.Sum256([]byte(u))
		return hex.EncodeToString(sum[:])
	}
	type row struct {
		url, hash string
		starred   bool
		label     bool
	}
	rows := []row{
		{base + "/n/1", legacy(base + "/n/1"), false, false},
		{base + "/n/1?utm_source=telegram&utm_medium=post", legacy(base + "/n/1?utm_source=telegram&utm_medium=post"), true, true},
		{base + "/n/1?utm_source=vk", legacy(base + "/n/1?utm_source=vk"), false, false},
		{base + "/n/2?utm_campaign=x", legacy(base + "/n/2?utm_campaign=x"), false, false},
		{base + "/p?id=7&utm_source=a&fbclid=zzz", legacy(base + "/p?id=7&utm_source=a&fbclid=zzz"), false, false},
		{base + "/p?yclid=1&id=7", legacy(base + "/p?yclid=1&id=7"), false, true},
		{base + "/v/9?utm_source=share", model.DedupHashFromString("post-9-" + suffix), false, false},
		{base + "/p?id=8&page=2", legacy(base + "/p?id=8&page=2"), false, false},
		{base + "/utm_source/x?id=9", legacy(base + "/utm_source/x?id=9"), false, false},
		{base + "/p?UTM_Source=X&id=10", legacy(base + "/p?UTM_Source=X&id=10"), false, false},
		{base + "/p?_openstat=a;b;c&id=11", legacy(base + "/p?_openstat=a;b;c&id=11"), false, false},
		{base + "/s?q=%D0%BF%D1%80+x&utm_term=t", legacy(base + "/s?q=%D0%BF%D1%80+x&utm_term=t"), false, false},
		{base + "/p?id=8&page=2&utm_source=rm", legacy(base + "/p?id=8&page=2&utm_source=rm"), true, false},
		{base + "/p?a=1&&utm_source=x", legacy(base + "/p?a=1&&utm_source=x"), false, false},
	}

	tx, err := store.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()

	ids := make([]int64, len(rows))
	for i, r := range rows {
		if err := tx.QueryRow(ctx, `
INSERT INTO entries(feed_id, user_id, title, url, content, hash, status, starred, search_vector, created_at)
VALUES ($1, $2, 't', $3, '', $4, 'unread', $5, ''::tsvector, now() - ($6 || ' minutes')::interval)
RETURNING id`, feedID, u.ID, r.url, r.hash, r.starred, fmt.Sprint(len(rows)-i)).Scan(&ids[i]); err != nil {
			t.Fatalf("insert %s: %v", r.url, err)
		}
		if r.label {
			if _, err := tx.Exec(ctx, `INSERT INTO entry_labels(entry_id, label_id) VALUES ($1, $2)`, ids[i], labelID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO feed_entry_dedup(feed_id, hash, url, first_seen_at) VALUES
 ($1, $2, $3, now() - interval '2 day'),
 ($1, $4, $5, now() - interval '1 day'),
 ($1, $6, $7, now())`,
		feedID,
		legacy(base+"/d/5?utm_source=a"), base+"/d/5?utm_source=a",
		legacy(base+"/d/5?utm_source=b"), base+"/d/5?utm_source=b",
		legacy(base+"/d/5"), base+"/d/5"); err != nil {
		t.Fatal(err)
	}

	body, err := migrations.Source("0037_strip_tracking_params.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, body); err != nil {
		t.Fatalf("replay migration: %v", err)
	}

	got := map[string]struct {
		id      int64
		hash    string
		starred bool
	}{}
	rs, err := tx.Query(ctx, `SELECT id, url, hash, starred FROM entries WHERE feed_id = $1`, feedID)
	if err != nil {
		t.Fatal(err)
	}
	for rs.Next() {
		var id int64
		var url, hash string
		var starred bool
		if err := rs.Scan(&id, &url, &hash, &starred); err != nil {
			t.Fatal(err)
		}
		got[url] = struct {
			id      int64
			hash    string
			starred bool
		}{id, hash, starred}
	}
	rs.Close()

	wantURLs := map[string]struct{}{}
	for i, r := range rows {
		n := model.NormalizeURL(r.url)
		wantURLs[n] = struct{}{}
		g, ok := got[n]
		if !ok {
			t.Errorf("row %d %s: no entry with url %s after migration", i, r.url, n)
			continue
		}
		if r.hash == legacy(r.url) {
			if want := model.DedupHashFromURL(r.url); g.hash != want {
				t.Errorf("%s: hash %s, Go computes %s", n, g.hash, want)
			}
		} else if g.hash != r.hash {
			t.Errorf("%s: id-based hash was rewritten to %s", n, g.hash)
		}
	}
	if len(got) != len(wantURLs) {
		t.Errorf("entries after migration: %d, want %d distinct normalised urls", len(got), len(wantURLs))
	}

	// Collapsed group n/1: the oldest row (id of the clean url) survives with
	// the star and the label of the deleted duplicates.
	n1 := got[base+"/n/1"]
	if n1.id != ids[0] {
		t.Errorf("n/1 survivor id %d, want oldest %d", n1.id, ids[0])
	}
	if !n1.starred {
		t.Error("n/1 did not inherit the star")
	}
	var labelled int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM entry_labels WHERE entry_id = $1 AND label_id = $2`, n1.id, labelID).Scan(&labelled); err != nil {
		t.Fatal(err)
	}
	if labelled != 1 {
		t.Errorf("n/1 label rows = %d, want 1", labelled)
	}
	// Group p?id=7: the older tracked row survives (rewritten), keeps the label of the newer one.
	p7 := got[base+"/p?id=7"]
	if p7.id != ids[4] {
		t.Errorf("p?id=7 survivor id %d, want %d", p7.id, ids[4])
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM entry_labels WHERE entry_id = $1`, p7.id).Scan(&labelled); err != nil {
		t.Fatal(err)
	}
	if labelled != 1 {
		t.Errorf("p?id=7 label rows = %d, want 1", labelled)
	}
	// Untracked row stays byte-identical, including hash, and absorbs the star of its removed duplicate.
	p8 := got[base+"/p?id=8&page=2"]
	if p8.id != ids[7] || p8.hash != rows[7].hash || !p8.starred {
		t.Errorf("p?id=8&page=2: id=%d hash=%s starred=%v", p8.id, p8.hash, p8.starred)
	}

	var dedupURL string
	var dedupN int
	var firstSeen time.Time
	if err := tx.QueryRow(ctx, `
SELECT count(*), min(url), min(first_seen_at) FROM feed_entry_dedup WHERE feed_id = $1`, feedID).Scan(&dedupN, &dedupURL, &firstSeen); err != nil {
		t.Fatal(err)
	}
	if dedupN != 1 || dedupURL != base+"/d/5" || time.Since(firstSeen) < 47*time.Hour {
		t.Errorf("feed_entry_dedup after migration: n=%d url=%s first_seen=%s", dedupN, dedupURL, firstSeen)
	}
	var dh string
	if err := tx.QueryRow(ctx, `SELECT hash FROM feed_entry_dedup WHERE feed_id = $1`, feedID).Scan(&dh); err != nil {
		t.Fatal(err)
	}
	if want := model.DedupHashFromURL(base + "/d/5?utm_source=a"); dh != want {
		t.Errorf("dedup hash %s, Go computes %s", dh, want)
	}
}
