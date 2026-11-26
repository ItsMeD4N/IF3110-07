package migration

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Draft struct {
	ID         int64     `json:"id"`
	DatabaseID int64     `json:"database_id"`
	Name       string    `json:"name"`
	SQL        string    `json:"sql"`
	CreatedBy  string    `json:"created_by"`
	UpdatedBy  string    `json:"updated_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

const draftColumns = `id, database_id, name, sql_text, created_by, updated_by, created_at, updated_at`

func scanDraft(row interface{ Scan(...any) error }) (Draft, error) {
	var d Draft
	err := row.Scan(&d.ID, &d.DatabaseID, &d.Name, &d.SQL, &d.CreatedBy, &d.UpdatedBy, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, ErrDraftNotFound
	}
	return d, err
}

func (m *Module) CreateDraft(ctx context.Context, databaseID int64, author, name, sqlText string) (Draft, error) {
	name, sqlText = strings.TrimSpace(name), strings.TrimSpace(sqlText)
	if err := validate(name, sqlText); err != nil {
		return Draft{}, err
	}
	return scanDraft(m.PlatformDB.QueryRowContext(ctx, `
		INSERT INTO platform_migration_drafts (database_id, name, sql_text, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $4) RETURNING `+draftColumns,
		databaseID, name, sqlText, author))
}

func (m *Module) UpdateDraft(ctx context.Context, databaseID, draftID int64, author, name, sqlText string) (Draft, error) {
	name, sqlText = strings.TrimSpace(name), strings.TrimSpace(sqlText)
	if err := validate(name, sqlText); err != nil {
		return Draft{}, err
	}
	return scanDraft(m.PlatformDB.QueryRowContext(ctx, `
		UPDATE platform_migration_drafts
		SET name = $3, sql_text = $4, updated_by = $5, updated_at = now()
		WHERE database_id = $1 AND id = $2 RETURNING `+draftColumns,
		databaseID, draftID, name, sqlText, author))
}

func (m *Module) DeleteDraft(ctx context.Context, databaseID, draftID int64) error {
	res, err := m.PlatformDB.ExecContext(ctx,
		`DELETE FROM platform_migration_drafts WHERE database_id = $1 AND id = $2`, databaseID, draftID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrDraftNotFound
	}
	return nil
}

func (m *Module) getDraft(ctx context.Context, databaseID, draftID int64) (Draft, error) {
	return scanDraft(m.PlatformDB.QueryRowContext(ctx,
		`SELECT `+draftColumns+` FROM platform_migration_drafts WHERE database_id = $1 AND id = $2`, databaseID, draftID))
}

func (m *Module) Drafts(ctx context.Context, databaseID int64) ([]Draft, error) {
	rows, err := m.PlatformDB.QueryContext(ctx,
		`SELECT `+draftColumns+` FROM platform_migration_drafts WHERE database_id = $1 ORDER BY updated_at DESC`, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Draft{}
	for rows.Next() {
		d, err := scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
