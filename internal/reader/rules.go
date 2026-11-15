package reader

import (
	"rssam/internal/bridge/v1"
	"rssam/internal/feedrules"
)

// ApplyFeedRules filters entries using blocked_rules and keep_rules (regexp per line).
func ApplyFeedRules(entries []bridge.Entry, blockedRules, keepRules string) []bridge.Entry {
	r := feedrules.Cached(blockedRules, keepRules, "")
	if r == nil || (r.Blocked == nil && r.Keep == nil) {
		return entries
	}

	filtered := make([]bridge.Entry, 0, len(entries))
	for _, entry := range entries {
		text := entry.Title + " " + entry.Content + " " + entry.URL
		if entry.Author != nil {
			text += " " + *entry.Author
		}
		if r.Blocked != nil && r.Blocked.MatchString(text) {
			continue
		}
		if r.Keep != nil && !r.Keep.MatchString(text) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// ApplyURLRewriteRules rewrites entry URLs using pattern/replacement pairs.
// Format: "pattern1\nreplacement1\npattern2\nreplacement2".
func ApplyURLRewriteRules(entries []bridge.Entry, rules string) []bridge.Entry {
	r := feedrules.Cached("", "", rules)
	if r == nil || len(r.Rewrites) == 0 {
		return entries
	}
	out := make([]bridge.Entry, len(entries))
	copy(out, entries)
	for i := range out {
		out[i].URL = r.Apply(out[i].URL)
	}
	return out
}
