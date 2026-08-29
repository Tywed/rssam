package githubrel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"rssam/internal/version"
)

type Release struct {
	Tag         string
	Name        string
	Body        string
	HTMLURL     string
	PublishedAt time.Time
}

type Status struct {
	Current     string
	Latest      string
	UpdateAvail bool
	CheckedOK   bool
	HTMLURL     string
	Notes       string
}

type Client struct {
	Repo    string
	BaseURL string
	HTTP    *http.Client
	TTL    time.Duration
	mu     sync.Mutex
	cached Release
	at     time.Time
	err    error
}

func New(repo string) *Client {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		repo = version.DefaultGitHubRepo
	}
	return &Client{
		Repo: repo,
		HTTP: &http.Client{Timeout: 8 * time.Second},
		TTL:  6 * time.Hour,
	}
}

func (c *Client) Latest() (Release, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.TTL <= 0 {
		c.TTL = 6 * time.Hour
	}
	if !c.at.IsZero() && time.Since(c.at) < c.TTL && c.err == nil && c.cached.Tag != "" {
		return c.cached, nil
	}
	rel, err := c.fetch()
	c.at = time.Now()
	c.err = err
	if err != nil {
		return c.cached, err
	}
	c.cached = rel
	return rel, nil
}

func (c *Client) Status() Status {
	st := Status{Current: version.Version}
	c.mu.Lock()
	rel := c.cached
	ok := c.err == nil && rel.Tag != ""
	stale := c.at.IsZero() || time.Since(c.at) >= c.TTL
	c.mu.Unlock()
	if stale {
		go func() { _, _ = c.Latest() }()
	}
	if !ok {
		return st
	}
	st.CheckedOK = true
	st.Latest = rel.Tag
	st.HTMLURL = rel.HTMLURL
	st.Notes = rel.Body
	st.UpdateAvail = version.CompareSemver(st.Current, rel.Tag) < 0
	return st
}

func (c *Client) apiRoot() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return "https://api.github.com"
}

func (c *Client) fetch() (Release, error) {
	url := fmt.Sprintf("%s/repos/%s/releases/latest", strings.TrimRight(c.apiRoot(), "/"), c.Repo)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "rssam")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return Release{}, fmt.Errorf("no release")
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("github %d", resp.StatusCode)
	}
	var raw struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Release{}, err
	}
	if strings.TrimSpace(raw.TagName) == "" {
		return Release{}, fmt.Errorf("empty tag")
	}
	return Release{
		Tag:         raw.TagName,
		Name:        raw.Name,
		Body:        raw.Body,
		HTMLURL:     raw.HTMLURL,
		PublishedAt: raw.PublishedAt,
	}, nil
}
