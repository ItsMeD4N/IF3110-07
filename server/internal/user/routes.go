package user

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
)

type Handler struct {
	userRepo   auth.UserRepository
	sessionMgr *session.Manager
}

func NewHandler(userRepo auth.UserRepository, sessionMgr *session.Manager) *Handler {
	return &Handler{
		userRepo:   userRepo,
		sessionMgr: sessionMgr,
	}
}

func RequirePlatformAdmin(next http.Handler) http.Handler {
	return authz.RequirePlatform(authz.PermManageUsers, next)
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	authMw := auth.RequireAuth(h.userRepo, h.sessionMgr)
	adminMw := RequirePlatformAdmin

	protect := func(handler http.HandlerFunc) http.Handler {
		return authMw(adminMw(handler))
	}

	mux.Handle("GET /api/admin/users", protect(h.handleListUsers))

	mux.Handle("POST /api/admin/users", protect(h.handleCreateUser))

	mux.Handle("GET /api/admin/users/{id}", protect(h.handleGetUser))

	mux.Handle("PUT /api/admin/users/{id}", protect(h.handleUpdateUser))
	mux.Handle("POST /api/admin/users/{id}", protect(h.handleUpdateUser))

	mux.Handle("POST /api/admin/users/{id}/reset-password", protect(h.handleResetPassword))

	mux.Handle("POST /api/admin/users/{id}/revoke-sessions", protect(h.handleRevokeSessions))

	mux.Handle("DELETE /api/admin/users/{id}", protect(h.handleDeleteUser))
	mux.Handle("POST /api/admin/users/{id}/delete", protect(h.handleDeleteUser))
}

func RegisterRoutes(mux *http.ServeMux, userRepo auth.UserRepository, sessionMgr *session.Manager) {
	h := NewHandler(userRepo, sessionMgr)
	h.RegisterRoutes(mux)
}

func (h *Handler) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.userRepo.List(r.Context())
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal mengambil daftar pengguna",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"users": users,
	})
}

type createUserRequest struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
}

func (h *Handler) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest

	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Format payload JSON tidak valid",
			})
			return
		}
	} else {
		_ = r.ParseForm()
		req.Username = r.FormValue("username")
		req.Password = r.FormValue("password")
		req.IsPlatformAdmin = r.FormValue("is_platform_admin") == "true" || r.FormValue("is_platform_admin") == "1"
	}

	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Username minimal terdiri dari 3 karakter",
		})
		return
	}

	if len(req.Password) < 8 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Password minimal terdiri dari 8 karakter",
		})
		return
	}

	passwordHash, err := auth.HashPassword(req.Password)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal melakukan hashing password",
		})
		return
	}

	newUser, err := h.userRepo.Create(r.Context(), req.Username, passwordHash, req.IsPlatformAdmin)
	if err != nil {
		if errors.Is(err, auth.ErrUsernameTaken) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Username sudah digunakan oleh akun lain",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal menyimpan pengguna baru",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Pengguna baru berhasil dibuat",
		"user":    newUser,
	})
}

func (h *Handler) handleGetUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	u, err := h.userRepo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Pengguna tidak ditemukan",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal mengambil data pengguna",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"user": u,
	})
}

type updateUserRequest struct {
	Username        string `json:"username"`
	IsPlatformAdmin bool   `json:"is_platform_admin"`
	IsActive        bool   `json:"is_active"`
}

func (h *Handler) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req updateUserRequest

	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Format payload JSON tidak valid",
			})
			return
		}
	} else {
		_ = r.ParseForm()
		req.Username = r.FormValue("username")
		req.IsPlatformAdmin = r.FormValue("is_platform_admin") == "true" || r.FormValue("is_platform_admin") == "1"
		req.IsActive = r.FormValue("is_active") == "true" || r.FormValue("is_active") == "1"
	}

	req.Username = strings.TrimSpace(req.Username)
	if len(req.Username) < 3 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Username minimal terdiri dari 3 karakter",
		})
		return
	}

	existing, err := h.userRepo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Pengguna tidak ditemukan",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal memverifikasi pengguna",
		})
		return
	}

	currentUser := auth.CurrentUser(r.Context())
	if currentUser != nil && currentUser.ID == id && !req.IsActive {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Kamu tidak bisa menonaktifkan akun sendiri",
		})
		return
	}

	err = h.userRepo.Update(r.Context(), id, req.Username, req.IsPlatformAdmin, req.IsActive)
	if err != nil {
		if errors.Is(err, auth.ErrUsernameTaken) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Username sudah digunakan oleh akun lain",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal memperbarui pengguna",
		})
		return
	}

	if !req.IsActive || (existing.IsPlatformAdmin != req.IsPlatformAdmin) {
		_ = h.sessionMgr.RevokeAllForUser(r.Context(), id)
	}

	updatedUser, _ := h.userRepo.GetByID(r.Context(), id)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Pengguna berhasil diperbarui",
		"user":    updatedUser,
	})
}

type resetPasswordRequest struct {
	NewPassword string `json:"new_password"`
}

func (h *Handler) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req resetPasswordRequest

	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Format payload JSON tidak valid",
			})
			return
		}
	} else {
		_ = r.ParseForm()
		req.NewPassword = r.FormValue("new_password")
		if id == "" {
			id = r.FormValue("user_id")
		}
	}

	if len(req.NewPassword) < 8 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Password baru minimal terdiri dari 8 karakter",
		})
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal melakukan hashing password",
		})
		return
	}

	if err := h.userRepo.UpdatePassword(r.Context(), id, hash); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Pengguna tidak ditemukan",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal mereset password pengguna",
		})
		return
	}

	_ = h.sessionMgr.RevokeAllForUser(r.Context(), id)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "Password pengguna berhasil direset dan seluruh sesi aktif telah dicabut",
	})
}

func (h *Handler) handleRevokeSessions(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	_, err := h.userRepo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Pengguna tidak ditemukan",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal memverifikasi pengguna",
		})
		return
	}

	if err := h.sessionMgr.RevokeAllForUser(r.Context(), id); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal mencabut sesi pengguna",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "Seluruh sesi aktif pengguna berhasil dicabut",
	})
}

func (h *Handler) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	currentUser := auth.CurrentUser(r.Context())
	if currentUser != nil && currentUser.ID == id {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Kamu tidak bisa menghapus akun sendiri",
		})
		return
	}

	_ = h.sessionMgr.RevokeAllForUser(r.Context(), id)

	if err := h.userRepo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, auth.ErrUserNotFound) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Pengguna tidak ditemukan",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal menghapus pengguna",
		})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "Pengguna berhasil dihapus",
	})
}
