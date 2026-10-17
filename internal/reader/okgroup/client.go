package okgroup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"rssam/internal/ssrf"
)

const (
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	rateLimitPause   = 10 * time.Minute
	// A group page is ~450–510 KB, a topic page ~250 KB; the limit only
	// guards against a runaway response.
	maxBodyBytes = 4 << 20
)

// Config controls the OK group bridge.
type Config struct {
	// BaseURL replaces https://ok.ru in tests.
	BaseURL   string
	UserAgent string
}

func (c Config) withDefaults() Config {
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if c.BaseURL == "" {
		c.BaseURL = "https://ok.ru"
	}
	if strings.TrimSpace(c.UserAgent) == "" {
		c.UserAgent = defaultUserAgent
	}
	return c
}

type Client struct {
	http  *http.Client
	guard *ssrf.Guard
	cfg   Config
}

func NewClient(client *http.Client, guard *ssrf.Guard, cfg Config) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{http: client, guard: guard, cfg: cfg.withDefaults()}
}

// GroupPage downloads the desktop page of the group: the newest ~10 posts.
func (c *Client) GroupPage(ctx context.Context, g Group) ([]byte, error) {
	path := "/" + g.Slug
	if g.Slug == "" {
		path = "/group/" + g.ID
	}
	body, status, err := c.get(ctx, c.cfg.BaseURL+path)
	if err != nil {
		return nil, err
	}
	switch status {
	case http.StatusOK:
		return body, nil
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrGroupNotFound, g)
	default:
		return nil, fmt.Errorf("ok_group: unexpected status %d", status)
	}
}

// TopicPage downloads one post; ok=false for any non-200 answer, which the
// caller covers with the data already seen on the group page.
func (c *Client) TopicPage(ctx context.Context, gid, topicID string) ([]byte, bool, error) {
	body, status, err := c.get(ctx, c.cfg.BaseURL+"/group/"+gid+"/topic/"+topicID)
	if err != nil {
		return nil, false, err
	}
	return body, status == http.StatusOK, nil
}

func (c *Client) get(ctx context.Context, reqURL string) ([]byte, int, error) {
	if c.guard != nil {
		if err := c.guard.ValidateURL(reqURL); err != nil {
			return nil, 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("ok_group: create request: %w", err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9")
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("ok_group: http request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		pause := rateLimitPause
		if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s > 0 {
			pause = time.Duration(s) * time.Second
		}
		return nil, 0, CooldownError{At: time.Now().Add(pause)}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("ok_group: read body: %w", err)
	}
	return body, resp.StatusCode, nil
}
