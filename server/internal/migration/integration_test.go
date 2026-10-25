package migration_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/apptest"
	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/migration"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

type fixture struct {
	e                     *apptest.Env
	admin, editor, viewer *auth.User
	outsider              *auth.User
	dbID                  int64
	target                *sql.DB
	schema                string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	e := apptest.New(t)
	f := &fixture{e: e, admin: e.NewUser(true), editor: e.NewUser(false), viewer: e.NewUser(false), outsider: e.NewUser(false)}
	f.target, f.schema = testutil.OpenTargetSchema(t)
	f.dbID = e.Database(f.admin, apptest.TargetConn(t))
	e.Grant(f.admin, f.editor, f.dbID, "view_edit")
	e.Grant(f.admin, f.viewer, f.dbID, "view_only")
	return f
}

func (f *fixture) path(format string, a ...any) string {
	return fmt.Sprintf("/api/databases/%d", f.dbID) + fmt.Sprintf(format, a...)
}

func (f *fixture) table(name string) string { return f.schema + "." + name }

func (f *fixture) tableExists(t *testing.T, name string) bool {
	t.Helper()
	var ok bool
	if err := f.target.QueryRow(`SELECT to_regclass($1) IS NOT NULL`, f.table(name)).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

type runResp struct {
	Migration migration.Record `json:"migration"`
	Code      string           `json:"code"`
}

func TestRun_CommitRollbackAndLinearHistory(t *testing.T) {
	f := setup(t)

	rec := f.e.Do(f.editor, "POST", f.path("/migrations"), map[string]string{
		"name": "buat items",
		"sql":  fmt.Sprintf("CREATE TABLE %s (id serial PRIMARY KEY, name text NOT NULL); INSERT INTO %[1]s (name) VALUES ('a');", f.table("items")),
	})
	apptest.Expect(t, rec, http.StatusOK)
	v1 := apptest.Decode[runResp](t, rec).Migration
	if v1.Version != 1 || v1.Status != "success" || v1.Executor != f.editor.Username || v1.Name != "buat items" {
		t.Fatalf("record v1 = %+v", v1)
	}
	if !f.tableExists(t, "items") {
		t.Fatal("tabel items tidak ada setelah migration sukses")
	}

	rec = f.e.Do(f.editor, "POST", f.path("/migrations"), map[string]string{
		"sql": fmt.Sprintf("CREATE TABLE %s (x int); INSERT INTO %s (name) VALUES (NULL);", f.table("partial"), f.table("items")),
	})
	apptest.Expect(t, rec, http.StatusUnprocessableEntity)
	v2 := apptest.Decode[runResp](t, rec)
	if v2.Code != "migration_failed" || v2.Migration.Version != 2 || v2.Migration.Status != "failed" || v2.Migration.Error == "" {
		t.Fatalf("record v2 = %+v", v2)
	}
	if f.tableExists(t, "partial") {
		t.Fatal("statement pertama dari migration gagal tidak di-rollback")
	}

	rec = f.e.Do(f.editor, "POST", f.path("/migrations"), url.Values{"sql": {fmt.Sprintf("ALTER TABLE %s ADD COLUMN note text", f.table("items"))}})
	apptest.Expect(t, rec, http.StatusOK)
	if v := apptest.Decode[runResp](t, rec).Migration.Version; v != 3 {
		t.Fatalf("versi berikutnya = %d, want 3", v)
	}

	rec = f.e.Do(f.viewer, "GET", f.path("/migrations"), nil)
	apptest.Expect(t, rec, http.StatusOK)
	hist := apptest.Decode[struct{ Migrations []migration.Record }](t, rec).Migrations
	if len(hist) != 3 || hist[0].Version != 3 || hist[1].Status != "failed" || hist[2].Status != "success" {
		t.Fatalf("history = %+v", hist)
	}

	rec = f.e.Do(f.viewer, "GET", f.path("/migrations/2"), nil)
	apptest.Expect(t, rec, http.StatusOK)
	if d := apptest.Decode[migration.Record](t, rec); !strings.Contains(d.SQL, "partial") || d.Error == "" {
		t.Fatalf("detail v2 = %+v", d)
	}
	apptest.Expect(t, f.e.Do(f.viewer, "GET", f.path("/migrations/99"), nil), http.StatusNotFound)
}

func TestRun_RejectsTransactionControl(t *testing.T) {
	f := setup(t)
	rec := f.e.Do(f.editor, "POST", f.path("/migrations"), map[string]string{
		"sql": fmt.Sprintf("CREATE TABLE %s (x int); COMMIT; SELECT * FROM tidak_ada;", f.table("sneaky")),
	})
	apptest.Expect(t, rec, http.StatusUnprocessableEntity)
	if f.tableExists(t, "sneaky") {
		t.Fatal("SQL dengan COMMIT tetap dieksekusi")
	}
	rec = f.e.Do(f.viewer, "GET", f.path("/migrations"), nil)
	if n := len(apptest.Decode[struct{ Migrations []migration.Record }](t, rec).Migrations); n != 0 {
		t.Fatalf("SQL yang ditolak validasi tidak boleh masuk history, ada %d", n)
	}
	apptest.Expect(t, f.e.Do(f.editor, "POST", f.path("/migrations"), map[string]string{"sql": "  "}), http.StatusUnprocessableEntity)
}

func TestRun_AccessControl(t *testing.T) {
	f := setup(t)
	sqlBody := map[string]string{"sql": fmt.Sprintf("CREATE TABLE %s (x int)", f.table("t1"))}

	apptest.Expect(t, f.e.Do(f.viewer, "POST", f.path("/migrations"), sqlBody), http.StatusForbidden)
	apptest.Expect(t, f.e.Do(f.outsider, "POST", f.path("/migrations"), sqlBody), http.StatusNotFound)
	apptest.Expect(t, f.e.Do(f.outsider, "GET", f.path("/migrations"), nil), http.StatusNotFound)
	apptest.Expect(t, f.e.Do(f.admin, "POST", f.path("/migrations"), sqlBody), http.StatusNotFound)
	apptest.Expect(t, f.e.Do(nil, "POST", f.path("/migrations"), sqlBody), http.StatusUnauthorized)
	apptest.Expect(t, f.e.DoNoCSRF(f.editor, "POST", f.path("/migrations"), sqlBody), http.StatusForbidden)
	if f.tableExists(t, "t1") {
		t.Fatal("request yang ditolak tetap mengeksekusi SQL")
	}
}

func TestRun_TargetUnreachable(t *testing.T) {
	e := apptest.New(t)
	admin, editor := e.NewUser(true), e.NewUser(false)
	dead := e.Database(admin, apptest.DeadConn())
	e.Grant(admin, editor, dead, "view_edit")
	rec := e.Do(editor, "POST", fmt.Sprintf("/api/databases/%d/migrations", dead), map[string]string{"sql": "SELECT 1"})
	apptest.Expect(t, rec, http.StatusServiceUnavailable)
	if strings.Contains(rec.Body.String(), `"password"`) || strings.Contains(rec.Body.String(), "nobody:x") {
		t.Fatalf("respons membocorkan kredensial: %s", rec.Body.String())
	}
}

func TestDrafts_CreateEditRunDelete(t *testing.T) {
	f := setup(t)
	drafts := f.path("/migration-drafts")

	rec := f.e.Do(f.editor, "POST", drafts, map[string]string{"name": "draft a", "sql": "SELECT 1"})
	apptest.Expect(t, rec, http.StatusCreated)
	d := apptest.Decode[migration.Draft](t, rec)
	draft := fmt.Sprintf("%s/%d", drafts, d.ID)

	apptest.Expect(t, f.e.Do(f.viewer, "GET", draft, nil), http.StatusOK)
	apptest.Expect(t, f.e.Do(f.viewer, "POST", drafts, map[string]string{"sql": "SELECT 1"}), http.StatusForbidden)
	apptest.Expect(t, f.e.Do(f.viewer, "PUT", draft, map[string]string{"sql": "SELECT 2"}), http.StatusForbidden)
	apptest.Expect(t, f.e.Do(f.viewer, "DELETE", draft, nil), http.StatusForbidden)
	apptest.Expect(t, f.e.Do(f.viewer, "POST", draft+"/execute", nil), http.StatusForbidden)
	apptest.Expect(t, f.e.Do(f.outsider, "GET", draft, nil), http.StatusNotFound)

	apptest.Expect(t, f.e.Do(f.editor, "PUT", draft, map[string]string{"sql": "BEGIN; SELECT 1"}), http.StatusUnprocessableEntity)
	rec = f.e.Do(f.editor, "PUT", draft, map[string]string{"name": "buat audit", "sql": fmt.Sprintf("CREATE TABLE %s (x int)", f.table("audit"))})
	apptest.Expect(t, rec, http.StatusOK)
	if got := apptest.Decode[migration.Draft](t, rec); got.Name != "buat audit" || got.UpdatedBy != f.editor.Username {
		t.Fatalf("draft setelah update = %+v", got)
	}
	if f.tableExists(t, "audit") {
		t.Fatal("menyimpan draft tidak boleh mengeksekusi SQL")
	}

	rec = f.e.Do(f.editor, "POST", draft+"/execute", nil)
	apptest.Expect(t, rec, http.StatusOK)
	if m := apptest.Decode[runResp](t, rec).Migration; m.Name != "buat audit" || m.Status != "success" {
		t.Fatalf("hasil eksekusi draft = %+v", m)
	}
	if !f.tableExists(t, "audit") {
		t.Fatal("tabel dari draft tidak dibuat")
	}
	apptest.Expect(t, f.e.Do(f.editor, "GET", draft, nil), http.StatusNotFound)
	apptest.Expect(t, f.e.Do(f.editor, "POST", draft+"/execute", nil), http.StatusNotFound)

	rec = f.e.Do(f.editor, "POST", drafts, map[string]string{"sql": "SELECT * FROM tidak_ada"})
	failing := fmt.Sprintf("%s/%d", drafts, apptest.Decode[migration.Draft](t, rec).ID)
	apptest.Expect(t, f.e.Do(f.editor, "POST", failing+"/execute", nil), http.StatusUnprocessableEntity)
	apptest.Expect(t, f.e.Do(f.editor, "GET", failing, nil), http.StatusOK)
	apptest.Expect(t, f.e.Do(f.editor, "DELETE", failing, nil), http.StatusNoContent)
	apptest.Expect(t, f.e.Do(f.editor, "DELETE", failing, nil), http.StatusNotFound)
}

func TestDrafts_ConcurrentRunExecutesOnce(t *testing.T) {
	f := setup(t)
	rec := f.e.Do(f.editor, "POST", f.path("/migration-drafts"), map[string]string{
		"sql": fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (x int); INSERT INTO %[1]s VALUES (1);", f.table("once")),
	})
	apptest.Expect(t, rec, http.StatusCreated)
	run := f.path("/migration-drafts/%d/execute", apptest.Decode[migration.Draft](t, rec).ID)

	codes := make([]int, 4)
	var wg sync.WaitGroup
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = f.e.Do(f.editor, "POST", run, nil).Code
		}(i)
	}
	wg.Wait()

	ok := 0
	for _, c := range codes {
		if c == http.StatusOK {
			ok++
		} else if c != http.StatusNotFound {
			t.Fatalf("status tak terduga %d (semua: %v)", c, codes)
		}
	}
	var rows int
	f.target.QueryRow(`SELECT count(*) FROM ` + f.table("once")).Scan(&rows)
	if ok != 1 || rows != 1 {
		t.Fatalf("draft dieksekusi %d kali, baris = %d (status: %v)", ok, rows, codes)
	}
}

func TestPages_EditorDraftHistory(t *testing.T) {
	f := setup(t)
	base := fmt.Sprintf("/databases/%d/migrations", f.dbID)

	rec := f.e.Do(f.viewer, "GET", base, nil)
	apptest.Expect(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), `name="sql"`) {
		t.Fatal("View Only tidak boleh melihat form migration")
	}
	rec = f.e.Do(f.viewer, "POST", base, url.Values{"sql": {"SELECT 1"}, "action": {"draft"}})
	apptest.Expect(t, rec, http.StatusForbidden)
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatal("penolakan di halaman harus berupa HTML, bukan JSON")
	}

	rec = f.e.Do(f.editor, "POST", base, url.Values{"name": {"lewat form"}, "sql": {fmt.Sprintf("CREATE TABLE %s (x int)", f.table("viaform"))}, "action": {"draft"}})
	apptest.Expect(t, rec, http.StatusSeeOther)
	draftPath := rec.Header().Get("Location")
	if !strings.HasPrefix(draftPath, base+"/drafts/") {
		t.Fatalf("redirect simpan draft = %q", draftPath)
	}
	apptest.Expect(t, f.e.Do(f.viewer, "GET", draftPath, nil), http.StatusOK)

	rec = f.e.Do(f.editor, "POST", draftPath+"/run", url.Values{})
	apptest.Expect(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != base+"/history/1" {
		t.Fatalf("redirect setelah run = %q", loc)
	}

	rec = f.e.Do(f.editor, "POST", base, url.Values{"sql": {"SELECT * FROM tidak_ada"}, "action": {"run"}})
	apptest.Expect(t, rec, http.StatusUnprocessableEntity)
	if !strings.Contains(rec.Body.String(), "rollback") {
		t.Fatal("pesan gagal tidak ditampilkan di form")
	}

	rec = f.e.Do(f.viewer, "GET", base+"/history", nil)
	apptest.Expect(t, rec, http.StatusOK)
	for _, want := range []string{"lewat form", "badge-success", "badge-failed"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("halaman riwayat tidak memuat %q", want)
		}
	}
	rec = f.e.Do(f.viewer, "GET", base+"/history/1", nil)
	apptest.Expect(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "viaform") {
		t.Fatal("detail migration tidak menampilkan SQL")
	}
	apptest.Expect(t, f.e.Do(f.outsider, "GET", base+"/history", nil), http.StatusNotFound)
}
