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

// RegisterRoutes mendaftarkan seluruh endpoint autentikasi ke ServeMux
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	middleware := NewMiddleware(h.userRepo, h.sessionMgr)

	mux.HandleFunc("POST /api/auth/login", h.handleLogin)
	mux.HandleFunc("POST /api/auth/logout", h.handleLogout)
	mux.Handle("GET /api/auth/me", middleware.RequireAuth(http.HandlerFunc(h.handleMe)))
}

// RegisterRoutes mendaftarkan rute dengan dependensi repository & session manager
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
	}

	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Username dan password wajib diisi",
		})
		return
	}

	// 1. Ambil data user dari DB
	user, err := h.userRepo.GetByUsername(r.Context(), req.Username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error": "Username atau password salah",
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Terjadi kesalahan internal server",
		})
		return
	}

	// 2. Cek apakah user aktif
	if !user.IsActive {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Akun Anda telah dinonaktifkan. Silakan hubungi Platform Admin.",
		})
		return
	}

	// 3. Verifikasi password hash menggunakan bcrypt
	if !CheckPassword(user.PasswordHash, req.Password) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Username atau password salah",
		})
		return
	}

	// 4. Buat sesi baru melalui Session Manager
	ip := r.RemoteAddr
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		ip = strings.Split(forwarded, ",")[0]
	}
	userAgent := r.UserAgent()

	_, token, err := h.sessionMgr.CreateSession(r.Context(), user.ID, ip, userAgent)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "Gagal membuat sesi login",
		})
		return
	}

	// 5. Pasang cookie session HttpOnly
	h.sessionMgr.SetCookie(w, token)

	// 6. Response JSON tanpa mengekspos password_hash (json:"-") maupun session token
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
