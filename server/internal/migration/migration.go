package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

const advisoryLockKey int64 = 31100703

const (
	maxSQLBytes = 1 << 20
	maxNameLen  = 200
)

var (
	ErrDraftNotFound     = errors.New("migration: draft tidak ditemukan")
	ErrMigrationNotFound = errors.New("migration: versi tidak ditemukan")
)

type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

type ExecError struct{ Err error }

func (e *ExecError) Error() string { return e.Err.Error() }
func (e *ExecError) Unwrap() error { return e.Err }

type Module struct {
	PlatformDB *sql.DB
	Conns      *connmgr.Manager
	Authz      *authz.Authorizer
	MW         *authz.Middleware
	AuthMW     *auth.Middleware
	Views      *web.Views
}

type Record struct {
	ID         int64     `json:"id"`
	DatabaseID int64     `json:"database_id"`
	Version    int64     `json:"version"`
	Name       string    `json:"name"`
	SQL        string    `json:"sql"`
	Executor   string    `json:"executor"`
	ExecutedAt time.Time `json:"executed_at"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
}

func New(platformDB *sql.DB, conns *connmgr.Manager, az *authz.Authorizer, users auth.UserRepository, sessions *session.Manager, views *web.Views) *Module {
	return &Module{
		PlatformDB: platformDB,
		Conns:      conns,
		Authz:      az,
		MW:         authz.NewMiddleware(az),
		AuthMW:     auth.NewMiddleware(users, sessions),
		Views:      views,
	}
}

func validate(name, sqlText string) error {
	switch {
	case sqlText == "":
		return &ValidationError{"SQL migration tidak boleh kosong."}
	case len(sqlText) > maxSQLBytes:
		return &ValidationError{"SQL migration maksimal 1 MiB."}
	case len(name) > maxNameLen:
		return &ValidationError{"Nama migration maksimal 200 karakter."}
	}
	if err := checkTransactionControl(sqlText); err != nil {
		return &ValidationError{err.Error()}
	}
	return nil
}

func (m *Module) Run(ctx context.Context, databaseID int64, executor, name, sqlText string) (Record, error) {
	name, sqlText = strings.TrimSpace(name), strings.TrimSpace(sqlText)
	if err := validate(name, sqlText); err != nil {
		return Record{}, err
	}
	return m.execute(ctx, databaseID, executor, func(context.Context) (string, string, error) {
		return name, sqlText, nil
	}, nil)
}

func (m *Module) RunDraft(ctx context.Context, databaseID, draftID int64, executor string) (Record, error) {
	load := func(ctx context.Context) (string, string, error) {
		d, err := m.getDraft(ctx, databaseID, draftID)
		if err != nil {
			return "", "", err
		}
		if err := validate(d.Name, d.SQL); err != nil {
			return "", "", err
		}
		return d.Name, d.SQL, nil
	}
	onSuccess := func(ctx context.Context) error {
		_, err := m.PlatformDB.ExecContext(ctx, `DELETE FROM platform_migration_drafts WHERE id = $1`, draftID)
		return err
	}
	return m.execute(ctx, databaseID, executor, load, onSuccess)
}

func (m *Module) execute(ctx context.Context, databaseID int64, executor string,
	load func(context.Context) (name, sqlText string, err error), onSuccess func(context.Context) error) (Record, error) {

	target, err := m.Conns.Pool(ctx, databaseID)
	if err != nil {
		return Record{}, err
	}
	conn, err := target.Conn(ctx)
	if err != nil {
		return Record{}, &connmgr.ConnError{DatabaseID: databaseID, Detail: err.Error()}
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return Record{}, fmt.Errorf("gagal memperoleh migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockKey)
	}()

	name, sqlText, err := load(ctx)
	if err != nil {
		return Record{}, err
	}

	rec := Record{DatabaseID: databaseID, Name: name, SQL: sqlText, Executor: executor, Status: "running"}
	err = m.PlatformDB.QueryRowContext(ctx, `
		INSERT INTO platform_migration_history (database_id, version, name, sql_text, executor, status)
		SELECT $1, COALESCE(MAX(version), 0) + 1, $2, $3, $4, 'running'
		FROM platform_migration_history WHERE database_id = $1
		RETURNING id, version, executed_at`,
		databaseID, name, sqlText, executor).Scan(&rec.ID, &rec.Version, &rec.ExecutedAt)
	if err != nil {
		return Record{}, fmt.Errorf("gagal mencatat migration: %w", err)
	}

	if err := runInTx(ctx, conn, sqlText); err != nil {
		m.markFailed(ctx, &rec, err)
		return rec, &ExecError{err}
	}

	if err := m.PlatformDB.QueryRowContext(ctx, `
		UPDATE platform_migration_history SET status = 'success', executed_at = now()
		WHERE id = $1 RETURNING executed_at`, rec.ID).Scan(&rec.ExecutedAt); err != nil {
		return rec, fmt.Errorf("migration sudah di-commit tetapi gagal memperbarui history: %w", err)
	}
	rec.Status = "success"

	if onSuccess != nil {
		if err := onSuccess(ctx); err != nil {
			log.Printf("[Migration] migration v%d berhasil tetapi draft gagal dihapus: %v", rec.Version, err)
		}
	}
	return rec, nil
}

func runInTx(ctx context.Context, conn *sql.Conn, sqlText string) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, sqlText); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (m *Module) markFailed(ctx context.Context, rec *Record, runErr error) {
	if ctx.Err() != nil {
		ctx = context.Background()
	}
	rec.Status = "failed"
	rec.Error = runErr.Error()
	err := m.PlatformDB.QueryRowContext(ctx, `
		UPDATE platform_migration_history SET status = 'failed', error_message = $2, executed_at = now()
		WHERE id = $1 RETURNING executed_at`, rec.ID, rec.Error).Scan(&rec.ExecutedAt)
	if err != nil {
		log.Printf("[Migration] gagal mencatat status failed untuk migration %d: %v", rec.ID, err)
	}
}

const recordColumns = `id, database_id, version, name, sql_text, executor, executed_at, status, COALESCE(error_message, '')`

func scanRecord(row interface{ Scan(...any) error }) (Record, error) {
	var r Record
	err := row.Scan(&r.ID, &r.DatabaseID, &r.Version, &r.Name, &r.SQL, &r.Executor, &r.ExecutedAt, &r.Status, &r.Error)
	return r, err
}

func (m *Module) History(ctx context.Context, databaseID int64) ([]Record, error) {
	rows, err := m.PlatformDB.QueryContext(ctx,
		`SELECT `+recordColumns+` FROM platform_migration_history WHERE database_id = $1 ORDER BY version DESC`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		r, err := scanRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (m *Module) Get(ctx context.Context, databaseID, version int64) (Record, error) {
	r, err := scanRecord(m.PlatformDB.QueryRowContext(ctx,
		`SELECT `+recordColumns+` FROM platform_migration_history WHERE database_id = $1 AND version = $2`, databaseID, version))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, ErrMigrationNotFound
	}
	return r, err
}
