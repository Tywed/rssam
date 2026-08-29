package smotrim

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"rssam/internal/ssrf"
)

type Client struct {
	http  *http.Client
	guard *ssrf.Guard
	cfg   Config
}

func NewClient(client *http.Client, guard *ssrf.Guard, cfg Config) (*Client, error) {
	cfg = cfg.withDefaults()
	if client == nil {
		client = http.DefaultClient
	}
	return &Client{http: client, guard: guard, cfg: cfg}, nil
}

func (c *Client) UpdateConfig(cfg Config) {
	c.cfg = cfg.withDefaults()
}

func (c *Client) ListBrandVideos(ctx context.Context, st FetchState) ([]VideoItem, string, error) {
	items, brandTitle, err := c.listViaGraphQL(ctx, st)
	if err == nil && len(items) > 0 {
		return items, brandTitle, nil
	}
	return c.listViaBrandPage(ctx, st)
}

func (c *Client) listViaBrandPage(ctx context.Context, st FetchState) ([]VideoItem, string, error) {
	pageURL := BrandPageURL(c.cfg.BrandBaseURL, st.BrandID)
	if c.guard != nil {
		if err := c.guard.ValidateURL(pageURL); err != nil {
			return nil, "", err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("smotrim: create request: %w", err)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9")
	if ua := strings.TrimSpace(c.cfg.UserAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("smotrim: http request: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 12<<20))
	if err != nil {
		return nil, "", fmt.Errorf("smotrim: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("smotrim: unexpected status %d", resp.StatusCode)
	}
	return ExtractVideosFromBrandHTML(body, time.Now(), st.VideoType, st.Limit)
}

func (c *Client) listViaGraphQL(ctx context.Context, st FetchState) ([]VideoItem, string, error) {
	brandID, err := strconv.Atoi(strings.TrimSpace(st.BrandID))
	if err != nil || brandID <= 0 {
		return nil, "", errMissingBrandID
	}
	if c.guard != nil {
		if err := c.guard.ValidateURL(c.cfg.GraphQLURL); err != nil {
			return nil, "", err
		}
	}

	now := time.Now()
	videoType := normalizeVideoType(st.VideoType)
	var (
		items      []VideoItem
		brandTitle string
	)

	switch videoType {
	case "plot":
		items, brandTitle, err = c.fetchPlots(ctx, brandID, st.Limit)
	case "episode":
		items, brandTitle, err = c.fetchEpisodes(ctx, brandID, st.Limit)
	default:
		var errPlots, errEpisodes error
		plots, plotBrand, errPlots := c.fetchPlots(ctx, brandID, st.Limit)
		episodes, epBrand, errEpisodes := c.fetchEpisodes(ctx, brandID, st.Limit)
		if len(plots) > 0 {
			items = append(items, plots...)
			brandTitle = plotBrand
		}
		if len(episodes) > 0 {
			items = mergeVideoItems(items, episodes)
			if brandTitle == "" {
				brandTitle = epBrand
			}
		}
		if len(items) == 0 {
			if errPlots != nil {
				return nil, "", errPlots
			}
			return nil, "", errEpisodes
		}
		sortVideoItems(items)
		if st.Limit > 0 && len(items) > st.Limit {
			items = items[:st.Limit]
		}
		err = nil
	}
	if err != nil {
		return nil, "", err
	}
	for i := range items {
		if items[i].PublishedAt.IsZero() && items[i].DateText != "" {
			items[i].PublishedAt = ParsePublishedTime(items[i].DateText, now)
		}
	}
	if len(items) == 0 {
		return nil, brandTitle, errEmptyResults
	}
	return items, brandTitle, nil
}

func (c *Client) fetchPlots(ctx context.Context, brandID, limit int) ([]VideoItem, string, error) {
	var payload gqlPlotsResponse
	if err := c.graphql(ctx, brandMorePlotsQuery, map[string]any{
		"id":    brandID,
		"limit": limit,
		"page":  1,
	}, &payload); err != nil {
		return nil, "", err
	}
	brandTitle := payload.Brand.Title
	out := make([]VideoItem, 0, len(payload.Brand.Plots.Data))
	for _, plot := range payload.Brand.Plots.Data {
		if plot.ID <= 0 || strings.TrimSpace(plot.Title) == "" {
			continue
		}
		item := VideoItem{
			PublicID:     plot.ID,
			Title:        plot.Title,
			BrandTitle:   brandTitle,
			BrandID:      int64(brandID),
			VideoType:    "plot",
			PublishedAt:  ParsePublishedTime(plot.PublishedAt, time.Now()),
			Thumbnail:    firstPresetLink(plot.Images),
			EpisodeTitle: plot.Episode.Title,
		}
		if item.PublishedAt.IsZero() {
			item.PublishedAt = parseAnyTime(plot.Episode.PublicationDate, plot.Episode.AirDate)
		}
		out = append(out, item)
	}
	return out, brandTitle, nil
}

func (c *Client) fetchEpisodes(ctx context.Context, brandID, limit int) ([]VideoItem, string, error) {
	var payload gqlEpisodesResponse
	if err := c.graphql(ctx, episodesQuery, map[string]any{
		"brandId": brandID,
		"first":   limit,
		"page":    1,
		"order":   "DESC",
	}, &payload); err != nil {
		return nil, "", err
	}
	var brandTitle string
	out := make([]VideoItem, 0, len(payload.EpisodesFilter.Data))
	for _, ep := range payload.EpisodesFilter.Data {
		video := ep.FullVideo
		publicID := video.PublicID
		if publicID <= 0 {
			publicID = int64(ep.ID)
		}
		if publicID <= 0 {
			continue
		}
		title := strings.TrimSpace(ep.Title)
		if title == "" {
			continue
		}
		if brandTitle == "" {
			brandTitle = ep.Brand.Title
		}
		item := VideoItem{
			PublicID:    publicID,
			InternalID:  int64(ep.ID),
			Title:       title,
			Description: ep.Description,
			BrandTitle:  ep.Brand.Title,
			BrandID:     int64(ep.Brand.ID),
			VideoType:   "episode",
			PublishedAt: parseAnyTime(ep.PublicationDate, ep.AirDate),
			Duration:    formatDuration(video.Duration, nil),
			Thumbnail:   firstPresetLink(ep.Images),
		}
		if item.Thumbnail == "" {
			item.Thumbnail = firstPresetLink(video.Images)
		}
		out = append(out, item)
	}
	return out, brandTitle, nil
}

func (c *Client) graphql(ctx context.Context, query string, variables map[string]any, out any) error {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.GraphQLURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("smotrim: create graphql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/graphql-response+json, application/json")
	req.Header.Set("Origin", "https://smotrim.ru")
	req.Header.Set("Referer", "https://smotrim.ru/")
	if ua := strings.TrimSpace(c.cfg.UserAgent); ua != "" {
		req.Header.Set("User-Agent", ua)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("smotrim: graphql request: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return fmt.Errorf("smotrim: read graphql body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("smotrim: graphql status %d", resp.StatusCode)
	}
	var envelope gqlEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("smotrim: decode graphql envelope: %w", err)
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("smotrim: graphql: %s", envelope.Errors[0].Message)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("smotrim: decode graphql data: %w", err)
	}
	return nil
}

type gqlEnvelope struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type gqlPlotsResponse struct {
	Brand struct {
		Title string `json:"title"`
		Plots struct {
			Data []struct {
				ID          int64  `json:"id"`
				Title       string `json:"title"`
				PublishedAt string `json:"publishedAt"`
				Episode     struct {
					Title           string `json:"title"`
					PublicationDate string `json:"publicationDate"`
					AirDate         string `json:"airDate"`
				} `json:"episode"`
				Images []gqlImage `json:"images"`
			} `json:"data"`
		} `json:"plotsPaginate"`
	} `json:"brand"`
}

type gqlEpisodesResponse struct {
	EpisodesFilter struct {
		Data []struct {
			ID              int    `json:"id"`
			Title           string `json:"title"`
			Description     string `json:"description"`
			AirDate         string `json:"airDate"`
			PublicationDate string `json:"publicationDate"`
			Brand           struct {
				ID    int    `json:"id"`
				Title string `json:"title"`
			} `json:"brand"`
			FullVideo struct {
				ID        int64      `json:"id"`
				PublicID  int64      `json:"publicId"`
				Duration  float64    `json:"duration"`
				VideoType string     `json:"videoType"`
				Images    []gqlImage `json:"images"`
			} `json:"fullVideo"`
			Images []gqlImage `json:"images"`
		} `json:"data"`
	} `json:"episodesFilter"`
}

type gqlImage struct {
	Presets []struct {
		Name string `json:"name"`
		Link string `json:"link"`
	} `json:"presets"`
}

func firstPresetLink(images []gqlImage) string {
	pref := []string{"Large", "hd", "prm", "xw", "mw", "lw", "b"}
	byName := make(map[string]string)
	var first string
	for _, img := range images {
		for _, p := range img.Presets {
			if first == "" && p.Link != "" {
				first = p.Link
			}
			if p.Name != "" && p.Link != "" {
				byName[p.Name] = p.Link
			}
		}
	}
	for _, name := range pref {
		if link := byName[name]; link != "" {
			return link
		}
	}
	return first
}

func mergeVideoItems(base, extra []VideoItem) []VideoItem {
	seen := make(map[int64]struct{}, len(base)+len(extra))
	out := make([]VideoItem, 0, len(base)+len(extra))
	for _, item := range base {
		if _, ok := seen[item.PublicID]; ok {
			continue
		}
		seen[item.PublicID] = struct{}{}
		out = append(out, item)
	}
	for _, item := range extra {
		if _, ok := seen[item.PublicID]; ok {
			continue
		}
		seen[item.PublicID] = struct{}{}
		out = append(out, item)
	}
	sortVideoItems(out)
	return out
}

func sortVideoItems(items []VideoItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].PublishedAt.Equal(items[j].PublishedAt) {
			return items[i].PublicID > items[j].PublicID
		}
		if items[i].PublishedAt.IsZero() {
			return false
		}
		if items[j].PublishedAt.IsZero() {
			return true
		}
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
}
