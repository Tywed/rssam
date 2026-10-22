package worker

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// A 429 is the endpoint saying "not now", not a failed delivery: the row
// keeps its attempt count and the whole webhook waits for the announced
// time, so the rows behind it are not sent into the same wall (each would
// burn an attempt and write a failed status). The wait is bounded: a
// provider cannot park a webhook for longer than webhookFloodMaxWait.
const (
	webhookFloodDefaultWait = 10 * time.Second
	webhookFloodMaxWait     = time.Hour
)

// webhookPauses is the set of webhooks waiting out a 429, in memory: this
// process is the only deliverer, and after a restart the rows are simply due.
type webhookPauses struct {
	mu    sync.Mutex
	until map[int64]time.Time
}

func (p *webhookPauses) pause(webhookID int64, until time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.until == nil {
		p.until = make(map[int64]time.Time)
	}
	if until.After(p.until[webhookID]) {
		p.until[webhookID] = until
	}
}

// active returns the webhooks still paused at now and the earliest moment
// one of them resumes; expired pauses are forgotten.
func (p *webhookPauses) active(now time.Time) (ids []int64, resume time.Time, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, until := range p.until {
		if !until.After(now) {
			delete(p.until, id)
			continue
		}
		ids = append(ids, id)
		if !ok || until.Before(resume) {
			resume, ok = until, true
		}
	}
	return ids, resume, ok
}

// retryAfterOf is how long a 429 asks to wait: Telegram's
// parameters.retry_after, else the Retry-After header (seconds or HTTP date),
// else webhookFloodDefaultWait.
func retryAfterOf(resp *http.Response, body string, now time.Time) time.Duration {
	d := webhookFloodDefaultWait
	var tg struct {
		Parameters struct {
			RetryAfter float64 `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(body), &tg); err == nil && tg.Parameters.RetryAfter > 0 {
		d = time.Duration(tg.Parameters.RetryAfter * float64(time.Second))
	} else if h := strings.TrimSpace(resp.Header.Get("Retry-After")); h != "" {
		if secs, err := strconv.ParseFloat(h, 64); err == nil && secs > 0 {
			d = time.Duration(secs * float64(time.Second))
		} else if at, err := http.ParseTime(h); err == nil && at.After(now) {
			d = at.Sub(now)
		}
	}
	return min(max(d, time.Second), webhookFloodMaxWait)
}
