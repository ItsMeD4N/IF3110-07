package auth

import (
	"encoding/json"
	"net/http"
)

// RegisterRoutes mendaftarkan endpoint autentikasi ke ServeMux
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", handleLoginStub)
	mux.HandleFunc("POST /api/auth/logout", handleLogoutStub)
	mux.HandleFunc("GET /api/auth/me", handleMeStub)
}

func handleLoginStub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "Login endpoint ready to implement",
	})
}

func handleLogoutStub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "Logout endpoint ready to implement",
	})
}

func handleMeStub(w http.ResponseWriter, r *http.Request) {
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
