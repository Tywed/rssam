package storage

import (
	"errors"
	"net/url"
	"regexp"
)

// The Telegram Bot API keeps the bot token in the request path
// (/bot<token>/sendMessage), and Go's transport errors quote the whole URL:
// `Post "http://host/bot123:ABC/sendMessage": dial tcp …`. Anything that
// turns such an error into text a person can read — the delivery log, the
// test button, the process log — goes through RedactWebhookError first.
var telegramTokenPath = regexp.MustCompile(`/bot[^/"\s]+`)

// RedactWebhookURL hides a bot token in the path and drops the query string
// (an HTTP webhook may carry its own key there).
func RedactWebhookURL(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		u.RawQuery = ""
		u.User = nil
		raw = u.String()
	}
	return telegramTokenPath.ReplaceAllString(raw, "/bot***")
}

// RedactWebhookText hides bot tokens quoted in free text (a receiver that
// echoes the request path in its error page).
func RedactWebhookText(s string) string {
	return telegramTokenPath.ReplaceAllString(s, "/bot***")
}

// RedactWebhookError is err.Error() with the request URL of a transport
// error reduced to its redacted form.
func RedactWebhookError(err error) string {
	if err == nil {
		return ""
	}
	var ue *url.Error
	if errors.As(err, &ue) && ue.URL != "" {
		return ue.Op + " " + RedactWebhookURL(ue.URL) + ": " + ue.Err.Error()
	}
	return RedactWebhookText(err.Error())
}
