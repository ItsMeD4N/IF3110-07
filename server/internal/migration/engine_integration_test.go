package migration_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ItsMeD4N/IF3110-07/server/internal/apptest"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/dblock"
	"github.com/ItsMeD4N/IF3110-07/server/internal/migration"
	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

type enqueueResp struct {
	JobID        string    `json:"job_id"`
	Deduplicated bool      `json:"deduplicated"`
	Job          queue.Job `json:"job"`
}

type jobPollResp struct {
	Job       queue.Job            `json:"job"`
	Execution *migration.Execution `json:"execution"`
}

func waitForJob(t *testing.T, f *fixture, jobID string, timeout time.Duration) jobPollResp {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		rec := f.e.Do(f.viewer, "GET", f.path("/jobs/%s", jobID), nil)
		apptest.Expect(t, rec, http.StatusOK)
		res := apptest.Decode[jobPollResp](t, rec)
		if res.Job.IsTerminal() {
			return res
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("timeout menunggu job %s selesai", jobID)
	return jobPollResp{}
}

func TestEngine_NormalLifecycle_AsyncRollbackForwardImmutabilityHideRollback(t *testing.T) {
	f := setup(t)

	createDef := func(in migration.CreateDefinitionInput) migration.Definition {
		rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), in)
		apptest.Expect(t, rec, http.StatusCreated)
		return apptest.Decode[migration.Definition](t, rec)
	}

	defA := createDef(migration.CreateDefinitionInput{
		Name:    "A_create_items",
		SQLUp:   fmt.Sprintf(`CREATE TABLE %s (id int PRIMARY KEY, label text NOT NULL); INSERT INTO %[1]s VALUES (1, 'v1');`, f.table("eng_items")),
		SQLDown: fmt.Sprintf(`DROP TABLE %s;`, f.table("eng_items")),
	})
	defB := createDef(migration.CreateDefinitionInput{
		Name:    "B_add_note",
		SQLUp:   fmt.Sprintf(`ALTER TABLE %s ADD COLUMN note text DEFAULT 'init';`, f.table("eng_items")),
		SQLDown: fmt.Sprintf(`ALTER TABLE %s DROP COLUMN note;`, f.table("eng_items")),
	})
	defC := createDef(migration.CreateDefinitionInput{
		Name:    "C_insert_row",
		SQLUp:   fmt.Sprintf(`INSERT INTO %s (id, label, note) VALUES (2, 'v2', 'added');`, f.table("eng_items")),
		SQLDown: fmt.Sprintf(`DELETE FROM %s WHERE id = 2;`, f.table("eng_items")),
	})
	defIrrev := createDef(migration.CreateDefinitionInput{
		Name:               "D_irreversible_cleanup",
		SQLUp:              fmt.Sprintf(`UPDATE %s SET label = upper(label);`, f.table("eng_items")),
		IsIrreversible:     true,
		IrreversibleReason: "casing asli tidak disimpan",
	})

	rec := f.e.Do(f.editor, "PUT", f.path("/migrations/definitions/%d", defB.ID), migration.UpdateDefinitionInput{
		Name:    "B_add_note_rev2",
		SQLUp:   defB.SQLUp,
		SQLDown: defB.SQLDown,
	})
	apptest.Expect(t, rec, http.StatusOK)
	defB = apptest.Decode[migration.Definition](t, rec)
	if defB.Revision != 2 {
		t.Fatalf("revision B = %d, want 2", defB.Revision)
	}

	execAsync := func(migID int64) jobPollResp {
		rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", migID), nil)
		apptest.Expect(t, rec, http.StatusAccepted)
		enq := apptest.Decode[enqueueResp](t, rec)
		if enq.JobID == "" {
			t.Fatal("API execute harus mengembalikan job_id")
		}
		res := waitForJob(t, f, enq.JobID, 5*time.Second)
		if res.Job.Status != queue.StatusCompleted {
			t.Fatalf("job %s status = %s err=%s", enq.JobID, res.Job.Status, res.Job.LastError)
		}
		return res
	}

	resA := execAsync(defA.ID)
	resB := execAsync(defB.ID)
	resC := execAsync(defC.ID)

	rec = f.e.Do(f.editor, "PUT", f.path("/migrations/definitions/%d", defB.ID), migration.UpdateDefinitionInput{
		Name:    "tampered",
		SQLUp:   "SELECT 999",
		SQLDown: "SELECT 999",
	})
	apptest.Expect(t, rec, http.StatusConflict)

	platformDB := testutil.OpenDB(t)
	if _, err := platformDB.Exec(`UPDATE platform_migrations SET sql_up = 'SELECT 999' WHERE id = $1`, defB.ID); err == nil {
		t.Fatal("trigger database harus menolak perubahan sql_up pada migration yang sudah dieksekusi")
	}

	apptest.Expect(t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/rollback", defB.ID), nil), http.StatusConflict)

	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/rollback", defC.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	rbC := waitForJob(t, f, apptest.Decode[enqueueResp](t, rec).JobID, 5*time.Second)
	if rbC.Job.Status != queue.StatusCompleted || rbC.Execution.RevertedExecutionID == nil || *rbC.Execution.RevertedExecutionID != resC.Execution.ID {
		t.Fatalf("rollback C tidak merujuk execution ID C (%d): %+v", resC.Execution.ID, rbC)
	}

	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/rollback", defB.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	rbB := waitForJob(t, f, apptest.Decode[enqueueResp](t, rec).JobID, 5*time.Second)
	if rbB.Job.Status != queue.StatusCompleted || rbB.Execution.RevertedExecutionID == nil || *rbB.Execution.RevertedExecutionID != resB.Execution.ID {
		t.Fatalf("rollback B = %+v", rbB)
	}

	apptest.Expect(t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/forward", defC.ID), nil), http.StatusConflict)

	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/forward", defB.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	fwdB := waitForJob(t, f, apptest.Decode[enqueueResp](t, rec).JobID, 5*time.Second)
	if fwdB.Job.Status != queue.StatusCompleted || fwdB.Execution.Action != migration.ActionForward {
		t.Fatalf("forward B = %+v", fwdB)
	}
	var countAfterFwdB int
	if err := f.target.QueryRow(`SELECT count(*) FROM ` + f.table("eng_items")).Scan(&countAfterFwdB); err != nil {
		t.Fatal(err)
	}
	if countAfterFwdB != 1 {
		t.Fatalf("forward B tidak boleh otomatis menjalankan C; row count = %d, want 1", countAfterFwdB)
	}

	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/forward", defC.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	fwdC := waitForJob(t, f, apptest.Decode[enqueueResp](t, rec).JobID, 5*time.Second)
	if fwdC.Job.Status != queue.StatusCompleted {
		t.Fatalf("forward C gagal: %+v", fwdC)
	}

	defFail := createDef(migration.CreateDefinitionInput{
		Name:    "F_bad_sql",
		SQLUp:   fmt.Sprintf(`INSERT INTO %s (id, label) VALUES (99, NULL);`, f.table("eng_items")),
		SQLDown: `SELECT 1;`,
	})
	rec = f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", defFail.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	failRes := waitForJob(t, f, apptest.Decode[enqueueResp](t, rec).JobID, 5*time.Second)
	if failRes.Job.Status != queue.StatusFailed || failRes.Execution == nil || failRes.Execution.Status != migration.ExecFailed || failRes.Execution.SQLState != "23502" {
		t.Fatalf("attempt gagal harus tercatat dengan SQLSTATE 23502: %+v", failRes)
	}

	recAll := f.e.Do(f.viewer, "GET", f.path("/migrations/executions?hide_rollback=false"), nil)
	apptest.Expect(t, recAll, http.StatusOK)
	allExecs := apptest.Decode[struct{ Executions []migration.Execution }](t, recAll).Executions
	if len(allExecs) != 8 {
		t.Fatalf("full executions count = %d, want 8", len(allExecs))
	}

	recHidden := f.e.Do(f.viewer, "GET", f.path("/migrations/executions?hide_rollback=true"), nil)
	apptest.Expect(t, recHidden, http.StatusOK)
	hiddenExecs := apptest.Decode[struct{ Executions []migration.Execution }](t, recHidden).Executions
	if len(hiddenExecs) != 4 {
		t.Fatalf("hide_rollback executions count = %d, want 4", len(hiddenExecs))
	}
	seenIDs := map[int64]bool{}
	for _, ex := range hiddenExecs {
		seenIDs[ex.ID] = true
	}
	if !seenIDs[resA.Execution.ID] || !seenIDs[fwdB.Execution.ID] || !seenIDs[fwdC.Execution.ID] || !seenIDs[failRes.Execution.ID] {
		t.Fatalf("hide_rollback menyembunyikan execution yang salah: %v", seenIDs)
	}
	if seenIDs[resB.Execution.ID] || seenIDs[rbB.Execution.ID] || seenIDs[resC.Execution.ID] || seenIDs[rbC.Execution.ID] {
		t.Fatalf("hide_rollback tidak menyembunyikan pasangan rollback: %v", seenIDs)
	}

	_ = execAsync(defIrrev.ID)
	apptest.Expect(t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/rollback", defIrrev.ID), nil), http.StatusUnprocessableEntity)
}

func ensureSecondTargetDB(t *testing.T) (string, *sql.DB, string) {
	t.Helper()
	const secondDBName = "if3110_test_target_parallel"
	u, err := url.Parse(testutil.DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	adminDB, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer adminDB.Close()

	var exists bool
	if err := adminDB.QueryRow(`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, secondDBName).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		if _, err := adminDB.Exec(`CREATE DATABASE ` + secondDBName); err != nil {
			var pqErr *pq.Error
			if !(errors.As(err, &pqErr) && pqErr.Code == "42P04") {
				t.Fatal(err)
			}
		}
	}
	u.Path = "/" + secondDBName
	db2, err := sql.Open("postgres", u.String())
	if err != nil {
		t.Fatal(err)
	}
	schema2 := testutil.Unique("p")
	if _, err := db2.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema2)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = db2.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema2) + ` CASCADE`)
		_ = db2.Close()
	})
	return u.String(), db2, schema2
}

func TestEngine_Concurrency_SameDBSerialized_DifferentDBsParallel(t *testing.T) {
	f := setup(t)
	url2, _, schema2 := ensureSecondTargetDB(t)
	c2, err := connmgr.ConfigFromURL(url2)
	if err != nil {
		t.Fatal(err)
	}
	dbID2 := f.e.Database(f.admin, map[string]any{
		"name": testutil.Unique("target2"), "host": c2.Host, "port": c2.Port,
		"database_name": c2.Database, "username": c2.Username, "password": c2.Password, "ssl_mode": "disable",
	})
	f.e.Grant(f.admin, f.editor, dbID2, "view_edit")
	f.e.Grant(f.admin, f.viewer, dbID2, "view_only")

	d1 := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name: "sleep1", SQLUp: fmt.Sprintf(`SELECT pg_sleep(0.35); CREATE TABLE %s (x int);`, f.table("ser1")), SQLDown: fmt.Sprintf(`DROP TABLE %s;`, f.table("ser1")),
	}))
	d2 := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name: "sleep2", SQLUp: fmt.Sprintf(`SELECT pg_sleep(0.35); CREATE TABLE %s (x int);`, f.table("ser2")), SQLDown: fmt.Sprintf(`DROP TABLE %s;`, f.table("ser2")),
	}))

	startSame := time.Now()
	j1 := apptest.Decode[enqueueResp](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", d1.ID), nil)).JobID
	j2 := apptest.Decode[enqueueResp](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", d2.ID), nil)).JobID
	r1 := waitForJob(t, f, j1, 5*time.Second)
	r2 := waitForJob(t, f, j2, 5*time.Second)
	sameElapsed := time.Since(startSame)
	if r1.Job.Status != queue.StatusCompleted || r2.Job.Status != queue.StatusCompleted {
		t.Fatalf("same-DB jobs gagal: r1=%+v r2=%+v", r1, r2)
	}
	if sameElapsed < 650*time.Millisecond {
		t.Fatalf("dua migration pada DB yang sama selesai terlalu cepat (%v), seharusnya serialized >= 650ms", sameElapsed)
	}

	dA := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name: "parA", SQLUp: fmt.Sprintf(`SELECT pg_sleep(0.45); CREATE TABLE %s (x int);`, f.table("par_a")), SQLDown: fmt.Sprintf(`DROP TABLE %s;`, f.table("par_a")),
	}))
	dB := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", fmt.Sprintf("/api/databases/%d/migrations/definitions", dbID2), migration.CreateDefinitionInput{
		Name: "parB", SQLUp: fmt.Sprintf(`SELECT pg_sleep(0.45); CREATE TABLE %s.par_b (x int);`, schema2), SQLDown: fmt.Sprintf(`DROP TABLE %s.par_b;`, schema2),
	}))

	startPar := time.Now()
	jA := apptest.Decode[enqueueResp](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", dA.ID), nil)).JobID
	jB := apptest.Decode[enqueueResp](t, f.e.Do(f.editor, "POST", fmt.Sprintf("/api/databases/%d/migrations/definitions/%d/execute", dbID2, dB.ID), nil)).JobID

	rA := waitForJob(t, f, jA, 5*time.Second)
	deadline := time.Now().Add(5 * time.Second)
	var rB jobPollResp
	for time.Now().Before(deadline) {
		rec := f.e.Do(f.viewer, "GET", fmt.Sprintf("/api/databases/%d/jobs/%s", dbID2, jB), nil)
		apptest.Expect(t, rec, http.StatusOK)
		rB = apptest.Decode[jobPollResp](t, rec)
		if rB.Job.IsTerminal() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	parElapsed := time.Since(startPar)
	if rA.Job.Status != queue.StatusCompleted || rB.Job.Status != queue.StatusCompleted {
		t.Fatalf("parallel jobs gagal: rA=%+v rB=%+v", rA, rB)
	}
	if parElapsed >= 850*time.Millisecond {
		t.Fatalf("migration lintas database tidak berjalan parallel: elapsed=%v (expected < 850ms)", parElapsed)
	}
	t.Logf("Concurrency verified: same-DB serialized=%v, cross-DB parallel=%v", sameElapsed, parElapsed)
}

func TestEngine_DuplicateDelivery_AndUncertainCommitRecovery(t *testing.T) {
	f := setup(t)
	tbl := f.table("dup_guard")
	if _, err := f.target.Exec(fmt.Sprintf(`CREATE TABLE %s (id serial PRIMARY KEY, val text NOT NULL)`, tbl)); err != nil {
		t.Fatal(err)
	}

	def := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name:    "insert_once",
		SQLUp:   fmt.Sprintf(`INSERT INTO %s (val) VALUES ('once');`, tbl),
		SQLDown: fmt.Sprintf(`DELETE FROM %s WHERE val = 'once';`, tbl),
	}))

	idemKey := testutil.Unique("dupkey")
	rec1 := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID), map[string]string{"idempotency_key": idemKey})
	apptest.Expect(t, rec1, http.StatusAccepted)
	enq1 := apptest.Decode[enqueueResp](t, rec1)

	res1 := waitForJob(t, f, enq1.JobID, 5*time.Second)
	if res1.Job.Status != queue.StatusCompleted {
		t.Fatalf("job 1 gagal: %+v", res1)
	}

	jobCopy := res1.Job
	jobCopy.Attempt = 2
	outcome := f.e.App.Engine.HandleJob(context.Background(), &jobCopy)
	if outcome.Status != queue.StatusCompleted || outcome.Err != nil {
		t.Fatalf("duplicate delivery outcome = %+v", outcome)
	}
	var count int
	if err := f.target.QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("pesan duplikat menyebabkan double commit: count = %d, want 1", count)
	}

	def2 := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name:    "uncertain_then_retry",
		SQLUp:   fmt.Sprintf(`INSERT INTO %s (val) VALUES ('after_uncertain');`, tbl),
		SQLDown: fmt.Sprintf(`DELETE FROM %s WHERE val = 'after_uncertain';`, tbl),
	}))

	var injected atomic.Bool
	f.e.App.Engine.BeforeNormalCommitHook = func(ctx context.Context, execID int64, action migration.Action) error {
		if injected.CompareAndSwap(false, true) {
			return io.EOF
		}
		return nil
	}
	t.Cleanup(func() { f.e.App.Engine.BeforeNormalCommitHook = nil })

	rec2 := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def2.ID), nil)
	apptest.Expect(t, rec2, http.StatusAccepted)
	res2 := waitForJob(t, f, apptest.Decode[enqueueResp](t, rec2).JobID, 5*time.Second)
	if res2.Job.Status != queue.StatusCompleted || res2.Job.Attempt < 2 {
		t.Fatalf("expected uncertain commit evidence check -> clean retry -> completed on attempt >=2, got %+v", res2)
	}
	if err := f.target.QueryRow(`SELECT count(*) FROM ` + tbl + ` WHERE val = 'after_uncertain'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("after retry row count = %d, want 1", count)
	}

	platformDB := testutil.OpenDB(t)
	var stuckExecID int64
	if err := platformDB.QueryRow(`
		INSERT INTO platform_migration_executions (
			database_id, migration_id, migration_version, migration_revision, migration_name,
			action, execution_mode, status, sql_executed, actor, attempt
		) VALUES ($1, $2, $3, 1, 'stuck_uncertain', 'up', 'normal', 'uncertain', 'SELECT 1', 'tester', 1)
		RETURNING id`, f.dbID, def2.ID, def2.Version).Scan(&stuckExecID); err != nil {
		t.Fatal(err)
	}
	lease, err := f.e.App.Locker.Acquire(context.Background(), dblock.AcquireRequest{DatabaseID: f.dbID, OwnerID: "sim-crash", TTL: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.e.App.Locker.TransitionToBlocked(context.Background(), lease, dblock.StateBlockedUncertain, stuckExecID, "simulasi hasil commit belum pasti"); err != nil {
		t.Fatal(err)
	}

	def3 := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name: "blocked_by_uncertain", SQLUp: "SELECT 1;", SQLDown: "SELECT 1;",
	}))
	apptest.Expect(t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def3.ID), nil), http.StatusConflict)

	recRecon := f.e.Do(f.editor, "POST", f.path("/migrations/executions/%d/reconcile", stuckExecID), nil)
	apptest.Expect(t, recRecon, http.StatusOK)

	recLock := f.e.Do(f.viewer, "GET", f.path("/lock"), nil)
	apptest.Expect(t, recLock, http.StatusOK)
	lockInfo := apptest.Decode[dblock.LockInfo](t, recLock)
	if lockInfo.Locked {
		t.Fatalf("lock seharusnya sudah bersih setelah rekonsiliasi: %+v", lockInfo)
	}
}

func TestEngine_RecoverExpiredJob_ChecksCommitEvidence(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	tbl := f.table("crash_rec")
	if _, err := f.target.Exec(fmt.Sprintf(`CREATE TABLE %s (id serial PRIMARY KEY, note text)`, tbl)); err != nil {
		t.Fatal(err)
	}

	_ = f.e.App.WorkerPool.Stop(ctx)

	def := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name:    "crash_after_commit",
		SQLUp:   fmt.Sprintf(`INSERT INTO %s (note) VALUES ('committed_before_crash');`, tbl),
		SQLDown: fmt.Sprintf(`DELETE FROM %s WHERE note = 'committed_before_crash';`, tbl),
	}))

	idemKey := testutil.Unique("crash-committed")
	job, _, err := f.e.App.Engine.EnqueueExecute(ctx, f.dbID, def.ID, f.editor.Username, migration.EnqueueOptions{IdempotencyKey: idemKey})
	if err != nil {
		t.Fatal(err)
	}
	platformDB := testutil.OpenDB(t)
	var execID int64
	if err := platformDB.QueryRow(`
		INSERT INTO platform_migration_executions (
			database_id, migration_id, migration_version, migration_revision, migration_name,
			action, execution_mode, status, sql_executed, actor, job_id, attempt
		) VALUES ($1, $2, $3, 1, 'crash_after_commit', 'up', 'normal', 'running', $4, 'tester', $5::uuid, 1)
		RETURNING id`, f.dbID, def.ID, def.Version, def.SQLUp, job.ID).Scan(&execID); err != nil {
		t.Fatal(err)
	}
	_, err = platformDB.Exec(`
		UPDATE platform_migration_jobs
		SET status = 'running', attempt = 1, execution_id = $2, locked_by = 'dead-worker',
		    lease_expires_at = now() - interval '1 minute'
		WHERE id = $1::uuid`, job.ID, execID)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.target.Exec(`CREATE SCHEMA IF NOT EXISTS _if3110_meta`)
	_, _ = f.target.Exec(`CREATE TABLE IF NOT EXISTS _if3110_meta.migration_receipts (idempotency_key TEXT PRIMARY KEY, execution_id BIGINT NOT NULL, migration_id BIGINT NOT NULL, action TEXT NOT NULL, committed_at TIMESTAMPTZ NOT NULL DEFAULT now())`)
	_, err = f.target.Exec(fmt.Sprintf(`INSERT INTO %s (note) VALUES ('committed_before_crash')`, tbl))
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.target.Exec(`INSERT INTO _if3110_meta.migration_receipts (idempotency_key, execution_id, migration_id, action) VALUES ($1, $2, $3, 'up')`, idemKey, execID, def.ID)
	if err != nil {
		t.Fatal(err)
	}

	f.e.App.WorkerPool.ReclaimOnce(ctx)

	gotJob, err := f.e.App.Broker.GetJobByID(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotJob.Status != queue.StatusCompleted {
		t.Fatalf("expired job with target receipt should reconcile to completed, got %+v", gotJob)
	}
	gotExec, err := f.e.App.Engine.GetExecution(ctx, f.dbID, execID)
	if err != nil {
		t.Fatal(err)
	}
	if gotExec.Status != migration.ExecSuccess {
		t.Fatalf("execution status = %s, want success", gotExec.Status)
	}
	var count int
	_ = f.target.QueryRow(`SELECT count(*) FROM ` + tbl).Scan(&count)
	if count != 1 {
		t.Fatalf("recovery tidak boleh mengeksekusi SQL dua kali; count = %d, want 1", count)
	}

	def2 := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name:    "crash_before_commit",
		SQLUp:   fmt.Sprintf(`INSERT INTO %s (note) VALUES ('retry_after_crash');`, tbl),
		SQLDown: fmt.Sprintf(`DELETE FROM %s WHERE note = 'retry_after_crash';`, tbl),
	}))
	job2, _, err := f.e.App.Engine.EnqueueExecute(ctx, f.dbID, def2.ID, f.editor.Username, migration.EnqueueOptions{IdempotencyKey: testutil.Unique("crash-uncommitted")})
	if err != nil {
		t.Fatal(err)
	}
	var execID2 int64
	if err := platformDB.QueryRow(`
		INSERT INTO platform_migration_executions (
			database_id, migration_id, migration_version, migration_revision, migration_name,
			action, execution_mode, status, sql_executed, actor, job_id, attempt
		) VALUES ($1, $2, $3, 1, 'crash_before_commit', 'up', 'normal', 'running', $4, 'tester', $5::uuid, 1)
		RETURNING id`, f.dbID, def2.ID, def2.Version, def2.SQLUp, job2.ID).Scan(&execID2); err != nil {
		t.Fatal(err)
	}
	_, _ = platformDB.Exec(`
		UPDATE platform_migration_jobs
		SET status = 'running', attempt = 1, execution_id = $2, locked_by = 'dead-worker-2',
		    lease_expires_at = now() - interval '1 minute'
		WHERE id = $1::uuid`, job2.ID, execID2)

	f.e.App.WorkerPool.ReclaimOnce(ctx)

	gotJob2, err := f.e.App.Broker.GetJobByID(ctx, job2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotJob2.Status != queue.StatusRetrying {
		t.Fatalf("expired job without target receipt should be scheduled for retry, got %+v", gotJob2)
	}
	gotExec2, err := f.e.App.Engine.GetExecution(ctx, f.dbID, execID2)
	if err != nil {
		t.Fatal(err)
	}
	if gotExec2.Status != migration.ExecFailed {
		t.Fatalf("crashed uncommitted execution status = %s, want failed", gotExec2.Status)
	}
}

func TestEngine_BatchMigration_PartialResumeAndReversePartialRevert(t *testing.T) {
	f := setup(t)
	tbl := f.table("batch_orders")
	if _, err := f.target.Exec(fmt.Sprintf(`
		CREATE TABLE %s (
			id BIGSERIAL PRIMARY KEY,
			status TEXT NOT NULL,
			amount INTEGER NOT NULL
		);
		INSERT INTO %[1]s (status, amount)
		SELECT CASE WHEN g %% 2 = 0 THEN 'pending' ELSE 'open' END, g * 10
		FROM generate_series(1, 50) g;`, tbl)); err != nil {
		t.Fatal(err)
	}

	def := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name:          "batch_archive_orders",
		ExecutionMode: migration.ModeBatch,
		BatchConfig: &migration.BatchConfig{
			Schema:         f.schema,
			Table:          "batch_orders",
			CursorColumn:   "id",
			BatchSize:      10,
			CaptureColumns: []string{"status", "amount"},
		},
		SQLUp: fmt.Sprintf(`UPDATE %s SET status = 'archived', amount = amount + 100 WHERE id = ANY({{batch_ids}});`, tbl),
		SQLDown: fmt.Sprintf(`
			UPDATE %s t
			SET status = r.status, amount = r.amount
			FROM jsonb_to_recordset({{recovery_json}}) AS r(id bigint, status text, amount integer)
			WHERE t.id = r.id;`, tbl),
	}))

	var executedBatches []int
	f.e.App.Engine.BatchBeforeExecHook = func(ctx context.Context, execID int64, batchNum int, action migration.Action) error {
		if action == migration.ActionBatchUp {
			executedBatches = append(executedBatches, batchNum)
			if batchNum == 3 {
				return errors.New("simulated constraint failure on batch 3")
			}
		}
		return nil
	}
	t.Cleanup(func() { f.e.App.Engine.BatchBeforeExecHook = nil })

	rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	partialRes := waitForJob(t, f, apptest.Decode[enqueueResp](t, rec).JobID, 5*time.Second)

	if partialRes.Job.Status != queue.StatusPartial || partialRes.Execution == nil || partialRes.Execution.Status != migration.ExecPartial {
		t.Fatalf("expected partial status, got %+v", partialRes)
	}
	if partialRes.Execution.CompletedBatches != 2 || partialRes.Execution.RowsAffected != 20 || partialRes.Execution.LastCursor != "20" {
		t.Fatalf("batch progress tidak sesuai: %+v", partialRes.Execution)
	}

	var archivedCount int
	_ = f.target.QueryRow(`SELECT count(*) FROM ` + tbl + ` WHERE status = 'archived'`).Scan(&archivedCount)
	if archivedCount != 20 {
		t.Fatalf("20 row dari batch 1-2 harus tetap committed, got %d", archivedCount)
	}

	otherDef := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name: "blocked_during_partial", SQLUp: "SELECT 1;", SQLDown: "SELECT 1;",
	}))
	apptest.Expect(t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", otherDef.ID), nil), http.StatusConflict)

	executedBatches = nil
	f.e.App.Engine.BatchBeforeExecHook = func(ctx context.Context, execID int64, batchNum int, action migration.Action) error {
		if action == migration.ActionBatchUp {
			executedBatches = append(executedBatches, batchNum)
		}
		return nil
	}

	recResume := f.e.Do(f.editor, "POST", f.path("/migrations/executions/%d/resume", partialRes.Execution.ID), nil)
	apptest.Expect(t, recResume, http.StatusAccepted)
	resumeRes := waitForJob(t, f, apptest.Decode[enqueueResp](t, recResume).JobID, 5*time.Second)
	if resumeRes.Job.Status != queue.StatusCompleted || resumeRes.Execution.Status != migration.ExecSuccess {
		t.Fatalf("resume batch gagal: %+v", resumeRes)
	}
	if len(executedBatches) != 3 || executedBatches[0] != 3 || executedBatches[1] != 4 || executedBatches[2] != 5 {
		t.Fatalf("resume seharusnya hanya menjalankan batch [3 4 5], didapat %v", executedBatches)
	}
	_ = f.target.QueryRow(`SELECT count(*) FROM ` + tbl + ` WHERE status = 'archived'`).Scan(&archivedCount)
	if archivedCount != 50 {
		t.Fatalf("semua 50 row harus sudah archived, got %d", archivedCount)
	}

	var revertOrder []int
	f.e.App.Engine.BatchBeforeExecHook = func(ctx context.Context, execID int64, batchNum int, action migration.Action) error {
		if action == migration.ActionBatchRevert {
			revertOrder = append(revertOrder, batchNum)
			if batchNum == 2 {
				return errors.New("simulated revert failure on batch 2")
			}
		}
		return nil
	}

	recRev1 := f.e.Do(f.editor, "POST", f.path("/migrations/executions/%d/revert", resumeRes.Execution.ID), nil)
	apptest.Expect(t, recRev1, http.StatusAccepted)
	rev1Res := waitForJob(t, f, apptest.Decode[enqueueResp](t, recRev1).JobID, 5*time.Second)
	if rev1Res.Job.Status != queue.StatusPartiallyReverted || rev1Res.Execution.Status != migration.ExecPartiallyReverted {
		t.Fatalf("expected partially_reverted, got %+v", rev1Res)
	}
	if len(revertOrder) != 4 || revertOrder[0] != 5 || revertOrder[1] != 4 || revertOrder[2] != 3 || revertOrder[3] != 2 {
		t.Fatalf("batch revert harus berjalan urutan terbalik [5 4 3 2], didapat %v", revertOrder)
	}

	_ = f.target.QueryRow(`SELECT count(*) FROM ` + tbl + ` WHERE status = 'archived'`).Scan(&archivedCount)
	if archivedCount != 20 {
		t.Fatalf("setelah partially_reverted harus tersisa 20 row archived, got %d", archivedCount)
	}

	revertOrder = nil
	f.e.App.Engine.BatchBeforeExecHook = func(ctx context.Context, execID int64, batchNum int, action migration.Action) error {
		if action == migration.ActionBatchRevert {
			revertOrder = append(revertOrder, batchNum)
		}
		return nil
	}

	recRev2 := f.e.Do(f.editor, "POST", f.path("/migrations/executions/%d/revert", rev1Res.Execution.ID), nil)
	apptest.Expect(t, recRev2, http.StatusAccepted)
	rev2Res := waitForJob(t, f, apptest.Decode[enqueueResp](t, recRev2).JobID, 5*time.Second)
	if rev2Res.Job.Status != queue.StatusCompleted || rev2Res.Execution.Status != migration.ExecSuccess {
		t.Fatalf("lanjutan batch revert gagal: %+v", rev2Res)
	}
	if len(revertOrder) != 2 || revertOrder[0] != 2 || revertOrder[1] != 1 {
		t.Fatalf("lanjutan revert seharusnya hanya batch [2 1], didapat %v", revertOrder)
	}

	var mismatched int
	if err := f.target.QueryRow(fmt.Sprintf(`
		SELECT count(*) FROM %s
		WHERE amount <> id * 10
		   OR status <> (CASE WHEN id %% 2 = 0 THEN 'pending' ELSE 'open' END)`, tbl)).Scan(&mismatched); err != nil {
		t.Fatal(err)
	}
	if mismatched != 0 {
		t.Fatalf("%d row tidak kembali ke nilai asli setelah full batch revert", mismatched)
	}

	irrevBatch := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name:               "irrev_batch",
		ExecutionMode:      migration.ModeBatch,
		IsIrreversible:     true,
		IrreversibleReason: "penghitungan agregat tidak dapat dibalik",
		BatchConfig:        &migration.BatchConfig{Schema: f.schema, Table: "batch_orders", CursorColumn: "id", BatchSize: 25},
		SQLUp:              fmt.Sprintf(`UPDATE %s SET amount = amount + 1 WHERE id = ANY({{batch_ids}});`, tbl),
	}))
	recIB := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", irrevBatch.ID), nil)
	apptest.Expect(t, recIB, http.StatusAccepted)
	ibRes := waitForJob(t, f, apptest.Decode[enqueueResp](t, recIB).JobID, 5*time.Second)
	if ibRes.Job.Status != queue.StatusCompleted {
		t.Fatalf("irreversible batch exec gagal: %+v", ibRes)
	}
	apptest.Expect(t, f.e.Do(f.editor, "POST", f.path("/migrations/executions/%d/revert", ibRes.Execution.ID), nil), http.StatusUnprocessableEntity)
}

func TestEngine_BatchMigration_LargeDatasetOverOneSecond(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping >1s large dataset batch benchmark in -short mode")
	}
	f := setup(t)
	tbl := f.table("large_events")

	if _, err := f.target.Exec(fmt.Sprintf(`
		CREATE TABLE %s (
			id BIGSERIAL PRIMARY KEY,
			tag TEXT NOT NULL,
			payload TEXT NOT NULL,
			processed BOOLEAN NOT NULL DEFAULT FALSE
		);
		CREATE INDEX %s_tag_idx ON %s (tag);
		CREATE INDEX %s_processed_idx ON %s (processed);`,
		tbl, testutil.Unique("idx"), tbl, testutil.Unique("idx"), tbl)); err != nil {
		t.Fatal(err)
	}

	rowCount := 150000
	seedRows := func(n int) {
		if _, err := f.target.Exec(fmt.Sprintf(`
			INSERT INTO %s (tag, payload, processed)
			SELECT 'tag_' || (g %% 1000), repeat('x', 80) || g::text, FALSE
			FROM generate_series(1, $1) g`, tbl), n); err != nil {
			t.Fatal(err)
		}
	}
	seedRows(rowCount)

	var unbatchedDur time.Duration
	for attempt := 0; attempt < 3; attempt++ {
		start := time.Now()
		if _, err := f.target.Exec(fmt.Sprintf(`UPDATE %s SET processed = TRUE, tag = 'unbatched_' || (id %% 1000)`, tbl)); err != nil {
			t.Fatal(err)
		}
		unbatchedDur = time.Since(start)
		if unbatchedDur >= time.Second {
			break
		}
		seedRows(150000)
		rowCount += 150000
	}
	if unbatchedDur < time.Second {
		t.Fatalf("unbatched UPDATE hanya %v pada %d rows; spesifikasi meminta dataset > 1 detik", unbatchedDur, rowCount)
	}
	t.Logf("Benchmark unbatched UPDATE: rows=%d duration=%v (>1s requirement met)", rowCount, unbatchedDur)

	batchSize := 30000
	def := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name:          "large_batch_update",
		ExecutionMode: migration.ModeBatch,
		BatchConfig: &migration.BatchConfig{
			Schema:       f.schema,
			Table:        "large_events",
			CursorColumn: "id",
			BatchSize:    batchSize,
		},
		SQLUp:   fmt.Sprintf(`UPDATE %s SET processed = FALSE, tag = 'batched_' || (id %% 1000) WHERE id >= {{cursor_start}} AND id <= {{cursor_end}};`, tbl),
		SQLDown: fmt.Sprintf(`UPDATE %s SET processed = TRUE, tag = 'reverted_' || (id %% 1000) WHERE id >= {{cursor_start}} AND id <= {{cursor_end}};`, tbl),
	}))

	startBatch := time.Now()
	rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	res := waitForJob(t, f, apptest.Decode[enqueueResp](t, rec).JobID, 60*time.Second)
	batchDur := time.Since(startBatch)

	if res.Job.Status != queue.StatusCompleted || res.Execution == nil || res.Execution.Status != migration.ExecSuccess {
		t.Fatalf("batch migration besar gagal: %+v", res)
	}
	if res.Execution.RowsAffected != int64(rowCount) {
		t.Fatalf("rows_affected = %d, want %d", res.Execution.RowsAffected, rowCount)
	}
	expectedBatches := (rowCount + batchSize - 1) / batchSize
	if res.Execution.CompletedBatches != expectedBatches {
		t.Fatalf("completed_batches = %d, want %d", res.Execution.CompletedBatches, expectedBatches)
	}

	var remainingTrue int
	if err := f.target.QueryRow(`SELECT count(*) FROM ` + tbl + ` WHERE processed = TRUE`).Scan(&remainingTrue); err != nil {
		t.Fatal(err)
	}
	if remainingTrue != 0 {
		t.Fatalf("%d row belum ter-update oleh batch migration", remainingTrue)
	}
	t.Logf("Benchmark batch migration: rows=%d batch_size=%d batches=%d total_duration=%v",
		rowCount, batchSize, res.Execution.CompletedBatches, batchDur)
}

func TestEngine_EndToEndTracingAndStructuredJSONLogs(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	var logBuf bytes.Buffer
	prevLogger := slog.Default()
	telemetry.InitLogger(&logBuf, slog.LevelInfo)
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	f := setup(t)
	def := apptest.Decode[migration.Definition](t, f.e.Do(f.editor, "POST", f.path("/migrations/definitions"), migration.CreateDefinitionInput{
		Name:    "traced_mig",
		SQLUp:   fmt.Sprintf(`CREATE TABLE %s (id int PRIMARY KEY);`, f.table("traced_t")),
		SQLDown: fmt.Sprintf(`DROP TABLE %s;`, f.table("traced_t")),
	}))

	rec := f.e.Do(f.editor, "POST", f.path("/migrations/definitions/%d/execute", def.ID), nil)
	apptest.Expect(t, rec, http.StatusAccepted)
	apiTraceID := rec.Header().Get("X-Trace-ID")
	if apiTraceID == "" {
		t.Fatal("X-Trace-ID tidak diset oleh HTTP middleware")
	}

	enq := apptest.Decode[enqueueResp](t, rec)
	res := waitForJob(t, f, enq.JobID, 5*time.Second)
	if res.Job.Status != queue.StatusCompleted {
		t.Fatalf("traced job gagal: %+v", res)
	}

	expectedSpans := map[string]bool{
		"queue.publish":            false,
		"worker.process_job":       false,
		"lock.acquire":             false,
		"migration.revalidate":     false,
		"migration.execute_normal": false,
		"db.exec":                  false,
		"db.commit":                false,
		"lock.release":             false,
	}
	for _, sp := range sr.Ended() {
		if sp.SpanContext().TraceID().String() == apiTraceID {
			if _, wanted := expectedSpans[sp.Name()]; wanted {
				expectedSpans[sp.Name()] = true
			}
		}
	}
	for name, seen := range expectedSpans {
		if !seen {
			t.Errorf("span %q dengan trace_id=%s tidak ditemukan pada rantai API->Queue->Worker->Lock->DB", name, apiTraceID)
		}
	}

	expectedEvents := map[string]bool{
		"http.request":         false,
		"queue.enqueued":       false,
		"worker.job_claimed":   false,
		"lock.acquired":        false,
		"sql.start":            false,
		"sql.result":           false,
		"sql.commit":           false,
		"lock.released":        false,
		"worker.job_completed": false,
	}
	for _, line := range strings.Split(strings.TrimSpace(logBuf.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line bukan JSON valid: %v\nline=%s", err, line)
		}
		if entry["trace_id"] == apiTraceID {
			if ev, ok := entry["event"].(string); ok {
				if _, wanted := expectedEvents[ev]; wanted {
					expectedEvents[ev] = true
				}
			}
		}
	}
	for ev, seen := range expectedEvents {
		if !seen {
			t.Errorf("structured JSON event %q dengan trace_id=%s tidak ditemukan", ev, apiTraceID)
		}
	}
}
