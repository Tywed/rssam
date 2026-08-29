package reader

import (
	"regexp"
	"strings"

	"rssam/internal/storage"
)

// ApplyFeedRules filters entries using blocked_rules and keep_rules (regexp per line).
func ApplyFeedRules(entries []storage.CreateEntryParams, blockedRules, keepRules string) []storage.CreateEntryParams {
	blockedRE := compileMultilineRegex(blockedRules)
	keepRE := compileMultilineRegex(keepRules)
	if blockedRE == nil && keepRE == nil {
		return entries
	}

	filtered := make([]storage.CreateEntryParams, 0, len(entries))
	for _, entry := range entries {
		text := entry.Title + " " + entry.Content + " " + entry.URL
		if entry.Author != nil {
			text += " " + *entry.Author
		}
		if blockedRE != nil && blockedRE.MatchString(text) {
			continue
		}
		if keepRE != nil && !keepRE.MatchString(text) {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// ApplyURLRewriteRules rewrites entry URLs using pattern/replacement pairs.
// Format: "pattern1\nreplacement1\npattern2\nreplacement2".
func ApplyURLRewriteRules(entries []storage.CreateEntryParams, rules string) []storage.CreateEntryParams {
	rules = strings.TrimSpace(rules)
	if rules == "" {
		return entries
	}
	lines := strings.Split(rules, "\n")
	if len(lines)%2 != 0 {
		return entries
	}

	type rewriteRule struct {
		pattern     *regexp.Regexp
		replacement string
	}
	compiled := make([]rewriteRule, 0, len(lines)/2)
	for i := 0; i < len(lines)-1; i += 2 {
		pattern := strings.TrimSpace(lines[i])
		if pattern == "" {
			continue
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			continue
		}
		compiled = append(compiled, rewriteRule{pattern: re, replacement: lines[i+1]})
	}
	if len(compiled) == 0 {
		return entries
	}

	out := make([]storage.CreateEntryParams, len(entries))
	copy(out, entries)
	for i := range out {
		for _, rule := range compiled {
			out[i].URL = rule.pattern.ReplaceAllString(out[i].URL, rule.replacement)
		}
	}
	return out
}

func compileMultilineRegex(rules string) *regexp.Regexp {
	rules = strings.TrimSpace(rules)
	if rules == "" {
		return nil
	}
	lines := strings.Split(rules, "\n")
	patterns := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			patterns = append(patterns, line)
		}
	}
	if len(patterns) == 0 {
		return nil
	}
	re, err := regexp.Compile("(?:" + strings.Join(patterns, ")|(?:") + ")")
	if err != nil {
		return nil
	}
	return re
}
