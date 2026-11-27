package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/migration"
	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
	"github.com/ItsMeD4N/IF3110-07/server/internal/store"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
)

const (
	maxNoteLen    = 2000
	maxCommentLen = 2000
	defaultLimit  = 100
	maxLimit      = 500
)

type DefinitionSource interface {
	GetDefinition(ctx context.Context, databaseID, migrationID int64) (migration.Definition, error)
	GetExecution(ctx context.Context, databaseID, executionID int64) (migration.Execution, error)
}

type GroupSource interface {
	DatabaseGroups(ctx context.Context, databaseID int64) ([]store.DatabaseGroup, error)
}

type Service struct {
	DB              *sql.DB
	Defs            DefinitionSource
	Groups          GroupSource
	RequireApproval bool
	Now             func() time.Time
}

func NewService(db *sql.DB, defs DefinitionSource, groups GroupSource, requireApproval bool) *Service {
	return &Service{DB: db, Defs: defs, Groups: groups, RequireApproval: requireApproval, Now: time.Now}
}

var (
	_ migration.ApprovalChecker          = (*Service)(nil)
	_ migration.ApprovalConsumer         = (*Service)(nil)
	_ migration.DefinitionChangeListener = (*Service)(nil)
)

func NormalizeAction(def migration.Definition, a migration.Action) (migration.Action, error) {
	batch := def.ExecutionMode == migration.ModeBatch
	switch a {
	case migration.ActionUp, migration.ActionBatchUp:
		if batch {
			return migration.ActionBatchUp, nil
		}
		return migration.ActionUp, nil
	case migration.ActionRollback:
		if batch {
			return migration.ActionBatchRevert, nil
		}
		return migration.ActionRollback, nil
	case migration.ActionBatchResume, migration.ActionBatchRevert:
		if !batch {
			return "", &ValidationError{fmt.Sprintf("Aksi %s hanya untuk migration mode batch.", a)}
		}
		return a, nil
	case migration.ActionForward:
		return a, nil
	}
	return "", &ValidationError{"action harus salah satu dari up, batch_up, rollback, forward, batch_resume, batch_revert."}
}

func (s *Service) validatePreconditions(ctx context.Context, def migration.Definition, action migration.Action, targetExecID *int64) error {
	switch action {
	case migration.ActionUp, migration.ActionBatchUp:
		if def.IsActive {
			return migration.ErrAlreadyActive
		}
	case migration.ActionForward:
		if def.IsActive {
			return migration.ErrAlreadyActive
		}
		if !def.CanForward {
			return migration.ErrInvalidForwardOrder
		}
	case migration.ActionRollback:
		if def.IsIrreversible || strings.TrimSpace(def.SQLDown) == "" {
			return migration.ErrIrreversibleMigration
		}
		if !def.CanRollback || def.ActiveExecutionID == 0 {
			return migration.ErrNotLatestActive
		}
		id := def.ActiveExecutionID
		*targetExecID = id
	case migration.ActionBatchRevert:
		if def.IsIrreversible || strings.TrimSpace(def.SQLDown) == "" {
			return migration.ErrUnsafeBatchRevert
		}
		if *targetExecID == 0 {
			if !def.CanRollback || def.ActiveExecutionID == 0 {
				return &ValidationError{"target_execution_id wajib diisi untuk batch_revert execution partial/partially_reverted."}
			}
			*targetExecID = def.ActiveExecutionID
			return nil
		}
		ex, err := s.Defs.GetExecution(ctx, def.DatabaseID, *targetExecID)
		if err != nil {
			return err
		}
		if ex.MigrationID != def.ID {
			return &ValidationError{"target_execution_id bukan milik migration ini."}
		}
		switch ex.Status {
		case migration.ExecPartial, migration.ExecPartiallyReverted:
		case migration.ExecSuccess:
			if !def.CanRollback || def.ActiveExecutionID != ex.ID {
				return migration.ErrNotLatestActive
			}
		default:
			return fmt.Errorf("%w: batch revert hanya untuk execution partial, partially_reverted, atau aktif terakhir", migration.ErrInvalidState)
		}
	case migration.ActionBatchResume:
		if *targetExecID == 0 {
			return &ValidationError{"target_execution_id wajib diisi untuk batch_resume."}
		}
		ex, err := s.Defs.GetExecution(ctx, def.DatabaseID, *targetExecID)
		if err != nil {
			return err
		}
		if ex.MigrationID != def.ID {
			return &ValidationError{"target_execution_id bukan milik migration ini."}
		}
		if ex.Status != migration.ExecPartial {
			return fmt.Errorf("%w: hanya batch execution berstatus partial yang dapat dilanjutkan", migration.ErrInvalidState)
		}
	}
	return nil
}

func (s *Service) Create(ctx context.Context, databaseID, migrationID int64, p *authz.Principal, in CreateInput) (Request, error) {
	in.Note = strings.TrimSpace(in.Note)
	if len([]rune(in.Note)) > maxNoteLen {
		return Request{}, &ValidationError{"note maksimal 2000 karakter."}
	}
	action, ok := ParseAction(string(in.Action))
	if !ok {
		return Request{}, &ValidationError{"action harus salah satu dari up, batch_up, rollback, forward, batch_resume, batch_revert."}
	}
	if in.TargetExecutionID < 0 {
		return Request{}, &ValidationError{"target_execution_id tidak valid."}
	}
	def, err := s.Defs.GetDefinition(ctx, databaseID, migrationID)
	if err != nil {
		return Request{}, err
	}
	action, err = NormalizeAction(def, action)
	if err != nil {
		return Request{}, err
	}
	target := in.TargetExecutionID
	if err := s.validatePreconditions(ctx, def, action, &target); err != nil {
		return Request{}, err
	}

	groups, err := s.Groups.DatabaseGroups(ctx, databaseID)
	if err != nil {
		return Request{}, err
	}
	if len(groups) == 0 {
		return Request{}, &ValidationError{"Tidak ada group yang memiliki akses ke database ini."}
	}
	var missing []string
	for _, g := range groups {
		if g.LeaderUserID == nil {
			missing = append(missing, g.GroupName)
		}
	}
	if len(missing) > 0 {
		return Request{}, &LeaderMissingError{Groups: missing}
	}

	var targetArg any
	if target > 0 {
		targetArg = target
	}
	var userArg any
	if p != nil && p.ID != "" {
		userArg = p.ID
	}
	username := ""
	if p != nil {
		username = p.Username
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO platform_approval_requests (
			database_id, migration_id, migration_revision, action, target_execution_id,
			requested_by, requested_by_user_id, note
		) VALUES ($1, $2, $3, $4, $5, $6, $7::uuid, $8)
		RETURNING id`,
		databaseID, migrationID, def.Revision, string(action), targetArg, username, userArg, in.Note,
	).Scan(&id)
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return Request{}, ErrAlreadyOpen
		}
		return Request{}, err
	}
	for _, g := range groups {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO platform_approval_decisions (request_id, group_id, group_name, access_level)
			VALUES ($1, $2, $3, $4)`, id, g.GroupID, g.GroupName, int(g.AccessLevel)); err != nil {
			return Request{}, err
		}
	}
	if IsWorkflowAction(action) {
		if _, err := tx.ExecContext(ctx, `
			UPDATE platform_migrations SET workflow_status = 'pending_review', updated_at = now()
			WHERE id = $1 AND database_id = $2`, migrationID, databaseID); err != nil {
			return Request{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Request{}, err
	}

	telemetry.LogEvent(ctx, slog.LevelInfo, "approval.requested",
		slog.Int64("approval_request_id", id),
		slog.Int64("migration_id", migrationID),
		slog.Int("migration_revision", def.Revision),
		slog.String("action", string(action)),
		slog.Int("required_approvals", len(groups)),
		slog.String("status", StatusPendingReview),
	)
	return s.Get(ctx, databaseID, id, p)
}

const requestSelect = `SELECT r.id, r.database_id, c.name, r.migration_id, m.version, r.migration_revision, m.name,
	r.action, r.target_execution_id, r.requested_by, r.note, r.status,
	COALESCE(r.consumed_job_id::text, ''), r.consumed_at, r.created_at, r.updated_at
	FROM platform_approval_requests r
	JOIN platform_migrations m ON m.id = r.migration_id
	JOIN db_connections c ON c.id = r.database_id`

func scanRequest(sc interface{ Scan(...any) error }) (Request, error) {
	var r Request
	var action string
	var target sql.NullInt64
	var consumedAt sql.NullTime
	err := sc.Scan(&r.ID, &r.DatabaseID, &r.DatabaseName, &r.MigrationID, &r.MigrationVersion, &r.MigrationRevision, &r.MigrationName,
		&action, &target, &r.RequestedBy, &r.Note, &r.Status,
		&r.JobID, &consumedAt, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, err
	}
	r.Action = migration.Action(action)
	if target.Valid {
		v := target.Int64
		r.TargetExecutionID = &v
	}
	if consumedAt.Valid {
		t := consumedAt.Time
		r.ConsumedAt = &t
	}
	r.Approvals = []Decision{}
	return r, nil
}

func (s *Service) queryRequests(ctx context.Context, p *authz.Principal, where string, args ...any) ([]Request, error) {
	rows, err := s.DB.QueryContext(ctx, requestSelect+" "+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Request{}
	idx := map[int64]int{}
	ids := []int64{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, err
		}
		idx[r.ID] = len(out)
		ids = append(ids, r.ID)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return out, nil
	}
	if err := s.attachDecisions(ctx, out, idx, ids); err != nil {
		return nil, err
	}
	for i := range out {
		finalize(&out[i], p)
	}
	return out, nil
}

func (s *Service) attachDecisions(ctx context.Context, out []Request, idx map[int64]int, ids []int64) error {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT d.request_id, d.group_id, d.group_name, d.access_level,
		       g.leader_user_id::text, lu.username,
		       d.decision, d.decided_by, d.comment, d.decided_at
		FROM platform_approval_decisions d
		LEFT JOIN user_groups g ON g.id = d.group_id
		LEFT JOIN users lu ON lu.id = g.leader_user_id
		WHERE d.request_id = ANY($1)
		ORDER BY d.request_id, lower(d.group_name), d.id`, pq.Array(ids))
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var reqID int64
		var d Decision
		var gid sql.NullInt64
		var lvl int
		var lid, lname sql.NullString
		var decidedAt sql.NullTime
		if err := rows.Scan(&reqID, &gid, &d.GroupName, &lvl, &lid, &lname, &d.Decision, &d.DecidedBy, &d.Comment, &decidedAt); err != nil {
			return err
		}
		d.AccessLevel = authz.AccessLevel(lvl)
		if gid.Valid {
			v := gid.Int64
			d.GroupID = &v
		}
		if lid.Valid {
			d.LeaderUserID = &lid.String
		}
		if lname.Valid {
			d.LeaderUsername = &lname.String
		}
		if decidedAt.Valid {
			t := decidedAt.Time
			d.DecidedAt = &t
		}
		if i, ok := idx[reqID]; ok {
			out[i].Approvals = append(out[i].Approvals, d)
		}
	}
	return rows.Err()
}

func finalize(r *Request, p *authz.Principal) {
	r.RequiredApprovals, r.ApprovedCount, _, _ = summarize(r.Approvals)
	r.CanCurrentUserDecide = false
	if p == nil || r.Status != StatusPendingReview {
		return
	}
	for _, d := range r.Approvals {
		if d.GroupID != nil && d.Decision == DecisionPending && d.LeaderUserID != nil && *d.LeaderUserID == p.ID {
			r.CanCurrentUserDecide = true
			return
		}
	}
}

func (s *Service) Get(ctx context.Context, databaseID, requestID int64, p *authz.Principal) (Request, error) {
	list, err := s.queryRequests(ctx, p, `WHERE r.database_id = $1 AND r.id = $2`, databaseID, requestID)
	if err != nil {
		return Request{}, err
	}
	if len(list) == 0 {
		return Request{}, ErrNotFound
	}
	r := list[0]
	if def, err := s.Defs.GetDefinition(ctx, databaseID, r.MigrationID); err == nil {
		r.Definition = &DefinitionSummary{
			SQLUp: def.SQLUp, SQLDown: def.SQLDown,
			IsIrreversible: def.IsIrreversible, IrreversibleReason: def.IrreversibleReason,
			ExecutionMode: def.ExecutionMode, BatchConfig: def.BatchConfig,
			WorkflowStatus: def.WorkflowStatus, CurrentRevision: def.Revision,
		}
	}
	return r, nil
}

func (s *Service) List(ctx context.Context, databaseID int64, f ListFilter, p *authz.Principal) ([]Request, error) {
	where := []string{"r.database_id = $1"}
	args := []any{databaseID}
	if f.Status != "" {
		args = append(args, f.Status)
		where = append(where, fmt.Sprintf("r.status = $%d", len(args)))
	}
	if f.MigrationID > 0 {
		args = append(args, f.MigrationID)
		where = append(where, fmt.Sprintf("r.migration_id = $%d", len(args)))
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	args = append(args, limit)
	return s.queryRequests(ctx, p, fmt.Sprintf("WHERE %s ORDER BY r.id DESC LIMIT $%d", strings.Join(where, " AND "), len(args)), args...)
}

func (s *Service) PendingForLeader(ctx context.Context, p *authz.Principal) ([]Request, error) {
	return s.queryRequests(ctx, p, `
		WHERE r.status = 'pending_review'
		  AND EXISTS (
			SELECT 1 FROM platform_approval_decisions d
			JOIN user_groups g ON g.id = d.group_id
			WHERE d.request_id = r.id AND d.decision = 'pending' AND g.leader_user_id = $1::uuid
		  )
		ORDER BY r.id ASC`, p.ID)
}

func (s *Service) Decide(ctx context.Context, databaseID, requestID int64, p *authz.Principal, approve bool, comment string, groupID int64) (Request, error) {
	if p == nil {
		return Request{}, ErrNotLeader
	}
	comment = strings.TrimSpace(comment)
	if !approve && comment == "" {
		return Request{}, &ValidationError{"Alasan penolakan (comment) wajib diisi."}
	}
	if len([]rune(comment)) > maxCommentLen {
		return Request{}, &ValidationError{"comment maksimal 2000 karakter."}
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Request{}, err
	}
	defer tx.Rollback()

	var status, action string
	var migrationID int64
	err = tx.QueryRowContext(ctx, `
		SELECT status, action, migration_id FROM platform_approval_requests
		WHERE database_id = $1 AND id = $2 FOR UPDATE`, databaseID, requestID).Scan(&status, &action, &migrationID)
	if errors.Is(err, sql.ErrNoRows) {
		return Request{}, ErrNotFound
	}
	if err != nil {
		return Request{}, err
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT d.id, d.group_id, d.decision
		FROM platform_approval_decisions d
		JOIN user_groups g ON g.id = d.group_id
		WHERE d.request_id = $1 AND g.leader_user_id = $2::uuid
		FOR UPDATE OF d`, requestID, p.ID)
	if err != nil {
		return Request{}, err
	}
	var pendingIDs []int64
	led := 0
	for rows.Next() {
		var id, gid int64
		var dec string
		if err := rows.Scan(&id, &gid, &dec); err != nil {
			rows.Close()
			return Request{}, err
		}
		if groupID > 0 && gid != groupID {
			continue
		}
		led++
		if dec == DecisionPending {
			pendingIDs = append(pendingIDs, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Request{}, err
	}
	if led == 0 {
		return Request{}, ErrNotLeader
	}
	if status != StatusPendingReview {
		return Request{}, ErrNotPending
	}
	if len(pendingIDs) == 0 {
		return Request{}, ErrAlreadyDecided
	}

	decision := DecisionApproved
	if !approve {
		decision = DecisionRejected
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE platform_approval_decisions
		SET decision = $2, decided_by_user_id = $3::uuid, decided_by = $4, comment = $5, decided_at = now()
		WHERE id = ANY($1)`, pq.Array(pendingIDs), decision, p.ID, p.Username, comment); err != nil {
		return Request{}, err
	}

	var pending, rejected int
	if err := tx.QueryRowContext(ctx, `
		SELECT count(*) FILTER (WHERE decision = 'pending' AND group_id IS NOT NULL),
		       count(*) FILTER (WHERE decision = 'rejected')
		FROM platform_approval_decisions WHERE request_id = $1`, requestID).Scan(&pending, &rejected); err != nil {
		return Request{}, err
	}
	newStatus := StatusPendingReview
	switch {
	case rejected > 0:
		newStatus = StatusRejected
	case pending == 0:
		newStatus = StatusApproved
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE platform_approval_requests SET status = $2, updated_at = now() WHERE id = $1`,
		requestID, newStatus); err != nil {
		return Request{}, err
	}
	if IsWorkflowAction(migration.Action(action)) && newStatus != StatusPendingReview {
		if _, err := tx.ExecContext(ctx, `
			UPDATE platform_migrations SET workflow_status = $2, updated_at = now()
			WHERE id = $1 AND workflow_status IN ('draft', 'pending_review', 'approved', 'rejected')`,
			migrationID, newStatus); err != nil {
			return Request{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Request{}, err
	}

	telemetry.LogEvent(ctx, slog.LevelInfo, "approval.decided",
		slog.Int64("approval_request_id", requestID),
		slog.Int64("migration_id", migrationID),
		slog.String("action", action),
		slog.String("decision", decision),
		slog.Int("groups_decided", len(pendingIDs)),
		slog.String("status", newStatus),
	)
	return s.Get(ctx, databaseID, requestID, p)
}

func (s *Service) CheckApproval(ctx context.Context, databaseID, migrationID int64, action migration.Action) error {
	key := migration.ApprovalKeyFrom(ctx)
	var ok bool
	err := s.DB.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM platform_approval_requests r
			JOIN platform_migrations m ON m.id = r.migration_id
			WHERE r.database_id = $1 AND r.migration_id = $2 AND r.action = $3
			  AND r.migration_revision = m.revision
			  AND (r.status = 'approved'
			       OR (r.status = 'consumed' AND $4 <> '' AND r.consumed_idempotency_key = $4))
		)`, databaseID, migrationID, string(action), key).Scan(&ok)
	if err != nil {
		return err
	}
	if !ok {
		return migration.ErrApprovalRequired
	}
	return nil
}

func (s *Service) ConsumeApproval(ctx context.Context, databaseID, migrationID int64, action migration.Action, key string) error {
	if key == "" {
		return migration.ErrApprovalRequired
	}
	var already bool
	if err := s.DB.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM platform_approval_requests
		WHERE database_id = $1 AND migration_id = $2 AND action = $3
		  AND status = 'consumed' AND consumed_idempotency_key = $4)`,
		databaseID, migrationID, string(action), key).Scan(&already); err != nil {
		return err
	}
	if already {
		return nil
	}
	var id int64
	err := s.DB.QueryRowContext(ctx, `
		UPDATE platform_approval_requests r
		SET status = 'consumed', consumed_idempotency_key = $4, consumed_at = now(), updated_at = now()
		WHERE r.id = (
			SELECT r2.id FROM platform_approval_requests r2
			JOIN platform_migrations m ON m.id = r2.migration_id
			WHERE r2.database_id = $1 AND r2.migration_id = $2 AND r2.action = $3
			  AND r2.status = 'approved' AND r2.migration_revision = m.revision
			ORDER BY r2.id
			LIMIT 1
			FOR UPDATE OF r2 SKIP LOCKED
		) AND r.status = 'approved'
		RETURNING r.id`, databaseID, migrationID, string(action), key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return migration.ErrApprovalRequired
	}
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return queue.ErrIdempotencyConflict
		}
		return err
	}
	telemetry.LogEvent(ctx, slog.LevelInfo, "approval.consumed",
		slog.Int64("approval_request_id", id),
		slog.Int64("migration_id", migrationID),
		slog.String("action", string(action)),
		slog.String("status", StatusConsumed),
	)
	return nil
}

func (s *Service) ReleaseApproval(ctx context.Context, key string) {
	if key == "" {
		return
	}
	_, err := s.DB.ExecContext(ctx, `
		UPDATE platform_approval_requests
		SET status = 'approved', consumed_idempotency_key = NULL, consumed_at = NULL, updated_at = now()
		WHERE consumed_idempotency_key = $1 AND status = 'consumed' AND consumed_job_id IS NULL`, key)
	if err != nil {
		telemetry.LogEvent(ctx, slog.LevelWarn, "approval.release_failed",
			slog.String("error", err.Error()),
			slog.String("status", "warning"),
		)
	}
}

func (s *Service) AttachApprovalJob(ctx context.Context, key, jobID string) {
	if key == "" || jobID == "" {
		return
	}
	_, _ = s.DB.ExecContext(ctx, `
		UPDATE platform_approval_requests SET consumed_job_id = $2::uuid, updated_at = now()
		WHERE consumed_idempotency_key = $1 AND consumed_job_id IS NULL`, key, jobID)
}

func (s *Service) DefinitionChanged(ctx context.Context, databaseID, migrationID int64, actor string) {
	res, err := s.DB.ExecContext(ctx, `
		UPDATE platform_approval_requests SET status = 'superseded', updated_at = now()
		WHERE database_id = $1 AND migration_id = $2 AND status IN ('pending_review', 'approved')`,
		databaseID, migrationID)
	if err != nil {
		telemetry.LogEvent(ctx, slog.LevelWarn, "approval.supersede_failed",
			slog.Int64("migration_id", migrationID),
			slog.String("error", err.Error()),
			slog.String("status", "warning"),
		)
		return
	}
	reset := "draft"
	if !s.RequireApproval {
		reset = "approved"
	}
	_, _ = s.DB.ExecContext(ctx, `
		UPDATE platform_migrations SET workflow_status = $3, updated_at = now()
		WHERE database_id = $1 AND id = $2 AND workflow_status IN ('pending_review', 'approved', 'rejected')`,
		databaseID, migrationID, reset)
	if n, _ := res.RowsAffected(); n > 0 {
		telemetry.LogEvent(ctx, slog.LevelInfo, "approval.superseded",
			slog.Int64("migration_id", migrationID),
			slog.Int64("superseded_requests", n),
			slog.String("actor", actor),
			slog.String("status", StatusSuperseded),
		)
	}
}
