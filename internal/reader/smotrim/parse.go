package smotrim

import (
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var nuxtPayloadRe = regexp.MustCompile(`(?s)<script[^>]*type="application/json"[^>]*>(\[\[.*?\])\s*</script>`)

type VideoItem struct {
	PublicID    int64
	InternalID  int64
	Title       string
	Description string
	BrandTitle  string
	BrandID     int64
	EpisodeTitle string
	VideoType   string
	DateText    string
	PublishedAt time.Time
	Duration    string
	Thumbnail   string
}

type nuxtArray []json.RawMessage

func ExtractVideosFromBrandHTML(body []byte, now time.Time, videoType string, limit int) ([]VideoItem, string, error) {
	m := nuxtPayloadRe.FindSubmatch(body)
	if len(m) < 2 {
		return nil, "", fmt.Errorf("smotrim: nuxt payload not found")
	}
	var arr nuxtArray
	if err := json.Unmarshal(m[1], &arr); err != nil {
		return nil, "", fmt.Errorf("smotrim: decode nuxt payload: %w", err)
	}
	root := arr.resolve(1, map[int]struct{}{})
	brandTitle := findBrandTitle(root)
	items := collectVideoItems(root, now)
	items = filterVideoType(items, videoType)
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
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	if len(items) == 0 {
		return nil, brandTitle, errEmptyResults
	}
	if brandTitle == "" {
		brandTitle = items[0].BrandTitle
	}
	return items, brandTitle, nil
}

func (a nuxtArray) resolve(idx int, seen map[int]struct{}) any {
	return a.valueAt(idx, seen)
}

func (a nuxtArray) valueAt(idx int, seen map[int]struct{}) any {
	if idx < 0 || idx >= len(a) {
		return nil
	}
	if _, ok := seen[idx]; ok {
		return nil
	}
	seen[idx] = struct{}{}
	defer delete(seen, idx)

	raw := strings.TrimSpace(string(a[idx]))
	if raw == "" || raw == "null" {
		return nil
	}
	switch raw[0] {
	case '{', '[':
		var v any
		if err := json.Unmarshal(a[idx], &v); err != nil {
			return nil
		}
		return a.expand(v, seen)
	default:
		var v any
		if err := json.Unmarshal(a[idx], &v); err != nil {
			return nil
		}
		return v
	}
}

func (a nuxtArray) expand(v any, seen map[int]struct{}) any {
	switch x := v.(type) {
	case float64:
		if x == float64(int(x)) {
			return a.valueAt(int(x), seen)
		}
		return x
	case []any:
		if len(x) == 2 {
			if s, ok := x[0].(string); ok && isNuxtWrapper(s) {
				if f, ok := x[1].(float64); ok {
					return a.valueAt(int(f), seen)
				}
			}
		}
		out := make([]any, len(x))
		for i, el := range x {
			out[i] = a.expand(el, seen)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, el := range x {
			out[k] = a.expand(el, seen)
		}
		return out
	default:
		return v
	}
}

func isNuxtWrapper(s string) bool {
	switch s {
	case "ShallowReactive", "Reactive", "Ref", "EmptyRef", "Set", "Map":
		return true
	default:
		return false
	}
}

func collectVideoItems(root any, now time.Time) []VideoItem {
	byPublicID := make(map[int64]VideoItem)
	walkAny(root, func(m map[string]any) {
		publicID := int64FromAny(m["publicId"])
		if publicID <= 0 {
			return
		}
		title := stringFromAny(m["title"])
		if title == "" {
			return
		}
		item := VideoItem{
			PublicID:    publicID,
			InternalID:  int64FromAny(m["id"]),
			Title:       title,
			Description: stringFromAny(m["description"]),
			DateText:    stringFromAny(m["date"]),
			VideoType:   normalizeVideoType(stringFromAny(m["videoType"])),
			Duration:    formatDuration(m["duration"], m["durationSeconds"]),
			Thumbnail:   firstImageLink(m["images"]),
		}
		if item.VideoType == "" {
			if t := stringFromAny(m["type"]); t != "" {
				item.VideoType = normalizeVideoType(t)
			}
		}
		if item.VideoType == "" {
			item.VideoType = "episode"
		}
		if ep, ok := m["episode"].(map[string]any); ok {
			item.EpisodeTitle = stringFromAny(ep["title"])
			if item.PublishedAt.IsZero() {
				item.PublishedAt = parseAnyTime(ep["publicationDate"], ep["airDate"], now)
			}
		}
		if brand, ok := m["brand"].(map[string]any); ok {
			item.BrandTitle = stringFromAny(brand["title"])
			item.BrandID = int64FromAny(brand["id"])
		}
		if item.PublishedAt.IsZero() {
			item.PublishedAt = ParsePublishedTime(item.DateText, now)
		}
		if item.PublishedAt.IsZero() {
			item.PublishedAt = ParsePublishedTime(stringFromAny(m["publishedAt"]), now)
		}
		if prev, ok := byPublicID[publicID]; !ok || len(item.Description) > len(prev.Description) {
			byPublicID[publicID] = item
		}
	})
	out := make([]VideoItem, 0, len(byPublicID))
	for _, item := range byPublicID {
		out = append(out, item)
	}
	return out
}

func filterVideoType(items []VideoItem, videoType string) []VideoItem {
	videoType = normalizeVideoType(videoType)
	if videoType == "" {
		return items
	}
	out := make([]VideoItem, 0, len(items))
	for _, item := range items {
		if item.VideoType == videoType {
			out = append(out, item)
		}
	}
	return out
}

func findBrandTitle(root any) string {
	var title string
	walkAny(root, func(m map[string]any) {
		if title != "" {
			return
		}
		if stringFromAny(m["h1"]) != "" {
			return
		}
		if t := stringFromAny(m["title"]); t != "" {
			if _, ok := m["originalTitle"]; ok {
				title = t
			}
		}
	})
	if title == "" {
		walkAny(root, func(m map[string]any) {
			if title != "" {
				return
			}
			if id := int64FromAny(m["id"]); id <= 0 {
				return
			}
			if t := stringFromAny(m["title"]); t != "" && stringFromAny(m["description"]) != "" {
				if _, ok := m["publishedEpisodesCount"]; ok {
					title = t
				}
			}
		})
	}
	return title
}

func walkAny(v any, fn func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		fn(x)
		for _, child := range x {
			walkAny(child, fn)
		}
	case []any:
		for _, child := range x {
			walkAny(child, fn)
		}
	}
}

func firstImageLink(v any) string {
	items, ok := v.([]any)
	if !ok {
		return ""
	}
	for _, it := range items {
		m, ok := it.(map[string]any)
		if !ok {
			continue
		}
		presets, ok := m["presets"].([]any)
		if !ok {
			continue
		}
		for _, p := range presets {
			pm, ok := p.(map[string]any)
			if !ok {
				continue
			}
			if link := stringFromAny(pm["link"]); link != "" {
				return link
			}
		}
	}
	return ""
}

func formatDuration(raw any, seconds any) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		if v <= 0 {
			break
		}
		sec := int(v)
		if sec >= 3600 {
			return fmt.Sprintf("%d ч %02d мин", sec/3600, (sec%3600)/60)
		}
		if sec >= 60 {
			return fmt.Sprintf("%d мин", sec/60)
		}
		return fmt.Sprintf("%d сек", sec)
	}
	if sec := int64FromAny(seconds); sec > 0 {
		return formatDuration(float64(sec), nil)
	}
	return ""
}

func parseAnyTime(values ...any) time.Time {
	for _, v := range values {
		if s := stringFromAny(v); s != "" {
			if t := ParsePublishedTime(s, time.Now()); !t.IsZero() {
				return t
			}
		}
	}
	return time.Time{}
}

func stringFromAny(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case json.Number:
		return x.String()
	default:
		return ""
	}
}

func int64FromAny(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case json.Number:
		n, _ := x.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		return n
	default:
		return 0
	}
}

func BuildContentHTML(item VideoItem) string {
	var b strings.Builder
	if u := strings.TrimSpace(item.Thumbnail); u != "" {
		b.WriteString(`<p><img src="`)
		b.WriteString(html.EscapeString(u))
		b.WriteString(`" alt=""></p>`)
	}
	if desc := strings.TrimSpace(item.Description); desc != "" && desc != item.Title {
		b.WriteString("<p>")
		b.WriteString(html.EscapeString(desc))
		b.WriteString("</p>")
	}
	if ep := strings.TrimSpace(item.EpisodeTitle); ep != "" {
		b.WriteString("<p><strong>Выпуск:</strong> ")
		b.WriteString(html.EscapeString(ep))
		b.WriteString("</p>")
	}
	if d := strings.TrimSpace(item.Duration); d != "" {
		b.WriteString("<p><strong>Длительность:</strong> ")
		b.WriteString(html.EscapeString(d))
		b.WriteString("</p>")
	}
	url := VideoPageURL(item.PublicID)
	b.WriteString(`<p><a href="`)
	b.WriteString(html.EscapeString(url))
	b.WriteString(`" rel="noopener noreferrer" target="_blank">Смотреть на СМОТРИМ ↗</a></p>`)
	return b.String()
}
