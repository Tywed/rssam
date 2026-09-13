package ui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"rssam/internal/reader"
	dzenbridge "rssam/internal/reader/dzen"
	maxstatbridge "rssam/internal/reader/maxstat"
	"rssam/internal/reader/rutube"
	smotrimbridge "rssam/internal/reader/smotrim"
	vkbridge "rssam/internal/reader/vk"
	"rssam/internal/storage"
)

type feedDetectResponse struct {
	FeedType      string `json:"feed_type"`
	FeedTypeLabel string `json:"feed_type_label"`
	Valid         bool   `json:"valid"`
	Title         string `json:"title,omitempty"`
	// FeedURL is the address that actually serves the feed when it differs
	// from the typed one (site page with a feed link, permanent redirect).
	FeedURL      string `json:"feed_url,omitempty"`
	Error        string `json:"error,omitempty"`
	TLSCertError bool   `json:"tls_cert_error,omitempty"`
	ChannelID    string `json:"channel_id,omitempty"`
	SearchQuery  string `json:"search_query,omitempty"`
	BrandID      string `json:"brand_id,omitempty"`
}

func (h *Handler) handleFeedDetectType(w http.ResponseWriter, r *http.Request) {
	feedURL := strings.TrimSpace(r.URL.Query().Get("url"))
	if feedURL == "" {
		writeJSON(w, http.StatusBadRequest, feedDetectResponse{Error: "url обязателен"})
		return
	}
	tlsInsecure := queryBool(r, "tls_insecure")
	if err := reader.ValidateFeedURL(feedURL, h.cfg.SSRFGuard); err != nil {
		writeJSON(w, http.StatusOK, feedDetectResponse{Valid: false, Error: err.Error()})
		return
	}

	ft := reader.DetectFeedTypeFromURL(feedURL)
	resp := feedDetectResponse{
		FeedType:      ft,
		FeedTypeLabel: reader.FeedTypeLabel(ft),
		Valid:         true,
	}
	if err := reader.ValidateBridgeFeedURL(feedURL, ft); err != nil {
		resp.Valid = false
		resp.Error = err.Error()
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if ft == reader.FeedTypeRutube {
		if id, ok := rutube.ParseChannelIDFromFeedURL(feedURL); ok {
			resp.ChannelID = id
		}
	}
	if ft == reader.FeedTypeVKSearch {
		if q, ok := vkbridge.ParseQueryFromFeedURL(feedURL); ok {
			resp.SearchQuery = q
			if resp.Title == "" {
				resp.Title = "VK: " + q
			}
		}
	}
	if ft == reader.FeedTypeMaxstat {
		if q, ok := maxstatbridge.ParseQueryFromFeedURL(feedURL); ok {
			resp.SearchQuery = q
			if resp.Title == "" {
				resp.Title = maxstatbridge.FeedTitle(q)
			}
		}
	}
	if ft == reader.FeedTypeDzenNews {
		if q, ok := dzenbridge.ParseQueryFromFeedURL(feedURL); ok {
			resp.SearchQuery = q
			if resp.Title == "" {
				resp.Title = "Dzen: " + q
			}
		}
	}
	if ft == reader.FeedTypeSmotrim {
		if opts, ok := smotrimbridge.ParseOptionsFromFeedURL(feedURL); ok {
			resp.BrandID = opts.BrandID
			if resp.Title == "" {
				resp.Title = "Smotrim: brand " + opts.BrandID
			}
		}
	}
	if h.cfg.DiscoverFeed != nil {
		switch ft {
		case reader.FeedTypeTelegram, reader.FeedTypeRSS:
			d, err := h.cfg.DiscoverFeed(r.Context(), feedURL, ft, tlsInsecure)
			if err != nil {
				if ft == reader.FeedTypeRSS {
					resp.Valid = false
					resp.Error = formatRSSDetectError(err)
					if reader.IsTLSCertError(err) && !tlsInsecure {
						resp.TLSCertError = true
					}
				}
			} else {
				if strings.TrimSpace(d.Title) != "" {
					resp.Title = strings.TrimSpace(d.Title)
				}
				if d.FeedURL != "" && d.FeedURL != feedURL {
					resp.FeedURL = d.FeedURL
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func queryBool(r *http.Request, key string) bool {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

func formatRSSDetectError(err error) string {
	if err == nil {
		return ""
	}
	if reader.IsTLSCertError(err) {
		return reader.TLSCertErrorMessage()
	}
	msg := err.Error()
	if errors.Is(err, reader.ErrNoFeedFound) {
		return "На странице не найдено ссылок на RSS/Atom ленту; укажите адрес ленты вручную"
	}
	if strings.Contains(msg, "Failed to detect feed type") || strings.Contains(msg, "parse feed:") {
		return "Не удалось распознать RSS/Atom ленту по этому URL"
	}
	return "Не удалось проверить ленту: " + msg
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (h *Handler) renderFeedFormError(w http.ResponseWriter, r *http.Request, params storage.CreateFeedParams, errMsg string) {
	data := h.baseData(r, "settings")
	data.SettingsSection = "feeds"
	data.Feed = storage.Feed{
		FeedURL:         params.FeedURL,
		Title:           params.Title,
		CategoryID:      params.CategoryID,
		IntervalMinutes: params.IntervalMinutes,
		FeedType:        reader.DetectFeedTypeFromURL(params.FeedURL),
		TLSInsecure:     params.TLSInsecure,
	}
	data.TgUseProxy = r.FormValue("telegram_use_proxy") == "1"
	data.TgProxyServiceURL = strings.TrimSpace(r.FormValue("telegram_proxy_service_url"))
	data.TgStaticProxy = strings.TrimSpace(r.FormValue("telegram_static_proxy"))
	data.FlashErr = errMsg
	data.Title = "Новая лента"
	h.render(w, r, "feeds_form", data)
}

// resolveFeedBeforeCreate validates the URL and, for RSS, fetches it once:
// the same request checks that a feed is reachable, follows the page/redirect
// to the real feed address and yields a title for an empty title field.
func resolveFeedBeforeCreate(ctx context.Context, h *Handler, params *storage.CreateFeedParams) error {
	if err := reader.ValidateBridgeFeedURL(params.FeedURL, params.FeedType); err != nil {
		return err
	}
	if h.cfg.DiscoverFeed == nil {
		return nil
	}
	switch params.FeedType {
	case reader.FeedTypeRSS:
		d, err := h.cfg.DiscoverFeed(ctx, params.FeedURL, params.FeedType, params.TLSInsecure)
		if err != nil {
			return fmt.Errorf("%s", formatRSSDetectError(err))
		}
		if d.FeedURL != "" && d.FeedURL != params.FeedURL {
			if err := reader.ValidateFeedURL(d.FeedURL, h.cfg.SSRFGuard); err != nil {
				return err
			}
			params.FeedURL = d.FeedURL
		}
		if params.Title == "" {
			params.Title = strings.TrimSpace(d.Title)
		}
	case reader.FeedTypeTelegram:
		if params.Title == "" {
			if d, err := h.cfg.DiscoverFeed(ctx, params.FeedURL, params.FeedType, params.TLSInsecure); err == nil {
				params.Title = strings.TrimSpace(d.Title)
			}
		}
	}
	return nil
}
