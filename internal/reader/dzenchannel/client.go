package dzenchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"rssam/internal/ssrf"
)

const (
	defaultExportURL = "https://dzen.ru/api/web/v1/export"
	defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	defaultCookie    = "zen_sso_checked=1"
	rateLimitPause   = 10 * time.Minute
	// One export page is ~120–270 KB; the limit only guards against a
	// runaway response.
	maxBodyBytes = 4 << 20
)

// Config controls the Dzen channel bridge.
type Config struct {
	ExportURL string
	UserAgent string
	Cookie    string
}

func (c Config) withDefaults() Config {
	c.ExportURL = strings.TrimSpace(c.ExportURL)
	if c.ExportURL == "" {
		c.ExportURL = defaultExportURL
	}
	if strings.TrimSpace(c.UserAgent) == "" {
		c.UserAgent = defaultUserAgent
	}
	if strings.TrimSpace(c.Cookie) == "" {
		c.Cookie = defaultCookie
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

// exportPage is the part of an export response the bridge reads.
type exportPage struct {
	Channel struct {
		Source struct {
			ID          string `json:"id"`
			Title       string `json:"title"`
			Description string `json:"description"`
		} `json:"source"`
	} `json:"channel"`
	Tabs []struct {
		ID string `json:"id"`
	} `json:"tabs"`
	FeedData struct {
		Items []exportItem `json:"items"`
	} `json:"feedData"`
}

type exportItem struct {
	ID              string      `json:"id"`
	Type            string      `json:"type"`
	Title           string      `json:"title"`
	Text            string      `json:"text"`
	Link            string      `json:"link"`
	ShareLink       string      `json:"shareLink"`
	PublicationDate json.Number `json:"publicationDate"`
	Image           *struct {
		URLTemplate string `json:"urlTemplate"`
		Namespace   string `json:"namespace"`
		SizeName    string `json:"sizeName"`
	} `json:"image"`
}

// tabTypes lists the content types the channel page offers.
func (p *exportPage) tabTypes() []string {
	out := make([]string, 0, len(p.Tabs))
	for _, t := range p.Tabs {
		for _, k := range allTypes {
			if t.ID == k {
				out = append(out, k)
			}
		}
	}
	return out
}

// Export fetches one page of a channel's content of the given type, newest first.
func (c *Client) Export(ctx context.Context, ch Channel, contentType string) (*exportPage, error) {
	key, value := ch.queryParam()
	q := url.Values{}
	q.Set(key, value)
	q.Set("content_type", contentType)
	q.Set("sort_type", "regular")
	q.Set("country_code", "ru")
	q.Set("lang", "ru")
	q.Set("clid", "1410")
	reqURL := c.cfg.ExportURL + "?" + q.Encode()
	if c.guard != nil {
		if err := c.guard.ValidateURL(reqURL); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("dzen_channel: create request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Cookie", c.cfg.Cookie)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dzen_channel: http request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("dzen_channel: read body: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, fmt.Errorf("%w: %s", ErrChannelNotFound, ch)
	case http.StatusTooManyRequests:
		pause := rateLimitPause
		if s, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && s > 0 {
			pause = time.Duration(s) * time.Second
		}
		return nil, CooldownError{At: time.Now().Add(pause)}
	default:
		return nil, fmt.Errorf("dzen_channel: unexpected status %d", resp.StatusCode)
	}
	var page exportPage
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, fmt.Errorf("dzen_channel: decode response: %w", err)
	}
	return &page, nil
}
