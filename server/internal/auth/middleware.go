package auth

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
)

type Middleware struct {
	userRepo   UserRepository
	sessionMgr *session.Manager
}

func NewMiddleware(userRepo UserRepository, sessionMgr *session.Manager) *Middleware {
	return &Middleware{
		userRepo:   userRepo,
		sessionMgr: sessionMgr,
	}
}

// RequireAuth memastikan request memiliki sesi valid di backend.
// Jika valid: injeksi objek User ke context via WithUser.
// Jika tidak valid: tolak request (401 JSON untuk request API, atau redirect /login untuk browser).
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := m.sessionMgr.ExtractToken(r)
		if err != nil {
			m.unauthorized(w, r, "Sesi tidak ditemukan atau belum login")
			return
		}

		sess, err := m.sessionMgr.ValidateSession(r.Context(), token, w)
		if err != nil {
			m.sessionMgr.ClearCookie(w)
			m.unauthorized(w, r, "Sesi telah berakhir atau tidak valid")
			return
		}

		user, err := m.userRepo.GetByID(r.Context(), sess.UserID)
		if err != nil || !user.IsActive {
			_ = m.sessionMgr.Revoke(r.Context(), token, w)
			m.unauthorized(w, r, "Akun tidak ditemukan atau telah dinonaktifkan")
			return
		}

		ctx := WithUser(r.Context(), user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Authenticate mencoba membaca sesi dari cookie jika ada.
// Jika sesi valid, inject User ke context. Jika tidak ada / invalid, request tetap diteruskan tanpa user di context.
func (m *Middleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := m.sessionMgr.ExtractToken(r)
		if err == nil && token != "" {
			sess, err := m.sessionMgr.ValidateSession(r.Context(), token, w)
			if err == nil && sess != nil {
				user, err := m.userRepo.GetByID(r.Context(), sess.UserID)
				if err == nil && user != nil && user.IsActive {
					ctx := WithUser(r.Context(), user)
					r = r.WithContext(ctx)
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (m *Middleware) unauthorized(w http.ResponseWriter, r *http.Request, msg string) {
	if isAPIRequest(r) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": msg,
		})
		return
	}

	http.Redirect(w, r, "/login", http.StatusFound)
}

func isAPIRequest(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		return true
	}
	accept := r.Header.Get("Accept")
	return strings.Contains(accept, "application/json")
}

// RequireAuth helper fungsional untuk memudahkan penggunaan sebagai middleware standar
func RequireAuth(userRepo UserRepository, sessionMgr *session.Manager) func(http.Handler) http.Handler {
	m := NewMiddleware(userRepo, sessionMgr)
	return m.RequireAuth
}

// Authenticate helper fungsional untuk memudahkan penggunaan sebagai middleware standar
func Authenticate(userRepo UserRepository, sessionMgr *session.Manager) func(http.Handler) http.Handler {
	m := NewMiddleware(userRepo, sessionMgr)
	return m.Authenticate
}
