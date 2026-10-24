package web

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/user"
	"github.com/ItsMeD4N/IF3110-07/server/internal/view"
)

type Handler struct {
	userRepo   auth.UserRepository
	sessionMgr *session.Manager
	viewEngine *view.Engine
}

func NewHandler(userRepo auth.UserRepository, sessionMgr *session.Manager, viewEngine *view.Engine) *Handler {
	return &Handler{
		userRepo:   userRepo,
		sessionMgr: sessionMgr,
		viewEngine: viewEngine,
	}
}

// RegisterRoutes mendaftarkan seluruh rute web / MPA (Go HTML Template)
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	authMw := auth.RequireAuth(h.userRepo, h.sessionMgr)
	adminMw := user.RequirePlatformAdmin
	maybeAuth := auth.Authenticate(h.userRepo, h.sessionMgr)

	// 1. Root & Login
	mux.Handle("GET /{$}", maybeAuth(http.HandlerFunc(h.handleRoot)))
	mux.Handle("GET /login", maybeAuth(http.HandlerFunc(h.handleLoginPage)))
	mux.HandleFunc("POST /login", h.handleLoginForm)
	mux.Handle("POST /logout", authMw(http.HandlerFunc(h.handleLogoutForm)))

	// 2. Admin Users Management (Protected)
	adminProtect := func(fn http.HandlerFunc) http.Handler {
		return authMw(adminMw(fn))
	}

	mux.Handle("GET /admin/users", adminProtect(h.handleAdminUsersPage))
	mux.Handle("POST /admin/users", adminProtect(h.handleWebCreateUser))
	mux.Handle("POST /admin/users/{id}/edit", adminProtect(h.handleWebEditUser))
	mux.Handle("POST /admin/users/{id}/reset-password", adminProtect(h.handleWebResetPassword))
	mux.Handle("POST /admin/users/{id}/revoke-sessions", adminProtect(h.handleWebRevokeSessions))
	mux.Handle("POST /admin/users/{id}/delete", adminProtect(h.handleWebDeleteUser))
}

func (h *Handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	currentUser := auth.CurrentUser(r.Context())
	if currentUser != nil {
		if currentUser.IsPlatformAdmin {
			http.Redirect(w, r, "/admin/users", http.StatusFound)
			return
		}
		// Untuk user biasa, bisa ke dashboard atau halaman DB (Orang C)
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (h *Handler) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if auth.CurrentUser(r.Context()) != nil {
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	h.viewEngine.Render(w, r, http.StatusOK, "auth/login.html", view.TemplateData{
		Title:      "Masuk ke Akun",
		ActiveMenu: "login",
	})
}

func (h *Handler) handleLoginForm(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")

	if username == "" || password == "" {
		view.SetFlashError(w, "Username dan password wajib diisi")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	u, err := h.userRepo.GetByUsername(r.Context(), username)
	if err != nil || !auth.CheckPassword(u.PasswordHash, password) {
		view.SetFlashError(w, "Username atau password salah")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	if !u.IsActive {
		view.SetFlashError(w, "Akun Anda telah dinonaktifkan. Silakan hubungi Platform Admin.")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	// Buat sesi baru
	_, opaqueToken, err := h.sessionMgr.CreateSession(r.Context(), u.ID, r.RemoteAddr, r.UserAgent())
	if err != nil {
		view.SetFlashError(w, "Gagal membuat sesi login. Silakan coba lagi.")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	// Pasang cookie sesi
	h.sessionMgr.SetCookie(w, opaqueToken)
	view.SetFlashSuccess(w, fmt.Sprintf("Selamat datang kembali, %s!", u.Username))

	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

func (h *Handler) handleLogoutForm(w http.ResponseWriter, r *http.Request) {
	if token, err := h.sessionMgr.ExtractToken(r); err == nil && token != "" {
		_ = h.sessionMgr.Revoke(r.Context(), token, w)
	}

	h.sessionMgr.ClearCookie(w)
	view.SetFlashSuccess(w, "Anda telah berhasil keluar dari sistem.")
	http.Redirect(w, r, "/login", http.StatusFound)
}

type adminUsersViewData struct {
	Users       []*auth.User
	TotalUsers  int
	TotalAdmins int
	TotalActive int
}

func (h *Handler) handleAdminUsersPage(w http.ResponseWriter, r *http.Request) {
	users, err := h.userRepo.List(r.Context())
	if err != nil {
		view.SetFlashError(w, "Gagal memuat daftar pengguna")
		users = []*auth.User{}
	}

	totalAdmins := 0
	totalActive := 0
	for _, u := range users {
		if u.IsPlatformAdmin {
			totalAdmins++
		}
		if u.IsActive {
			totalActive++
		}
	}

	data := adminUsersViewData{
		Users:       users,
		TotalUsers:  len(users),
		TotalAdmins: totalAdmins,
		TotalActive: totalActive,
	}

	h.viewEngine.Render(w, r, http.StatusOK, "admin/users.html", view.TemplateData{
		Title:      "Manajemen Pengguna",
		ActiveMenu: "users",
		Data:       data,
	})
}

func (h *Handler) handleWebCreateUser(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	password := r.FormValue("password")
	isAdmin := r.FormValue("is_platform_admin") == "true" || r.FormValue("is_platform_admin") == "1"

	if len(username) < 3 {
		view.SetFlashError(w, "Username minimal terdiri dari 3 karakter")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	if len(password) < 8 {
		view.SetFlashError(w, "Password minimal terdiri dari 8 karakter")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		view.SetFlashError(w, "Gagal melakukan hashing password")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	_, err = h.userRepo.Create(r.Context(), username, hash, isAdmin)
	if err != nil {
		if errors.Is(err, auth.ErrUsernameTaken) {
			view.SetFlashError(w, fmt.Sprintf("Username '%s' sudah digunakan oleh akun lain", username))
		} else {
			view.SetFlashError(w, "Gagal menyimpan pengguna baru")
		}
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	view.SetFlashSuccess(w, fmt.Sprintf("Pengguna baru '%s' berhasil dibuat!", username))
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

func (h *Handler) handleWebEditUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_ = r.ParseForm()
	username := strings.TrimSpace(r.FormValue("username"))
	isAdmin := r.FormValue("is_platform_admin") == "true" || r.FormValue("is_platform_admin") == "1"
	isActive := r.FormValue("is_active") == "true" || r.FormValue("is_active") == "1"

	if len(username) < 3 {
		view.SetFlashError(w, "Username minimal terdiri dari 3 karakter")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	currentUser := auth.CurrentUser(r.Context())
	if currentUser != nil && currentUser.ID == id && !isActive {
		view.SetFlashError(w, "Anda tidak dapat menonaktifkan akun Platform Admin Anda sendiri")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	existing, err := h.userRepo.GetByID(r.Context(), id)
	if err != nil {
		view.SetFlashError(w, "Pengguna tidak ditemukan")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	if err := h.userRepo.Update(r.Context(), id, username, isAdmin, isActive); err != nil {
		if errors.Is(err, auth.ErrUsernameTaken) {
			view.SetFlashError(w, fmt.Sprintf("Username '%s' sudah digunakan oleh akun lain", username))
		} else {
			view.SetFlashError(w, "Gagal memperbarui data pengguna")
		}
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	// Jika akun dinonaktifkan atau privilege admin berubah: cabut semua sesi aktif pengguna
	if !isActive || (existing.IsPlatformAdmin != isAdmin) {
		_ = h.sessionMgr.RevokeAllForUser(r.Context(), id)
	}

	view.SetFlashSuccess(w, fmt.Sprintf("Data pengguna '%s' berhasil diperbarui!", username))
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

func (h *Handler) handleWebResetPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_ = r.ParseForm()
	newPassword := r.FormValue("new_password")

	if len(newPassword) < 8 {
		view.SetFlashError(w, "Password baru minimal terdiri dari 8 karakter")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		view.SetFlashError(w, "Gagal melakukan enkripsi password baru")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	if err := h.userRepo.UpdatePassword(r.Context(), id, hash); err != nil {
		view.SetFlashError(w, "Gagal mereset kata sandi pengguna")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	// Reset password wajib mencabut seluruh sesi aktif
	_ = h.sessionMgr.RevokeAllForUser(r.Context(), id)

	view.SetFlashSuccess(w, "Kata sandi pengguna berhasil direset dan seluruh sesi aktif telah dicabut!")
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

func (h *Handler) handleWebRevokeSessions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	if err := h.sessionMgr.RevokeAllForUser(r.Context(), id); err != nil {
		view.SetFlashError(w, "Gagal mencabut sesi pengguna")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	view.SetFlashSuccess(w, "Seluruh sesi aktif pengguna berhasil dicabut (force logout)!")
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}

func (h *Handler) handleWebDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	currentUser := auth.CurrentUser(r.Context())

	if currentUser != nil && currentUser.ID == id {
		view.SetFlashError(w, "Anda tidak dapat menghapus akun Platform Admin Anda sendiri")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	_ = h.sessionMgr.RevokeAllForUser(r.Context(), id)
	if err := h.userRepo.Delete(r.Context(), id); err != nil {
		view.SetFlashError(w, "Gagal menghapus pengguna")
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}

	view.SetFlashSuccess(w, "Pengguna berhasil dihapus!")
	http.Redirect(w, r, "/admin/users", http.StatusFound)
}
