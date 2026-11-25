package migration

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

type Action string

const (
	ActionUp          Action = "up"
	ActionRollback    Action = "rollback"
	ActionForward     Action = "forward"
	ActionBatchUp     Action = "batch_up"
	ActionBatchResume Action = "batch_resume"
	ActionBatchRevert Action = "batch_revert"
)

type ExecutionMode string

const (
	ModeNormal ExecutionMode = "normal"
	ModeBatch  ExecutionMode = "batch"
)

type ExecutionStatus string

const (
	ExecPending           ExecutionStatus = "pending"
	ExecRunning           ExecutionStatus = "running"
	ExecSuccess           ExecutionStatus = "success"
	ExecFailed            ExecutionStatus = "failed"
	ExecPartial           ExecutionStatus = "partial"
	ExecPartiallyReverted ExecutionStatus = "partially_reverted"
	ExecReverted          ExecutionStatus = "reverted"
	ExecUncertain         ExecutionStatus = "uncertain"
)

var (
	ErrImmutableMigration    = errors.New("migration: definisi SQL migration yang pernah dieksekusi bersifat immutable; buat migration baru")
	ErrIrreversibleMigration = errors.New("migration: migration ditandai irreversible dan tidak dapat di-rollback")
	ErrNotLatestActive       = errors.New("migration: rollback hanya dapat dilakukan pada migration aktif terakhir")
	ErrInvalidForwardOrder   = errors.New("migration: forward harus mengikuti urutan linear migration yang di-rollback")
	ErrAlreadyActive         = errors.New("migration: migration ini sudah aktif di database target")
	ErrApprovalRequired      = errors.New("migration: perubahan ini belum mendapatkan approval yang diperlukan")
	ErrUnsafeBatchRevert     = errors.New("migration: pembatalan batch tidak aman; buat migration perbaikan baru")
	ErrExecutionNotFound     = errors.New("migration: execution tidak ditemukan")
	ErrInvalidState          = errors.New("migration: aksi tidak valid untuk status migration saat ini")
)

type ApprovalChecker interface {
	CheckApproval(ctx context.Context, databaseID, migrationID int64, action Action) error
}

type WorkflowStatusChecker struct {
	RequireApproval bool
}

type BatchConfig struct {
	Schema         string   `json:"schema,omitempty"`
	Table          string   `json:"table"`
	CursorColumn   string   `json:"cursor_column"`
	BatchSize      int      `json:"batch_size"`
	WhereClause    string   `json:"where_clause,omitempty"`
	CaptureColumns []string `json:"capture_columns,omitempty"`
	BatchDelayMS   int      `json:"batch_delay_ms,omitempty"`
}

func (c BatchConfig) NormalizedSchema() string {
	if c.Schema == "" {
		return "public"
	}
	return c.Schema
}

type Definition struct {
	ID                 int64         `json:"id"`
	DatabaseID         int64         `json:"database_id"`
	Version            int64         `json:"version"`
	Name               string        `json:"name"`
	SQLUp              string        `json:"sql_up"`
	SQLDown            string        `json:"sql_down,omitempty"`
	IsIrreversible     bool          `json:"is_irreversible"`
	IrreversibleReason string        `json:"irreversible_reason,omitempty"`
	ExecutionMode      ExecutionMode `json:"execution_mode"`
	BatchConfig        *BatchConfig  `json:"batch_config,omitempty"`
	WorkflowStatus     string        `json:"workflow_status"`
	IsLockedImmutable  bool          `json:"is_locked_immutable"`
	Revision           int           `json:"revision"`
	CreatedBy          string        `json:"created_by"`
	UpdatedBy          string        `json:"updated_by"`
	CreatedAt          time.Time     `json:"created_at"`
	UpdatedAt          time.Time     `json:"updated_at"`

	IsActive          bool  `json:"is_active"`
	ActiveExecutionID int64 `json:"active_execution_id,omitempty"`
	CanRollback       bool  `json:"can_rollback"`
	CanForward        bool  `json:"can_forward"`
}

type CreateDefinitionInput struct {
	Name               string        `json:"name"`
	SQLUp              string        `json:"sql_up"`
	SQLDown            string        `json:"sql_down"`
	IsIrreversible     bool          `json:"is_irreversible"`
	IrreversibleReason string        `json:"irreversible_reason"`
	ExecutionMode      ExecutionMode `json:"execution_mode"`
	BatchConfig        *BatchConfig  `json:"batch_config,omitempty"`
	WorkflowStatus     string        `json:"workflow_status,omitempty"`
}

type UpdateDefinitionInput struct {
	Name               string        `json:"name"`
	SQLUp              string        `json:"sql_up"`
	SQLDown            string        `json:"sql_down"`
	IsIrreversible     bool          `json:"is_irreversible"`
	IrreversibleReason string        `json:"irreversible_reason"`
	ExecutionMode      ExecutionMode `json:"execution_mode"`
	BatchConfig        *BatchConfig  `json:"batch_config,omitempty"`
}

type Execution struct {
	ID                  int64             `json:"execution_id"`
	DatabaseID          int64             `json:"database_id"`
	MigrationID         int64             `json:"migration_id"`
	MigrationVersion    int64             `json:"migration_version"`
	MigrationRevision   int               `json:"migration_revision"`
	MigrationName       string            `json:"migration_name"`
	Action              Action            `json:"action"`
	ExecutionMode       ExecutionMode     `json:"execution_mode"`
	Status              ExecutionStatus   `json:"status"`
	SQLExecuted         string            `json:"sql_executed"`
	Actor               string            `json:"actor"`
	RevertedExecutionID *int64            `json:"reverted_execution_id,omitempty"`
	ParentExecutionID   *int64            `json:"parent_execution_id,omitempty"`
	JobID               string            `json:"job_id,omitempty"`
	Attempt             int               `json:"attempt"`
	TotalBatches        int               `json:"total_batches"`
	CompletedBatches    int               `json:"completed_batches"`
	RowsAffected        int64             `json:"rows_affected"`
	RevertedBatches     int               `json:"reverted_batches"`
	RevertedRows        int64             `json:"reverted_rows"`
	LastCursor          string            `json:"last_cursor,omitempty"`
	ScopeMaxCursor      string            `json:"scope_max_cursor,omitempty"`
	Error               string            `json:"error,omitempty"`
	SQLState            string            `json:"sqlstate,omitempty"`
	CorrelationID       string            `json:"correlation_id,omitempty"`
	TraceID             string            `json:"trace_id,omitempty"`
	StartedAt           *time.Time        `json:"started_at,omitempty"`
	FinishedAt          *time.Time        `json:"finished_at,omitempty"`
	CreatedAt           time.Time         `json:"created_at"`
	Checkpoints         []BatchCheckpoint `json:"checkpoints,omitempty"`
}

type BatchCheckpoint struct {
	ID              int64           `json:"id"`
	ExecutionID     int64           `json:"execution_id"`
	RootExecutionID int64           `json:"root_execution_id"`
	DatabaseID      int64           `json:"database_id"`
	MigrationID     int64           `json:"migration_id"`
	BatchNumber     int             `json:"batch_number"`
	CursorStart     string          `json:"cursor_start"`
	CursorEnd       string          `json:"cursor_end"`
	RowsAffected    int64           `json:"rows_affected"`
	RowIDs          []string        `json:"-"`
	RecoveryData    json.RawMessage `json:"-"`
	Status          string          `json:"status"`
	Attempt         int             `json:"attempt"`
	Error           string          `json:"error,omitempty"`
	SQLState        string          `json:"sqlstate,omitempty"`
	DurationMS      int64           `json:"duration_ms"`
	CommittedAt     *time.Time      `json:"committed_at,omitempty"`
	RevertedAt      *time.Time      `json:"reverted_at,omitempty"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type JobPayload struct {
	TargetExecutionID int64 `json:"target_execution_id,omitempty"`
	ParentExecutionID int64 `json:"parent_execution_id,omitempty"`
	RootExecutionID   int64 `json:"root_execution_id,omitempty"`
}

type EnqueueOptions struct {
	IdempotencyKey string
	MaxAttempts    int
}
