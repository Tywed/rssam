package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"rssam/internal/auth"
)

var (
	ErrDuplicateUsername = errors.New("username already exists")
	ErrDuplicateAPIKey   = errors.New("api key name conflict")
	ErrInvalidRole       = errors.New("invalid role")
)

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

func (u User) IsAdmin() bool { return u.Role == auth.RoleAdmin }

const userColumns = `id, username, password_hash, role, created_at`

func scanUser(row pgx.Row, u *User, extra ...any) error {
	return row.Scan(append([]any{&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.CreatedAt}, extra...)...)
}

type APIKey struct {
	ID         int64
	UserID     int64
	Name       string
	TokenHash  string
	Scope      string
	ExpiresAt  *time.Time
	LastUsedAt *time.Time
	CreatedAt  time.Time
}

// Expired reports whether the key can no longer authenticate at now.
func (k APIKey) Expired(now time.Time) bool {
	return k.ExpiresAt != nil && !k.ExpiresAt.After(now)
}

type APIKeyWithToken struct {
	APIKey
	Token string // plain token, only on create
}

type CreateUserParams struct {
	Username     string
	PasswordHash string
	// Role defaults to reader.
	Role string
}

type UpdateUserParams struct {
	ID           int64
	PasswordHash *string
	Role         *string
}

type CreateAPIKeyParams struct {
	UserID    int64
	Name      string
	TokenHash string
	Scope     string
	ExpiresAt *time.Time
}

type UserStore interface {
	CountUsers(ctx context.Context) (int, error)
	// CountLoginCapableUsers counts users that have a password set and can
	// actually sign in. Migration 0009 seeds a placeholder user "default" with
	// an empty password_hash, which must not count as a bootstrapped admin.
	CountLoginCapableUsers(ctx context.Context) (int, error)
	ListUsers(ctx context.Context, limit, offset int) ([]User, int, error)
	GetUser(ctx context.Context, id int64) (User, error)
	GetUserByUsername(ctx context.Context, username string) (User, error)
	CreateUser(ctx context.Context, params CreateUserParams) (User, error)
	// UpdateUser changes the password and/or the role; nil fields are left
	// alone. Demoting the only login-capable admin returns ErrLastAdmin.
	UpdateUser(ctx context.Context, params UpdateUserParams) (User, error)
	// DeleteUser returns ErrLastAdmin when the user is the only admin that
	// can log in (role admin with a non-empty password_hash).
	DeleteUser(ctx context.Context, id int64) error

	LookupAPIKey(ctx context.Context, tokenHash string) (APIKey, error)
	TouchAPIKeyUsed(ctx context.Context, keyID int64) error
	ListAPIKeys(ctx context.Context, userID int64) ([]APIKey, error)
	CreateAPIKey(ctx context.Context, params CreateAPIKeyParams) (APIKey, error)
	DeleteAPIKey(ctx context.Context, userID, keyID int64) error
}

func (s *PostgresStore) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

func (s *PostgresStore) CountLoginCapableUsers(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM users WHERE password_hash <> ''`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count login-capable users: %w", err)
	}
	return n, nil
}

func (s *PostgresStore) ListUsers(ctx context.Context, limit, offset int) ([]User, int, error) {
	if limit <= 0 {
		limit = 100
	}
	const q = `
SELECT ` + userColumns + `, count(*) OVER()
FROM users
ORDER BY id ASC
LIMIT $1 OFFSET $2`
	rows, err := s.db.Query(ctx, q, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	out := make([]User, 0, limit)
	total := 0
	for rows.Next() {
		var u User
		if err := scanUser(rows, &u, &total); err != nil {
			return nil, 0, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate users: %w", err)
	}
	return out, total, nil
}

func (s *PostgresStore) GetUser(ctx context.Context, id int64) (User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE id = $1`
	var u User
	err := scanUser(s.db.QueryRow(ctx, q, id), &u)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

func (s *PostgresStore) GetUserByUsername(ctx context.Context, username string) (User, error) {
	const q = `SELECT ` + userColumns + ` FROM users WHERE username = $1`
	var u User
	err := scanUser(s.db.QueryRow(ctx, q, strings.TrimSpace(username)), &u)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, fmt.Errorf("get user by username: %w", err)
	}
	return u, nil
}

func (s *PostgresStore) CreateUser(ctx context.Context, params CreateUserParams) (User, error) {
	username := strings.TrimSpace(params.Username)
	if username == "" {
		return User{}, errors.New("username is required")
	}
	role := params.Role
	if role == "" {
		role = auth.RoleReader
	}
	if !auth.IsValidRole(role) {
		return User{}, ErrInvalidRole
	}
	const q = `
INSERT INTO users(username, password_hash, role)
VALUES ($1, $2, $3)
RETURNING ` + userColumns
	var u User
	err := scanUser(s.db.QueryRow(ctx, q, username, params.PasswordHash, role), &u)
	if err != nil {
		if isDuplicateUsername(err) {
			return User{}, ErrDuplicateUsername
		}
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

func (s *PostgresStore) UpdateUser(ctx context.Context, params UpdateUserParams) (User, error) {
	if params.PasswordHash == nil && params.Role == nil {
		return s.GetUser(ctx, params.ID)
	}
	if params.Role != nil && !auth.IsValidRole(*params.Role) {
		return User{}, ErrInvalidRole
	}
	var u User
	err := withTx(ctx, s.db, func(tx pgx.Tx) error {
		if params.Role != nil && *params.Role != auth.RoleAdmin {
			if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userAdminLockKey); err != nil {
				return fmt.Errorf("update user: lock: %w", err)
			}
			last, err := isLastLoginCapableAdmin(ctx, tx, params.ID)
			if err != nil {
				return err
			}
			if last {
				return ErrLastAdmin
			}
		}
		const q = `
UPDATE users
SET password_hash = COALESCE($2, password_hash),
    role = COALESCE($3, role)
WHERE id = $1
RETURNING ` + userColumns
		err := scanUser(tx.QueryRow(ctx, q, params.ID, params.PasswordHash, params.Role), &u)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("update user: %w", err)
		}
		return nil
	})
	if err != nil {
		return User{}, err
	}
	return u, nil
}

// userAdminLockKey serialises the admin-count checks in DeleteUser and
// UpdateUser so two concurrent changes cannot each see "another admin still
// exists".
const userAdminLockKey int64 = 0x7273616d5f61646d // "rsam_adm"

// isLastLoginCapableAdmin reports whether id is an admin with a password
// and no other such admin exists. Callers hold userAdminLockKey.
func isLastLoginCapableAdmin(ctx context.Context, tx pgx.Tx, id int64) (bool, error) {
	var role, hash string
	err := tx.QueryRow(ctx, `SELECT role, password_hash FROM users WHERE id = $1 FOR UPDATE`, id).Scan(&role, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, fmt.Errorf("load user: %w", err)
	}
	if role != auth.RoleAdmin || hash == "" {
		return false, nil
	}
	var others int
	err = tx.QueryRow(ctx, `
SELECT count(*) FROM users
WHERE role = 'admin' AND password_hash <> '' AND id <> $1`, id).Scan(&others)
	if err != nil {
		return false, fmt.Errorf("count admins: %w", err)
	}
	return others == 0, nil
}

// DeleteUser removes a user and everything owned by it (ON DELETE CASCADE).
// Deleting the last login-capable admin is refused with ErrLastAdmin:
// without one nobody could administer the instance through the UI anymore.
func (s *PostgresStore) DeleteUser(ctx context.Context, id int64) error {
	return withTx(ctx, s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, userAdminLockKey); err != nil {
			return fmt.Errorf("delete user: lock: %w", err)
		}
		last, err := isLastLoginCapableAdmin(ctx, tx, id)
		if err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		if last {
			return ErrLastAdmin
		}
		if _, err := tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id); err != nil {
			return fmt.Errorf("delete user: %w", err)
		}
		return nil
	})
}

func (s *PostgresStore) LookupAPIKey(ctx context.Context, tokenHash string) (APIKey, error) {
	const q = `
SELECT id, user_id, name, token_hash, scope, expires_at, last_used_at, created_at
FROM api_keys
WHERE token_hash = $1`
	var k APIKey
	err := s.db.QueryRow(ctx, q, tokenHash).Scan(&k.ID, &k.UserID, &k.Name, &k.TokenHash, &k.Scope, &k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIKey{}, ErrNotFound
		}
		return APIKey{}, fmt.Errorf("lookup api key: %w", err)
	}
	return k, nil
}

// TouchAPIKeyUsed records use of a key. last_used_at is informational, so
// it is written at most once per hour per key rather than on every request.
func (s *PostgresStore) TouchAPIKeyUsed(ctx context.Context, keyID int64) error {
	_, err := s.db.Exec(ctx, `UPDATE api_keys SET last_used_at = now()
WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 hour')`, keyID)
	if err != nil {
		return fmt.Errorf("touch api key: %w", err)
	}
	return nil
}

func (s *PostgresStore) ListAPIKeys(ctx context.Context, userID int64) ([]APIKey, error) {
	const q = `
SELECT id, user_id, name, token_hash, scope, expires_at, last_used_at, created_at
FROM api_keys
WHERE user_id = $1
ORDER BY id DESC`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list api keys: %w", err)
	}
	defer rows.Close()

	var out []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.UserID, &k.Name, &k.TokenHash, &k.Scope, &k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan api key: %w", err)
		}
		out = append(out, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate api keys: %w", err)
	}
	return out, nil
}

func (s *PostgresStore) CreateAPIKey(ctx context.Context, params CreateAPIKeyParams) (APIKey, error) {
	scope := params.Scope
	if scope == "" {
		scope = "admin"
	}
	const q = `
INSERT INTO api_keys(user_id, name, token_hash, scope, expires_at)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, user_id, name, token_hash, scope, expires_at, last_used_at, created_at`
	var k APIKey
	err := s.db.QueryRow(ctx, q, params.UserID, strings.TrimSpace(params.Name), params.TokenHash, scope, params.ExpiresAt).
		Scan(&k.ID, &k.UserID, &k.Name, &k.TokenHash, &k.Scope, &k.ExpiresAt, &k.LastUsedAt, &k.CreatedAt)
	if err != nil {
		return APIKey{}, fmt.Errorf("create api key: %w", err)
	}
	return k, nil
}

func (s *PostgresStore) DeleteAPIKey(ctx context.Context, userID, keyID int64) error {
	cmd, err := s.db.Exec(ctx, `DELETE FROM api_keys WHERE id = $1 AND user_id = $2`, keyID, userID)
	if err != nil {
		return fmt.Errorf("delete api key: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func isDuplicateUsername(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "username")
}

// EnsureBootstrapAdmin creates the first admin when no user can sign in yet.
//
// Migration 0009 seeds users(id=1, username=default) with an empty
// password_hash. That row must not count as a bootstrapped admin, otherwise
// ADMIN_USERNAME/ADMIN_PASSWORD are ignored and UI login fails.
//
// When the only rows are password-less placeholders, the env admin is written
// onto id=1 (rename + password) so the data already owned by id=1 stays
// visible. A password-less row that already has the requested username is
// adopted in place. Only if neither exists is a new user inserted.
func (s *PostgresStore) EnsureBootstrapAdmin(ctx context.Context, username, password string) error {
	username = strings.TrimSpace(username)
	password = strings.TrimSpace(password)
	if username == "" || password == "" {
		return nil
	}
	n, err := s.CountLoginCapableUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	existing, err := s.GetUserByUsername(ctx, username)
	if err == nil && existing.PasswordHash == "" {
		return s.adoptPlaceholderAdmin(ctx, existing.ID, username, hash)
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	u, err := s.GetUser(ctx, 1)
	if err == nil && u.PasswordHash == "" {
		return s.adoptPlaceholderAdmin(ctx, u.ID, username, hash)
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	_, err = s.CreateUser(ctx, CreateUserParams{
		Username:     username,
		PasswordHash: hash,
		Role:         auth.RoleAdmin,
	})
	if errors.Is(err, ErrDuplicateUsername) {
		return nil
	}
	return err
}

func (s *PostgresStore) adoptPlaceholderAdmin(ctx context.Context, id int64, username, hash string) error {
	const q = `
UPDATE users
SET username = $2, password_hash = $3, role = 'admin'
WHERE id = $1 AND password_hash = ''`
	if _, err := s.db.Exec(ctx, q, id, username, hash); err != nil {
		return fmt.Errorf("bootstrap admin: adopt placeholder user: %w", err)
	}
	return nil
}
