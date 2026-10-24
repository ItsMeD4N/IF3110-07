package user

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func setupTestUserEnv(t *testing.T) (*http.ServeMux, auth.UserRepository, *session.Manager) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable"
	}

	db, err := testutil.InitDB(connStr)
	if err != nil {
		t.Fatalf("Gagal terhubung ke DB: %v", err)
	}

	userRepo := auth.NewUserRepository(db)
	sessionRepo := session.NewSessionRepository(db)
	sessionMgr := session.NewManager(sessionRepo, false)

	mux := http.NewServeMux()
	RegisterRoutes(mux, userRepo, sessionMgr)

	return mux, userRepo, sessionMgr
}

// helper membuat cookie login admin
func createAdminSessionCookie(t *testing.T, sessionMgr *session.Manager) *http.Cookie {
	adminID := "a0000000-0000-0000-0000-000000000001"
	_, token, err := sessionMgr.CreateSession(context.Background(), adminID, "127.0.0.1", "Admin-Test")
	if err != nil {
		t.Fatalf("Gagal membuat sesi admin: %v", err)
	}
	return &http.Cookie{
		Name:     session.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
	}
}

// helper membuat cookie login user biasa
func createRegularUserSessionCookie(t *testing.T, userRepo auth.UserRepository, sessionMgr *session.Manager, username string) (*http.Cookie, string) {
	ctx := context.Background()
	_ = userRepo.Delete(ctx, username)
	if existing, err := userRepo.GetByUsername(ctx, username); err == nil && existing != nil {
		_ = userRepo.Delete(ctx, existing.ID)
	}

	dummyHash, _ := auth.HashPassword("UserPassword#123")
	u, err := userRepo.Create(ctx, username, dummyHash, false)
	if err != nil {
		t.Fatalf("Gagal membuat user biasa: %v", err)
	}

	_, token, err := sessionMgr.CreateSession(ctx, u.ID, "127.0.0.1", "User-Test")
	if err != nil {
		t.Fatalf("Gagal membuat sesi user: %v", err)
	}

	return &http.Cookie{
		Name:     session.SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
	}, u.ID
}

func TestAdminUsers_Authorization(t *testing.T) {
	mux, userRepo, sessionMgr := setupTestUserEnv(t)

	// 1. Tanpa Login -> 401 Unauthorized
	reqUnauth := httptest.NewRequest("GET", "/api/admin/users", nil)
	recUnauth := httptest.NewRecorder()
	mux.ServeHTTP(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized tanpa login, got %d", recUnauth.Code)
	}

	// 2. Login sebagai User Biasa (bukan Platform Admin) -> 403 Forbidden
	userCookie, regularUserID := createRegularUserSessionCookie(t, userRepo, sessionMgr, "regular_test_user")
	defer userRepo.Delete(context.Background(), regularUserID)

	reqForbidden := httptest.NewRequest("GET", "/api/admin/users", nil)
	reqForbidden.AddCookie(userCookie)
	recForbidden := httptest.NewRecorder()
	mux.ServeHTTP(recForbidden, reqForbidden)

	if recForbidden.Code != http.StatusForbidden {
		t.Fatalf("Expected 403 Forbidden untuk user biasa, got %d", recForbidden.Code)
	}

	// 3. Login sebagai Platform Admin -> 200 OK
	adminCookie := createAdminSessionCookie(t, sessionMgr)
	reqAdmin := httptest.NewRequest("GET", "/api/admin/users", nil)
	reqAdmin.AddCookie(adminCookie)
	recAdmin := httptest.NewRecorder()
	mux.ServeHTTP(recAdmin, reqAdmin)

	if recAdmin.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK untuk Platform Admin, got %d", recAdmin.Code)
	}
}

func TestAdminUsers_CRUDLifecycle(t *testing.T) {
	mux, userRepo, sessionMgr := setupTestUserEnv(t)
	adminCookie := createAdminSessionCookie(t, sessionMgr)
	ctx := context.Background()

	testUsername := "crud_managed_user"
	if existing, err := userRepo.GetByUsername(ctx, testUsername); err == nil && existing != nil {
		_ = userRepo.Delete(ctx, existing.ID)
	}

	// 1. CREATE USER (POST /api/admin/users)
	createBody, _ := json.Marshal(map[string]interface{}{
		"username":          testUsername,
		"password":          "StrongSecret#123",
		"is_platform_admin": false,
	})
	reqCreate := httptest.NewRequest("POST", "/api/admin/users", bytes.NewReader(createBody))
	reqCreate.Header.Set("Content-Type", "application/json")
	reqCreate.AddCookie(adminCookie)
	recCreate := httptest.NewRecorder()
	mux.ServeHTTP(recCreate, reqCreate)

	if recCreate.Code != http.StatusCreated {
		t.Fatalf("Expected 201 Created, got %d. Body: %s", recCreate.Code, recCreate.Body.String())
	}

	var createResp struct {
		User auth.User `json:"user"`
	}
	_ = json.Unmarshal(recCreate.Body.Bytes(), &createResp)
	createdID := createResp.User.ID
	if createdID == "" {
		t.Fatalf("User ID tidak boleh kosong pada response create")
	}
	defer userRepo.Delete(ctx, createdID)

	// Duplicate Create -> 409 Conflict
	reqDup := httptest.NewRequest("POST", "/api/admin/users", bytes.NewReader(createBody))
	reqDup.Header.Set("Content-Type", "application/json")
	reqDup.AddCookie(adminCookie)
	recDup := httptest.NewRecorder()
	mux.ServeHTTP(recDup, reqDup)
	if recDup.Code != http.StatusConflict {
		t.Fatalf("Expected 409 Conflict untuk duplicate user, got %d", recDup.Code)
	}

	// 2. GET USER DETAIL (GET /api/admin/users/{id})
	reqGet := httptest.NewRequest("GET", "/api/admin/users/"+createdID, nil)
	reqGet.AddCookie(adminCookie)
	recGet := httptest.NewRecorder()
	mux.ServeHTTP(recGet, reqGet)
	if recGet.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK untuk get user, got %d", recGet.Code)
	}

	// 3. UPDATE USER (PUT /api/admin/users/{id})
	updateBody, _ := json.Marshal(map[string]interface{}{
		"username":          testUsername + "_renamed",
		"is_platform_admin": true,
		"is_active":         true,
	})
	reqUpdate := httptest.NewRequest("PUT", "/api/admin/users/"+createdID, bytes.NewReader(updateBody))
	reqUpdate.Header.Set("Content-Type", "application/json")
	reqUpdate.AddCookie(adminCookie)
	recUpdate := httptest.NewRecorder()
	mux.ServeHTTP(recUpdate, reqUpdate)

	if recUpdate.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK untuk update user, got %d. Body: %s", recUpdate.Code, recUpdate.Body.String())
	}

	// 4. RESET PASSWORD (POST /api/admin/users/{id}/reset-password)
	// Buat dummy active session untuk user ini terlebih dahulu
	_, userToken, _ := sessionMgr.CreateSession(ctx, createdID, "127.0.0.1", "Before-Reset")
	resetBody, _ := json.Marshal(map[string]string{
		"new_password": "NewSecretPassword#456",
	})
	reqReset := httptest.NewRequest("POST", "/api/admin/users/"+createdID+"/reset-password", bytes.NewReader(resetBody))
	reqReset.Header.Set("Content-Type", "application/json")
	reqReset.AddCookie(adminCookie)
	recReset := httptest.NewRecorder()
	mux.ServeHTTP(recReset, reqReset)

	if recReset.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK untuk reset password, got %d", recReset.Code)
	}

	// Verifikasi: Sesi lama user harus langsung revoked setelah reset password
	_, err := sessionMgr.ValidateSession(ctx, userToken, nil)
	if err != session.ErrSessionRevoked {
		t.Fatalf("Sesi lama user harus revoked setelah password direset, got %v", err)
	}

	// 5. REVOKE SESSIONS (POST /api/admin/users/{id}/revoke-sessions)
	_, userToken2, _ := sessionMgr.CreateSession(ctx, createdID, "127.0.0.1", "Before-Revoke")
	reqRevoke := httptest.NewRequest("POST", "/api/admin/users/"+createdID+"/revoke-sessions", nil)
	reqRevoke.AddCookie(adminCookie)
	recRevoke := httptest.NewRecorder()
	mux.ServeHTTP(recRevoke, reqRevoke)

	if recRevoke.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK untuk revoke-sessions, got %d", recRevoke.Code)
	}
	_, err = sessionMgr.ValidateSession(ctx, userToken2, nil)
	if err != session.ErrSessionRevoked {
		t.Fatalf("Sesi harus revoked setelah force logout, got %v", err)
	}

	// 6. DELETE USER (DELETE /api/admin/users/{id})
	reqDelete := httptest.NewRequest("DELETE", "/api/admin/users/"+createdID, nil)
	reqDelete.AddCookie(adminCookie)
	recDelete := httptest.NewRecorder()
	mux.ServeHTTP(recDelete, reqDelete)

	if recDelete.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK untuk delete user, got %d", recDelete.Code)
	}

	// Pastikan user sudah tidak ditemukan
	_, err = userRepo.GetByID(ctx, createdID)
	if err != auth.ErrUserNotFound {
		t.Fatalf("User harusnya sudah dihapus dari DB, got %v", err)
	}
}

func TestAdminUsers_SelfActionsPrevented(t *testing.T) {
	mux, _, sessionMgr := setupTestUserEnv(t)
	adminCookie := createAdminSessionCookie(t, sessionMgr)
	adminID := "a0000000-0000-0000-0000-000000000001"

	// 1. Admin mencoba menonaktifkan akunnya sendiri -> harus ditolak 400 Bad Request
	deactivateSelfBody, _ := json.Marshal(map[string]interface{}{
		"username":          "admin",
		"is_platform_admin": true,
		"is_active":         false,
	})
	reqDeact := httptest.NewRequest("PUT", "/api/admin/users/"+adminID, bytes.NewReader(deactivateSelfBody))
	reqDeact.Header.Set("Content-Type", "application/json")
	reqDeact.AddCookie(adminCookie)
	recDeact := httptest.NewRecorder()
	mux.ServeHTTP(recDeact, reqDeact)

	if recDeact.Code != http.StatusBadRequest {
		t.Fatalf("Admin menonaktifkan dirinya sendiri harus ditolak 400 Bad Request, got %d", recDeact.Code)
	}

	// 2. Admin mencoba menghapus akunnya sendiri -> harus ditolak 400 Bad Request
	reqDel := httptest.NewRequest("DELETE", "/api/admin/users/"+adminID, nil)
	reqDel.AddCookie(adminCookie)
	recDel := httptest.NewRecorder()
	mux.ServeHTTP(recDel, reqDel)

	if recDel.Code != http.StatusBadRequest {
		t.Fatalf("Admin menghapus akunnya sendiri harus ditolak 400 Bad Request, got %d", recDel.Code)
	}
}
