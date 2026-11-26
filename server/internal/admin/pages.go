package admin

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/store"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

func (m *Module) registerPages(mux *http.ServeMux) {
	pmw := m.MW.WithDeniedHandler(m.pageDenied)
	admin := func(perm authz.Permission, h http.HandlerFunc) http.Handler {
		return m.requireAuth(pmw.Require(perm, h))
	}
	db := func(perm authz.Permission, h http.HandlerFunc) http.Handler {
		return m.requireAuth(pmw.RequireDB(perm, h))
	}

	mux.Handle("GET /admin/users", admin(authz.PermManageUsers, m.pageUsers))
	mux.Handle("POST /admin/users", admin(authz.PermManageUsers, m.pageCreateUser))
	mux.Handle("GET /admin/users/{userID}", admin(authz.PermManageUsers, m.pageUserDetail))
	mux.Handle("POST /admin/users/{userID}", admin(authz.PermManageUsers, m.pageUpdateUser))
	mux.Handle("POST /admin/users/{userID}/password", admin(authz.PermManageUsers, m.pageResetPassword))
	mux.Handle("POST /admin/users/{userID}/revoke-sessions", admin(authz.PermManageUsers, m.pageRevokeSessions))
	mux.Handle("POST /admin/users/{userID}/delete", admin(authz.PermManageUsers, m.pageDeleteUser))

	mux.Handle("GET /admin/groups", admin(authz.PermManageGroups, m.pageGroups))
	mux.Handle("POST /admin/groups", admin(authz.PermManageGroups, m.pageCreateGroup))
	mux.Handle("GET /admin/groups/{groupID}", admin(authz.PermManageGroups, m.pageGroupDetail))
	mux.Handle("POST /admin/groups/{groupID}", admin(authz.PermManageGroups, m.pageUpdateGroup))
	mux.Handle("POST /admin/groups/{groupID}/delete", admin(authz.PermManageGroups, m.pageDeleteGroup))
	mux.Handle("POST /admin/groups/{groupID}/members", admin(authz.PermManageGroups, m.pageAddMember))
	mux.Handle("POST /admin/groups/{groupID}/members/{userID}/delete", admin(authz.PermManageGroups, m.pageRemoveMember))
	mux.Handle("POST /admin/groups/{groupID}/grants", admin(authz.PermManageGrants, m.pageSetGrant))
	mux.Handle("POST /admin/groups/{groupID}/grants/{connectionID}/delete", admin(authz.PermManageGrants, m.pageDeleteGrant))

	mux.Handle("GET /admin/connections", admin(authz.PermManageConnections, m.pageConnections))
	mux.Handle("GET /admin/connections/new", admin(authz.PermManageConnections, m.pageNewConnection))
	mux.Handle("POST /admin/connections", admin(authz.PermManageConnections, m.pageSaveConnection))
	mux.Handle("GET /admin/connections/{connectionID}/edit", admin(authz.PermManageConnections, m.pageEditConnection))
	mux.Handle("POST /admin/connections/{connectionID}", admin(authz.PermManageConnections, m.pageSaveConnection))
	mux.Handle("POST /admin/connections/{connectionID}/delete", admin(authz.PermManageConnections, m.pageDeleteConnection))
	mux.Handle("POST /admin/connections/{connectionID}/test", admin(authz.PermManageConnections, m.pageTestConnection))

	mux.Handle("GET /databases", m.requireAuth(pmw.RequireAuth(http.HandlerFunc(m.pageDatabases))))
	mux.Handle("POST /databases/{dbID}/open", db(authz.PermDBViewSchema, m.pageOpenDatabase))
	mux.Handle("GET /databases/{dbID}", db(authz.PermDBViewSchema, m.pageDatabase))
}

func (m *Module) pageDenied(w http.ResponseWriter, r *http.Request, err error) {
	m.Views.Denied(w, r, err)
}

func (m *Module) notFound(w http.ResponseWriter, r *http.Request) {
	m.Views.Error(w, r, http.StatusNotFound, "Tidak ditemukan", httpx.MsgNotFound)
}

func (m *Module) internal(w http.ResponseWriter, r *http.Request, err error) {
	m.Views.Internal(w, r, err)
}

func (m *Module) pageStoreErr(w http.ResponseWriter, r *http.Request, err error, back, conflictMsg string) {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrReference):
		m.notFound(w, r)
	case errors.Is(err, store.ErrConflict):
		web.SetFlash(w, "error", conflictMsg)
		web.Redirect(w, r, back)
	default:
		m.internal(w, r, err)
	}
}

func pathID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

type groupsPage struct {
	Groups []store.Group
	Form   groupRequest
	Error  string
}

func (m *Module) pageGroups(w http.ResponseWriter, r *http.Request) {
	m.renderGroups(w, r, http.StatusOK, groupRequest{}, "")
}

func (m *Module) renderGroups(w http.ResponseWriter, r *http.Request, status int, form groupRequest, errMsg string) {
	groups, err := m.Store.ListGroups(r.Context())
	if err != nil {
		m.internal(w, r, err)
		return
	}
	m.Views.Render(w, r, status, "admin/groups.html", "Group & akses", groupsPage{Groups: groups, Form: form, Error: errMsg})
}

func (m *Module) pageCreateGroup(w http.ResponseWriter, r *http.Request) {
	form := groupRequest{Name: r.FormValue("name"), Description: r.FormValue("description")}
	if msg := form.validate(); msg != "" {
		m.renderGroups(w, r, http.StatusUnprocessableEntity, form, msg)
		return
	}
	g, err := m.Store.CreateGroup(r.Context(), form.Name, form.Description)
	if errors.Is(err, store.ErrConflict) {
		m.renderGroups(w, r, http.StatusConflict, form, groupConflictMsg)
		return
	}
	if err != nil {
		m.internal(w, r, err)
		return
	}
	web.SetFlash(w, "success", fmt.Sprintf("Group %q dibuat. Tambahkan anggota dan akses database.", g.Name))
	web.Redirect(w, r, fmt.Sprintf("/admin/groups/%d", g.ID))
}

type groupDetailPage struct {
	Group          store.Group
	Members        []store.Member
	Grants         []store.Grant
	CandidateUsers []*auth.User
	CandidateConns []store.Connection
	HasConnections bool
}

func (m *Module) pageGroupDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "groupID")
	if !ok {
		m.notFound(w, r)
		return
	}
	ctx := r.Context()
	g, err := m.Store.GetGroup(ctx, id)
	if err != nil {
		m.pageStoreErr(w, r, err, "/admin/groups", "")
		return
	}
	members, err := m.Store.ListMembers(ctx, id)
	if err != nil {
		m.internal(w, r, err)
		return
	}
	grants, err := m.Store.ListGroupGrants(ctx, id)
	if err != nil {
		m.internal(w, r, err)
		return
	}
	users, err := m.Users.List(ctx)
	if err != nil {
		m.internal(w, r, err)
		return
	}
	conns, err := m.Store.ListConnections(ctx)
	if err != nil {
		m.internal(w, r, err)
		return
	}

	isMember := map[string]bool{}
	for _, mb := range members {
		isMember[mb.UserID] = true
	}
	var candUsers []*auth.User
	for _, u := range users {
		if !isMember[u.ID] && u.IsActive {
			candUsers = append(candUsers, u)
		}
	}
	sort.Slice(candUsers, func(i, j int) bool {
		return strings.ToLower(candUsers[i].Username) < strings.ToLower(candUsers[j].Username)
	})
	granted := map[int64]bool{}
	for _, gr := range grants {
		granted[gr.ConnectionID] = true
	}
	var candConns []store.Connection
	for _, c := range conns {
		if !granted[c.ID] {
			candConns = append(candConns, c)
		}
	}

	m.Views.Render(w, r, http.StatusOK, "admin/group_detail.html", g.Name, groupDetailPage{
		Group: g, Members: members, Grants: grants,
		CandidateUsers: candUsers, CandidateConns: candConns, HasConnections: len(conns) > 0,
	})
}

func (m *Module) pageUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "groupID")
	if !ok {
		m.notFound(w, r)
		return
	}
	back := fmt.Sprintf("/admin/groups/%d", id)
	form := groupRequest{Name: r.FormValue("name"), Description: r.FormValue("description")}
	if msg := form.validate(); msg != "" {
		web.SetFlash(w, "error", msg)
		web.Redirect(w, r, back)
		return
	}
	if _, err := m.Store.UpdateGroup(r.Context(), id, form.Name, form.Description); err != nil {
		m.pageStoreErr(w, r, err, back, groupConflictMsg)
		return
	}
	web.SetFlash(w, "success", "Group diperbarui.")
	web.Redirect(w, r, back)
}

func (m *Module) pageDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "groupID")
	if !ok {
		m.notFound(w, r)
		return
	}
	g, err := m.Store.GetGroup(r.Context(), id)
	if err != nil {
		m.pageStoreErr(w, r, err, "/admin/groups", "")
		return
	}
	affected, err := m.Store.DeleteGroup(r.Context(), id)
	if err != nil {
		m.pageStoreErr(w, r, err, "/admin/groups", "")
		return
	}
	m.privilegeChanged(r.Context(), affected, "group_deleted")
	web.SetFlash(w, "success", fmt.Sprintf("Group %q dihapus.", g.Name))
	web.Redirect(w, r, "/admin/groups")
}

func (m *Module) pageAddMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "groupID")
	if !ok {
		m.notFound(w, r)
		return
	}
	back := fmt.Sprintf("/admin/groups/%d", id)
	userID := r.FormValue("user_id")
	if !validUUID(userID) {
		web.SetFlash(w, "error", "Pilih user yang akan ditambahkan.")
		web.Redirect(w, r, back)
		return
	}
	switch err := m.checkMemberCandidate(r.Context(), userID); {
	case errors.Is(err, errInactiveMember):
		web.SetFlash(w, "error", msgInactiveMember)
		web.Redirect(w, r, back)
		return
	case err != nil:
		m.pageStoreErr(w, r, err, back, "")
		return
	}
	created, err := m.Store.AddMember(r.Context(), id, userID)
	if err != nil {
		m.pageStoreErr(w, r, err, back, "")
		return
	}
	if created {
		m.privilegeChanged(r.Context(), []string{userID}, "group_member_added")
	}
	web.SetFlash(w, "success", "Anggota ditambahkan.")
	web.Redirect(w, r, back)
}

func (m *Module) pageRemoveMember(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "groupID")
	userID := r.PathValue("userID")
	if !ok || !validUUID(userID) {
		m.notFound(w, r)
		return
	}
	if err := m.Store.RemoveMember(r.Context(), id, userID); err != nil {
		m.pageStoreErr(w, r, err, "", "")
		return
	}
	m.privilegeChanged(r.Context(), []string{userID}, "group_member_removed")
	web.SetFlash(w, "success", "Anggota dikeluarkan; aksesnya dari group ini langsung dicabut.")
	web.Redirect(w, r, fmt.Sprintf("/admin/groups/%d", id))
}

func (m *Module) pageSetGrant(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "groupID")
	if !ok {
		m.notFound(w, r)
		return
	}
	back := fmt.Sprintf("/admin/groups/%d", id)
	connID, err := strconv.ParseInt(r.FormValue("connection_id"), 10, 64)
	if err != nil || connID <= 0 {
		web.SetFlash(w, "error", "Pilih database yang akan diberi akses.")
		web.Redirect(w, r, back)
		return
	}
	level, err := authz.ParseAccessLevel(r.FormValue("access_level"))
	if err != nil {
		web.SetFlash(w, "error", "Tingkat akses harus View Only atau View + Edit.")
		web.Redirect(w, r, back)
		return
	}
	if err := m.Store.SetGrant(r.Context(), id, connID, level); err != nil {
		m.pageStoreErr(w, r, err, back, "")
		return
	}
	m.notifyGroupMembers(r, id, "grant_set")
	web.SetFlash(w, "success", "Akses database disimpan: "+level.Label()+".")
	web.Redirect(w, r, back)
}

func (m *Module) pageDeleteGrant(w http.ResponseWriter, r *http.Request) {
	id, ok1 := pathID(r, "groupID")
	connID, ok2 := pathID(r, "connectionID")
	if !ok1 || !ok2 {
		m.notFound(w, r)
		return
	}
	if err := m.Store.DeleteGrant(r.Context(), id, connID); err != nil {
		m.pageStoreErr(w, r, err, "", "")
		return
	}
	m.notifyGroupMembers(r, id, "grant_removed")
	web.SetFlash(w, "success", "Akses database dicabut.")
	web.Redirect(w, r, fmt.Sprintf("/admin/groups/%d", id))
}

func (m *Module) pageConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := m.Store.ListConnections(r.Context())
	if err != nil {
		m.internal(w, r, err)
		return
	}
	m.Views.Render(w, r, http.StatusOK, "admin/connections.html", "Koneksi database", conns)
}

type connFormPage struct {
	Editing   bool
	ID        int64
	Form      connectionRequest
	Error     string
	Test      *connmgr.TestResult
	SSLModes  []string
	UpdatedAt time.Time
}

var sslModeList = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

func (m *Module) pageNewConnection(w http.ResponseWriter, r *http.Request) {
	m.renderConnForm(w, r, http.StatusOK, connFormPage{
		Form: connectionRequest{Host: "localhost", Port: 5432, SSLMode: "disable", TestBeforeSave: true},
	})
}

func (m *Module) pageEditConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "connectionID")
	if !ok {
		m.notFound(w, r)
		return
	}
	c, err := m.Store.GetConnection(r.Context(), id)
	if err != nil {
		m.pageStoreErr(w, r, err, "/admin/connections", "")
		return
	}

	m.renderConnForm(w, r, http.StatusOK, connFormPage{
		Editing: true, ID: id, UpdatedAt: c.UpdatedAt,
		Form: connectionRequest{
			Name: c.Name, Description: c.Description, Host: c.Host, Port: c.Port,
			DatabaseName: c.DatabaseName, Username: c.Username, SSLMode: c.SSLMode, TestBeforeSave: true,
		},
	})
}

func (m *Module) renderConnForm(w http.ResponseWriter, r *http.Request, status int, p connFormPage) {
	p.SSLModes = sslModeList
	title := "Tambah koneksi"
	if p.Editing {
		title = "Ubah koneksi"
	}
	m.Views.Render(w, r, status, "admin/connection_form.html", title, p)
}

func (m *Module) pageSaveConnection(w http.ResponseWriter, r *http.Request) {
	var id int64
	editing := r.PathValue("connectionID") != ""
	if editing {
		var ok bool
		if id, ok = pathID(r, "connectionID"); !ok {
			m.notFound(w, r)
			return
		}
	}
	port, _ := strconv.Atoi(strings.TrimSpace(r.FormValue("port")))
	pw := r.FormValue("password")
	form := connectionRequest{
		Name: r.FormValue("name"), Description: r.FormValue("description"),
		Host: r.FormValue("host"), Port: port, DatabaseName: r.FormValue("database_name"),
		Username: r.FormValue("username"), SSLMode: r.FormValue("ssl_mode"),
		TestBeforeSave: r.FormValue("test_before_save") != "",
	}
	page := connFormPage{Editing: editing, ID: id, Form: form}
	if port == 0 && strings.TrimSpace(r.FormValue("port")) != "" {
		page.Error = "Port harus berupa angka."
		m.renderConnForm(w, r, http.StatusUnprocessableEntity, page)
		return
	}
	if msg := page.Form.validate(true); msg != "" {
		page.Error = msg
		m.renderConnForm(w, r, http.StatusUnprocessableEntity, page)
		return
	}

	var newPassword *string
	if pw != "" || !editing {
		newPassword = &pw
	}
	pwd := pw
	if newPassword == nil {
		stored, err := m.storedPassword(r.Context(), id, page.Form.config(""))
		switch {
		case errors.Is(err, errPasswordRequired):
			page.Error = MsgPasswordRequired
			m.renderConnForm(w, r, http.StatusUnprocessableEntity, page)
			return
		case errors.Is(err, store.ErrNotFound):
			m.notFound(w, r)
			return
		case err != nil:
			m.internal(w, r, err)
			return
		}
		pwd = stored
	}

	if r.FormValue("action") == "test" || page.Form.TestBeforeSave {
		res := m.Conns.Test(r.Context(), page.Form.config(pwd))
		page.Test = &res
		if r.FormValue("action") == "test" {
			m.renderConnForm(w, r, http.StatusOK, page)
			return
		}
		if !res.OK {
			page.Error = "Tes koneksi gagal, koneksi tidak disimpan."
			m.renderConnForm(w, r, http.StatusUnprocessableEntity, page)
			return
		}
	}

	var (
		c   store.Connection
		err error
	)
	if editing {
		c, err = m.Store.UpdateConnection(r.Context(), id, page.Form.input(), newPassword)
		if err == nil {
			m.Conns.Invalidate(id)
		}
	} else {
		c, err = m.Store.CreateConnection(r.Context(), page.Form.input(), pw)
	}
	switch {
	case errors.Is(err, store.ErrConflict):
		page.Error = connConflictMsg
		page.Test = nil
		m.renderConnForm(w, r, http.StatusConflict, page)
		return
	case err != nil:
		m.pageStoreErr(w, r, err, "/admin/connections", "")
		return
	}
	web.SetFlash(w, "success", fmt.Sprintf("Koneksi %q disimpan.", c.Name))
	web.Redirect(w, r, "/admin/connections")
}

func (m *Module) pageDeleteConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "connectionID")
	if !ok {
		m.notFound(w, r)
		return
	}
	c, err := m.Store.GetConnection(r.Context(), id)
	if err != nil {
		m.pageStoreErr(w, r, err, "/admin/connections", "")
		return
	}
	affected, err := m.Store.DeleteConnection(r.Context(), id)
	if err != nil {
		m.pageStoreErr(w, r, err, "/admin/connections", "")
		return
	}
	m.Conns.Invalidate(id)
	m.privilegeChanged(r.Context(), affected, "connection_deleted")
	web.SetFlash(w, "success", fmt.Sprintf("Koneksi %q dihapus beserta seluruh grant-nya.", c.Name))
	web.Redirect(w, r, "/admin/connections")
}

func (m *Module) pageTestConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r, "connectionID")
	if !ok {
		m.notFound(w, r)
		return
	}
	c, err := m.Store.GetConnection(r.Context(), id)
	if err != nil {
		m.pageStoreErr(w, r, err, "/admin/connections", "")
		return
	}
	res, err := m.Conns.TestSaved(r.Context(), id)
	if err != nil {
		m.pageStoreErr(w, r, err, "/admin/connections", "")
		return
	}
	if res.OK {
		web.SetFlash(w, "success", fmt.Sprintf("%s: terhubung (PostgreSQL %s, %d ms).", c.Name, res.ServerVersion, res.LatencyMS))
	} else {
		web.SetFlash(w, "error", fmt.Sprintf("%s: %s", c.Name, res.Error))
	}
	web.Redirect(w, r, "/admin/connections")
}

type databasesPage struct {
	Databases []store.AccessibleDatabase
	IsAdmin   bool
}

func (m *Module) pageDatabases(w http.ResponseWriter, r *http.Request) {
	p := authz.PrincipalFrom(r.Context())
	dbs, err := m.Store.AccessibleDatabases(r.Context(), p.ID)
	if err != nil {
		m.internal(w, r, err)
		return
	}
	m.Views.Render(w, r, http.StatusOK, "databases/select.html", "Pilih database", databasesPage{Databases: dbs, IsAdmin: p.IsPlatformAdmin})
}

func (m *Module) pageOpenDatabase(w http.ResponseWriter, r *http.Request) {
	access, _ := authz.DBAccessFrom(r.Context())
	if _, err := m.Conns.Pool(r.Context(), access.DatabaseID); err != nil {
		m.Views.ConnError(w, r, err)
		return
	}
	web.Redirect(w, r, fmt.Sprintf("/databases/%d", access.DatabaseID))
}

type databasePage struct {
	ID           int64
	Name         string
	Description  string
	DatabaseName string
	Level        authz.AccessLevel
	Permissions  []authz.Permission
	CanEdit      bool
}

func (m *Module) pageDatabase(w http.ResponseWriter, r *http.Request) {
	access, _ := authz.DBAccessFrom(r.Context())
	c, err := m.Store.GetConnection(r.Context(), access.DatabaseID)
	if err != nil {
		m.pageStoreErr(w, r, err, "/databases", "")
		return
	}
	m.Views.Render(w, r, http.StatusOK, "databases/overview.html", c.Name, databasePage{
		ID: c.ID, Name: c.Name, Description: c.Description, DatabaseName: c.DatabaseName,
		Level: access.Level, Permissions: authz.PermissionsForLevel(access.Level),
		CanEdit: access.Can(authz.PermDBExecuteMigration),
	})
}
