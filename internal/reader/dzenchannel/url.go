package dzenchannel

import (
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const feedType = "dzen_channel"

// Content types of the export API; the channel page shows the same three tabs.
const (
	TypeArticle    = "article"
	TypeLongVideo  = "long_video"
	TypeShortVideo = "short_video"
)

var allTypes = []string{TypeArticle, TypeLongVideo, TypeShortVideo}

// Channel identifies a Dzen channel: by public name (dzen.ru/tass) or, for
// channels without one, by the 24-hex id (dzen.ru/id/5f9abb2e…). Types is
// the subset of content types to poll; empty means every tab the channel has.
type Channel struct {
	Name  string
	ID    string
	Types []string
}

var (
	nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,99}$`)
	hexRe  = regexp.MustCompile(`^[0-9a-f]{24}$`)
)

// reservedPaths are first segments of dzen.ru URLs that are not channels.
var reservedPaths = map[string]struct{}{
	"a": {}, "b": {}, "api": {}, "auth": {}, "embed": {}, "help": {}, "id": {}, "media": {}, "news": {},
	"profile": {}, "search": {}, "shorts": {}, "sport": {}, "suite": {}, "support": {}, "t": {}, "user": {},
	"video": {}, "pogoda": {}, "weather": {}, "cards": {}, "edit": {}, "login": {}, "settings": {}, "studio": {},
}

// Parse extracts the channel from a subscription address:
//
//	https://dzen.ru/tass            https://dzen.ru/id/5f9abb2e66afb7042d59a0af
//	dzen-channel://tass             dzen-channel://id/5f9abb2e66afb7042d59a0af
//
// with an optional ?types=article,long_video,short_video on either form.
// zen.yandex.ru is accepted as the old host.
func Parse(feedURL string) (Channel, bool) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return Channel{}, false
	}
	u, err := url.Parse(feedURL)
	if err != nil || u.Host == "" {
		return Channel{}, false
	}
	var path string
	switch strings.ToLower(u.Scheme) {
	case "dzen-channel":
		path = u.Host + u.Path
	case "http", "https":
		switch strings.ToLower(u.Hostname()) {
		case "dzen.ru", "www.dzen.ru", "zen.yandex.ru", "www.zen.yandex.ru":
		default:
			return Channel{}, false
		}
		path = u.Path
	default:
		return Channel{}, false
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	ch := Channel{}
	switch {
	case len(segs) == 2 && segs[0] == "id" && hexRe.MatchString(segs[1]):
		ch.ID = segs[1]
	case len(segs) == 1 && nameRe.MatchString(segs[0]):
		if _, reserved := reservedPaths[strings.ToLower(segs[0])]; reserved {
			return Channel{}, false
		}
		ch.Name = segs[0]
	default:
		return Channel{}, false
	}
	types, ok := parseTypes(u.Query().Get("types"))
	if !ok {
		return Channel{}, false
	}
	ch.Types = types
	return ch, true
}

func parseTypes(raw string) ([]string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true
	}
	seen := map[string]bool{}
	for _, t := range strings.Split(raw, ",") {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" {
			continue
		}
		known := false
		for _, k := range allTypes {
			if k == t {
				known = true
			}
		}
		if !known {
			return nil, false
		}
		seen[t] = true
	}
	if len(seen) == 0 {
		return nil, true
	}
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out, true
}

// Detect reports whether feedURL is a Dzen channel subscription.
func Detect(feedURL string) bool {
	_, ok := Parse(feedURL)
	return ok
}

// String is the human-readable channel key: the name, or id/<hex>.
func (c Channel) String() string {
	if c.Name != "" {
		return c.Name
	}
	return "id/" + c.ID
}

// PageURL is the public channel page.
func (c Channel) PageURL() string {
	return "https://dzen.ru/" + c.String()
}

// queryParam is the export API parameter that selects the channel.
func (c Channel) queryParam() (key, value string) {
	if c.Name != "" {
		return "channel_name", c.Name
	}
	return "channel_id", c.ID
}
