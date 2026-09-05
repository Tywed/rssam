package ui

import (
	"context"
	"encoding/json"
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
	Error         string `json:"error,omitempty"`
	TLSCertError  bool   `json:"tls_cert_error,omitempty"`
	ChannelID     string `json:"channel_id,omitempty"`
	SearchQuery   string `json:"search_query,omitempty"`
	BrandID       string `json:"brand_id,omitempty"`
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
	if h.cfg.ResolveFeedTitle != nil {
		switch ft {
		case reader.FeedTypeTelegram, reader.FeedTypeRSS:
			title, err := h.cfg.ResolveFeedTitle(r.Context(), feedURL, ft, tlsInsecure)
			if err != nil {
				if ft == reader.FeedTypeRSS {
					resp.Valid = false
					resp.Error = formatRSSDetectError(err)
					if reader.IsTLSCertError(err) && !tlsInsecure {
						resp.TLSCertError = true
					}
				}
			} else if strings.TrimSpace(title) != "" {
				resp.Title = strings.TrimSpace(title)
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

func validateFeedBeforeCreate(ctx context.Context, h *Handler, feedURL, feedType string, tlsInsecure bool) error {
	if err := reader.ValidateBridgeFeedURL(feedURL, feedType); err != nil {
		return err
	}
	if feedType == reader.FeedTypeRSS && h.cfg.ResolveFeedTitle != nil {
		if _, err := h.cfg.ResolveFeedTitle(ctx, feedURL, feedType, tlsInsecure); err != nil {
			return fmt.Errorf("%s", formatRSSDetectError(err))
		}
	}
	return nil
}
