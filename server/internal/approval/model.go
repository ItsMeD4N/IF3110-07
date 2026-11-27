package approval

import (
	"errors"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/migration"
)

const (
	StatusPendingReview = "pending_review"
	StatusApproved      = "approved"
	StatusRejected      = "rejected"
	StatusConsumed      = "consumed"
	StatusSuperseded    = "superseded"
)

const (
	DecisionPending  = "pending"
	DecisionApproved = "approved"
	DecisionRejected = "rejected"
)

var (
	ErrNotFound       = errors.New("approval: pengajuan tidak ditemukan")
	ErrNotLeader      = errors.New("approval: hanya leader group yang memiliki akses ke database ini yang dapat memberi keputusan")
	ErrAlreadyDecided = errors.New("approval: kamu sudah memberi keputusan untuk seluruh group yang kamu pimpin pada pengajuan ini")
	ErrNotPending     = errors.New("approval: pengajuan tidak lagi berstatus pending_review")
	ErrAlreadyOpen    = errors.New("approval: masih ada pengajuan terbuka (pending_review/approved) untuk migration dan aksi ini")
)

type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

type LeaderMissingError struct{ Groups []string }

func (e *LeaderMissingError) Error() string {
	return "approval: group berikut belum memiliki leader sehingga approval tidak dapat diajukan"
}

type Decision struct {
	GroupID        *int64            `json:"group_id"`
	GroupName      string            `json:"group_name"`
	AccessLevel    authz.AccessLevel `json:"access_level"`
	LeaderUserID   *string           `json:"leader_user_id"`
	LeaderUsername *string           `json:"leader_username"`
	Decision       string            `json:"decision"`
	DecidedBy      string            `json:"decided_by,omitempty"`
	Comment        string            `json:"comment"`
	DecidedAt      *time.Time        `json:"decided_at"`
}

type DefinitionSummary struct {
	SQLUp              string                  `json:"sql_up"`
	SQLDown            string                  `json:"sql_down,omitempty"`
	IsIrreversible     bool                    `json:"is_irreversible"`
	IrreversibleReason string                  `json:"irreversible_reason,omitempty"`
	ExecutionMode      migration.ExecutionMode `json:"execution_mode"`
	BatchConfig        *migration.BatchConfig  `json:"batch_config,omitempty"`
	WorkflowStatus     string                  `json:"workflow_status"`
	CurrentRevision    int                     `json:"current_revision"`
}

type Request struct {
	ID                   int64              `json:"id"`
	DatabaseID           int64              `json:"database_id"`
	DatabaseName         string             `json:"database_name"`
	MigrationID          int64              `json:"migration_id"`
	MigrationVersion     int64              `json:"migration_version"`
	MigrationRevision    int                `json:"migration_revision"`
	MigrationName        string             `json:"migration_name"`
	Action               migration.Action   `json:"action"`
	TargetExecutionID    *int64             `json:"target_execution_id"`
	RequestedBy          string             `json:"requested_by"`
	Note                 string             `json:"note"`
	Status               string             `json:"status"`
	RequiredApprovals    int                `json:"required_approvals"`
	ApprovedCount        int                `json:"approved_count"`
	CanCurrentUserDecide bool               `json:"can_current_user_decide"`
	Approvals            []Decision         `json:"approvals"`
	JobID                string             `json:"job_id,omitempty"`
	ConsumedAt           *time.Time         `json:"consumed_at,omitempty"`
	CreatedAt            time.Time          `json:"created_at"`
	UpdatedAt            time.Time          `json:"updated_at"`
	Definition           *DefinitionSummary `json:"definition,omitempty"`
}

type CreateInput struct {
	Action            migration.Action `json:"action"`
	TargetExecutionID int64            `json:"target_execution_id"`
	Note              string           `json:"note"`
}

type ListFilter struct {
	Status      string
	MigrationID int64
	Limit       int
}

func ParseAction(s string) (migration.Action, bool) {
	switch a := migration.Action(s); a {
	case migration.ActionUp, migration.ActionBatchUp, migration.ActionRollback,
		migration.ActionForward, migration.ActionBatchResume, migration.ActionBatchRevert:
		return a, true
	}
	return "", false
}

func IsWorkflowAction(a migration.Action) bool {
	return a == migration.ActionUp || a == migration.ActionBatchUp
}

func summarize(ds []Decision) (required, approved, pending int, rejected bool) {
	for _, d := range ds {
		if d.GroupID == nil && d.Decision == DecisionPending {
			continue
		}
		required++
		switch d.Decision {
		case DecisionApproved:
			approved++
		case DecisionPending:
			pending++
		case DecisionRejected:
			rejected = true
		}
	}
	return
}
