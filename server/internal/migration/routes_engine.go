package migration

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/dblock"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
)

func (m *Module) registerEngineRoutes(mux *http.ServeMux) {
	if m.Engine == nil {
		return
	}
	api := func(p authz.Permission, h http.HandlerFunc) http.Handler {
		return m.AuthMW.RequireAuth(m.MW.RequireDB(p, h))
	}

	mux.Handle("GET /api/databases/{dbID}/migrations/definitions", api(authz.PermDBViewMigrations, m.apiListDefinitions))
	mux.Handle("POST /api/databases/{dbID}/migrations/definitions", api(authz.PermDBCreateMigration, m.apiCreateDefinition))
	mux.Handle("GET /api/databases/{dbID}/migrations/definitions/{migrationID}", api(authz.PermDBViewMigrations, m.apiGetDefinition))
	mux.Handle("PUT /api/databases/{dbID}/migrations/definitions/{migrationID}", api(authz.PermDBEditMigration, m.apiUpdateDefinition))

	mux.Handle("POST /api/databases/{dbID}/migrations/definitions/{migrationID}/execute", api(authz.PermDBExecuteMigration, m.apiEnqueueExecute))
	mux.Handle("POST /api/databases/{dbID}/migrations/definitions/{migrationID}/rollback", api(authz.PermDBExecuteMigration, m.apiEnqueueRollback))
	mux.Handle("POST /api/databases/{dbID}/migrations/definitions/{migrationID}/forward", api(authz.PermDBExecuteMigration, m.apiEnqueueForward))

	mux.Handle("GET /api/databases/{dbID}/migrations/executions", api(authz.PermDBViewMigrations, m.apiListExecutions))
	mux.Handle("GET /api/databases/{dbID}/migrations/executions/{executionID}", api(authz.PermDBViewMigrations, m.apiGetExecution))
	mux.Handle("POST /api/databases/{dbID}/migrations/executions/{executionID}/resume", api(authz.PermDBExecuteMigration, m.apiEnqueueBatchResume))
	mux.Handle("POST /api/databases/{dbID}/migrations/executions/{executionID}/revert", api(authz.PermDBExecuteMigration, m.apiEnqueueBatchRevert))
	mux.Handle("POST /api/databases/{dbID}/migrations/executions/{executionID}/reconcile", api(authz.PermDBExecuteMigration, m.apiReconcileExecution))

	mux.Handle("GET /api/databases/{dbID}/jobs", api(authz.PermDBViewMigrations, m.apiListJobs))
	mux.Handle("GET /api/databases/{dbID}/jobs/{jobID}", api(authz.PermDBViewMigrations, m.apiGetJob))
	mux.Handle("GET /api/databases/{dbID}/lock", api(authz.PermDBViewMigrations, m.apiGetLock))
}

type enqueueReqBody struct {
	IdempotencyKey string `json:"idempotency_key,omitempty"`
	MaxAttempts    int    `json:"max_attempts,omitempty"`
}

func decodeOptionalEnqueueBody(w http.ResponseWriter, r *http.Request) (EnqueueOptions, bool) {
	if r.Body == nil || r.ContentLength == 0 {
		return EnqueueOptions{IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key"))}, true
	}
	var body enqueueReqBody
	if !httpx.DecodeJSON(w, r, &body) {
		return EnqueueOptions{}, false
	}
	key := strings.TrimSpace(body.IdempotencyKey)
	if key == "" {
		key = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	}
	return EnqueueOptions{IdempotencyKey: key, MaxAttempts: body.MaxAttempts}, true
}

func writeEngineErr(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	var be *dblock.BlockedError
	switch {
	case errors.As(err, &ve):
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, ve.Msg)
	case errors.Is(err, ErrMigrationNotFound), errors.Is(err, ErrExecutionNotFound), errors.Is(err, queue.ErrJobNotFound):
		httpx.NotFound(w)
	case errors.Is(err, queue.ErrIdempotencyConflict):
		httpx.WriteError(w, http.StatusConflict, "idempotency_conflict", err.Error())
	case errors.Is(err, ErrImmutableMigration):
		httpx.WriteError(w, http.StatusConflict, "migration_immutable", err.Error())
	case errors.Is(err, ErrIrreversibleMigration):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "migration_irreversible", err.Error())
	case errors.Is(err, ErrUnsafeBatchRevert):
		httpx.WriteError(w, http.StatusUnprocessableEntity, "unsafe_batch_revert", err.Error())
	case errors.Is(err, ErrNotLatestActive):
		httpx.WriteError(w, http.StatusConflict, "not_latest_active", err.Error())
	case errors.Is(err, ErrInvalidForwardOrder):
		httpx.WriteError(w, http.StatusConflict, "invalid_forward_order", err.Error())
	case errors.Is(err, ErrAlreadyActive):
		httpx.WriteError(w, http.StatusConflict, "already_active", err.Error())
	case errors.Is(err, ErrInvalidState):
		httpx.WriteError(w, http.StatusConflict, "invalid_state", err.Error())
	case errors.Is(err, ErrApprovalRequired):
		httpx.WriteError(w, http.StatusForbidden, "approval_required", err.Error())
	case errors.As(err, &be):
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{
			"error":                 be.Error(),
			"code":                  "database_blocked",
			"lock_state":            be.State,
			"blocking_execution_id": be.ExecutionID,
		})
	default:
		httpx.ConnError(w, r, err)
	}
}

func withRequestContext(r *http.Request) *http.Request {
	ctx := telemetry.WithDatabaseID(r.Context(), dbID(r))
	if act := executor(r); act != "" {
		ctx = telemetry.WithActor(ctx, act)
	}
	return r.WithContext(ctx)
}

func (m *Module) apiListDefinitions(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	defs, err := m.Engine.ListDefinitions(r.Context(), dbID(r))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"migrations": defs})
}

func (m *Module) apiCreateDefinition(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	var in CreateDefinitionInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	def, err := m.Engine.CreateDefinition(r.Context(), dbID(r), executor(r), in)
	if err != nil {
		writeEngineErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, def)
}

func (m *Module) apiGetDefinition(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	migID, ok := pathInt(r, "migrationID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	def, err := m.Engine.GetDefinition(r.Context(), dbID(r), migID)
	if err != nil {
		writeEngineErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, def)
}

func (m *Module) apiUpdateDefinition(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	migID, ok := pathInt(r, "migrationID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	var in UpdateDefinitionInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	def, err := m.Engine.UpdateDefinition(r.Context(), dbID(r), migID, executor(r), in)
	if err != nil {
		writeEngineErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, def)
}

func (m *Module) apiEnqueueExecute(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	migID, ok := pathInt(r, "migrationID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	opts, ok := decodeOptionalEnqueueBody(w, r)
	if !ok {
		return
	}
	job, dedup, err := m.Engine.EnqueueExecute(r.Context(), dbID(r), migID, executor(r), opts)
	if err != nil {
		writeEngineErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"message":      "Migration dimasukkan ke antrean untuk dijalankan secara asynchronous.",
		"job_id":       job.ID,
		"deduplicated": dedup,
		"job":          job,
	})
}

func (m *Module) apiEnqueueRollback(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	migID, ok := pathInt(r, "migrationID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	opts, ok := decodeOptionalEnqueueBody(w, r)
	if !ok {
		return
	}
	job, dedup, err := m.Engine.EnqueueRollback(r.Context(), dbID(r), migID, executor(r), opts)
	if err != nil {
		writeEngineErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"message":      "Rollback migration dimasukkan ke antrean untuk dijalankan secara asynchronous.",
		"job_id":       job.ID,
		"deduplicated": dedup,
		"job":          job,
	})
}

func (m *Module) apiEnqueueForward(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	migID, ok := pathInt(r, "migrationID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	opts, ok := decodeOptionalEnqueueBody(w, r)
	if !ok {
		return
	}
	job, dedup, err := m.Engine.EnqueueForward(r.Context(), dbID(r), migID, executor(r), opts)
	if err != nil {
		writeEngineErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"message":      "Forward migration dimasukkan ke antrean untuk dijalankan secara asynchronous.",
		"job_id":       job.ID,
		"deduplicated": dedup,
		"job":          job,
	})
}

func (m *Module) apiListExecutions(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	hideRollback := r.URL.Query().Get("hide_rollback") == "true" || r.URL.Query().Get("hide_rollback") == "1"
	execs, err := m.Engine.ListExecutions(r.Context(), dbID(r), hideRollback)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"hide_rollback": hideRollback,
		"executions":    execs,
	})
}

func (m *Module) apiGetExecution(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	execID, ok := pathInt(r, "executionID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	ex, err := m.Engine.GetExecution(r.Context(), dbID(r), execID)
	if err != nil {
		writeEngineErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, ex)
}

func (m *Module) apiEnqueueBatchResume(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	execID, ok := pathInt(r, "executionID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	opts, ok := decodeOptionalEnqueueBody(w, r)
	if !ok {
		return
	}
	job, dedup, err := m.Engine.EnqueueBatchResume(r.Context(), dbID(r), execID, executor(r), opts)
	if err != nil {
		writeEngineErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"message":      "Resume batch migration dari checkpoint terakhir dimasukkan ke antrean.",
		"job_id":       job.ID,
		"deduplicated": dedup,
		"job":          job,
	})
}

func (m *Module) apiEnqueueBatchRevert(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	execID, ok := pathInt(r, "executionID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	opts, ok := decodeOptionalEnqueueBody(w, r)
	if !ok {
		return
	}
	job, dedup, err := m.Engine.EnqueueBatchRevert(r.Context(), dbID(r), execID, executor(r), opts)
	if err != nil {
		writeEngineErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
		"message":      "Pembatalan batch committed (urutan terbalik) dimasukkan ke antrean.",
		"job_id":       job.ID,
		"deduplicated": dedup,
		"job":          job,
	})
}

func (m *Module) apiReconcileExecution(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	execID, ok := pathInt(r, "executionID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	ex, err := m.Engine.ReconcileExecution(r.Context(), dbID(r), execID, executor(r))
	if err != nil {
		writeEngineErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"message":   "Rekonsiliasi bukti eksekusi selesai.",
		"execution": ex,
	})
}

func (m *Module) apiListJobs(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	jobs, err := m.Engine.Broker.ListJobs(r.Context(), dbID(r), limit)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (m *Module) apiGetJob(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	jobID := strings.TrimSpace(r.PathValue("jobID"))
	if jobID == "" {
		httpx.NotFound(w)
		return
	}
	job, err := m.Engine.Broker.GetJob(r.Context(), dbID(r), jobID)
	if err != nil {
		writeEngineErr(w, r, err)
		return
	}
	resp := map[string]any{"job": job}
	if job.ExecutionID > 0 {
		if ex, eErr := m.Engine.GetExecution(r.Context(), dbID(r), job.ExecutionID); eErr == nil {
			resp["execution"] = ex
		}
	} else if ex, eErr := m.Engine.GetLatestExecutionByJobID(r.Context(), dbID(r), job.ID); eErr == nil {
		job.ExecutionID = ex.ID
		resp["job"] = job
		resp["execution"] = ex
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

func (m *Module) apiGetLock(w http.ResponseWriter, r *http.Request) {
	r = withRequestContext(r)
	info, err := m.Engine.Locker.Inspect(r.Context(), dbID(r))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, info)
}
