package admin

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
)

func sorted(s []string) []string {
	out := append([]string{}, s...)
	sort.Strings(out)
	return out
}

func expectEvent(t *testing.T, got []privEvent, reason string, users ...string) {
	t.Helper()
	if len(got) != 1 || got[0].reason != reason || fmt.Sprint(sorted(got[0].users)) != fmt.Sprint(sorted(users)) {
		t.Fatalf("event = %+v, want 1 event %q untuk %v", got, reason, users)
	}
}

func TestPrivilegeChangeHook(t *testing.T) {
	e := newEnv(t)
	admin, alice, bob := e.newUser(true), e.newUser(false), e.newUser(false)
	g, _ := e.createGroup(admin)
	db := e.createConn(admin, deadTarget(t))
	e.takeEvents()

	e.addMember(admin, g, alice)
	expectEvent(t, e.takeEvents(), "group_member_added", alice.ID)
	e.addMember(admin, g, alice)
	if ev := e.takeEvents(); len(ev) != 0 {
		t.Fatalf("anggota ganda memicu event: %+v", ev)
	}
	e.addMember(admin, g, bob)
	e.takeEvents()

	e.setGrant(admin, g, db, "view_edit")
	expectEvent(t, e.takeEvents(), "grant_set", alice.ID, bob.ID)

	expectStatus(t, e.api(admin, "DELETE", fmt.Sprintf("/api/admin/groups/%d/grants/%d", g, db), nil), http.StatusNoContent)
	expectEvent(t, e.takeEvents(), "grant_removed", alice.ID, bob.ID)

	expectStatus(t, e.api(admin, "DELETE", fmt.Sprintf("/api/admin/groups/%d/members/%s", g, bob.ID), nil), http.StatusNoContent)
	expectEvent(t, e.takeEvents(), "group_member_removed", bob.ID)

	e.setGrant(admin, g, db, "view_only")
	e.takeEvents()
	expectStatus(t, e.api(admin, "DELETE", fmt.Sprintf("/api/admin/connections/%d", db), nil), http.StatusNoContent)
	expectEvent(t, e.takeEvents(), "connection_deleted", alice.ID)

	expectStatus(t, e.api(admin, "DELETE", fmt.Sprintf("/api/admin/groups/%d", g), nil), http.StatusNoContent)
	expectEvent(t, e.takeEvents(), "group_deleted", alice.ID)
}

func sessionCookieFrom(rec interface{ Result() *http.Response }) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.SessionCookieName {
			return c.Value
		}
	}
	return ""
}

func TestPrivilegeChange_RotatesSessionIdentifier(t *testing.T) {
	e := newEnv(t)
	e.mod.OnPrivilegeChange = func(ctx context.Context, ids []string, _ string) {
		if err := e.sessions.RequireRotation(ctx, ids); err != nil {
			t.Errorf("RequireRotation: %v", err)
		}
	}
	admin, alice := e.newUser(true), e.newUser(false)
	db := e.createConn(admin, realTarget(t))
	g, _ := e.createGroup(admin)
	e.setGrant(admin, g, db, "view_only")

	oldToken := e.cookie(alice).Value

	rec := e.api(alice, "GET", "/api/databases", nil)
	expectStatus(t, rec, http.StatusOK)
	if sessionCookieFrom(rec) != "" {
		t.Fatal("session dirotasi padahal privilege tidak berubah")
	}

	e.addMember(admin, g, alice)

	rec = e.api(alice, "GET", "/api/databases", nil)
	expectStatus(t, rec, http.StatusOK)
	newToken := sessionCookieFrom(rec)
	if newToken == "" || newToken == oldToken {
		t.Fatalf("session tidak dirotasi setelah privilege berubah (cookie baru = %q)", newToken)
	}

	var pending bool
	e.db.QueryRow(`SELECT bool_or(rotation_required) FROM sessions WHERE user_id = $1 AND NOT is_revoked`, alice.ID).Scan(&pending)
	if pending {
		t.Fatal("penanda rotasi tidak dibersihkan setelah rotasi")
	}

	expectStatus(t, e.api(alice, "GET", "/api/databases", nil), http.StatusOK)

	e.mu.Lock()
	e.cookies[alice.ID] = &http.Cookie{Name: session.SessionCookieName, Value: newToken}
	e.mu.Unlock()
	rec = e.api(alice, "GET", "/api/databases", nil)
	expectStatus(t, rec, http.StatusOK)
	if sessionCookieFrom(rec) != "" {
		t.Fatal("rotasi berulang padahal tidak ada perubahan privilege baru")
	}
}
