package vk

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const feedTypeVKGroup = "vk_group"

// Wall is one VK wall: a numeric owner (negative for communities) or a
// screen name that the API resolves itself via the domain parameter.
type Wall struct {
	OwnerID int
	Domain  string
}

func (w Wall) String() string {
	if w.Domain != "" {
		return w.Domain
	}
	return strconv.Itoa(w.OwnerID)
}

var (
	wallPrefixed = regexp.MustCompile(`^(public|club|event|id)(\d+)$`)
	wallDomain   = regexp.MustCompile(`^[a-z][a-z0-9_.]{2,31}$`)
)

// vkServicePaths are first path segments of vk.com that are never a wall.
var vkServicePaths = map[string]struct{}{
	"feed": {}, "im": {}, "video": {}, "audio": {}, "market": {}, "docs": {}, "friends": {},
	"groups": {}, "settings": {}, "search": {}, "wall": {}, "photos": {}, "blog": {}, "dev": {},
	"apps": {}, "mail": {}, "support": {}, "about": {}, "terms": {}, "privacy": {}, "login": {},
	"away": {}, "topic": {}, "album": {}, "photo": {}, "clips": {}, "music": {}, "games": {},
	"stickers": {}, "calls": {}, "donut": {}, "services": {}, "ads": {}, "business": {},
	"legal": {}, "press": {}, "jobs": {}, "mini_apps": {}, "app": {}, "bugs": {}, "faq": {},
	"notifications": {}, "bookmarks": {}, "stories": {}, "podcasts": {}, "articles": {},
}

// ParseWallFromFeedURL accepts https://vk.com/<name>, /public123, /club123,
// /event123, /id123 and vk-group://<name>; posts, search and service pages
// are rejected so that vk_search and plain links keep their own detection.
func ParseWallFromFeedURL(feedURL string) (Wall, bool) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return Wall{}, false
	}
	lower := strings.ToLower(feedURL)
	if strings.HasPrefix(lower, "vk-group://") {
		return parseWallSegment(strings.Trim(feedURL[len("vk-group://"):], "/ "))
	}
	u, err := url.Parse(feedURL)
	if err != nil {
		return Wall{}, false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return Wall{}, false
	}
	switch strings.ToLower(u.Hostname()) {
	case "vk.com", "www.vk.com", "m.vk.com", "vk.ru", "www.vk.ru", "m.vk.ru":
	default:
		return Wall{}, false
	}
	if u.RawQuery != "" {
		return Wall{}, false
	}
	path := strings.Trim(u.Path, "/")
	if path == "" || strings.Contains(path, "/") {
		return Wall{}, false
	}
	return parseWallSegment(path)
}

func parseWallSegment(seg string) (Wall, bool) {
	seg = strings.ToLower(strings.TrimSpace(seg))
	if seg == "" {
		return Wall{}, false
	}
	if _, service := vkServicePaths[seg]; service {
		return Wall{}, false
	}
	if m := wallPrefixed.FindStringSubmatch(seg); m != nil {
		n, err := strconv.Atoi(m[2])
		if err != nil || n <= 0 {
			return Wall{}, false
		}
		if m[1] == "id" {
			return Wall{OwnerID: n}, true
		}
		return Wall{OwnerID: -n}, true
	}
	if strings.HasPrefix(seg, "wall") {
		return Wall{}, false
	}
	if !wallDomain.MatchString(seg) {
		return Wall{}, false
	}
	return Wall{Domain: seg}, true
}

// DetectGroupFeedURL reports whether feedURL is a VK wall subscription.
func DetectGroupFeedURL(feedURL string) bool {
	_, ok := ParseWallFromFeedURL(feedURL)
	return ok
}
