package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/lib/pq"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/ItsMeD4N/IF3110-07/server/internal/dblock"
	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
)

const maxBatchTransientRetries = 3

type BatchBindParams struct {
	CursorStart  string
	CursorEnd    string
	LastCursor   string
	BatchSize    int
	BatchIDs     []string
	RecoveryJSON string
	CursorUDT    string
}

func CompileBatchSQL(tmpl string, p BatchBindParams) (string, []any, error) {
	udt := p.CursorUDT
	if udt == "" || !validSQLIdentRe.MatchString(udt) {
		udt = "text"
	}

	var args []any
	addArg := func(val any, cast string) string {
		args = append(args, val)
		placeholder := fmt.Sprintf("$%d", len(args))
		if cast != "" {
			return fmt.Sprintf("(%s%s)", placeholder, cast)
		}
		return placeholder
	}

	var b strings.Builder
	i := 0
	for i < len(tmpl) {
		idx := strings.Index(tmpl[i:], "{{")
		if idx < 0 {
			b.WriteString(tmpl[i:])
			break
		}
		b.WriteString(tmpl[i : i+idx])
		rest := tmpl[i+idx:]
		end := strings.Index(rest, "}}")
		if end < 0 {
			b.WriteString(rest)
			break
		}
		token := strings.TrimSpace(rest[2:end])
		switch token {
		case "cursor_start":
			b.WriteString(addArg(p.CursorStart, "::text::"+udt))
		case "cursor_end":
			b.WriteString(addArg(p.CursorEnd, "::text::"+udt))
		case "last_cursor":
			b.WriteString(addArg(p.LastCursor, "::text::"+udt))
		case "batch_size":
			b.WriteString(addArg(p.BatchSize, "::int"))
		case "batch_ids":
			b.WriteString(addArg(pq.Array(p.BatchIDs), "::text[]::"+udt+"[]"))
		case "recovery_json":
			rec := p.RecoveryJSON
			if rec == "" {
				rec = "[]"
			}
			b.WriteString(addArg(telemetry.Sensitive(rec), "::jsonb"))
		default:
			return "", nil, fmt.Errorf("placeholder batch tidak dikenal: {{%s}}", token)
		}
		i += idx + end + 2
	}

	return b.String(), args, nil
}

func resolveCursorUDT(ctx context.Context, conn *sql.Conn, schema, table, column string) (string, error) {
	var udt string
	err := telemetry.QueryRowScan(ctx, conn, `
		SELECT udt_name
		FROM information_schema.columns
		WHERE table_schema = $1 AND table_name = $2 AND column_name = $3`,
		[]any{schema, table, column}, &udt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("kolom cursor %s.%s.%s tidak ditemukan di database target", schema, table, column)
	}
	if err != nil {
		return "", fmt.Errorf("gagal memeriksa tipe kolom cursor: %w", err)
	}
	return udt, nil
}

func (e *Engine) executeBatchUpOrResume(
	ctx context.Context,
	lease *dblock.Lease,
	targetDB *sql.DB,
	connRef **sql.Conn,
	tables targetMetaTables,
	def Definition,
	exec *Execution,
	job *queue.Job,
	payload JobPayload,
	isResume bool,
) queue.JobOutcome {
	conn := *connRef
	if def.BatchConfig == nil {
		err := &ValidationError{"batch_config tidak ditemukan pada definisi batch migration"}
		e.finishExecution(ctx, exec, ExecFailed, err.Error(), "")
		return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err}
	}
	cfg := *def.BatchConfig
	schema := cfg.NormalizedSchema()
	qTable := pq.QuoteIdentifier(schema) + "." + pq.QuoteIdentifier(cfg.Table)
	qCursor := pq.QuoteIdentifier(cfg.CursorColumn)

	cursorUDT, err := resolveCursorUDT(ctx, conn, schema, cfg.Table, cfg.CursorColumn)
	if err != nil {
		e.finishExecution(ctx, exec, ExecFailed, err.Error(), telemetry.ExtractSQLState(err))
		return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err, Retryable: queue.IsTransientError(err)}
	}

	rootExecID := exec.ID
	var lastCursor string
	var scopeMaxCursor string
	var totalBatches int
	var completedBatches int
	var totalRowsAffected int64

	if isResume {
		rootExecID = payload.RootExecutionID
		if rootExecID == 0 && payload.ParentExecutionID > 0 {
			rootExecID = e.resolveRootExecutionID(ctx, payload.ParentExecutionID)
		}
		if rootExecID == 0 {
			err := errors.New("root_execution_id tidak ditemukan untuk resume batch")
			e.finishExecution(ctx, exec, ExecFailed, err.Error(), "")
			return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err}
		}

		if err := e.reconcileTargetBatchCheckpoints(ctx, conn, tables, def, rootExecID, exec.ID); err != nil {
			e.finishExecution(ctx, exec, ExecFailed, err.Error(), telemetry.ExtractSQLState(err))
			return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err, Retryable: queue.IsTransientError(err)}
		}

		parentExec, err := e.GetExecution(ctx, def.DatabaseID, rootExecID)
		if err != nil {
			e.finishExecution(ctx, exec, ExecFailed, err.Error(), "")
			return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err}
		}
		scopeMaxCursor = parentExec.ScopeMaxCursor
		totalBatches = parentExec.TotalBatches

		cps, err := e.ListBatchCheckpoints(ctx, rootExecID)
		if err != nil {
			e.finishExecution(ctx, exec, ExecFailed, err.Error(), "")
			return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err}
		}
		for _, cp := range cps {
			if cp.Status == "committed" {
				completedBatches = cp.BatchNumber
				lastCursor = cp.CursorEnd
				totalRowsAffected += cp.RowsAffected
			}
		}
		e.updateBatchProgress(ctx, exec.ID, totalBatches, completedBatches, totalRowsAffected, lastCursor, scopeMaxCursor)
	} else {
		scopeQuery := fmt.Sprintf(`SELECT COALESCE(MAX(%s)::text, ''), COUNT(*) FROM %s`, qCursor, qTable)
		if strings.TrimSpace(cfg.WhereClause) != "" {
			scopeQuery += " WHERE " + cfg.WhereClause
		}
		var estimatedRows int64
		if err := telemetry.QueryRowScan(ctx, conn, scopeQuery, nil, &scopeMaxCursor, &estimatedRows); err != nil {
			e.finishExecution(ctx, exec, ExecFailed, err.Error(), telemetry.ExtractSQLState(err))
			return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err, Retryable: queue.IsTransientError(err)}
		}
		if estimatedRows > 0 {
			totalBatches = int(math.Ceil(float64(estimatedRows) / float64(cfg.BatchSize)))
		}
		e.updateBatchProgress(ctx, exec.ID, totalBatches, 0, 0, "", scopeMaxCursor)

		if estimatedRows == 0 || scopeMaxCursor == "" {
			if err := e.commitEmptyReceipt(ctx, conn, tables, job.IdempotencyKey, exec.ID, def.ID, ActionBatchUp); err != nil {
				e.finishExecution(ctx, exec, ExecFailed, err.Error(), telemetry.ExtractSQLState(err))
				return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err, Retryable: queue.IsTransientError(err)}
			}
			e.finishExecution(ctx, exec, ExecSuccess, "", "")
			_ = e.SetWorkflowStatus(ctx, def.DatabaseID, def.ID, "applied")
			return queue.JobOutcome{Status: queue.StatusCompleted, ExecutionID: exec.ID}
		}
	}

	batchNum := completedBatches + 1
	for {
		if ctx.Err() != nil {
			return e.handleBatchInterrupted(ctx, lease, def, exec, rootExecID, completedBatches, totalRowsAffected, lastCursor, ctx.Err())
		}
		if err := e.Locker.Verify(ctx, lease); err != nil {
			return e.handleBatchInterrupted(ctx, lease, def, exec, rootExecID, completedBatches, totalRowsAffected, lastCursor, err)
		}

		batchMeta := telemetry.ExecMetaFrom(ctx)
		batchMeta.BatchNumber = batchNum
		batchCtx := telemetry.WithExecMeta(ctx, batchMeta)

		batchCtx, bSpan := telemetry.Tracer().Start(batchCtx, "migration.batch_execute")
		bSpan.SetAttributes(
			attribute.Int64("migration.id", def.ID),
			attribute.Int64("execution.id", exec.ID),
			attribute.Int64("execution.root_id", rootExecID),
			attribute.Int("migration.batch_number", batchNum),
			attribute.Int("migration.batch_size", cfg.BatchSize),
			attribute.String("migration.last_cursor", lastCursor),
		)

		var outcome batchStepResult
		var stepErr error
		var uncertainCommit bool

		for try := 1; try <= maxBatchTransientRetries; try++ {
			stepMeta := telemetry.ExecMetaFrom(batchCtx)
			stepMeta.Attempt = try
			stepCtx := telemetry.WithExecMeta(batchCtx, stepMeta)

			outcome, stepErr, uncertainCommit = e.runSingleBatchUp(
				stepCtx, lease, targetDB, conn, tables, def, cfg, cursorUDT,
				rootExecID, exec.ID, batchNum, lastCursor, scopeMaxCursor,
			)
			if stepErr == nil {
				break
			}
			if uncertainCommit {
				break
			}
			if !queue.IsTransientError(stepErr) || try == maxBatchTransientRetries {
				break
			}
			delay := e.BatchBackoff.NextDelay(try)
			telemetry.LogEvent(stepCtx, slog.LevelWarn, "migration.batch_retry",
				slog.Int64("migration_id", def.ID),
				slog.Int64("execution_id", exec.ID),
				slog.Int("batch_number", batchNum),
				slog.Int("attempt", try),
				slog.Int64("backoff_ms", delay.Milliseconds()),
				slog.String("sqlstate", telemetry.ExtractSQLState(stepErr)),
				slog.String("error", stepErr.Error()),
				slog.String("status", "retrying"),
			)
			select {
			case <-batchCtx.Done():
				stepErr = batchCtx.Err()
			case <-time.After(delay):
			}
			if newConn, cErr := targetDB.Conn(batchCtx); cErr == nil {
				dblock.ReleaseTargetAdvisory(context.Background(), conn)
				_ = conn.Close()
				conn = newConn
				*connRef = newConn
				_, _ = dblock.AcquireTargetAdvisory(batchCtx, conn)
			}
		}

		if uncertainCommit {
			bSpan.RecordError(stepErr)
			bSpan.SetStatus(codes.Error, stepErr.Error())
			bSpan.End()
			e.finishExecution(ctx, exec, ExecUncertain, stepErr.Error(), telemetry.ExtractSQLState(stepErr))
			_ = e.SetWorkflowStatus(ctx, def.DatabaseID, def.ID, "uncertain")
			_ = e.Locker.TransitionToBlocked(ctx, lease, dblock.StateBlockedUncertain, exec.ID,
				fmt.Sprintf("hasil commit batch #%d belum pasti: %s", batchNum, stepErr.Error()))
			return queue.JobOutcome{
				Status:      queue.StatusUncertain,
				ExecutionID: exec.ID,
				Err:         stepErr,
				SQLState:    telemetry.ExtractSQLState(stepErr),
			}
		}

		if stepErr != nil {
			bSpan.RecordError(stepErr)
			bSpan.SetStatus(codes.Error, stepErr.Error())
			bSpan.End()

			sqlState := telemetry.ExtractSQLState(stepErr)
			_ = e.upsertPlatformBatchCheckpoint(ctx, BatchCheckpoint{
				ExecutionID:     exec.ID,
				RootExecutionID: rootExecID,
				DatabaseID:      def.DatabaseID,
				MigrationID:     def.ID,
				BatchNumber:     batchNum,
				CursorStart:     outcome.CursorStart,
				CursorEnd:       outcome.CursorEnd,
				RowsAffected:    0,
				RowIDs:          outcome.RowIDs,
				Status:          "failed",
				Attempt:         maxBatchTransientRetries,
				Error:           stepErr.Error(),
				SQLState:        sqlState,
				DurationMS:      outcome.DurationMS,
			})

			if completedBatches > 0 {
				e.finishExecution(ctx, exec, ExecPartial, stepErr.Error(), sqlState)
				_ = e.SetWorkflowStatus(ctx, def.DatabaseID, def.ID, "partial")
				_ = e.Locker.TransitionToBlocked(ctx, lease, dblock.StateBlockedPartial, exec.ID,
					fmt.Sprintf("batch #%d gagal setelah %d batch committed (%d row); lanjutkan dari checkpoint atau batalkan", batchNum, completedBatches, totalRowsAffected))
				return queue.JobOutcome{
					Status:      queue.StatusPartial,
					ExecutionID: exec.ID,
					Err:         stepErr,
					SQLState:    sqlState,
				}
			}

			e.finishExecution(ctx, exec, ExecFailed, stepErr.Error(), sqlState)
			_ = e.SetWorkflowStatus(ctx, def.DatabaseID, def.ID, "failed")
			return queue.JobOutcome{
				Status:      queue.StatusFailed,
				ExecutionID: exec.ID,
				Err:         stepErr,
				SQLState:    sqlState,
				Retryable:   queue.IsTransientError(stepErr),
			}
		}

		if outcome.Empty {
			bSpan.End()
			break
		}

		completedBatches = batchNum
		lastCursor = outcome.CursorEnd
		totalRowsAffected += outcome.RowsAffected
		exec.CompletedBatches = completedBatches
		exec.RowsAffected = totalRowsAffected
		exec.LastCursor = lastCursor

		storedRowIDs := outcome.RowIDs
		if !strings.Contains(def.SQLDown, "{{batch_ids}}") {
			storedRowIDs = nil
		}

		now := time.Now().UTC()
		_ = e.upsertPlatformBatchCheckpoint(ctx, BatchCheckpoint{
			ExecutionID:     exec.ID,
			RootExecutionID: rootExecID,
			DatabaseID:      def.DatabaseID,
			MigrationID:     def.ID,
			BatchNumber:     batchNum,
			CursorStart:     outcome.CursorStart,
			CursorEnd:       outcome.CursorEnd,
			RowsAffected:    outcome.RowsAffected,
			RowIDs:          storedRowIDs,
			RecoveryData:    outcome.RecoveryJSON,
			Status:          "committed",
			Attempt:         1,
			DurationMS:      outcome.DurationMS,
			CommittedAt:     &now,
		})
		e.updateBatchProgress(ctx, exec.ID, maxInt(totalBatches, completedBatches), completedBatches, totalRowsAffected, lastCursor, scopeMaxCursor)

		bSpan.SetAttributes(
			attribute.Int64("migration.batch_rows_affected", outcome.RowsAffected),
			attribute.String("migration.cursor_end", lastCursor),
		)
		bSpan.End()

		telemetry.LogEvent(batchCtx, slog.LevelInfo, "migration.batch_committed",
			slog.Int64("migration_id", def.ID),
			slog.Int64("execution_id", exec.ID),
			slog.Int("batch_number", batchNum),
			slog.String("cursor_start", outcome.CursorStart),
			slog.String("cursor_end", outcome.CursorEnd),
			slog.Int64("rows_affected", outcome.RowsAffected),
			slog.Int64("total_rows_affected", totalRowsAffected),
			slog.Int64("duration_ms", outcome.DurationMS),
			slog.String("status", "committed"),
		)

		batchNum++
		if cfg.BatchDelayMS > 0 {
			select {
			case <-ctx.Done():
				return e.handleBatchInterrupted(ctx, lease, def, exec, rootExecID, completedBatches, totalRowsAffected, lastCursor, ctx.Err())
			case <-time.After(time.Duration(cfg.BatchDelayMS) * time.Millisecond):
			}
		}
	}

	exec.TotalBatches = maxInt(totalBatches, completedBatches)
	exec.CompletedBatches = completedBatches
	exec.RowsAffected = totalRowsAffected
	exec.LastCursor = lastCursor
	if err := e.commitEmptyReceipt(ctx, conn, tables, job.IdempotencyKey, exec.ID, def.ID, exec.Action); err != nil {
		telemetry.LogEvent(ctx, slog.LevelWarn, "migration.batch_receipt_warn",
			slog.Int64("execution_id", exec.ID),
			slog.String("error", err.Error()),
			slog.String("status", "warning"),
		)
	}

	e.finishExecution(ctx, exec, ExecSuccess, "", "")
	_ = e.SetWorkflowStatus(ctx, def.DatabaseID, def.ID, "applied")
	return queue.JobOutcome{Status: queue.StatusCompleted, ExecutionID: exec.ID}
}

type batchStepResult struct {
	Empty        bool
	CursorStart  string
	CursorEnd    string
	RowIDs       []string
	RecoveryJSON json.RawMessage
	RowsAffected int64
	DurationMS   int64
}

func (e *Engine) runSingleBatchUp(
	ctx context.Context,
	lease *dblock.Lease,
	targetDB *sql.DB,
	conn *sql.Conn,
	tables targetMetaTables,
	def Definition,
	cfg BatchConfig,
	cursorUDT string,
	rootExecID, execID int64,
	batchNum int,
	lastCursor, scopeMaxCursor string,
) (batchStepResult, error, bool) {
	start := time.Now()
	schema := cfg.NormalizedSchema()
	qTable := pq.QuoteIdentifier(schema) + "." + pq.QuoteIdentifier(cfg.Table)
	qCursor := pq.QuoteIdentifier(cfg.CursorColumn)

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return batchStepResult{}, err, false
	}

	if existing, found, cpErr := getTargetBatchCheckpoint(ctx, tx, tables, rootExecID, batchNum); cpErr == nil && found && existing.Status == "committed" {
		_ = telemetry.RollbackTx(ctx, tx, "batch_already_committed")
		return batchStepResult{
			CursorStart:  existing.CursorStart,
			CursorEnd:    existing.CursorEnd,
			RowsAffected: existing.RowsAffected,
			DurationMS:   time.Since(start).Milliseconds(),
		}, nil, false
	}

	var cursorQuery string
	var cursorArgs []any
	whereExtra := ""
	if strings.TrimSpace(cfg.WhereClause) != "" {
		whereExtra = " AND (" + cfg.WhereClause + ")"
	}
	if lastCursor == "" {
		cursorQuery = fmt.Sprintf(
			`SELECT %s::text AS _cursor_val FROM %s WHERE %s <= ($1::text::%s)%s ORDER BY %s ASC LIMIT $2`,
			qCursor, qTable, qCursor, cursorUDT, whereExtra, qCursor,
		)
		cursorArgs = []any{scopeMaxCursor, cfg.BatchSize}
	} else {
		cursorQuery = fmt.Sprintf(
			`SELECT %s::text AS _cursor_val FROM %s WHERE %s > ($1::text::%s) AND %s <= ($2::text::%s)%s ORDER BY %s ASC LIMIT $3`,
			qCursor, qTable, qCursor, cursorUDT, qCursor, cursorUDT, whereExtra, qCursor,
		)
		cursorArgs = []any{lastCursor, scopeMaxCursor, cfg.BatchSize}
	}

	rows, err := telemetry.QuerySQL(ctx, tx, cursorQuery, cursorArgs...)
	if err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "cursor_fetch_error")
		return batchStepResult{}, err, false
	}
	ids := make([]string, 0, cfg.BatchSize)
	for rows.Next() {
		var idStr string
		if err := rows.Scan(&idStr); err != nil {
			rows.Close()
			_ = telemetry.RollbackTx(ctx, tx, "cursor_scan_error")
			return batchStepResult{}, err, false
		}
		ids = append(ids, idStr)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		_ = telemetry.RollbackTx(ctx, tx, "cursor_rows_error")
		return batchStepResult{}, err, false
	}
	rows.Close()

	if len(ids) == 0 {
		_ = telemetry.RollbackTx(ctx, tx, "no_more_rows")
		return batchStepResult{Empty: true}, nil, false
	}

	cursorStart := ids[0]
	cursorEnd := ids[len(ids)-1]

	var recoveryJSON json.RawMessage
	if len(cfg.CaptureColumns) > 0 && !def.IsIrreversible {
		recBytes, capErr := captureBatchRecovery(ctx, tx, qTable, qCursor, cfg.CursorColumn, cursorUDT, cfg.CaptureColumns, ids)
		if capErr != nil {
			_ = telemetry.RollbackTx(ctx, tx, "recovery_capture_error")
			return batchStepResult{CursorStart: cursorStart, CursorEnd: cursorEnd, RowIDs: ids, DurationMS: time.Since(start).Milliseconds()}, capErr, false
		}
		recoveryJSON = recBytes
	}

	if e.BatchBeforeExecHook != nil {
		if hookErr := e.BatchBeforeExecHook(ctx, execID, batchNum, ActionBatchUp); hookErr != nil {
			_ = telemetry.RollbackTx(ctx, tx, "hook_injected_error")
			return batchStepResult{CursorStart: cursorStart, CursorEnd: cursorEnd, RowIDs: ids, DurationMS: time.Since(start).Milliseconds()}, hookErr, false
		}
	}

	compiledSQL, compiledArgs, err := CompileBatchSQL(def.SQLUp, BatchBindParams{
		CursorStart: cursorStart,
		CursorEnd:   cursorEnd,
		LastCursor:  lastCursor,
		BatchSize:   cfg.BatchSize,
		BatchIDs:    ids,
		CursorUDT:   cursorUDT,
	})
	if err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "compile_batch_sql_error")
		return batchStepResult{CursorStart: cursorStart, CursorEnd: cursorEnd, RowIDs: ids, DurationMS: time.Since(start).Milliseconds()}, err, false
	}

	res, err := telemetry.ExecSQL(ctx, tx, compiledSQL, compiledArgs...)
	if err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "batch_exec_error")
		return batchStepResult{CursorStart: cursorStart, CursorEnd: cursorEnd, RowIDs: ids, DurationMS: time.Since(start).Milliseconds()}, err, false
	}
	affected, _ := res.RowsAffected()

	if err := upsertTargetBatchCheckpointInTx(ctx, tx, tables, targetBatchCP{
		RootExecutionID: rootExecID,
		BatchNumber:     batchNum,
		ExecutionID:     execID,
		CursorStart:     cursorStart,
		CursorEnd:       cursorEnd,
		RowsAffected:    affected,
		Status:          "committed",
	}); err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "checkpoint_insert_error")
		return batchStepResult{CursorStart: cursorStart, CursorEnd: cursorEnd, RowIDs: ids, DurationMS: time.Since(start).Milliseconds()}, err, false
	}

	if err := e.Locker.Verify(ctx, lease); err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "lease_lost_before_commit")
		return batchStepResult{CursorStart: cursorStart, CursorEnd: cursorEnd, RowIDs: ids, DurationMS: time.Since(start).Milliseconds()}, err, false
	}

	if err := telemetry.CommitTx(ctx, tx); err != nil {
		if queue.IsUncertainCommitError(err) {
			vctx, vcancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer vcancel()
			vconn, cErr := targetDB.Conn(vctx)
			if cErr == nil {
				defer vconn.Close()
				cp, found, qErr := getTargetBatchCheckpoint(vctx, vconn, tables, rootExecID, batchNum)
				if qErr == nil && found && cp.Status == "committed" {
					return batchStepResult{
						CursorStart:  cursorStart,
						CursorEnd:    cursorEnd,
						RowIDs:       ids,
						RecoveryJSON: recoveryJSON,
						RowsAffected: affected,
						DurationMS:   time.Since(start).Milliseconds(),
					}, nil, false
				}
				if qErr == nil && !found {
					return batchStepResult{CursorStart: cursorStart, CursorEnd: cursorEnd, RowIDs: ids, DurationMS: time.Since(start).Milliseconds()}, err, false
				}
			}
			return batchStepResult{CursorStart: cursorStart, CursorEnd: cursorEnd, RowIDs: ids, DurationMS: time.Since(start).Milliseconds()}, err, true
		}
		return batchStepResult{CursorStart: cursorStart, CursorEnd: cursorEnd, RowIDs: ids, DurationMS: time.Since(start).Milliseconds()}, err, false
	}

	return batchStepResult{
		CursorStart:  cursorStart,
		CursorEnd:    cursorEnd,
		RowIDs:       ids,
		RecoveryJSON: recoveryJSON,
		RowsAffected: affected,
		DurationMS:   time.Since(start).Milliseconds(),
	}, nil, false
}

func captureBatchRecovery(ctx context.Context, tx *sql.Tx, qTable, qCursor, cursorCol, cursorUDT string, captureCols, ids []string) (json.RawMessage, error) {
	pairs := make([]string, 0, len(captureCols)+1)
	seen := map[string]bool{cursorCol: true}
	pairs = append(pairs, fmt.Sprintf("%s, %s", pq.QuoteLiteral(cursorCol), qCursor))
	for _, col := range captureCols {
		if seen[col] {
			continue
		}
		seen[col] = true
		pairs = append(pairs, fmt.Sprintf("%s, %s", pq.QuoteLiteral(col), pq.QuoteIdentifier(col)))
	}
	query := fmt.Sprintf(`
		SELECT COALESCE(jsonb_agg(jsonb_build_object(%s) ORDER BY %s ASC), '[]'::jsonb)::text
		FROM %s
		WHERE %s = ANY(($1::text[])::%s[])`,
		strings.Join(pairs, ", "), qCursor, qTable, qCursor, cursorUDT,
	)
	var raw string
	if err := telemetry.QueryRowScan(ctx, tx, query, []any{pq.Array(ids)}, &raw); err != nil {
		return nil, fmt.Errorf("gagal menyimpan data pemulihan batch: %w", err)
	}
	return json.RawMessage(raw), nil
}

func (e *Engine) executeBatchRevert(
	ctx context.Context,
	lease *dblock.Lease,
	targetDB *sql.DB,
	conn *sql.Conn,
	tables targetMetaTables,
	def Definition,
	exec *Execution,
	job *queue.Job,
	payload JobPayload,
) queue.JobOutcome {
	if def.IsIrreversible || strings.TrimSpace(def.SQLDown) == "" {
		e.finishExecution(ctx, exec, ExecFailed, ErrUnsafeBatchRevert.Error(), "")
		return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: ErrUnsafeBatchRevert}
	}
	if def.BatchConfig == nil {
		err := ErrUnsafeBatchRevert
		e.finishExecution(ctx, exec, ExecFailed, err.Error(), "")
		return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err}
	}

	rootExecID := payload.RootExecutionID
	if rootExecID == 0 && payload.ParentExecutionID > 0 {
		rootExecID = e.resolveRootExecutionID(ctx, payload.ParentExecutionID)
	}
	if rootExecID == 0 && payload.TargetExecutionID > 0 {
		rootExecID = e.resolveRootExecutionID(ctx, payload.TargetExecutionID)
	}
	if rootExecID == 0 {
		err := errors.New("root_execution_id tidak ditemukan untuk batch revert")
		e.finishExecution(ctx, exec, ExecFailed, err.Error(), "")
		return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err}
	}

	if err := e.reconcileTargetBatchCheckpoints(ctx, conn, tables, def, rootExecID, exec.ID); err != nil {
		e.finishExecution(ctx, exec, ExecFailed, err.Error(), telemetry.ExtractSQLState(err))
		return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err, Retryable: queue.IsTransientError(err)}
	}

	cps, err := e.ListBatchCheckpoints(ctx, rootExecID)
	if err != nil {
		e.finishExecution(ctx, exec, ExecFailed, err.Error(), "")
		return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err}
	}

	needsRecovery := strings.Contains(def.SQLDown, "{{recovery_json}}")
	needsBatchIDs := strings.Contains(def.SQLDown, "{{batch_ids}}")
	toRevert := []BatchCheckpoint{}
	alreadyReverted := 0
	var alreadyRevertedRows int64

	for _, cp := range cps {
		switch cp.Status {
		case "committed", "revert_failed":
			toRevert = append(toRevert, cp)
		case "reverted":
			alreadyReverted++
			alreadyRevertedRows += cp.RowsAffected
		}
	}

	for _, chk := range []struct {
		needed bool
		column string
		label  string
	}{
		{needsRecovery, "recovery_data", "data pemulihan"},
		{needsBatchIDs, "row_ids", "daftar row"},
	} {
		if !chk.needed {
			continue
		}
		missing, found, pErr := e.firstBatchMissingPayload(ctx, rootExecID, chk.column)
		if pErr != nil {
			e.finishExecution(ctx, exec, ExecFailed, pErr.Error(), telemetry.ExtractSQLState(pErr))
			return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: pErr, Retryable: queue.IsTransientError(pErr)}
		}
		if found {
			uErr := fmt.Errorf("%w: %s untuk batch #%d tidak tersedia", ErrUnsafeBatchRevert, chk.label, missing)
			e.finishExecution(ctx, exec, ExecFailed, uErr.Error(), "")
			return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: uErr}
		}
	}

	cfg := *def.BatchConfig
	cursorUDT, err := resolveCursorUDT(ctx, conn, cfg.NormalizedSchema(), cfg.Table, cfg.CursorColumn)
	if err != nil {
		e.finishExecution(ctx, exec, ExecFailed, err.Error(), telemetry.ExtractSQLState(err))
		return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: err, Retryable: queue.IsTransientError(err)}
	}

	totalToRevert := alreadyReverted + len(toRevert)
	revertedBatches := alreadyReverted
	revertedRows := alreadyRevertedRows
	e.updateBatchRevertProgress(ctx, exec.ID, totalToRevert, revertedBatches, revertedRows)

	for i := len(toRevert) - 1; i >= 0; i-- {
		cp := toRevert[i]
		if ctx.Err() != nil {
			return e.handleBatchRevertPartial(ctx, lease, def, exec, revertedBatches, revertedRows, ctx.Err())
		}
		if err := e.Locker.Verify(ctx, lease); err != nil {
			return e.handleBatchRevertPartial(ctx, lease, def, exec, revertedBatches, revertedRows, err)
		}

		if needsRecovery || needsBatchIDs {
			rowIDs, recovery, lErr := e.loadBatchPayload(ctx, rootExecID, cp.BatchNumber)
			if lErr != nil {
				return e.handleBatchRevertPartial(ctx, lease, def, exec, revertedBatches, revertedRows, lErr)
			}
			cp.RowIDs = rowIDs
			cp.RecoveryData = recovery
		}

		bMeta := telemetry.ExecMetaFrom(ctx)
		bMeta.BatchNumber = cp.BatchNumber
		bCtx := telemetry.WithExecMeta(ctx, bMeta)

		bCtx, bSpan := telemetry.Tracer().Start(bCtx, "migration.batch_revert")
		bSpan.SetAttributes(
			attribute.Int64("migration.id", def.ID),
			attribute.Int64("execution.id", exec.ID),
			attribute.Int("migration.batch_number", cp.BatchNumber),
		)

		affected, rErr := e.runSingleBatchRevert(bCtx, lease, conn, tables, def, cfg, cursorUDT, rootExecID, exec.ID, cp)
		if rErr != nil {
			bSpan.RecordError(rErr)
			bSpan.SetStatus(codes.Error, rErr.Error())
			bSpan.End()

			sqlState := telemetry.ExtractSQLState(rErr)
			_ = e.markPlatformCheckpointRevertStatus(ctx, rootExecID, cp.BatchNumber, "revert_failed", rErr.Error(), sqlState)
			return e.handleBatchRevertPartial(ctx, lease, def, exec, revertedBatches, revertedRows, rErr)
		}

		revertedBatches++
		revertedRows += affected
		exec.RevertedBatches = revertedBatches
		exec.RevertedRows = revertedRows
		_ = e.markPlatformCheckpointRevertStatus(ctx, rootExecID, cp.BatchNumber, "reverted", "", "")
		e.updateBatchRevertProgress(ctx, exec.ID, totalToRevert, revertedBatches, revertedRows)

		bSpan.SetAttributes(attribute.Int64("migration.batch_reverted_rows", affected))
		bSpan.End()

		telemetry.LogEvent(bCtx, slog.LevelInfo, "migration.batch_reverted",
			slog.Int64("migration_id", def.ID),
			slog.Int64("execution_id", exec.ID),
			slog.Int("batch_number", cp.BatchNumber),
			slog.Int64("rows_affected", affected),
			slog.Int("reverted_batches", revertedBatches),
			slog.String("status", "reverted"),
		)
	}

	_ = e.commitEmptyReceipt(ctx, conn, tables, job.IdempotencyKey, exec.ID, def.ID, ActionBatchRevert)
	e.finishExecution(ctx, exec, ExecSuccess, "", "")
	if payload.ParentExecutionID > 0 {
		_, _ = e.PlatformDB.ExecContext(ctx,
			`UPDATE platform_migration_executions SET status = 'reverted' WHERE id IN ($1, $2) AND status IN ('partial', 'partially_reverted')`,
			payload.ParentExecutionID, rootExecID,
		)
	}
	_ = e.SetWorkflowStatus(ctx, def.DatabaseID, def.ID, "rolled_back")
	return queue.JobOutcome{Status: queue.StatusCompleted, ExecutionID: exec.ID}
}

func (e *Engine) runSingleBatchRevert(
	ctx context.Context,
	lease *dblock.Lease,
	conn *sql.Conn,
	tables targetMetaTables,
	def Definition,
	cfg BatchConfig,
	cursorUDT string,
	rootExecID, execID int64,
	cp BatchCheckpoint,
) (int64, error) {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}

	if existing, found, err := getTargetBatchCheckpoint(ctx, tx, tables, rootExecID, cp.BatchNumber); err == nil && found && existing.Status == "reverted" {
		_ = telemetry.RollbackTx(ctx, tx, "batch_already_reverted")
		return cp.RowsAffected, nil
	}

	if e.BatchBeforeExecHook != nil {
		if hookErr := e.BatchBeforeExecHook(ctx, execID, cp.BatchNumber, ActionBatchRevert); hookErr != nil {
			_ = telemetry.RollbackTx(ctx, tx, "hook_injected_revert_error")
			return 0, hookErr
		}
	}

	compiledSQL, compiledArgs, err := CompileBatchSQL(def.SQLDown, BatchBindParams{
		CursorStart:  cp.CursorStart,
		CursorEnd:    cp.CursorEnd,
		BatchSize:    cfg.BatchSize,
		BatchIDs:     cp.RowIDs,
		RecoveryJSON: string(cp.RecoveryData),
		CursorUDT:    cursorUDT,
	})
	if err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "compile_batch_down_error")
		return 0, err
	}

	res, err := telemetry.ExecSQL(ctx, tx, compiledSQL, compiledArgs...)
	if err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "batch_revert_exec_error")
		return 0, err
	}
	affected, _ := res.RowsAffected()

	if err := upsertTargetBatchCheckpointInTx(ctx, tx, tables, targetBatchCP{
		RootExecutionID: rootExecID,
		BatchNumber:     cp.BatchNumber,
		ExecutionID:     execID,
		CursorStart:     cp.CursorStart,
		CursorEnd:       cp.CursorEnd,
		RowsAffected:    cp.RowsAffected,
		Status:          "reverted",
	}); err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "batch_revert_checkpoint_error")
		return 0, err
	}

	if err := e.Locker.Verify(ctx, lease); err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "lease_lost_before_revert_commit")
		return 0, err
	}

	if err := telemetry.CommitTx(ctx, tx); err != nil {
		return 0, err
	}
	return affected, nil
}

func (e *Engine) handleBatchInterrupted(
	ctx context.Context,
	lease *dblock.Lease,
	def Definition,
	exec *Execution,
	rootExecID int64,
	completedBatches int,
	totalRows int64,
	lastCursor string,
	cause error,
) queue.JobOutcome {
	cctx := context.Background()
	if completedBatches > 0 {
		e.finishExecution(cctx, exec, ExecPartial, cause.Error(), telemetry.ExtractSQLState(cause))
		_ = e.SetWorkflowStatus(cctx, def.DatabaseID, def.ID, "partial")
		_ = e.Locker.TransitionToBlocked(cctx, lease, dblock.StateBlockedPartial, exec.ID,
			fmt.Sprintf("batch terhenti setelah %d batch committed (%d row): %s", completedBatches, totalRows, cause.Error()))
		return queue.JobOutcome{Status: queue.StatusPartial, ExecutionID: exec.ID, Err: cause}
	}
	e.finishExecution(cctx, exec, ExecFailed, cause.Error(), telemetry.ExtractSQLState(cause))
	return queue.JobOutcome{Status: queue.StatusFailed, ExecutionID: exec.ID, Err: cause, Retryable: true}
}

func (e *Engine) handleBatchRevertPartial(
	ctx context.Context,
	lease *dblock.Lease,
	def Definition,
	exec *Execution,
	revertedBatches int,
	revertedRows int64,
	cause error,
) queue.JobOutcome {
	cctx := context.Background()
	sqlState := telemetry.ExtractSQLState(cause)
	e.finishExecution(cctx, exec, ExecPartiallyReverted, cause.Error(), sqlState)
	_ = e.SetWorkflowStatus(cctx, def.DatabaseID, def.ID, "partially_reverted")
	_ = e.Locker.TransitionToBlocked(cctx, lease, dblock.StateBlockedPartial, exec.ID,
		fmt.Sprintf("pembatalan batch berhenti sebagian (%d batch reverted, %d row): %s", revertedBatches, revertedRows, cause.Error()))
	return queue.JobOutcome{
		Status:      queue.StatusPartiallyReverted,
		ExecutionID: exec.ID,
		Err:         cause,
		SQLState:    sqlState,
	}
}

func (e *Engine) reconcileTargetBatchCheckpoints(ctx context.Context, conn *sql.Conn, tables targetMetaTables, def Definition, rootExecID, currentExecID int64) error {
	targetCPs, err := listTargetBatchCheckpoints(ctx, conn, tables, rootExecID)
	if err != nil {
		return err
	}
	for _, tcp := range targetCPs {
		t := tcp.UpdatedAt
		cp := BatchCheckpoint{
			ExecutionID:     tcp.ExecutionID,
			RootExecutionID: rootExecID,
			DatabaseID:      def.DatabaseID,
			MigrationID:     def.ID,
			BatchNumber:     tcp.BatchNumber,
			CursorStart:     tcp.CursorStart,
			CursorEnd:       tcp.CursorEnd,
			RowsAffected:    tcp.RowsAffected,
			Status:          tcp.Status,
			Attempt:         1,
		}
		if tcp.Status == "committed" {
			cp.CommittedAt = &t
		} else if tcp.Status == "reverted" {
			cp.RevertedAt = &t
		}
		if err := e.upsertPlatformBatchCheckpoint(ctx, cp); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) upsertPlatformBatchCheckpoint(ctx context.Context, cp BatchCheckpoint) error {
	rowIDsBytes, _ := json.Marshal(cp.RowIDs)
	if len(rowIDsBytes) == 0 || string(rowIDsBytes) == "null" {
		rowIDsBytes = []byte("[]")
	}
	recBytes := cp.RecoveryData
	if len(recBytes) == 0 {
		recBytes = []byte("[]")
	}
	var errArg, stateArg any
	if cp.Error != "" {
		errArg = telemetry.MaskString(cp.Error)
	}
	if cp.SQLState != "" {
		stateArg = cp.SQLState
	}
	_, err := e.PlatformDB.ExecContext(ctx, `
		INSERT INTO platform_migration_batch_checkpoints (
			execution_id, root_execution_id, database_id, migration_id,
			batch_number, cursor_start, cursor_end, rows_affected, row_ids, recovery_data,
			status, attempt, error_message, sqlstate, duration_ms, committed_at, reverted_at, updated_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8, $9::jsonb, $10::jsonb,
			$11, $12, $13, $14, $15, $16, $17, now()
		)
		ON CONFLICT (root_execution_id, batch_number) DO UPDATE SET
			execution_id  = EXCLUDED.execution_id,
			cursor_start  = CASE WHEN EXCLUDED.cursor_start <> '' THEN EXCLUDED.cursor_start ELSE platform_migration_batch_checkpoints.cursor_start END,
			cursor_end    = CASE WHEN EXCLUDED.cursor_end <> '' THEN EXCLUDED.cursor_end ELSE platform_migration_batch_checkpoints.cursor_end END,
			rows_affected = CASE WHEN EXCLUDED.rows_affected > 0 THEN EXCLUDED.rows_affected ELSE platform_migration_batch_checkpoints.rows_affected END,
			row_ids       = CASE WHEN jsonb_array_length(EXCLUDED.row_ids) > 0 THEN EXCLUDED.row_ids ELSE platform_migration_batch_checkpoints.row_ids END,
			recovery_data = CASE WHEN jsonb_array_length(EXCLUDED.recovery_data) > 0 THEN EXCLUDED.recovery_data ELSE platform_migration_batch_checkpoints.recovery_data END,
			status        = EXCLUDED.status,
			attempt       = GREATEST(platform_migration_batch_checkpoints.attempt, EXCLUDED.attempt),
			error_message = EXCLUDED.error_message,
			sqlstate      = EXCLUDED.sqlstate,
			duration_ms   = CASE WHEN EXCLUDED.duration_ms > 0 THEN EXCLUDED.duration_ms ELSE platform_migration_batch_checkpoints.duration_ms END,
			committed_at  = COALESCE(EXCLUDED.committed_at, platform_migration_batch_checkpoints.committed_at),
			reverted_at   = COALESCE(EXCLUDED.reverted_at, platform_migration_batch_checkpoints.reverted_at),
			updated_at    = now()`,
		cp.ExecutionID, cp.RootExecutionID, cp.DatabaseID, cp.MigrationID,
		cp.BatchNumber, cp.CursorStart, cp.CursorEnd, cp.RowsAffected, string(rowIDsBytes), string(recBytes),
		cp.Status, maxInt(cp.Attempt, 1), errArg, stateArg, cp.DurationMS, cp.CommittedAt, cp.RevertedAt,
	)
	return err
}

func (e *Engine) markPlatformCheckpointRevertStatus(ctx context.Context, rootExecID int64, batchNum int, status, errMsg, sqlState string) error {
	var errArg, stateArg any
	if errMsg != "" {
		errArg = telemetry.MaskString(errMsg)
	}
	if sqlState != "" {
		stateArg = sqlState
	}
	_, err := e.PlatformDB.ExecContext(ctx, `
		UPDATE platform_migration_batch_checkpoints
		SET status        = $3,
		    error_message = $4,
		    sqlstate      = $5,
		    reverted_at   = CASE WHEN $3 = 'reverted' THEN now() ELSE reverted_at END,
		    updated_at    = now()
		WHERE root_execution_id = $1 AND batch_number = $2`,
		rootExecID, batchNum, status, errArg, stateArg,
	)
	return err
}

func (e *Engine) updateBatchProgress(ctx context.Context, execID int64, totalBatches, completedBatches int, rowsAffected int64, lastCursor, scopeMaxCursor string) {
	_, _ = e.PlatformDB.ExecContext(ctx, `
		UPDATE platform_migration_executions
		SET total_batches     = $2,
		    completed_batches = $3,
		    rows_affected     = $4,
		    last_cursor       = NULLIF($5, ''),
		    scope_max_cursor  = COALESCE(NULLIF($6, ''), scope_max_cursor)
		WHERE id = $1`,
		execID, totalBatches, completedBatches, rowsAffected, lastCursor, scopeMaxCursor,
	)
}

func (e *Engine) updateBatchRevertProgress(ctx context.Context, execID int64, totalBatches, revertedBatches int, revertedRows int64) {
	_, _ = e.PlatformDB.ExecContext(ctx, `
		UPDATE platform_migration_executions
		SET total_batches    = $2,
		    reverted_batches = $3,
		    reverted_rows    = $4
		WHERE id = $1`,
		execID, totalBatches, revertedBatches, revertedRows,
	)
}

func (e *Engine) commitEmptyReceipt(ctx context.Context, conn *sql.Conn, tables targetMetaTables, idempotencyKey string, execID, migID int64, action Action) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := recordReceiptInTx(ctx, tx, tables, idempotencyKey, execID, migID, action); err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "receipt_error")
		return err
	}
	return telemetry.CommitTx(ctx, tx)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (e *Engine) firstBatchMissingPayload(ctx context.Context, rootExecID int64, column string) (int, bool, error) {
	var batchNum int
	err := e.PlatformDB.QueryRowContext(ctx, `
		SELECT batch_number
		FROM platform_migration_batch_checkpoints
		WHERE root_execution_id = $1
		  AND status IN ('committed', 'revert_failed')
		  AND (`+column+` IS NULL OR `+column+` = '[]'::jsonb OR `+column+` = 'null'::jsonb)
		ORDER BY batch_number ASC
		LIMIT 1`, rootExecID,
	).Scan(&batchNum)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return batchNum, true, nil
}

func (e *Engine) loadBatchPayload(ctx context.Context, rootExecID int64, batchNum int) ([]string, json.RawMessage, error) {
	var rowIDBytes, recBytes []byte
	err := e.PlatformDB.QueryRowContext(ctx, `
		SELECT row_ids, recovery_data
		FROM platform_migration_batch_checkpoints
		WHERE root_execution_id = $1 AND batch_number = $2`, rootExecID, batchNum,
	).Scan(&rowIDBytes, &recBytes)
	if err != nil {
		return nil, nil, err
	}
	var rowIDs []string
	if len(rowIDBytes) > 0 {
		_ = json.Unmarshal(rowIDBytes, &rowIDs)
	}
	return rowIDs, json.RawMessage(recBytes), nil
}
