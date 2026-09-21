package auth

import "context"

type ctxKey int

const userKey ctxKey = 1

// Principal holds authenticated user identity for a request.
type Principal struct {
	UserID  int64
	IsAdmin bool
	// Username is filled by the session/token authenticators (the user row
	// was already loaded there) so page rendering does not re-query it.
	Username string
	// Scope is the API key scope ("" for sessions = unrestricted).
	Scope string
}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, userKey, p)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(userKey).(Principal)
	return p, ok
}
