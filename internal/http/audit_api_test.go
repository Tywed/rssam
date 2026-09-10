package httpserver

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"

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
