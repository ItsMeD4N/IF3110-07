package admin

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

var passwordInput = regexp.MustCompile(`<input[^>]*name="password"[^>]*>`)

func expectBody(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Fatalf("halaman tidak memuat %q", w)
		}
	}
}

func TestAdminPages_GroupsMembersGrants(t *testing.T) {
	e := newEnv(t)
	admin, alice, inactive := e.newUser(true), e.newUser(false), e.newUser(false)
	e.deactivate(inactive)
	db := e.createConn(admin, deadTarget(t))
	dbName := decode[struct {
		Name string `json:"name"`
	}](t, e.api(admin, "GET", fmt.Sprintf("/api/admin/connections/%d", db), nil)).Name

	name := testutil.Unique("Page Group")
	rec := e.page(admin, "POST", "/admin/groups", url.Values{"name": {name}, "description": {"dari form"}})
	expectStatus(t, rec, http.StatusSeeOther)
	loc := rec.Header().Get("Location")
	var gid int64
	if _, err := fmt.Sscanf(loc, "/admin/groups/%d", &gid); err != nil {
		t.Fatalf("redirect setelah buat group = %q", loc)
	}
	t.Cleanup(func() { e.db.Exec(`DELETE FROM user_groups WHERE id = $1`, gid) })

	expectStatus(t, e.page(admin, "POST", "/admin/groups", url.Values{"name": {"  "}}), http.StatusUnprocessableEntity)
	expectStatus(t, e.page(admin, "POST", "/admin/groups", url.Values{"name": {strings.ToLower(name)}}), http.StatusConflict)

	rec = e.page(admin, "GET", "/admin/groups", nil)
	expectStatus(t, rec, http.StatusOK)
	expectBody(t, rec.Body.String(), name)

	groupPath := fmt.Sprintf("/admin/groups/%d", gid)
	rec = e.page(admin, "POST", groupPath+"/members", url.Values{"user_id": {alice.ID}})
	expectStatus(t, rec, http.StatusSeeOther)
	if kind, _ := flash(rec); kind != "success" {
		t.Fatalf("flash tambah anggota = %q", kind)
	}
	rec = e.page(admin, "POST", groupPath+"/members", url.Values{"user_id": {inactive.ID}})
	expectStatus(t, rec, http.StatusSeeOther)
	if kind, msg := flash(rec); kind != "error" || !strings.Contains(msg, "nonaktif") {
		t.Fatalf("flash user nonaktif = %q %q", kind, msg)
	}

	rec = e.page(admin, "POST", groupPath+"/grants", url.Values{"connection_id": {fmt.Sprint(db)}, "access_level": {"view_edit"}})
	expectStatus(t, rec, http.StatusSeeOther)
	rec = e.page(admin, "POST", groupPath+"/grants", url.Values{"connection_id": {fmt.Sprint(db)}, "access_level": {"root"}})
	if kind, _ := flash(rec); kind != "error" {
		t.Fatal("level grant tidak valid lewat form tidak ditolak")
	}

	rec = e.page(admin, "GET", groupPath, nil)
	expectStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	expectBody(t, body, alice.Username, dbName, "View + Edit")
	if strings.Contains(body, inactive.Username) {
		t.Fatal("user nonaktif ditawarkan sebagai kandidat anggota")
	}

	expectStatus(t, e.page(admin, "POST", fmt.Sprintf("%s/grants/%d/delete", groupPath, db), url.Values{}), http.StatusSeeOther)
	expectStatus(t, e.page(admin, "POST", fmt.Sprintf("%s/members/%s/delete", groupPath, alice.ID), url.Values{}), http.StatusSeeOther)
	expectStatus(t, e.page(admin, "POST", groupPath+"/delete", url.Values{}), http.StatusSeeOther)
	expectStatus(t, e.page(admin, "GET", groupPath, nil), http.StatusNotFound)
	expectStatus(t, e.page(admin, "GET", "/admin/groups/abc", nil), http.StatusNotFound)
}

func TestAdminPages_Connections(t *testing.T) {
	e := newEnv(t)
	admin := e.newUser(true)
	target := realTarget(t)

	expectStatus(t, e.page(admin, "GET", "/admin/connections/new", nil), http.StatusOK)

	form := url.Values{}
	for k, v := range target {
		form.Set(k, fmt.Sprint(v))
	}
	form.Set("action", "test")
	rec := e.page(admin, "POST", "/admin/connections", form)
	expectStatus(t, rec, http.StatusOK)
	expectBody(t, rec.Body.String(), "Berhasil terhubung")

	form.Set("action", "save")
	form.Set("test_before_save", "1")
	rec = e.page(admin, "POST", "/admin/connections", form)
	expectStatus(t, rec, http.StatusSeeOther)
	var id int64
	e.db.QueryRow(`SELECT id FROM db_connections WHERE name = $1`, target["name"]).Scan(&id)
	if id == 0 {
		t.Fatal("koneksi tidak tersimpan")
	}
	t.Cleanup(func() { e.db.Exec(`DELETE FROM db_connections WHERE id = $1`, id) })

	bad := url.Values{"name": {testutil.Unique("Bad")}, "host": {"127.0.0.1"}, "port": {"abc"}, "database_name": {"x"}, "username": {"u"}, "password": {"p"}}
	expectStatus(t, e.page(admin, "POST", "/admin/connections", bad), http.StatusUnprocessableEntity)
	bad.Set("port", fmt.Sprint(closedPort(t)))
	bad.Set("test_before_save", "1")
	rec = e.page(admin, "POST", "/admin/connections", bad)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	expectBody(t, rec.Body.String(), "Tes koneksi gagal")

	for _, p := range []string{"/admin/connections", fmt.Sprintf("/admin/connections/%d/edit", id)} {
		rec := e.page(admin, "GET", p, nil)
		expectStatus(t, rec, http.StatusOK)
		if m := passwordInput.FindString(rec.Body.String()); strings.Contains(m, "value=") {
			t.Fatalf("%s mengisi input password: %s", p, m)
		}
		expectBody(t, rec.Body.String(), fmt.Sprint(target["name"]))
	}

	rec = e.page(admin, "POST", fmt.Sprintf("/admin/connections/%d/test", id), url.Values{})
	expectStatus(t, rec, http.StatusSeeOther)
	if kind, msg := flash(rec); kind != "success" || !strings.Contains(msg, "terhubung") {
		t.Fatalf("flash tes koneksi = %q %q", kind, msg)
	}

	expectStatus(t, e.page(admin, "POST", fmt.Sprintf("/admin/connections/%d/delete", id), url.Values{}), http.StatusSeeOther)
	expectStatus(t, e.page(admin, "GET", fmt.Sprintf("/admin/connections/%d/edit", id), nil), http.StatusNotFound)
}

func TestDatabasePages(t *testing.T) {
	e := newEnv(t)
	admin, alice := e.newUser(true), e.newUser(false)
	live := e.createConn(admin, realTarget(t))
	dead := e.createConn(admin, deadTarget(t))
	hidden := e.createConn(admin, realTarget(t))
	names := map[int64]string{}
	for _, id := range []int64{live, dead, hidden} {
		names[id] = decode[struct {
			Name string `json:"name"`
		}](t, e.api(admin, "GET", fmt.Sprintf("/api/admin/connections/%d", id), nil)).Name
	}
	g, _ := e.createGroup(admin)
	e.setGrant(admin, g, live, "view_only")
	e.setGrant(admin, g, dead, "view_edit")
	e.addMember(admin, g, alice)

	rec := e.page(alice, "GET", "/databases", nil)
	expectStatus(t, rec, http.StatusOK)
	body := rec.Body.String()
	expectBody(t, body, names[live], names[dead], "badge-view_only", "badge-view_edit")
	if strings.Contains(body, names[hidden]) {
		t.Fatal("database tanpa akses ditampilkan di halaman pilih database")
	}
	if strings.Contains(body, `href="/admin/groups"`) {
		t.Fatal("menu admin ditampilkan untuk user biasa")
	}

	rec = e.page(alice, "POST", fmt.Sprintf("/databases/%d/open", live), url.Values{})
	expectStatus(t, rec, http.StatusSeeOther)
	if loc := rec.Header().Get("Location"); loc != fmt.Sprintf("/databases/%d", live) {
		t.Fatalf("redirect open = %q", loc)
	}
	rec = e.page(alice, "GET", fmt.Sprintf("/databases/%d", live), nil)
	expectStatus(t, rec, http.StatusOK)
	expectBody(t, rec.Body.String(), names[live])

	rec = e.page(alice, "POST", fmt.Sprintf("/databases/%d/open", dead), url.Values{})
	expectStatus(t, rec, http.StatusServiceUnavailable)
	expectBody(t, rec.Body.String(), httpx.MsgConnectionFailed)
	if strings.Contains(rec.Body.String(), deadPassword) {
		t.Fatal("halaman koneksi gagal membocorkan password")
	}

	expectStatus(t, e.page(alice, "GET", fmt.Sprintf("/databases/%d", hidden), nil), http.StatusNotFound)
	expectStatus(t, e.page(alice, "POST", fmt.Sprintf("/databases/%d/open", hidden), url.Values{}), http.StatusNotFound)

	rec = e.page(admin, "GET", "/databases", nil)
	expectStatus(t, rec, http.StatusOK)
	expectBody(t, rec.Body.String(), "Sebagai Platform Admin")
}
