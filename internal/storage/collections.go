package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

var ErrAlreadyFollowing = errors.New("already following")

// Collection is a curated set of catalog feeds; Followed and CategoryID
// describe the viewing user's membership.
type Collection struct {
	ID            int64
	OwnerID       int64
	OwnerName     string
	Title         string
	Description   string
	FeedCount     int
	FollowerCount int
	Followed      bool
	CategoryID    *int64
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type CollectionParams struct {
	Title       string
	Description string
}

// FollowResult: how many new subscriptions the follow created and the
// category they went into.
type FollowResult struct {
	Subscribed int
	CategoryID int64
}

// CollectionStore manages collections and keeps followers' subscriptions in
// step with the set: feeds added to a collection are subscribed for every
// follower, feeds removed from it (or the whole follow) are unsubscribed
// unless another followed collection still lists the feed. Subscriptions
// the user made by hand are never touched.
type CollectionStore interface {
	CreateCollection(ctx context.Context, ownerID int64, params CollectionParams) (Collection, error)
	UpdateCollection(ctx context.Context, id int64, params CollectionParams) error
	// DeleteCollection drops the set; followers keep the subscriptions it
	// created (they are detached, not removed).
	DeleteCollection(ctx context.Context, id int64) error
	GetCollection(ctx context.Context, userID, id int64) (Collection, error)
	ListCollections(ctx context.Context, userID int64) ([]Collection, error)
	ListCollectionFeeds(ctx context.Context, userID, id int64) ([]CatalogFeed, error)
	// AddCollectionFeeds returns how many feeds were new to the set;
	// ErrInvalidReference when a feed id is not in the catalog.
	AddCollectionFeeds(ctx context.Context, id int64, feedIDs []int64) (int, error)
	RemoveCollectionFeed(ctx context.Context, id, feedID int64) error
	// FollowCollection subscribes the user to every feed of the set into
	// categoryID, or into the user's category titled like the collection
	// (created when missing) when nil. ErrAlreadyFollowing on repeat.
	FollowCollection(ctx context.Context, userID, id int64, categoryID *int64) (FollowResult, error)
	// UnfollowCollection returns how many subscriptions were removed.
	UnfollowCollection(ctx context.Context, userID, id int64) (int, error)
	CountCollections(ctx context.Context) (int, error)
}

const collectionColumns = `c.id, COALESCE(c.owner_id, 0), COALESCE(u.username, ''), c.title, c.description,
       (SELECT count(*)::int FROM collection_feeds cf WHERE cf.collection_id = c.id),
       (SELECT count(*)::int FROM collection_followers f WHERE f.collection_id = c.id),
       f.user_id IS NOT NULL, f.category_id, c.created_at, c.updated_at`

const collectionFrom = `FROM collections c
LEFT JOIN users u ON u.id = c.owner_id
LEFT JOIN collection_followers f ON f.collection_id = c.id AND f.user_id = $1`

func scanCollection(row pgx.Row) (Collection, error) {
	var c Collection
	err := row.Scan(&c.ID, &c.OwnerID, &c.OwnerName, &c.Title, &c.Description, &c.FeedCount, &c.FollowerCount, &c.Followed, &c.CategoryID, &c.CreatedAt, &c.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Collection{}, ErrNotFound
	}
	return c, err
}

func (s *PostgresStore) CreateCollection(ctx context.Context, ownerID int64, params CollectionParams) (Collection, error) {
	var id int64
	if err := s.db.QueryRow(ctx, `INSERT INTO collections(owner_id, title, description) VALUES ($1, $2, $3) RETURNING id`,
		ownerID, params.Title, params.Description).Scan(&id); err != nil {
		return Collection{}, fmt.Errorf("create collection: %w", err)
	}
	return s.GetCollection(ctx, ownerID, id)
}

func (s *PostgresStore) UpdateCollection(ctx context.Context, id int64, params CollectionParams) error {
	cmd, err := s.db.Exec(ctx, `UPDATE collections SET title = $2, description = $3, updated_at = now() WHERE id = $1`, id, params.Title, params.Description)
	if err != nil {
		return fmt.Errorf("update collection: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) DeleteCollection(ctx context.Context, id int64) error {
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		if err := rehomeCollectionSubscriptions(ctx, tx, id, nil, nil); err != nil {
			return err
		}
		cmd, err := tx.Exec(ctx, `DELETE FROM collections WHERE id = $1`, id)
		if err != nil {
			return fmt.Errorf("delete collection: %w", err)
		}
		if cmd.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	})
}

func (s *PostgresStore) GetCollection(ctx context.Context, userID, id int64) (Collection, error) {
	c, err := scanCollection(s.db.QueryRow(ctx, `SELECT `+collectionColumns+` `+collectionFrom+` WHERE c.id = $2`, userID, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return Collection{}, fmt.Errorf("get collection: %w", err)
	}
	return c, err
}

func (s *PostgresStore) ListCollections(ctx context.Context, userID int64) ([]Collection, error) {
	rows, err := s.db.Query(ctx, `SELECT `+collectionColumns+` `+collectionFrom+` ORDER BY lower(c.title), c.id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()
	out := make([]Collection, 0)
	for rows.Next() {
		c, err := scanCollection(rows)
		if err != nil {
			return nil, fmt.Errorf("scan collection: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collections: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) ListCollectionFeeds(ctx context.Context, userID, id int64) ([]CatalogFeed, error) {
	rows, err := s.db.Query(ctx, `
SELECT `+catalogColumns+`
FROM collection_feeds cf
JOIN feeds f ON f.id = cf.feed_id
LEFT JOIN users u ON u.id = f.owner_id
WHERE cf.collection_id = $2
ORDER BY lower(f.title), f.id`, userID, id)
	if err != nil {
		return nil, fmt.Errorf("list collection feeds: %w", err)
	}
	defer rows.Close()
	out := make([]CatalogFeed, 0)
	for rows.Next() {
		var c CatalogFeed
		if err := rows.Scan(&c.ID, &c.FeedURL, &c.FeedType, &c.Title, &c.OwnerID, &c.OwnerName, &c.SubscriberCount, &c.Subscribed, &c.LastEntryAt, &c.LastError, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan collection feed: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate collection feeds: %w", err)
	}
	return out, nil
}

type collectionFollower struct {
	userID     int64
	categoryID *int64
}

func listCollectionFollowers(ctx context.Context, tx pgx.Tx, collectionID int64) ([]collectionFollower, error) {
	rows, err := tx.Query(ctx, `SELECT user_id, category_id FROM collection_followers WHERE collection_id = $1 ORDER BY user_id`, collectionID)
	if err != nil {
		return nil, fmt.Errorf("list collection followers: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (collectionFollower, error) {
		var f collectionFollower
		err := row.Scan(&f.userID, &f.categoryID)
		return f, err
	})
}

func (s *PostgresStore) AddCollectionFeeds(ctx context.Context, id int64, feedIDs []int64) (int, error) {
	added := 0
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM collections WHERE id = $1 FOR UPDATE)`, id).Scan(&exists); err != nil {
			return fmt.Errorf("lock collection: %w", err)
		}
		if !exists {
			return ErrNotFound
		}
		followers, err := listCollectionFollowers(ctx, tx, id)
		if err != nil {
			return err
		}
		for _, feedID := range feedIDs {
			cmd, err := tx.Exec(ctx, `INSERT INTO collection_feeds(collection_id, feed_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, id, feedID)
			if err != nil {
				if isForeignKeyViolation(err) {
					return ErrInvalidReference
				}
				return fmt.Errorf("add collection feed: %w", err)
			}
			if cmd.RowsAffected() == 0 {
				continue
			}
			added++
			for _, f := range followers {
				if _, err := subscribeTx(ctx, tx, f.userID, feedID, SubscriptionParams{CategoryID: f.categoryID}, &id); err != nil && !errors.Is(err, ErrAlreadySubscribed) {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return added, nil
}

func (s *PostgresStore) RemoveCollectionFeed(ctx context.Context, id, feedID int64) error {
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		cmd, err := tx.Exec(ctx, `DELETE FROM collection_feeds WHERE collection_id = $1 AND feed_id = $2`, id, feedID)
		if err != nil {
			return fmt.Errorf("remove collection feed: %w", err)
		}
		if cmd.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := rehomeCollectionSubscriptions(ctx, tx, id, &feedID, nil); err != nil {
			return err
		}
		_, err = unsubscribeCollectionRest(ctx, tx, id, &feedID, nil)
		return err
	})
}

func (s *PostgresStore) FollowCollection(ctx context.Context, userID, id int64, categoryID *int64) (FollowResult, error) {
	var res FollowResult
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		var title string
		if err := tx.QueryRow(ctx, `SELECT title FROM collections WHERE id = $1 FOR SHARE`, id).Scan(&title); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("follow collection: %w", err)
		}
		if categoryID == nil {
			cid, err := userCategoryByTitle(ctx, tx, userID, title)
			if err != nil {
				return err
			}
			categoryID = &cid
		} else if err := checkSubscriptionRefs(ctx, tx, userID, SubscriptionParams{CategoryID: categoryID}); err != nil {
			return err
		}
		cmd, err := tx.Exec(ctx, `INSERT INTO collection_followers(collection_id, user_id, category_id) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, id, userID, categoryID)
		if err != nil {
			return fmt.Errorf("follow collection: %w", err)
		}
		if cmd.RowsAffected() == 0 {
			return ErrAlreadyFollowing
		}
		rows, err := tx.Query(ctx, `SELECT feed_id FROM collection_feeds WHERE collection_id = $1 ORDER BY feed_id`, id)
		if err != nil {
			return fmt.Errorf("follow collection feeds: %w", err)
		}
		feedIDs, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			return fmt.Errorf("follow collection feeds: %w", err)
		}
		for _, feedID := range feedIDs {
			if _, err := subscribeTx(ctx, tx, userID, feedID, SubscriptionParams{CategoryID: categoryID}, &id); err != nil {
				if errors.Is(err, ErrAlreadySubscribed) {
					continue
				}
				return err
			}
			res.Subscribed++
		}
		res.CategoryID = *categoryID
		return nil
	})
	if err != nil {
		return FollowResult{}, err
	}
	return res, nil
}

// userCategoryByTitle returns the user's first category with this title,
// creating it when there is none.
func userCategoryByTitle(ctx context.Context, tx pgx.Tx, userID int64, title string) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM categories WHERE user_id = $1 AND title = $2 ORDER BY id LIMIT 1`, userID, title).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("find category: %w", err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO categories(user_id, title, color, sort_order)
VALUES ($1, $2, '', COALESCE((SELECT MAX(sort_order) + 1 FROM categories WHERE user_id = $1), 0))
RETURNING id`, userID, title).Scan(&id); err != nil {
		return 0, fmt.Errorf("create category: %w", err)
	}
	return id, nil
}

func (s *PostgresStore) UnfollowCollection(ctx context.Context, userID, id int64) (int, error) {
	removed := 0
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		cmd, err := tx.Exec(ctx, `DELETE FROM collection_followers WHERE collection_id = $1 AND user_id = $2`, id, userID)
		if err != nil {
			return fmt.Errorf("unfollow collection: %w", err)
		}
		if cmd.RowsAffected() == 0 {
			return ErrNotFound
		}
		if err := rehomeCollectionSubscriptions(ctx, tx, id, nil, &userID); err != nil {
			return err
		}
		removed, err = unsubscribeCollectionRest(ctx, tx, id, nil, &userID)
		return err
	})
	if err != nil {
		return 0, err
	}
	return removed, nil
}

// rehomeCollectionSubscriptions moves subscriptions created by collection
// id (optionally only for one feed or one user) to another collection the
// user follows that still lists the feed, so that the follow-up
// unsubscribe only drops feeds no followed collection covers.
func rehomeCollectionSubscriptions(ctx context.Context, tx pgx.Tx, id int64, feedID, userID *int64) error {
	if _, err := tx.Exec(ctx, `
UPDATE subscriptions s SET collection_id = o.collection_id
FROM (SELECT DISTINCT ON (cfo.user_id, cf.feed_id) cfo.user_id, cf.feed_id, cf.collection_id
      FROM collection_feeds cf
      JOIN collection_followers cfo ON cfo.collection_id = cf.collection_id
      WHERE cf.collection_id <> $1
        AND ($2::bigint IS NULL OR cf.feed_id = $2)
        AND ($3::bigint IS NULL OR cfo.user_id = $3)
      ORDER BY cfo.user_id, cf.feed_id, cf.collection_id) o
WHERE s.collection_id = $1 AND s.user_id = o.user_id AND s.feed_id = o.feed_id`, id, feedID, userID); err != nil {
		return fmt.Errorf("rehome collection subscriptions: %w", err)
	}
	return nil
}

// unsubscribeCollectionRest removes the subscriptions still attributed to
// collection id (optionally narrowed to one feed or one user) and drops
// catalog feeds left without readers.
func unsubscribeCollectionRest(ctx context.Context, tx pgx.Tx, id int64, feedID, userID *int64) (int, error) {
	rows, err := tx.Query(ctx, `
SELECT user_id, feed_id FROM subscriptions
WHERE collection_id = $1 AND ($2::bigint IS NULL OR feed_id = $2) AND ($3::bigint IS NULL OR user_id = $3)
ORDER BY user_id, feed_id`, id, feedID, userID)
	if err != nil {
		return 0, fmt.Errorf("list collection subscriptions: %w", err)
	}
	type pair struct{ user, feed int64 }
	pairs, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pair, error) {
		var p pair
		err := row.Scan(&p.user, &p.feed)
		return p, err
	})
	if err != nil {
		return 0, fmt.Errorf("list collection subscriptions: %w", err)
	}
	for _, p := range pairs {
		if err := unsubscribeTx(ctx, tx, p.user, p.feed); err != nil {
			return 0, err
		}
		if err := dropOrphanFeed(ctx, tx, p.feed); err != nil {
			return 0, err
		}
	}
	return len(pairs), nil
}

func (s *PostgresStore) CountCollections(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM collections`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count collections: %w", err)
	}
	return n, nil
}
