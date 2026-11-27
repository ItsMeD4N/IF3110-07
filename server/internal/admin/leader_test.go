package admin

import (
	"fmt"
	"net/http"
	"testing"
)

type groupWithLeader struct {
	ID             int64   `json:"id"`
	LeaderUserID   *string `json:"leader_user_id"`
	LeaderUsername *string `json:"leader_username"`
}

func TestGroupLeader_SetValidateAndAutoClear(t *testing.T) {
	e := newEnv(t)
	admin, alice, bob, inactive := e.newUser(true), e.newUser(false), e.newUser(false), e.newUser(false)
	g, _ := e.createGroup(admin)
	leaderPath := fmt.Sprintf("/api/admin/groups/%d/leader", g)

	expectStatus(t, e.api(admin, "PUT", leaderPath, map[string]string{"user_id": alice.ID}), http.StatusUnprocessableEntity)
	expectStatus(t, e.api(admin, "PUT", leaderPath, map[string]string{"user_id": "bukan-uuid"}), http.StatusUnprocessableEntity)
	expectStatus(t, e.api(admin, "PUT", "/api/admin/groups/999999999/leader", map[string]string{"user_id": alice.ID}), http.StatusNotFound)
	expectStatus(t, e.api(admin, "PUT", leaderPath, map[string]string{"user_id": "00000000-0000-0000-0000-000000000000"}), http.StatusNotFound)

	e.addMember(admin, g, alice)
	e.addMember(admin, g, bob)
	e.addMember(admin, g, inactive)
	e.deactivate(inactive)
	expectStatus(t, e.api(admin, "PUT", leaderPath, map[string]string{"user_id": inactive.ID}), http.StatusUnprocessableEntity)

	expectStatus(t, e.api(alice, "PUT", leaderPath, map[string]string{"user_id": alice.ID}), http.StatusForbidden)
	expectStatus(t, e.api(nil, "PUT", leaderPath, map[string]string{"user_id": alice.ID}), http.StatusUnauthorized)

	rec := e.api(admin, "PUT", leaderPath, map[string]string{"user_id": alice.ID})
	expectStatus(t, rec, http.StatusOK)
	got := decode[groupWithLeader](t, rec)
	if got.LeaderUserID == nil || *got.LeaderUserID != alice.ID || got.LeaderUsername == nil || *got.LeaderUsername != alice.Username {
		t.Fatalf("leader = %+v", got)
	}

	detail := decode[groupWithLeader](t, e.api(admin, "GET", fmt.Sprintf("/api/admin/groups/%d", g), nil))
	if detail.LeaderUserID == nil || *detail.LeaderUserID != alice.ID {
		t.Fatalf("detail leader = %+v", detail)
	}
	list := decode[struct {
		Groups []groupWithLeader `json:"groups"`
	}](t, e.api(admin, "GET", "/api/admin/groups", nil))
	seen := false
	for _, x := range list.Groups {
		if x.ID == g && x.LeaderUserID != nil && *x.LeaderUserID == alice.ID {
			seen = true
		}
	}
	if !seen {
		t.Fatal("leader tidak muncul pada daftar group")
	}

	type me struct {
		LedGroups []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"led_groups"`
	}
	if m := decode[me](t, e.api(alice, "GET", "/api/authz/me", nil)); len(m.LedGroups) != 1 || m.LedGroups[0].ID != g {
		t.Fatalf("alice led_groups = %+v", m.LedGroups)
	}
	if m := decode[me](t, e.api(bob, "GET", "/api/authz/me", nil)); m.LedGroups == nil || len(m.LedGroups) != 0 {
		t.Fatalf("bob led_groups = %+v (harus array kosong)", m.LedGroups)
	}

	rec = e.api(admin, "PUT", leaderPath, map[string]string{"user_id": bob.ID})
	expectStatus(t, rec, http.StatusOK)
	if got := decode[groupWithLeader](t, rec); got.LeaderUserID == nil || *got.LeaderUserID != bob.ID {
		t.Fatalf("leader baru = %+v", got)
	}

	expectStatus(t, e.api(admin, "DELETE", fmt.Sprintf("/api/admin/groups/%d/members/%s", g, bob.ID), nil), http.StatusNoContent)
	if got := decode[groupWithLeader](t, e.api(admin, "GET", fmt.Sprintf("/api/admin/groups/%d", g), nil)); got.LeaderUserID != nil {
		t.Fatalf("leader harus kosong setelah dikeluarkan, got %v", *got.LeaderUserID)
	}

	expectStatus(t, e.api(admin, "PUT", leaderPath, map[string]string{"user_id": alice.ID}), http.StatusOK)
	rec = e.api(admin, "PUT", leaderPath, map[string]any{"user_id": nil})
	expectStatus(t, rec, http.StatusOK)
	if got := decode[groupWithLeader](t, rec); got.LeaderUserID != nil {
		t.Fatalf("leader harus kosong, got %v", *got.LeaderUserID)
	}

	expectStatus(t, e.api(admin, "PUT", leaderPath, map[string]string{"user_id": alice.ID}), http.StatusOK)
	expectStatus(t, e.api(admin, "DELETE", fmt.Sprintf("/api/admin/groups/%d", g), nil), http.StatusNoContent)
}
