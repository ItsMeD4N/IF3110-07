package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/dblock"
	"github.com/ItsMeD4N/IF3110-07/server/internal/metrics"
	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
)

type EngineOptions struct {
	ApprovalChecker ApprovalChecker
	RequireApproval bool
	LockTTL         time.Duration
	LockHeartbeat   time.Duration
	BatchBackoff    queue.BackoffPolicy
}

type Engine struct {
	PlatformDB      *sql.DB
	Conns           *connmgr.Manager
	Locker          dblock.Locker
	Broker          queue.Broker
	ApprovalChecker ApprovalChecker
	RequireApproval bool
	LockTTL         time.Duration
	LockHeartbeat   time.Duration
	BatchBackoff    queue.BackoffPolicy
	
	Workflow DefinitionChangeListener

	BatchBeforeExecHook    func(ctx context.Context, execID int64, batchNum int, action Action) error
	BeforeNormalCommitHook func(ctx context.Context, execID int64, action Action) error
}

func NewEngine(platformDB *sql.DB, conns *connmgr.Manager, locker dblock.Locker, broker queue.Broker, opts EngineOptions) *Engine {
	if opts.LockTTL <= 0 {
		opts.LockTTL = dblock.DefaultLeaseTTL
	}
	if opts.LockHeartbeat <= 0 {
		opts.LockHeartbeat = opts.LockTTL / 3
		if opts.LockHeartbeat < 100*time.Millisecond {
			opts.LockHeartbeat = 100 * time.Millisecond
		}
	}
	if opts.BatchBackoff.BaseDelay <= 0 {
		opts.BatchBackoff = queue.DefaultBackoffPolicy()
	}
	return &Engine{
		PlatformDB:      platformDB,
		Conns:           conns,
		Locker:          locker,
		Broker:          broker,
		ApprovalChecker: opts.ApprovalChecker,
		RequireApproval: opts.RequireApproval,
		LockTTL:         opts.LockTTL,
		LockHeartbeat:   opts.LockHeartbeat,
		BatchBackoff:    opts.BatchBackoff,
	}
}

func (e *Engine) checkApproval(ctx context.Context, def Definition, action Action) error {
	if e.ApprovalChecker != nil {
		return e.ApprovalChecker.CheckApproval(ctx, def.DatabaseID, def.ID, action)
	}
	if e.RequireApproval {
		switch def.WorkflowStatus {
		case "approved", "queued", "running", "applied", "rolled_back", "partial", "partially_reverted", "failed":
			return nil
		default:
			return ErrApprovalRequired
		}
	}
	return nil
}

func (e *Engine) EnqueueExecute(ctx context.Context, databaseID, migrationID int64, actor string, opts EnqueueOptions) (job queue.Job, dedup bool, err error) {
	def, err := e.GetDefinition(ctx, databaseID, migrationID)
	if err != nil {
		return queue.Job{}, false, err
	}
	action := ActionUp
	if def.ExecutionMode == ModeBatch {
		action = ActionBatchUp
	}
	if def.IsActive {
		return queue.Job{}, false, ErrAlreadyActive
	}
	if err := e.checkNotBlocked(ctx, databaseID, 0); err != nil {
		return queue.Job{}, false, err
	}
	ctx, claim, err := e.claimApproval(ctx, def, action, &opts)
	if err != nil {
		return queue.Job{}, false, err
	}
	defer func() { claim.settle(ctx, job, err) }()
	if err := e.lockDefinitionImmutable(ctx, databaseID, migrationID); err != nil {
		return queue.Job{}, false, err
	}
	_ = e.SetWorkflowStatus(ctx, databaseID, migrationID, "queued")

	return e.Broker.Enqueue(ctx, queue.EnqueueRequest{
		DatabaseID:     databaseID,
		MigrationID:    migrationID,
		Action:         string(action),
		Actor:          actor,
		IdempotencyKey: opts.IdempotencyKey,
		MaxAttempts:    opts.MaxAttempts,
		Payload:        JobPayload{},
	})
}

func (e *Engine) EnqueueRollback(ctx context.Context, databaseID, migrationID int64, actor string, opts EnqueueOptions) (job queue.Job, dedup bool, err error) {
	def, err := e.GetDefinition(ctx, databaseID, migrationID)
	if err != nil {
		return queue.Job{}, false, err
	}
	if def.IsIrreversible || strings.TrimSpace(def.SQLDown) == "" {
		return queue.Job{}, false, ErrIrreversibleMigration
	}
	if !def.CanRollback || def.ActiveExecutionID == 0 {
		return queue.Job{}, false, ErrNotLatestActive
	}
	action := ActionRollback
	payload := JobPayload{TargetExecutionID: def.ActiveExecutionID}
	if def.ExecutionMode == ModeBatch {
		action = ActionBatchRevert
		payload.RootExecutionID = e.resolveRootExecutionID(ctx, def.ActiveExecutionID)
	}
	if err := e.checkNotBlocked(ctx, databaseID, 0); err != nil {
		return queue.Job{}, false, err
	}
	ctx, claim, err := e.claimApproval(ctx, def, action, &opts)
	if err != nil {
		return queue.Job{}, false, err
	}
	defer func() { claim.settle(ctx, job, err) }()
	if err := e.lockDefinitionImmutable(ctx, databaseID, migrationID); err != nil {
		return queue.Job{}, false, err
	}

	return e.Broker.Enqueue(ctx, queue.EnqueueRequest{
		DatabaseID:     databaseID,
		MigrationID:    migrationID,
		Action:         string(action),
		Actor:          actor,
		IdempotencyKey: opts.IdempotencyKey,
		MaxAttempts:    opts.MaxAttempts,
		Payload:        payload,
	})
}

func (e *Engine) EnqueueForward(ctx context.Context, databaseID, migrationID int64, actor string, opts EnqueueOptions) (job queue.Job, dedup bool, err error) {
	def, err := e.GetDefinition(ctx, databaseID, migrationID)
	if err != nil {
		return queue.Job{}, false, err
	}
	if def.IsActive {
		return queue.Job{}, false, ErrAlreadyActive
	}
	if !def.CanForward {
		return queue.Job{}, false, ErrInvalidForwardOrder
	}
	action := ActionForward
	if err := e.checkNotBlocked(ctx, databaseID, 0); err != nil {
		return queue.Job{}, false, err
	}
	ctx, claim, err := e.claimApproval(ctx, def, action, &opts)
	if err != nil {
		return queue.Job{}, false, err
	}
	defer func() { claim.settle(ctx, job, err) }()
	if err := e.lockDefinitionImmutable(ctx, databaseID, migrationID); err != nil {
		return queue.Job{}, false, err
	}

	return e.Broker.Enqueue(ctx, queue.EnqueueRequest{
		DatabaseID:     databaseID,
		MigrationID:    migrationID,
		Action:         string(action),
		Actor:          actor,
		IdempotencyKey: opts.IdempotencyKey,
		MaxAttempts:    opts.MaxAttempts,
		Payload:        JobPayload{},
	})
}

func (e *Engine) EnqueueBatchResume(ctx context.Context, databaseID, executionID int64, actor string, opts EnqueueOptions) (job queue.Job, dedup bool, err error) {
	ex, err := e.GetExecution(ctx, databaseID, executionID)
	if err != nil {
		return queue.Job{}, false, err
	}
	if ex.ExecutionMode != ModeBatch || ex.Status != ExecPartial {
		return queue.Job{}, false, fmt.Errorf("%w: hanya batch execution berstatus partial yang dapat dilanjutkan", ErrInvalidState)
	}
	def, err := e.GetDefinitionRaw(ctx, databaseID, ex.MigrationID)
	if err != nil {
		return queue.Job{}, false, err
	}
	if err := e.checkNotBlocked(ctx, databaseID, ex.ID); err != nil {
		return queue.Job{}, false, err
	}
	ctx, claim, err := e.claimApproval(ctx, def, ActionBatchResume, &opts)
	if err != nil {
		return queue.Job{}, false, err
	}
	defer func() { claim.settle(ctx, job, err) }()

	rootID := e.resolveRootExecutionID(ctx, ex.ID)
	return e.Broker.Enqueue(ctx, queue.EnqueueRequest{
		DatabaseID:     databaseID,
		MigrationID:    def.ID,
		Action:         string(ActionBatchResume),
		Actor:          actor,
		IdempotencyKey: opts.IdempotencyKey,
		MaxAttempts:    opts.MaxAttempts,
		Payload: JobPayload{
			ParentExecutionID: ex.ID,
			RootExecutionID:   rootID,
		},
	})
}

func (e *Engine) EnqueueBatchRevert(ctx context.Context, databaseID, executionID int64, actor string, opts EnqueueOptions) (job queue.Job, dedup bool, err error) {
	ex, err := e.GetExecution(ctx, databaseID, executionID)
	if err != nil {
		return queue.Job{}, false, err
	}
	if ex.ExecutionMode != ModeBatch {
		return queue.Job{}, false, fmt.Errorf("%w: execution bukan mode batch", ErrInvalidState)
	}
	def, err := e.GetDefinition(ctx, databaseID, ex.MigrationID)
	if err != nil {
		return queue.Job{}, false, err
	}
	if def.IsIrreversible || strings.TrimSpace(def.SQLDown) == "" {
		return queue.Job{}, false, ErrUnsafeBatchRevert
	}

	payload := JobPayload{
		ParentExecutionID: ex.ID,
		RootExecutionID:   e.resolveRootExecutionID(ctx, ex.ID),
	}
	var allowBlocked int64
	switch ex.Status {
	case ExecPartial, ExecPartiallyReverted:
		allowBlocked = ex.ID
	case ExecSuccess:
		if !def.CanRollback || def.ActiveExecutionID != ex.ID {
			return queue.Job{}, false, ErrNotLatestActive
		}
		payload.TargetExecutionID = ex.ID
	default:
		return queue.Job{}, false, fmt.Errorf("%w: batch revert hanya untuk execution partial, partially_reverted, atau aktif terakhir", ErrInvalidState)
	}

	if err := e.checkNotBlocked(ctx, databaseID, allowBlocked); err != nil {
		return queue.Job{}, false, err
	}
	ctx, claim, err := e.claimApproval(ctx, def, ActionBatchRevert, &opts)
	if err != nil {
		return queue.Job{}, false, err
	}
	defer func() { claim.settle(ctx, job, err) }()

	return e.Broker.Enqueue(ctx, queue.EnqueueRequest{
		DatabaseID:     databaseID,
		MigrationID:    def.ID,
		Action:         string(ActionBatchRevert),
		Actor:          actor,
		IdempotencyKey: opts.IdempotencyKey,
		MaxAttempts:    opts.MaxAttempts,
		Payload:        payload,
	})
}

func (e *Engine) checkNotBlocked(ctx context.Context, databaseID, allowExecID int64) error {
	info, err := e.Locker.Inspect(ctx, databaseID)
	if err != nil {
		return err
	}
	if !info.Locked {
		return nil
	}
	if info.State == dblock.StateBlockedPartial || info.State == dblock.StateBlockedUncertain {
		if allowExecID > 0 && info.ExecutionID == allowExecID {
			return nil
		}
		return &dblock.BlockedError{
			DatabaseID:  databaseID,
			State:       info.State,
			ExecutionID: info.ExecutionID,
			Reason:      info.Reason,
		}
	}
	return nil
}

func (e *Engine) HandleJob(ctx context.Context, job *queue.Job) queue.JobOutcome {
	action := Action(job.Action)
	var payload JobPayload
	if len(job.Payload) > 0 {
		_ = json.Unmarshal(job.Payload, &payload)
	}

	var allowBlocked int64
	if action == ActionBatchResume || action == ActionBatchRevert {
		if payload.ParentExecutionID > 0 {
			allowBlocked = payload.ParentExecutionID
		} else if payload.RootExecutionID > 0 {
			allowBlocked = payload.RootExecutionID
		}
	}

	ownerID := job.LockedBy
	if ownerID == "" {
		host, _ := os.Hostname()
		ownerID = fmt.Sprintf("%s-%s", host, uuid.NewString()[:6])
	}

	lockStart := time.Now()
	lease, err := e.Locker.Acquire(ctx, dblock.AcquireRequest{
		DatabaseID:              job.DatabaseID,
		OwnerID:                 ownerID,
		JobID:                   job.ID,
		TTL:                     e.LockTTL,
		AllowBlockedExecutionID: allowBlocked,
	})
	if err != nil {
		if errors.Is(err, dblock.ErrLockHeld) {
			metrics.LockWait("contended", time.Since(lockStart))
			return queue.JobOutcome{LockContended: true}
		}
		if errors.Is(err, dblock.ErrDatabaseBlocked) {
			metrics.LockWait("blocked", time.Since(lockStart))
		} else {
			metrics.LockWait("error", time.Since(lockStart))
		}
		return queue.JobOutcome{
			Status: queue.StatusFailed,
			Err:    err,
		}
	}

	leaseCtx, stopHeartbeat := dblock.StartHeartbeat(ctx, e.Locker, lease, e.LockTTL, e.LockHeartbeat)
	defer func() {
		stopHeartbeat()
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = e.Locker.Release(rctx, lease)
	}()

	defRaw, defErr := e.GetDefinitionRaw(leaseCtx, job.DatabaseID, job.MigrationID)
	if defErr != nil {
		return queue.JobOutcome{Status: queue.StatusFailed, Err: defErr}
	}

	targetDB, err := e.Conns.Pool(leaseCtx, job.DatabaseID)
	if err != nil {
		exec := e.insertExecutionAttempt(leaseCtx, defRaw, job, action, payload.TargetExecutionID, payload.ParentExecutionID, sqlForAction(defRaw, action))
		e.finishExecution(leaseCtx, &exec, ExecFailed, err.Error(), telemetry.ExtractSQLState(err))
		return queue.JobOutcome{
			Status:      queue.StatusFailed,
			ExecutionID: exec.ID,
			Err:         err,
			SQLState:    telemetry.ExtractSQLState(err),
			Retryable:   queue.IsTransientError(err) || errors.Is(err, connmgr.ErrConnectionFailed),
		}
	}

	conn, err := targetDB.Conn(leaseCtx)
	if err != nil {
		exec := e.insertExecutionAttempt(leaseCtx, defRaw, job, action, payload.TargetExecutionID, payload.ParentExecutionID, sqlForAction(defRaw, action))
		e.finishExecution(leaseCtx, &exec, ExecFailed, err.Error(), telemetry.ExtractSQLState(err))
		return queue.JobOutcome{
			Status:      queue.StatusFailed,
			ExecutionID: exec.ID,
			Err:         err,
			SQLState:    telemetry.ExtractSQLState(err),
			Retryable:   queue.IsTransientError(err),
		}
	}
	defer func() { _ = conn.Close() }()

	acquired, err := dblock.AcquireTargetAdvisory(leaseCtx, conn)
	if err != nil {
		exec := e.insertExecutionAttempt(leaseCtx, defRaw, job, action, payload.TargetExecutionID, payload.ParentExecutionID, sqlForAction(defRaw, action))
		e.finishExecution(leaseCtx, &exec, ExecFailed, err.Error(), telemetry.ExtractSQLState(err))
		return queue.JobOutcome{
			Status:      queue.StatusFailed,
			ExecutionID: exec.ID,
			Err:         err,
			Retryable:   queue.IsTransientError(err),
		}
	}
	if !acquired {
		metrics.LockWait("contended", time.Since(lockStart))
		return queue.JobOutcome{LockContended: true}
	}
	defer func() { dblock.ReleaseTargetAdvisory(context.WithoutCancel(ctx), conn) }()
	metrics.LockWait("acquired", time.Since(lockStart))

	tables, err := ensureTargetMeta(leaseCtx, conn)
	if err != nil {
		exec := e.insertExecutionAttempt(leaseCtx, defRaw, job, action, payload.TargetExecutionID, payload.ParentExecutionID, sqlForAction(defRaw, action))
		e.finishExecution(leaseCtx, &exec, ExecFailed, err.Error(), telemetry.ExtractSQLState(err))
		return queue.JobOutcome{
			Status:      queue.StatusFailed,
			ExecutionID: exec.ID,
			Err:         err,
			SQLState:    telemetry.ExtractSQLState(err),
			Retryable:   queue.IsTransientError(err),
		}
	}

	if alreadyCommitted, err := checkReceiptExists(leaseCtx, conn, tables, job.IdempotencyKey); err == nil && alreadyCommitted {
		telemetry.LogEvent(leaseCtx, slog.LevelWarn, "migration.duplicate_delivery_skipped",
			slog.Int64("migration_id", defRaw.ID),
			slog.String("job_id", job.ID),
			slog.String("idempotency_key", job.IdempotencyKey),
			slog.String("status", "already_committed"),
		)
		sqlCopy := defRaw.SQLUp
		if action == ActionRollback || action == ActionBatchRevert {
			sqlCopy = defRaw.SQLDown
		}
		execID := e.reconcileCommittedReceipt(leaseCtx, defRaw, job, action, payload.TargetExecutionID, payload.ParentExecutionID, sqlCopy)
		return queue.JobOutcome{Status: queue.StatusCompleted, ExecutionID: execID}
	}

	def, targetExecID, sqlToRun, revalErr := e.revalidateUnderLock(leaseCtx, job, action, payload)
	if revalErr != nil {
		execID := e.recordFailedValidationAttempt(leaseCtx, def, job, action, payload, sqlToRun, revalErr)
		return queue.JobOutcome{
			Status:      queue.StatusFailed,
			ExecutionID: execID,
			Err:         revalErr,
		}
	}

	_ = e.lockDefinitionImmutable(leaseCtx, def.DatabaseID, def.ID)

	exec := e.insertExecutionAttempt(leaseCtx, def, job, action, targetExecID, payload.ParentExecutionID, sqlToRun)
	if exec.ID == 0 {
		return queue.JobOutcome{
			Status:    queue.StatusFailed,
			Err:       errors.New("migration: gagal mencatat execution attempt ke database platform"),
			Retryable: true,
		}
	}
	_ = e.Locker.UpdateExecution(leaseCtx, lease, exec.ID)
	_, _ = e.PlatformDB.ExecContext(leaseCtx,
		`UPDATE platform_migration_jobs SET execution_id = $2, updated_at = now() WHERE id = $1::uuid`,
		job.ID, exec.ID,
	)
	_ = e.SetWorkflowStatus(leaseCtx, def.DatabaseID, def.ID, "running")

	execMeta := telemetry.ExecMetaFrom(leaseCtx)
	execMeta.MigrationID = def.ID
	execMeta.ExecutionID = exec.ID
	execMeta.JobID = job.ID
	execMeta.Attempt = job.Attempt
	execMeta.Action = string(action)
	execCtx := telemetry.WithExecMeta(leaseCtx, execMeta)

	switch {
	case action == ActionBatchUp || (action == ActionForward && def.ExecutionMode == ModeBatch):
		return e.executeBatchUpOrResume(execCtx, lease, targetDB, &conn, tables, def, &exec, job, payload, false)
	case action == ActionBatchResume:
		return e.executeBatchUpOrResume(execCtx, lease, targetDB, &conn, tables, def, &exec, job, payload, true)
	case action == ActionBatchRevert:
		return e.executeBatchRevert(execCtx, lease, targetDB, conn, tables, def, &exec, job, payload)
	default:
		return e.executeNormal(execCtx, lease, targetDB, conn, tables, def, &exec, job, action, sqlToRun)
	}
}

func (e *Engine) revalidateUnderLock(ctx context.Context, job *queue.Job, action Action, payload JobPayload) (Definition, int64, string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "migration.revalidate",
		trace.WithAttributes(
			attribute.Int64("db.database_id", job.DatabaseID),
			attribute.Int64("migration.id", job.MigrationID),
			attribute.String("migration.action", string(action)),
			attribute.String("job.id", job.ID),
		),
	)
	defer span.End()

	def, err := e.GetDefinition(ctx, job.DatabaseID, job.MigrationID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Definition{}, 0, "", err
	}

	if err := e.checkApproval(WithApprovalKey(ctx, job.IdempotencyKey), def, action); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return def, 0, def.SQLUp, err
	}

	switch action {
	case ActionUp, ActionBatchUp:
		if def.IsActive {
			span.RecordError(ErrAlreadyActive)
			span.SetStatus(codes.Error, ErrAlreadyActive.Error())
			return def, 0, def.SQLUp, ErrAlreadyActive
		}
		if err := checkTransactionControl(def.SQLUp); err != nil {
			return def, 0, def.SQLUp, &ValidationError{err.Error()}
		}
		return def, 0, def.SQLUp, nil

	case ActionRollback:
		if def.IsIrreversible || strings.TrimSpace(def.SQLDown) == "" {
			span.RecordError(ErrIrreversibleMigration)
			span.SetStatus(codes.Error, ErrIrreversibleMigration.Error())
			return def, 0, def.SQLDown, ErrIrreversibleMigration
		}
		if !def.CanRollback || def.ActiveExecutionID == 0 {
			span.RecordError(ErrNotLatestActive)
			span.SetStatus(codes.Error, ErrNotLatestActive.Error())
			return def, 0, def.SQLDown, ErrNotLatestActive
		}
		if payload.TargetExecutionID > 0 && payload.TargetExecutionID != def.ActiveExecutionID {
			span.RecordError(ErrNotLatestActive)
			span.SetStatus(codes.Error, ErrNotLatestActive.Error())
			return def, 0, def.SQLDown, ErrNotLatestActive
		}
		if err := checkTransactionControl(def.SQLDown); err != nil {
			return def, def.ActiveExecutionID, def.SQLDown, &ValidationError{err.Error()}
		}
		return def, def.ActiveExecutionID, def.SQLDown, nil

	case ActionForward:
		if def.IsActive {
			span.RecordError(ErrAlreadyActive)
			span.SetStatus(codes.Error, ErrAlreadyActive.Error())
			return def, 0, def.SQLUp, ErrAlreadyActive
		}
		if !def.CanForward {
			span.RecordError(ErrInvalidForwardOrder)
			span.SetStatus(codes.Error, ErrInvalidForwardOrder.Error())
			return def, 0, def.SQLUp, ErrInvalidForwardOrder
		}
		if err := checkTransactionControl(def.SQLUp); err != nil {
			return def, 0, def.SQLUp, &ValidationError{err.Error()}
		}
		return def, 0, def.SQLUp, nil

	case ActionBatchResume:
		if def.ExecutionMode != ModeBatch || def.BatchConfig == nil {
			return def, 0, def.SQLUp, ErrInvalidState
		}
		if payload.ParentExecutionID > 0 {
			parent, pErr := e.GetExecution(ctx, def.DatabaseID, payload.ParentExecutionID)
			if pErr != nil {
				return def, 0, def.SQLUp, pErr
			}
			if parent.Status != ExecPartial {
				return def, 0, def.SQLUp, fmt.Errorf("%w: execution #%d sudah berstatus %s", ErrInvalidState, parent.ID, parent.Status)
			}
		}
		return def, 0, def.SQLUp, nil

	case ActionBatchRevert:
		if def.IsIrreversible || strings.TrimSpace(def.SQLDown) == "" {
			return def, 0, def.SQLDown, ErrUnsafeBatchRevert
		}
		var targetID int64
		if payload.TargetExecutionID > 0 {
			if !def.CanRollback || def.ActiveExecutionID != payload.TargetExecutionID {
				return def, 0, def.SQLDown, ErrNotLatestActive
			}
			targetID = def.ActiveExecutionID
		}
		return def, targetID, def.SQLDown, nil

	default:
		return def, 0, "", fmt.Errorf("migration: action tidak dikenal: %s", action)
	}
}

func (e *Engine) executeNormal(
	ctx context.Context,
	lease *dblock.Lease,
	targetDB *sql.DB,
	conn *sql.Conn,
	tables targetMetaTables,
	def Definition,
	exec *Execution,
	job *queue.Job,
	action Action,
	sqlText string,
) queue.JobOutcome {
	ctx, span := telemetry.Tracer().Start(ctx, "migration.execute_normal",
		trace.WithAttributes(
			attribute.Int64("migration.id", def.ID),
			attribute.Int64("execution.id", exec.ID),
			attribute.String("migration.action", string(action)),
			attribute.Int("migration.attempt", job.Attempt),
		),
	)
	defer span.End()

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		sqlState := telemetry.ExtractSQLState(err)
		e.finishExecution(ctx, exec, ExecFailed, err.Error(), sqlState)
		return queue.JobOutcome{
			Status:      queue.StatusFailed,
			ExecutionID: exec.ID,
			Err:         err,
			SQLState:    sqlState,
			Retryable:   queue.IsTransientError(err),
		}
	}

	alreadyExists, err := recordReceiptInTx(ctx, tx, tables, job.IdempotencyKey, exec.ID, def.ID, action)
	if err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "receipt_insert_failed")
		sqlState := telemetry.ExtractSQLState(err)
		e.finishExecution(ctx, exec, ExecFailed, err.Error(), sqlState)
		return queue.JobOutcome{
			Status:      queue.StatusFailed,
			ExecutionID: exec.ID,
			Err:         err,
			SQLState:    sqlState,
			Retryable:   queue.IsTransientError(err),
		}
	}
	if alreadyExists {
		_ = telemetry.RollbackTx(ctx, tx, "duplicate_receipt_detected")
		e.finishExecution(ctx, exec, ExecSuccess, "", "")
		e.updateWorkflowAfterSuccess(ctx, def, action)
		return queue.JobOutcome{Status: queue.StatusCompleted, ExecutionID: exec.ID}
	}

	res, err := telemetry.ExecSQL(ctx, tx, sqlText)
	if err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "sql_execution_failed")
		sqlState := telemetry.ExtractSQLState(err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		e.finishExecution(ctx, exec, ExecFailed, err.Error(), sqlState)
		if action == ActionUp {
			_ = e.SetWorkflowStatus(ctx, def.DatabaseID, def.ID, "failed")
		}
		return queue.JobOutcome{
			Status:      queue.StatusFailed,
			ExecutionID: exec.ID,
			Err:         err,
			SQLState:    sqlState,
			Retryable:   queue.IsTransientError(err),
		}
	}

	if res != nil {
		if n, rErr := res.RowsAffected(); rErr == nil {
			_, _ = e.PlatformDB.ExecContext(ctx, `UPDATE platform_migration_executions SET rows_affected = $2 WHERE id = $1`, exec.ID, n)
			exec.RowsAffected = n
		}
	}

	if err := e.Locker.Verify(ctx, lease); err != nil {
		_ = telemetry.RollbackTx(ctx, tx, "lock_lease_lost_before_commit")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		e.finishExecution(ctx, exec, ExecFailed, err.Error(), "")
		return queue.JobOutcome{
			Status:      queue.StatusFailed,
			ExecutionID: exec.ID,
			Err:         err,
			Retryable:   true,
		}
	}

	var commitErr error
	if e.BeforeNormalCommitHook != nil {
		commitErr = e.BeforeNormalCommitHook(ctx, exec.ID, action)
		if commitErr != nil {
			_ = telemetry.RollbackTx(ctx, tx, "hook_commit_error")
		}
	}
	if commitErr == nil {
		commitErr = telemetry.CommitTx(ctx, tx)
	}

	if commitErr != nil {
		if queue.IsUncertainCommitError(commitErr) {
			return e.handleUncertainNormalCommit(ctx, lease, targetDB, tables, def, exec, job, action, commitErr)
		}
		sqlState := telemetry.ExtractSQLState(commitErr)
		span.RecordError(commitErr)
		span.SetStatus(codes.Error, commitErr.Error())
		e.finishExecution(ctx, exec, ExecFailed, commitErr.Error(), sqlState)
		return queue.JobOutcome{
			Status:      queue.StatusFailed,
			ExecutionID: exec.ID,
			Err:         commitErr,
			SQLState:    sqlState,
			Retryable:   queue.IsTransientError(commitErr),
		}
	}

	e.finishExecution(ctx, exec, ExecSuccess, "", "")
	e.updateWorkflowAfterSuccess(ctx, def, action)
	return queue.JobOutcome{Status: queue.StatusCompleted, ExecutionID: exec.ID}
}

func (e *Engine) handleUncertainNormalCommit(
	ctx context.Context,
	lease *dblock.Lease,
	targetDB *sql.DB,
	tables targetMetaTables,
	def Definition,
	exec *Execution,
	job *queue.Job,
	action Action,
	commitErr error,
) queue.JobOutcome {
	vctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	vconn, cErr := targetDB.Conn(vctx)
	if cErr == nil {
		defer vconn.Close()
		exists, qErr := checkReceiptExists(vctx, vconn, tables, job.IdempotencyKey)
		if qErr == nil {
			if exists {
				telemetry.LogEvent(ctx, slog.LevelInfo, "migration.uncertain_commit_verified_committed",
					slog.Int64("migration_id", def.ID),
					slog.Int64("execution_id", exec.ID),
					slog.String("job_id", job.ID),
					slog.String("status", "success"),
				)
				e.finishExecution(vctx, exec, ExecSuccess, "", "")
				e.updateWorkflowAfterSuccess(vctx, def, action)
				return queue.JobOutcome{Status: queue.StatusCompleted, ExecutionID: exec.ID}
			}
			telemetry.LogEvent(ctx, slog.LevelWarn, "migration.uncertain_commit_verified_rolled_back",
				slog.Int64("migration_id", def.ID),
				slog.Int64("execution_id", exec.ID),
				slog.String("job_id", job.ID),
				slog.String("status", "failed"),
			)
			e.finishExecution(vctx, exec, ExecFailed, commitErr.Error(), telemetry.ExtractSQLState(commitErr))
			return queue.JobOutcome{
				Status:      queue.StatusFailed,
				ExecutionID: exec.ID,
				Err:         commitErr,
				SQLState:    telemetry.ExtractSQLState(commitErr),
				Retryable:   true,
			}
		}
	}

	reason := fmt.Sprintf("hasil commit tidak dapat dipastikan setelah error: %s; jalankan rekonsiliasi sebelum migration berikutnya", commitErr.Error())
	e.finishExecution(vctx, exec, ExecUncertain, reason, telemetry.ExtractSQLState(commitErr))
	_ = e.SetWorkflowStatus(vctx, def.DatabaseID, def.ID, "uncertain")
	_ = e.Locker.TransitionToBlocked(vctx, lease, dblock.StateBlockedUncertain, exec.ID, reason)

	telemetry.LogEvent(ctx, slog.LevelError, "migration.commit_uncertain_blocked",
		slog.Int64("migration_id", def.ID),
		slog.Int64("execution_id", exec.ID),
		slog.String("job_id", job.ID),
		slog.String("reason", reason),
		slog.String("status", "uncertain"),
	)
	return queue.JobOutcome{
		Status:      queue.StatusUncertain,
		ExecutionID: exec.ID,
		Err:         commitErr,
		SQLState:    telemetry.ExtractSQLState(commitErr),
	}
}

func (e *Engine) RecoverExpiredJob(ctx context.Context, job *queue.Job) error {
	def, err := e.GetDefinitionRaw(ctx, job.DatabaseID, job.MigrationID)
	if err != nil {
		return err
	}

	ownerID := "recovery-" + uuid.NewString()[:6]
	lease, err := e.Locker.Acquire(ctx, dblock.AcquireRequest{
		DatabaseID:              job.DatabaseID,
		OwnerID:                 ownerID,
		JobID:                   job.ID,
		ExecutionID:             job.ExecutionID,
		TTL:                     e.LockTTL,
		AllowBlockedExecutionID: job.ExecutionID,
	})
	if err != nil {
		if errors.Is(err, dblock.ErrLockHeld) {
			return nil
		}
		return err
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = e.Locker.Release(rctx, lease)
	}()

	targetDB, err := e.Conns.Pool(ctx, job.DatabaseID)
	if err != nil {
		return e.markRecoveryUncertain(ctx, lease, def, job, fmt.Sprintf("worker berhenti dan target DB tidak dapat dijangkau untuk verifikasi bukti commit: %v", err))
	}
	conn, err := targetDB.Conn(ctx)
	if err != nil {
		return e.markRecoveryUncertain(ctx, lease, def, job, fmt.Sprintf("gagal membuka koneksi verifikasi bukti commit: %v", err))
	}
	defer conn.Close()

	tables, err := ensureTargetMeta(ctx, conn)
	if err != nil {
		return e.markRecoveryUncertain(ctx, lease, def, job, err.Error())
	}

	exists, err := checkReceiptExists(ctx, conn, tables, job.IdempotencyKey)
	if err != nil {
		return e.markRecoveryUncertain(ctx, lease, def, job, err.Error())
	}
	if exists {
		execID := job.ExecutionID
		if execID > 0 {
			ex, gErr := e.GetExecution(ctx, job.DatabaseID, execID)
			if gErr == nil {
				e.finishExecution(ctx, &ex, ExecSuccess, "", "")
			}
		} else {
			execID = e.reconcileCommittedReceipt(ctx, def, job, Action(job.Action), 0, 0, def.SQLUp)
		}
		e.updateWorkflowAfterSuccess(ctx, def, Action(job.Action))
		return e.Broker.Complete(ctx, job.ID, execID)
	}

	if def.ExecutionMode == ModeBatch && job.ExecutionID > 0 {
		rootID := e.resolveRootExecutionID(ctx, job.ExecutionID)
		cps, cpErr := listTargetBatchCheckpoints(ctx, conn, tables, rootID)
		if cpErr == nil && len(cps) > 0 {
			_ = e.reconcileTargetBatchCheckpoints(ctx, conn, tables, def, rootID, job.ExecutionID)
			ex, gErr := e.GetExecution(ctx, job.DatabaseID, job.ExecutionID)
			if gErr == nil {
				reason := fmt.Sprintf("worker berhenti di tengah batch setelah %d batch committed; lanjutkan dari checkpoint atau batalkan", len(cps))
				e.finishExecution(ctx, &ex, ExecPartial, reason, "")
				_ = e.SetWorkflowStatus(ctx, def.DatabaseID, def.ID, "partial")
				_ = e.Locker.TransitionToBlocked(ctx, lease, dblock.StateBlockedPartial, ex.ID, reason)
				return e.Broker.MarkStatus(ctx, job.ID, queue.StatusPartial, ex.ID, reason, "")
			}
		}
	}

	crashMsg := "worker berhenti sebelum commit; verifikasi bukti eksekusi memastikan tidak ada perubahan yang ter-commit"
	if job.ExecutionID > 0 {
		if ex, gErr := e.GetExecution(ctx, job.DatabaseID, job.ExecutionID); gErr == nil && ex.Status == ExecRunning {
			e.finishExecution(ctx, &ex, ExecFailed, crashMsg, "")
		}
	}
	if job.Attempt < job.MaxAttempts {
		nextAt := time.Now().UTC().Add(e.BatchBackoff.NextDelay(job.Attempt))
		return e.Broker.Retry(ctx, job.ID, job.ExecutionID, nextAt, crashMsg, "")
	}
	return e.Broker.MarkStatus(ctx, job.ID, queue.StatusFailed, job.ExecutionID, crashMsg, "")
}

func (e *Engine) ReconcileExecution(ctx context.Context, databaseID, executionID int64, actor string) (Execution, error) {
	ex, err := e.GetExecution(ctx, databaseID, executionID)
	if err != nil {
		return Execution{}, err
	}
	if ex.Status != ExecUncertain && ex.Status != ExecRunning {
		return ex, fmt.Errorf("%w: execution #%d tidak berstatus uncertain/running (status: %s)", ErrInvalidState, executionID, ex.Status)
	}
	def, err := e.GetDefinitionRaw(ctx, databaseID, ex.MigrationID)
	if err != nil {
		return Execution{}, err
	}
	var idempotencyKey string
	if ex.JobID != "" {
		if job, jErr := e.Broker.GetJobByID(ctx, ex.JobID); jErr == nil {
			idempotencyKey = job.IdempotencyKey
		}
	}

	targetDB, err := e.Conns.Pool(ctx, databaseID)
	if err != nil {
		return ex, err
	}
	conn, err := targetDB.Conn(ctx)
	if err != nil {
		return ex, err
	}
	defer conn.Close()

	tables, err := ensureTargetMeta(ctx, conn)
	if err != nil {
		return ex, err
	}

	if idempotencyKey != "" {
		exists, err := checkReceiptExists(ctx, conn, tables, idempotencyKey)
		if err != nil {
			return ex, err
		}
		if exists {
			e.finishExecution(ctx, &ex, ExecSuccess, "", "")
			e.updateWorkflowAfterSuccess(ctx, def, ex.Action)
			if ex.JobID != "" {
				_ = e.Broker.Complete(ctx, ex.JobID, ex.ID)
			}
			_ = e.Locker.ForceClearBlock(ctx, databaseID, ex.ID)
			telemetry.LogEvent(ctx, slog.LevelInfo, "migration.reconciled",
				slog.Int64("database_id", databaseID),
				slog.Int64("execution_id", ex.ID),
				slog.String("actor", actor),
				slog.String("status", "success"),
			)
			return e.GetExecution(ctx, databaseID, executionID)
		}
	}

	if ex.ExecutionMode == ModeBatch {
		rootID := e.resolveRootExecutionID(ctx, ex.ID)
		cps, err := listTargetBatchCheckpoints(ctx, conn, tables, rootID)
		if err != nil {
			return ex, err
		}
		if len(cps) > 0 {
			_ = e.reconcileTargetBatchCheckpoints(ctx, conn, tables, def, rootID, ex.ID)
			reason := fmt.Sprintf("rekonsiliasi menemukan %d batch committed; lanjutkan dari checkpoint atau batalkan", len(cps))
			e.finishExecution(ctx, &ex, ExecPartial, reason, "")
			_ = e.SetWorkflowStatus(ctx, databaseID, def.ID, "partial")
			if ex.JobID != "" {
				_ = e.Broker.MarkStatus(ctx, ex.JobID, queue.StatusPartial, ex.ID, reason, "")
			}
			_, _ = e.PlatformDB.ExecContext(ctx, `
				UPDATE platform_db_locks SET state = 'blocked_partial', reason = $3, heartbeat_at = now()
				WHERE database_id = $1 AND execution_id = $2`, databaseID, ex.ID, reason)
			return e.GetExecution(ctx, databaseID, executionID)
		}
	}

	msg := "rekonsiliasi memastikan perubahan belum ter-commit di database target; aman untuk dicoba ulang"
	e.finishExecution(ctx, &ex, ExecFailed, msg, "")
	if ex.JobID != "" {
		_ = e.Broker.MarkStatus(ctx, ex.JobID, queue.StatusFailed, ex.ID, msg, "")
	}
	_ = e.Locker.ForceClearBlock(ctx, databaseID, ex.ID)
	telemetry.LogEvent(ctx, slog.LevelInfo, "migration.reconciled",
		slog.Int64("database_id", databaseID),
		slog.Int64("execution_id", ex.ID),
		slog.String("actor", actor),
		slog.String("status", "failed"),
	)
	return e.GetExecution(ctx, databaseID, executionID)
}

func (e *Engine) markRecoveryUncertain(ctx context.Context, lease *dblock.Lease, def Definition, job *queue.Job, reason string) error {
	if job.ExecutionID > 0 {
		if ex, err := e.GetExecution(ctx, job.DatabaseID, job.ExecutionID); err == nil {
			e.finishExecution(ctx, &ex, ExecUncertain, reason, "")
		}
	}
	_ = e.SetWorkflowStatus(ctx, def.DatabaseID, def.ID, "uncertain")
	_ = e.Locker.TransitionToBlocked(ctx, lease, dblock.StateBlockedUncertain, job.ExecutionID, reason)
	return e.Broker.MarkStatus(ctx, job.ID, queue.StatusUncertain, job.ExecutionID, reason, "")
}

func (e *Engine) insertExecutionAttempt(ctx context.Context, def Definition, job *queue.Job, action Action, revertedExecID, parentExecID int64, sqlExecuted string) Execution {
	var revArg, parentArg, jobArg any
	if revertedExecID > 0 {
		revArg = revertedExecID
	}
	if parentExecID > 0 {
		parentArg = parentExecID
	}
	if job.ID != "" {
		jobArg = job.ID
	}
	attempt := job.Attempt
	if attempt < 1 {
		attempt = 1
	}

	ex, err := scanExecution(e.PlatformDB.QueryRowContext(ctx, `
		INSERT INTO platform_migration_executions (
			database_id, migration_id, migration_version, migration_revision, migration_name,
			action, execution_mode, status, sql_executed, actor,
			reverted_execution_id, parent_execution_id, job_id, attempt,
			correlation_id, trace_id, started_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, 'running', $8, $9,
			$10, $11, $12::uuid, $13,
			$14, $15, now()
		)
		RETURNING `+executionColumns,
		def.DatabaseID, def.ID, def.Version, def.Revision, def.Name,
		string(action), string(def.ExecutionMode), sqlExecuted, job.Actor,
		revArg, parentArg, jobArg, attempt,
		telemetry.CorrelationIDFrom(ctx), telemetry.TraceID(ctx),
	))
	if err != nil {
		telemetry.LogEvent(ctx, slog.LevelError, "migration.insert_execution_failed",
			slog.Int64("migration_id", def.ID),
			slog.String("job_id", job.ID),
			slog.String("error", err.Error()),
			slog.String("status", "error"),
		)
		return Execution{}
	}
	return ex
}

func (e *Engine) recordFailedValidationAttempt(ctx context.Context, def Definition, job *queue.Job, action Action, payload JobPayload, sqlText string, revalErr error) int64 {
	if def.ID == 0 {
		return 0
	}
	ex := e.insertExecutionAttempt(ctx, def, job, action, payload.TargetExecutionID, payload.ParentExecutionID, sqlText)
	if ex.ID > 0 {
		e.finishExecution(ctx, &ex, ExecFailed, revalErr.Error(), "")
	}
	return ex.ID
}

func (e *Engine) reconcileCommittedReceipt(ctx context.Context, def Definition, job *queue.Job, action Action, revertedExecID, parentExecID int64, sqlText string) int64 {
	var existingID int64
	err := e.PlatformDB.QueryRowContext(ctx,
		`SELECT id FROM platform_migration_executions WHERE job_id = $1::uuid AND status = 'success' ORDER BY id DESC LIMIT 1`,
		job.ID,
	).Scan(&existingID)
	if err == nil && existingID > 0 {
		return existingID
	}
	ex := e.insertExecutionAttempt(ctx, def, job, action, revertedExecID, parentExecID, sqlText)
	if ex.ID > 0 {
		e.finishExecution(ctx, &ex, ExecSuccess, "", "")
		e.updateWorkflowAfterSuccess(ctx, def, action)
	}
	return ex.ID
}

func (e *Engine) finishExecution(ctx context.Context, exec *Execution, status ExecutionStatus, errMsg, sqlState string) {
	if exec == nil || exec.ID == 0 {
		return
	}
	if ctx.Err() != nil {
		ctx = context.WithoutCancel(ctx)
	}
	var errArg, stateArg any
	if errMsg != "" {
		errArg = telemetry.MaskString(errMsg)
	}
	if sqlState != "" {
		stateArg = sqlState
	}
	var fin time.Time
	err := e.PlatformDB.QueryRowContext(ctx, `
		UPDATE platform_migration_executions
		SET status        = $2,
		    error_message = $3,
		    sqlstate      = $4,
		    finished_at   = now()
		WHERE id = $1
		RETURNING finished_at`,
		exec.ID, string(status), errArg, stateArg,
	).Scan(&fin)
	if err != nil {
		telemetry.LogEvent(ctx, slog.LevelError, "migration.finish_execution_failed",
			slog.Int64("execution_id", exec.ID),
			slog.String("status", string(status)),
			slog.String("error", err.Error()),
		)
		return
	}
	exec.Status = status
	exec.Error = errMsg
	exec.SQLState = sqlState
	exec.FinishedAt = &fin

	level := slog.LevelInfo
	if status == ExecFailed || status == ExecUncertain || status == ExecPartial || status == ExecPartiallyReverted {
		level = slog.LevelError
	}
	telemetry.LogEvent(ctx, level, "migration.execution_finished",
		slog.Int64("migration_id", exec.MigrationID),
		slog.Int64("migration_version", exec.MigrationVersion),
		slog.Int("migration_revision", exec.MigrationRevision),
		slog.Int64("execution_id", exec.ID),
		slog.String("job_id", exec.JobID),
		slog.String("action", string(exec.Action)),
		slog.Int("attempt", exec.Attempt),
		slog.Int64("rows_affected", exec.RowsAffected),
		slog.String("sqlstate", sqlState),
		slog.String("error", errMsg),
		slog.String("status", string(status)),
	)
}

func (e *Engine) updateWorkflowAfterSuccess(ctx context.Context, def Definition, action Action) {
	switch action {
	case ActionUp, ActionForward, ActionBatchUp, ActionBatchResume:
		_ = e.SetWorkflowStatus(ctx, def.DatabaseID, def.ID, "applied")
	case ActionRollback, ActionBatchRevert:
		_ = e.SetWorkflowStatus(ctx, def.DatabaseID, def.ID, "rolled_back")
	}
}

func sqlForAction(def Definition, action Action) string {
	if action == ActionRollback || action == ActionBatchRevert {
		return def.SQLDown
	}
	return def.SQLUp
}
