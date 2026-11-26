package migration

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
)

type DefinitionChangeListener interface {
	DefinitionChanged(ctx context.Context, databaseID, migrationID int64, actor string)
}

func (e *Engine) notifyDefinitionChanged(ctx context.Context, databaseID, migrationID int64, actor string) {
	if e.Workflow != nil {
		e.Workflow.DefinitionChanged(ctx, databaseID, migrationID, actor)
	}
}

func (e *Engine) DeleteDefinition(ctx context.Context, databaseID, migrationID int64, actor string) error {
	tx, err := e.PlatformDB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var locked, hasHistory bool
	err = tx.QueryRowContext(ctx, `
		SELECT m.is_locked_immutable,
		       EXISTS (SELECT 1 FROM platform_migration_executions x WHERE x.migration_id = m.id)
		       OR EXISTS (SELECT 1 FROM platform_migration_jobs j WHERE j.migration_id = m.id)
		FROM platform_migrations m
		WHERE m.database_id = $1 AND m.id = $2
		FOR UPDATE OF m`, databaseID, migrationID).Scan(&locked, &hasHistory)
	if err != nil {
		if _, sErr := scanDefinition(errRow{err}); sErr != nil {
			return sErr
		}
		return err
	}
	if locked || hasHistory {
		return ErrImmutableMigration
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM platform_migrations WHERE database_id = $1 AND id = $2`, databaseID, migrationID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	telemetry.LogEvent(ctx, slog.LevelInfo, "migration.definition_deleted",
		slog.Int64("migration_id", migrationID),
		slog.String("actor", actor),
		slog.String("status", "success"),
	)
	return nil
}

type errRow struct{ err error }

func (r errRow) Scan(...any) error { return r.err }

func (m *Module) apiDeleteDefinition(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	migID, ok := pathInt(r, "migrationID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	if err := m.Engine.DeleteDefinition(r.Context(), dbID(r), migID, executor(r)); err != nil {
		writeEngineErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
