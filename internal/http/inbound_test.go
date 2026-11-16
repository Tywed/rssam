package httpserver

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/storage"
)

// stubInboundFeeds: every feed is inbound with a fixed secret.
type stubInboundFeeds struct{}

func (stubInboundFeeds) CreateInboundFeed(context.Context, int64, storage.CreateInboundFeedParams) (storage.Feed, string, error) {
	return storage.Feed{}, "", nil
}
func (stubInboundFeeds) InboundSecret(context.Context, int64) (string, error) { return "stub", nil }
func (stubInboundFeeds) RotateInboundSecret(context.Context, int64) (string, error) {
	return "stub", nil
}

func TestInboundSignatureOK(t *testing.T) {
	body := []byte(`{"a":1}`)
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	cases := map[string]bool{
		good:                                   true,
		" " + good + " ":                       true,
		strings.ToUpper(good):                  false,
		"sha256=" + good[7:len(good)-2] + "00": false,
		good[7:]:                               false,
		"sha256=zz":                            false,
		"":                                     false,
	}
	for sig, want := range cases {
		if got := inboundSignatureOK(sig, "s3cret", body); got != want {
			t.Errorf("signature %q: got %v, want %v", sig, got, want)
		}
	}
	if inboundSignatureOK(good, "other", body) {
		t.Error("signature accepted with another secret")
	}
}

func TestParseInboundPayload(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	at := now.Add(-time.Minute).Format(time.RFC3339)
	single := `{"event_version":1,"event_type":"new_entry","sent_at":"` + at + `","entry":{"ID":7,"Title":" T ","URL":"https://Example.org/a?utm_source=x","Content":"c","Author":"me","PublishedAt":"2026-10-01T00:00:00Z","Hash":"ignored"}}`
	entries, err := parseInboundPayload([]byte(single), now)
	if err != nil || len(entries) != 1 {
		t.Fatalf("single: %v %d", err, len(entries))
	}
	e := entries[0]
	if e.Title != "T" || e.URL != "https://example.org/a" || e.Hash == "" || e.Hash == "ignored" || e.Author == nil || *e.Author != "me" || e.PublishedAt == nil {
		t.Fatalf("entry = %+v", e)
	}
	digest := `{"event_version":1,"event_type":"digest","count":2,"sent_at":"` + at + `","items":[{"entry":{"Title":"a","URL":"https://x/a"}},{"entry":{"Title":"b","URL":"https://x/b"}}]}`
	if entries, err = parseInboundPayload([]byte(digest), now); err != nil || len(entries) != 2 {
		t.Fatalf("digest: %v %d", err, len(entries))
	}
	bad := map[string]string{
		"invalid json":   `{`,
		"version":        `{"event_version":2,"sent_at":"` + at + `","entry":{"URL":"https://x"}}`,
		"no sent_at":     `{"event_version":1,"entry":{"URL":"https://x"}}`,
		"stale":          `{"event_version":1,"sent_at":"` + now.Add(-11*time.Minute).Format(time.RFC3339) + `","entry":{"URL":"https://x"}}`,
		"future":         `{"event_version":1,"sent_at":"` + now.Add(11*time.Minute).Format(time.RFC3339) + `","entry":{"URL":"https://x"}}`,
		"empty":          `{"event_version":1,"sent_at":"` + at + `"}`,
		"entry w/o url":  `{"event_version":1,"sent_at":"` + at + `","entry":{"Title":"x"}}`,
		"too many items": `{"event_version":1,"sent_at":"` + at + `","items":[` + strings.Repeat(`{"entry":{"URL":"https://x"}},`, 200) + `{"entry":{"URL":"https://x"}}]}`,
	}
	for name, body := range bad {
		if _, err := parseInboundPayload([]byte(body), now); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// inboundByID: feed 1 is inbound, 2 is a polled feed, anything else unknown.
type inboundByID struct{}

func (inboundByID) CreateInboundFeed(context.Context, int64, storage.CreateInboundFeedParams) (storage.Feed, string, error) {
	return storage.Feed{}, "", nil
}
func (inboundByID) InboundSecret(_ context.Context, id int64) (string, error) {
	switch id {
	case 1:
		return "s3cret", nil
	case 2:
		return "", storage.ErrNotInboundFeed
	}
	return "", storage.ErrNotFound
}
func (inboundByID) RotateInboundSecret(context.Context, int64) (string, error) { return "", nil }

func TestInboundWebhook_UniformUnauthorized(t *testing.T) {
	s := New(Dependencies{InboundFeedStore: inboundByID{}, InboundWebhooks: true})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hooks/{id}", s.handleInboundWebhook)
	body := []byte(`{}`)
	post := func(path, sig string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		if sig != "" {
			req.Header.Set("X-RSSAM-Signature", sig)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(body)
	good := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	for name, path := range map[string]string{"unknown feed": "/hooks/999", "polled feed": "/hooks/2", "bad id": "/hooks/abc", "inbound feed": "/hooks/1"} {
		rec := post(path, "sha256=00")
		if rec.Code != http.StatusUnauthorized || rec.Body.String() != post("/hooks/1", "sha256=00").Body.String() {
			t.Fatalf("%s with bad signature: status=%d body=%s (must be identical to the inbound-feed answer)", name, rec.Code, rec.Body.String())
		}
	}
	// The right signature for a missing feed still fails — nothing leaks.
	if rec := post("/hooks/999", good); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unknown feed with a valid-looking signature: %d", rec.Code)
	}
	// The real feed with the right signature gets past authentication
	// (400: `{}` is not a payload).
	if rec := post("/hooks/1", good); rec.Code != http.StatusBadRequest {
		t.Fatalf("inbound feed, good signature: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestParseInboundPayload_HashFormat(t *testing.T) {
	now := time.Now()
	mk := func(hash string) string {
		return `{"event_version":1,"event_type":"entry.created","sent_at":"` + now.UTC().Format(time.RFC3339) + `","entry":{"Title":"t","URL":"","Hash":"` + hash + `"}}`
	}
	if _, err := parseInboundPayload([]byte(mk(strings.Repeat("ab", 32))), now); err != nil {
		t.Fatalf("64-hex hash must be accepted: %v", err)
	}
	for _, bad := range []string{"", "abc", strings.Repeat("AB", 32), strings.Repeat("zz", 32), strings.Repeat("ab", 33)} {
		if _, err := parseInboundPayload([]byte(mk(bad)), now); err == nil {
			t.Fatalf("hash %q must be rejected", bad)
		}
	}
}
