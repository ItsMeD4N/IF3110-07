package approval

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/dblock"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/migration"
	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
)

type Handler struct {
	Svc    *Service
	Engine *migration.Engine
	MW     *authz.Middleware
	AuthMW *auth.Middleware
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	db := func(p authz.Permission, fn http.HandlerFunc) http.Handler {
		return h.AuthMW.RequireAuth(h.MW.RequireDB(p, fn))
	}

	mux.Handle("POST /api/databases/{dbID}/migrations/definitions/{migrationID}/approval-requests",
		db(authz.PermDBExecuteMigration, h.apiCreate))
	mux.Handle("GET /api/databases/{dbID}/migrations/definitions/{migrationID}/approval-requests",
		db(authz.PermDBViewMigrations, h.apiListForMigration))
	mux.Handle("GET /api/databases/{dbID}/migrations/definitions/{migrationID}/history",
		db(authz.PermDBViewMigrations, h.apiMigrationHistory))

	mux.Handle("GET /api/databases/{dbID}/approval-requests", db(authz.PermDBViewMigrations, h.apiList))
	mux.Handle("GET /api/databases/{dbID}/approval-requests/{requestID}", db(authz.PermDBViewMigrations, h.apiGet))
	mux.Handle("POST /api/databases/{dbID}/approval-requests/{requestID}/approve", db(authz.PermDBViewMigrations, h.apiApprove))
	mux.Handle("POST /api/databases/{dbID}/approval-requests/{requestID}/reject", db(authz.PermDBViewMigrations, h.apiReject))

	mux.Handle("GET /api/approvals/pending", h.AuthMW.RequireAuth(h.MW.RequireAuth(http.HandlerFunc(h.apiPending))))
}

func dbID(r *http.Request) int64 {
	a, _ := authz.DBAccessFrom(r.Context())
	return a.DatabaseID
}

func pathInt(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func withCtx(r *http.Request) *http.Request {
	ctx := telemetry.WithDatabaseID(r.Context(), dbID(r))
	if p := authz.PrincipalFrom(ctx); p != nil {
		ctx = telemetry.WithActor(ctx, p.Username)
	}
	return r.WithContext(ctx)
}

func WriteErr(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	var lm *LeaderMissingError
	var mve *migration.ValidationError
	var be *dblock.BlockedError
	switch {
	case errors.As(err, &ve):
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, ve.Msg)
	case errors.As(err, &mve):
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, mve.Msg)
	case errors.As(err, &lm):
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":  "Group berikut belum memiliki leader: " + strings.Join(lm.Groups, ", ") + ". Minta Platform Admin menetapkan leader terlebih dahulu.",
			"code":   "group_leader_missing",
			"groups": lm.Groups,
		})
	case errors.Is(err, ErrNotFound), errors.Is(err, migration.ErrMigrationNotFound), errors.Is(err, migration.ErrExecutionNotFound):
		httpx.NotFound(w)
	case errors.Is(err, ErrNotLeader):
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, err.Error())
	case errors.Is(err, ErrAlreadyOpen):
		httpx.WriteError(w, http.StatusConflict, "approval_already_open", err.Error())
	case errors.Is(err, ErrAlreadyDecided):
		httpx.WriteError(w, http.StatusConflict, "already_decided", err.Error())
	case errors.Is(err, ErrNotPending):
		httpx.WriteError(w, http.StatusConflict, "invalid_state", err.Error())
	case errors.Is(err, migration.ErrAlreadyActive):
		httpx.WriteError(w, http.StatusConflict, "already_active", err.Error())
	case errors.Is(err, migration.ErrNotLatestActive):
		httpx.WriteError(w, http.StatusConflict, "not_latest_active", err.Error())
	case errors.Is(err, migration.ErrInvalidForwardOrder):
		httpx.WriteError(w, http.StatusConflict, "invalid_forward_order", err.Error())
	case errors.Is(err, migration.ErrInvalidState):
		httpx.WriteError(w, http.StatusConflict, "invalid_state", err.Error())
	case errors.Is(err, migration.ErrIrreversibleMigration):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "migration_irreversible", err.Error())
	case errors.Is(err, migration.ErrUnsafeBatchRevert):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "unsafe_batch_revert", err.Error())
	case errors.Is(err, queue.ErrIdempotencyConflict):
		httpx.WriteError(w, http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.As(err, &be):
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{
			"error": be.Error(), "code": "database_blocked",
			"lock_state": be.State, "blocking_execution_id": be.ExecutionID,
		})
	default:
		httpx.Internal(w, r, err)
	}
}

func (h *Handler) apiCreate(w http.ResponseWriter, r *http.Request) {
	r = withCtx(r)
	migID, ok := pathInt(r, "migrationID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	var in CreateInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	req, err := h.Svc.Create(r.Context(), dbID(r), migID, authz.PrincipalFrom(r.Context()), in)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, req)
}

func (h *Handler) apiList(w http.ResponseWriter, r *http.Request) {
	r = withCtx(r)
	q := r.URL.Query()
	f := ListFilter{Status: strings.TrimSpace(q.Get("status"))}
	if f.Status != "" {
		switch f.Status {
		case StatusPendingReview, StatusApproved, StatusRejected, StatusConsumed, StatusSuperseded:
		default:
			httpx.ValidationError(w, "status tidak dikenal.")
			return
		}
	}
	if v := q.Get("migration_id"); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			httpx.ValidationError(w, "migration_id tidak valid.")
			return
		}
		f.MigrationID = id
	}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	list, err := h.Svc.List(r.Context(), dbID(r), f, authz.PrincipalFrom(r.Context()))
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"approval_requests": list})
}

func (h *Handler) apiListForMigration(w http.ResponseWriter, r *http.Request) {
	r = withCtx(r)
	migID, ok := pathInt(r, "migrationID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	list, err := h.Svc.List(r.Context(), dbID(r), ListFilter{MigrationID: migID, Limit: maxLimit}, authz.PrincipalFrom(r.Context()))
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"approval_requests": list})
}

func (h *Handler) apiGet(w http.ResponseWriter, r *http.Request) {
	r = withCtx(r)
	id, ok := pathInt(r, "requestID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	req, err := h.Svc.Get(r.Context(), dbID(r), id, authz.PrincipalFrom(r.Context()))
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, req)
}

type decisionBody struct {
	Comment string `json:"comment"`
	GroupID int64  `json:"group_id,omitempty"`
}

func (h *Handler) decide(w http.ResponseWriter, r *http.Request, approve bool) {
	r = withCtx(r)
	id, ok := pathInt(r, "requestID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	var body decisionBody
	if r.ContentLength != 0 {
		if !httpx.DecodeJSON(w, r, &body) {
			return
		}
	}
	req, err := h.Svc.Decide(r.Context(), dbID(r), id, authz.PrincipalFrom(r.Context()), approve, body.Comment, body.GroupID)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, req)
}

func (h *Handler) apiApprove(w http.ResponseWriter, r *http.Request) { h.decide(w, r, true) }
func (h *Handler) apiReject(w http.ResponseWriter, r *http.Request)  { h.decide(w, r, false) }

func (h *Handler) apiPending(w http.ResponseWriter, r *http.Request) {
	list, err := h.Svc.PendingForLeader(r.Context(), authz.PrincipalFrom(r.Context()))
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"approval_requests": list})
}

func (h *Handler) apiMigrationHistory(w http.ResponseWriter, r *http.Request) {
	r = withCtx(r)
	migID, ok := pathInt(r, "migrationID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	def, err := h.Engine.GetDefinition(r.Context(), dbID(r), migID)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	hide := r.URL.Query().Get("hide_rollback") == "true" || r.URL.Query().Get("hide_rollback") == "1"
	execs, err := h.Engine.ListExecutions(r.Context(), dbID(r), hide)
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	execs = migration.FilterExecutions(execs, migration.ExecutionFilter{MigrationID: migID})
	reqs, err := h.Svc.List(r.Context(), dbID(r), ListFilter{MigrationID: migID, Limit: maxLimit}, authz.PrincipalFrom(r.Context()))
	if err != nil {
		WriteErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"definition":        def,
		"hide_rollback":     hide,
		"executions":        execs,
		"approval_requests": reqs,
	})
}
