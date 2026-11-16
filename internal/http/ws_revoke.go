package httpserver

import (
	"context"
	"time"

	"rssam/internal/storage"
	"rssam/internal/ws"
)

// revokingSessions closes the WebSockets of every session it deletes, so a
// logout or password change ends the live connection too, not only the
// next HTTP request. It wraps the store once; UI and API share it.
type revokingSessions struct {
	storage.SessionStore
	users storage.UserStore
	hub   *ws.Hub
}

// LookupSessionUser keeps the single-query path of the wrapped store.
func (r revokingSessions) LookupSessionUser(ctx context.Context, sessionID string) (storage.Session, storage.User, error) {
	return storage.LookupSessionUser(ctx, r.SessionStore, r.users, sessionID)
}

func (r revokingSessions) DeleteSession(ctx context.Context, sessionID string) error {
	err := r.SessionStore.DeleteSession(ctx, sessionID)
	if err == nil {
		r.hub.KickSession(sessionID)
	}
	return err
}

func (r revokingSessions) DeleteUserSessions(ctx context.Context, userID int64) error {
	err := r.SessionStore.DeleteUserSessions(ctx, userID)
	if err == nil {
		r.hub.KickUser(userID, "")
	}
	return err
}

func (r revokingSessions) DeleteUserSessionsExcept(ctx context.Context, userID int64, keepSessionID string) error {
	err := r.SessionStore.DeleteUserSessionsExcept(ctx, userID, keepSessionID)
	if err == nil {
		r.hub.KickUser(userID, keepSessionID)
	}
	return err
}

type revokingUsers struct {
	storage.UserStore
	hub *ws.Hub
}

// APIKeyExpiry forwards to the wrapped store; a store without it never
// expires keys early (nil, nil).
func (r revokingUsers) APIKeyExpiry(ctx context.Context, keyID int64) (*time.Time, error) {
	if ks, ok := r.UserStore.(apiKeyExpiryStore); ok {
		return ks.APIKeyExpiry(ctx, keyID)
	}
	return nil, nil
}

func (r revokingUsers) DeleteUser(ctx context.Context, id int64) error {
	err := r.UserStore.DeleteUser(ctx, id)
	if err == nil {
		r.hub.KickUser(id, "")
	}
	return err
}

func (r revokingUsers) DeleteAPIKey(ctx context.Context, userID, keyID int64) error {
	err := r.UserStore.DeleteAPIKey(ctx, userID, keyID)
	if err == nil {
		r.hub.KickKey(keyID)
	}
	return err
}
