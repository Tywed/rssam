package okgroup

import (
	"net/url"
	"regexp"
	"strings"
)

const feedType = "ok_group"

// Group identifies an Odnoklassniki group by its numeric id
// (ok.ru/group/58904363073576) or public slug (ok.ru/rianovosti).
type Group struct {
	ID   string
	Slug string
}

var (
	idRe   = regexp.MustCompile(`^[0-9]{6,20}$`)
	slugRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{1,99}$`)
)

// reservedPaths are first segments of ok.ru URLs that are never a group slug.
var reservedPaths = map[string]struct{}{
	"profile": {}, "group": {}, "groups": {}, "video": {}, "live": {}, "music": {}, "games": {}, "game": {},
	"feed": {}, "discussions": {}, "messages": {}, "settings": {}, "search": {}, "dk": {}, "topic": {},
	"app": {}, "apps": {}, "help": {}, "about": {}, "regulations": {}, "login": {}, "signup": {}, "privacy": {},
	"hobby": {}, "moments": {}, "gifts": {}, "payment": {}, "mall": {}, "notifications": {}, "friends": {},
	"photos": {}, "album": {}, "guests": {}, "holidays": {}, "events": {}, "news": {}, "statuses": {},
}

// Parse extracts the group from a subscription address:
//
//	https://ok.ru/group/58904363073576    https://ok.ru/rianovosti
//	ok-group://58904363073576             ok-group://rianovosti
//
// A profile or topic link is not a group.
func Parse(feedURL string) (Group, bool) {
	feedURL = strings.TrimSpace(feedURL)
	if feedURL == "" {
		return Group{}, false
	}
	u, err := url.Parse(feedURL)
	if err != nil || u.Host == "" {
		return Group{}, false
	}
	var path string
	switch strings.ToLower(u.Scheme) {
	case "ok-group":
		path = u.Host + u.Path
	case "http", "https":
		switch strings.ToLower(u.Hostname()) {
		case "ok.ru", "www.ok.ru", "m.ok.ru", "odnoklassniki.ru", "www.odnoklassniki.ru":
		default:
			return Group{}, false
		}
		path = u.Path
	default:
		return Group{}, false
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case len(segs) == 2 && segs[0] == "group" && idRe.MatchString(segs[1]):
		return Group{ID: segs[1]}, true
	case len(segs) == 1 && idRe.MatchString(segs[0]):
		return Group{ID: segs[0]}, true
	case len(segs) == 1 && slugRe.MatchString(segs[0]):
		if _, reserved := reservedPaths[strings.ToLower(segs[0])]; reserved {
			return Group{}, false
		}
		return Group{Slug: segs[0]}, true
	}
	return Group{}, false
}

// Detect reports whether feedURL is an OK group subscription.
func Detect(feedURL string) bool {
	_, ok := Parse(feedURL)
	return ok
}

// String is the human-readable key: the slug or the numeric id.
func (g Group) String() string {
	if g.Slug != "" {
		return g.Slug
	}
	return g.ID
}

// PageURL is the public group page.
func (g Group) PageURL() string {
	if g.Slug != "" {
		return "https://ok.ru/" + g.Slug
	}
	return "https://ok.ru/group/" + g.ID
}

// TopicURL is the canonical link of one post of group gid.
func TopicURL(gid, topicID string) string {
	return "https://ok.ru/group/" + gid + "/topic/" + topicID
}
