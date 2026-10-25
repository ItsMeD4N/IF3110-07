package admin

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func TestUserPages_CRUD(t *testing.T) {
	e := newEnv(t)
	admin, bob := e.newUser(true), e.newUser(false)

	rec := e.page(admin, "GET", "/admin/users", nil)
	expectStatus(t, rec, http.StatusOK)
	expectBody(t, rec.Body.String(), "Tambah user", admin.Username, bob.Username)
	if passwordInput.FindString(rec.Body.String()) == "" {
		t.Fatal("form tambah user tidak punya input password")
	}

	expectStatus(t, e.page(admin, "POST", "/admin/users", url.Values{"username": {"ab"}, "password": {"Passw0rd!!"}}), http.StatusUnprocessableEntity)
	expectStatus(t, e.page(admin, "POST", "/admin/users", url.Values{"username": {testutil.Unique("c")}, "password": {"short"}}), http.StatusUnprocessableEntity)
	expectStatus(t, e.page(admin, "POST", "/admin/users", url.Values{"username": {bob.Username}, "password": {"Passw0rd!!"}}), http.StatusConflict)

	name := testutil.Unique("carol")
	rec = e.page(admin, "POST", "/admin/users", url.Values{"username": {name}, "password": {"Passw0rd!!"}})
	expectStatus(t, rec, http.StatusSeeOther)
	carol, err := e.users.GetByUsername(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.users.Delete(ctx, carol.ID) })
	if carol.IsPlatformAdmin || !carol.IsActive || strings.Contains(carol.PasswordHash, "Passw0rd") {
		t.Fatalf("user baru = %+v", carol)
	}

	carolPath := "/admin/users/" + carol.ID
	expectStatus(t, e.page(admin, "GET", carolPath, nil), http.StatusOK)
	expectStatus(t, e.page(admin, "GET", "/admin/users/00000000-0000-0000-0000-000000000000", nil), http.StatusNotFound)

	_, carolToken, err := e.sessions.CreateSession(ctx, carol.ID, "127.0.0.1", "test")
	if err != nil {
		t.Fatal(err)
	}
	rec = e.page(admin, "POST", carolPath, url.Values{"username": {name}, "is_platform_admin": {"on"}, "is_active": {"on"}})
	expectStatus(t, rec, http.StatusSeeOther)
	if u, _ := e.users.GetByID(ctx, carol.ID); !u.IsPlatformAdmin {
		t.Fatal("status admin tidak tersimpan")
	}
	if _, err := e.sessions.ValidateSession(ctx, carolToken, nil); err == nil {
		t.Fatal("perubahan privilege harus mengakhiri sesi lama")
	}

	expectStatus(t, e.page(admin, "POST", carolPath+"/password", url.Values{"password": {"pendek"}}), http.StatusUnprocessableEntity)
	expectStatus(t, e.page(admin, "POST", carolPath+"/password", url.Values{"password": {"PasswordBaru123"}}), http.StatusSeeOther)
	if _, err := auth.VerifyCredentials(ctx, e.users, name, "PasswordBaru123"); err != nil {
		t.Fatalf("password baru tidak berlaku: %v", err)
	}

	_, tok, _ := e.sessions.CreateSession(ctx, carol.ID, "127.0.0.1", "test")
	expectStatus(t, e.page(admin, "POST", carolPath+"/revoke-sessions", url.Values{}), http.StatusSeeOther)
	if _, err := e.sessions.ValidateSession(ctx, tok, nil); err != session.ErrSessionRevoked {
		t.Fatalf("sesi harus dicabut, err = %v", err)
	}

	expectStatus(t, e.page(admin, "POST", carolPath+"/delete", url.Values{}), http.StatusSeeOther)
	if _, err := e.users.GetByID(ctx, carol.ID); err != auth.ErrUserNotFound {
		t.Fatalf("user tidak terhapus: %v", err)
	}
}

func TestUserPages_SelfProtectionAndAccess(t *testing.T) {
	e := newEnv(t)
	admin, bob := e.newUser(true), e.newUser(false)
	self := "/admin/users/" + admin.ID

	expectStatus(t, e.page(admin, "POST", self, url.Values{"username": {admin.Username}, "is_active": {"on"}}), http.StatusUnprocessableEntity)
	expectStatus(t, e.page(admin, "POST", self+"/delete", url.Values{}), http.StatusUnprocessableEntity)
	if u, _ := e.users.GetByID(ctx, admin.ID); !u.IsPlatformAdmin || !u.IsActive {
		t.Fatal("admin berhasil mencabut status admin/aktif dirinya sendiri")
	}

	rec := e.page(bob, "GET", "/admin/users", nil)
	expectStatus(t, rec, http.StatusForbidden)
	expectStatus(t, e.page(bob, "POST", "/admin/users", url.Values{"username": {"zzz"}, "password": {"Passw0rd!!"}}), http.StatusForbidden)
	rec = e.page(nil, "GET", "/admin/users", nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
		t.Fatalf("tanpa login harus diarahkan ke /login, dapat %d %q", rec.Code, rec.Header().Get("Location"))
	}
}
