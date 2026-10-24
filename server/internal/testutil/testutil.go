package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/database"
	"github.com/ItsMeD4N/IF3110-07/server/migrations"
)

const DefaultDatabaseURL = "postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable"

func DatabaseURL() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	return DefaultDatabaseURL
}

func InitDB(connStr string) (*sql.DB, error) {
	db, err := database.InitDB(connStr)
	if err != nil {
		return nil, err
	}
	if err := migrations.Apply(context.Background(), db); err != nil {
		db.Close()
		return nil, fmt.Errorf("gagal menerapkan migrasi: %w", err)
	}
	return db, nil
}

func OpenDB(t testing.TB) *sql.DB {
	t.Helper()
	db, err := InitDB(DatabaseURL())
	if err != nil {
		t.Fatalf("Gagal terhubung ke DB test (jalankan `docker compose up -d` atau set DATABASE_URL): %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func Unique(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
