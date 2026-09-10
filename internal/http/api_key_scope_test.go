package httpserver

import (
	"net/http"
	"testing"
	"time"
)

// A key is created with a scope and optional lifetime; read keys cannot
// mutate, write keys may only change entry state, expired keys are 401.
func TestRouter_APIKeyScopesAndExpiry(t *testing.T) {
	env := newRouterEnv(t, func(d *Dependencies) { d.EntryStore = newTenantEntryStore() })

	rec := env.want(env.do(http.MethodPost, "/v1/me/api-keys", env.bobKey, `{"name":"ro","scope":"read","expires_in_days":30}`), http.StatusCreated)
	ro, _ := decodeData[apiKeyCreateResponse](t, rec)
	if ro.Scope != "read" || ro.ExpiresAt == nil || time.Until(*ro.ExpiresAt) > 30*24*time.Hour {
		t.Fatalf("read key: %+v", ro.apiKeyDTO)
	}
	env.want(env.do(http.MethodGet, "/v1/me", ro.Token, ""), http.StatusOK)
	env.want(env.do(http.MethodGet, "/v1/entries", ro.Token, ""), http.StatusOK)
	env.want(env.do(http.MethodPut, "/v1/entries", ro.Token, `{"entry_ids":[1],"status":"read"}`), http.StatusForbidden)
	env.want(env.do(http.MethodPost, "/v1/me/api-keys", ro.Token, `{"name":"escalate","scope":"admin"}`), http.StatusForbidden)

	rec = env.want(env.do(http.MethodPost, "/v1/me/api-keys", env.bobKey, `{"name":"rw","scope":"write"}`), http.StatusCreated)
	rw, _ := decodeData[apiKeyCreateResponse](t, rec)
	if rw.Scope != "write" || rw.ExpiresAt != nil {
		t.Fatalf("write key: %+v", rw.apiKeyDTO)
	}
	// Allowed by scope; the body is validated only after the scope gate.
	if rec := env.do(http.MethodPut, "/v1/entries", rw.Token, `{"entry_ids":[1],"status":"read"}`); rec.Code == http.StatusForbidden {
		t.Fatalf("write key must pass the scope gate for PUT /v1/entries, got 403")
	}
	env.want(env.do(http.MethodPost, "/v1/feeds", rw.Token, `{"url":"https://example.com/rss"}`), http.StatusForbidden)
	env.want(env.do(http.MethodDelete, "/v1/me/api-keys/"+itoa(ro.ID), rw.Token, ""), http.StatusForbidden)

	// Default scope is admin; keys created before the migration behave the same.
	rec = env.want(env.do(http.MethodPost, "/v1/me/api-keys", env.bobKey, `{"name":"full"}`), http.StatusCreated)
	full, _ := decodeData[apiKeyCreateResponse](t, rec)
	if full.Scope != "admin" {
		t.Fatalf("default scope = %q", full.Scope)
	}
	env.want(env.do(http.MethodDelete, "/v1/me/api-keys/"+itoa(rw.ID), full.Token, ""), http.StatusOK)

	env.want(env.do(http.MethodPost, "/v1/me/api-keys", env.bobKey, `{"name":"x","scope":"root"}`), http.StatusBadRequest)
	env.want(env.do(http.MethodPost, "/v1/me/api-keys", env.bobKey, `{"name":"x","expires_in_days":-1}`), http.StatusBadRequest)

	// Expired key: the store still returns it, the authenticator rejects it.
	env.users.mu.Lock()
	for h, k := range env.users.keys {
		if k.ID == ro.ID {
			past := time.Now().Add(-time.Minute)
			k.ExpiresAt = &past
			env.users.keys[h] = k
		}
	}
	env.users.mu.Unlock()
	env.want(env.do(http.MethodGet, "/v1/entries", ro.Token, ""), http.StatusUnauthorized)
}
