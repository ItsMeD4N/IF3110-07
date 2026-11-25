package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	maxBatchSize = 100000
)

var validSQLIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateDefinitionInput(name, sqlUp, sqlDown string, isIrreversible bool, irreversibleReason string, mode ExecutionMode, batchCfg *BatchConfig) error {
	name = strings.TrimSpace(name)
	sqlUp = strings.TrimSpace(sqlUp)
	sqlDown = strings.TrimSpace(sqlDown)
	irreversibleReason = strings.TrimSpace(irreversibleReason)

	if sqlUp == "" {
		return &ValidationError{"SQL up migration tidak boleh kosong."}
	}
	if len(sqlUp) > maxSQLBytes {
		return &ValidationError{"SQL up migration maksimal 1 MiB."}
	}
	if len(name) > maxNameLen {
		return &ValidationError{"Nama migration maksimal 200 karakter."}
	}
	if err := checkTransactionControl(sqlUp); err != nil {
		return &ValidationError{"SQL up: " + err.Error()}
	}

	if isIrreversible {
		if irreversibleReason == "" {
			return &ValidationError{"Alasan wajib diisi untuk migration yang ditandai irreversible."}
		}
	} else {
		if sqlDown == "" {
			return &ValidationError{"SQL down wajib diisi, atau tandai migration sebagai irreversible beserta alasannya."}
		}
		if len(sqlDown) > maxSQLBytes {
			return &ValidationError{"SQL down migration maksimal 1 MiB."}
		}
		if err := checkTransactionControl(sqlDown); err != nil {
			return &ValidationError{"SQL down: " + err.Error()}
		}
	}

	if mode == "" {
		mode = ModeNormal
	}
	switch mode {
	case ModeNormal:
		return nil
	case ModeBatch:
		if batchCfg == nil {
			return &ValidationError{"batch_config wajib diisi untuk mode eksekusi batch."}
		}
		if err := validateBatchConfig(*batchCfg, sqlUp, sqlDown, isIrreversible); err != nil {
			return err
		}
		return nil
	default:
		return &ValidationError{fmt.Sprintf("execution_mode %q tidak valid (gunakan normal atau batch).", mode)}
	}
}

func validateBatchConfig(cfg BatchConfig, sqlUp, sqlDown string, isIrreversible bool) error {
	schema := cfg.NormalizedSchema()
	if !validSQLIdentRe.MatchString(schema) {
		return &ValidationError{"Nama schema batch tidak valid."}
	}
	if !validSQLIdentRe.MatchString(cfg.Table) {
		return &ValidationError{"Nama tabel batch tidak valid."}
	}
	if !validSQLIdentRe.MatchString(cfg.CursorColumn) {
		return &ValidationError{"Nama kolom cursor batch tidak valid."}
	}
	if cfg.BatchSize < 1 || cfg.BatchSize > maxBatchSize {
		return &ValidationError{fmt.Sprintf("batch_size harus antara 1 dan %d.", maxBatchSize)}
	}
	if cfg.BatchDelayMS < 0 || cfg.BatchDelayMS > 60000 {
		return &ValidationError{"batch_delay_ms harus antara 0 dan 60000."}
	}
	for _, col := range cfg.CaptureColumns {
		if !validSQLIdentRe.MatchString(col) {
			return &ValidationError{fmt.Sprintf("Nama kolom capture %q tidak valid.", col)}
		}
	}
	if cfg.WhereClause != "" {
		if err := checkTransactionControl("SELECT 1 WHERE " + cfg.WhereClause); err != nil {
			return &ValidationError{"where_clause tidak valid."}
		}
		if len(splitStatements(cfg.WhereClause)) > 1 {
			return &ValidationError{"where_clause tidak boleh berisi beberapa statement."}
		}
	}
	if !hasBatchScopePlaceholder(sqlUp) {
		return &ValidationError{"SQL up batch harus menggunakan minimal satu placeholder cakupan batch ({{batch_ids}}, {{cursor_start}}, {{cursor_end}}, atau {{last_cursor}})."}
	}
	if !isIrreversible && sqlDown != "" && !hasBatchScopePlaceholder(sqlDown) && !strings.Contains(sqlDown, "{{recovery_json}}") {
		return &ValidationError{"SQL down batch harus menggunakan placeholder cakupan batch ({{batch_ids}}, {{cursor_start}}, {{cursor_end}}, atau {{recovery_json}})."}
	}
	return nil
}

func hasBatchScopePlaceholder(sqlText string) bool {
	for _, ph := range []string{"{{batch_ids}}", "{{cursor_start}}", "{{cursor_end}}", "{{last_cursor}}"} {
		if strings.Contains(sqlText, ph) {
			return true
		}
	}
	return false
}

const definitionColumns = `id, database_id, version, name, sql_up, sql_down,
	is_irreversible, irreversible_reason, execution_mode, batch_config,
	workflow_status, is_locked_immutable, revision, created_by, updated_by, created_at, updated_at`

func scanDefinition(row interface{ Scan(...any) error }) (Definition, error) {
	var d Definition
	var mode string
	var batchBytes []byte
	err := row.Scan(
		&d.ID, &d.DatabaseID, &d.Version, &d.Name, &d.SQLUp, &d.SQLDown,
		&d.IsIrreversible, &d.IrreversibleReason, &mode, &batchBytes,
		&d.WorkflowStatus, &d.IsLockedImmutable, &d.Revision, &d.CreatedBy, &d.UpdatedBy, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Definition{}, ErrMigrationNotFound
		}
		return Definition{}, err
	}
	d.ExecutionMode = ExecutionMode(mode)
	if len(batchBytes) > 0 && string(batchBytes) != "null" {
		var bc BatchConfig
		if err := json.Unmarshal(batchBytes, &bc); err == nil {
			d.BatchConfig = &bc
		}
	}
	return d, nil
}

const executionColumns = `id, database_id, migration_id, migration_version, migration_revision, migration_name,
	action, execution_mode, status, sql_executed, actor,
	reverted_execution_id, parent_execution_id, COALESCE(job_id::text, ''), attempt,
	total_batches, completed_batches, rows_affected, reverted_batches, reverted_rows,
	COALESCE(last_cursor, ''), COALESCE(scope_max_cursor, ''),
	COALESCE(error_message, ''), COALESCE(sqlstate, ''),
	correlation_id, trace_id, started_at, finished_at, created_at`

func scanExecution(row interface{ Scan(...any) error }) (Execution, error) {
	var e Execution
	var action, mode, status string
	var revID, parentID sql.NullInt64
	var startedAt, finishedAt sql.NullTime

	err := row.Scan(
		&e.ID, &e.DatabaseID, &e.MigrationID, &e.MigrationVersion, &e.MigrationRevision, &e.MigrationName,
		&action, &mode, &status, &e.SQLExecuted, &e.Actor,
		&revID, &parentID, &e.JobID, &e.Attempt,
		&e.TotalBatches, &e.CompletedBatches, &e.RowsAffected, &e.RevertedBatches, &e.RevertedRows,
		&e.LastCursor, &e.ScopeMaxCursor,
		&e.Error, &e.SQLState,
		&e.CorrelationID, &e.TraceID, &startedAt, &finishedAt, &e.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Execution{}, ErrExecutionNotFound
		}
		return Execution{}, err
	}
	e.Action = Action(action)
	e.ExecutionMode = ExecutionMode(mode)
	e.Status = ExecutionStatus(status)
	if revID.Valid {
		v := revID.Int64
		e.RevertedExecutionID = &v
	}
	if parentID.Valid {
		v := parentID.Int64
		e.ParentExecutionID = &v
	}
	if startedAt.Valid {
		t := startedAt.Time
		e.StartedAt = &t
	}
	if finishedAt.Valid {
		t := finishedAt.Time
		e.FinishedAt = &t
	}
	return e, nil
}

func (e *Engine) CreateDefinition(ctx context.Context, databaseID int64, actor string, in CreateDefinitionInput) (Definition, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.SQLUp = strings.TrimSpace(in.SQLUp)
	in.SQLDown = strings.TrimSpace(in.SQLDown)
	in.IrreversibleReason = strings.TrimSpace(in.IrreversibleReason)
	if in.ExecutionMode == "" {
		in.ExecutionMode = ModeNormal
	}
	if err := validateDefinitionInput(in.Name, in.SQLUp, in.SQLDown, in.IsIrreversible, in.IrreversibleReason, in.ExecutionMode, in.BatchConfig); err != nil {
		return Definition{}, err
	}

	wfStatus := strings.TrimSpace(in.WorkflowStatus)
	if wfStatus == "" {
		if e.RequireApproval {
			wfStatus = "draft"
		} else {
			wfStatus = "approved"
		}
	}
	switch wfStatus {
	case "draft", "pending_review", "approved":
	default:
		return Definition{}, &ValidationError{"workflow_status awal harus draft, pending_review, atau approved."}
	}

	var batchArg any
	if in.BatchConfig != nil {
		b, err := json.Marshal(in.BatchConfig)
		if err != nil {
			return Definition{}, err
		}
		batchArg = string(b)
	}

	tx, err := e.PlatformDB.BeginTx(ctx, nil)
	if err != nil {
		return Definition{}, err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(31100710, $1::int)`, databaseID); err != nil {
		return Definition{}, err
	}

	def, err := scanDefinition(tx.QueryRowContext(ctx, `
		INSERT INTO platform_migrations (
			database_id, version, name, sql_up, sql_down,
			is_irreversible, irreversible_reason, execution_mode, batch_config,
			workflow_status, created_by, updated_by
		)
		SELECT $1, COALESCE(MAX(version), 0) + 1, $2, $3, $4,
		       $5, $6, $7, $8::jsonb,
		       $9, $10, $10
		FROM platform_migrations WHERE database_id = $1
		RETURNING `+definitionColumns,
		databaseID, in.Name, in.SQLUp, in.SQLDown,
		in.IsIrreversible, in.IrreversibleReason, string(in.ExecutionMode), batchArg,
		wfStatus, actor,
	))
	if err != nil {
		return Definition{}, fmt.Errorf("migration: buat definisi: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Definition{}, err
	}
	return def, nil
}

func (e *Engine) UpdateDefinition(ctx context.Context, databaseID, migrationID int64, actor string, in UpdateDefinitionInput) (Definition, error) {
	existing, err := e.GetDefinitionRaw(ctx, databaseID, migrationID)
	if err != nil {
		return Definition{}, err
	}
	if existing.IsLockedImmutable {
		return Definition{}, ErrImmutableMigration
	}

	in.Name = strings.TrimSpace(in.Name)
	in.SQLUp = strings.TrimSpace(in.SQLUp)
	in.SQLDown = strings.TrimSpace(in.SQLDown)
	in.IrreversibleReason = strings.TrimSpace(in.IrreversibleReason)
	if in.ExecutionMode == "" {
		in.ExecutionMode = existing.ExecutionMode
	}
	if err := validateDefinitionInput(in.Name, in.SQLUp, in.SQLDown, in.IsIrreversible, in.IrreversibleReason, in.ExecutionMode, in.BatchConfig); err != nil {
		return Definition{}, err
	}

	var batchArg any
	if in.BatchConfig != nil {
		b, err := json.Marshal(in.BatchConfig)
		if err != nil {
			return Definition{}, err
		}
		batchArg = string(b)
	}

	def, err := scanDefinition(e.PlatformDB.QueryRowContext(ctx, `
		UPDATE platform_migrations
		SET name = $3,
		    sql_up = $4,
		    sql_down = $5,
		    is_irreversible = $6,
		    irreversible_reason = $7,
		    execution_mode = $8,
		    batch_config = $9::jsonb,
		    revision = revision + 1,
		    updated_by = $10,
		    updated_at = now()
		WHERE database_id = $1 AND id = $2 AND is_locked_immutable = FALSE
		RETURNING `+definitionColumns,
		databaseID, migrationID, in.Name, in.SQLUp, in.SQLDown,
		in.IsIrreversible, in.IrreversibleReason, string(in.ExecutionMode), batchArg, actor,
	))
	if errors.Is(err, ErrMigrationNotFound) {
		if cur, gErr := e.GetDefinitionRaw(ctx, databaseID, migrationID); gErr == nil && cur.IsLockedImmutable {
			return Definition{}, ErrImmutableMigration
		}
		return Definition{}, ErrMigrationNotFound
	}
	if err != nil {
		return Definition{}, err
	}
	return def, nil
}

func (e *Engine) SetWorkflowStatus(ctx context.Context, databaseID, migrationID int64, status string) error {
	res, err := e.PlatformDB.ExecContext(ctx, `
		UPDATE platform_migrations SET workflow_status = $3, updated_at = now()
		WHERE database_id = $1 AND id = $2`,
		databaseID, migrationID, status,
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrMigrationNotFound
	}
	return nil
}

func (e *Engine) lockDefinitionImmutable(ctx context.Context, databaseID, migrationID int64) error {
	_, err := e.PlatformDB.ExecContext(ctx, `
		UPDATE platform_migrations
		SET is_locked_immutable = TRUE, updated_at = now()
		WHERE database_id = $1 AND id = $2`,
		databaseID, migrationID,
	)
	return err
}

func (e *Engine) GetDefinitionRaw(ctx context.Context, databaseID, migrationID int64) (Definition, error) {
	return scanDefinition(e.PlatformDB.QueryRowContext(ctx,
		`SELECT `+definitionColumns+` FROM platform_migrations WHERE database_id = $1 AND id = $2`,
		databaseID, migrationID,
	))
}

type migrationStateSummary struct {
	activeStack      []Execution
	activeByMigID    map[int64]Execution
	rolledBackMigIDs map[int64]bool
	nextForwardMigID int64
	latestActiveExec *Execution
}

func (e *Engine) computeState(ctx context.Context, databaseID int64) (migrationStateSummary, error) {
	execs, err := e.listExecutionsAsc(ctx, databaseID)
	if err != nil {
		return migrationStateSummary{}, err
	}
	defs, err := e.listDefinitionsAsc(ctx, databaseID)
	if err != nil {
		return migrationStateSummary{}, err
	}
	return deriveMigrationState(defs, execs), nil
}

func deriveMigrationState(defs []Definition, execs []Execution) migrationStateSummary {
	revertedExecIDs := map[int64]bool{}
	rolledBackMigIDs := map[int64]bool{}

	for _, ex := range execs {
		if (ex.Action == ActionRollback || ex.Action == ActionBatchRevert) && ex.Status == ExecSuccess {
			if ex.RevertedExecutionID != nil {
				revertedExecIDs[*ex.RevertedExecutionID] = true
			}
			rolledBackMigIDs[ex.MigrationID] = true
		}
	}

	activeStack := []Execution{}
	activeByMigID := map[int64]Execution{}
	var maxActiveVersion int64

	for _, ex := range execs {
		switch ex.Action {
		case ActionUp, ActionForward, ActionBatchUp, ActionBatchResume:
			if ex.Status != ExecSuccess {
				continue
			}
			if revertedExecIDs[ex.ID] {
				continue
			}
			activeStack = append(activeStack, ex)
			activeByMigID[ex.MigrationID] = ex
			if ex.MigrationVersion > maxActiveVersion {
				maxActiveVersion = ex.MigrationVersion
			}
		}
	}

	var latestActive *Execution
	if len(activeStack) > 0 {
		last := activeStack[len(activeStack)-1]
		latestActive = &last
	}

	var nextForwardMigID int64
	for _, d := range defs {
		if d.Version <= maxActiveVersion {
			continue
		}
		if _, isActive := activeByMigID[d.ID]; isActive {
			continue
		}
		if rolledBackMigIDs[d.ID] {
			nextForwardMigID = d.ID
			break
		}
	}

	return migrationStateSummary{
		activeStack:      activeStack,
		activeByMigID:    activeByMigID,
		rolledBackMigIDs: rolledBackMigIDs,
		nextForwardMigID: nextForwardMigID,
		latestActiveExec: latestActive,
	}
}

func (e *Engine) listDefinitionsAsc(ctx context.Context, databaseID int64) ([]Definition, error) {
	rows, err := e.PlatformDB.QueryContext(ctx,
		`SELECT `+definitionColumns+` FROM platform_migrations WHERE database_id = $1 ORDER BY version ASC`,
		databaseID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		d, err := scanDefinition(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (e *Engine) listExecutionsAsc(ctx context.Context, databaseID int64) ([]Execution, error) {
	rows, err := e.PlatformDB.QueryContext(ctx,
		`SELECT `+executionColumns+` FROM platform_migration_executions WHERE database_id = $1 ORDER BY id ASC`,
		databaseID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Execution{}
	for rows.Next() {
		ex, err := scanExecution(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ex)
	}
	return out, rows.Err()
}

func enrichDefinition(d Definition, st migrationStateSummary) Definition {
	if ex, ok := st.activeByMigID[d.ID]; ok {
		d.IsActive = true
		d.ActiveExecutionID = ex.ID
		if st.latestActiveExec != nil && st.latestActiveExec.ID == ex.ID && !d.IsIrreversible && strings.TrimSpace(d.SQLDown) != "" {
			d.CanRollback = true
		}
	} else if st.nextForwardMigID == d.ID {
		d.CanForward = true
	}
	return d
}

func (e *Engine) GetDefinition(ctx context.Context, databaseID, migrationID int64) (Definition, error) {
	d, err := e.GetDefinitionRaw(ctx, databaseID, migrationID)
	if err != nil {
		return Definition{}, err
	}
	st, err := e.computeState(ctx, databaseID)
	if err != nil {
		return Definition{}, err
	}
	return enrichDefinition(d, st), nil
}

func (e *Engine) ListDefinitions(ctx context.Context, databaseID int64) ([]Definition, error) {
	defs, err := e.listDefinitionsAsc(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	execs, err := e.listExecutionsAsc(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	st := deriveMigrationState(defs, execs)
	out := make([]Definition, len(defs))
	for i := range defs {
		out[len(defs)-1-i] = enrichDefinition(defs[i], st)
	}
	return out, nil
}

func FilterHideRollback(execs []Execution) []Execution {
	cancelledExecIDs := map[int64]bool{}
	rollbackPairIDs := map[int64]bool{}

	for _, ex := range execs {
		if (ex.Action == ActionRollback || ex.Action == ActionBatchRevert) &&
			ex.Status == ExecSuccess &&
			ex.RevertedExecutionID != nil {
			cancelledExecIDs[*ex.RevertedExecutionID] = true
			rollbackPairIDs[ex.ID] = true
		}
	}

	out := make([]Execution, 0, len(execs))
	for _, ex := range execs {
		if cancelledExecIDs[ex.ID] || rollbackPairIDs[ex.ID] {
			continue
		}
		out = append(out, ex)
	}
	return out
}

func (e *Engine) ListExecutions(ctx context.Context, databaseID int64, hideRollback bool) ([]Execution, error) {
	execs, err := e.listExecutionsAsc(ctx, databaseID)
	if err != nil {
		return nil, err
	}
	if hideRollback {
		execs = FilterHideRollback(execs)
	}
	for i, j := 0, len(execs)-1; i < j; i, j = i+1, j-1 {
		execs[i], execs[j] = execs[j], execs[i]
	}
	return execs, nil
}

func (e *Engine) GetExecution(ctx context.Context, databaseID, executionID int64) (Execution, error) {
	ex, err := scanExecution(e.PlatformDB.QueryRowContext(ctx,
		`SELECT `+executionColumns+` FROM platform_migration_executions WHERE database_id = $1 AND id = $2`,
		databaseID, executionID,
	))
	if err != nil {
		return Execution{}, err
	}
	if ex.ExecutionMode == ModeBatch {
		rootID := ex.ID
		if ex.ParentExecutionID != nil && *ex.ParentExecutionID > 0 {
			rootID = e.resolveRootExecutionID(ctx, *ex.ParentExecutionID)
		}
		cps, err := e.ListBatchCheckpoints(ctx, rootID)
		if err == nil {
			ex.Checkpoints = cps
		}
	}
	return ex, nil
}

func (e *Engine) resolveRootExecutionID(ctx context.Context, executionID int64) int64 {
	cur := executionID
	for i := 0; i < 32; i++ {
		var parent sql.NullInt64
		err := e.PlatformDB.QueryRowContext(ctx,
			`SELECT parent_execution_id FROM platform_migration_executions WHERE id = $1`, cur,
		).Scan(&parent)
		if err != nil || !parent.Valid || parent.Int64 == 0 {
			return cur
		}
		cur = parent.Int64
	}
	return cur
}

func (e *Engine) ListBatchCheckpoints(ctx context.Context, rootExecutionID int64) ([]BatchCheckpoint, error) {
	rows, err := e.PlatformDB.QueryContext(ctx, `
		SELECT id, execution_id, root_execution_id, database_id, migration_id,
		       batch_number, cursor_start, cursor_end, rows_affected,
		       status, attempt, COALESCE(error_message, ''), COALESCE(sqlstate, ''),
		       duration_ms, committed_at, reverted_at, updated_at
		FROM platform_migration_batch_checkpoints
		WHERE root_execution_id = $1
		ORDER BY batch_number ASC`, rootExecutionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BatchCheckpoint{}
	for rows.Next() {
		var cp BatchCheckpoint
		var commAt, revAt sql.NullTime
		if err := rows.Scan(
			&cp.ID, &cp.ExecutionID, &cp.RootExecutionID, &cp.DatabaseID, &cp.MigrationID,
			&cp.BatchNumber, &cp.CursorStart, &cp.CursorEnd, &cp.RowsAffected,
			&cp.Status, &cp.Attempt, &cp.Error, &cp.SQLState,
			&cp.DurationMS, &commAt, &revAt, &cp.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if commAt.Valid {
			t := commAt.Time
			cp.CommittedAt = &t
		}
		if revAt.Valid {
			t := revAt.Time
			cp.RevertedAt = &t
		}
		out = append(out, cp)
	}
	return out, rows.Err()
}
