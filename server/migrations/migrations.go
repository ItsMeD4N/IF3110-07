package migrations

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed *.sql
var files embed.FS

const lockKey = 31100701

func Apply(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("migrations: ambil koneksi: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, lockKey); err != nil {
		return fmt.Errorf("migrations: ambil advisory lock: %w", err)
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, lockKey)

	if _, err := conn.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS platform_schema_migrations (
		version    TEXT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		return fmt.Errorf("migrations: buat tabel pencatat: %w", err)
	}
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		if err := applyOne(ctx, conn, name); err != nil {
			return err
		}
	}
	return nil
}

func applyOne(ctx context.Context, conn *sql.Conn, name string) error {
	version := strings.TrimSuffix(name, ".sql")
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var done bool
	if err := tx.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM platform_schema_migrations WHERE version = $1)`, version).Scan(&done); err != nil {
		return err
	}
	if done {
		return nil
	}
	body, err := files.ReadFile(name)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, string(body)); err != nil {
		return fmt.Errorf("migrations: %s: %w", version, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO platform_schema_migrations (version) VALUES ($1)`, version); err != nil {
		return err
	}
	return tx.Commit()
}
