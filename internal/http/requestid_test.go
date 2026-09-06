package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"rssam/internal/logger"
	"rssam/internal/requestid"
	"rssam/internal/storage"
)

type failingCategoryStore struct{ *fakeCategoryStore }

func (*failingCategoryStore) ListCategories(context.Context, int64, int, int) ([]storage.Category, int, error) {
	return nil, 0, errors.New("pg: connection reset")
}

// A 5xx must be traceable end to end: the same id in the response header,
// in the JSON body and in the ERROR log line written by the handler, plus in
// the access log line.
func TestRequestID_EndToEnd(t *testing.T) {
	var buf bytes.Buffer
	log := logger.NewWithWriter("info", "json", &buf)
	env := newRouterEnv(t, func(d *Dependencies) {
		d.Logger = log
		d.CategoryStore = &failingCategoryStore{&fakeCategoryStore{}}
	})

	rec := env.want(env.do(http.MethodGet, "/v1/categories", env.adminKey, ""), http.StatusInternalServerError)
	id := rec.Header().Get(requestid.Header)
	if !requestid.Valid(id) {
		t.Fatalf("no request id in response header: %q", id)
	}
	var body errorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RequestID != id || body.ErrorMessage == "" {
		t.Fatalf("body %+v, header id %q", body, id)
	}

	var handlerLine, accessLine map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		switch m["msg"] {
		case "list categories failed":
			handlerLine = m
		case "http access":
			accessLine = m
		}
	}
	if handlerLine == nil || handlerLine["request_id"] != id || handlerLine["level"] != "ERROR" {
		t.Fatalf("handler log line: %v", handlerLine)
	}
	if accessLine == nil || accessLine["request_id"] != id || accessLine["status"] != float64(500) {
		t.Fatalf("access log line: %v", accessLine)
	}

	// 4xx responses carry the header but do not leak the id into the body.
	rec = env.want(env.do(http.MethodGet, "/v1/categories", "", ""), http.StatusUnauthorized)
	if rec.Header().Get(requestid.Header) == "" {
		t.Fatal("4xx without X-Request-Id header")
	}
	if strings.Contains(rec.Body.String(), "request_id") {
		t.Fatalf("4xx body must not include request_id: %s", rec.Body.String())
	}

	// Ids from untrusted clients are ignored (httptest.NewRequest uses 192.0.2.1).
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.Header.Set(requestid.Header, "client-chosen")
	rr := httptest.NewRecorder()
	env.h.ServeHTTP(rr, req)
	if got := rr.Header().Get(requestid.Header); got == "client-chosen" || got == "" {
		t.Fatalf("untrusted id handling: %q", got)
	}
	// ...and honoured from a trusted proxy.
	req = httptest.NewRequest(http.MethodGet, "/healthz", nil)
	req.RemoteAddr = "127.0.0.1:9"
	req.Header.Set(requestid.Header, "proxy-7")
	rr = httptest.NewRecorder()
	env.h.ServeHTTP(rr, req)
	if got := rr.Header().Get(requestid.Header); got != "proxy-7" {
		t.Fatalf("trusted proxy id: %q", got)
	}
}
