package rutube

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type personVideosResponse struct {
	Results []personVideo `json:"results"`
}

type personVideo struct {
	ID            string      `json:"id"`
	VideoURL      string      `json:"video_url"`
	Title         string      `json:"title"`
	PublicationTS flexUnixTS  `json:"publication_ts"`
	CreatedTS     flexUnixTS  `json:"created_ts"`
	Description   string      `json:"description"`
	ThumbnailURL  string      `json:"thumbnail_url"`
	Author        videoAuthor `json:"author"`
}

type videoAuthor struct {
	Name string `json:"name"`
}

// flexUnixTS accepts JSON numbers or numeric strings (Rutube API variants).
type flexUnixTS int64

func (f *flexUnixTS) UnmarshalJSON(b []byte) error {
	b = []byte(strings.TrimSpace(string(b)))
	if len(b) == 0 || string(b) == "null" {
		*f = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		ts, err := parseFlexUnixTS(s)
		if err != nil {
			return err
		}
		*f = flexUnixTS(ts)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	v, err := n.Int64()
	if err != nil {
		return err
	}
	*f = flexUnixTS(v)
	return nil
}

func (f flexUnixTS) Unix() int64 {
	return int64(f)
}

func parseFlexUnixTS(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n, nil
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, layout := range layouts {
		t, err := time.Parse(layout, raw)
		if err == nil {
			return t.UTC().Unix(), nil
		}
	}
	return 0, fmt.Errorf("unsupported timestamp %q", raw)
}
