// Package bridge is the contract between the rssam core and a source bridge
// (Telegram, VK, Rutube, Dzen, …). A bridge turns a subscription address into
// entries; it depends on nothing but this package, so it is built and tested
// without the storage layer or the HTTP server. The core adapts a Handler
// into its registry (reader.Adapt) and persists State per feed.
//
// The import path carries the major version: a breaking change to Request,
// Response or Handler lands in bridge/v2 while v1 keeps compiling for the
// bridges that have not moved yet.
package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// Version is the contract version this package describes.
const Version = 1

// Entry is one item a bridge produced. Hash is the dedup key (sha256 of a
// stable source id, or of the normalized URL when the source has none).
type Entry struct {
	Title       string
	URL         string
	Content     string
	Author      *string
	PublishedAt *time.Time
	Hash        string
	Enclosures  []Enclosure
}

// Enclosure is an attached media file.
type Enclosure struct {
	URL      string
	Size     int64
	MIMEType string
}

// Request is what the core hands to a bridge for one poll.
type Request struct {
	FeedURL      string
	FeedType     string
	UserAgent    string
	ETag         string
	LastModified string
	// State is the bridge's own document as stored after the previous poll
	// (nil on the first one). Its shape is private to the bridge.
	State         json.RawMessage
	FetchViaProxy bool
	TLSInsecure   bool
}

// Response is the outcome of one poll.
type Response struct {
	Entries      []Entry
	ETag         string
	LastModified string
	// NotModified reports that the source had nothing new; Entries is empty.
	NotModified bool
	// State replaces the stored document; nil keeps the previous one.
	State json.RawMessage
	// MinNextCheck is the source's own freshness hint (max-age, <ttl>); the
	// core never polls earlier than this, but may poll later.
	MinNextCheck time.Time
	// NewURL is a permanent-redirect destination the feed should move to.
	NewURL string
}

// Handler is a source bridge. Name is the feed type it serves and the key of
// its State inside feeds.bridge_state; DetectFeedType returns that name when
// feedURL belongs to the bridge and "" otherwise.
type Handler interface {
	Name() string
	DetectFeedType(feedURL string) string
	Fetch(ctx context.Context, req Request) (Response, error)
}

// TitleDiscoverer is optional: a Handler that can name a subscription before
// the first poll (the feed form suggests the title, API fills an empty one).
type TitleDiscoverer interface {
	DiscoverTitle(ctx context.Context, feedURL string) (string, error)
}

// RetryAtError is a fetch error that should be retried at a given time
// without counting as a feed failure (rate limits, cool-downs).
type RetryAtError interface {
	error
	RetryAt() time.Time
}

// RetryAt extracts the retry time from err when it is a RetryAtError.
func RetryAt(err error) (time.Time, bool) {
	if e, ok := errors.AsType[RetryAtError](err); ok {
		at := e.RetryAt()
		if !at.IsZero() {
			return at, true
		}
	}
	return time.Time{}, false
}

// DecodeState unmarshals a stored State into v; an empty document is not an
// error and leaves v untouched.
func DecodeState(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// EncodeState marshals v for Response.State.
func EncodeState(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}
