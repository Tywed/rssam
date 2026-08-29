package httpserver

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"rssam/internal/storage"
)

const maxFeedIconBytes = 512 * 1024

type feedIconDTO struct {
	Icon        string `json:"icon"`
	ContentType string `json:"content_type"`
}

func (s *Server) handleFeedIcon(w http.ResponseWriter, r *http.Request) {
	p, ok := requireUser(w, r)
	if !ok || s.feeds == nil {
		if ok {
			writeError(w, http.StatusServiceUnavailable, "feed storage is not configured")
		}
		return
	}
	feedID, err := parsePathInt64(r, "feedID")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	ctx := r.Context()
	feed, err := s.feeds.GetFeed(ctx, p.UserID, feedID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, http.StatusNotFound, "feed not found")
			return
		}
		s.log.Error("get feed icon failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}

	if len(feed.IconData) > 0 {
		writeJSON(w, http.StatusOK, listResponse[feedIconDTO]{
			Data: feedIconDTO{
				Icon:        base64.StdEncoding.EncodeToString(feed.IconData),
				ContentType: iconContentType(feed.IconData),
			},
			Total: 1,
		})
		return
	}

	iconURL := strings.TrimSpace(feed.IconURL)
	if iconURL == "" {
		iconURL = defaultFaviconURL(feed.FeedURL)
	}
	if iconURL == "" {
		writeError(w, http.StatusNotFound, "icon not available")
		return
	}

	data, contentType, fetchErr := s.fetchIcon(ctx, iconURL)
	if fetchErr != nil {
		s.log.Warn("fetch feed icon failed", "feed_id", feedID, "icon_url", iconURL, "err", fetchErr)
		writeError(w, http.StatusBadGateway, "icon fetch failed")
		return
	}

	if err := s.feeds.UpdateFeedIcon(ctx, p.UserID, feedID, iconURL, data); err != nil {
		s.log.Warn("cache feed icon failed", "feed_id", feedID, "err", err)
	}

	writeJSON(w, http.StatusOK, listResponse[feedIconDTO]{
		Data: feedIconDTO{
			Icon:        base64.StdEncoding.EncodeToString(data),
			ContentType: contentType,
		},
		Total: 1,
	})
}

func (s *Server) fetchIcon(ctx context.Context, iconURL string) ([]byte, string, error) {
	if s.ssrfGuard != nil {
		if err := s.ssrfGuard.ValidateURL(iconURL); err != nil {
			return nil, "", err
		}
	}

	client := &http.Client{Timeout: 10 * time.Second}
	if s.ssrfGuard != nil {
		client = s.ssrfGuard.HTTPClient(10 * time.Second)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, iconURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "image/*,*/*;q=0.8")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedIconBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(body) > maxFeedIconBytes {
		return nil, "", fmt.Errorf("icon too large")
	}
	if len(body) == 0 {
		return nil, "", fmt.Errorf("empty icon body")
	}

	ct := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if ct == "" {
		ct = iconContentType(body)
	}
	return body, ct, nil
}

func defaultFaviconURL(feedURL string) string {
	u, err := url.Parse(strings.TrimSpace(feedURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	u.Path = "/favicon.ico"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func iconContentType(data []byte) string {
	if len(data) >= 4 && data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G' {
		return "image/png"
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg"
	}
	if len(data) >= 4 && string(data[:4]) == "<svg" {
		return "image/svg+xml"
	}
	return "image/x-icon"
}
