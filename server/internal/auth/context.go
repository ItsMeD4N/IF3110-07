package auth

import "context"

type contextKey string

const userContextKey contextKey = "current_user"

func CurrentUser(ctx context.Context) *User {
	if u, ok := ctx.Value(userContextKey).(*User); ok {
		return u
	}
	return nil
}

func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}
