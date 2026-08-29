package rutube

import (
	"context"
	"fmt"
	"strings"
	"time"

	"rssam/internal/model"
	"rssam/internal/storage"
)

// Handler fetches videos for a Rutube person (channel) via public API.
type Handler struct {
	client *Client
	cfg    Config
}

func NewHandler(client *Client, cfg Config) *Handler {
	return &Handler{client: client, cfg: cfg.withDefaults()}
}

// UpdateConfig replaces global Rutube bridge settings at runtime.
func (h *Handler) UpdateConfig(cfg Config) {
	cfg = cfg.withDefaults()
	h.cfg = cfg
	if h.client != nil {
		h.client.UpdateConfig(cfg)
	}
}

func (h *Handler) Name() string { return feedTypeRutube }

func (h *Handler) DetectFeedType(feedURL string) string {
	if DetectFeedURL(feedURL) {
		return feedTypeRutube
	}
	return ""
}

// FetchState is per-feed bridge state for Rutube.
type FetchState struct {
	ChannelID string
}

// FetchResult is the outcome of a Rutube poll.
type FetchResult struct {
	Entries   []storage.CreateEntryParams
	State     FetchState
	FeedTitle string
	FeedURI   string
}

func (h *Handler) Fetch(ctx context.Context, feedURL string, st FetchState) (FetchResult, error) {
	if h == nil || h.client == nil {
		return FetchResult{}, fmt.Errorf("rutube: handler is not configured")
	}

	channelID, err := ResolveChannelID(feedURL, st.ChannelID)
	if err != nil {
		return FetchResult{}, err
	}
	st.ChannelID = channelID

	body, err := h.client.ListPersonVideos(ctx, channelID)
	if err != nil {
		return FetchResult{State: st}, err
	}

	feedAuthor := ""
	entries := make([]storage.CreateEntryParams, 0, len(body.Results))
	for _, v := range body.Results {
		entry, author := entryFromVideo(v)
		entries = append(entries, entry)
		if feedAuthor == "" && strings.TrimSpace(author) != "" {
			feedAuthor = strings.TrimSpace(author)
		}
	}

	return FetchResult{
		Entries:   entries,
		State:     st,
		FeedTitle: FeedTitle(feedAuthor),
		FeedURI:   PersonPageURL(channelID),
	}, nil
}

func entryFromVideo(v personVideo) (storage.CreateEntryParams, string) {
	title := strings.TrimSpace(v.Title)
	if title == "" {
		title = "Video"
	}

	entryURL := strings.TrimSpace(v.VideoURL)
	if entryURL == "" && strings.TrimSpace(v.ID) != "" {
		entryURL = VideoURL(v.ID)
	}
	entryURL = model.NormalizeURL(entryURL)

	author := strings.TrimSpace(v.Author.Name)
	var authorPtr *string
	if author != "" {
		authorPtr = &author
	}

	var pub *time.Time
	if ts := v.PublicationTS.Unix(); ts > 0 {
		t := time.Unix(ts, 0).UTC()
		pub = &t
	} else if ts := v.CreatedTS.Unix(); ts > 0 {
		t := time.Unix(ts, 0).UTC()
		pub = &t
	}

	hash := DedupHash(v.ID, entryURL)

	return storage.CreateEntryParams{
		Title:       title,
		URL:         entryURL,
		Content:     BuildContentHTML(v.ThumbnailURL, v.Description),
		Author:      authorPtr,
		PublishedAt: pub,
		Hash:        hash,
		Status:      storage.EntryStatusUnread,
	}, author
}

// DedupHash returns sha256(video_id) or sha256(normalized uri).
func DedupHash(videoID, entryURL string) string {
	if id := strings.TrimSpace(videoID); id != "" {
		return model.DedupHashFromString(id)
	}
	return model.DedupHashFromURL(entryURL)
}
