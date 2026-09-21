package httpserver

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

	"rssam/internal/filter"
	"rssam/internal/storage"
)

type memAuditLog struct {
	mu   sync.Mutex
	rows []storage.AuditEvent
}

func (m *memAuditLog) RecordAudit(_ context.Context, ev storage.AuditEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows = append(m.rows, ev)
	return nil
}

func (m *memAuditLog) ListAuditLog(context.Context, storage.AuditListParams) ([]storage.AuditEntry, int, error) {
	return nil, 0, nil
}

func (m *memAuditLog) CountAuditLog(context.Context) (int, error) { return len(m.rows), nil }

// Account-security endpoints of the REST API write audit rows under the
// calling principal; failures and unrelated calls do not.
func TestRouter_APIWritesAudit(t *testing.T) {
	log := &memAuditLog{}
	env := newRouterEnv(t, func(d *Dependencies) { d.AuditLogStore = log })

	rec := env.want(env.do(http.MethodPost, "/v1/users", env.adminKey, `{"username":"carol","password":"longenough1"}`), http.StatusCreated)
	carol, _ := decodeData[userDTO](t, rec)
	env.want(env.do(http.MethodPost, "/v1/users", env.adminKey, `{"username":"carol","password":"x"}`), http.StatusBadRequest)
	env.want(env.do(http.MethodGet, "/v1/users", env.adminKey, ""), http.StatusOK)
	rec = env.want(env.do(http.MethodPost, "/v1/me/api-keys", env.bobKey, `{"name":"ci","scope":"write"}`), http.StatusCreated)
	key, _ := decodeData[apiKeyCreateResponse](t, rec)
	env.want(env.do(http.MethodDelete, "/v1/me/api-keys/"+itoa(key.ID), env.bobKey, ""), http.StatusOK)
	env.want(env.do(http.MethodDelete, "/v1/users/"+itoa(carol.ID), env.adminKey, ""), http.StatusOK)

	got := make([]string, 0, len(log.rows))
	for _, ev := range log.rows {
		got = append(got, ev.Action)
	}
	if strings.Join(got, ",") != "user.create,api_key.create,api_key.delete,user.delete" {
		t.Fatalf("actions = %v", got)
	}
	if ev := log.rows[0]; ev.ActorID != 1 || ev.TargetType != "user" || ev.TargetID != carol.ID || ev.Details["username"] != "carol" || ev.IP == "" {
		t.Fatalf("user.create = %+v", ev)
	}
	if ev := log.rows[1]; ev.ActorID != env.bobID || ev.TargetID != key.ID || ev.Details["scope"] != "write" {
		t.Fatalf("api_key.create = %+v", ev)
	}
}

// Catalog writes (feeds, filters) are audited under the editor who made
// them; reads and rejected requests are not.
func TestRouter_CatalogWritesAudit(t *testing.T) {
	log := &memAuditLog{}
	env := newRouterEnv(t, func(d *Dependencies) {
		d.AuditLogStore = log
		d.FeedStore = newTenantFeedStore()
		d.CategoryStore = &fakeCategoryStore{}
		d.FilterStore = newMemFilterStore()
		d.FilterEngine = filter.New(filter.Config{MaxRulesPerFilter: 50, MaxRegexLength: 2048})
	})

	rec := env.want(env.do(http.MethodPost, "/v1/feeds", env.editorKey, `{"feed_url":"https://example.com/a.xml"}`), http.StatusCreated)
	feed, _ := decodeData[feedDTO](t, rec)
	env.want(env.do(http.MethodPost, "/v1/feeds", env.bobKey, `{"feed_url":"https://example.com/b.xml"}`), http.StatusForbidden)
	env.want(env.do(http.MethodGet, "/v1/feeds", env.editorKey, ""), http.StatusOK)
	env.want(env.do(http.MethodPut, "/v1/feeds/"+itoa(feed.ID), env.editorKey, `{"feed_url":"https://example.com/a.xml","title":"A"}`), http.StatusOK)
	rec = env.want(env.do(http.MethodPost, "/v1/filters", env.editorKey, `{"name":"f","enabled":true,"rules":[{"field":"title","pattern":"foo","op":"and"}]}`), http.StatusCreated)
	f, _ := decodeData[filterDTO](t, rec)
	env.want(env.do(http.MethodDelete, "/v1/filters/"+itoa(f.ID), env.editorKey, ""), http.StatusOK)
	env.want(env.do(http.MethodDelete, "/v1/feeds/"+itoa(feed.ID), env.editorKey, ""), http.StatusOK)

	got := make([]string, 0, len(log.rows))
	for _, ev := range log.rows {
		got = append(got, ev.Action)
		if ev.ActorID != env.editorID {
			t.Fatalf("%s: actor=%d want editor %d", ev.Action, ev.ActorID, env.editorID)
		}
	}
	if strings.Join(got, ",") != "feed.create,feed.update,filter.create,filter.delete,feed.delete" {
		t.Fatalf("actions = %v", got)
	}
	if ev := log.rows[0]; ev.TargetType != "feed" || ev.TargetID != feed.ID || ev.Details["url"] != "https://example.com/a.xml" {
		t.Fatalf("feed.create = %+v", ev)
	}
}
