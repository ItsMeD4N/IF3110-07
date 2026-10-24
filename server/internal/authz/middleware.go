package authz

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
)

const DatabaseIDPathParam = "dbID"

type Middleware struct {
	az     *Authorizer
	denied DeniedHandler
}

type DeniedHandler func(w http.ResponseWriter, r *http.Request, err error)

func NewMiddleware(az *Authorizer) *Middleware { return &Middleware{az: az, denied: WriteDenied} }

func (m *Middleware) WithDeniedHandler(f DeniedHandler) *Middleware {
	return &Middleware{az: m.az, denied: f}
}

func (m *Middleware) Authorizer() *Authorizer { return m.az }

func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if PrincipalFrom(r.Context()) == nil {
			m.denied(w, r, ErrUnauthenticated)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) Require(perm Permission, next http.Handler) http.Handler {
	if !IsPlatformPermission(perm) {
		panic("authz: Require hanya untuk permission platform; gunakan RequireDB untuk " + string(perm))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		err := m.az.Check(r.Context(), PrincipalFrom(r.Context()), perm, Platform)
		if err != nil {
			m.denied(w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) RequireDB(perm Permission, next http.Handler) http.Handler {
	if !IsDatabasePermission(perm) {
		panic("authz: RequireDB hanya untuk permission database; gunakan Require untuk " + string(perm))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := PrincipalFrom(r.Context())
		if p == nil {
			m.denied(w, r, ErrUnauthenticated)
			return
		}
		id, err := strconv.ParseInt(r.PathValue(DatabaseIDPathParam), 10, 64)
		if err != nil || id <= 0 {
			m.denied(w, r, ErrNotFound)
			return
		}
		level, err := m.az.check(r.Context(), p, perm, Database(id))
		if err != nil {
			m.denied(w, r, err)
			return
		}
		ctx := withDBAccess(r.Context(), DBAccess{DatabaseID: id, Level: level})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func StatusFor(err error) int {
	switch {
	case err == nil:
		return http.StatusOK
	case errors.Is(err, ErrUnauthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func WriteDenied(w http.ResponseWriter, r *http.Request, err error) {
	switch StatusFor(err) {
	case http.StatusUnauthorized:
		httpx.Unauthenticated(w)
	case http.StatusForbidden:
		httpx.Forbidden(w)
	case http.StatusNotFound:
		httpx.NotFound(w)
	default:
		httpx.Internal(w, r, err)
	}
}
