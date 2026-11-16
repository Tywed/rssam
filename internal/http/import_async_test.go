package httpserver

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rssam/internal/ssrf"
	"rssam/internal/storage"
)

// ctxCheckingFeedStore behaves like a real pgx store: any call with a
// cancelled context fails.
type ctxCheckingFeedStore struct{ *opmlFeedStore }

func (s ctxCheckingFeedStore) CreateFeed(ctx context.Context, userID int64, p storage.CreateFeedParams) (storage.Feed, error) {
	if err := ctx.Err(); err != nil {
		return storage.Feed{}, err
	}
	return s.opmlFeedStore.CreateFeed(ctx, userID, p)
}

func (s ctxCheckingFeedStore) ListFeeds(ctx context.Context, userID int64, limit, offset int) ([]storage.Feed, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, err
	}
	return s.opmlFeedStore.ListFeeds(ctx, userID, limit, offset)
}

func TestOPMLImportAsync_OutlivesRequestContext(t *testing.T) {
	guard, err := ssrf.New(ssrf.Config{LookupHost: publicExampleLookup})
	if err != nil {
		t.Fatal(err)
	}
	feeds := ctxCheckingFeedStore{newOpmlFeedStore()}
	s := New(Dependencies{
		UserStore:      secretTokenUsers(),
		CategoryStore:  newOpmlCategoryStore(),
		FeedStore:      feeds,
		SSRFGuard:      guard,
		MaxImportFeeds: 500,
	})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/feeds/import", s.handleImportFeeds)
	mux.HandleFunc("GET /v1/feeds/import/jobs/{jobID}", s.handleGetImportJob)
	h := s.wrapAPI(mux)

	fixture, err := os.ReadFile(filepath.Join("..", "opml", "testdata", "sample.opml"))
	if err != nil {
		t.Fatal(err)
	}
	fixture = bytes.ReplaceAll(fixture, []byte("https://go.dev/blog/.atom"), []byte("https://example.com/go.atom"))

	// Like net/http: the request context is cancelled once the handler returns.
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/v1/feeds/import?async=1", bytes.NewReader(fixture)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("X-Auth-Token", "secret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	cancel()
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	i := strings.Index(body, `"job_id":"`)
	if i < 0 {
		t.Fatalf("no job id: %s", body)
	}
	jobID := body[i+len(`"job_id":"`):]
	jobID = jobID[:strings.Index(jobID, `"`)]

	deadline := time.Now().Add(5 * time.Second)
	for {
		jr := httptest.NewRequest(http.MethodGet, "/v1/feeds/import/jobs/"+jobID, nil)
		jr.Header.Set("X-Auth-Token", "secret")
		jrec := httptest.NewRecorder()
		h.ServeHTTP(jrec, jr)
		if jrec.Code != http.StatusOK {
			t.Fatalf("job status=%d body=%s", jrec.Code, jrec.Body.String())
		}
		if strings.Contains(jrec.Body.String(), `"status":"done"`) {
			if !strings.Contains(jrec.Body.String(), `"feeds_created":2`) {
				t.Fatalf("import ran with a dead context: %s", jrec.Body.String())
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not finish: %s", jrec.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got, _, _ := feeds.ListFeeds(context.Background(), 1, 100, 0); len(got) != 2 {
		t.Fatalf("feeds created=%d want 2", len(got))
	}
}
