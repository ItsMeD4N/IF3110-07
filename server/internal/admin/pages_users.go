package admin

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

const (
	minUsernameLen = 3
	minPasswordLen = 8
)

type userForm struct {
	Username        string
	IsPlatformAdmin bool
	IsActive        bool
}

type usersPage struct {
	Users []*auth.User
	Form  userForm
	Error string
}

type userDetailPage struct {
	User  *auth.User
	Self  bool
	Error string
}

func checkbox(r *http.Request, name string) bool {
	v := r.FormValue(name)
	return v == "on" || v == "true" || v == "1"
}

func (m *Module) pageUsers(w http.ResponseWriter, r *http.Request) {
	m.renderUsers(w, r, http.StatusOK, userForm{}, "")
}

func (m *Module) renderUsers(w http.ResponseWriter, r *http.Request, status int, form userForm, errMsg string) {
	users, err := m.Users.List(r.Context())
	if err != nil {
		m.internal(w, r, err)
		return
	}
	m.Views.Render(w, r, status, "admin/users.html", "User", usersPage{Users: users, Form: form, Error: errMsg})
}

func (m *Module) pageCreateUser(w http.ResponseWriter, r *http.Request) {
	form := userForm{Username: strings.TrimSpace(r.FormValue("username")), IsPlatformAdmin: checkbox(r, "is_platform_admin")}
	password := r.FormValue("password")
	switch {
	case len(form.Username) < minUsernameLen:
		m.renderUsers(w, r, http.StatusUnprocessableEntity, form, "Username minimal 3 karakter.")
		return
	case len(password) < minPasswordLen:
		m.renderUsers(w, r, http.StatusUnprocessableEntity, form, "Password minimal 8 karakter.")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		m.internal(w, r, err)
		return
	}
	u, err := m.Users.Create(r.Context(), form.Username, hash, form.IsPlatformAdmin)
	if errors.Is(err, auth.ErrUsernameTaken) {
		m.renderUsers(w, r, http.StatusConflict, form, "Username sudah dipakai.")
		return
	}
	if err != nil {
		m.internal(w, r, err)
		return
	}
	web.SetFlash(w, "success", fmt.Sprintf("User %q dibuat. Masukkan ke group agar bisa mengakses database.", u.Username))
	web.Redirect(w, r, "/admin/users")
}

func (m *Module) loadUser(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	u, err := m.Users.GetByID(r.Context(), r.PathValue("userID"))
	if errors.Is(err, auth.ErrUserNotFound) {
		m.notFound(w, r)
		return nil, false
	}
	if err != nil {
		m.internal(w, r, err)
		return nil, false
	}
	return u, true
}

func isSelf(r *http.Request, id string) bool {
	cur := auth.CurrentUser(r.Context())
	return cur != nil && cur.ID == id
}

func (m *Module) pageUserDetail(w http.ResponseWriter, r *http.Request) {
	u, ok := m.loadUser(w, r)
	if !ok {
		return
	}
	m.renderUserDetail(w, r, http.StatusOK, u, "")
}

func (m *Module) renderUserDetail(w http.ResponseWriter, r *http.Request, status int, u *auth.User, errMsg string) {
	m.Views.Render(w, r, status, "admin/user_detail.html", u.Username, userDetailPage{User: u, Self: isSelf(r, u.ID), Error: errMsg})
}

func (m *Module) pageUpdateUser(w http.ResponseWriter, r *http.Request) {
	u, ok := m.loadUser(w, r)
	if !ok {
		return
	}
	form := userForm{
		Username:        strings.TrimSpace(r.FormValue("username")),
		IsPlatformAdmin: checkbox(r, "is_platform_admin"),
		IsActive:        checkbox(r, "is_active"),
	}
	edited := *u
	edited.Username, edited.IsPlatformAdmin, edited.IsActive = form.Username, form.IsPlatformAdmin, form.IsActive

	if len(form.Username) < minUsernameLen {
		m.renderUserDetail(w, r, http.StatusUnprocessableEntity, &edited, "Username minimal 3 karakter.")
		return
	}
	if isSelf(r, u.ID) && (!form.IsActive || !form.IsPlatformAdmin) {
		m.renderUserDetail(w, r, http.StatusUnprocessableEntity, u, "Kamu tidak bisa menonaktifkan akun sendiri atau mencabut status admin-mu sendiri.")
		return
	}
	err := m.Users.Update(r.Context(), u.ID, form.Username, form.IsPlatformAdmin, form.IsActive)
	if errors.Is(err, auth.ErrUsernameTaken) {
		m.renderUserDetail(w, r, http.StatusConflict, &edited, "Username sudah dipakai.")
		return
	}
	if err != nil {
		m.internal(w, r, err)
		return
	}
	if !form.IsActive || form.IsPlatformAdmin != u.IsPlatformAdmin {
		if err := m.Sessions.RevokeAllForUser(r.Context(), u.ID); err != nil {
			m.internal(w, r, err)
			return
		}
	}
	web.SetFlash(w, "success", "Perubahan user disimpan.")
	web.Redirect(w, r, "/admin/users/"+u.ID)
}

func (m *Module) pageResetPassword(w http.ResponseWriter, r *http.Request) {
	u, ok := m.loadUser(w, r)
	if !ok {
		return
	}
	password := r.FormValue("password")
	if len(password) < minPasswordLen {
		m.renderUserDetail(w, r, http.StatusUnprocessableEntity, u, "Password minimal 8 karakter.")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		m.internal(w, r, err)
		return
	}
	if err := m.Users.UpdatePassword(r.Context(), u.ID, hash); err != nil {
		m.internal(w, r, err)
		return
	}
	if err := m.Sessions.RevokeAllForUser(r.Context(), u.ID); err != nil {
		m.internal(w, r, err)
		return
	}
	web.SetFlash(w, "success", "Password diganti dan semua sesi user ini diakhiri.")
	web.Redirect(w, r, "/admin/users/"+u.ID)
}

func (m *Module) pageRevokeSessions(w http.ResponseWriter, r *http.Request) {
	u, ok := m.loadUser(w, r)
	if !ok {
		return
	}
	if err := m.Sessions.RevokeAllForUser(r.Context(), u.ID); err != nil {
		m.internal(w, r, err)
		return
	}
	web.SetFlash(w, "success", "Semua sesi user ini diakhiri.")
	if isSelf(r, u.ID) {
		web.Redirect(w, r, "/login")
		return
	}
	web.Redirect(w, r, "/admin/users/"+u.ID)
}

func (m *Module) pageDeleteUser(w http.ResponseWriter, r *http.Request) {
	u, ok := m.loadUser(w, r)
	if !ok {
		return
	}
	if isSelf(r, u.ID) {
		m.renderUserDetail(w, r, http.StatusUnprocessableEntity, u, "Kamu tidak bisa menghapus akun sendiri.")
		return
	}
	if err := m.Users.Delete(r.Context(), u.ID); err != nil {
		m.internal(w, r, err)
		return
	}
	web.SetFlash(w, "success", fmt.Sprintf("User %q dihapus.", u.Username))
	web.Redirect(w, r, "/admin/users")
}
