package dblock

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
)

const (
	StateHeld             = "held"
	StateBlockedPartial   = "blocked_partial"
	StateBlockedUncertain = "blocked_uncertain"

	DefaultLeaseTTL          = 30 * time.Second
	DefaultHeartbeatInterval = 10 * time.Second
	TargetAdvisoryLockKey    = int64(31100703)
)

var (
	ErrLockHeld        = errors.New("dblock: database target sedang dikunci oleh worker lain")
	ErrDatabaseBlocked = errors.New("dblock: database target ditahan karena migration partial atau hasil commit belum pasti")
	ErrLeaseLost       = errors.New("dblock: lease lock sudah hilang atau kedaluwarsa")
)

type LockConflictError struct {
	DatabaseID int64
	OwnerID    string
	JobID      string
	ExpiresAt  time.Time
}

func (e *LockConflictError) Error() string {
	return fmt.Sprintf("database %d sedang dikunci oleh worker %s (job %s) sampai %s",
		e.DatabaseID, e.OwnerID, e.JobID, e.ExpiresAt.UTC().Format(time.RFC3339))
}

func (e *LockConflictError) Unwrap() error { return ErrLockHeld }

type BlockedError struct {
	DatabaseID  int64
	State       string
	ExecutionID int64
	Reason      string
}

func (e *BlockedError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("database %d ditahan (%s, execution #%d): %s", e.DatabaseID, e.State, e.ExecutionID, e.Reason)
	}
	return fmt.Sprintf("database %d ditahan dalam status %s (execution #%d)", e.DatabaseID, e.State, e.ExecutionID)
}

func (e *BlockedError) Unwrap() error { return ErrDatabaseBlocked }

type AcquireRequest struct {
	DatabaseID              int64
	OwnerID                 string
	JobID                   string
	ExecutionID             int64
	TTL                     time.Duration
	AllowBlockedExecutionID int64
}

type Lease struct {
	DatabaseID     int64     `json:"database_id"`
	LockToken      string    `json:"lock_token"`
	FencingVersion int64     `json:"fencing_version"`
	OwnerID        string    `json:"owner_id"`
	JobID          string    `json:"job_id,omitempty"`
	ExecutionID    int64     `json:"execution_id,omitempty"`
	AcquiredAt     time.Time `json:"acquired_at"`
	ExpiresAt      time.Time `json:"expires_at"`
}

type LockInfo struct {
	DatabaseID     int64      `json:"database_id"`
	Locked         bool       `json:"locked"`
	FencingVersion int64      `json:"fencing_version,omitempty"`
	OwnerID        string     `json:"owner_id,omitempty"`
	JobID          string     `json:"job_id,omitempty"`
	ExecutionID    int64      `json:"execution_id,omitempty"`
	State          string     `json:"state,omitempty"`
	Reason         string     `json:"reason,omitempty"`
	AcquiredAt     *time.Time `json:"acquired_at,omitempty"`
	HeartbeatAt    *time.Time `json:"heartbeat_at,omitempty"`
	ExpiresAt      *time.Time `json:"expires_at,omitempty"`
	Expired        bool       `json:"expired"`
}

type Locker interface {
	Acquire(ctx context.Context, req AcquireRequest) (*Lease, error)
	Renew(ctx context.Context, lease *Lease, ttl time.Duration) error
	Verify(ctx context.Context, lease *Lease) error
	UpdateExecution(ctx context.Context, lease *Lease, executionID int64) error
	TransitionToBlocked(ctx context.Context, lease *Lease, state string, executionID int64, reason string) error
	Release(ctx context.Context, lease *Lease) error
	ForceClearBlock(ctx context.Context, databaseID, executionID int64) error
	Inspect(ctx context.Context, databaseID int64) (LockInfo, error)
	ListExpiredHeld(ctx context.Context) ([]LockInfo, error)
}

type PostgresLocker struct {
	db *sql.DB
}

func NewPostgresLocker(db *sql.DB) *PostgresLocker {
	return &PostgresLocker{db: db}
}

func (l *PostgresLocker) Acquire(ctx context.Context, req AcquireRequest) (*Lease, error) {
	if req.TTL <= 0 {
		req.TTL = DefaultLeaseTTL
	}
	if req.OwnerID == "" {
		req.OwnerID = "worker-" + uuid.NewString()[:8]
	}

	ctx = telemetry.WithDatabaseID(ctx, req.DatabaseID)
	ctx, span := telemetry.Tracer().Start(ctx, "lock.acquire",
		trace.WithAttributes(
			attribute.Int64("db.database_id", req.DatabaseID),
			attribute.String("lock.owner_id", req.OwnerID),
			attribute.String("job.id", req.JobID),
			attribute.Int64("lock.ttl_ms", req.TTL.Milliseconds()),
		),
	)
	defer span.End()

	start := time.Now()
	token := uuid.NewString()
	var jobIDArg any
	if req.JobID != "" {
		jobIDArg = req.JobID
	}
	var execIDArg any
	if req.ExecutionID > 0 {
		execIDArg = req.ExecutionID
	}

	lease := &Lease{
		DatabaseID:  req.DatabaseID,
		LockToken:   token,
		OwnerID:     req.OwnerID,
		JobID:       req.JobID,
		ExecutionID: req.ExecutionID,
	}

	ttlSeconds := req.TTL.Seconds()
	err := l.db.QueryRowContext(ctx, `
		INSERT INTO platform_db_locks (
			database_id, lock_token, fencing_version, owner_id, job_id, execution_id,
			state, reason, acquired_at, heartbeat_at, expires_at
		) VALUES (
			$1, $2::uuid, 1, $3, $4::uuid, $5,
			'held', '', now(), now(), now() + ($6 * interval '1 second')
		)
		ON CONFLICT (database_id) DO UPDATE SET
			lock_token      = EXCLUDED.lock_token,
			fencing_version = platform_db_locks.fencing_version + 1,
			owner_id        = EXCLUDED.owner_id,
			job_id          = EXCLUDED.job_id,
			execution_id    = EXCLUDED.execution_id,
			state           = 'held',
			reason          = '',
			acquired_at     = now(),
			heartbeat_at    = now(),
			expires_at      = EXCLUDED.expires_at
		WHERE (platform_db_locks.state = 'held' AND platform_db_locks.expires_at <= now())
		   OR ($7::bigint > 0
		       AND platform_db_locks.state IN ('blocked_partial', 'blocked_uncertain')
		       AND platform_db_locks.execution_id = $7::bigint)
		RETURNING fencing_version, acquired_at, expires_at`,
		req.DatabaseID, token, req.OwnerID, jobIDArg, execIDArg, ttlSeconds, req.AllowBlockedExecutionID,
	).Scan(&lease.FencingVersion, &lease.AcquiredAt, &lease.ExpiresAt)

	durMS := float64(time.Since(start).Microseconds()) / 1000.0
	span.SetAttributes(attribute.Float64("lock.wait_ms", durMS))

	if err == nil {
		span.SetAttributes(
			attribute.Int64("lock.fencing_version", lease.FencingVersion),
			attribute.String("lock.token", lease.LockToken),
		)
		telemetry.LogEvent(ctx, slog.LevelInfo, "lock.acquired",
			slog.Int64("database_id", req.DatabaseID),
			slog.String("owner_id", req.OwnerID),
			slog.String("job_id", req.JobID),
			slog.String("lock_token", lease.LockToken),
			slog.Int64("fencing_version", lease.FencingVersion),
			slog.Float64("duration_ms", durMS),
			slog.String("status", "acquired"),
		)
		return lease, nil
	}

	if !errors.Is(err, sql.ErrNoRows) {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("dblock: acquire database %d: %w", req.DatabaseID, err)
	}

	info, inspectErr := l.Inspect(ctx, req.DatabaseID)
	if inspectErr != nil {
		span.RecordError(inspectErr)
		span.SetStatus(codes.Error, inspectErr.Error())
		return nil, fmt.Errorf("dblock: inspect conflict database %d: %w", req.DatabaseID, inspectErr)
	}

	if info.State == StateBlockedPartial || info.State == StateBlockedUncertain {
		bErr := &BlockedError{
			DatabaseID:  req.DatabaseID,
			State:       info.State,
			ExecutionID: info.ExecutionID,
			Reason:      info.Reason,
		}
		span.RecordError(bErr)
		span.SetStatus(codes.Error, bErr.Error())
		telemetry.LogEvent(ctx, slog.LevelWarn, "lock.blocked",
			slog.Int64("database_id", req.DatabaseID),
			slog.String("owner_id", req.OwnerID),
			slog.String("job_id", req.JobID),
			slog.String("lock_state", info.State),
			slog.Int64("blocking_execution_id", info.ExecutionID),
			slog.String("reason", info.Reason),
			slog.String("status", "blocked"),
		)
		return nil, bErr
	}

	var exp time.Time
	if info.ExpiresAt != nil {
		exp = *info.ExpiresAt
	}
	cErr := &LockConflictError{
		DatabaseID: req.DatabaseID,
		OwnerID:    info.OwnerID,
		JobID:      info.JobID,
		ExpiresAt:  exp,
	}
	span.SetAttributes(
		attribute.Bool("lock.contended", true),
		attribute.String("lock.holder", info.OwnerID),
	)
	telemetry.LogEvent(ctx, slog.LevelInfo, "lock.contended",
		slog.Int64("database_id", req.DatabaseID),
		slog.String("owner_id", req.OwnerID),
		slog.String("holder_owner_id", info.OwnerID),
		slog.String("holder_job_id", info.JobID),
		slog.String("status", "contended"),
	)
	return nil, cErr
}

func (l *PostgresLocker) Renew(ctx context.Context, lease *Lease, ttl time.Duration) error {
	if lease == nil {
		return ErrLeaseLost
	}
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	var newExpiry time.Time
	err := l.db.QueryRowContext(ctx, `
		UPDATE platform_db_locks
		SET heartbeat_at = now(),
		    expires_at   = now() + ($4 * interval '1 second')
		WHERE database_id = $1
		  AND lock_token = $2::uuid
		  AND fencing_version = $3
		  AND state = 'held'
		RETURNING expires_at`,
		lease.DatabaseID, lease.LockToken, lease.FencingVersion, ttl.Seconds(),
	).Scan(&newExpiry)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	if err != nil {
		return fmt.Errorf("dblock: renew database %d: %w", lease.DatabaseID, err)
	}
	lease.ExpiresAt = newExpiry
	return nil
}

func (l *PostgresLocker) Verify(ctx context.Context, lease *Lease) error {
	if lease == nil {
		return ErrLeaseLost
	}
	var ok bool
	err := l.db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM platform_db_locks
			WHERE database_id = $1
			  AND lock_token = $2::uuid
			  AND fencing_version = $3
			  AND state = 'held'
			  AND expires_at > now()
		)`,
		lease.DatabaseID, lease.LockToken, lease.FencingVersion,
	).Scan(&ok)
	if err != nil {
		return fmt.Errorf("dblock: verify database %d: %w", lease.DatabaseID, err)
	}
	if !ok {
		return ErrLeaseLost
	}
	return nil
}

func (l *PostgresLocker) UpdateExecution(ctx context.Context, lease *Lease, executionID int64) error {
	if lease == nil {
		return ErrLeaseLost
	}
	res, err := l.db.ExecContext(ctx, `
		UPDATE platform_db_locks
		SET execution_id = $4, heartbeat_at = now()
		WHERE database_id = $1
		  AND lock_token = $2::uuid
		  AND fencing_version = $3
		  AND state = 'held'`,
		lease.DatabaseID, lease.LockToken, lease.FencingVersion, executionID,
	)
	if err != nil {
		return fmt.Errorf("dblock: update execution database %d: %w", lease.DatabaseID, err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrLeaseLost
	}
	lease.ExecutionID = executionID
	return nil
}

func (l *PostgresLocker) TransitionToBlocked(ctx context.Context, lease *Lease, state string, executionID int64, reason string) error {
	if lease == nil {
		return ErrLeaseLost
	}
	if state != StateBlockedPartial && state != StateBlockedUncertain {
		return fmt.Errorf("dblock: status block tidak valid: %s", state)
	}
	res, err := l.db.ExecContext(ctx, `
		UPDATE platform_db_locks
		SET state        = $4,
		    execution_id = $5,
		    reason       = $6,
		    heartbeat_at = now(),
		    expires_at   = '9999-12-31T23:59:59Z'::timestamptz
		WHERE database_id = $1
		  AND lock_token = $2::uuid
		  AND fencing_version = $3`,
		lease.DatabaseID, lease.LockToken, lease.FencingVersion, state, executionID, telemetry.MaskString(reason),
	)
	if err != nil {
		return fmt.Errorf("dblock: transition to %s database %d: %w", state, lease.DatabaseID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrLeaseLost
	}
	telemetry.LogEvent(ctx, slog.LevelWarn, "lock.transitioned_blocked",
		slog.Int64("database_id", lease.DatabaseID),
		slog.String("lock_state", state),
		slog.Int64("execution_id", executionID),
		slog.String("reason", telemetry.MaskString(reason)),
		slog.String("status", state),
	)
	return nil
}

func (l *PostgresLocker) Release(ctx context.Context, lease *Lease) error {
	if lease == nil {
		return nil
	}
	ctx, span := telemetry.Tracer().Start(ctx, "lock.release",
		trace.WithAttributes(
			attribute.Int64("db.database_id", lease.DatabaseID),
			attribute.String("lock.owner_id", lease.OwnerID),
			attribute.Int64("lock.fencing_version", lease.FencingVersion),
		),
	)
	defer span.End()

	res, err := l.db.ExecContext(ctx, `
		DELETE FROM platform_db_locks
		WHERE database_id = $1
		  AND lock_token = $2::uuid
		  AND fencing_version = $3
		  AND state = 'held'`,
		lease.DatabaseID, lease.LockToken, lease.FencingVersion,
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return fmt.Errorf("dblock: release database %d: %w", lease.DatabaseID, err)
	}
	released, _ := res.RowsAffected()
	telemetry.LogEvent(ctx, slog.LevelInfo, "lock.released",
		slog.Int64("database_id", lease.DatabaseID),
		slog.String("owner_id", lease.OwnerID),
		slog.Int64("fencing_version", lease.FencingVersion),
		slog.Bool("released", released > 0),
		slog.String("status", "released"),
	)
	return nil
}

func (l *PostgresLocker) ForceClearBlock(ctx context.Context, databaseID, executionID int64) error {
	var err error
	if executionID > 0 {
		_, err = l.db.ExecContext(ctx, `
			DELETE FROM platform_db_locks
			WHERE database_id = $1
			  AND state IN ('blocked_partial', 'blocked_uncertain')
			  AND (execution_id = $2 OR execution_id IS NULL)`,
			databaseID, executionID,
		)
	} else {
		_, err = l.db.ExecContext(ctx, `
			DELETE FROM platform_db_locks
			WHERE database_id = $1
			  AND state IN ('blocked_partial', 'blocked_uncertain')`,
			databaseID,
		)
	}
	if err != nil {
		return fmt.Errorf("dblock: clear block database %d: %w", databaseID, err)
	}
	telemetry.LogEvent(ctx, slog.LevelInfo, "lock.block_cleared",
		slog.Int64("database_id", databaseID),
		slog.Int64("execution_id", executionID),
		slog.String("status", "cleared"),
	)
	return nil
}

func (l *PostgresLocker) Inspect(ctx context.Context, databaseID int64) (LockInfo, error) {
	var info LockInfo
	info.DatabaseID = databaseID
	var jobID sql.NullString
	var execID sql.NullInt64
	var acq, hb, exp time.Time
	var expired bool

	err := l.db.QueryRowContext(ctx, `
		SELECT fencing_version, owner_id, job_id::text, execution_id, state, reason,
		       acquired_at, heartbeat_at, expires_at,
		       (state = 'held' AND expires_at <= now())
		FROM platform_db_locks
		WHERE database_id = $1`, databaseID,
	).Scan(&info.FencingVersion, &info.OwnerID, &jobID, &execID, &info.State, &info.Reason,
		&acq, &hb, &exp, &expired)
	if errors.Is(err, sql.ErrNoRows) {
		return info, nil
	}
	if err != nil {
		return LockInfo{}, err
	}
	info.Locked = true
	info.JobID = jobID.String
	info.ExecutionID = execID.Int64
	info.AcquiredAt = &acq
	info.HeartbeatAt = &hb
	info.ExpiresAt = &exp
	info.Expired = expired
	return info, nil
}

func (l *PostgresLocker) ListExpiredHeld(ctx context.Context) ([]LockInfo, error) {
	rows, err := l.db.QueryContext(ctx, `
		SELECT database_id, fencing_version, owner_id, job_id::text, execution_id, state, reason,
		       acquired_at, heartbeat_at, expires_at
		FROM platform_db_locks
		WHERE state = 'held' AND expires_at <= now()
		ORDER BY expires_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LockInfo{}
	for rows.Next() {
		var info LockInfo
		var jobID sql.NullString
		var execID sql.NullInt64
		var acq, hb, exp time.Time
		if err := rows.Scan(&info.DatabaseID, &info.FencingVersion, &info.OwnerID, &jobID, &execID, &info.State, &info.Reason, &acq, &hb, &exp); err != nil {
			return nil, err
		}
		info.Locked = true
		info.Expired = true
		info.JobID = jobID.String
		info.ExecutionID = execID.Int64
		info.AcquiredAt = &acq
		info.HeartbeatAt = &hb
		info.ExpiresAt = &exp
		out = append(out, info)
	}
	return out, rows.Err()
}

func StartHeartbeat(parent context.Context, locker Locker, lease *Lease, ttl, interval time.Duration) (context.Context, func()) {
	if ttl <= 0 {
		ttl = DefaultLeaseTTL
	}
	if interval <= 0 {
		interval = ttl / 3
		if interval < 100*time.Millisecond {
			interval = 100 * time.Millisecond
		}
	}

	leaseCtx, cancel := context.WithCancel(parent)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-ticker.C:
				rctx, rcancel := context.WithTimeout(leaseCtx, ttl/2+time.Second)
				err := locker.Renew(rctx, lease, ttl)
				rcancel()
				if err != nil {
					if errors.Is(err, ErrLeaseLost) {
						telemetry.LogEvent(leaseCtx, slog.LevelError, "lock.lease_lost",
							slog.Int64("database_id", lease.DatabaseID),
							slog.String("owner_id", lease.OwnerID),
							slog.Int64("fencing_version", lease.FencingVersion),
							slog.String("status", "lease_lost"),
						)
						cancel()
						return
					}
					telemetry.LogEvent(leaseCtx, slog.LevelWarn, "lock.heartbeat_failed",
						slog.Int64("database_id", lease.DatabaseID),
						slog.String("owner_id", lease.OwnerID),
						slog.String("error", err.Error()),
						slog.String("status", "warning"),
					)
				}
			}
		}
	}()

	stop := func() {
		cancel()
		<-done
	}
	return leaseCtx, stop
}

func AcquireTargetAdvisory(ctx context.Context, conn *sql.Conn) (bool, error) {
	var acquired bool
	err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, TargetAdvisoryLockKey).Scan(&acquired)
	return acquired, err
}

func ReleaseTargetAdvisory(ctx context.Context, conn *sql.Conn) {
	if conn == nil {
		return
	}
	_, _ = conn.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, TargetAdvisoryLockKey)
}
