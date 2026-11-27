package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/app"
	"github.com/ItsMeD4N/IF3110-07/server/internal/config"
	"github.com/ItsMeD4N/IF3110-07/server/internal/database"
	"github.com/ItsMeD4N/IF3110-07/server/internal/migration"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
	"github.com/ItsMeD4N/IF3110-07/server/migrations"
)

func main() {
	cfg := config.LoadConfig()
	ctx := context.Background()

	logOut, closeLog, err := telemetry.LogOutput(os.Stdout, cfg.LogFile)
	if err != nil {
		slog.Error("inisialisasi log gagal", "error", err)
		os.Exit(1)
	}
	defer closeLog()
	telemetry.InitLogger(logOut, telemetry.ParseLogLevel(cfg.LogLevel))

	_, shutdownTrace, err := telemetry.InitTracing(ctx, telemetry.TracingConfig{
		ServiceName:  cfg.OTelServiceName,
		Environment:  cfg.AppEnv,
		Exporter:     cfg.OTelExporter,
		OTLPEndpoint: cfg.OTelOTLPEndpoint,
	})
	if err != nil {
		slog.Error("inisialisasi tracing gagal", "error", err)
		os.Exit(1)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTrace(sctx)
	}()

	db, err := database.InitDB(cfg.DatabaseURL)
	if err != nil {
		slog.Error("koneksi ke database platform gagal", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := migrations.Apply(ctx, db); err != nil {
		slog.Error("migrasi database platform gagal", "error", err)
		os.Exit(1)
	}

	if n, err := migration.RecoverInterrupted(ctx, db); err != nil {
		slog.Error("pemulihan migration legacy gagal", "error", err)
		os.Exit(1)
	} else if n > 0 {
		telemetry.LogEvent(ctx, slog.LevelWarn, "migration.legacy_recovered",
			slog.Int64("count", n),
			slog.String("status", "recovered"),
		)
	}

	clientDir, err := web.FindClientDir()
	if err != nil {
		slog.Error("folder client tidak ditemukan", "error", err)
		os.Exit(1)
	}

	spaDir := web.FindSPADir(clientDir)
	legacyTemplates := os.Getenv("SERVE_SPA") == "false"
	serveSPA := spaDir != "" && !legacyTemplates
	apiOnly := spaDir == "" && !legacyTemplates
	if apiOnly {
		telemetry.LogEvent(ctx, slog.LevelWarn, "server.spa_not_built",
			slog.String("client_dir", clientDir),
			slog.String("hint", "jalankan `npm install && npm run build` di folder client, atau pakai Vite dev server (npm run dev)"),
			slog.String("status", "api_only"),
		)
	}

	a, err := app.New(db, app.Options{
		AppEnv:            cfg.AppEnv,
		ClientDir:         clientDir,
		SPADir:            spaDir,
		ServeSPA:          serveSPA,
		APIOnly:           apiOnly,
		DatabaseDSN:       cfg.DatabaseURL,
		WorkerConcurrency: cfg.WorkerConcurrency,
		DisableWorker:     !cfg.EmbeddedWorker,
		RequireApproval:   cfg.RequireApproval,
		DisableLegacySync: !cfg.LegacySync,
		EnableMetrics:     cfg.MetricsEnabled,
	})
	if err != nil {
		slog.Error("inisialisasi aplikasi gagal", "error", err)
		os.Exit(1)
	}
	defer a.Close()

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           a.Handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		telemetry.LogEvent(ctx, slog.LevelInfo, "server.started",
			slog.String("addr", srv.Addr),
			slog.String("env", cfg.AppEnv),
			slog.Bool("spa_mode", serveSPA),
			slog.Bool("api_only", apiOnly),
			slog.String("spa_dir", spaDir),
			slog.Bool("embedded_worker", cfg.EmbeddedWorker),
			slog.Int("worker_concurrency", cfg.WorkerConcurrency),
			slog.Bool("require_approval", cfg.RequireApproval),
			slog.Bool("legacy_sync_migration", cfg.LegacySync),
			slog.Bool("metrics_enabled", cfg.MetricsEnabled),
			slog.String("status", "running"),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("server berhenti dengan error", "error", err)
			os.Exit(1)
		}
	}()

	<-stop
	telemetry.LogEvent(ctx, slog.LevelInfo, "server.shutting_down", slog.String("status", "stopping"))
	shCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(shCtx)
}
