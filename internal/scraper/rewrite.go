package scraper

import (
	"regexp"
	"strings"
)

// ApplyRewriteRules applies regexp replacements to text.
// Format (Miniflux-style): "pattern1\nreplacement1\npattern2\nreplacement2".
func ApplyRewriteRules(text, rules string) string {
	rules = strings.TrimSpace(rules)
	if rules == "" {
		return text
	}
	lines := strings.Split(rules, "\n")
	if len(lines)%2 != 0 {
		return text
	}

	type rewriteRule struct {
		pattern     *regexp.Regexp
		replacement string
	}
	compiled := make([]rewriteRule, 0, len(lines)/2)
	for i := 0; i < len(lines)-1; i += 2 {
		pattern := strings.TrimSpace(lines[i])
		replacement := lines[i+1]
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		compiled = append(compiled, rewriteRule{pattern: re, replacement: replacement})
	}

	out := text
	for _, rule := range compiled {
		out = rule.pattern.ReplaceAllString(out, rule.replacement)
	}
	return out
}
