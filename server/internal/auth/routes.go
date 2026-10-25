package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
)

type Handler struct {
	userRepo   UserRepository
	sessionMgr *session.Manager
}

func NewHandler(userRepo UserRepository, sessionMgr *session.Manager) *Handler {
	return &Handler{
		userRepo:   userRepo,
		sessionMgr: sessionMgr,
	}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	middleware := NewMiddleware(h.userRepo, h.sessionMgr)

	mux.HandleFunc("POST /api/auth/login", h.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", h.handleLogout)
	mux.Handle("GET /api/auth/me", middleware.RequireAuth(http.HandlerFunc(h.handleMe)))
}

func RegisterRoutes(mux *http.ServeMux, userRepo UserRepository, sessionMgr *session.Manager) {
	h := NewHandler(userRepo, sessionMgr)
	h.RegisterRoutes(mux)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (h *Handler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest

	if strings.Contains(r.Header.Get("Content-Type"), "application/json") {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "Format payload JSON tidak valid")
			return
		}
	} else {
		_ = r.ParseForm()
		req.Username = r.FormValue("username")
		req.Password = r.FormValue("password")
	}

	user, err := VerifyCredentials(r.Context(), h.userRepo, req.Username, req.Password)
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		writeJSONError(w, http.StatusUnauthorized, "Username atau password salah")
		return
	case errors.Is(err, ErrInactiveUser):
		writeJSONError(w, http.StatusForbidden, "Akun Anda telah dinonaktifkan. Silakan hubungi Platform Admin.")
		return
	case err != nil:
		writeJSONError(w, http.StatusInternalServerError, "Terjadi kesalahan internal server")
		return
	}

	if old, err := h.sessionMgr.ExtractToken(r); err == nil {
		_ = h.sessionMgr.Revoke(r.Context(), old, nil)
	}
	sess, token, err := h.sessionMgr.CreateSession(r.Context(), user.ID, r.RemoteAddr, r.UserAgent())
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "Gagal membuat sesi login")
		return
	}
	h.sessionMgr.SetCookie(w, token, sess.AbsoluteExpiresAt)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"message": "Login berhasil",
		"user":    user,
	})
}

func (h *Handler) handleLogout(w http.ResponseWriter, r *http.Request) {
	token, err := h.sessionMgr.ExtractToken(r)
	if err == nil && token != "" {
		_ = h.sessionMgr.Revoke(r.Context(), token, w)
	} else {
		h.sessionMgr.ClearCookie(w)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "Logout berhasil",
	})
}

func (h *Handler) handleMe(w http.ResponseWriter, r *http.Request) {
	user := CurrentUser(r.Context())
	w.Header().Set("Content-Type", "application/json")
	if user == nil {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Belum terautentikasi",
		})
		return
	}
	_ = json.NewEncoder(w).Encode(user)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
