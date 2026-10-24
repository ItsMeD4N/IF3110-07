package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/admin"
	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/auth/loginpage"
	"github.com/ItsMeD4N/IF3110-07/server/internal/config"
	"github.com/ItsMeD4N/IF3110-07/server/internal/csrf"
	"github.com/ItsMeD4N/IF3110-07/server/internal/database"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/user"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
	"github.com/ItsMeD4N/IF3110-07/server/migrations"
)

func main() {
	cfg := config.LoadConfig()
	isProd := cfg.AppEnv == "production"

	// 1. Inisialisasi Pool Database PostgreSQL
	db, err := database.InitDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("[Database] Gagal menghubungkan ke database: %v", err)
	}
	defer db.Close()
	log.Println("[Database] Berhasil terhubung ke PostgreSQL platform_db")

	if err := migrations.Apply(context.Background(), db); err != nil {
		log.Fatalf("[Database] Gagal menjalankan migrasi: %v", err)
	}

	mux := http.NewServeMux()

	// 2. Health check & status endpoint
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "healthy",
			"timestamp": time.Now().Format(time.RFC3339),
			"version":   "1.0.0",
		})
	})

	// Inisialisasi Repository & Manager [Orang A]
	userRepo := auth.NewUserRepository(db)
	sessionRepo := session.NewSessionRepository(db)
	sessionMgr := session.NewManager(sessionRepo, isProd)

	// 3. Registrasi Rute Per Modul
	// [Orang A] Modul Identity & Session
	auth.RegisterRoutes(mux, userRepo, sessionMgr)
	user.RegisterRoutes(mux, userRepo, sessionMgr)
	csrf.RegisterRoutes(mux)

	clientDir, err := web.FindClientDir()
	if err != nil {
		log.Fatalf("[Web] %v", err)
	}
	views, err := web.New(clientDir, !isProd)
	if err != nil {
		log.Fatalf("[Web] Gagal memuat template: %v", err)
	}
	mux.Handle("GET /static/", web.StaticHandler(clientDir))

	loginpage.New(userRepo, sessionMgr, views).RegisterRoutes(mux)

	adminMod, err := admin.New(db, userRepo, sessionMgr, admin.Options{AppEnv: cfg.AppEnv, Views: views})
	if err != nil {
		log.Fatalf("[Admin] %v", err)
	}
	defer adminMod.Close()
	adminMod.OnPrivilegeChange = func(ctx context.Context, userIDs []string, reason string) {
		if err := sessionMgr.RequireRotation(ctx, userIDs); err != nil {
			log.Printf("[Admin] gagal menandai rotasi session (%s): %v", reason, err)
		}
	}
	adminMod.RegisterRoutes(mux)

	// [Orang C] Modul Database Viewer & Migration (Akan didaftarkan oleh Orang C)
	// dbmanager.RegisterRoutes(mux)
	// migration.RegisterRoutes(mux)

	// 4. Pasang Global Middleware: Logging, CORS, CSRF, dan Security Headers
	handler := securityHeadersMiddleware(mux)
	handler = csrf.Middleware(isProd)(handler)
	handler = corsMiddleware(cfg.AllowedOrigin, handler)
	handler = loggingMiddleware(handler)

	addr := fmt.Sprintf(":%s", cfg.Port)
	log.Printf("[Server] Database Management Platform berjalan di port %s (env: %s)...", cfg.Port, cfg.AppEnv)
	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("[Server] Gagal menjalankan HTTP server: %v", err)
	}
}

// corsMiddleware mengatur CORS untuk integrasi client/ dan server/
func corsMiddleware(allowedOrigin string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, PATCH, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-CSRF-Token, Authorization")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// securityHeadersMiddleware memasang header keamanan standar
func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware mencatat incoming request dengan penyaringan data sensitif (password & token tidak di-log)
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		// Catat hanya path dan method, JANGAN log isi body atau query berisi kredensial/token
		log.Printf("[%s] %s %s - %v", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}
