package admin

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func TestGroupsAPI_CRUD(t *testing.T) {
	e := newEnv(t)
	admin := e.newUser(true)
	id, name := e.createGroup(admin)

	expectStatus(t, e.api(admin, "POST", "/api/admin/groups", map[string]string{"name": "  " + strings.ToUpper(name) + " "}), http.StatusConflict)
	expectStatus(t, e.api(admin, "POST", "/api/admin/groups", map[string]string{"name": "   "}), http.StatusUnprocessableEntity)
	expectStatus(t, e.api(admin, "POST", "/api/admin/groups", map[string]string{"name": strings.Repeat("x", 65)}), http.StatusUnprocessableEntity)
	expectStatus(t, e.api(admin, "POST", "/api/admin/groups", `{"name":"a","role":"superuser"}`), http.StatusBadRequest)
	expectStatus(t, e.api(admin, "POST", "/api/admin/groups", `{bukan json`), http.StatusBadRequest)

	path := fmt.Sprintf("/api/admin/groups/%d", id)
	rec := e.api(admin, "PUT", path, map[string]string{"name": name + " Baru", "description": "deskripsi"})
	expectStatus(t, rec, http.StatusOK)
	g := decode[struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}](t, rec)
	if g.Name != name+" Baru" || g.Description != "deskripsi" {
		t.Fatalf("update = %+v", g)
	}

	other, otherName := e.createGroup(admin)
	expectStatus(t, e.api(admin, "PUT", fmt.Sprintf("/api/admin/groups/%d", other), map[string]string{"name": name + " baru"}), http.StatusConflict)
	_ = otherName

	detail := decode[struct {
		ID      int64 `json:"id"`
		Members []any `json:"members"`
		Grants  []any `json:"grants"`
	}](t, e.api(admin, "GET", path, nil))
	if detail.ID != id || detail.Members == nil || detail.Grants == nil {
		t.Fatalf("detail = %+v", detail)
	}

	list := decode[struct {
		Groups []struct {
			ID int64 `json:"id"`
		} `json:"groups"`
	}](t, e.api(admin, "GET", "/api/admin/groups", nil))
	found := false
	for _, x := range list.Groups {
		found = found || x.ID == id
	}
	if !found {
		t.Fatal("group tidak muncul di daftar")
	}

	expectStatus(t, e.api(admin, "DELETE", path, nil), http.StatusNoContent)
	for _, m := range []string{"GET", "DELETE"} {
		expectStatus(t, e.api(admin, m, path, nil), http.StatusNotFound)
	}
	expectStatus(t, e.api(admin, "PUT", path, map[string]string{"name": "x"}), http.StatusNotFound)
	expectStatus(t, e.api(admin, "GET", "/api/admin/groups/abc", nil), http.StatusNotFound)
}

func TestGroupsAPI_Members(t *testing.T) {
	e := newEnv(t)
	admin, alice, inactive := e.newUser(true), e.newUser(false), e.newUser(false)
	e.deactivate(inactive)
	g, _ := e.createGroup(admin)
	members := fmt.Sprintf("/api/admin/groups/%d/members", g)

	expectStatus(t, e.api(admin, "POST", members, map[string]string{"user_id": alice.ID}), http.StatusCreated)
	expectStatus(t, e.api(admin, "POST", members, map[string]string{"user_id": alice.ID}), http.StatusOK)

	rec := e.api(admin, "POST", members, map[string]string{"user_id": inactive.ID})
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if !strings.Contains(rec.Body.String(), "nonaktif") {
		t.Fatalf("pesan user nonaktif: %s", rec.Body.String())
	}
	expectStatus(t, e.api(admin, "POST", members, map[string]string{"user_id": "00000000-0000-0000-0000-00000000beef"}), http.StatusNotFound)
	expectStatus(t, e.api(admin, "POST", members, map[string]string{"user_id": "bukan-uuid"}), http.StatusUnprocessableEntity)
	expectStatus(t, e.api(admin, "POST", "/api/admin/groups/999999999/members", map[string]string{"user_id": alice.ID}), http.StatusNotFound)

	list := decode[struct {
		Members []struct {
			UserID string `json:"user_id"`
		} `json:"members"`
	}](t, e.api(admin, "GET", members, nil))
	if len(list.Members) != 1 || list.Members[0].UserID != alice.ID {
		t.Fatalf("anggota = %+v", list.Members)
	}

	expectStatus(t, e.api(admin, "DELETE", members+"/"+alice.ID, nil), http.StatusNoContent)
	expectStatus(t, e.api(admin, "DELETE", members+"/"+alice.ID, nil), http.StatusNotFound)
	expectStatus(t, e.api(admin, "DELETE", members+"/bukan-uuid", nil), http.StatusNotFound)
	expectStatus(t, e.api(admin, "GET", "/api/admin/groups/999999999/members", nil), http.StatusNotFound)
}

func TestGroupsAPI_GrantsPerDatabase(t *testing.T) {
	e := newEnv(t)
	admin := e.newUser(true)
	g, _ := e.createGroup(admin)
	dbA := e.createConn(admin, deadTarget(t))
	dbB := e.createConn(admin, deadTarget(t))

	e.setGrant(admin, g, dbA, "view_edit")
	e.setGrant(admin, g, dbB, "view_only")
	grants := func() map[int64]string {
		t.Helper()
		resp := decode[struct {
			Grants []struct {
				ConnectionID int64  `json:"connection_id"`
				AccessLevel  string `json:"access_level"`
			} `json:"grants"`
		}](t, e.api(admin, "GET", fmt.Sprintf("/api/admin/groups/%d/grants", g), nil))
		out := map[int64]string{}
		for _, x := range resp.Grants {
			out[x.ConnectionID] = x.AccessLevel
		}
		return out
	}
	if got := grants(); got[dbA] != "view_edit" || got[dbB] != "view_only" {
		t.Fatalf("grant per database = %v", got)
	}

	e.setGrant(admin, g, dbA, "view_only")
	if got := grants(); got[dbA] != "view_only" || len(got) != 2 {
		t.Fatalf("setelah ubah level = %v", got)
	}

	grant := fmt.Sprintf("/api/admin/groups/%d/grants/%d", g, dbA)
	expectStatus(t, e.api(admin, "PUT", grant, map[string]string{"access_level": "admin"}), http.StatusUnprocessableEntity)
	expectStatus(t, e.api(admin, "PUT", grant, map[string]string{"access_level": ""}), http.StatusUnprocessableEntity)
	expectStatus(t, e.api(admin, "PUT", fmt.Sprintf("/api/admin/groups/%d/grants/999999999", g), map[string]string{"access_level": "view_only"}), http.StatusNotFound)
	expectStatus(t, e.api(admin, "PUT", fmt.Sprintf("/api/admin/groups/999999999/grants/%d", dbA), map[string]string{"access_level": "view_only"}), http.StatusNotFound)

	expectStatus(t, e.api(admin, "DELETE", grant, nil), http.StatusNoContent)
	expectStatus(t, e.api(admin, "DELETE", grant, nil), http.StatusNotFound)
	if got := grants(); len(got) != 1 {
		t.Fatalf("setelah hapus grant = %v", got)
	}
}

func TestConnectionsAPI_CRUDNeverReturnsCredentials(t *testing.T) {
	e := newEnv(t)
	admin := e.newUser(true)

	target := realTarget(t)
	target["test_before_save"] = true
	rec := e.api(admin, "POST", "/api/admin/connections", target)
	expectStatus(t, rec, http.StatusCreated)
	id := decode[idResp](t, rec).ID
	t.Cleanup(func() { e.db.Exec(`DELETE FROM db_connections WHERE id = $1`, id) })
	secret := target["password"].(string)

	noSecret := func(where string, rec interface{ String() string }) {
		t.Helper()
		if body := rec.String(); strings.Contains(strings.ToLower(body), "password") {
			t.Fatalf("%s mengembalikan kredensial: %s", where, body)
		}
	}
	noSecret("create", rec.Body)
	path := fmt.Sprintf("/api/admin/connections/%d", id)
	for _, p := range []string{path, "/api/admin/connections"} {
		r := e.api(admin, "GET", p, nil)
		expectStatus(t, r, http.StatusOK)
		noSecret("GET "+p, r.Body)
	}

	var raw []byte
	e.db.QueryRow(`SELECT password_encrypted FROM db_connections WHERE id = $1`, id).Scan(&raw)
	if secret != "" && strings.Contains(string(raw), secret) {
		t.Fatal("password tersimpan dalam bentuk plaintext")
	}

	dup := realTarget(t)
	dup["name"] = strings.ToUpper(target["name"].(string))
	expectStatus(t, e.api(admin, "POST", "/api/admin/connections", dup), http.StatusConflict)
	for field, bad := range map[string]any{"host": "evil host", "port": 70000, "ssl_mode": "maybe", "database_name": "", "username": ""} {
		p := realTarget(t)
		p[field] = bad
		expectStatus(t, e.api(admin, "POST", "/api/admin/connections", p), http.StatusUnprocessableEntity)
	}

	upd := realTarget(t)
	delete(upd, "password")
	upd["name"] = testutil.Unique("Renamed")
	upd["test_before_save"] = true
	expectStatus(t, e.api(admin, "PUT", path, upd), http.StatusOK)
	res := decode[struct {
		OK bool `json:"ok"`
	}](t, e.api(admin, "POST", path+"/test", nil))
	if !res.OK {
		t.Fatal("password tersimpan hilang setelah update tanpa password")
	}

	expectStatus(t, e.api(admin, "DELETE", path, nil), http.StatusNoContent)
	expectStatus(t, e.api(admin, "GET", path, nil), http.StatusNotFound)
	expectStatus(t, e.api(admin, "POST", path+"/test", nil), http.StatusNotFound)
	expectStatus(t, e.api(admin, "DELETE", path, nil), http.StatusNotFound)
}

func TestConnectionsAPI_FailedConnection(t *testing.T) {
	e := newEnv(t)
	admin, alice := e.newUser(true), e.newUser(false)

	dead := deadTarget(t)
	dead["test_before_save"] = true
	rec := e.api(admin, "POST", "/api/admin/connections", dead)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if errCode(t, rec) != "db_connection_failed" || strings.Contains(rec.Body.String(), deadPassword) {
		t.Fatalf("simpan dengan tes gagal: %s", rec.Body.String())
	}
	list := e.api(admin, "GET", "/api/admin/connections", nil)
	if strings.Contains(list.Body.String(), dead["name"].(string)) {
		t.Fatal("koneksi yang gagal dites tetap tersimpan")
	}

	rec = e.api(admin, "POST", "/api/admin/connections/test", deadTarget(t))
	expectStatus(t, rec, http.StatusOK)
	tr := decode[struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}](t, rec)
	if tr.OK || tr.Error == "" || strings.Contains(tr.Error, deadPassword) {
		t.Fatalf("tes koneksi gagal = %+v", tr)
	}

	id := e.createConn(admin, deadTarget(t))
	tr = decode[struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}](t, e.api(admin, "POST", fmt.Sprintf("/api/admin/connections/%d/test", id), nil))
	if tr.OK || tr.Error == "" {
		t.Fatalf("tes koneksi tersimpan = %+v", tr)
	}

	g, _ := e.createGroup(admin)
	e.setGrant(admin, g, id, "view_only")
	e.addMember(admin, g, alice)
	rec = e.api(alice, "GET", fmt.Sprintf("/api/databases/%d/status", id), nil)
	expectStatus(t, rec, http.StatusServiceUnavailable)
	if errCode(t, rec) != "db_connection_failed" || strings.Contains(rec.Body.String(), deadPassword) || strings.Contains(rec.Body.String(), "127.0.0.1") {
		t.Fatalf("respons koneksi gagal membocorkan detail: %s", rec.Body.String())
	}
	expectStatus(t, e.api(alice, "GET", fmt.Sprintf("/api/databases/%d", id), nil), http.StatusOK)
}
