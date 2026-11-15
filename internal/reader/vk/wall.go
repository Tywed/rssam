package vk

import (
	"context"
	"errors"
	"fmt"
	"time"

	"rssam/internal/bridge/v1"
)

var (
	// ErrWallNotFound is a screen name or id that VK does not know.
	ErrWallNotFound = errors.New("vk: wall not found")
	// ErrWallClosed is a private, blocked or deleted wall.
	ErrWallClosed = errors.New("vk: wall is not accessible")
	// ErrTokenRejected is a token VK no longer accepts: an operator problem
	// shared by every VK feed, not a feed fault.
	ErrTokenRejected = errors.New("vk: access token rejected")
)

// CooldownError defers the poll on API throttling without counting a
// failure.
type CooldownError struct {
	Code int
	At   time.Time
}

func (e CooldownError) Error() string      { return fmt.Sprintf("vk: rate limited (error %d)", e.Code) }
func (e CooldownError) RetryAt() time.Time { return e.At }

// fetchWall serves the wall of one community or user via wall.get. It keeps
// no cursor: the newest wallGetCount posts come back every poll and the
// core's dedup drops the known ones, so edited or pinned posts never
// reappear and nothing is lost when a poll fails.
func (h *Handler) fetchWall(ctx context.Context, req bridge.Request) (bridge.Response, error) {
	if h == nil || h.client == nil {
		return bridge.Response{}, ErrMissingToken
	}
	wall, ok := ParseWallFromFeedURL(req.FeedURL)
	if !ok {
		return bridge.Response{}, errInvalidWallURL
	}
	body, err := h.wall(ctx, wall)
	if err != nil {
		return bridge.Response{}, err
	}
	ownerNames := buildOwnerNames(body.Profiles, body.Groups)
	entries := make([]bridge.Entry, 0, len(body.Items))
	for _, post := range body.Items {
		entries = append(entries, entryFromPost(post, ownerNames))
	}
	return bridge.Response{Entries: entries}, nil
}

// DiscoverTitle fills an empty title field: "VK: query" for a search, the
// wall owner's name (one wall.get) for a wall.
func (h *Handler) DiscoverTitle(ctx context.Context, feedURL string) (string, error) {
	if q, ok := ParseQueryFromFeedURL(feedURL); ok {
		return FeedTitle(q), nil
	}
	if h == nil || h.client == nil {
		return "", ErrMissingToken
	}
	wall, ok := ParseWallFromFeedURL(feedURL)
	if !ok {
		return "", errInvalidWallURL
	}
	body, err := h.wall(ctx, wall)
	if err != nil {
		return "", err
	}
	if name := wallOwnerName(wall, body); name != "" {
		return name, nil
	}
	return wall.String(), nil
}

func (h *Handler) wall(ctx context.Context, wall Wall) (*searchResponseBody, error) {
	body, apiErr, err := h.client.WallGet(ctx, wall)
	if err != nil {
		return nil, err
	}
	if apiErr != nil {
		return nil, mapWallError(apiErr, time.Now().UTC(), h.config().RateLimitCooldown)
	}
	return body, nil
}

// mapAPIError turns the VK error codes both modes share into the bridge
// contract: throttling defers, a bad token is an operator error, the rest is
// a plain feed error.
func mapAPIError(e *apiError, now time.Time, cooldown time.Duration) error {
	switch e.ErrorCode {
	case 6, 9: // too many requests / flood control
		return CooldownError{Code: e.ErrorCode, At: now.Add(cooldown)}
	case 29: // method rate limit reached (per day)
		return CooldownError{Code: e.ErrorCode, At: now.Add(time.Hour)}
	case 5, 27, 28: // authorization failed / community or application token invalid
		return fmt.Errorf("%w: %s", ErrTokenRejected, e.ErrorMsg)
	default:
		return fmt.Errorf("vk api error %d: %s", e.ErrorCode, e.ErrorMsg)
	}
}

// mapWallError adds the wall sentinels the UI explains.
func mapWallError(e *apiError, now time.Time, cooldown time.Duration) error {
	switch e.ErrorCode {
	case 15, 18, 19, 30: // access denied / deleted or banned / private profile
		return fmt.Errorf("%w (vk error %d: %s)", ErrWallClosed, e.ErrorCode, e.ErrorMsg)
	case 100, 113, 125: // bad parameter / invalid user id / invalid group id
		return fmt.Errorf("%w (vk error %d: %s)", ErrWallNotFound, e.ErrorCode, e.ErrorMsg)
	default:
		return mapAPIError(e, now, cooldown)
	}
}

// wallOwnerName is the name of the wall's owner: the group or profile the
// API resolved for the request (the first post's owner when the wall is
// empty of metadata).
func wallOwnerName(wall Wall, body *searchResponseBody) string {
	if wall.OwnerID != 0 {
		if wall.OwnerID < 0 {
			for _, g := range body.Groups {
				if g.ID == -wall.OwnerID {
					return g.Name
				}
			}
		} else {
			for _, p := range body.Profiles {
				if p.ID == wall.OwnerID {
					return profileName(p)
				}
			}
		}
	}
	if len(body.Items) > 0 {
		owner := body.Items[0].OwnerID
		for _, g := range body.Groups {
			if g.ID == -owner {
				return g.Name
			}
		}
		for _, p := range body.Profiles {
			if p.ID == owner {
				return profileName(p)
			}
		}
	}
	if len(body.Groups) == 1 {
		return body.Groups[0].Name
	}
	if len(body.Profiles) == 1 {
		return profileName(body.Profiles[0])
	}
	return ""
}
