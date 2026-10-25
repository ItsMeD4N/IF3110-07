package migrations_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/database"
	"github.com/ItsMeD4N/IF3110-07/server/migrations"
)

func databaseURL() string {
	if v := os.Getenv("DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable"
}

func TestApply_ConcurrentAndIdempotent(t *testing.T) {
	db, err := database.InitDB(databaseURL())
	if err != nil {
		t.Fatalf("Gagal terhubung ke DB: %v", err)
	}
	defer db.Close()

	var wg sync.WaitGroup
	errs := make(chan error, 6)
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- migrations.Apply(context.Background(), db)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("Apply paralel gagal: %v", err)
		}
	}

	files, _ := fs.Glob(os.DirFS("."), "*.sql")
	for _, f := range files {
		version := strings.TrimSuffix(filepath.Base(f), ".sql")
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM platform_schema_migrations WHERE version = $1`, version).Scan(&n); err != nil || n != 1 {
			t.Fatalf("migrasi %s tercatat %d kali (err %v)", version, n, err)
		}
	}

	var exists bool
	db.QueryRow(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'sessions' AND column_name = 'rotation_required')`).Scan(&exists)
	if !exists {
		t.Fatal("kolom sessions.rotation_required tidak ada setelah migrasi")
	}
}

func TestApply_RenamesOldVersions(t *testing.T) {
	db, err := database.InitDB(databaseURL())
	if err != nil {
		t.Fatalf("Gagal terhubung ke DB: %v", err)
	}
	defer db.Close()
	ctx := context.Background()

	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE platform_schema_migrations SET version = '001_orang_a_users_sessions', applied_at = '2001-01-01T00:00:00Z' WHERE version = '001_users_sessions'`); err != nil {
		t.Fatal(err)
	}
	if err := migrations.Apply(ctx, db); err != nil {
		t.Fatal(err)
	}

	var oldCount int
	if err := db.QueryRow(`SELECT count(*) FROM platform_schema_migrations WHERE version = '001_orang_a_users_sessions'`).Scan(&oldCount); err != nil {
		t.Fatal(err)
	}
	var year int
	if err := db.QueryRow(`SELECT extract(year FROM applied_at)::int FROM platform_schema_migrations WHERE version = '001_users_sessions'`).Scan(&year); err != nil {
		t.Fatal(err)
	}
	if oldCount != 0 || year != 2001 {
		t.Fatalf("versi lama tidak diganti nama: sisa versi lama %d, tahun applied_at %d", oldCount, year)
	}
}
