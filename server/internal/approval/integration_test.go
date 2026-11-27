package approval_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/app"
	"github.com/ItsMeD4N/IF3110-07/server/internal/approval"
	"github.com/ItsMeD4N/IF3110-07/server/internal/apptest"
	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/migration"
	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

type fixture struct {
	e                     *apptest.Env
	admin, editor, viewer *auth.User
	dev2, outsider        *auth.User
	devGroup, anaGroup    int64
	dbID                  int64
	target                *sql.DB
	schema                string
}

func newEnv(t *testing.T, requireApproval bool) *apptest.Env {
	return apptest.NewWithOptions(t, func(o *app.Options) { o.RequireApproval = requireApproval })
}

func (f *fixture) group(t *testing.T, level string, members ...*auth.User) int64 {
	t.Helper()
	rec := f.e.Do(f.admin, "POST", "/api/admin/groups", map[string]string{"name": testutil.Unique("g")})
	apptest.Expect(t, rec, http.StatusCreated)
	gid := apptest.Decode[struct{ ID int64 }](t, rec).ID
	t.Cleanup(func() { f.e.Do(f.admin, "DELETE", fmt.Sprintf("/api/admin/groups/%d", gid), nil) })
	for _, m := range members {
		apptest.Expect(t, f.e.Do(f.admin, "POST", fmt.Sprintf("/api/admin/groups/%d/members", gid), map[string]string{"user_id": m.ID}), http.StatusCreated)
	}
	apptest.Expect(t, f.e.Do(f.admin, "PUT", fmt.Sprintf("/api/admin/groups/%d/grants/%d", gid, f.dbID), map[string]string{"access_level": level}), http.StatusOK)
	return gid
}

func (f *fixture) setLeader(t *testing.T, gid int64, u *auth.User) {
	t.Helper()
	apptest.Expect(t, f.e.Do(f.admin, "PUT", fmt.Sprintf("/api/admin/groups/%d/leader", gid), map[string]string{"user_id": u.ID}), http.StatusOK)
}

func setup(t *testing.T, requireApproval bool) *fixture {
	t.Helper()
	e := newEnv(t, requireApproval)
	f := &fixture{e: e, admin: e.NewUser(true), editor: e.NewUser(false), viewer: e.NewUser(false), dev2: e.NewUser(false), outsider: e.NewUser(false)}
	f.target, f.schema = testutil.OpenTargetSchema(t)
	f.dbID = e.Database(f.admin, apptest.TargetConn(t))
	f.devGroup = f.group(t, "view_edit", f.editor, f.dev2)
	f.anaGroup = f.group(t, "view_only", f.viewer)
	f.setLeader(t, f.devGroup, f.editor)
	f.setLeader(t, f.anaGroup, f.viewer)
	return f
}

func (f *fixture) path(format string, a ...any) string {
	return fmt.Sprintf("/api/databases/%d", f.dbID) + fmt.Sprintf(format, a...)
}

func (f *fixture) table(name string) string { return f.schema + "." + name }

func (f *fixture) createDef(t *testing.T, name, up, down string) migration.Definition {
	t.Helper()
	rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{Name: name, SQLUp: up, SQLDown: down})
	apptest.Expect(t, rec, http.StatusCreated)
	return apptest.Decode[migration.Definition](t, rec)
}

func (f *fixture) getDef(t *testing.T, id int64) migration.Definition {
	t.Helper()
	rec := f.e.Do(f.viewer, "GET", f.path("/migrations/definitions/%d", id), nil)
	apptest.Expect(t, rec, http.StatusOK)
	return apptest.Decode[migration.Definition](t, rec)
}

func (f *fixture) submit(t *testing.T, as *auth.User, migID int64, action string, wantStatus int) approval.Request {
	t.Helper()
	rec := f.e.Do(as, "POST", f.path("/migrations/definitions/%d/approval-requests", migID), map[string]any{"action": action, "note": "tolong direview"})
	apptest.Expect(t, rec, wantStatus)
	if wantStatus != http.StatusCreated {
		return approval.Request{}
	}
	return apptest.Decode[approval.Request](t, rec)
}

func (f *fixture) decide(t *testing.T, as *auth.User, reqID int64, verb, comment string, want int) *httptest.ResponseRecorder {
	t.Helper()
	rec := f.e.Do(as, "POST", f.path("/approval-requests/%d/%s", reqID, verb), map[string]string{"comment": comment})
	apptest.Expect(t, rec, want)
	return rec
}

func (f *fixture) approveAll(t *testing.T, reqID int64) approval.Request {
	t.Helper()
	f.decide(t, f.viewer, reqID, "approve", "", http.StatusOK)
	rec := f.decide(t, f.editor, reqID, "approve", "aman", http.StatusOK)
	r := apptest.Decode[approval.Request](t, rec)
	if r.Status != approval.StatusApproved {
		t.Fatalf("status setelah semua leader approve = %s", r.Status)
	}
	return r
}

type enqueueResp struct {
	JobID        string    `json:"job_id"`
	Deduplicated bool      `json:"deduplicated"`
	Job          queue.Job `json:"job"`
}

func (f *fixture) waitJob(t *testing.T, jobID string) queue.Job {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		rec := f.e.Do(f.viewer, "GET", f.path("/jobs/%s", jobID), nil)
		apptest.Expect(t, rec, http.StatusOK)
		j := apptest.Decode[struct {
			Job queue.Job `json:"job"`
		}](t, rec).Job
		if j.IsTerminal() {
			return j
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout menunggu job %s", jobID)
	return queue.Job{}
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return apptest.Decode[struct {
		Code string `json:"code"`
	}](t, rec).Code
}

func (f *fixture) getRequest(t *testing.T, as *auth.User, id int64) approval.Request {
	t.Helper()
	rec := f.e.Do(as, "GET", f.path("/approval-requests/%d", id), nil)
	apptest.Expect(t, rec, http.StatusOK)
	return apptest.Decode[approval.Request](t, rec)
}

func TestWorkflow_FullLifecycleWithMultiGroupApproval(t *testing.T) {
	f := setup(t, true)
	def := f.createDef(t, "create_orders",
		fmt.Sprintf(`CREATE TABLE %s (id int PRIMARY KEY);`, f.table("orders")),
		fmt.Sprintf(`DROP TABLE %s;`, f.table("orders")))
	if def.WorkflowStatus != "draft" {
		t.Fatalf("workflow awal = %s, want draft", def.WorkflowStatus)
	}

	rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID), nil)
	apptest.Expect(t, rec, http.StatusForbidden)
	if c := errCode(t, rec); c != "approval_required" {
		t.Fatalf("code = %s", c)
	}

	f.submit(t, f.viewer, def.ID, "up", http.StatusForbidden)

	f.submit(t, f.outsider, def.ID, "up", http.StatusNotFound)

	req := f.submit(t, f.editor, def.ID, "up", http.StatusCreated)
	if req.Status != approval.StatusPendingReview || req.RequiredApprovals != 2 || req.ApprovedCount != 0 || len(req.Approvals) != 2 {
		t.Fatalf("request = %+v", req)
	}
	if !req.CanCurrentUserDecide {
		t.Fatal("editor adalah leader group dev, seharusnya can_current_user_decide = true")
	}
	if got := f.getDef(t, def.ID).WorkflowStatus; got != "pending_review" {
		t.Fatalf("workflow = %s, want pending_review", got)
	}

	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/approval-requests", def.ID), map[string]any{"action": "up"})
	apptest.Expect(t, rec, http.StatusConflict)
	if c := errCode(t, rec); c != "approval_already_open" {
		t.Fatalf("code = %s", c)
	}

	rec = f.e.Do(f.viewer, "GET", "/api/approvals/pending", nil)
	apptest.Expect(t, rec, http.StatusOK)
	inbox := apptest.Decode[struct {
		ApprovalRequests []approval.Request `json:"approval_requests"`
	}](t, rec).ApprovalRequests
	found := false
	for _, r := range inbox {
		if r.ID == req.ID {
			found = r.CanCurrentUserDecide
		}
	}
	if !found {
		t.Fatalf("request %d tidak muncul di inbox leader viewer: %+v", req.ID, inbox)
	}

	f.decide(t, f.dev2, req.ID, "approve", "", http.StatusForbidden)

	f.decide(t, f.outsider, req.ID, "approve", "", http.StatusNotFound)

	r1 := apptest.Decode[approval.Request](t, f.decide(t, f.viewer, req.ID, "approve", "aman untuk analytics", http.StatusOK))
	if r1.Status != approval.StatusPendingReview || r1.ApprovedCount != 1 || r1.CanCurrentUserDecide {
		t.Fatalf("setelah approve viewer = %+v", r1)
	}
	rec = f.decide(t, f.viewer, req.ID, "approve", "", http.StatusConflict)
	if c := errCode(t, rec); c != "already_decided" {
		t.Fatalf("code = %s", c)
	}

	apptest.Expect(t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID), nil), http.StatusForbidden)

	f.decide(t, f.editor, req.ID, "approve", "", http.StatusOK)
	if got := f.getDef(t, def.ID).WorkflowStatus; got != "approved" {
		t.Fatalf("workflow = %s, want approved", got)
	}

	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID), map[string]any{"idempotency_key": "exec-" + testutil.Unique("k")})
	apptest.Expect(t, rec, http.StatusAccepted)
	enq := apptest.Decode[enqueueResp](t, rec)
	job := f.waitJob(t, enq.JobID)
	if job.Status != queue.StatusCompleted {
		t.Fatalf("job status = %s (%v)", job.Status, job.LastError)
	}
	consumed := f.getRequest(t, f.viewer, req.ID)
	if consumed.Status != approval.StatusConsumed || consumed.JobID != enq.JobID {
		t.Fatalf("request setelah enqueue = %+v", consumed)
	}
	if got := f.getDef(t, def.ID); !got.IsActive || got.WorkflowStatus != "applied" {
		t.Fatalf("definisi setelah sukses = active:%v workflow:%s", got.IsActive, got.WorkflowStatus)
	}

	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/rollback", def.ID), nil)
	apptest.Expect(t, rec, http.StatusForbidden)
	rb := f.submit(t, f.editor, def.ID, "rollback", http.StatusCreated)
	if rb.TargetExecutionID == nil || *rb.TargetExecutionID == 0 {
		t.Fatalf("rollback request harus merujuk execution aktif: %+v", rb)
	}

	f.submit(t, f.editor, def.ID, "up", http.StatusConflict)
	f.approveAll(t, rb.ID)
	if got := f.getDef(t, def.ID).WorkflowStatus; got != "applied" {
		t.Fatalf("workflow berubah oleh approval rollback: %s", got)
	}
	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/rollback", def.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	if j := f.waitJob(t, apptest.Decode[enqueueResp](t, rec).JobID); j.Status != queue.StatusCompleted {
		t.Fatalf("rollback job = %s", j.Status)
	}

	apptest.Expect(t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/forward", def.ID), nil), http.StatusForbidden)
	fw := f.submit(t, f.editor, def.ID, "forward", http.StatusCreated)
	f.approveAll(t, fw.ID)
	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/forward", def.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	if j := f.waitJob(t, apptest.Decode[enqueueResp](t, rec).JobID); j.Status != queue.StatusCompleted {
		t.Fatalf("forward job = %s", j.Status)
	}

	rec = f.e.Do(f.viewer, "GET", f.path("/migrations/definitions/%d/history", def.ID), nil)
	apptest.Expect(t, rec, http.StatusOK)
	hist := apptest.Decode[struct {
		Executions       []migration.Execution `json:"executions"`
		ApprovalRequests []approval.Request    `json:"approval_requests"`
	}](t, rec)
	if len(hist.ApprovalRequests) != 3 || len(hist.Executions) != 3 {
		t.Fatalf("history: %d approval, %d eksekusi", len(hist.ApprovalRequests), len(hist.Executions))
	}
	for _, r := range hist.ApprovalRequests {
		if r.Status != approval.StatusConsumed {
			t.Fatalf("approval %d (%s) status %s", r.ID, r.Action, r.Status)
		}
	}
	rec = f.e.Do(f.viewer, "GET", f.path("/migrations/definitions/%d/history?hide_rollback=true", def.ID), nil)
	apptest.Expect(t, rec, http.StatusOK)
	hidden := apptest.Decode[struct {
		Executions []migration.Execution `json:"executions"`
	}](t, rec).Executions
	if len(hidden) != 1 || hidden[0].Action != migration.ActionForward {
		t.Fatalf("hide rollback = %+v", hidden)
	}
}

func TestWorkflow_RejectEditSupersedeAndRevisionGuard(t *testing.T) {
	f := setup(t, true)
	def := f.createDef(t, "add_tbl",
		fmt.Sprintf(`CREATE TABLE %s (id int);`, f.table("t1")),
		fmt.Sprintf(`DROP TABLE %s;`, f.table("t1")))

	req := f.submit(t, f.editor, def.ID, "up", http.StatusCreated)
	f.decide(t, f.viewer, req.ID, "reject", "  ", http.StatusUnprocessableEntity)
	r := apptest.Decode[approval.Request](t, f.decide(t, f.viewer, req.ID, "reject", "kolom kurang index", http.StatusOK))
	if r.Status != approval.StatusRejected {
		t.Fatalf("status = %s", r.Status)
	}
	if got := f.getDef(t, def.ID).WorkflowStatus; got != "rejected" {
		t.Fatalf("workflow = %s, want rejected", got)
	}
	rec := f.decide(t, f.editor, req.ID, "approve", "", http.StatusConflict)
	if c := errCode(t, rec); c != "invalid_state" {
		t.Fatalf("code = %s", c)
	}

	upd := migration.UpdateDefinitionInput{
		Name:    "add_tbl_v2",
		SQLUp:   fmt.Sprintf(`CREATE TABLE %s (id int PRIMARY KEY);`, f.table("t1")),
		SQLDown: fmt.Sprintf(`DROP TABLE %s;`, f.table("t1")),
	}
	rec = f.e.Do(f.editor, "PUT", f.path("/migrations/definitions/%d", def.ID), upd)
	apptest.Expect(t, rec, http.StatusOK)
	if got := apptest.Decode[migration.Definition](t, rec).WorkflowStatus; got != "draft" {
		t.Fatalf("workflow setelah edit = %s, want draft", got)
	}
	req2 := f.submit(t, f.editor, def.ID, "up", http.StatusCreated)
	f.approveAll(t, req2.ID)

	upd.Name = "add_tbl_v3"
	apptest.Expect(t, f.e.Do(f.editor, "PUT", f.path("/migrations/definitions/%d", def.ID), upd), http.StatusOK)
	if got := f.getRequest(t, f.viewer, req2.ID).Status; got != approval.StatusSuperseded {
		t.Fatalf("request lama = %s, want superseded", got)
	}
	apptest.Expect(t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID), nil), http.StatusForbidden)

	rec = f.e.Do(f.viewer, "GET", f.path("/approval-requests?status=superseded&migration_id=%d", def.ID), nil)
	apptest.Expect(t, rec, http.StatusOK)
	list := apptest.Decode[struct {
		ApprovalRequests []approval.Request `json:"approval_requests"`
	}](t, rec).ApprovalRequests
	if len(list) != 1 || list[0].ID != req2.ID {
		t.Fatalf("filter superseded = %+v", list)
	}
	apptest.Expect(t, f.e.Do(f.viewer, "GET", f.path("/approval-requests?status=bogus"), nil), http.StatusUnprocessableEntity)
}

func TestWorkflow_LeaderMissingAndValidation(t *testing.T) {
	f := setup(t, true)
	third := f.e.NewUser(false)
	f.group(t, "view_only", third)
	def := f.createDef(t, "x", `SELECT 1`, `SELECT 1`)
	rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/approval-requests", def.ID), map[string]any{"action": "up"})
	apptest.Expect(t, rec, http.StatusUnprocessableEntity)
	if c := errCode(t, rec); c != "group_leader_missing" {
		t.Fatalf("code = %s", c)
	}
	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/approval-requests", def.ID), map[string]any{"action": "drop_everything"})
	apptest.Expect(t, rec, http.StatusUnprocessableEntity)
	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/approval-requests", def.ID), map[string]any{"action": "rollback"})
	apptest.Expect(t, rec, http.StatusConflict)
	apptest.Expect(t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/999999999/approval-requests"), map[string]any{"action": "up"}), http.StatusNotFound)
	apptest.Expect(t, f.e.Do(f.viewer, "GET", f.path("/approval-requests/999999999"), nil), http.StatusNotFound)
	apptest.Expect(t, f.e.Do(nil, "GET", "/api/approvals/pending", nil), http.StatusUnauthorized)
}

func TestWorkflow_LegacySyncEndpointBlockedWhenApprovalRequired(t *testing.T) {
	f := setup(t, true)
	rec := f.e.Do(f.editor, "POST", f.path("/migrations"), map[string]string{"name": "x", "sql": "SELECT 1"})
	apptest.Expect(t, rec, http.StatusForbidden)
	if c := errCode(t, rec); c != "approval_required" {
		t.Fatalf("code = %s", c)
	}
}

func TestWorkflow_ApprovalOptionalMode(t *testing.T) {
	f := setup(t, false)
	def := f.createDef(t, "opt", fmt.Sprintf(`CREATE TABLE %s (id int);`, f.table("opt")), fmt.Sprintf(`DROP TABLE %s;`, f.table("opt")))
	if def.WorkflowStatus != "approved" {
		t.Fatalf("workflow default tanpa approval = %s", def.WorkflowStatus)
	}
	req := f.submit(t, f.editor, def.ID, "up", http.StatusCreated)
	if req.Status != approval.StatusPendingReview {
		t.Fatalf("status = %s", req.Status)
	}
	rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	f.waitJob(t, apptest.Decode[enqueueResp](t, rec).JobID)
}

func TestDefinition_DeleteDraftOnly(t *testing.T) {
	f := setup(t, false)
	draft := f.createDef(t, "draft", `SELECT 1`, `SELECT 1`)
	apptest.Expect(t, f.e.Do(f.viewer, "DELETE", f.path("/migrations/definitions/%d", draft.ID), nil), http.StatusForbidden)
	apptest.Expect(t, f.e.Do(f.editor, "DELETE", f.path("/migrations/definitions/%d", draft.ID), nil), http.StatusNoContent)
	apptest.Expect(t, f.e.Do(f.editor, "GET", f.path("/migrations/definitions/%d", draft.ID), nil), http.StatusNotFound)
	apptest.Expect(t, f.e.Do(f.editor, "DELETE", f.path("/migrations/definitions/%d", draft.ID), nil), http.StatusNotFound)

	ran := f.createDef(t, "ran", fmt.Sprintf(`CREATE TABLE %s (id int);`, f.table("ran")), fmt.Sprintf(`DROP TABLE %s;`, f.table("ran")))
	rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", ran.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	f.waitJob(t, apptest.Decode[enqueueResp](t, rec).JobID)
	rec = f.e.Do(f.editor, "DELETE", f.path("/migrations/definitions/%d", ran.ID), nil)
	apptest.Expect(t, rec, http.StatusConflict)
	if c := errCode(t, rec); c != "migration_immutable" {
		t.Fatalf("code = %s", c)
	}
}

func TestHistory_FiltersAndFailedAttemptsRecorded(t *testing.T) {
	f := setup(t, false)
	ok := f.createDef(t, "ok", fmt.Sprintf(`CREATE TABLE %s (id int);`, f.table("h1")), fmt.Sprintf(`DROP TABLE %s;`, f.table("h1")))
	bad := f.createDef(t, "bad", fmt.Sprintf(`INSERT INTO %s VALUES ('bukan angka');`, f.table("h1")), `SELECT 1`)
	for _, id := range []int64{ok.ID, bad.ID} {
		rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", id), map[string]any{"max_attempts": 1})
		apptest.Expect(t, rec, http.StatusAccepted)
		f.waitJob(t, apptest.Decode[enqueueResp](t, rec).JobID)
	}

	type histResp struct {
		Executions []migration.Execution    `json:"executions"`
		Total      int                      `json:"total"`
		Summary    migration.HistorySummary `json:"summary"`
	}
	rec := f.e.Do(f.viewer, "GET", f.path("/migrations/executions?status=failed"), nil)
	apptest.Expect(t, rec, http.StatusOK)
	h := apptest.Decode[histResp](t, rec)
	if h.Total < 1 {
		t.Fatalf("attempt gagal tidak tercatat: %+v", h)
	}
	for _, ex := range h.Executions {
		if ex.Status != migration.ExecFailed || ex.MigrationID != bad.ID || ex.SQLExecuted == "" || ex.Actor != f.editor.Username {
			t.Fatalf("eksekusi gagal = %+v", ex)
		}
	}
	rec = f.e.Do(f.viewer, "GET", f.path("/migrations/executions?migration_id=%d&limit=1", ok.ID), nil)
	apptest.Expect(t, rec, http.StatusOK)
	h = apptest.Decode[histResp](t, rec)
	if h.Total != 1 || len(h.Executions) != 1 || h.Executions[0].Status != migration.ExecSuccess {
		t.Fatalf("filter migration_id = %+v", h)
	}
	if h.Summary.TotalAttempts < 2 || h.Summary.ByStatus[migration.ExecFailed] < 1 || h.Summary.ActiveVersion != ok.Version {
		t.Fatalf("summary = %+v", h.Summary)
	}
	apptest.Expect(t, f.e.Do(f.viewer, "GET", f.path("/migrations/executions?status=nope"), nil), http.StatusUnprocessableEntity)
	apptest.Expect(t, f.e.Do(f.viewer, "GET", f.path("/migrations/executions?since=kemarin"), nil), http.StatusUnprocessableEntity)
	apptest.Expect(t, f.e.Do(f.outsider, "GET", f.path("/migrations/executions"), nil), http.StatusNotFound)
}

func TestWorkflow_ConcurrentEnqueueConsumesApprovalOnce(t *testing.T) {
	f := setup(t, true)
	def := f.createDef(t, "race", fmt.Sprintf(`CREATE TABLE %s (id int);`, f.table("race")), fmt.Sprintf(`DROP TABLE %s;`, f.table("race")))
	req := f.submit(t, f.editor, def.ID, "up", http.StatusCreated)
	f.approveAll(t, req.ID)

	const n = 6
	codes := make(chan int, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		go func(i int) {
			<-start
			rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID),
				map[string]any{"idempotency_key": fmt.Sprintf("race-%d-%s", i, req.CreatedAt.Format(time.RFC3339Nano))})
			codes <- rec.Code
		}(i)
	}
	close(start)
	accepted := 0
	for i := 0; i < n; i++ {
		switch c := <-codes; c {
		case http.StatusAccepted:
			accepted++
		case http.StatusForbidden, http.StatusConflict:
		default:
			t.Fatalf("status tak terduga %d", c)
		}
	}
	if accepted != 1 {
		t.Fatalf("job diterima = %d, want tepat 1", accepted)
	}
}

func TestWorkflow_IdempotentRetryKeepsApproval(t *testing.T) {
	f := setup(t, true)
	def := f.createDef(t, "idem", fmt.Sprintf(`CREATE TABLE %s (id int);`, f.table("idem")), fmt.Sprintf(`DROP TABLE %s;`, f.table("idem")))
	f.approveAll(t, f.submit(t, f.editor, def.ID, "up", http.StatusCreated).ID)

	key := "idem-" + testutil.Unique("k")
	rec1 := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID), map[string]any{"idempotency_key": key})
	apptest.Expect(t, rec1, http.StatusAccepted)
	first := apptest.Decode[enqueueResp](t, rec1)
	rec2 := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID), map[string]any{"idempotency_key": key})
	if rec2.Code == http.StatusAccepted {
		second := apptest.Decode[enqueueResp](t, rec2)
		if !second.Deduplicated || second.JobID != first.JobID {
			t.Fatalf("retry = %+v, want dedup job %s", second, first.JobID)
		}
	} else if rec2.Code != http.StatusConflict {
		t.Fatalf("retry status = %d body %s", rec2.Code, rec2.Body.String())
	}
	f.waitJob(t, first.JobID)
}

func TestAdmin_SelfDemoteGuardAndDatabaseGroups(t *testing.T) {
	f := setup(t, true)
	rec := f.e.Do(f.admin, "PUT", "/api/admin/users/"+f.admin.ID, map[string]any{
		"username": f.admin.Username, "is_platform_admin": false, "is_active": true,
	})
	apptest.Expect(t, rec, http.StatusBadRequest)

	rec = f.e.Do(f.viewer, "GET", f.path(""), nil)
	apptest.Expect(t, rec, http.StatusOK)
	d := apptest.Decode[struct {
		ViaGroups    []string `json:"via_groups"`
		AccessGroups []struct {
			GroupID        int64   `json:"group_id"`
			AccessLevel    string  `json:"access_level"`
			LeaderUsername *string `json:"leader_username"`
		} `json:"access_groups"`
	}](t, rec)
	if len(d.ViaGroups) != 1 || len(d.AccessGroups) != 2 {
		t.Fatalf("detail db = %+v", d)
	}
	for _, g := range d.AccessGroups {
		if g.LeaderUsername == nil {
			t.Fatalf("leader group %d kosong", g.GroupID)
		}
	}
}
