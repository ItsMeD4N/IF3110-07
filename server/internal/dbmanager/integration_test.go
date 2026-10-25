package dbmanager_test

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/apptest"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

type schemaResp struct {
	Schemas []string
	Tables  []struct {
		Name    string
		Columns []struct {
			Name, Type string
			Nullable   bool
		}
		Constraints []struct{ Name, Type, Definition string }
		Indexes     []struct{ Name, Definition string }
	}
}

type dataResp struct {
	Columns []string
	Rows    [][]any
	Total   int64
}

func TestViewer_SchemaAndData(t *testing.T) {
	e := apptest.New(t)
	admin, viewer, outsider := e.NewUser(true), e.NewUser(false), e.NewUser(false)
	target, schema := testutil.OpenTargetSchema(t)
	_, err := target.Exec(fmt.Sprintf(`
		CREATE TABLE %[1]s.customers (id serial PRIMARY KEY, email text NOT NULL UNIQUE, note text);
		CREATE INDEX customers_note_idx ON %[1]s.customers (note);
		INSERT INTO %[1]s.customers (email) SELECT 'u' || g || '@x.id' FROM generate_series(1, 7) g;`, schema))
	if err != nil {
		t.Fatal(err)
	}
	dbID := e.Database(admin, apptest.TargetConn(t))
	e.Grant(admin, viewer, dbID, "view_only")
	base := fmt.Sprintf("/api/databases/%d", dbID)

	rec := e.Do(viewer, "GET", base+"/schema?schema="+schema, nil)
	apptest.Expect(t, rec, http.StatusOK)
	s := apptest.Decode[schemaResp](t, rec)
	if len(s.Tables) != 1 || s.Tables[0].Name != "customers" {
		t.Fatalf("tabel = %+v", s.Tables)
	}
	tbl := s.Tables[0]
	if len(tbl.Columns) != 3 || tbl.Columns[1].Name != "email" || tbl.Columns[1].Nullable || !tbl.Columns[2].Nullable {
		t.Fatalf("kolom = %+v", tbl.Columns)
	}
	types := map[string]bool{}
	for _, c := range tbl.Constraints {
		types[c.Type] = true
	}
	if !types["PRIMARY KEY"] || !types["UNIQUE"] {
		t.Fatalf("constraint = %+v", tbl.Constraints)
	}
	foundIdx := false
	for _, i := range tbl.Indexes {
		foundIdx = foundIdx || i.Name == "customers_note_idx"
	}
	if !foundIdx {
		t.Fatalf("index = %+v", tbl.Indexes)
	}
	apptest.Expect(t, e.Do(viewer, "GET", base+"/schema?schema=tidak_ada", nil), http.StatusBadRequest)

	rec = e.Do(viewer, "GET", fmt.Sprintf("%s/tables/customers/data?schema=%s&limit=5&offset=5", base, schema), nil)
	apptest.Expect(t, rec, http.StatusOK)
	d := apptest.Decode[dataResp](t, rec)
	if d.Total != 7 || len(d.Rows) != 2 || len(d.Columns) != 3 || d.Rows[0][1] != "u6@x.id" {
		t.Fatalf("data = %+v", d)
	}
	apptest.Expect(t, e.Do(viewer, "GET", fmt.Sprintf("%s/tables/tidak_ada/data?schema=%s", base, schema), nil), http.StatusNotFound)
	apptest.Expect(t, e.Do(viewer, "GET", base+"/tables/pg_class/data?schema=pg_catalog", nil), http.StatusNotFound)

	rec = e.Do(viewer, "GET", fmt.Sprintf("%s/tables/customers%%22%%3B%%20DROP%%20TABLE%%20x%%3B--/data?schema=%s", base, schema), nil)
	apptest.Expect(t, rec, http.StatusNotFound)

	apptest.Expect(t, e.Do(outsider, "GET", base+"/schema", nil), http.StatusNotFound)
	apptest.Expect(t, e.Do(admin, "GET", base+"/schema", nil), http.StatusNotFound)
	apptest.Expect(t, e.Do(nil, "GET", base+"/schema", nil), http.StatusUnauthorized)

	rec = e.Do(viewer, "GET", fmt.Sprintf("/databases/%d/viewer", dbID), nil)
	apptest.Expect(t, rec, http.StatusOK)
	rec = e.Do(outsider, "GET", fmt.Sprintf("/databases/%d/viewer", dbID), nil)
	apptest.Expect(t, rec, http.StatusNotFound)
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/html") {
		t.Fatal("penolakan halaman viewer harus HTML")
	}
}

func TestViewer_ConnectionFailure(t *testing.T) {
	e := apptest.New(t)
	admin, viewer := e.NewUser(true), e.NewUser(false)
	dbID := e.Database(admin, apptest.DeadConn())
	e.Grant(admin, viewer, dbID, "view_only")

	rec := e.Do(viewer, "GET", fmt.Sprintf("/api/databases/%d/schema", dbID), nil)
	apptest.Expect(t, rec, http.StatusServiceUnavailable)
	if code := apptest.Decode[struct{ Code string }](t, rec).Code; code != "db_connection_failed" {
		t.Fatalf("code = %q", code)
	}
	rec = e.Do(viewer, "GET", fmt.Sprintf("/databases/%d/viewer", dbID), nil)
	apptest.Expect(t, rec, http.StatusServiceUnavailable)
	if !strings.Contains(rec.Body.String(), "tidak dapat dijangkau") {
		t.Fatal("halaman viewer tidak menampilkan pesan koneksi gagal")
	}
}

func TestViewer_RefusesPlatformDatabase(t *testing.T) {
	e := apptest.New(t)
	admin, viewer := e.NewUser(true), e.NewUser(false)
	conn := apptest.TargetConn(t)
	platform, err := connmgr.ConfigFromURL(testutil.DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	conn["database_name"] = platform.Database
	dbID := e.Database(admin, conn)
	e.Grant(admin, viewer, dbID, "view_only")

	rec := e.Do(viewer, "GET", fmt.Sprintf("/api/databases/%d/schema", dbID), nil)
	apptest.Expect(t, rec, http.StatusConflict)
	rec = e.Do(admin, "POST", fmt.Sprintf("/api/admin/connections/%d/test", dbID), nil)
	if strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("tes koneksi ke database platform seharusnya gagal: %s", rec.Body.String())
	}
}
