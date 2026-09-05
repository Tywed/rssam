package smotrim

import (
	"regexp"
	"strings"
	"time"
)

var (
	relHoursAgoRe   = regexp.MustCompile(`(?i)^(\d+)\s+час(?:а|ов)?\s+назад$`)
	relMinutesAgoRe = regexp.MustCompile(`(?i)^(\d+)\s+мин(?:ут(?:ы|у)?)?\s+назад$`)
	relDaysAgoRe    = regexp.MustCompile(`(?i)^(\d+)\s+д(?:ень|ня|ней)?\s+назад$`)
	ruDayMonthRe    = regexp.MustCompile(`(?i)^(\d{1,2})\s+(января|февраля|марта|апреля|мая|июня|июля|августа|сентября|октября|ноября|декабря)$`)
)

var ruMonths = map[string]time.Month{
	"января": time.January, "февраля": time.February, "марта": time.March,
	"апреля": time.April, "мая": time.May, "июня": time.June,
	"июля": time.July, "августа": time.August, "сентября": time.September,
	"октября": time.October, "ноября": time.November, "декабря": time.December,
}

// MoscowLocation is used for Smotrim timestamps.
var MoscowLocation = time.FixedZone("Europe/Moscow", 3*3600)

// ParsePublishedTime parses Smotrim human-readable and ISO timestamps.
func ParsePublishedTime(raw string, now time.Time) time.Time {
	raw = strings.TrimSpace(strings.ReplaceAll(raw, "\u00a0", " "))
	if raw == "" {
		return time.Time{}
	}
	loc := MoscowLocation
	now = now.In(loc)

	layouts := []string{
		time.RFC3339,
		"2006-01-02T15:04:05-0700",
		"02-01-2006 15:04:05",
		"02.01.2006 15:04",
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t
		}
	}

	if m := relMinutesAgoRe.FindStringSubmatch(raw); len(m) > 0 {
		return now.Add(-time.Duration(atoi(m[1])) * time.Minute)
	}
	if m := relHoursAgoRe.FindStringSubmatch(raw); len(m) > 0 {
		return now.Add(-time.Duration(atoi(m[1])) * time.Hour)
	}
	if m := relDaysAgoRe.FindStringSubmatch(raw); len(m) > 0 {
		return now.AddDate(0, 0, -atoi(m[1]))
	}
	if m := ruDayMonthRe.FindStringSubmatch(raw); len(m) > 0 {
		day := atoi(m[1])
		month, ok := ruMonths[strings.ToLower(m[2])]
		if !ok {
			return time.Time{}
		}
		year := now.Year()
		candidate := time.Date(year, month, day, 12, 0, 0, 0, loc)
		if candidate.After(now.Add(48 * time.Hour)) {
			year--
			candidate = time.Date(year, month, day, 12, 0, 0, 0, loc)
		}
		return candidate
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
