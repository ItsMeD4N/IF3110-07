package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/lib/pq"

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

const targetDatabaseName = "if3110_test_target"

var (
	targetOnce sync.Once
	targetURL  string
	targetErr  error
)

func TargetDatabaseURL(t testing.TB) string {
	t.Helper()
	targetOnce.Do(func() {
		u, err := url.Parse(DatabaseURL())
		if err != nil {
			targetErr = err
			return
		}
		db, err := sql.Open("postgres", u.String())
		if err != nil {
			targetErr = err
			return
		}
		defer db.Close()
		var exists bool
		if targetErr = db.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, targetDatabaseName).Scan(&exists); targetErr != nil {
			return
		}
		if !exists {
			_, err := db.Exec(`CREATE DATABASE ` + targetDatabaseName)
			var pqErr *pq.Error
			if err != nil && !(errors.As(err, &pqErr) && (pqErr.Code == "42P04" || pqErr.Code == "23505")) {
				targetErr = fmt.Errorf("buat database target test: %w", err)
				return
			}
		}
		u.Path = "/" + targetDatabaseName
		targetURL = u.String()
	})
	if targetErr != nil {
		t.Fatalf("database target test tidak tersedia: %v", targetErr)
	}
	return targetURL
}

func OpenTargetSchema(t testing.TB) (*sql.DB, string) {
	t.Helper()
	db, err := sql.Open("postgres", TargetDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	schema := Unique("t")
	if _, err := db.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		db.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		db.Close()
	})
	return db, schema
}
