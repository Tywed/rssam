package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"rssam/internal/storage"
)

type memDigestStore struct {
	mu       sync.Mutex
	webhook  storage.Webhook
	items    []storage.WebhookDigestItem
	peers    []storage.WebhookLog
	peerReq  [][]int64
	sentIDs  []int64
	failIDs  []int64
	failDead bool
	failMsg  string
	failNext *time.Time
}

func (m *memDigestStore) ClaimWebhookDigestPeers(_ context.Context, _ int64, exclude []int64, _ int) ([]storage.WebhookLog, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.peerReq = append(m.peerReq, exclude)
	return m.peers, nil
}

func (m *memDigestStore) LoadWebhookDigestContext(context.Context, int64, []int64) (storage.Webhook, []storage.WebhookDigestItem, error) {
	return m.webhook, m.items, nil
}

func (m *memDigestStore) MarkWebhookLogsSent(_ context.Context, ids []int64, _ int, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sentIDs = ids
	return nil
}

func (m *memDigestStore) MarkWebhookLogsFailed(_ context.Context, ids []int64, _ *int, msg string, _ string, next *time.Time, dead bool, _ bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failIDs, m.failMsg, m.failNext, m.failDead = ids, msg, next, dead
	return nil
}

func discardLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func digestItems() []storage.WebhookDigestItem {
	return []storage.WebhookDigestItem{
		{LogID: 11, Entry: storage.Entry{ID: 1, Title: "A1", URL: "https://a/1"}, Feed: storage.WebhookFeed{ID: 1, Title: "Feed A"}},
		{LogID: 12, Entry: storage.Entry{ID: 2, Title: "A2", URL: "https://a/2"}, Feed: storage.WebhookFeed{ID: 1, Title: "Feed A"}},
		{LogID: 13, Entry: storage.Entry{ID: 3, Title: "B1 <x>", URL: "https://b/1?a=1&b=2"}, Feed: storage.WebhookFeed{ID: 2, Title: "Feed B"}},
	}
}

func TestGroupDigestLogs_CollapsesPerWebhookAndPullsPeers(t *testing.T) {
	ds := &memDigestStore{peers: []storage.WebhookLog{{ID: 13, WebhookID: 5}}}
	r := &Runner{WebhookDigest: ds, Log: discardLogger()}
	in := []storage.WebhookLog{
		{ID: 1, WebhookID: 9},
		{ID: 11, WebhookID: 5, DigestMinutes: 60},
		{ID: 2, WebhookID: 9},
		{ID: 12, WebhookID: 5, DigestMinutes: 60},
	}
	out := r.groupDigestLogs(context.Background(), in)
	if len(out) != 3 {
		t.Fatalf("got %d rows, want 3: %+v", len(out), out)
	}
	if out[0].ID != 1 || out[2].ID != 2 || out[0].Peers != nil {
		t.Fatalf("plain rows must pass through untouched: %+v", out)
	}
	c := out[1]
	if c.ID != 11 || len(c.Peers) != 3 || c.Peers[0] != 11 || c.Peers[1] != 12 || c.Peers[2] != 13 {
		t.Fatalf("carrier %+v", c)
	}
	if len(ds.peerReq) != 1 || len(ds.peerReq[0]) != 2 {
		t.Fatalf("peer claim must exclude the already claimed ids: %v", ds.peerReq)
	}
}

func TestProcessWebhookDigest_HTTPSendsOneBatch(t *testing.T) {
	var body []byte
	var sig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ = io.ReadAll(req.Body)
		sig = req.Header.Get("X-RSSAM-Signature")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	ds := &memDigestStore{
		webhook: storage.Webhook{ID: 5, Enabled: true, URL: srv.URL, Kind: storage.WebhookKindHTTP, Secret: "s3", DigestMinutes: 60, OnSuccessEntry: storage.WebhookOnSuccessMarkRead},
		items:   digestItems(),
	}
	del := newMemWebhookDelivery(storage.WebhookLog{ID: 11}, storage.WebhookDeliveryContext{})
	r := processTestRunner(del, 0)
	r.WebhookDigest = ds
	r.processWebhookDigest(context.Background(), storage.WebhookLog{ID: 11, WebhookID: 5, DigestMinutes: 60, Peers: []int64{11, 12, 13}})

	if len(ds.sentIDs) != 3 || ds.failIDs != nil {
		t.Fatalf("sent=%v fail=%v", ds.sentIDs, ds.failIDs)
	}
	var p struct {
		EventType string `json:"event_type"`
		Count     int    `json:"count"`
		Items     []struct {
			Entry storage.Entry       `json:"entry"`
			Feed  storage.WebhookFeed `json:"feed"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatalf("payload: %v %s", err, body)
	}
	if p.EventType != "digest" || p.Count != 3 || len(p.Items) != 3 || p.Items[2].Feed.Title != "Feed B" {
		t.Fatalf("payload %+v", p)
	}
	if !strings.HasPrefix(sig, "sha256=") {
		t.Fatalf("signature missing: %q", sig)
	}
	if len(del.read) != 3 {
		t.Fatalf("on_success mark_read must run per entry: %v", del.read)
	}
}

func TestProcessWebhookDigest_503RetriesWholeBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ds := &memDigestStore{
		webhook: storage.Webhook{ID: 5, Enabled: true, URL: srv.URL, Kind: storage.WebhookKindHTTP, DigestMinutes: 60},
		items:   digestItems(),
	}
	r := processTestRunner(newMemWebhookDelivery(storage.WebhookLog{ID: 11}, storage.WebhookDeliveryContext{}), 0)
	r.WebhookDigest = ds
	r.processWebhookDigest(context.Background(), storage.WebhookLog{ID: 11, WebhookID: 5, DigestMinutes: 60, Peers: []int64{11, 12, 13}})
	if len(ds.failIDs) != 3 || ds.failDead || ds.failNext == nil {
		t.Fatalf("fail=%v dead=%v next=%v", ds.failIDs, ds.failDead, ds.failNext)
	}
}

func TestProcessWebhookDigest_DisabledParks(t *testing.T) {
	ds := &memDigestStore{webhook: storage.Webhook{ID: 5, Enabled: false, Kind: storage.WebhookKindHTTP}, items: digestItems()}
	r := processTestRunner(newMemWebhookDelivery(storage.WebhookLog{ID: 11}, storage.WebhookDeliveryContext{}), 0)
	r.WebhookDigest = ds
	r.processWebhookDigest(context.Background(), storage.WebhookLog{ID: 11, WebhookID: 5, DigestMinutes: 60, Peers: []int64{11, 12}})
	if len(ds.failIDs) != 2 || ds.failDead || ds.failNext == nil || ds.failMsg != "webhook is disabled" {
		t.Fatalf("fail=%v dead=%v next=%v msg=%q", ds.failIDs, ds.failDead, ds.failNext, ds.failMsg)
	}
}

func TestProcessWebhookDigest_TelegramText(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewDecoder(req.Body).Decode(&body)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	cfg, _ := json.Marshal(storage.TelegramProviderConfig{APIBase: srv.URL, BotToken: "t", ChatID: "1"})
	ds := &memDigestStore{
		webhook: storage.Webhook{ID: 5, Enabled: true, Kind: storage.WebhookKindTelegram, ProviderConfig: cfg, DigestMinutes: 60},
		items:   digestItems(),
	}
	r := processTestRunner(newMemWebhookDelivery(storage.WebhookLog{ID: 11}, storage.WebhookDeliveryContext{}), 0)
	r.WebhookDigest = ds
	r.processWebhookDigest(context.Background(), storage.WebhookLog{ID: 11, WebhookID: 5, DigestMinutes: 60, Peers: []int64{11, 12, 13}})
	if len(ds.sentIDs) != 3 {
		t.Fatalf("sent=%v fail=%v msg=%q", ds.sentIDs, ds.failIDs, ds.failMsg)
	}
	text, _ := body["text"].(string)
	for _, want := range []string{"<b>Дайджест: 3</b>", "<b>Feed A</b>", "<b>Feed B</b>", `<a href="https://a/1">A1</a>`, "B1 &lt;x&gt;", "a=1&amp;b=2"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "<b>Feed A</b>") != 1 {
		t.Fatalf("feed header must appear once per feed:\n%s", text)
	}
}

func TestBuildWebhookDigestOutbound_TemplateAndTruncation(t *testing.T) {
	w := storage.Webhook{Kind: storage.WebhookKindHTTP, URL: "https://x", BodyTemplate: `{{.count}}:{{range .items}}{{.entry.Title}}@{{.feed.Title}};{{end}}`}
	out, err := storage.BuildWebhookDigestOutbound(w, digestItems(), time.Now())
	if err != nil || string(out.Body) != "3:A1@Feed A;A2@Feed A;B1 <x>@Feed B;" {
		t.Fatalf("template: %v %q", err, out.Body)
	}

	cfg, _ := json.Marshal(storage.MaxProviderConfig{APIBase: "http://m", Target: "chat_id", TargetID: "1"})
	many := make([]storage.WebhookDigestItem, 0, 300)
	for i := range 300 {
		many = append(many, storage.WebhookDigestItem{Entry: storage.Entry{ID: int64(i), Title: strings.Repeat("т", 40), URL: "https://e/" + strings.Repeat("x", 30)}, Feed: storage.WebhookFeed{ID: 1, Title: "F"}})
	}
	out, err = storage.BuildWebhookDigestOutbound(storage.Webhook{Kind: storage.WebhookKindMax, ProviderConfig: cfg}, many, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	var b map[string]string
	_ = json.Unmarshal(out.Body, &b)
	if len(b["text"]) > 4096 || !strings.Contains(b["text"], "… ещё ") {
		t.Fatalf("len=%d tail=%q", len(b["text"]), b["text"][len(b["text"])-30:])
	}
}
