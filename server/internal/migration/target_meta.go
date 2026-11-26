package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

const (
	targetMetaSchema          = "_if3110_meta"
	targetReceiptTableName    = "migration_receipts"
	targetCheckpointTableName = "batch_checkpoints"
	fallbackReceiptTable      = "public._if3110_migration_receipts"
	fallbackCheckpointTable   = "public._if3110_batch_checkpoints"
)

type targetMetaTables struct {
	receiptTable    string
	checkpointTable string
}

type targetBatchCP struct {
	RootExecutionID int64
	BatchNumber     int
	ExecutionID     int64
	CursorStart     string
	CursorEnd       string
	RowsAffected    int64
	Status          string
	UpdatedAt       time.Time
}

func ensureTargetMeta(ctx context.Context, conn *sql.Conn) (targetMetaTables, error) {
	receiptTbl := pq.QuoteIdentifier(targetMetaSchema) + "." + pq.QuoteIdentifier(targetReceiptTableName)
	cpTbl := pq.QuoteIdentifier(targetMetaSchema) + "." + pq.QuoteIdentifier(targetCheckpointTableName)

	_, err := conn.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+pq.QuoteIdentifier(targetMetaSchema))
	if err != nil {
		receiptTbl = fallbackReceiptTable
		cpTbl = fallbackCheckpointTable
	}

	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			idempotency_key TEXT PRIMARY KEY,
			execution_id    BIGINT NOT NULL,
			migration_id    BIGINT NOT NULL,
			action          TEXT NOT NULL,
			committed_at    TIMESTAMPTZ NOT NULL DEFAULT now()
		)`, receiptTbl)); err != nil {
		return targetMetaTables{}, fmt.Errorf("migration: buat tabel receipt di target DB: %w", err)
	}

	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`
		CREATE TABLE IF NOT EXISTS %s (
			root_execution_id BIGINT NOT NULL,
			batch_number      INTEGER NOT NULL,
			execution_id      BIGINT NOT NULL,
			cursor_start      TEXT NOT NULL,
			cursor_end        TEXT NOT NULL,
			rows_affected     BIGINT NOT NULL DEFAULT 0,
			status            TEXT NOT NULL,
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY (root_execution_id, batch_number)
		)`, cpTbl)); err != nil {
		return targetMetaTables{}, fmt.Errorf("migration: buat tabel batch checkpoint di target DB: %w", err)
	}

	return targetMetaTables{receiptTable: receiptTbl, checkpointTable: cpTbl}, nil
}

func checkReceiptExists(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, tables targetMetaTables, idempotencyKey string) (bool, error) {
	var ok bool
	err := q.QueryRowContext(ctx,
		fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE idempotency_key = $1)`, tables.receiptTable),
		idempotencyKey,
	).Scan(&ok)
	return ok, err
}

func recordReceiptInTx(ctx context.Context, tx *sql.Tx, tables targetMetaTables, idempotencyKey string, executionID, migrationID int64, action Action) (bool, error) {
	var inserted string
	err := tx.QueryRowContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (idempotency_key, execution_id, migration_id, action)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING idempotency_key`, tables.receiptTable),
		idempotencyKey, executionID, migrationID, string(action),
	).Scan(&inserted)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func getTargetBatchCheckpoint(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, tables targetMetaTables, rootExecutionID int64, batchNumber int) (targetBatchCP, bool, error) {
	var cp targetBatchCP
	err := q.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT root_execution_id, batch_number, execution_id, cursor_start, cursor_end, rows_affected, status, updated_at
		FROM %s
		WHERE root_execution_id = $1 AND batch_number = $2`, tables.checkpointTable),
		rootExecutionID, batchNumber,
	).Scan(&cp.RootExecutionID, &cp.BatchNumber, &cp.ExecutionID, &cp.CursorStart, &cp.CursorEnd, &cp.RowsAffected, &cp.Status, &cp.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return targetBatchCP{}, false, nil
	}
	if err != nil {
		return targetBatchCP{}, false, err
	}
	return cp, true, nil
}

func upsertTargetBatchCheckpointInTx(ctx context.Context, tx *sql.Tx, tables targetMetaTables, cp targetBatchCP) error {
	_, err := tx.ExecContext(ctx, fmt.Sprintf(`
		INSERT INTO %s (root_execution_id, batch_number, execution_id, cursor_start, cursor_end, rows_affected, status, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, now())
		ON CONFLICT (root_execution_id, batch_number) DO UPDATE SET
			execution_id = EXCLUDED.execution_id,
			status       = EXCLUDED.status,
			updated_at   = now()`, tables.checkpointTable),
		cp.RootExecutionID, cp.BatchNumber, cp.ExecutionID, cp.CursorStart, cp.CursorEnd, cp.RowsAffected, cp.Status,
	)
	return err
}

func listTargetBatchCheckpoints(ctx context.Context, q interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, tables targetMetaTables, rootExecutionID int64) ([]targetBatchCP, error) {
	rows, err := q.QueryContext(ctx, fmt.Sprintf(`
		SELECT root_execution_id, batch_number, execution_id, cursor_start, cursor_end, rows_affected, status, updated_at
		FROM %s
		WHERE root_execution_id = $1
		ORDER BY batch_number ASC`, tables.checkpointTable),
		rootExecutionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []targetBatchCP{}
	for rows.Next() {
		var cp targetBatchCP
		if err := rows.Scan(&cp.RootExecutionID, &cp.BatchNumber, &cp.ExecutionID, &cp.CursorStart, &cp.CursorEnd, &cp.RowsAffected, &cp.Status, &cp.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, cp)
	}
	return out, rows.Err()
}
