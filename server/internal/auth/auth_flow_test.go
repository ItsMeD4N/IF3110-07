package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func setupTestAuthEnv(t *testing.T) (*http.ServeMux, UserRepository, *session.Manager) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable"
	}

	db, err := testutil.InitDB(connStr)
	if err != nil {
		t.Fatalf("Gagal terhubung ke DB: %v", err)
	}

	userRepo := NewUserRepository(db)
	sessionRepo := session.NewSessionRepository(db)
	sessionMgr := session.NewManager(sessionRepo, false)

	mux := http.NewServeMux()
	RegisterRoutes(mux, userRepo, sessionMgr)

	return mux, userRepo, sessionMgr
}

func TestLogin_Success(t *testing.T) {
	mux, _, _ := setupTestAuthEnv(t)

	body, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": "Admin#Password123",
	})

	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == session.SessionCookieName {
			sessionCookie = c
			break
		}
	}

	if sessionCookie == nil {
		t.Fatalf("Cookie %s wajib ada pada response login", session.SessionCookieName)
	}
	if !sessionCookie.HttpOnly {
		t.Fatalf("Cookie %s harus memiliki atribut HttpOnly = true", session.SessionCookieName)
	}

	bodyStr := rec.Body.String()
	if strings.Contains(bodyStr, "password_hash") {
		t.Fatalf("Response body dilarang mengekspos field password_hash: %s", bodyStr)
	}
	if strings.Contains(bodyStr, sessionCookie.Value) {
		t.Fatalf("Response body dilarang mengekspos session token mentah: %s", bodyStr)
	}
}

func TestLogin_InvalidCredentials(t *testing.T) {
	mux, _, _ := setupTestAuthEnv(t)

	bodyWrongPass, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": "WrongPassword#999",
	})
	req1 := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(bodyWrongPass))
	req1.Header.Set("Content-Type", "application/json")
	rec1 := httptest.NewRecorder()
	mux.ServeHTTP(rec1, req1)

	if rec1.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized, got %d", rec1.Code)
	}
	for _, c := range rec1.Result().Cookies() {
		if c.Name == session.SessionCookieName && c.Value != "" {
			t.Fatalf("Tidak boleh ada cookie sesi yang dibuat saat kredensial salah")
		}
	}

	bodyNonExist, _ := json.Marshal(map[string]string{
		"username": "non_existent_user_999",
		"password": "AnyPassword123!",
	})
	req2 := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(bodyNonExist))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)

	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized, got %d", rec2.Code)
	}
}

func TestLogin_InactiveUser(t *testing.T) {
	mux, userRepo, _ := setupTestAuthEnv(t)
	ctx := context.Background()

	testInactiveUser := "inactive_login_test"
	_ = userRepo.Delete(ctx, testInactiveUser)
	if existing, err := userRepo.GetByUsername(ctx, testInactiveUser); err == nil && existing != nil {
		_ = userRepo.Delete(ctx, existing.ID)
	}

	dummyHash, _ := HashPassword("SecretPass#123")
	u, err := userRepo.Create(ctx, testInactiveUser, dummyHash, false)
	if err != nil {
		t.Fatalf("Gagal membuat test user: %v", err)
	}
	defer userRepo.Delete(ctx, u.ID)

	_ = userRepo.Update(ctx, u.ID, testInactiveUser, false, false)

	body, _ := json.Marshal(map[string]string{
		"username": testInactiveUser,
		"password": "SecretPass#123",
	})
	req := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("User nonaktif harus ditolak dengan 403 Forbidden, got %d", rec.Code)
	}
}

func TestAuth_Lifecycle_MeAndLogout(t *testing.T) {
	mux, _, _ := setupTestAuthEnv(t)

	reqUnauth := httptest.NewRequest("GET", "/api/auth/me", nil)
	recUnauth := httptest.NewRecorder()
	mux.ServeHTTP(recUnauth, reqUnauth)
	if recUnauth.Code != http.StatusUnauthorized {
		t.Fatalf("Akses ke /api/auth/me tanpa login harus 401, got %d", recUnauth.Code)
	}

	loginBody, _ := json.Marshal(map[string]string{
		"username": "admin",
		"password": "Admin#Password123",
	})
	reqLogin := httptest.NewRequest("POST", "/api/auth/login", bytes.NewReader(loginBody))
	reqLogin.Header.Set("Content-Type", "application/json")
	recLogin := httptest.NewRecorder()
	mux.ServeHTTP(recLogin, reqLogin)

	cookies := recLogin.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == session.SessionCookieName {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatalf("Login gagal membuat cookie sesi")
	}

	reqMe := httptest.NewRequest("GET", "/api/auth/me", nil)
	reqMe.AddCookie(sessionCookie)
	recMe := httptest.NewRecorder()
	mux.ServeHTTP(recMe, reqMe)

	if recMe.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK untuk /api/auth/me, got %d. Body: %s", recMe.Code, recMe.Body.String())
	}

	var userResp User
	if err := json.Unmarshal(recMe.Body.Bytes(), &userResp); err != nil {
		t.Fatalf("Gagal decode user response: %v", err)
	}
	if userResp.Username != "admin" || !userResp.IsPlatformAdmin {
		t.Fatalf("Data user me tidak sesuai: %+v", userResp)
	}

	reqLogout := httptest.NewRequest("POST", "/api/auth/logout", nil)
	reqLogout.AddCookie(sessionCookie)
	recLogout := httptest.NewRecorder()
	mux.ServeHTTP(recLogout, reqLogout)

	if recLogout.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK untuk logout, got %d", recLogout.Code)
	}

	var clearCookie *http.Cookie
	for _, c := range recLogout.Result().Cookies() {
		if c.Name == session.SessionCookieName {
			clearCookie = c
			break
		}
	}
	if clearCookie == nil || clearCookie.MaxAge != -1 {
		t.Fatalf("Logout harus mengirim cookie penghapus dengan MaxAge = -1")
	}

	reqMeAfter := httptest.NewRequest("GET", "/api/auth/me", nil)
	reqMeAfter.AddCookie(sessionCookie)
	recMeAfter := httptest.NewRecorder()
	mux.ServeHTTP(recMeAfter, reqMeAfter)

	if recMeAfter.Code != http.StatusUnauthorized {
		t.Fatalf("Expected 401 Unauthorized setelah logout, got %d", recMeAfter.Code)
	}
}
