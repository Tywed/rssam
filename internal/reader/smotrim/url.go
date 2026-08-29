package smotrim

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const feedTypeSmotrim = "smotrim"

var brandPathRe = regexp.MustCompile(`^/brand/(\d+)(?:/|$)`)

// FeedOptions holds optional subscription parameters parsed from the feed URL.
type FeedOptions struct {
	BrandID   string
	Limit     int
	VideoType string
}

// ParseOptionsFromFeedURL extracts brand id and optional query params from supported URLs.
func ParseOptionsFromFeedURL(feedURL string) (FeedOptions, bool) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return FeedOptions{}, false
	}
	if opts, ok := parseSmotrimScheme(feedURL); ok {
		return opts, true
	}
	u, err := url.Parse(feedURL)
	if err != nil || u.Host == "" {
		return FeedOptions{}, false
	}
	host := strings.ToLower(u.Hostname())
	if host != "smotrim.ru" && host != "www.smotrim.ru" && !strings.HasSuffix(host, ".smotrim.ru") {
		return FeedOptions{}, false
	}
	m := brandPathRe.FindStringSubmatch(strings.ToLower(u.Path))
	if len(m) != 2 {
		return FeedOptions{}, false
	}
	opts := FeedOptions{BrandID: m[1]}
	applyQueryOptions(u.Query(), &opts)
	return opts, opts.BrandID != ""
}

func parseSmotrimScheme(feedURL string) (FeedOptions, bool) {
	for _, prefix := range []string{"smotrim://", "smotrim-brand://"} {
		if !strings.HasPrefix(strings.ToLower(feedURL), prefix) {
			continue
		}
		raw := strings.TrimSpace(feedURL[len(prefix):])
		if raw == "" {
			return FeedOptions{}, false
		}
		u, err := url.Parse(prefix + raw)
		if err != nil {
			return FeedOptions{}, false
		}
		id := strings.Trim(u.Host, "/")
		if id == "" {
			id = strings.Trim(strings.TrimPrefix(raw, "/"), "/")
		}
		opts := FeedOptions{BrandID: id}
		applyQueryOptions(u.Query(), &opts)
		return opts, id != ""
	}
	return FeedOptions{}, false
}

func applyQueryOptions(q url.Values, opts *FeedOptions) {
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			opts.Limit = clampLimit(n)
		}
	}
	if v := strings.TrimSpace(q.Get("type")); v != "" {
		opts.VideoType = normalizeVideoType(v)
	}
	if v := strings.TrimSpace(q.Get("video_type")); v != "" && opts.VideoType == "" {
		opts.VideoType = normalizeVideoType(v)
	}
}

func normalizeVideoType(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "plot", "сюжет", "story":
		return "plot"
	case "episode", "episodes", "выпуск":
		return "episode"
	default:
		return ""
	}
}

func clampLimit(n int) int {
	if n <= 0 {
		return defaultLimit
	}
	if n > maxLimit {
		return maxLimit
	}
	return n
}

// DetectFeedURL reports whether feedURL is a Smotrim brand subscription.
func DetectFeedURL(feedURL string) bool {
	_, ok := ParseOptionsFromFeedURL(feedURL)
	return ok
}

// BrandPageURL builds the public Smotrim brand page URL.
func BrandPageURL(baseURL, brandID string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultBrandURL
	}
	return baseURL + "/" + strings.Trim(brandID, "/")
}

// VideoPageURL builds a Smotrim video page URL.
func VideoPageURL(publicID int64) string {
	if publicID <= 0 {
		return defaultBrandURL
	}
	return "https://smotrim.ru/video/" + strconv.FormatInt(publicID, 10)
}

// ResolveOptions returns options from bridge override or feed URL.
func ResolveOptions(feedURL string, st FetchState) (FetchState, error) {
	if id := strings.TrimSpace(st.BrandID); id != "" {
		st.BrandID = id
	} else if opts, ok := ParseOptionsFromFeedURL(feedURL); ok {
		st.BrandID = opts.BrandID
		if st.Limit == 0 && opts.Limit > 0 {
			st.Limit = opts.Limit
		}
		if st.VideoType == "" && opts.VideoType != "" {
			st.VideoType = opts.VideoType
		}
	} else {
		return FetchState{}, errInvalidFeedURL
	}
	if st.Limit == 0 {
		if opts, ok := ParseOptionsFromFeedURL(feedURL); ok && opts.Limit > 0 {
			st.Limit = opts.Limit
		} else {
			st.Limit = defaultLimit
		}
	}
	st.Limit = clampLimit(st.Limit)
	st.VideoType = normalizeVideoType(st.VideoType)
	if strings.TrimSpace(st.BrandID) == "" {
		return FetchState{}, errMissingBrandID
	}
	return st, nil
}
