package dzen

import (
	"regexp"
	"strings"
	"time"
)

var (
	relTimeRe     = regexp.MustCompile(`(?i)(Сегодня|Вчера)\s+в\s+(\d{1,2}):(\d{2})`)
	ruMonthTimeRe = regexp.MustCompile(`(?i)(\d{1,2})\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)\s+в\s+(\d{1,2}):(\d{2})`)
)

var ruMonths = map[string]time.Month{
	"января":   time.January,
	"февраля":  time.February,
	"марта":    time.March,
	"апреля":   time.April,
	"мая":      time.May,
	"июня":     time.June,
	"июля":     time.July,
	"августа":  time.August,
	"сентября": time.September,
	"октября":  time.October,
	"ноября":   time.November,
	"декабря":  time.December,
}

// MoscowLocation is the timezone used by Dzen News relative timestamps.
var MoscowLocation = time.FixedZone("Europe/Moscow", 3*3600)

// ParsePublishedTime parses Dzen human-readable timestamps.
func ParsePublishedTime(raw string, now time.Time) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	loc := MoscowLocation
	now = now.In(loc)

	if m := relTimeRe.FindStringSubmatch(raw); len(m) > 0 {
		rel := strings.ToLower(m[1])
		hour := atoi(m[2])
		min := atoi(m[3])
		base := now
		if rel == "вчера" {
			base = now.AddDate(0, 0, -1)
		}
		return time.Date(base.Year(), base.Month(), base.Day(), hour, min, 0, 0, loc)
	}
	if m := ruMonthTimeRe.FindStringSubmatch(raw); len(m) > 0 {
		day := atoi(m[1])
		month, ok := ruMonths[strings.ToLower(m[2])]
		if !ok {
			return time.Time{}
		}
		hour := atoi(m[3])
		min := atoi(m[4])
		year := now.Year()
		candidate := time.Date(year, month, day, hour, min, 0, 0, loc)
		if candidate.After(now.Add(24 * time.Hour)) {
			year--
			candidate = time.Date(year, month, day, hour, min, 0, 0, loc)
		}
		return candidate
	}

	layouts := []string{
		time.RFC3339,
		"02.01.2006 15:04",
		"02.01.2006",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t
		}
	}
	return time.Time{}
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return n
		}
		n = n*10 + int(r-'0')
	}
	return n
}
