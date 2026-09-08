package storage

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound         = errors.New("not found")
	ErrDuplicateFeedURL = errors.New("feed_url already exists")
	ErrInvalidReference = errors.New("invalid reference")
	// ErrLastAdmin is returned when a change would leave the instance without
	// any admin that can log in (is_admin with a password set).
	ErrLastAdmin = errors.New("cannot remove the last admin")
)

const (
	MinFeedIntervalMinutes    = 1
	MaxFeedIntervalMinutes    = 10080 // 7 days
	MinFeedEntryRetentionDays = 1
	MaxFeedEntryRetentionDays = 3650 // ~10 years
	// NoLimit means no SQL LIMIT clause (list all rows).
	NoLimit = 0
)

type Category struct {
	ID        int64
	UserID    int64
	Title     string
	Color     string
	SortOrder int
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Feed struct {
	ID                 int64
	UserID             int64
	FeedURL            string
	FeedType           string
	Title              string
	CategoryID         *int64
	IntervalMinutes    int
	ETag               string
	LastModified       string
	LastCheckedAt      *time.Time
	LastError          string
	ParsingErrorCount  int
	PollPaused         bool
	ManualPaused       bool
	StoreHashOnly      bool
	EntryRetentionDays *int
	NextCheckAt        *time.Time
	BridgeState        []byte
	ScraperRules       string
	RewriteRules       string
	BlockedRules       string
	KeepRules          string
	FetchViaProxy      bool
	TLSInsecure        bool
	Crawler            bool
	UserAgent          string
	WebhookID          *int64
	IconURL            string
	IconData           []byte
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

type Job struct {
	ID        int64
	Type      string
	FeedID    *int64
	RunAt     time.Time
	Payload   []byte
	Attempts  int
	LastError *string
	LockedAt  *time.Time
	LockedBy  *string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Entry struct {
	ID              int64
	FeedID          int64
	Title           string
	URL             string
	Content         string
	OriginalContent string
	ContentFetched  bool
	Author          *string
	PublishedAt     *time.Time
	Hash            string
	Status          string
	Starred         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type Enclosure struct {
	ID               int64
	UserID           int64
	EntryID          int64
	URL              string
	Size             int64
	MIMEType         string
	MediaProgression int
}

type Filter struct {
	ID           int64
	UserID       int64
	Name         string
	Enabled      bool
	MatchAnyRule bool
	Inverse      bool
	OrderID      int
	FeedScope    string
	MatchCount   int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Rules        []FilterRule
	ScopeItems   []FilterScopeItem
	Actions      []FilterAction
}

const (
	FilterFeedScopeAll     = "all"
	FilterFeedScopeInclude = "include"
	FilterFeedScopeExclude = "exclude"
)

type FilterScopeItem struct {
	ID         int64
	FilterID   int64
	FeedID     *int64
	CategoryID *int64
	CreatedAt  time.Time
}

type FilterAction struct {
	ID          int64
	FilterID    int64
	ActionType  string
	ActionParam string
	Priority    int
	CreatedAt   time.Time
}

const (
	FilterActionLabel   = "label"
	FilterActionWebhook = "webhook"
	FilterActionDelete  = "delete"
)

type Label struct {
	ID        int64
	UserID    int64
	Caption   string
	FgColor   string
	BgColor   string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type FilterRule struct {
	ID        int64
	FilterID  int64
	Field     string
	Pattern   string
	Negate    bool
	Op        string
	Priority  int
	CreatedAt time.Time
}

type FilterMatch struct {
	ID        int64
	FilterID  int64
	EntryID   int64
	MatchedAt time.Time
	Details   []byte
}

type FilterMatchWithEntry struct {
	Match FilterMatch
	Entry Entry
}

type Webhook struct {
	ID             int64
	UserID         int64
	FilterID       *int64
	Name           string
	URL            string
	Method         string
	Headers        []byte
	BodyTemplate   string
	Secret         string
	Enabled        bool
	OnSuccessEntry string
	Kind           string
	ProviderConfig []byte
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type WebhookLog struct {
	ID              int64
	WebhookID       int64
	EntryID         int64
	Status          string
	Attempt         int
	NextRetryAt     *time.Time
	LastStatusCode  *int
	LastError       *string
	ResponseSnippet *string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type CategoryStore interface {
	CreateCategory(ctx context.Context, userID int64, title, color string) (Category, error)
	ListCategories(ctx context.Context, userID int64, limit, offset int) ([]Category, int, error)
	UpdateCategory(ctx context.Context, userID int64, id int64, title, color string) (Category, error)
	DeleteCategory(ctx context.Context, userID int64, id int64) error
	ReorderCategories(ctx context.Context, userID int64, ids []int64) error
}

type FeedStore interface {
	CreateFeed(ctx context.Context, userID int64, params CreateFeedParams) (Feed, error)
	GetFeed(ctx context.Context, userID int64, id int64) (Feed, error)
	GetFeedByID(ctx context.Context, id int64) (Feed, error) // internal/worker: no tenant check
	ListFeeds(ctx context.Context, userID int64, limit, offset int) ([]Feed, int, error)
	ListFeedsByCategory(ctx context.Context, userID, categoryID int64) ([]Feed, error)
	ListFeedsByCategoryPaginated(ctx context.Context, userID, categoryID int64, limit, offset int) ([]Feed, int, error)
	ListFeedsByStatus(ctx context.Context, userID int64, status string, limit, offset int) ([]Feed, int, error)
	SearchFeeds(ctx context.Context, userID int64, filter SearchFeedsFilter) ([]Feed, error)
	ListFeedsByIDs(ctx context.Context, userID int64, ids []int64) ([]Feed, error)
	FeedCountsByCategory(ctx context.Context, userID int64) (FeedCategoryCounts, error)
	CountFeedStatuses(ctx context.Context, userID int64) (errors, inactive int, err error)
	ListAllFeeds(ctx context.Context, limit int) ([]Feed, error)
	UpdateFeed(ctx context.Context, userID int64, params UpdateFeedParams) (Feed, error)
	UpdateFeedRefreshMeta(ctx context.Context, params UpdateFeedRefreshMetaParams) error
	UpdateFeedIcon(ctx context.Context, userID, feedID int64, iconURL string, iconData []byte) error
	SetFeedNextCheckAt(ctx context.Context, feedID int64, nextCheckAt time.Time) error
	RecordFeedPollFailure(ctx context.Context, feedID int64, errMsg string, threshold int, checkedAt time.Time) error
	ResetFeedPollCircuit(ctx context.Context, feedID int64) error
	ResetErrorFeedPollCircuits(ctx context.Context) (int64, error)
	SetFeedManualPaused(ctx context.Context, feedID int64, paused bool) error
	BulkUpdateFeedsByCategory(ctx context.Context, userID, categoryID int64, update BulkFeedUpdate) (feedIDs []int64, count int, err error)
	DeleteFeed(ctx context.Context, userID int64, id int64) error
}

type JobStore interface {
	EnqueuePollFeedJob(ctx context.Context, feedID int64, runAt time.Time) error
	EnqueuePollFeedJobs(ctx context.Context, feedIDs []int64, runAt time.Time) error
	EnqueueRefreshAllPollJobs(ctx context.Context) (feeds int, queued int, err error)
	ClaimDueJobs(ctx context.Context, limit int, lockedBy string) ([]Job, error)
	CompleteJob(ctx context.Context, jobID int64, lockedBy string) (deleted bool, err error)
	ReleaseJob(ctx context.Context, jobID int64, lockedBy string) error
	ReclaimStalePollJobs(ctx context.Context, instanceID string, staleAfter time.Duration) (int64, error)
	RescheduleJob(ctx context.Context, jobID int64, lockedBy string, runAt time.Time, lastError string) error
}

type EntryDedupStore interface {
	FilterKnownEntryHashes(ctx context.Context, feedID int64, hashes []string) (map[string]struct{}, error)
	RecordFeedEntryDedup(ctx context.Context, feedID int64, items []FeedEntryDedupParams) (int, error)
	StripEntryPayloadAfterWebhook(ctx context.Context, entryID int64) error
	// CollapseEntriesToHashes copies hashes into feed_entry_dedup and deletes
	// full entry rows (starred and pending-webhook entries are kept).
	CollapseEntriesToHashes(ctx context.Context, params CollapseEntriesParams) (int64, error)
}

// CollapseEntriesParams selects which entries to convert to hash-only storage.
// CategoryID 0 means uncategorized feeds; nil means all categories.
type CollapseEntriesParams struct {
	UserID     int64
	FeedID     *int64
	CategoryID *int64
	// OnlyHashOnlyFeeds skips feeds that still store full entries.
	OnlyHashOnlyFeeds bool
	// IncludeLabeled also converts entries that have labels (filter matches).
	IncludeLabeled bool
}

type EntryStore interface {
	CreateEntries(ctx context.Context, feedID int64, entries []CreateEntryParams) (inserted int, insertedEntries []Entry, err error)
	GetEntry(ctx context.Context, userID int64, id int64) (Entry, error)
	GetFeedEntry(ctx context.Context, userID, feedID, entryID int64) (Entry, error)
	GetEntryByID(ctx context.Context, id int64) (Entry, error) // internal/worker
	UpdateEntryContent(ctx context.Context, userID int64, params UpdateEntryContentParams) (Entry, error)
	UpdateEntry(ctx context.Context, userID, feedID, entryID int64, params UpdateEntryParams) (Entry, error)
	ListEntries(ctx context.Context, userID int64, filter ListEntriesFilter) ([]Entry, int, error)
	ListFeedEntries(ctx context.Context, userID int64, feedID int64, filter ListEntriesFilter) ([]Entry, int, error)
	SearchEntries(ctx context.Context, userID int64, filter SearchEntriesFilter) ([]Entry, int, error)
	ListEnclosuresByEntryIDs(ctx context.Context, userID int64, entryIDs []int64) (map[int64][]Enclosure, error)
	CountUnreadByFeed(ctx context.Context, feedID int64) (int, error)
	CountUnreadByCategory(ctx context.Context, categoryID int64) (int, error)
	CountUnreadGlobal(ctx context.Context) (int, error)
	CountUnreadGlobalForUser(ctx context.Context, userID int64) (int, error)
	UnreadCountsForUser(ctx context.Context, userID int64) (feedCounts map[int64]int, categoryCounts map[int64]int, err error)
	BulkUpdateEntries(ctx context.Context, userID int64, entryIDs []int64, update BulkEntryUpdate) (int, error)
	// MarkEntriesRemoved soft-deletes the user's entries (filter "delete" action).
	MarkEntriesRemoved(ctx context.Context, userID int64, entryIDs []int64) (int, error)
	MarkAllFeedEntriesRead(ctx context.Context, userID, feedID int64) (int, error)
	MarkAllCategoryEntriesRead(ctx context.Context, userID, categoryID int64) (int, error)
	MarkAllEntriesRead(ctx context.Context, userID int64) (int, error)
}

type UpdateEntryContentParams struct {
	ID              int64
	Content         string
	OriginalContent string
	ContentFetched  bool
}

type FilterStore interface {
	ListFilters(ctx context.Context, userID int64, limit, offset int) ([]Filter, int, error)
	CreateFilter(ctx context.Context, params CreateFilterParams) (Filter, error)
	GetFilter(ctx context.Context, userID int64, id int64) (Filter, error)
	UpdateFilter(ctx context.Context, params UpdateFilterParams) (Filter, error)
	DeleteFilter(ctx context.Context, userID int64, id int64) error

	// ListEnabledFilters returns enabled filters with rules for matching.
	ListEnabledFilters(ctx context.Context, userID int64, limit int) ([]Filter, error)
}

type LabelStore interface {
	ListLabels(ctx context.Context, userID int64, limit, offset int) ([]Label, int, error)
	CreateLabel(ctx context.Context, params CreateLabelParams) (Label, error)
	GetLabel(ctx context.Context, userID int64, id int64) (Label, error)
	UpdateLabel(ctx context.Context, params UpdateLabelParams) (Label, error)
	DeleteLabel(ctx context.Context, userID int64, id int64) error
	AssignEntryLabel(ctx context.Context, entryID, labelID int64) error
	EntryCountsByLabel(ctx context.Context, userID int64) (map[int64]int, error)
	UnreadCountsByLabel(ctx context.Context, userID int64) (map[int64]int, error)
}

type FilterMatchStore interface {
	CreateFilterMatches(ctx context.Context, matches []CreateFilterMatchParams) (int, error)
	IncrementFilterMatchCount(ctx context.Context, filterID int64, delta int64) error
	ListFilterMatches(ctx context.Context, filterID int64, limit, offset int) ([]FilterMatchWithEntry, int, error)
}

type WebhookStore interface {
	ListWebhooks(ctx context.Context, userID int64, limit, offset int) ([]Webhook, int, error)
	CreateWebhook(ctx context.Context, params CreateWebhookParams) (Webhook, error)
	GetWebhook(ctx context.Context, userID int64, id int64) (Webhook, error)
	UpdateWebhook(ctx context.Context, params UpdateWebhookParams) (Webhook, error)
	SetWebhookEnabled(ctx context.Context, userID, id int64, enabled bool) error
	DeleteWebhook(ctx context.Context, userID int64, id int64) error

	// ListEnabledWebhooks returns enabled webhooks for dispatch/enqueue.
	ListEnabledWebhooks(ctx context.Context, userID int64, limit int) ([]Webhook, error)
}

type WebhookLogStore interface {
	// EnqueueWebhookLogs inserts delivery rows idempotently.
	EnqueueWebhookLogs(ctx context.Context, webhookIDs []int64, entryID int64) error

	// ClaimDueWebhookLogs claims due rows for delivery.
	ClaimDueWebhookLogs(ctx context.Context, limit int) ([]WebhookLog, error)

	MarkWebhookLogSent(ctx context.Context, logID int64, attempt int, statusCode int, responseSnippet string) error
	MarkWebhookLogFailed(ctx context.Context, logID int64, statusCode *int, errMsg string, responseSnippet string, attempt int, nextRetryAt *time.Time, dead bool) error

	ListWebhookLogs(ctx context.Context, webhookID int64, limit, offset int) ([]WebhookLog, int, error)
	// RetryWebhookLogNow re-queues a failed delivery. The row must belong to a
	// webhook owned by userID and must not already be sent; otherwise ErrNotFound.
	RetryWebhookLogNow(ctx context.Context, userID, logID int64) error
}

type CreateFeedParams struct {
	FeedURL            string
	FeedType           string
	Title              string
	CategoryID         *int64
	IntervalMinutes    int
	ScraperRules       string
	RewriteRules       string
	BlockedRules       string
	KeepRules          string
	FetchViaProxy      bool
	TLSInsecure        bool
	Crawler            bool
	UserAgent          string
	WebhookID          *int64
	StoreHashOnly      bool
	EntryRetentionDays *int
	BridgeState        []byte
}

type UpdateFeedParams struct {
	ID                 int64
	FeedURL            string
	Title              string
	CategoryID         *int64
	IntervalMinutes    int
	ScraperRules       string
	RewriteRules       string
	BlockedRules       string
	KeepRules          string
	FetchViaProxy      bool
	TLSInsecure        bool
	Crawler            bool
	UserAgent          string
	WebhookID          *int64
	StoreHashOnly      bool
	EntryRetentionDays *int
	BridgeState        []byte
}

type UpdateFeedRefreshMetaParams struct {
	ID            int64
	ETag          string
	LastModified  string
	LastCheckedAt time.Time
	LastError     string
	BridgeState   []byte
}

type CreateEntryParams struct {
	Title       string
	URL         string
	Content     string
	Author      *string
	PublishedAt *time.Time
	Hash        string
	Status      string
	Enclosures  []CreateEnclosureParams
}

type CreateEnclosureParams struct {
	URL      string
	Size     int64
	MIMEType string
}

type BulkEntryUpdate struct {
	Status  *string
	Starred *bool
}

// BulkFeedUpdate applies optional field changes to all feeds in a category.
// WebhookSet=true with WebhookID=nil clears the feed webhook.
// MoveCategory=true with MoveToCategoryID=nil moves feeds to uncategorized.
type BulkFeedUpdate struct {
	IntervalMinutes  *int
	WebhookSet       bool
	WebhookID        *int64
	StoreHashOnly    *bool
	ManualPaused     *bool
	MoveCategory     bool
	MoveToCategoryID *int64
}

type UpdateEntryParams struct {
	Status  *string
	Starred *bool
}

type CreateFilterRuleParams struct {
	Field    string
	Pattern  string
	Negate   bool
	Op       string
	Priority int
}

type CreateFilterScopeItemParams struct {
	FeedID     *int64
	CategoryID *int64
}

type CreateFilterActionParams struct {
	ActionType  string
	ActionParam string
	Priority    int
}

type CreateFilterParams struct {
	UserID       int64
	Name         string
	Enabled      bool
	MatchAnyRule bool
	Inverse      bool
	OrderID      int
	FeedScope    string
	Rules        []CreateFilterRuleParams
	ScopeItems   []CreateFilterScopeItemParams
	Actions      []CreateFilterActionParams
}

type UpdateFilterParams struct {
	ID           int64
	UserID       int64
	Name         string
	Enabled      bool
	MatchAnyRule bool
	Inverse      bool
	OrderID      int
	FeedScope    string
	Rules        []CreateFilterRuleParams
	ScopeItems   []CreateFilterScopeItemParams
	Actions      []CreateFilterActionParams
}

type CreateLabelParams struct {
	UserID  int64
	Caption string
	FgColor string
	BgColor string
}

type UpdateLabelParams struct {
	ID      int64
	UserID  int64
	Caption string
	FgColor string
	BgColor string
}

type CreateFilterMatchParams struct {
	FilterID  int64
	EntryID   int64
	MatchedAt time.Time
	Details   []byte
}

type CreateWebhookParams struct {
	UserID         int64
	FilterID       *int64
	Name           string
	URL            string
	Method         string
	Headers        []byte
	BodyTemplate   string
	Secret         string
	Enabled        bool
	OnSuccessEntry string
	Kind           string
	ProviderConfig []byte
}

type UpdateWebhookParams struct {
	ID             int64
	UserID         int64
	FilterID       *int64
	Name           *string
	URL            string
	Method         string
	Headers        []byte
	BodyTemplate   string
	Secret         *string
	Enabled        bool
	OnSuccessEntry string
	Kind           string
	ProviderConfig []byte
}

type ListEntriesFilter struct {
	FeedID     *int64
	CategoryID *int64
	LabelID    *int64
	Status     *string
	Starred    *bool
	Sort       string
	Limit      int
	Offset     int
}
