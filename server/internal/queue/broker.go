package queue

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type JobStatus string

const (
	StatusPending           JobStatus = "pending"
	StatusRunning           JobStatus = "running"
	StatusRetrying          JobStatus = "retrying"
	StatusCompleted         JobStatus = "completed"
	StatusFailed            JobStatus = "failed"
	StatusPartial           JobStatus = "partial"
	StatusPartiallyReverted JobStatus = "partially_reverted"
	StatusUncertain         JobStatus = "uncertain"
	StatusCancelled         JobStatus = "cancelled"
)

var (
	ErrJobNotFound         = errors.New("queue: job tidak ditemukan")
	ErrIdempotencyConflict = errors.New("queue: idempotency key sudah dipakai untuk permintaan lain")
)

type Job struct {
	ID             string            `json:"id"`
	DatabaseID     int64             `json:"database_id"`
	MigrationID    int64             `json:"migration_id"`
	ExecutionID    int64             `json:"execution_id,omitempty"`
	Action         string            `json:"action"`
	Actor          string            `json:"actor"`
	IdempotencyKey string            `json:"idempotency_key"`
	Payload        json.RawMessage   `json:"payload,omitempty"`
	TraceContext   map[string]string `json:"trace_context,omitempty"`
	Status         JobStatus         `json:"status"`
	Attempt        int               `json:"attempt"`
	MaxAttempts    int               `json:"max_attempts"`
	RunAt          time.Time         `json:"run_at"`
	LockedBy       string            `json:"locked_by,omitempty"`
	LeaseExpiresAt *time.Time        `json:"lease_expires_at,omitempty"`
	LastError      string            `json:"last_error,omitempty"`
	LastSQLState   string            `json:"last_sqlstate,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	StartedAt      *time.Time        `json:"started_at,omitempty"`
	CompletedAt    *time.Time        `json:"completed_at,omitempty"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

func (j Job) IsTerminal() bool {
	switch j.Status {
	case StatusCompleted, StatusFailed, StatusPartial, StatusPartiallyReverted, StatusUncertain, StatusCancelled:
		return true
	default:
		return false
	}
}

type EnqueueRequest struct {
	DatabaseID     int64
	MigrationID    int64
	Action         string
	Actor          string
	IdempotencyKey string
	Payload        any
	MaxAttempts    int
	RunAt          time.Time
}

type Broker interface {
	Enqueue(ctx context.Context, req EnqueueRequest) (job Job, deduplicated bool, err error)
	Dequeue(ctx context.Context, workerID string, leaseTTL time.Duration) (*Job, error)
	RenewLease(ctx context.Context, jobID, workerID string, leaseTTL time.Duration) error
	Complete(ctx context.Context, jobID string, executionID int64) error
	MarkStatus(ctx context.Context, jobID string, status JobStatus, executionID int64, errMsg, sqlState string) error
	Retry(ctx context.Context, jobID string, executionID int64, nextRunAt time.Time, errMsg, sqlState string) error
	RescheduleContended(ctx context.Context, jobID string, nextRunAt time.Time) error
	ReclaimExpired(ctx context.Context) ([]Job, error)
	GetJob(ctx context.Context, databaseID int64, jobID string) (Job, error)
	GetJobByID(ctx context.Context, jobID string) (Job, error)
	ListJobs(ctx context.Context, databaseID int64, limit int) ([]Job, error)
	Subscribe(ctx context.Context) (<-chan struct{}, func())
	Close() error
}
