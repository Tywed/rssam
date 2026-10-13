package httpserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	bridge "rssam/internal/bridge/v1"
	"rssam/internal/http/middleware"
	"rssam/internal/model"
	"rssam/internal/storage"
)

// Inbound deliveries: another rssam's outgoing HTTP webhook (per-entry or
// digest payload, signed with the feed's secret) POSTed to /hooks/{id}.
// Deliberately outside the per-IP rate limit: a node legitimately sends a
// burst of entries after one poll, and 429s would only turn into retries;
// a rejected request costs one primary-key lookup and one HMAC.
const (
	inboundMaxBodyBytes = 1 << 20
	inboundMaxItems     = 200
	// inboundMaxSkew bounds sent_at against the server clock: a captured
	// request cannot be replayed later (inside the window it is a
	// duplicate by hash and inserts nothing).
	inboundMaxSkew = 10 * time.Minute
)

// inboundEntry is storage.Entry as the sender marshals it (no JSON tags).
type inboundEntry struct {
	Title       string     `json:"Title"`
	URL         string     `json:"URL"`
	Content     string     `json:"Content"`
	Author      *string    `json:"Author"`
	PublishedAt *time.Time `json:"PublishedAt"`
	Hash        string     `json:"Hash"`
}

type inboundPayload struct {
	EventVersion int           `json:"event_version"`
	EventType    string        `json:"event_type"`
	SentAt       time.Time     `json:"sent_at"`
	Entry        *inboundEntry `json:"entry"`
	Items        []struct {
		Entry inboundEntry `json:"entry"`
	} `json:"items"`
}

func (s *Server) handleInboundWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.inboundAllowed(r) {
		writeError(w, http.StatusForbidden, "forbidden")
		return
	}
	feedID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || feedID <= 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	secret, err := s.inboundFeeds.InboundSecret(r.Context(), feedID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) || errors.Is(err, storage.ErrNotInboundFeed) {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		s.log.ErrorContext(r.Context(), "inbound secret lookup failed", "feed_id", feedID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, inboundMaxBodyBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body")
		return
	}
	if len(body) > inboundMaxBodyBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "body too large")
		return
	}
	if !inboundSignatureOK(r.Header.Get("X-RSSAM-Signature"), secret, body) {
		writeError(w, http.StatusUnauthorized, "bad signature")
		return
	}
	entries, err := parseInboundPayload(body, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	feed, err := s.feeds.GetFeedByID(r.Context(), feedID)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	inserted, err := s.refresher.Ingest(r.Context(), feed, entries)
	if err != nil {
		s.log.ErrorContext(r.Context(), "inbound ingest failed", "feed_id", feedID, "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]int{"received": len(entries), "inserted": inserted})
}

// inboundAllowed applies INBOUND_WEBHOOK_ALLOW_CIDRS (empty = any source).
func (s *Server) inboundAllowed(r *http.Request) bool {
	if len(s.inboundAllowCIDRs) == 0 {
		return true
	}
	ip, err := netip.ParseAddr(middleware.ClientIP(r))
	if err != nil {
		return false
	}
	ip = ip.Unmap()
	for _, p := range s.inboundAllowCIDRs {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// inboundSignatureOK checks "sha256=<hex HMAC-SHA256(secret, body)>", the
// signature the outgoing webhook worker produces.
func inboundSignatureOK(header, secret string, body []byte) bool {
	got, ok := strings.CutPrefix(strings.TrimSpace(header), "sha256=")
	if !ok {
		return false
	}
	gotMAC, err := hex.DecodeString(got)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	return hmac.Equal(gotMAC, mac.Sum(nil))
}

// parseInboundPayload accepts a per-entry event or a digest and returns the
// entries as a bridge would: hash from the normalized URL, so the same
// article arriving from two senders is one entry.
func parseInboundPayload(body []byte, now time.Time) ([]bridge.Entry, error) {
	var p inboundPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, errors.New("invalid json")
	}
	if p.EventVersion != 1 {
		return nil, errors.New("unsupported event_version")
	}
	if p.SentAt.IsZero() || p.SentAt.Before(now.Add(-inboundMaxSkew)) || p.SentAt.After(now.Add(inboundMaxSkew)) {
		return nil, errors.New("sent_at outside the accepted window")
	}
	raw := make([]inboundEntry, 0, len(p.Items)+1)
	if p.Entry != nil {
		raw = append(raw, *p.Entry)
	}
	for _, it := range p.Items {
		raw = append(raw, it.Entry)
	}
	if len(raw) == 0 {
		return nil, errors.New("no entries")
	}
	if len(raw) > inboundMaxItems {
		return nil, errors.New("too many entries")
	}
	out := make([]bridge.Entry, 0, len(raw))
	for _, e := range raw {
		u := model.NormalizeURL(strings.TrimSpace(e.URL))
		hash := strings.TrimSpace(e.Hash)
		if u != "" {
			hash = model.DedupHashFromURL(u)
		}
		if hash == "" {
			return nil, errors.New("entry without url")
		}
		out = append(out, bridge.Entry{
			Title:       strings.TrimSpace(e.Title),
			URL:         u,
			Content:     e.Content,
			Author:      e.Author,
			PublishedAt: e.PublishedAt,
			Hash:        hash,
		})
	}
	return out, nil
}
