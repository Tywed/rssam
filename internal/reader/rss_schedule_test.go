package reader

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

const scheduleRSS = `<?xml version="1.0"?><rss version="2.0"><channel><title>T</title><ttl>90</ttl>
<item><title>One</title><link>https://example.com/1</link></item></channel></rss>`

func TestRSSFetcher_ServerFreshnessHints(t *testing.T) {
	var headers http.Header
	var status int
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			w.Header()[k] = v
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	f := NewRSSFetcher(&http.Client{Timeout: 2 * time.Second}, "rssam-test", nil, "")
	fetch := func() (FetchResult, error) {
		return f.Fetch(context.Background(), srv.URL, "", "", false, false)
	}
	within := func(t *testing.T, got time.Time, want time.Duration) {
		t.Helper()
		d := time.Until(got)
		if d < want-5*time.Second || d > want+5*time.Second {
			t.Fatalf("delay=%s want ~%s", d, want)
		}
	}

	t.Run("ttl from body", func(t *testing.T) {
		headers, status, body = http.Header{}, 200, scheduleRSS
		res, err := fetch()
		if err != nil || len(res.Entries) != 1 {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		within(t, res.MinNextCheck, 90*time.Minute)
	})
	t.Run("max-age beats smaller ttl", func(t *testing.T) {
		headers, status, body = http.Header{"Cache-Control": {"public, max-age=7200"}}, 200, scheduleRSS
		res, err := fetch()
		if err != nil {
			t.Fatal(err)
		}
		within(t, res.MinNextCheck, 2*time.Hour)
	})
	t.Run("s-maxage preferred, expires fallback, short values ignored", func(t *testing.T) {
		headers, status, body = http.Header{"Cache-Control": {"max-age=60, s-maxage=1800"}}, 200, "<rss version=\"2.0\"><channel><title>x</title></channel></rss>"
		res, err := fetch()
		if err != nil {
			t.Fatal(err)
		}
		within(t, res.MinNextCheck, 30*time.Minute)

		headers = http.Header{"Expires": {time.Now().Add(45 * time.Minute).UTC().Format(http.TimeFormat)}}
		res, _ = fetch()
		within(t, res.MinNextCheck, 45*time.Minute)

		headers = http.Header{"Cache-Control": {"max-age=30"}}
		res, _ = fetch()
		if !res.MinNextCheck.IsZero() {
			t.Fatalf("30 s max-age must be ignored, got %v", res.MinNextCheck)
		}
	})
	t.Run("hints are capped at a day", func(t *testing.T) {
		headers, status, body = http.Header{"Cache-Control": {"max-age=604800"}}, 200, scheduleRSS
		res, _ := fetch()
		within(t, res.MinNextCheck, maxServerDelay)
	})
	t.Run("304 keeps caching hint", func(t *testing.T) {
		headers, status, body = http.Header{"Cache-Control": {"max-age=600"}}, 304, ""
		res, err := fetch()
		if err != nil || !res.NotModified {
			t.Fatalf("res=%+v err=%v", res, err)
		}
		within(t, res.MinNextCheck, 10*time.Minute)
	})
	t.Run("429 with Retry-After is a retry, not a failure", func(t *testing.T) {
		headers, status, body = http.Header{"Retry-After": {"120"}}, 429, ""
		_, err := fetch()
		at, ok := RetryAt(err)
		if !ok {
			t.Fatalf("want RetryAtError, got %v", err)
		}
		within(t, at, 2*time.Minute)

		headers = http.Header{"Retry-After": {time.Now().Add(5 * time.Minute).UTC().Format(http.TimeFormat)}}
		status = 503
		_, err = fetch()
		at, ok = RetryAt(err)
		if !ok {
			t.Fatalf("want RetryAtError for 503 with date, got %v", err)
		}
		within(t, at, 5*time.Minute)
	})
	t.Run("429 without Retry-After is an ordinary failure", func(t *testing.T) {
		headers, status, body = http.Header{}, 429, ""
		_, err := fetch()
		if _, ok := RetryAt(err); ok || err == nil {
			t.Fatalf("err=%v", err)
		}
	})
	t.Run("410 is ErrFeedGone", func(t *testing.T) {
		headers, status, body = http.Header{}, 410, ""
		_, err := fetch()
		if !errors.Is(err, ErrFeedGone) {
			t.Fatalf("err=%v", err)
		}
	})
}

func TestFeedTTL_MalformedIgnored(t *testing.T) {
	now := time.Now()
	for _, in := range []string{"", "<ttl></ttl>", "<ttl>abc</ttl>", "<ttl>-5</ttl>", "<ttl>12345678901</ttl>", "<ttl>60"} {
		if got := feedTTL([]byte(in), now); !got.IsZero() {
			t.Errorf("%q → %v, want zero", in, got)
		}
	}
	if got := feedTTL([]byte("<channel><ttl> 15 </ttl>"), now); got.Sub(now) != 15*time.Minute {
		t.Errorf("got %v", got.Sub(now))
	}
}
