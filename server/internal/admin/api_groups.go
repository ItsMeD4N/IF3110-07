package admin

import (
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/store"
)

const (
	maxNameLen        = 64
	maxDescriptionLen = 500
)

type meResponse struct {
	ID              string             `json:"id"`
	Username        string             `json:"username"`
	IsPlatformAdmin bool               `json:"is_platform_admin"`
	Permissions     []authz.Permission `json:"permissions"`
}

func (m *Module) apiMe(w http.ResponseWriter, r *http.Request) {
	p := authz.PrincipalFrom(r.Context())
	httpx.WriteJSON(w, http.StatusOK, meResponse{
		ID: p.ID, Username: p.Username, IsPlatformAdmin: p.IsPlatformAdmin,
		Permissions: authz.PlatformPermissions(p),
	})
}

type groupRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (req *groupRequest) validate() string {
	req.Name = store.NormalizeName(req.Name)
	switch {
	case req.Name == "":
		return "Nama group wajib diisi."
	case utf8.RuneCountInString(req.Name) > maxNameLen:
		return "Nama group maksimal 64 karakter."
	case utf8.RuneCountInString(req.Description) > maxDescriptionLen:
		return "Deskripsi maksimal 500 karakter."
	}
	return ""
}

const groupConflictMsg = "Nama group sudah digunakan."

func (m *Module) apiListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := m.Store.ListGroups(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"groups": groups})
}

func (m *Module) apiCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req groupRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		httpx.ValidationError(w, msg)
		return
	}
	g, err := m.Store.CreateGroup(r.Context(), req.Name, req.Description)
	if err != nil {
		writeStoreErr(w, r, err, groupConflictMsg)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, g)
}

type groupDetail struct {
	store.Group
	Members []store.Member `json:"members"`
	Grants  []store.Grant  `json:"grants"`
}

func (m *Module) apiGetGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, "groupID")
	if !ok {
		return
	}
	g, err := m.Store.GetGroup(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	members, err := m.Store.ListMembers(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	grants, err := m.Store.ListGroupGrants(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, groupDetail{Group: g, Members: members, Grants: grants})
}

func (m *Module) apiUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, "groupID")
	if !ok {
		return
	}
	var req groupRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if msg := req.validate(); msg != "" {
		httpx.ValidationError(w, msg)
		return
	}
	g, err := m.Store.UpdateGroup(r.Context(), id, req.Name, req.Description)
	if err != nil {
		writeStoreErr(w, r, err, groupConflictMsg)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, g)
}

func (m *Module) apiDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, "groupID")
	if !ok {
		return
	}
	affected, err := m.Store.DeleteGroup(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	m.privilegeChanged(r.Context(), affected, "group_deleted")
	w.WriteHeader(http.StatusNoContent)
}

type memberRequest struct {
	UserID string `json:"user_id"`
}

func (m *Module) apiListMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, "groupID")
	if !ok {
		return
	}
	members, err := m.Store.ListMembers(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"members": members})
}

func (m *Module) apiAddMember(w http.ResponseWriter, r *http.Request) {
	groupID, ok := httpx.PathID(w, r, "groupID")
	if !ok {
		return
	}
	var req memberRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if !validUUID(req.UserID) {
		httpx.ValidationError(w, "user_id wajib diisi (UUID).")
		return
	}
	created, err := m.Store.AddMember(r.Context(), groupID, req.UserID)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	if created {
		m.privilegeChanged(r.Context(), []string{req.UserID}, "group_member_added")
	}
	members, err := m.Store.ListMembers(r.Context(), groupID)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	httpx.WriteJSON(w, status, map[string]any{"members": members})
}

func (m *Module) apiRemoveMember(w http.ResponseWriter, r *http.Request) {
	groupID, ok := httpx.PathID(w, r, "groupID")
	if !ok {
		return
	}
	userID := r.PathValue("userID")
	if !validUUID(userID) {
		httpx.NotFound(w)
		return
	}
	if err := m.Store.RemoveMember(r.Context(), groupID, userID); err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	m.privilegeChanged(r.Context(), []string{userID}, "group_member_removed")
	w.WriteHeader(http.StatusNoContent)
}

type grantRequest struct {
	AccessLevel string `json:"access_level"`
}

func (m *Module) apiListGrants(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, "groupID")
	if !ok {
		return
	}
	grants, err := m.Store.ListGroupGrants(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"grants": grants})
}

func (m *Module) apiSetGrant(w http.ResponseWriter, r *http.Request) {
	groupID, ok := httpx.PathID(w, r, "groupID")
	if !ok {
		return
	}
	connID, ok := httpx.PathID(w, r, "connectionID")
	if !ok {
		return
	}
	var req grantRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	level, err := authz.ParseAccessLevel(req.AccessLevel)
	if err != nil {
		httpx.ValidationError(w, "access_level harus \"view_only\" atau \"view_edit\".")
		return
	}
	if err := m.Store.SetGrant(r.Context(), groupID, connID, level); err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	m.notifyGroupMembers(r, groupID, "grant_set")

	grants, err := m.Store.ListGroupGrants(r.Context(), groupID)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"grants": grants})
}

func (m *Module) apiDeleteGrant(w http.ResponseWriter, r *http.Request) {
	groupID, ok := httpx.PathID(w, r, "groupID")
	if !ok {
		return
	}
	connID, ok := httpx.PathID(w, r, "connectionID")
	if !ok {
		return
	}
	if err := m.Store.DeleteGrant(r.Context(), groupID, connID); err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	m.notifyGroupMembers(r, groupID, "grant_removed")
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) notifyGroupMembers(r *http.Request, groupID int64, reason string) {
	if m.OnPrivilegeChange == nil {
		return
	}
	ids, err := m.Store.GroupMemberIDs(r.Context(), groupID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {

		logWarn(r, "gagal memuat anggota group untuk hook privilege", err)
		return
	}
	m.privilegeChanged(r.Context(), ids, reason)
}
