package auth

import "context"

type ctxKey int

const userKey ctxKey = 1

// Principal holds authenticated user identity for a request.
type Principal struct {
	UserID  int64
	IsAdmin bool
}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, userKey, p)
}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(userKey).(Principal)
	return p, ok
}

func UserIDFromContext(ctx context.Context) (int64, bool) {
	p, ok := PrincipalFromContext(ctx)
	if !ok || p.UserID <= 0 {
		return 0, false
	}
	return p.UserID, true
}
