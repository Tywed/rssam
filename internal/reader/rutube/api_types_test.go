package rutube

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFlexUnixTS_UnmarshalJSON(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
	}{
		{name: "unix number", raw: `1716400000`, want: 1716400000},
		{name: "unix string", raw: `"1716400000"`, want: 1716400000},
		{name: "iso datetime", raw: `"2026-08-12T19:30:02"`, want: time.Date(2026, 8, 12, 19, 30, 2, 0, time.UTC).Unix()},
		{name: "null", raw: `null`, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got flexUnixTS
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got.Unix() != tc.want {
				t.Fatalf("got %d want %d", got.Unix(), tc.want)
			}
		})
	}
}

func TestPersonVideosResponse_ISOTimestamps(t *testing.T) {
	const body = `{
		"results": [{
			"id": "abc123",
			"video_url": "https://rutube.ru/video/abc123/",
			"title": "ISO test",
			"publication_ts": "2026-08-12T19:39:03",
			"created_ts": "2026-08-12T19:30:02",
			"description": "",
			"thumbnail_url": "",
			"author": {"name": "Author"}
		}]
	}`
	var parsed personVideosResponse
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(parsed.Results) != 1 {
		t.Fatalf("results=%d", len(parsed.Results))
	}
	want := time.Date(2026, 8, 12, 19, 39, 3, 0, time.UTC).Unix()
	if got := parsed.Results[0].PublicationTS.Unix(); got != want {
		t.Fatalf("publication_ts=%d want %d", got, want)
	}
}
