package queue

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
)

type JobOutcome struct {
	Status        JobStatus
	ExecutionID   int64
	LockContended bool
	Retryable     bool
	Err           error
	SQLState      string
}

type JobHandler interface {
	HandleJob(ctx context.Context, job *Job) JobOutcome
}

type ExpiredJobRecoverer interface {
	RecoverExpiredJob(ctx context.Context, job *Job) error
}

type PoolOptions struct {
	WorkerPrefix      string
	Concurrency       int
	PollInterval      time.Duration
	LeaseTTL          time.Duration
	HeartbeatInterval time.Duration
	ReclaimInterval   time.Duration
	ContentionDelay   time.Duration
	Backoff           BackoffPolicy
}

func (o *PoolOptions) withDefaults() {
	if o.WorkerPrefix == "" {
		host, _ := os.Hostname()
		if host == "" {
			host = "worker"
		}
		o.WorkerPrefix = fmt.Sprintf("%s-%s", host, uuid.NewString()[:6])
	}
	if o.Concurrency <= 0 {
		o.Concurrency = 4
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 150 * time.Millisecond
	}
	if o.LeaseTTL <= 0 {
		o.LeaseTTL = 30 * time.Second
	}
	if o.HeartbeatInterval <= 0 {
		o.HeartbeatInterval = o.LeaseTTL / 3
		if o.HeartbeatInterval < 100*time.Millisecond {
			o.HeartbeatInterval = 100 * time.Millisecond
		}
	}
	if o.ReclaimInterval <= 0 {
		o.ReclaimInterval = 3 * time.Second
	}
	if o.ContentionDelay <= 0 {
		o.ContentionDelay = 250 * time.Millisecond
	}
	if o.Backoff.BaseDelay <= 0 {
		o.Backoff = DefaultBackoffPolicy()
	}
}

type WorkerPool struct {
	broker  Broker
	handler JobHandler
	opts    PoolOptions

	mu      sync.Mutex
	started bool
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

func NewWorkerPool(broker Broker, handler JobHandler, opts PoolOptions) *WorkerPool {
	opts.withDefaults()
	return &WorkerPool{
		broker:  broker,
		handler: handler,
		opts:    opts,
	}
}

func (p *WorkerPool) Start(parent context.Context) {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return
	}
	p.started = true
	ctx, cancel := context.WithCancel(parent)
	p.cancel = cancel
	p.mu.Unlock()

	telemetry.LogEvent(ctx, slog.LevelInfo, "worker.pool_started",
		slog.String("worker_prefix", p.opts.WorkerPrefix),
		slog.Int("concurrency", p.opts.Concurrency),
		slog.Int64("poll_interval_ms", p.opts.PollInterval.Milliseconds()),
		slog.String("status", "started"),
	)

	for i := 0; i < p.opts.Concurrency; i++ {
		workerID := fmt.Sprintf("%s-%d", p.opts.WorkerPrefix, i+1)
		p.wg.Add(1)
		go func(id string) {
			defer p.wg.Done()
			p.runWorker(ctx, id)
		}(workerID)
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.runReclaimer(ctx)
	}()
}

func (p *WorkerPool) Stop(ctx context.Context) error {
	p.mu.Lock()
	if !p.started {
		p.mu.Unlock()
		return nil
	}
	cancel := p.cancel
	p.started = false
	p.mu.Unlock()

	cancel()

	done := make(chan struct{})
	go func() {
		p.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		telemetry.LogEvent(context.Background(), slog.LevelInfo, "worker.pool_stopped",
			slog.String("worker_prefix", p.opts.WorkerPrefix),
			slog.String("status", "stopped"),
		)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *WorkerPool) runWorker(ctx context.Context, workerID string) {
	notifyCh, unsub := p.broker.Subscribe(ctx)
	defer unsub()

	ticker := time.NewTicker(p.opts.PollInterval)
	defer ticker.Stop()

	for {
		if ctx.Err() != nil {
			return
		}
		processed, err := p.ProcessOne(ctx, workerID)
		if err != nil && ctx.Err() == nil {
			telemetry.LogEvent(ctx, slog.LevelError, "worker.dequeue_error",
				slog.String("worker_id", workerID),
				slog.String("error", err.Error()),
				slog.String("status", "error"),
			)
		}
		if processed {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-notifyCh:
		case <-ticker.C:
		}
	}
}

func (p *WorkerPool) ProcessOne(ctx context.Context, workerID string) (bool, error) {
	job, err := p.broker.Dequeue(ctx, workerID, p.opts.LeaseTTL)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}
	p.executeJob(ctx, workerID, job)
	return true, nil
}

func (p *WorkerPool) executeJob(parent context.Context, workerID string, job *Job) {
	jobCtx := telemetry.ExtractTraceContext(parent, job.TraceContext)
	if telemetry.CorrelationIDFrom(jobCtx) == "" {
		jobCtx = telemetry.WithCorrelationID(jobCtx, job.ID)
	}
	jobCtx = telemetry.WithActor(jobCtx, job.Actor)
	jobCtx = telemetry.WithDatabaseID(jobCtx, job.DatabaseID)
	jobCtx = telemetry.WithExecMeta(jobCtx, telemetry.ExecMeta{
		MigrationID: job.MigrationID,
		JobID:       job.ID,
		ExecutionID: job.ExecutionID,
		Attempt:     job.Attempt,
		Action:      job.Action,
	})

	jobCtx, span := telemetry.Tracer().Start(jobCtx, "worker.process_job",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.String("messaging.system", "postgresql_queue"),
			attribute.String("job.id", job.ID),
			attribute.String("worker.id", workerID),
			attribute.Int64("db.database_id", job.DatabaseID),
			attribute.Int64("migration.id", job.MigrationID),
			attribute.String("migration.action", job.Action),
			attribute.Int("job.attempt", job.Attempt),
			attribute.Int("job.max_attempts", job.MaxAttempts),
		),
	)
	defer span.End()

	start := time.Now()
	telemetry.LogEvent(jobCtx, slog.LevelInfo, "worker.job_claimed",
		slog.String("worker_id", workerID),
		slog.String("job_id", job.ID),
		slog.Int64("migration_id", job.MigrationID),
		slog.String("action", job.Action),
		slog.Int("attempt", job.Attempt),
		slog.String("status", "running"),
	)

	hbCtx, stopHB := context.WithCancel(jobCtx)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		t := time.NewTicker(p.opts.HeartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				if err := p.broker.RenewLease(hbCtx, job.ID, workerID, p.opts.LeaseTTL); err != nil && hbCtx.Err() == nil {
					telemetry.LogEvent(hbCtx, slog.LevelWarn, "worker.job_lease_renew_failed",
						slog.String("worker_id", workerID),
						slog.String("job_id", job.ID),
						slog.String("error", err.Error()),
						slog.String("status", "warning"),
					)
				}
			}
		}
	}()

	outcome := p.handler.HandleJob(jobCtx, job)
	stopHB()
	<-hbDone

	durMS := float64(time.Since(start).Microseconds()) / 1000.0
	span.SetAttributes(attribute.Float64("job.duration_ms", durMS))
	if outcome.ExecutionID > 0 {
		span.SetAttributes(attribute.Int64("execution.id", outcome.ExecutionID))
	}

	finalizeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if outcome.LockContended {
		nextAt := time.Now().UTC().Add(p.opts.ContentionDelay)
		_ = p.broker.RescheduleContended(finalizeCtx, job.ID, nextAt)
		span.SetAttributes(attribute.Bool("job.lock_contended", true))
		telemetry.LogEvent(jobCtx, slog.LevelInfo, "worker.job_contended",
			slog.String("worker_id", workerID),
			slog.String("job_id", job.ID),
			slog.Time("rescheduled_at", nextAt),
			slog.String("status", "requeued"),
		)
		return
	}

	if outcome.Err == nil && (outcome.Status == "" || outcome.Status == StatusCompleted) {
		if err := p.broker.Complete(finalizeCtx, job.ID, outcome.ExecutionID); err != nil {
			span.RecordError(err)
			telemetry.LogEvent(jobCtx, slog.LevelError, "worker.job_complete_update_failed",
				slog.String("worker_id", workerID),
				slog.String("job_id", job.ID),
				slog.String("error", err.Error()),
				slog.String("status", "error"),
			)
		}
		telemetry.LogEvent(jobCtx, slog.LevelInfo, "worker.job_completed",
			slog.String("worker_id", workerID),
			slog.String("job_id", job.ID),
			slog.Int64("execution_id", outcome.ExecutionID),
			slog.Float64("duration_ms", durMS),
			slog.String("status", "completed"),
		)
		return
	}

	errMsg := ""
	if outcome.Err != nil {
		errMsg = outcome.Err.Error()
		span.RecordError(outcome.Err)
		span.SetStatus(codes.Error, telemetry.MaskString(errMsg))
	}
	sqlState := outcome.SQLState
	if sqlState == "" && outcome.Err != nil {
		sqlState = telemetry.ExtractSQLState(outcome.Err)
	}

	if outcome.Retryable &&
		outcome.Status != StatusPartial &&
		outcome.Status != StatusPartiallyReverted &&
		outcome.Status != StatusUncertain &&
		job.Attempt < job.MaxAttempts {
		delay := p.opts.Backoff.NextDelay(job.Attempt)
		nextAt := time.Now().UTC().Add(delay)
		_ = p.broker.Retry(finalizeCtx, job.ID, outcome.ExecutionID, nextAt, errMsg, sqlState)
		span.SetAttributes(
			attribute.Bool("job.retrying", true),
			attribute.Int64("job.retry_delay_ms", delay.Milliseconds()),
		)
		telemetry.LogEvent(jobCtx, slog.LevelWarn, "worker.job_retry_scheduled",
			slog.String("worker_id", workerID),
			slog.String("job_id", job.ID),
			slog.Int64("execution_id", outcome.ExecutionID),
			slog.Int("attempt", job.Attempt),
			slog.Int("max_attempts", job.MaxAttempts),
			slog.Int64("backoff_ms", delay.Milliseconds()),
			slog.String("sqlstate", sqlState),
			slog.String("error", errMsg),
			slog.String("status", "retrying"),
		)
		return
	}

	finalStatus := outcome.Status
	if finalStatus == "" || finalStatus == StatusCompleted {
		finalStatus = StatusFailed
	}
	_ = p.broker.MarkStatus(finalizeCtx, job.ID, finalStatus, outcome.ExecutionID, errMsg, sqlState)
	telemetry.LogEvent(jobCtx, slog.LevelError, "worker.job_finished_unsuccessful",
		slog.String("worker_id", workerID),
		slog.String("job_id", job.ID),
		slog.Int64("execution_id", outcome.ExecutionID),
		slog.Int("attempt", job.Attempt),
		slog.Float64("duration_ms", durMS),
		slog.String("sqlstate", sqlState),
		slog.String("error", errMsg),
		slog.String("status", string(finalStatus)),
	)
}

func (p *WorkerPool) runReclaimer(ctx context.Context) {
	ticker := time.NewTicker(p.opts.ReclaimInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			p.ReclaimOnce(ctx)
		}
	}
}

func (p *WorkerPool) ReclaimOnce(ctx context.Context) {
	expired, err := p.broker.ReclaimExpired(ctx)
	if err != nil {
		if ctx.Err() == nil {
			telemetry.LogEvent(ctx, slog.LevelWarn, "worker.reclaim_error",
				slog.String("error", err.Error()),
				slog.String("status", "warning"),
			)
		}
		return
	}
	recoverer, hasRecoverer := p.handler.(ExpiredJobRecoverer)
	for i := range expired {
		job := expired[i]
		jobCtx := telemetry.ExtractTraceContext(ctx, job.TraceContext)
		jobCtx = telemetry.WithDatabaseID(jobCtx, job.DatabaseID)
		telemetry.LogEvent(jobCtx, slog.LevelWarn, "worker.reclaim_expired",
			slog.String("job_id", job.ID),
			slog.String("previous_worker", job.LockedBy),
			slog.Int64("execution_id", job.ExecutionID),
			slog.String("status", "recovering"),
		)
		if hasRecoverer {
			if err := recoverer.RecoverExpiredJob(jobCtx, &job); err != nil {
				telemetry.LogEvent(jobCtx, slog.LevelError, "worker.reclaim_failed",
					slog.String("job_id", job.ID),
					slog.String("error", err.Error()),
					slog.String("status", "error"),
				)
			}
		} else {
			_ = p.broker.MarkStatus(jobCtx, job.ID, StatusUncertain, job.ExecutionID,
				"lease worker kedaluwarsa sebelum job selesai", "")
		}
	}
}
