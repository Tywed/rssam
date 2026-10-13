package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// FeedTypeInbound marks a feed whose entries arrive by POST /hooks/{id}
// instead of polling; it mirrors reader.FeedTypeInbound.
const FeedTypeInbound = "inbound"

// ErrNotInboundFeed: the feed exists but is a polled one.
var ErrNotInboundFeed = errors.New("feed does not accept inbound webhooks")

// InboundFeedStore creates inbound feeds and manages their secrets.
type InboundFeedStore interface {
	CreateInboundFeed(ctx context.Context, userID int64, params CreateInboundFeedParams) (Feed, string, error)
	// InboundSecret is the shared secret of an inbound feed;
	// ErrNotInboundFeed for a polled feed, ErrNotFound when missing.
	InboundSecret(ctx context.Context, feedID int64) (string, error)
	RotateInboundSecret(ctx context.Context, feedID int64) (string, error)
}

type CreateInboundFeedParams struct {
	Title                 string
	CategoryID            *int64
	EntryRetentionDays    *int
	SkipOwnerSubscription bool
}

// inboundNeverDue keeps an inbound feed out of every "due" query and shows
// it as OK on the admin page: next_check_at a century away.
const inboundNeverDue = `now() + interval '100 years'`

// CreateInboundFeed adds an inbound feed to the catalog and returns it with
// the secret that signs deliveries. The feed_url is a synthetic unique
// inbound:// address, since the column is NOT NULL UNIQUE.
func (s *PostgresStore) CreateInboundFeed(ctx context.Context, userID int64, params CreateInboundFeedParams) (Feed, string, error) {
	secret, err := newInboundSecret()
	if err != nil {
		return Feed{}, "", err
	}
	id, err := newInboundSecret()
	if err != nil {
		return Feed{}, "", err
	}
	f, err := s.CreateFeed(ctx, userID, CreateFeedParams{
		FeedURL:               "inbound://" + id[:16],
		FeedType:              FeedTypeInbound,
		Title:                 params.Title,
		CategoryID:            params.CategoryID,
		IntervalMinutes:       60,
		EntryRetentionDays:    params.EntryRetentionDays,
		SkipOwnerSubscription: params.SkipOwnerSubscription,
	})
	if err != nil {
		return Feed{}, "", err
	}
	if _, err := s.db.Exec(ctx, `UPDATE feeds SET inbound_secret = $2, next_check_at = `+inboundNeverDue+` WHERE id = $1`, f.ID, secret); err != nil {
		return Feed{}, "", fmt.Errorf("set inbound secret: %w", err)
	}
	return f, secret, nil
}

func (s *PostgresStore) InboundSecret(ctx context.Context, feedID int64) (string, error) {
	var secret, feedType string
	err := s.db.QueryRow(ctx, `SELECT inbound_secret, feed_type FROM feeds WHERE id = $1`, feedID).Scan(&secret, &feedType)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("inbound secret: %w", err)
	}
	if feedType != FeedTypeInbound || secret == "" {
		return "", ErrNotInboundFeed
	}
	return secret, nil
}

func (s *PostgresStore) RotateInboundSecret(ctx context.Context, feedID int64) (string, error) {
	secret, err := newInboundSecret()
	if err != nil {
		return "", err
	}
	tag, err := s.db.Exec(ctx, `UPDATE feeds SET inbound_secret = $2, updated_at = now() WHERE id = $1 AND feed_type = '`+FeedTypeInbound+`'`, feedID, secret)
	if err != nil {
		return "", fmt.Errorf("rotate inbound secret: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return "", ErrNotFound
	}
	return secret, nil
}

// newInboundSecret is 256 random bits as 64 hex characters.
func newInboundSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("inbound secret: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}
