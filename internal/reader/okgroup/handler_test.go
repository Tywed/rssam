package okgroup

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"rssam/internal/bridge/v1"
)

type okServer struct {
	groupPage atomic.Pointer[[]byte]
	topics    atomic.Int32
	srv       *httptest.Server
}

func newOKServer(t *testing.T, group []byte, topic []byte) *okServer {
	t.Helper()
	s := &okServer{}
	s.groupPage.Store(&group)
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rianovosti" || r.URL.Path == "/group/58904363073576":
			_, _ = w.Write(*s.groupPage.Load())
		case strings.HasPrefix(r.URL.Path, "/group/58904363073576/topic/"):
			s.topics.Add(1)
			if strings.HasSuffix(r.URL.Path, "/157975841086248") {
				_, _ = w.Write(topic)
				return
			}
			// Other posts: the topic page is not available.
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/someuser":
			_, _ = w.Write(mustRead(t, "profile.html"))
		case r.URL.Path == "/limited":
			w.Header().Set("Retry-After", "120")
			w.WriteHeader(http.StatusTooManyRequests)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newTestHandler(s *okServer) *Handler {
	h := NewHandler(NewClient(s.srv.Client(), nil, Config{BaseURL: s.srv.URL}))
	h.now = func() time.Time { return time.Date(2026, 10, 16, 18, 36, 0, 0, time.UTC) }
	return h
}

// First poll: every listed post becomes an entry, each one read from its
// topic page when that page answers; the state remembers the ids. Second
// poll with the same page: nothing fetched, nothing emitted. A new post on
// the page: exactly one topic request and one entry.
func TestFetch_NewPostsOnly(t *testing.T) {
	group := mustRead(t, "group.html")
	s := newOKServer(t, group, mustRead(t, "topic.html"))
	h := newTestHandler(s)
	ctx := context.Background()

	res, err := h.Fetch(ctx, bridge.Request{FeedURL: "https://ok.ru/rianovosti"})
	if err != nil || len(res.Entries) != 3 {
		t.Fatalf("first poll: %d entries, %v", len(res.Entries), err)
	}
	if got := s.topics.Load(); got != 3 {
		t.Fatalf("topic requests = %d, want one per new post", got)
	}
	e := res.Entries[0]
	if e.URL != "https://ok.ru/group/58904363073576/topic/157975841086248" || e.Title != "В Надыме почти готова новая заправка" {
		t.Fatalf("entry = %+v", e)
	}
	if e.PublishedAt == nil || !e.PublishedAt.Equal(time.Date(2026, 10, 16, 12, 59, 2, 0, time.UTC)) {
		t.Fatalf("published = %v (want the topic page's exact time)", e.PublishedAt)
	}
	if !strings.Contains(e.Content, "Открытие запланировано на 21 октября") || strings.Contains(e.Content, "<p>В Надыме почти готова новая заправка") {
		t.Fatalf("content must be the full body without the repeated headline:\n%s", e.Content)
	}
	if !strings.Contains(e.Content, `<img src="https://i.okcdn.ru/`) || e.Author == nil || *e.Author != "Информационное агентство ЯНАО" {
		t.Fatalf("content/author: %+v", e)
	}
	// Topic page unavailable: the list data carries the entry, minute precision from the label.
	e2 := res.Entries[1]
	if e2.Title != "Если видишь, что кого-то травят, не оставайся в стороне." || e2.PublishedAt == nil || !e2.PublishedAt.Equal(time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("fallback entry = %+v published=%v", e2, e2.PublishedAt)
	}
	var st state
	if err := json.Unmarshal(res.State, &st); err != nil || len(st.Seen) != 3 || st.Seen[0] != "157975841086248" {
		t.Fatalf("state = %s (%v)", res.State, err)
	}

	res2, err := h.Fetch(ctx, bridge.Request{FeedURL: "https://ok.ru/rianovosti", State: res.State})
	if err != nil || len(res2.Entries) != 0 || res2.State != nil {
		t.Fatalf("quiet poll: entries=%d state=%s err=%v", len(res2.Entries), res2.State, err)
	}
	if got := s.topics.Load(); got != 3 {
		t.Fatalf("quiet poll must not fetch topics: %d", got)
	}

	// A new post appears on top of the page.
	newer := strings.Replace(string(group), `<div class="feed-list">`,
		`<div class="feed-list"><div class="feed-w"><div data-seen-params="{&#34;type&#34;:&#34;FEED&#34;,&#34;data&#34;:{&#34;mediaTopicId&#34;:&#34;157999999999999&#34;}}">`+
			`<time>вчера 23:10</time><div class="media-text_cnt_tx">Пост без заголовка<br>вторая строка</div></div></div>`, 1)
	nb := []byte(newer)
	s.groupPage.Store(&nb)
	res3, err := h.Fetch(ctx, bridge.Request{FeedURL: "https://ok.ru/rianovosti", State: res.State})
	if err != nil || len(res3.Entries) != 1 || s.topics.Load() != 4 {
		t.Fatalf("new post: entries=%d topics=%d err=%v", len(res3.Entries), s.topics.Load(), err)
	}
	e3 := res3.Entries[0]
	if e3.Title != "Пост без заголовка вторая строка" || !strings.Contains(e3.Content, "Пост без заголовка<br>\nвторая строка") {
		t.Fatalf("untitled entry = %+v", e3)
	}
	if e3.PublishedAt == nil || !e3.PublishedAt.Equal(time.Date(2026, 10, 15, 20, 10, 0, 0, time.UTC)) {
		t.Fatalf("published = %v", e3.PublishedAt)
	}
	_ = json.Unmarshal(res3.State, &st)
	if len(st.Seen) != 4 || st.Seen[0] != "157999999999999" || st.Seen[1] != "157975841086248" {
		t.Fatalf("state = %v", st.Seen)
	}
}

// More new posts than maxTopicFetches: the rest come from the list only;
// a state longer than seenKeep is cut.
func TestFetch_TopicFetchCap(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html><head><meta property="og:url" content="https://ok.ru/group/58904363073576"><meta property="og:title" content="G | Группа на OK.ru"></head><body><div class="feed-list">`)
	for i := range 12 {
		b.WriteString(`<div class="feed-w"><div class="media-text_cnt_tx" data-tid="1000` + string(rune('a'+i)) + `">text</div><time>10:00</time></div>`)
	}
	b.WriteString(`</div></body></html>`)
	s := newOKServer(t, []byte(b.String()), nil)
	h := newTestHandler(s)
	old := make([]string, seenKeep)
	for i := range old {
		old[i] = "old"
	}
	prev, _ := json.Marshal(state{Seen: old})
	res, err := h.Fetch(context.Background(), bridge.Request{FeedURL: "https://ok.ru/group/58904363073576", State: prev})
	if err != nil || len(res.Entries) != 12 || s.topics.Load() != maxTopicFetches {
		t.Fatalf("entries=%d topics=%d err=%v", len(res.Entries), s.topics.Load(), err)
	}
	var st state
	_ = json.Unmarshal(res.State, &st)
	if len(st.Seen) != seenKeep || st.Seen[0] != "1000a" || st.Seen[12] != "old" {
		t.Fatalf("state = %d %v", len(st.Seen), st.Seen[:13])
	}
}

func TestFetch_Errors(t *testing.T) {
	s := newOKServer(t, mustRead(t, "group.html"), nil)
	h := newTestHandler(s)
	ctx := context.Background()
	if _, err := h.Fetch(ctx, bridge.Request{FeedURL: "https://ok.ru/profile/1"}); !errors.Is(err, errInvalidFeedURL) {
		t.Fatalf("profile url: %v", err)
	}
	if _, err := h.Fetch(ctx, bridge.Request{FeedURL: "https://ok.ru/someuser"}); !errors.Is(err, ErrNotGroup) {
		t.Fatalf("profile page: %v", err)
	}
	if _, err := h.Fetch(ctx, bridge.Request{FeedURL: "https://ok.ru/nosuchgroup"}); !errors.Is(err, ErrGroupNotFound) {
		t.Fatalf("404: %v", err)
	}
	_, err := h.Fetch(ctx, bridge.Request{FeedURL: "https://ok.ru/limited"})
	at, ok := bridge.RetryAt(err)
	if !ok || time.Until(at) < 100*time.Second {
		t.Fatalf("429: %v (retry at %v)", err, at)
	}
	title, err := h.DiscoverTitle(ctx, "https://ok.ru/rianovosti")
	if err != nil || title != "Информационное агентство ЯНАО" {
		t.Fatalf("title = %q %v", title, err)
	}
}
