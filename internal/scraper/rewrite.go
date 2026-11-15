package scraper

import "rssam/internal/feedrules"

// ApplyRewriteRules applies regexp replacements to text.
// Format (Miniflux-style): "pattern1\nreplacement1\npattern2\nreplacement2".
func ApplyRewriteRules(text, rules string) string {
	return feedrules.Cached("", "", rules).Apply(text)
}
