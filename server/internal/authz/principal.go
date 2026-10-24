package authz

import (
	"context"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
)

type Principal struct {
	ID              string
	Username        string
	IsPlatformAdmin bool
}

func PrincipalFrom(ctx context.Context) *Principal {
	u := auth.CurrentUser(ctx)
	if u == nil || !u.IsActive {
		return nil
	}
	return &Principal{ID: u.ID, Username: u.Username, IsPlatformAdmin: u.IsPlatformAdmin}
}

type ctxKey int

const dbAccessKey ctxKey = iota

type DBAccess struct {
	DatabaseID int64
	Level      AccessLevel
}

func (a DBAccess) Can(p Permission) bool { return LevelAllows(a.Level, p) }

func withDBAccess(ctx context.Context, a DBAccess) context.Context {
	return context.WithValue(ctx, dbAccessKey, a)
}

func DBAccessFrom(ctx context.Context) (DBAccess, bool) {
	a, ok := ctx.Value(dbAccessKey).(DBAccess)
	return a, ok
}
