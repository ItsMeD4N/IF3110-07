package admin

import (
	"errors"
	"net/http"
	"regexp"

	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/store"
)

func (m *Module) registerAPI(mux *http.ServeMux) {
	admin := func(perm authz.Permission, h http.HandlerFunc) http.Handler {
		return m.requireAuth(m.MW.Require(perm, h))
	}
	db := func(perm authz.Permission, h http.HandlerFunc) http.Handler {
		return m.requireAuth(m.MW.RequireDB(perm, h))
	}

	mux.Handle("GET /api/authz/me", m.requireAuth(m.MW.RequireAuth(http.HandlerFunc(m.apiMe))))

	mux.Handle("GET /api/admin/groups", admin(authz.PermManageGroups, m.apiListGroups))
	mux.Handle("POST /api/admin/groups", admin(authz.PermManageGroups, m.apiCreateGroup))
	mux.Handle("GET /api/admin/groups/{groupID}", admin(authz.PermManageGroups, m.apiGetGroup))
	mux.Handle("PUT /api/admin/groups/{groupID}", admin(authz.PermManageGroups, m.apiUpdateGroup))
	mux.Handle("DELETE /api/admin/groups/{groupID}", admin(authz.PermManageGroups, m.apiDeleteGroup))

	mux.Handle("GET /api/admin/groups/{groupID}/members", admin(authz.PermManageGroups, m.apiListMembers))
	mux.Handle("POST /api/admin/groups/{groupID}/members", admin(authz.PermManageGroups, m.apiAddMember))
	mux.Handle("DELETE /api/admin/groups/{groupID}/members/{userID}", admin(authz.PermManageGroups, m.apiRemoveMember))

	mux.Handle("GET /api/admin/groups/{groupID}/grants", admin(authz.PermManageGrants, m.apiListGrants))
	mux.Handle("PUT /api/admin/groups/{groupID}/grants/{connectionID}", admin(authz.PermManageGrants, m.apiSetGrant))
	mux.Handle("DELETE /api/admin/groups/{groupID}/grants/{connectionID}", admin(authz.PermManageGrants, m.apiDeleteGrant))

	mux.Handle("GET /api/admin/connections", admin(authz.PermManageConnections, m.apiListConnections))
	mux.Handle("POST /api/admin/connections", admin(authz.PermManageConnections, m.apiCreateConnection))
	mux.Handle("POST /api/admin/connections/test", admin(authz.PermManageConnections, m.apiTestNewConnection))
	mux.Handle("GET /api/admin/connections/{connectionID}", admin(authz.PermManageConnections, m.apiGetConnection))
	mux.Handle("PUT /api/admin/connections/{connectionID}", admin(authz.PermManageConnections, m.apiUpdateConnection))
	mux.Handle("DELETE /api/admin/connections/{connectionID}", admin(authz.PermManageConnections, m.apiDeleteConnection))
	mux.Handle("POST /api/admin/connections/{connectionID}/test", admin(authz.PermManageConnections, m.apiTestSavedConnection))

	mux.Handle("GET /api/databases", m.requireAuth(m.MW.RequireAuth(http.HandlerFunc(m.apiListMyDatabases))))
	mux.Handle("GET /api/databases/{dbID}", db(authz.PermDBViewSchema, m.apiGetMyDatabase))
	mux.Handle("GET /api/databases/{dbID}/status", db(authz.PermDBViewSchema, m.apiDatabaseStatus))
}

func writeStoreErr(w http.ResponseWriter, r *http.Request, err error, conflictMsg string) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrReference):
		httpx.NotFound(w)
	case errors.Is(err, store.ErrConflict):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict, conflictMsg)
	default:
		httpx.Internal(w, r, err)
	}
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func validUUID(s string) bool { return uuidRe.MatchString(s) }
