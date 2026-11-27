package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/ItsMeD4N/IF3110-07/server/internal/metrics"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
)

const notifyChannel = "platform_migration_jobs"

type PostgresBroker struct {
	db  *sql.DB
	dsn string

	mu       sync.Mutex
	subs     map[chan struct{}]struct{}
	listener *pq.Listener
	closed   bool
}

func NewPostgresBroker(db *sql.DB, dsn string) *PostgresBroker {
	b := &PostgresBroker{
		db:   db,
		dsn:  dsn,
		subs: map[chan struct{}]struct{}{},
	}
	if dsn != "" {
		b.startListener(dsn)
	}
	return b
}

func (b *PostgresBroker) startListener(dsn string) {
	l := pq.NewListener(dsn, 1*time.Second, 15*time.Second, func(ev pq.ListenerEventType, err error) {
		if err != nil {
			telemetry.LogEvent(context.Background(), slog.LevelWarn, "queue.listener_event",
				slog.String("error", telemetry.MaskString(err.Error())),
				slog.String("status", "warning"),
			)
		}
	})
	if err := l.Listen(notifyChannel); err != nil {
		_ = l.Close()
		return
	}
	b.listener = l
	go func() {
		for n := range l.Notify {
			if n == nil {
				continue
			}
			b.broadcast()
		}
	}()
}

func (b *PostgresBroker) broadcast() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (b *PostgresBroker) Subscribe(ctx context.Context) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	b.mu.Lock()
	if !b.closed {
		b.subs[ch] = struct{}{}
	}
	b.mu.Unlock()

	var once sync.Once
	unsub := func() {
		once.Do(func() {
			b.mu.Lock()
			delete(b.subs, ch)
			b.mu.Unlock()
		})
	}
	go func() {
		<-ctx.Done()
		unsub()
	}()
	return ch, unsub
}

func (b *PostgresBroker) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	l := b.listener
	b.listener = nil
	for ch := range b.subs {
		delete(b.subs, ch)
	}
	b.mu.Unlock()
	if l != nil {
		return l.Close()
	}
	return nil
}

const jobColumns = `id::text, database_id, migration_id, COALESCE(execution_id, 0),
	action, actor, idempotency_key, payload, trace_context, status,
	attempt, max_attempts, run_at, COALESCE(locked_by, ''), lease_expires_at,
	COALESCE(last_error, ''), COALESCE(last_sqlstate, ''),
	created_at, started_at, completed_at, updated_at`

func scanJob(row interface{ Scan(...any) error }) (Job, error) {
	var j Job
	var payloadBytes, traceBytes []byte
	var leaseExp, startedAt, completedAt sql.NullTime
	var status string

	err := row.Scan(
		&j.ID, &j.DatabaseID, &j.MigrationID, &j.ExecutionID,
		&j.Action, &j.Actor, &j.IdempotencyKey, &payloadBytes, &traceBytes, &status,
		&j.Attempt, &j.MaxAttempts, &j.RunAt, &j.LockedBy, &leaseExp,
		&j.LastError, &j.LastSQLState,
		&j.CreatedAt, &startedAt, &completedAt, &j.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Job{}, ErrJobNotFound
		}
		return Job{}, err
	}
	j.Status = JobStatus(status)
	if len(payloadBytes) > 0 {
		j.Payload = json.RawMessage(payloadBytes)
	}
	if len(traceBytes) > 0 {
		var tc map[string]string
		if json.Unmarshal(traceBytes, &tc) == nil {
			j.TraceContext = tc
		}
	}
	if leaseExp.Valid {
		t := leaseExp.Time
		j.LeaseExpiresAt = &t
	}
	if startedAt.Valid {
		t := startedAt.Time
		j.StartedAt = &t
	}
	if completedAt.Valid {
		t := completedAt.Time
		j.CompletedAt = &t
	}
	return j, nil
}

func (b *PostgresBroker) Enqueue(ctx context.Context, req EnqueueRequest) (Job, bool, error) {
	if req.DatabaseID <= 0 || req.MigrationID <= 0 {
		return Job{}, false, fmt.Errorf("queue: database_id dan migration_id wajib valid")
	}
	if req.MaxAttempts <= 0 {
		req.MaxAttempts = DefaultBackoffPolicy().MaxAttempts
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = uuid.NewString()
	}
	if req.RunAt.IsZero() {
		req.RunAt = time.Now().UTC()
	}

	ctx = telemetry.WithDatabaseID(ctx, req.DatabaseID)
	if req.Actor != "" {
		ctx = telemetry.WithActor(ctx, req.Actor)
	}
	ctx, span := telemetry.Tracer().Start(ctx, "queue.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(
			attribute.String("messaging.system", "postgresql_queue"),
			attribute.String("messaging.destination.name", notifyChannel),
			attribute.Int64("db.database_id", req.DatabaseID),
			attribute.Int64("migration.id", req.MigrationID),
			attribute.String("migration.action", req.Action),
			attribute.String("job.idempotency_key", req.IdempotencyKey),
		),
	)
	defer span.End()

	payloadBytes := []byte("{}")
	if req.Payload != nil {
		var err error
		payloadBytes, err = json.Marshal(req.Payload)
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
			return Job{}, false, fmt.Errorf("queue: marshal payload: %w", err)
		}
	}

	traceMap := telemetry.InjectTraceContext(ctx)
	traceBytes, err := json.Marshal(traceMap)
	if err != nil {
		traceBytes = []byte("{}")
	}

	job, err := scanJob(b.db.QueryRowContext(ctx, `
		INSERT INTO platform_migration_jobs (
			database_id, migration_id, action, actor, idempotency_key,
			payload, trace_context, status, max_attempts, run_at
		) VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, 'pending', $8, $9)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING `+jobColumns,
		req.DatabaseID, req.MigrationID, req.Action, req.Actor, req.IdempotencyKey,
		string(payloadBytes), string(traceBytes), req.MaxAttempts, req.RunAt,
	))

	deduplicated := false
	if errors.Is(err, ErrJobNotFound) {
		deduplicated = true
		job, err = scanJob(b.db.QueryRowContext(ctx,
			`SELECT `+jobColumns+` FROM platform_migration_jobs WHERE idempotency_key = $1`,
			req.IdempotencyKey,
		))
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return Job{}, false, fmt.Errorf("queue: enqueue: %w", err)
	}

	if deduplicated && (job.DatabaseID != req.DatabaseID || job.MigrationID != req.MigrationID || job.Action != req.Action) {
		span.RecordError(ErrIdempotencyConflict)
		span.SetStatus(codes.Error, ErrIdempotencyConflict.Error())
		return Job{}, false, ErrIdempotencyConflict
	}

	span.SetAttributes(
		attribute.String("job.id", job.ID),
		attribute.Bool("job.deduplicated", deduplicated),
	)

	if !deduplicated {
		_, _ = b.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, notifyChannel, job.ID)
		b.broadcast()
		metrics.JobEnqueued(job.Action)
	}

	telemetry.LogEvent(ctx, slog.LevelInfo, "queue.enqueued",
		slog.String("job_id", job.ID),
		slog.Int64("database_id", job.DatabaseID),
		slog.Int64("migration_id", job.MigrationID),
		slog.String("action", job.Action),
		slog.Bool("deduplicated", deduplicated),
		slog.String("status", string(job.Status)),
	)

	return job, deduplicated, nil
}

func (b *PostgresBroker) Dequeue(ctx context.Context, workerID string, leaseTTL time.Duration) (*Job, error) {
	if leaseTTL <= 0 {
		leaseTTL = 30 * time.Second
	}
	tx, err := b.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var jobID string
	err = tx.QueryRowContext(ctx, `
		SELECT j.id::text
		FROM platform_migration_jobs j
		WHERE j.status IN ('pending', 'retrying')
		  AND j.run_at <= now()
		  AND NOT EXISTS (
		      SELECT 1 FROM platform_db_locks l
		      WHERE l.database_id = j.database_id
		        AND l.state = 'held'
		        AND l.expires_at > now()
		  )
		ORDER BY j.run_at ASC, j.created_at ASC
		FOR UPDATE OF j SKIP LOCKED
		LIMIT 1`,
	).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	job, err := scanJob(tx.QueryRowContext(ctx, `
		UPDATE platform_migration_jobs
		SET status           = 'running',
		    attempt          = attempt + 1,
		    locked_by        = $2,
		    lease_expires_at = now() + ($3 * interval '1 second'),
		    started_at       = COALESCE(started_at, now()),
		    updated_at       = now()
		WHERE id = $1::uuid
		RETURNING `+jobColumns,
		jobID, workerID, leaseTTL.Seconds(),
	))
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &job, nil
}

func (b *PostgresBroker) RenewLease(ctx context.Context, jobID, workerID string, leaseTTL time.Duration) error {
	if leaseTTL <= 0 {
		leaseTTL = 30 * time.Second
	}
	res, err := b.db.ExecContext(ctx, `
		UPDATE platform_migration_jobs
		SET lease_expires_at = now() + ($3 * interval '1 second'),
		    updated_at       = now()
		WHERE id = $1::uuid AND locked_by = $2 AND status = 'running'`,
		jobID, workerID, leaseTTL.Seconds(),
	)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("queue: lease job %s sudah hilang", jobID)
	}
	return nil
}

func (b *PostgresBroker) Complete(ctx context.Context, jobID string, executionID int64) error {
	return b.MarkStatus(ctx, jobID, StatusCompleted, executionID, "", "")
}

func (b *PostgresBroker) MarkStatus(ctx context.Context, jobID string, status JobStatus, executionID int64, errMsg, sqlState string) error {
	var execArg any
	if executionID > 0 {
		execArg = executionID
	}
	var errArg any
	if errMsg != "" {
		errArg = telemetry.MaskString(errMsg)
	}
	var stateArg any
	if sqlState != "" {
		stateArg = sqlState
	}
	_, err := b.db.ExecContext(ctx, `
		UPDATE platform_migration_jobs
		SET status           = $2,
		    execution_id     = COALESCE($3, execution_id),
		    last_error       = $4,
		    last_sqlstate    = $5,
		    locked_by        = NULL,
		    lease_expires_at = NULL,
		    completed_at     = now(),
		    updated_at       = now()
		WHERE id = $1::uuid AND status <> 'completed'`,
		jobID, string(status), execArg, errArg, stateArg,
	)
	if err != nil {
		return fmt.Errorf("queue: mark job %s as %s: %w", jobID, status, err)
	}
	_, _ = b.db.ExecContext(ctx, `SELECT pg_notify($1, $2)`, notifyChannel, jobID)
	b.broadcast()
	return nil
}

func (b *PostgresBroker) Retry(ctx context.Context, jobID string, executionID int64, nextRunAt time.Time, errMsg, sqlState string) error {
	var execArg any
	if executionID > 0 {
		execArg = executionID
	}
	var errArg any
	if errMsg != "" {
		errArg = telemetry.MaskString(errMsg)
	}
	var stateArg any
	if sqlState != "" {
		stateArg = sqlState
	}
	_, err := b.db.ExecContext(ctx, `
		UPDATE platform_migration_jobs
		SET status           = 'retrying',
		    execution_id     = COALESCE($2, execution_id),
		    run_at           = $3,
		    last_error       = $4,
		    last_sqlstate    = $5,
		    locked_by        = NULL,
		    lease_expires_at = NULL,
		    updated_at       = now()
		WHERE id = $1::uuid AND status = 'running'`,
		jobID, execArg, nextRunAt, errArg, stateArg,
	)
	if err != nil {
		return fmt.Errorf("queue: retry job %s: %w", jobID, err)
	}
	return nil
}

func (b *PostgresBroker) RescheduleContended(ctx context.Context, jobID string, nextRunAt time.Time) error {
	_, err := b.db.ExecContext(ctx, `
		UPDATE platform_migration_jobs
		SET status           = 'pending',
		    attempt          = GREATEST(attempt - 1, 0),
		    run_at           = $2,
		    locked_by        = NULL,
		    lease_expires_at = NULL,
		    updated_at       = now()
		WHERE id = $1::uuid AND status = 'running'`,
		jobID, nextRunAt,
	)
	return err
}

func (b *PostgresBroker) ReclaimExpired(ctx context.Context) ([]Job, error) {
	rows, err := b.db.QueryContext(ctx, `
		SELECT `+jobColumns+`
		FROM platform_migration_jobs
		WHERE status = 'running'
		  AND lease_expires_at IS NOT NULL
		  AND lease_expires_at <= now()
		ORDER BY lease_expires_at ASC
		LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (b *PostgresBroker) GetJob(ctx context.Context, databaseID int64, jobID string) (Job, error) {
	return scanJob(b.db.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM platform_migration_jobs WHERE database_id = $1 AND id = $2::uuid`,
		databaseID, jobID,
	))
}

func (b *PostgresBroker) GetJobByID(ctx context.Context, jobID string) (Job, error) {
	return scanJob(b.db.QueryRowContext(ctx,
		`SELECT `+jobColumns+` FROM platform_migration_jobs WHERE id = $1::uuid`,
		jobID,
	))
}

func (b *PostgresBroker) ListJobs(ctx context.Context, databaseID int64, limit int) ([]Job, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := b.db.QueryContext(ctx,
		`SELECT `+jobColumns+` FROM platform_migration_jobs WHERE database_id = $1 ORDER BY created_at DESC LIMIT $2`,
		databaseID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
