package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/approval"
	"github.com/ItsMeD4N/IF3110-07/server/internal/config"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/database"
	"github.com/ItsMeD4N/IF3110-07/server/internal/dblock"
	"github.com/ItsMeD4N/IF3110-07/server/internal/migration"
	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
	"github.com/ItsMeD4N/IF3110-07/server/internal/secretbox"
	"github.com/ItsMeD4N/IF3110-07/server/internal/store"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
	"github.com/ItsMeD4N/IF3110-07/server/migrations"
)

func main() {
	cfg := config.LoadConfig()
	ctx := context.Background()

	telemetry.InitLogger(os.Stdout, telemetry.ParseLogLevel(cfg.LogLevel))

	_, shutdownTrace, err := telemetry.InitTracing(ctx, telemetry.TracingConfig{
		ServiceName:  cfg.OTelServiceName + "-worker",
		Environment:  cfg.AppEnv,
		Exporter:     cfg.OTelExporter,
		OTLPEndpoint: cfg.OTelOTLPEndpoint,
	})
	if err != nil {
		slog.Error("worker: inisialisasi tracing gagal", "error", err)
		os.Exit(1)
	}
	defer func() {
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = shutdownTrace(sctx)
	}()

	db, err := database.InitDB(cfg.DatabaseURL)
	if err != nil {
		slog.Error("worker: koneksi database platform gagal", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := migrations.Apply(ctx, db); err != nil {
		slog.Error("worker: migrasi database platform gagal", "error", err)
		os.Exit(1)
	}

	key := secretbox.DevKey()
	if raw := os.Getenv("APP_ENCRYPTION_KEY"); raw != "" {
		key, err = secretbox.ParseKey(raw)
		if err != nil {
			slog.Error("worker: APP_ENCRYPTION_KEY tidak valid", "error", err)
			os.Exit(1)
		}
	} else if cfg.AppEnv == "production" {
		slog.Error("worker: APP_ENCRYPTION_KEY wajib di-set di production")
		os.Exit(1)
	}
	box, err := secretbox.New(key)
	if err != nil {
		slog.Error("worker: secretbox gagal", "error", err)
		os.Exit(1)
	}
	fp, err := connmgr.Fingerprint(ctx, db)
	if err != nil {
		slog.Error("worker: identifikasi platform DB gagal", "error", err)
		os.Exit(1)
	}
	st := store.New(db, box)
	conns := connmgr.New(st, connmgr.Options{PlatformFingerprint: fp})
	defer conns.Close()

	locker := dblock.NewPostgresLocker(db)
	broker := queue.NewPostgresBroker(db, cfg.DatabaseURL)
	defer broker.Close()

	engine := migration.NewEngine(db, conns, locker, broker, migration.EngineOptions{
		RequireApproval: cfg.RequireApproval,
	})
	approvals := approval.NewService(db, engine, st, cfg.RequireApproval)
	engine.Workflow = approvals
	if cfg.RequireApproval {
		engine.ApprovalChecker = approvals
	}
	pool := queue.NewWorkerPool(broker, engine, queue.PoolOptions{
		Concurrency: cfg.WorkerConcurrency,
	})

	pool.Start(ctx)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	telemetry.LogEvent(ctx, slog.LevelInfo, "worker.shutting_down", slog.String("status", "stopping"))
	shCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = pool.Stop(shCtx)
}
