package httpserver

import (
	"encoding/json"
	"net/http"
)

// webhookHeaderMask replaces credential-bearing header values in API
// responses; a PUT that sends the mask back keeps the stored value.
const webhookHeaderMask = "***"

var sensitiveWebhookHeaders = map[string]struct{}{
	"Authorization":       {},
	"Proxy-Authorization": {},
	"Cookie":              {},
	"X-Api-Key":           {},
	"X-Auth-Token":        {},
	"Api-Key":             {},
	"X-Access-Token":      {},
}

func isSensitiveWebhookHeader(name string) bool {
	_, ok := sensitiveWebhookHeaders[http.CanonicalHeaderKey(name)]
	return ok
}

func maskWebhookHeaders(raw json.RawMessage) json.RawMessage {
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil || len(m) == 0 {
		return raw
	}
	masked := false
	for k, v := range m {
		if v != "" && isSensitiveWebhookHeader(k) {
			m[k] = webhookHeaderMask
			masked = true
		}
	}
	if !masked {
		return raw
	}
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// unmaskWebhookHeaders restores stored values for headers a client sent
// back as the mask (the usual GET → edit → PUT round trip).
func unmaskWebhookHeaders(incoming, stored json.RawMessage) json.RawMessage {
	var in map[string]string
	if err := json.Unmarshal(incoming, &in); err != nil || len(in) == 0 {
		return incoming
	}
	var old map[string]string
	_ = json.Unmarshal(stored, &old)
	changed := false
	for k, v := range in {
		if v != webhookHeaderMask {
			continue
		}
		if prev, ok := old[k]; ok {
			in[k] = prev
		} else {
			delete(in, k)
		}
		changed = true
	}
	if !changed {
		return incoming
	}
	out, err := json.Marshal(in)
	if err != nil {
		return incoming
	}
	return out
}
