package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/admin"
	"github.com/ItsMeD4N/IF3110-07/server/internal/approval"
	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/auth/loginpage"
	"github.com/ItsMeD4N/IF3110-07/server/internal/csrf"
	"github.com/ItsMeD4N/IF3110-07/server/internal/dblock"
	"github.com/ItsMeD4N/IF3110-07/server/internal/dbmanager"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/metrics"
	"github.com/ItsMeD4N/IF3110-07/server/internal/migration"
	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
	"github.com/ItsMeD4N/IF3110-07/server/internal/user"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

type Options struct {
	AppEnv            string
	ClientDir         string
	SPADir            string
	ServeSPA          bool
	EncryptionKey     []byte
	DatabaseDSN       string
	WorkerConcurrency int
	DisableWorker     bool
	RequireApproval   bool
	ApprovalChecker   migration.ApprovalChecker
	PollInterval      time.Duration
	LockTTL           time.Duration
	DisableLegacySync bool
	EnableMetrics     bool
	APIOnly           bool
}

type App struct {
	Handler    http.Handler
	Users      auth.UserRepository
	Sessions   *session.Manager
	Engine     *migration.Engine
	Approvals  *approval.Service
	Broker     queue.Broker
	Locker     dblock.Locker
	WorkerPool *queue.WorkerPool
	admin      *admin.Module
}

func New(db *sql.DB, opt Options) (*App, error) {
	isProd := opt.AppEnv == "production"

	spaDir := opt.SPADir
	if opt.ServeSPA && spaDir == "" {
		spaDir = web.FindSPADir(opt.ClientDir)
	}
	useSPA := opt.ServeSPA && spaDir != ""
	apiOnly := opt.APIOnly && !useSPA

	var views *web.Views
	if !useSPA && !apiOnly {
		var err error
		views, err = web.New(opt.ClientDir, !isProd)
		if err != nil {
			return nil, err
		}
	}

	users := auth.NewUserRepository(db)
	sessions := session.NewManager(session.NewSessionRepository(db), isProd)

	adminMod, err := admin.New(db, users, sessions, admin.Options{AppEnv: opt.AppEnv, Views: views, EncryptionKey: opt.EncryptionKey})
	if err != nil {
		return nil, err
	}
	adminMod.OnPrivilegeChange = func(ctx context.Context, userIDs []string, reason string) {
		if err := sessions.RequireRotation(ctx, userIDs); err != nil {
			telemetry.LogEvent(ctx, slog.LevelWarn, "admin.session_rotation_failed",
				slog.String("reason", reason),
				slog.String("error", err.Error()),
				slog.String("status", "warning"),
			)
		}
	}

	locker := dblock.NewPostgresLocker(db)
	broker := queue.NewPostgresBroker(db, opt.DatabaseDSN)
	engine := migration.NewEngine(db, adminMod.Conns, locker, broker, migration.EngineOptions{
		ApprovalChecker: opt.ApprovalChecker,
		RequireApproval: opt.RequireApproval,
		LockTTL:         opt.LockTTL,
	})

	approvals := approval.NewService(db, engine, adminMod.Store, opt.RequireApproval)
	engine.Workflow = approvals
	if opt.ApprovalChecker == nil && opt.RequireApproval {
		engine.ApprovalChecker = approvals
	}

	var pool *queue.WorkerPool
	if !opt.DisableWorker {
		concurrency := opt.WorkerConcurrency
		if concurrency <= 0 {
			concurrency = 4
		}
		pollInterval := opt.PollInterval
		if pollInterval <= 0 {
			if opt.AppEnv == "test" {
				pollInterval = 25 * time.Millisecond
			} else {
				pollInterval = 150 * time.Millisecond
			}
		}
		poolOpts := queue.PoolOptions{
			Concurrency:  concurrency,
			PollInterval: pollInterval,
		}
		if opt.AppEnv == "test" {
			poolOpts.Backoff = queue.BackoffPolicy{
				BaseDelay:   30 * time.Millisecond,
				MaxDelay:    250 * time.Millisecond,
				MaxAttempts: 3,
				JitterRatio: 0.1,
			}
			poolOpts.ContentionDelay = 30 * time.Millisecond
			poolOpts.ReclaimInterval = 200 * time.Millisecond
		}
		pool = queue.NewWorkerPool(broker, engine, poolOpts)
		pool.Start(context.Background())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "healthy", "timestamp": time.Now().Format(time.RFC3339)})
	})

	if opt.EnableMetrics {
		metrics.RegisterDBPool("platform", db.Stats)
		metrics.RegisterDBPool("target", adminMod.Conns.Stats)
		metrics.RegisterJobStatusGauge(db)
		mux.Handle("GET /metrics", metrics.Handler())
	}

	auth.RegisterRoutes(mux, users, sessions)
	user.RegisterRoutes(mux, users, sessions)
	csrf.RegisterRoutes(mux)
	adminMod.RegisterRoutes(mux)
	dbmanager.New(adminMod.Conns, adminMod.Authz, users, sessions, views).RegisterRoutes(mux)

	migMod := migration.New(db, adminMod.Conns, adminMod.Authz, users, sessions, views)
	migMod.Engine = engine
	migMod.DisableLegacySync = opt.DisableLegacySync
	migMod.RegisterRoutes(mux)

	(&approval.Handler{Svc: approvals, Engine: engine, MW: migMod.MW, AuthMW: migMod.AuthMW}).RegisterRoutes(mux)

	if useSPA {
		mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
			httpx.NotFound(w)
		})
		mux.Handle("/", web.SPAHandler(spaDir))
	} else if apiOnly {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/api/") {
				httpx.NotFound(w)
				return
			}
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("SPA belum di-build. Jalankan `npm install && npm run build` di folder client lalu restart server, atau buka Vite dev server (npm run dev) di http://localhost:5173.\n"))
		})
	} else {
		mux.Handle("GET /static/", web.StaticHandler(opt.ClientDir))
		loginpage.New(users, sessions, views).RegisterRoutes(mux)
	}

	var h http.Handler = mux
	h = securityHeaders(h)
	h = csrf.Middleware(isProd)(h)
	h = telemetry.HTTPMiddleware(func(ctx context.Context) string {
		if u := auth.CurrentUser(ctx); u != nil {
			return u.Username
		}
		return ""
	})(h)
	if opt.EnableMetrics {
		h = metrics.HTTPMiddleware(h)
	}

	return &App{
		Handler:    h,
		Users:      users,
		Sessions:   sessions,
		Engine:     engine,
		Approvals:  approvals,
		Broker:     broker,
		Locker:     locker,
		WorkerPool: pool,
		admin:      adminMod,
	}, nil
}

func (a *App) Close() {
	if a.WorkerPool != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = a.WorkerPool.Stop(ctx)
		cancel()
	}
	if a.Broker != nil {
		_ = a.Broker.Close()
	}
	if a.admin != nil {
		a.admin.Close()
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}
