package authz

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrUnauthenticated = errors.New("authz: unauthenticated")
	ErrForbidden       = errors.New("authz: forbidden")
	ErrNotFound        = errors.New("authz: not found")
)

type AccessSource interface {
	DatabaseExists(ctx context.Context, databaseID int64) (bool, error)

	GroupAccessLevels(ctx context.Context, userID string, databaseID int64) ([]AccessLevel, error)
}

type Resource struct {
	DatabaseID int64
}

var Platform = Resource{}

func Database(id int64) Resource { return Resource{DatabaseID: id} }

type Authorizer struct {
	src AccessSource
}

func New(src AccessSource) *Authorizer { return &Authorizer{src: src} }

func (a *Authorizer) EffectiveAccess(ctx context.Context, p *Principal, databaseID int64) (AccessLevel, error) {
	if p == nil {
		return LevelNone, nil
	}
	levels, err := a.src.GroupAccessLevels(ctx, p.ID, databaseID)
	if err != nil {
		return LevelNone, fmt.Errorf("authz: load group access: %w", err)
	}
	return EffectiveLevel(levels...), nil
}

func (a *Authorizer) Check(ctx context.Context, p *Principal, perm Permission, res Resource) error {
	_, err := a.check(ctx, p, perm, res)
	return err
}

func (a *Authorizer) Can(ctx context.Context, p *Principal, perm Permission, res Resource) (bool, error) {
	err := a.Check(ctx, p, perm, res)
	switch {
	case err == nil:
		return true, nil
	case IsDenied(err):
		return false, nil
	default:
		return false, err
	}
}

func (a *Authorizer) check(ctx context.Context, p *Principal, perm Permission, res Resource) (AccessLevel, error) {
	if p == nil {
		return LevelNone, ErrUnauthenticated
	}

	switch {
	case IsPlatformPermission(perm):
		if p.IsPlatformAdmin {
			return LevelNone, nil
		}
		return LevelNone, ErrForbidden

	case IsDatabasePermission(perm):
		if res.DatabaseID <= 0 {
			return LevelNone, ErrNotFound
		}
		exists, err := a.src.DatabaseExists(ctx, res.DatabaseID)
		if err != nil {
			return LevelNone, fmt.Errorf("authz: check database: %w", err)
		}
		if !exists {
			return LevelNone, ErrNotFound
		}
		level, err := a.EffectiveAccess(ctx, p, res.DatabaseID)
		if err != nil {
			return LevelNone, err
		}
		if level == LevelNone {

			return LevelNone, ErrNotFound
		}
		if !LevelAllows(level, perm) {
			return level, ErrForbidden
		}
		return level, nil

	default:

		return LevelNone, ErrForbidden
	}
}

func IsDenied(err error) bool {
	return errors.Is(err, ErrUnauthenticated) || errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound)
}
