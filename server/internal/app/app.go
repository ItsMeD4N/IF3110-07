package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/admin"
	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/auth/loginpage"
	"github.com/ItsMeD4N/IF3110-07/server/internal/csrf"
	"github.com/ItsMeD4N/IF3110-07/server/internal/dbmanager"
	"github.com/ItsMeD4N/IF3110-07/server/internal/migration"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/user"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

type Options struct {
	AppEnv        string
	ClientDir     string
	EncryptionKey []byte
}

type App struct {
	Handler  http.Handler
	Users    auth.UserRepository
	Sessions *session.Manager
	admin    *admin.Module
}

func New(db *sql.DB, opt Options) (*App, error) {
	isProd := opt.AppEnv == "production"

	views, err := web.New(opt.ClientDir, !isProd)
	if err != nil {
		return nil, err
	}

	users := auth.NewUserRepository(db)
	sessions := session.NewManager(session.NewSessionRepository(db), isProd)

	adminMod, err := admin.New(db, users, sessions, admin.Options{AppEnv: opt.AppEnv, Views: views, EncryptionKey: opt.EncryptionKey})
	if err != nil {
		return nil, err
	}
	adminMod.OnPrivilegeChange = func(ctx context.Context, userIDs []string, reason string) {
		if err := sessions.RequireRotation(ctx, userIDs); err != nil {
			log.Printf("[Admin] gagal menandai rotasi session (%s): %v", reason, err)
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "healthy", "timestamp": time.Now().Format(time.RFC3339)})
	})
	mux.Handle("GET /static/", web.StaticHandler(opt.ClientDir))

	auth.RegisterRoutes(mux, users, sessions)
	user.RegisterRoutes(mux, users, sessions)
	csrf.RegisterRoutes(mux)
	loginpage.New(users, sessions, views).RegisterRoutes(mux)
	adminMod.RegisterRoutes(mux)
	dbmanager.New(adminMod.Conns, adminMod.Authz, users, sessions, views).RegisterRoutes(mux)
	migration.New(db, adminMod.Conns, adminMod.Authz, users, sessions, views).RegisterRoutes(mux)

	var h http.Handler = mux
	h = securityHeaders(h)
	h = csrf.Middleware(isProd)(h)
	h = logRequests(h)

	return &App{Handler: h, Users: users, Sessions: sessions, admin: adminMod}, nil
}

func (a *App) Close() { a.admin.Close() }

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %v", r.Method, r.URL.Path, time.Since(start))
	})
}
