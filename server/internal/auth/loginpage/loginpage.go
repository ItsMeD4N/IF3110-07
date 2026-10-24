package loginpage

import (
	"errors"
	"log"
	"net/http"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

const AfterLoginPath = "/databases"

const (
	msgInvalid  = "Username atau password salah."
	msgInactive = "Akun Anda telah dinonaktifkan. Silakan hubungi Platform Admin."
	msgInternal = "Terjadi kesalahan pada server. Coba lagi."
)

type Handler struct {
	users    auth.UserRepository
	sessions *session.Manager
	views    *web.Views
}

func New(users auth.UserRepository, sessions *session.Manager, views *web.Views) *Handler {
	return &Handler{users: users, sessions: sessions, views: views}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	optional := auth.Authenticate(h.users, h.sessions)
	mux.Handle("GET /login", optional(http.HandlerFunc(h.getLogin)))
	mux.HandleFunc("POST /login", h.postLogin)
	mux.HandleFunc("POST /logout", h.postLogout)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, AfterLoginPath, http.StatusFound)
	})
}

type pageData struct {
	Username string
	Error    string
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, status int, d pageData) {
	h.views.Render(w, r, status, "auth/login.html", "Masuk", d)
}

func (h *Handler) getLogin(w http.ResponseWriter, r *http.Request) {
	if auth.CurrentUser(r.Context()) != nil {
		http.Redirect(w, r, AfterLoginPath, http.StatusFound)
		return
	}
	h.render(w, r, http.StatusOK, pageData{})
}

func (h *Handler) postLogin(w http.ResponseWriter, r *http.Request) {
	username := r.FormValue("username")
	password := r.FormValue("password")

	user, err := auth.VerifyCredentials(r.Context(), h.users, username, password)
	switch {
	case errors.Is(err, auth.ErrInvalidCredentials):
		h.render(w, r, http.StatusUnauthorized, pageData{Username: username, Error: msgInvalid})
		return
	case errors.Is(err, auth.ErrInactiveUser):
		h.render(w, r, http.StatusForbidden, pageData{Username: username, Error: msgInactive})
		return
	case err != nil:
		log.Printf("[Login] gagal memverifikasi kredensial: path=%s err=%v", r.URL.Path, err)
		h.render(w, r, http.StatusInternalServerError, pageData{Username: username, Error: msgInternal})
		return
	}

	if old, err := h.sessions.ExtractToken(r); err == nil {
		_ = h.sessions.Revoke(r.Context(), old, nil)
	}
	_, token, err := h.sessions.CreateSession(r.Context(), user.ID, r.RemoteAddr, r.UserAgent())
	if err != nil {
		log.Printf("[Login] gagal membuat session: user=%s err=%v", user.ID, err)
		h.render(w, r, http.StatusInternalServerError, pageData{Username: username, Error: msgInternal})
		return
	}
	h.sessions.SetCookie(w, token)
	web.Redirect(w, r, AfterLoginPath)
}

func (h *Handler) postLogout(w http.ResponseWriter, r *http.Request) {
	if token, err := h.sessions.ExtractToken(r); err == nil {
		_ = h.sessions.Revoke(r.Context(), token, w)
	} else {
		h.sessions.ClearCookie(w)
	}
	web.SetFlash(w, "success", "Anda telah keluar.")
	web.Redirect(w, r, "/login")
}
