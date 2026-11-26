package admin

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/secretbox"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/store"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

type PrivilegeChangeFunc func(ctx context.Context, userIDs []string, reason string)

type Module struct {
	Store    *store.Store
	Conns    *connmgr.Manager
	Authz    *authz.Authorizer
	MW       *authz.Middleware
	Users    auth.UserRepository
	Sessions *session.Manager
	Views    *web.Views

	OnPrivilegeChange PrivilegeChangeFunc
}

type Options struct {
	EncryptionKey []byte
	AppEnv        string
	Views         *web.Views
}

func New(db *sql.DB, users auth.UserRepository, sessions *session.Manager, opt Options) (*Module, error) {
	key := opt.EncryptionKey
	if key == nil {
		if raw := os.Getenv("APP_ENCRYPTION_KEY"); raw != "" {
			k, err := secretbox.ParseKey(raw)
			if err != nil {
				return nil, err
			}
			key = k
		} else if opt.AppEnv == "production" {
			return nil, fmt.Errorf("APP_ENCRYPTION_KEY wajib di-set di production (openssl rand -base64 32)")
		} else {
			log.Println("[Admin] APP_ENCRYPTION_KEY kosong; memakai kunci development (JANGAN untuk production)")
			key = secretbox.DevKey()
		}
	}
	box, err := secretbox.New(key)
	if err != nil {
		return nil, err
	}
	fp, err := connmgr.Fingerprint(context.Background(), db)
	if err != nil {
		return nil, fmt.Errorf("admin: identifikasi database platform: %w", err)
	}
	st := store.New(db, box)
	az := authz.New(st)
	return &Module{
		Store:    st,
		Conns:    connmgr.New(st, connmgr.Options{PlatformFingerprint: fp}),
		Authz:    az,
		MW:       authz.NewMiddleware(az),
		Users:    users,
		Sessions: sessions,
		Views:    opt.Views,
	}, nil
}

func (m *Module) Close() { m.Conns.Close() }

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	m.registerAPI(mux)
	if m.Views != nil {
		m.registerPages(mux)
	}
}

func (m *Module) requireAuth(h http.Handler) http.Handler {
	return auth.RequireAuth(m.Users, m.Sessions)(h)
}

func (m *Module) privilegeChanged(ctx context.Context, userIDs []string, reason string) {
	if m.OnPrivilegeChange == nil || len(userIDs) == 0 {
		return
	}
	m.OnPrivilegeChange(ctx, userIDs, reason)
}
