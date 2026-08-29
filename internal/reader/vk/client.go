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
	if strings.TrimSpace(cfg.AccessToken) == "" {
		return nil, errMissingToken
	}
	if client == nil {
		client = http.DefaultClient
	}
	apiURL := vkAPIBaseURL + newsfeedSearchPath
	if guard != nil {
		if err := guard.ValidateURL(apiURL); err != nil {
			return nil, fmt.Errorf("vk: ssrf validate api url: %w", err)
		}
	}
	return &Client{
		http:   client,
		guard:  guard,
		cfg:    cfg,
		apiURL: apiURL,
	}, nil
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

	u, err := url.Parse(c.apiURL)
	if err != nil {
		return nil, nil, fmt.Errorf("vk: parse api url: %w", err)
	}
	q := u.Query()
	q.Set("q", p.Query)
	q.Set("extended", "1")
	q.Set("count", strconv.Itoa(count))
	q.Set("start_time", strconv.FormatInt(p.StartTime, 10))
	q.Set("end_time", strconv.FormatInt(p.EndTime, 10))
	q.Set("v", c.cfg.APIVersion)
	u.RawQuery = q.Encode()

	if c.guard != nil {
		if err := c.guard.ValidateURL(u.String()); err != nil {
			return nil, nil, err
		}
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
		return nil, nil, fmt.Errorf("vk: decode response: %w", err)
	}
	if parsed.Error != nil {
		return nil, parsed.Error, nil
	}
	if parsed.Response == nil {
		return nil, nil, fmt.Errorf("vk: empty response")
	}
	return parsed.Response, nil, nil
}
