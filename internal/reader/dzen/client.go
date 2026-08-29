package dzen

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"rssam/internal/ssrf"
)

type Client struct {
	http   *http.Client
	guard  *ssrf.Guard
	cfg    Config
}

func NewClient(client *http.Client, guard *ssrf.Guard, cfg Config) (*Client, error) {
	cfg = cfg.withDefaults()
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{
		http:  client,
		guard: guard,
		cfg:   cfg,
	}, nil
}

func (c *Client) UpdateConfig(cfg Config) {
	c.cfg = cfg.withDefaults()
}

func (c *Client) SearchNews(ctx context.Context, query string) ([]NewsItem, error) {
	query = NormalizeQuery(query)
	if query == "" {
		return nil, errMissingQuery
	}
	reqURL := SearchPageURL(c.cfg.SearchURL, query)
	if c.guard != nil {
		if err := c.guard.ValidateURL(reqURL); err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dzen: create request: %w", err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9")
	if ua := strings.TrimSpace(c.cfg.UserAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	if cookie := strings.TrimSpace(c.cfg.Cookie); cookie != "" {
		req.Header.Set("Cookie", cookie)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dzen: http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("dzen: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("dzen: unexpected status %d", resp.StatusCode)
	}

	items, err := ParseSearchHTML(body)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errEmptyResults
	}
	return items, nil
}
