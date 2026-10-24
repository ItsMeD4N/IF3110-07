package admin

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

type dbEntry struct {
	ID          int64    `json:"id"`
	AccessLevel string   `json:"access_level"`
	Permissions []string `json:"permissions"`
	ViaGroups   []string `json:"via_groups"`
}

func TestAdminAPI_NonAdminRejected(t *testing.T) {
	e := newEnv(t)
	admin, bob := e.newUser(true), e.newUser(false)
	g, _ := e.createGroup(admin)
	c := e.createConn(admin, deadTarget(t))

	routes := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/admin/groups", nil},
		{"POST", "/api/admin/groups", map[string]string{"name": "hacked"}},
		{"GET", fmt.Sprintf("/api/admin/groups/%d", g), nil},
		{"PUT", fmt.Sprintf("/api/admin/groups/%d", g), map[string]string{"name": "hacked"}},
		{"DELETE", fmt.Sprintf("/api/admin/groups/%d", g), nil},
		{"GET", fmt.Sprintf("/api/admin/groups/%d/members", g), nil},
		{"POST", fmt.Sprintf("/api/admin/groups/%d/members", g), map[string]string{"user_id": bob.ID}},
		{"DELETE", fmt.Sprintf("/api/admin/groups/%d/members/%s", g, bob.ID), nil},
		{"GET", fmt.Sprintf("/api/admin/groups/%d/grants", g), nil},
		{"PUT", fmt.Sprintf("/api/admin/groups/%d/grants/%d", g, c), map[string]string{"access_level": "view_edit"}},
		{"DELETE", fmt.Sprintf("/api/admin/groups/%d/grants/%d", g, c), nil},
		{"GET", "/api/admin/connections", nil},
		{"POST", "/api/admin/connections", deadTarget(t)},
		{"POST", "/api/admin/connections/test", map[string]any{"connection_id": c, "host": "127.0.0.1", "database_name": "x", "username": "y"}},
		{"GET", fmt.Sprintf("/api/admin/connections/%d", c), nil},
		{"PUT", fmt.Sprintf("/api/admin/connections/%d", c), deadTarget(t)},
		{"DELETE", fmt.Sprintf("/api/admin/connections/%d", c), nil},
		{"POST", fmt.Sprintf("/api/admin/connections/%d/test", c), nil},
	}
	for _, r := range routes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			rec := e.api(bob, r.method, r.path, r.body)
			expectStatus(t, rec, http.StatusForbidden)
			if code := errCode(t, rec); code != "forbidden" {
				t.Fatalf("code = %q, want forbidden", code)
			}
			expectStatus(t, e.api(nil, r.method, r.path, r.body), http.StatusUnauthorized)
		})
	}

	expectStatus(t, e.api(admin, "GET", fmt.Sprintf("/api/admin/groups/%d", g), nil), http.StatusOK)
	expectStatus(t, e.api(admin, "GET", fmt.Sprintf("/api/admin/connections/%d", c), nil), http.StatusOK)
	members := decode[struct {
		Members []any `json:"members"`
	}](t, e.api(admin, "GET", fmt.Sprintf("/api/admin/groups/%d/members", g), nil))
	if len(members.Members) != 0 {
		t.Fatalf("bob berhasil menambahkan dirinya ke group: %+v", members.Members)
	}
	if ev := e.takeEvents(); len(ev) != 0 {
		t.Fatalf("hook privilege terpanggil oleh request yang ditolak: %+v", ev)
	}
}

func TestAdminPages_NonAdminRejected(t *testing.T) {
	e := newEnv(t)
	admin, bob := e.newUser(true), e.newUser(false)
	g, _ := e.createGroup(admin)
	c := e.createConn(admin, deadTarget(t))

	for _, path := range []string{
		"/admin/groups", fmt.Sprintf("/admin/groups/%d", g),
		"/admin/connections", "/admin/connections/new", fmt.Sprintf("/admin/connections/%d/edit", c),
	} {
		rec := e.page(bob, "GET", path, nil)
		expectStatus(t, rec, http.StatusForbidden)
		if !strings.Contains(rec.Body.String(), "Akses ditolak") {
			t.Fatalf("%s: halaman 403 tidak dirender", path)
		}
		anon := e.page(nil, "GET", path, nil)
		if anon.Code != http.StatusFound || anon.Header().Get("Location") != "/login" {
			t.Fatalf("%s tanpa login: %d %q", path, anon.Code, anon.Header().Get("Location"))
		}
	}

	expectStatus(t, e.page(bob, "POST", fmt.Sprintf("/admin/groups/%d/delete", g), map[string][]string{}), http.StatusForbidden)
	expectStatus(t, e.page(bob, "POST", fmt.Sprintf("/admin/connections/%d/delete", c), map[string][]string{}), http.StatusForbidden)
	expectStatus(t, e.api(admin, "GET", fmt.Sprintf("/api/admin/groups/%d", g), nil), http.StatusOK)
	expectStatus(t, e.api(admin, "GET", fmt.Sprintf("/api/admin/connections/%d", c), nil), http.StatusOK)
}

func TestAuthzMe(t *testing.T) {
	e := newEnv(t)
	admin, bob := e.newUser(true), e.newUser(false)
	type me struct {
		ID              string   `json:"id"`
		IsPlatformAdmin bool     `json:"is_platform_admin"`
		Permissions     []string `json:"permissions"`
	}
	a := decode[me](t, e.api(admin, "GET", "/api/authz/me", nil))
	if a.ID != admin.ID || !a.IsPlatformAdmin || len(a.Permissions) != 4 {
		t.Fatalf("admin /me = %+v", a)
	}
	b := decode[me](t, e.api(bob, "GET", "/api/authz/me", nil))
	if b.IsPlatformAdmin || len(b.Permissions) != 0 {
		t.Fatalf("bob /me = %+v", b)
	}
	expectStatus(t, e.api(nil, "GET", "/api/authz/me", nil), http.StatusUnauthorized)
}

func TestAccess_ViewOnlyVsViewEdit(t *testing.T) {
	e := newEnv(t)
	admin := e.newUser(true)
	viewer, editor, outsider := e.newUser(false), e.newUser(false), e.newUser(false)
	db := e.createConn(admin, realTarget(t))
	viewers, _ := e.createGroup(admin)
	editors, _ := e.createGroup(admin)
	e.setGrant(admin, viewers, db, "view_only")
	e.setGrant(admin, editors, db, "view_edit")
	e.addMember(admin, viewers, viewer)
	e.addMember(admin, editors, editor)

	path := fmt.Sprintf("/api/databases/%d", db)
	v := decode[dbEntry](t, e.api(viewer, "GET", path, nil))
	if v.AccessLevel != "view_only" || strings.Contains(strings.Join(v.Permissions, ","), "db.migrations.execute") {
		t.Fatalf("viewer = %+v", v)
	}
	ed := decode[dbEntry](t, e.api(editor, "GET", path, nil))
	if ed.AccessLevel != "view_edit" || len(ed.Permissions) != 6 {
		t.Fatalf("editor = %+v", ed)
	}

	mig := fmt.Sprintf("/test/databases/%d/migrations", db)
	expectStatus(t, e.api(viewer, "GET", mig, nil), http.StatusOK)
	expectStatus(t, e.api(editor, "GET", mig, nil), http.StatusOK)
	rec := e.api(viewer, "POST", mig, nil)
	expectStatus(t, rec, http.StatusForbidden)
	if code := errCode(t, rec); code != "forbidden" {
		t.Fatalf("code = %q, want forbidden", code)
	}
	expectStatus(t, e.api(editor, "POST", mig, nil), http.StatusOK)
	expectStatus(t, e.api(outsider, "GET", mig, nil), http.StatusNotFound)
	expectStatus(t, e.api(outsider, "POST", mig, nil), http.StatusNotFound)

	expectStatus(t, e.api(viewer, "GET", path+"/status", nil), http.StatusOK)
}

func TestAccess_NoGrantLooksLikeNotFound(t *testing.T) {
	e := newEnv(t)
	admin, outsider := e.newUser(true), e.newUser(false)
	db := e.createConn(admin, realTarget(t))

	noAccess := e.api(outsider, "GET", fmt.Sprintf("/api/databases/%d", db), nil)
	missing := e.api(outsider, "GET", "/api/databases/999999999", nil)
	invalid := e.api(outsider, "GET", "/api/databases/abc", nil)
	for name, code := range map[string]int{"tanpa akses": noAccess.Code, "tidak ada": missing.Code, "id tidak valid": invalid.Code} {
		if code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", name, code)
		}
	}
	if noAccess.Body.String() != missing.Body.String() {
		t.Fatalf("respons database tanpa akses berbeda dari database tidak ada:\n%s\n%s", noAccess.Body, missing.Body)
	}
}

func TestAccess_PlatformAdminIsNotDatabaseAccess(t *testing.T) {
	e := newEnv(t)
	admin := e.newUser(true)
	db := e.createConn(admin, realTarget(t))

	expectStatus(t, e.api(admin, "GET", fmt.Sprintf("/api/databases/%d", db), nil), http.StatusNotFound)
	list := decode[struct {
		Databases []dbEntry `json:"databases"`
	}](t, e.api(admin, "GET", "/api/databases", nil))
	if len(list.Databases) != 0 {
		t.Fatalf("admin tanpa group melihat database: %+v", list.Databases)
	}

	g, _ := e.createGroup(admin)
	e.setGrant(admin, g, db, "view_only")
	e.addMember(admin, g, admin)
	expectStatus(t, e.api(admin, "GET", fmt.Sprintf("/api/databases/%d", db), nil), http.StatusOK)
}

func TestAccess_EffectiveMultiGroup(t *testing.T) {
	e := newEnv(t)
	admin, alice := e.newUser(true), e.newUser(false)
	dbA := e.createConn(admin, realTarget(t))
	dbB := e.createConn(admin, realTarget(t))
	viewers, viewersName := e.createGroup(admin)
	editors, editorsName := e.createGroup(admin)
	e.setGrant(admin, viewers, dbA, "view_only")
	e.setGrant(admin, editors, dbA, "view_edit")
	e.setGrant(admin, editors, dbB, "view_only")
	e.addMember(admin, viewers, alice)
	e.addMember(admin, editors, alice)

	mine := func() map[int64]dbEntry {
		t.Helper()
		resp := decode[struct {
			Databases []dbEntry `json:"databases"`
		}](t, e.api(alice, "GET", "/api/databases", nil))
		out := map[int64]dbEntry{}
		for _, d := range resp.Databases {
			out[d.ID] = d
		}
		return out
	}

	got := mine()
	if got[dbA].AccessLevel != "view_edit" || got[dbB].AccessLevel != "view_only" || len(got) != 2 {
		t.Fatalf("akses efektif awal = %+v", got)
	}
	if via := strings.Join(got[dbA].ViaGroups, ","); !strings.Contains(via, viewersName) || !strings.Contains(via, editorsName) {
		t.Fatalf("via_groups db A = %v", got[dbA].ViaGroups)
	}
	expectStatus(t, e.api(alice, "POST", fmt.Sprintf("/test/databases/%d/migrations", dbA), nil), http.StatusOK)
	expectStatus(t, e.api(alice, "POST", fmt.Sprintf("/test/databases/%d/migrations", dbB), nil), http.StatusForbidden)

	expectStatus(t, e.api(admin, "DELETE", fmt.Sprintf("/api/admin/groups/%d/members/%s", editors, alice.ID), nil), http.StatusNoContent)
	got = mine()
	if got[dbA].AccessLevel != "view_only" || len(got) != 1 {
		t.Fatalf("setelah keluar editors = %+v", got)
	}
	expectStatus(t, e.api(alice, "POST", fmt.Sprintf("/test/databases/%d/migrations", dbA), nil), http.StatusForbidden)
	expectStatus(t, e.api(alice, "GET", fmt.Sprintf("/api/databases/%d", dbB), nil), http.StatusNotFound)

	e.setGrant(admin, viewers, dbA, "view_edit")
	expectStatus(t, e.api(alice, "POST", fmt.Sprintf("/test/databases/%d/migrations", dbA), nil), http.StatusOK)

	expectStatus(t, e.api(admin, "DELETE", fmt.Sprintf("/api/admin/groups/%d", viewers), nil), http.StatusNoContent)
	if got = mine(); len(got) != 0 {
		t.Fatalf("setelah group dihapus = %+v", got)
	}
	expectStatus(t, e.api(alice, "GET", fmt.Sprintf("/api/databases/%d", dbA), nil), http.StatusNotFound)
}
