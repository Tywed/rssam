package rutube

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"rssam/internal/ssrf"
)

const personVideosPathPrefix = "/video/person/"

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
	return &Client{
		http:   client,
		guard:  guard,
		cfg:    cfg,
		apiURL: cfg.APIBaseURL + personVideosPathPrefix,
	}, nil
}

// UpdateConfig replaces client settings at runtime.
func (c *Client) UpdateConfig(cfg Config) {
	cfg = cfg.withDefaults()
	c.cfg = cfg
	c.apiURL = cfg.APIBaseURL + personVideosPathPrefix
}

func (c *Client) ListPersonVideos(ctx context.Context, channelID string) (*personVideosResponse, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, errMissingChannel
	}
	reqURL := c.apiURL + channelID + "/"
	if c.guard != nil {
		if err := c.guard.ValidateURL(reqURL); err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("rutube: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if ua := strings.TrimSpace(c.cfg.UserAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rutube: http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("rutube: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rutube: unexpected status %d", resp.StatusCode)
	}

	var parsed personVideosResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("rutube: decode response: %w", err)
	}
	return &parsed, nil
}
