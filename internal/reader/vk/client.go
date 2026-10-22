package vk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"rssam/internal/bridge/v1"
	"rssam/internal/ssrf"
)

type Client struct {
	http   *http.Client
	guard  *ssrf.Guard
	cfg    Config
	apiURL string

	paceMu sync.Mutex
	next   time.Time
}

func NewClient(client *http.Client, guard *ssrf.Guard, cfg Config) (*Client, error) {
	cfg = cfg.withDefaults()
	if strings.TrimSpace(cfg.AccessToken) == "" {
		return nil, errMissingToken
	}
	if client == nil {
		client = http.DefaultClient
	}
	if guard != nil {
		if err := guard.ValidateURL(vkAPIBaseURL); err != nil {
			return nil, fmt.Errorf("vk: ssrf validate api url: %w", err)
		}
	}
	return &Client{
		http:   client,
		guard:  guard,
		cfg:    cfg,
		apiURL: vkAPIBaseURL,
	}, nil
}

// pace blocks until the token's next request slot; a cancelled context
// returns its error.
func (c *Client) pace(ctx context.Context) error {
	c.paceMu.Lock()
	now := time.Now()
	wait := c.next.Sub(now)
	if wait < 0 {
		wait = 0
	}
	c.next = now.Add(wait + minRequestGap)
	c.paceMu.Unlock()
	if wait == 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// UpdateConfig replaces client settings at runtime.
func (c *Client) UpdateConfig(cfg Config) {
	cfg = cfg.withDefaults()
	c.cfg = cfg
}

type SearchParams struct {
	Query     string
	Count     int
	StartTime int64
	EndTime   int64
}

func (c *Client) Search(ctx context.Context, p SearchParams) (*searchResponseBody, *apiError, error) {
	count := p.Count
	if count < 1 {
		count = c.cfg.DefaultCount
	}
	if count > 100 {
		count = 100
	}

	q := url.Values{}
	q.Set("q", p.Query)
	q.Set("count", strconv.Itoa(count))
	q.Set("start_time", strconv.FormatInt(p.StartTime, 10))
	q.Set("end_time", strconv.FormatInt(p.EndTime, 10))
	return c.call(ctx, newsfeedSearchPath, q)
}

// WallGet reads the newest wallGetCount posts of a wall (filter=all: the
// community's own posts, reposts and posts by others).
func (c *Client) WallGet(ctx context.Context, w Wall) (*searchResponseBody, *apiError, error) {
	q := url.Values{}
	if w.Domain != "" {
		q.Set("domain", w.Domain)
	} else {
		q.Set("owner_id", strconv.Itoa(w.OwnerID))
	}
	q.Set("count", strconv.Itoa(wallGetCount))
	q.Set("filter", "all")
	return c.call(ctx, wallGetPath, q)
}

// call performs one extended API method call; API errors come back as the
// second value, transport and JSON problems as the third.
func (c *Client) call(ctx context.Context, method string, q url.Values) (*searchResponseBody, *apiError, error) {
	u, err := url.Parse(c.apiURL + method)
	if err != nil {
		return nil, nil, fmt.Errorf("vk: parse api url: %w", err)
	}
	q.Set("extended", "1")
	q.Set("v", c.cfg.APIVersion)
	u.RawQuery = q.Encode()

	if c.guard != nil {
		if err := c.guard.ValidateURL(u.String()); err != nil {
			return nil, nil, err
		}
	}
	if err := c.pace(ctx); err != nil {
		return nil, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, fmt.Errorf("vk: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.cfg.AccessToken)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("vk: http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, nil, fmt.Errorf("vk: read body: %w", err)
	}

	var parsed searchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, nil, &bridge.LayoutError{Bridge: "vk", Marker: "api json", Err: err}
	}
	if parsed.Error != nil {
		return nil, parsed.Error, nil
	}
	if parsed.Response == nil {
		return nil, nil, fmt.Errorf("vk: empty response")
	}
	return parsed.Response, nil, nil
}
