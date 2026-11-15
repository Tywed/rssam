package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"rssam/internal/auth"
	"rssam/internal/storage"
)

func TestRouter_WebhookHeadersMaskedAndRoundTrip(t *testing.T) {
	ws := newMemWebhookStore()
	env := newRouterEnv(t, func(d *Dependencies) { d.WebhookStore = ws })

	rec := env.want(env.do(http.MethodPost, "/v1/webhooks", env.adminKey,
		`{"name":"n8n","url":"https://example.com/hook","headers":{"Authorization":"Bearer top-secret","X-Trace":"1"}}`), http.StatusCreated)
	created, _ := decodeData[webhookDTO](t, rec)
	var hdr map[string]string
	_ = json.Unmarshal(created.Headers, &hdr)
	if hdr["Authorization"] != webhookHeaderMask || hdr["X-Trace"] != "1" {
		t.Fatalf("headers in response: %v", hdr)
	}
	rec = env.want(env.do(http.MethodGet, "/v1/webhooks", env.adminKey, ""), http.StatusOK)
	list, _ := decodeData[[]webhookDTO](t, rec)
	_ = json.Unmarshal(list[0].Headers, &hdr)
	if hdr["Authorization"] != webhookHeaderMask {
		t.Fatalf("list leaks header: %v", hdr)
	}

	// GET → edit → PUT with the mask keeps the stored secret; a new value replaces it.
	id := itoa(created.ID)
	env.want(env.do(http.MethodPut, "/v1/webhooks/"+id, env.adminKey,
		`{"name":"n8n","url":"https://example.com/hook","headers":{"Authorization":"***","X-Trace":"2"}}`), http.StatusOK)
	stored, _ := ws.GetWebhook(t.Context(), 1, created.ID)
	_ = json.Unmarshal(stored.Headers, &hdr)
	if hdr["Authorization"] != "Bearer top-secret" || hdr["X-Trace"] != "2" {
		t.Fatalf("stored after masked PUT: %v", hdr)
	}
	env.want(env.do(http.MethodPut, "/v1/webhooks/"+id, env.adminKey,
		`{"name":"n8n","url":"https://example.com/hook","headers":{"Authorization":"Bearer new"}}`), http.StatusOK)
	stored, _ = ws.GetWebhook(t.Context(), 1, created.ID)
	_ = json.Unmarshal(stored.Headers, &hdr)
	if hdr["Authorization"] != "Bearer new" {
		t.Fatalf("stored after new value: %v", hdr)
	}
}

func TestRouter_FetchContentScopeAndMethods(t *testing.T) {
	env := newRouterEnv(t, func(d *Dependencies) { d.EntryStore = newTenantEntryStore() })
	rec := env.want(env.do(http.MethodPost, "/v1/me/api-keys", env.bobKey, `{"name":"ro","scope":"read"}`), http.StatusCreated)
	ro, _ := decodeData[apiKeyCreateResponse](t, rec)
	rec = env.want(env.do(http.MethodPost, "/v1/me/api-keys", env.bobKey, `{"name":"rw","scope":"write"}`), http.StatusCreated)
	rw, _ := decodeData[apiKeyCreateResponse](t, rec)

	// read scope: denied on both forms, before any fetcher is consulted.
	env.want(env.do(http.MethodPost, "/v1/entries/1/fetch-content", ro.Token, ""), http.StatusForbidden)
	env.want(env.do(http.MethodGet, "/v1/entries/1/fetch-content", ro.Token, ""), http.StatusForbidden)
	// write scope passes the scope gate (fetcher is not configured in this env → 503).
	env.want(env.do(http.MethodPost, "/v1/entries/1/fetch-content", rw.Token, ""), http.StatusServiceUnavailable)
	env.want(env.do(http.MethodGet, "/v1/entries/1/fetch-content", rw.Token, ""), http.StatusServiceUnavailable)
	if !auth.ScopeAllows(auth.ScopeWrite, http.MethodPost, "/v1/entries/7/fetch-content") || auth.ScopeAllows(auth.ScopeWrite, http.MethodPost, "/v1/feeds") {
		t.Fatal("write scope: POST fetch-content must be allowed, other POSTs not")
	}
}

func TestAuthenticate_BadTokenBudget(t *testing.T) {
	users := secretTokenUsers()
	s := New(Dependencies{UserStore: users})
	call := func(ip, token string) int {
		req := httptest.NewRequest(http.MethodGet, "/v1/me", nil)
		req.RemoteAddr = ip + ":1234"
		req.Header.Set("X-Auth-Token", token)
		rec := httptest.NewRecorder()
		s.wrapAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })).ServeHTTP(rec, req)
		return rec.Code
	}
	lookups := func() int {
		users.mu.Lock()
		defer users.mu.Unlock()
		return users.lookups
	}
	if call("203.0.113.9", "secret") != http.StatusOK {
		t.Fatal("valid token must pass")
	}
	// The same stale token costs one query, then is answered from memory.
	before := lookups()
	for i := 0; i < 20; i++ {
		call("203.0.113.9", "stale")
	}
	if got := lookups() - before; got != 1 {
		t.Fatalf("same bad token: %d lookups, want 1", got)
	}
	// Distinct tokens: 100 queries (the burst), then rejected without one;
	// the valid token from that IP is rejected too until the bucket refills.
	before = lookups()
	for i := 0; i < 150; i++ {
		call("203.0.113.10", "bad-"+itoa(int64(i)))
	}
	if got := lookups() - before; got != 100 {
		t.Fatalf("distinct bad tokens: %d lookups, want 100", got)
	}
	if call("203.0.113.10", "secret") != http.StatusUnauthorized {
		t.Fatal("exhausted budget must reject before the database")
	}
	if call("203.0.113.9", "secret") != http.StatusOK {
		t.Fatal("another IP is unaffected")
	}
}

func TestTouchAPIKey_OncePerMinute(t *testing.T) {
	users := secretTokenUsers()
	s := New(Dependencies{UserStore: users})
	for i := 0; i < 5; i++ {
		s.touchAPIKey(t.Context(), 1)
	}
	users.mu.Lock()
	n := users.touches
	users.mu.Unlock()
	if n != 1 {
		t.Fatalf("touches=%d want 1", n)
	}
	s.apiKeyTouched.Store(int64(1), time.Now().Add(-2*time.Minute))
	s.touchAPIKey(t.Context(), 1)
	users.mu.Lock()
	n = users.touches
	users.mu.Unlock()
	if n != 2 {
		t.Fatalf("touches after a minute=%d want 2", n)
	}
	_ = storage.ErrNotFound
}
