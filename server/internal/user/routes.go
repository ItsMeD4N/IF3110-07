package user

import (
	"encoding/json"
	"net/http"
)

// RegisterRoutes mendaftarkan endpoint CRUD User untuk Platform Admin
func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/users", handleListUsersStub)
	mux.HandleFunc("POST /api/admin/users", handleCreateUserStub)
	mux.HandleFunc("GET /api/admin/users/{id}", handleGetUserStub)
	mux.HandleFunc("PUT /api/admin/users/{id}", handleUpdateUserStub)
	mux.HandleFunc("POST /api/admin/users/{id}/reset-password", handleResetPasswordStub)
	mux.HandleFunc("POST /api/admin/users/{id}/revoke-sessions", handleRevokeSessionsStub)
	mux.HandleFunc("DELETE /api/admin/users/{id}", handleDeleteUserStub)
}

func handleListUsersStub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"users": []interface{}{},
	})
}

func handleCreateUserStub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "User created stub",
	})
}

func handleGetUserStub(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"id": id,
	})
}

func handleUpdateUserStub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "User updated stub",
	})
}

func handleResetPasswordStub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "Password reset stub",
	})
}

func handleRevokeSessionsStub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "Sessions revoked stub",
	})
}

func handleDeleteUserStub(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"message": "User deleted stub",
	})
}
