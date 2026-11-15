// Package feedrules parses the per-feed rule texts (blocked_rules,
// keep_rules, rewrite_rules, scraper_rules) once, with line-numbered
// errors for the save paths, and caches the compiled form for the poll
// paths — a fleet polls hundreds of thousands of times a day and the texts
// change only when someone edits the feed.
package feedrules

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/andybalholm/cascadia"
)

// MaxRulesBytes bounds each rule text: longer than this is a paste error,
// not a rule set.
const MaxRulesBytes = 8 * 1024

// Rewrite is one pattern/replacement pair of rewrite_rules.
type Rewrite struct {
	Pattern     *regexp.Regexp
	Replacement string
}

// Rules is the compiled form of a feed's rule texts.
type Rules struct {
	Blocked  *regexp.Regexp
	Keep     *regexp.Regexp
	Rewrites []Rewrite
}

// Empty reports whether no rule applies.
func (r *Rules) Empty() bool {
	return r == nil || (r.Blocked == nil && r.Keep == nil && len(r.Rewrites) == 0)
}

// Lines splits a rule text into lines: CRLF from a textarea or a Windows
// OPML counts as a newline, not as a character of the pattern.
func Lines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return strings.Split(text, "\n")
}

// LineError says which line of which field is wrong.
type LineError struct {
	Field string
	Line  int
	Err   error
}

func (e *LineError) Error() string {
	return fmt.Sprintf("%s line %d: %v", e.Field, e.Line, e.Err)
}

func (e *LineError) Unwrap() error { return e.Err }

func tooLong(field, text string) error {
	if len(text) > MaxRulesBytes {
		return fmt.Errorf("%s: longer than %d bytes", field, MaxRulesBytes)
	}
	return nil
}

// CompileMatch compiles blocked_rules or keep_rules: one regexp per
// non-empty line, joined by alternation. nil means no rule.
func CompileMatch(field, text string) (*regexp.Regexp, error) {
	if err := tooLong(field, text); err != nil {
		return nil, err
	}
	var patterns []string
	for i, line := range Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if _, err := regexp.Compile(line); err != nil {
			return nil, &LineError{Field: field, Line: i + 1, Err: err}
		}
		patterns = append(patterns, line)
	}
	if len(patterns) == 0 {
		return nil, nil
	}
	return regexp.Compile("(?:" + strings.Join(patterns, ")|(?:") + ")")
}

// CompileRewrites compiles rewrite_rules: pattern on an odd line,
// replacement verbatim on the next (an empty replacement line deletes the
// match). Empty lines after the last pair are ignored; a pattern without a
// replacement line is an error.
func CompileRewrites(text string) ([]Rewrite, error) {
	const field = "rewrite_rules"
	if err := tooLong(field, text); err != nil {
		return nil, err
	}
	lines := Lines(text)
	if len(lines)%2 == 1 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	var out []Rewrite
	for i := 0; i < len(lines); i += 2 {
		pattern := strings.TrimSpace(lines[i])
		if i+1 >= len(lines) {
			if pattern == "" {
				break
			}
			return nil, &LineError{Field: field, Line: i + 1, Err: errors.New("pattern without a replacement line")}
		}
		replacement := lines[i+1]
		if pattern == "" {
			if strings.TrimSpace(replacement) == "" {
				continue
			}
			return nil, &LineError{Field: field, Line: i + 1, Err: errors.New("empty pattern")}
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, &LineError{Field: field, Line: i + 1, Err: err}
		}
		out = append(out, Rewrite{Pattern: re, Replacement: replacement})
	}
	return out, nil
}

// ValidateScraper checks scraper_rules: every non-empty line is a CSS
// selector, either bare or as key=selector.
func ValidateScraper(text string) error {
	const field = "scraper_rules"
	if err := tooLong(field, text); err != nil {
		return err
	}
	for i, line := range Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		sel := line
		if _, v, ok := strings.Cut(line, "="); ok {
			sel = strings.TrimSpace(v)
			if sel == "" {
				return &LineError{Field: field, Line: i + 1, Err: errors.New("empty selector")}
			}
		}
		if _, err := cascadia.Compile(sel); err != nil {
			return &LineError{Field: field, Line: i + 1, Err: err}
		}
	}
	return nil
}

// Validate checks all four texts the way the poll will read them.
func Validate(scraper, rewrite, blocked, keep string) error {
	if err := ValidateScraper(scraper); err != nil {
		return err
	}
	if _, err := CompileRewrites(rewrite); err != nil {
		return err
	}
	if _, err := CompileMatch("blocked_rules", blocked); err != nil {
		return err
	}
	_, err := CompileMatch("keep_rules", keep)
	return err
}

// Compile builds Rules from the three texts; a rule set with an error in
// it is reported, not silently dropped.
func Compile(blocked, keep, rewrite string) (*Rules, error) {
	var r Rules
	var err error
	if r.Blocked, err = CompileMatch("blocked_rules", blocked); err != nil {
		return nil, err
	}
	if r.Keep, err = CompileMatch("keep_rules", keep); err != nil {
		return nil, err
	}
	if r.Rewrites, err = CompileRewrites(rewrite); err != nil {
		return nil, err
	}
	return &r, nil
}

// cacheMax bounds the compiled sets kept: beyond it the map is dropped and
// refilled (an edit changes the key; a fleet has few distinct rule sets).
const cacheMax = 4096

type cacheKey struct{ blocked, keep, rewrite string }

var cache struct {
	mu sync.Mutex
	m  map[cacheKey]*Rules
}

// Cached compiles the texts with a process-wide cache keyed by them. A
// text that fails to compile (saved before validation existed) is
// inert, as it always was, while the other two still apply; the poll
// does not log it on every pass.
func Cached(blocked, keep, rewrite string) *Rules {
	if blocked == "" && keep == "" && rewrite == "" {
		return nil
	}
	key := cacheKey{blocked, keep, rewrite}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if r, ok := cache.m[key]; ok {
		return r
	}
	r := &Rules{}
	r.Blocked, _ = CompileMatch("blocked_rules", blocked)
	r.Keep, _ = CompileMatch("keep_rules", keep)
	r.Rewrites, _ = CompileRewrites(rewrite)
	if cache.m == nil || len(cache.m) >= cacheMax {
		cache.m = make(map[cacheKey]*Rules)
	}
	cache.m[key] = r
	return r
}

// Apply runs the rewrites over s.
func (r *Rules) Apply(s string) string {
	if r == nil {
		return s
	}
	for _, rw := range r.Rewrites {
		s = rw.Pattern.ReplaceAllString(s, rw.Replacement)
	}
	return s
}
