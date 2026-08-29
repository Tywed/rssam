package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

type memWebhookStore struct {
	nextID   int64
	webhooks map[int64]storage.Webhook
}

func newMemWebhookStore() *memWebhookStore {
	return &memWebhookStore{nextID: 1, webhooks: map[int64]storage.Webhook{}}
}

func (m *memWebhookStore) ListWebhooks(_ context.Context, _ int64, _, _ int) ([]storage.Webhook, int, error) {
	out := make([]storage.Webhook, 0, len(m.webhooks))
	for _, w := range m.webhooks {
		out = append(out, w)
	}
	return out, len(out), nil
}
func (m *memWebhookStore) ListEnabledWebhooks(_ context.Context, _ int64, _ int) ([]storage.Webhook, error) {
	out := make([]storage.Webhook, 0, len(m.webhooks))
	for _, w := range m.webhooks {
		if w.Enabled {
			out = append(out, w)
		}
	}
	return out, nil
}
func (m *memWebhookStore) CreateWebhook(_ context.Context, p storage.CreateWebhookParams) (storage.Webhook, error) {
	id := m.nextID
	m.nextID++
	now := time.Now().UTC()
	onSuccess, err := storage.NormalizeWebhookOnSuccess(p.OnSuccessEntry)
	if err != nil {
		return storage.Webhook{}, err
	}
	name, err := storage.NormalizeWebhookName(p.Name)
	if err != nil {
		return storage.Webhook{}, err
	}
	kind, displayURL, cfg, err := storage.ResolveWebhookWrite(p.Kind, p.URL, p.ProviderConfig)
	if err != nil {
		return storage.Webhook{}, err
	}
	secret := p.Secret
	if kind != storage.WebhookKindHTTP {
		secret = ""
	}
	w := storage.Webhook{
		ID:             id,
		UserID:         p.UserID,
		FilterID:       p.FilterID,
		Name:           name,
		URL:            displayURL,
		Method:         strings.ToUpper(strings.TrimSpace(p.Method)),
		Headers:        p.Headers,
		BodyTemplate:   p.BodyTemplate,
		Secret:         secret,
		Enabled:        p.Enabled,
		OnSuccessEntry: onSuccess,
		Kind:           kind,
		ProviderConfig: cfg,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if w.Method == "" {
		w.Method = "POST"
	}
	m.webhooks[id] = w
	return w, nil
}
func (m *memWebhookStore) GetWebhook(_ context.Context, _ int64, id int64) (storage.Webhook, error) {
	w, ok := m.webhooks[id]
	if !ok {
		return storage.Webhook{}, storage.ErrNotFound
	}
	return w, nil
}
func (m *memWebhookStore) UpdateWebhook(_ context.Context, p storage.UpdateWebhookParams) (storage.Webhook, error) {
	w, ok := m.webhooks[p.ID]
	if !ok {
		return storage.Webhook{}, storage.ErrNotFound
	}
	w.FilterID = p.FilterID
	w.Method = strings.ToUpper(strings.TrimSpace(p.Method))
	w.Headers = p.Headers
	w.BodyTemplate = p.BodyTemplate
	w.Enabled = p.Enabled
	onSuccess, err := storage.NormalizeWebhookOnSuccess(p.OnSuccessEntry)
	if err != nil {
		return storage.Webhook{}, err
	}
	w.OnSuccessEntry = onSuccess
	if p.Name != nil {
		n, nerr := storage.NormalizeWebhookName(*p.Name)
		if nerr != nil {
			return storage.Webhook{}, nerr
		}
		w.Name = n
	}
	kindHint := p.Kind
	if strings.TrimSpace(kindHint) == "" {
		kindHint = w.Kind
	}
	kn, err := storage.NormalizeWebhookKind(kindHint)
	if err != nil {
		return storage.Webhook{}, err
	}
	cfgIn := p.ProviderConfig
	if kn == storage.WebhookKindTelegram {
		cfgIn = storage.MergeTelegramToken(cfgIn, w.ProviderConfig)
	}
	kind, displayURL, cfg, err := storage.ResolveWebhookWrite(kn, p.URL, cfgIn)
	if err != nil {
		return storage.Webhook{}, err
	}
	w.Kind = kind
	w.URL = displayURL
	w.ProviderConfig = cfg
	if p.Secret != nil {
		w.Secret = *p.Secret
	}
	if kind != storage.WebhookKindHTTP {
		w.Secret = ""
	}
	if w.Method == "" {
		w.Method = "POST"
	}
	w.UpdatedAt = time.Now().UTC()
	m.webhooks[p.ID] = w
	return w, nil
}
func (m *memWebhookStore) DeleteWebhook(_ context.Context, _ int64, id int64) error {
	if _, ok := m.webhooks[id]; !ok {
		return storage.ErrNotFound
	}
	delete(m.webhooks, id)
	return nil
}

func TestWebhooksAPI_CreateAndTest_Smoke(t *testing.T) {
	var gotSig string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-RSSAM-Signature")
		b, _ := json.Marshal(map[string]any{"ok": true})
		w.WriteHeader(http.StatusNoContent)
		_, _ = w.Write(b)
	}))
	defer receiver.Close()

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	ws := newMemWebhookStore()
	s := New(Dependencies{
		AuthToken:            "secret",
		WebhookStore:         ws,
		WebhookHTTPClient:    guard.HTTPClient(2 * time.Second),
		FetchAllowPrivateNet: true,
		SSRFGuard:            guard,
	})

	api := http.NewServeMux()
	api.HandleFunc("POST /v1/webhooks", s.handleCreateWebhook)
	api.HandleFunc("POST /v1/webhooks/{id}/test", s.handleTestWebhook)
	mux := http.NewServeMux()
	mux.Handle("/v1/", s.wrapAPI(api))

	createReq := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewBufferString(`{
	  "name":"alerts",
	  "url":"`+receiver.URL+`",
	  "method":"POST",
	  "headers":{"X-Foo":"bar"},
	  "secret":"shh",
	  "enabled":true,
	  "on_success_entry":"mark_read"
	}`))
	createReq.Header.Set("X-Auth-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, createReq)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		Data struct {
			ID             int64  `json:"id"`
			Name           string `json:"name"`
			OnSuccessEntry string `json:"on_success_entry"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&createResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if createResp.Data.ID == 0 {
		t.Fatalf("expected id in response")
	}
	if createResp.Data.OnSuccessEntry != "mark_read" {
		t.Fatalf("on_success_entry: %q", createResp.Data.OnSuccessEntry)
	}
	if createResp.Data.Name != "alerts" {
		t.Fatalf("name: %q", createResp.Data.Name)
	}

	testReq := httptest.NewRequest(http.MethodPost, "/v1/webhooks/"+itoa(createResp.Data.ID)+"/test", nil)
	testReq.Header.Set("X-Auth-Token", "secret")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, testReq)
	if rec2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec2.Code, rec2.Body.String())
	}
	if !strings.HasPrefix(gotSig, "sha256=") {
		t.Fatalf("expected signature header sha256=..., got %q", gotSig)
	}
}

func TestWebhooksAPI_CreateTelegram(t *testing.T) {
	var gotPath string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer receiver.Close()

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Dependencies{
		AuthToken:            "secret",
		WebhookStore:         newMemWebhookStore(),
		WebhookHTTPClient:    guard.HTTPClient(2 * time.Second),
		FetchAllowPrivateNet: true,
		SSRFGuard:            guard,
	})
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/webhooks", s.handleCreateWebhook)
	api.HandleFunc("POST /v1/webhooks/{id}/test", s.handleTestWebhook)
	mux := http.NewServeMux()
	mux.Handle("/v1/", s.wrapAPI(api))

	body := `{
	  "kind":"telegram",
	  "telegram":{"api_base":"` + receiver.URL + `","bot_token":"tok","chat_id":"1"},
	  "enabled":true
	}`
	createReq := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewBufferString(body))
	createReq.Header.Set("X-Auth-Token", "secret")
	createReq.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, createReq)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var createResp struct {
		Data struct {
			ID       int64  `json:"id"`
			Kind     string `json:"kind"`
			URL      string `json:"url"`
			Telegram *struct {
				BotToken string `json:"bot_token"`
			} `json:"telegram"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&createResp); err != nil {
		t.Fatal(err)
	}
	if createResp.Data.Kind != "telegram" {
		t.Fatalf("kind=%s", createResp.Data.Kind)
	}
	if strings.Contains(createResp.Data.URL, "tok") {
		t.Fatalf("url leaked token: %s", createResp.Data.URL)
	}
	if createResp.Data.Telegram == nil || createResp.Data.Telegram.BotToken == "tok" {
		t.Fatalf("token should be masked: %+v", createResp.Data.Telegram)
	}

	testReq := httptest.NewRequest(http.MethodPost, "/v1/webhooks/"+itoa(createResp.Data.ID)+"/test", nil)
	testReq.Header.Set("X-Auth-Token", "secret")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, testReq)
	if rec2.Code != http.StatusOK {
		t.Fatalf("test: %d %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(gotPath, "sendMessage") {
		t.Fatalf("path=%s", gotPath)
	}
}

func TestWebhooksAPI_TestMaxAndTelegramReject(t *testing.T) {
	var maxPath string
	maxSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		maxPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer maxSrv.Close()
	tgSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":false,"description":"chat not found"}`))
	}))
	defer tgSrv.Close()

	guard, err := ssrf.New(ssrf.Config{AllowPrivateNetwork: true})
	if err != nil {
		t.Fatal(err)
	}
	s := New(Dependencies{
		AuthToken:            "secret",
		WebhookStore:         newMemWebhookStore(),
		WebhookHTTPClient:    guard.HTTPClient(2 * time.Second),
		FetchAllowPrivateNet: true,
		SSRFGuard:            guard,
	})
	api := http.NewServeMux()
	api.HandleFunc("POST /v1/webhooks", s.handleCreateWebhook)
	api.HandleFunc("POST /v1/webhooks/{id}/test", s.handleTestWebhook)
	mux := http.NewServeMux()
	mux.Handle("/v1/", s.wrapAPI(api))

	createMax := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewBufferString(`{
	  "kind":"max",
	  "max":{"api_base":"`+maxSrv.URL+`","target":"chat_id","target_id":"42"},
	  "enabled":true
	}`))
	createMax.Header.Set("X-Auth-Token", "secret")
	createMax.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, createMax)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create max: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ID int64 `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	testMax := httptest.NewRequest(http.MethodPost, "/v1/webhooks/"+itoa(created.Data.ID)+"/test", nil)
	testMax.Header.Set("X-Auth-Token", "secret")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, testMax)
	if rec2.Code != http.StatusOK {
		t.Fatalf("max test: %d %s", rec2.Code, rec2.Body.String())
	}
	if !strings.Contains(maxPath, "/channel/id/42/messages") {
		t.Fatalf("max path=%s", maxPath)
	}

	createTg := httptest.NewRequest(http.MethodPost, "/v1/webhooks", bytes.NewBufferString(`{
	  "kind":"telegram",
	  "telegram":{"api_base":"`+tgSrv.URL+`","bot_token":"tok","chat_id":"1"}
	}`))
	createTg.Header.Set("X-Auth-Token", "secret")
	createTg.Header.Set("Content-Type", "application/json")
	rec3 := httptest.NewRecorder()
	mux.ServeHTTP(rec3, createTg)
	if rec3.Code != http.StatusCreated {
		t.Fatalf("create tg: %d %s", rec3.Code, rec3.Body.String())
	}
	if err := json.NewDecoder(rec3.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	testTg := httptest.NewRequest(http.MethodPost, "/v1/webhooks/"+itoa(created.Data.ID)+"/test", nil)
	testTg.Header.Set("X-Auth-Token", "secret")
	rec4 := httptest.NewRecorder()
	mux.ServeHTTP(rec4, testTg)
	if rec4.Code != http.StatusBadGateway {
		t.Fatalf("tg reject: %d %s", rec4.Code, rec4.Body.String())
	}
}
