package maxstat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"rssam/internal/ssrf"
)

type Client struct {
	http   *http.Client
	guard  *ssrf.Guard
	cfg    Config
	apiURL string
}

func NewClient(client *http.Client, guard *ssrf.Guard, cfg Config) (*Client, error) {
	cfg = cfg.withDefaults()
	if client == nil {
		client = http.DefaultClient
	}
	apiURL := strings.TrimRight(cfg.APIBaseURL, "/") + "/posts"
	if guard != nil {
		if err := guard.ValidateURL(apiURL); err != nil {
			return nil, fmt.Errorf("maxstat: ssrf validate api url: %w", err)
		}
	}
	return &Client{
		http:   client,
		guard:  guard,
		cfg:    cfg,
		apiURL: apiURL,
	}, nil
}

func (c *Client) UpdateConfig(cfg Config) {
	cfg = cfg.withDefaults()
	c.cfg = cfg
	if base := strings.TrimSpace(cfg.APIBaseURL); base != "" {
		c.apiURL = strings.TrimRight(base, "/") + "/posts"
	}
}

type SearchParams struct {
	Query       string
	Limit       int
	OrderBy     string
	AccessToken string // per-feed override (RSS-Bridge api_token)
}

func (c *Client) Search(ctx context.Context, p SearchParams) (*postsResponse, int, error) {
	limit := p.Limit
	if limit < 1 {
		limit = c.cfg.DefaultLimit
	}
	if limit > 100 {
		limit = 100
	}
	orderBy := strings.TrimSpace(p.OrderBy)
	if orderBy == "" {
		orderBy = "date"
	}

	u, err := url.Parse(c.apiURL)
	if err != nil {
		return nil, 0, fmt.Errorf("maxstat: parse api url: %w", err)
	}
	q := u.Query()
	q.Set("search", p.Query)
	q.Set("order_by", orderBy)
	q.Set("order", "desc")
	q.Set("limit", strconv.Itoa(limit))
	q.Set("offset", "0")
	u.RawQuery = q.Encode()

	if c.guard != nil {
		if err := c.guard.ValidateURL(u.String()); err != nil {
			return nil, 0, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("maxstat: create request: %w", err)
	}
	token, err := resolveAccessToken(p.AccessToken, c.cfg.AccessToken)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Token", token)
	req.Header.Set("User-Agent", "rssam/MaxStatBridge")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("maxstat: http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("maxstat: read body: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, resp.StatusCode, fmt.Errorf("maxstat: rate limited (HTTP 429)")
	}

	var apiErr apiError
	if resp.StatusCode != http.StatusOK {
		_ = json.Unmarshal(body, &apiErr)
		msg := strings.TrimSpace(apiErr.Message)
		if msg == "" {
			msg = strings.TrimSpace(string(body))
		}
		if msg == "" {
			msg = http.StatusText(resp.StatusCode)
		}
		code := strings.TrimSpace(apiErr.Code)
		if code != "" {
			return nil, resp.StatusCode, fmt.Errorf("maxstat api error %s: %s", code, msg)
		}
		return nil, resp.StatusCode, fmt.Errorf("maxstat api HTTP %d: %s", resp.StatusCode, msg)
	}

	var parsed postsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("maxstat: decode response: %w", err)
	}
	return &parsed, resp.StatusCode, nil
}
