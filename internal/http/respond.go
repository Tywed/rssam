// JSON response envelope, error helper and the request parsing helpers
// shared by all API handlers.
package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"rssam/internal/http/middleware"
	"rssam/internal/requestid"
)

type listResponse[T any] struct {
	Data  T   `json:"data"`
	Total int `json:"total"`
}

type errorResponse struct {
	ErrorMessage string `json:"error_message"`
	// RequestID is set on 5xx responses so the caller can quote it; the
	// same value is in the X-Request-Id header and in the server log line.
	RequestID string `json:"request_id,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	resp := errorResponse{ErrorMessage: msg}
	if status >= http.StatusInternalServerError {
		// The middleware set the header before the handler ran, so it is
		// available here without threading the request through.
		resp.RequestID = w.Header().Get(requestid.Header)
	}
	writeJSON(w, status, resp)
}

type deletedDTO struct {
	Deleted bool `json:"deleted"`
}

func parseLimitOffset(r *http.Request, defLimit, capLimit int) (limit, offset int, err error) {
	q := r.URL.Query()
	limit = defLimit
	offset = 0

	if v := q.Get("limit"); v != "" {
		limit, err = atoiNonNeg(v)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid limit")
		}
	}
	if v := q.Get("offset"); v != "" {
		offset, err = atoiNonNeg(v)
		if err != nil {
			return 0, 0, fmt.Errorf("invalid offset")
		}
	}
	if limit > capLimit {
		limit = capLimit
	}
	return limit, offset, nil
}

func atoiNonNeg(s string) (int, error) {
	if s == "" {
		return 0, errors.New("empty")
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func decodeJSONBody(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if middleware.IsBodyTooLarge(err) {
			return errors.New("request body too large")
		}
		return errors.New("invalid JSON body")
	}
	return nil
}

func parsePathID(r *http.Request) (int64, error) {
	raw := strings.TrimSpace(r.PathValue("id"))
	if raw == "" {
		return 0, errors.New("id is required")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func parsePathInt64(r *http.Request, key string) (int64, error) {
	raw := strings.TrimSpace(r.PathValue(key))
	if raw == "" {
		return 0, errors.New("id is required")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}
