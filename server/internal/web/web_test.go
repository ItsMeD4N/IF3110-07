package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/csrf"
	"github.com/ItsMeD4N/IF3110-07/server/internal/database"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/view"
)

func findTemplatesDir() string {
	candidates := []string{
		"../../../client/templates",
		"../../client/templates",
		"client/templates",
		"../client/templates",
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	return candidates[0]
}

func setupTestWebEnv(t *testing.T) (http.Handler, auth.UserRepository, *session.Manager) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://postgres:postgres@127.0.0.1:5433/platform_db?sslmode=disable"
	}


	db, err := database.InitDB(connStr)
	if err != nil {
		t.Fatalf("Gagal terhubung ke DB: %v", err)
	}

	userRepo := auth.NewUserRepository(db)
	sessionRepo := session.NewSessionRepository(db)
	sessionMgr := session.NewManager(sessionRepo, false)
	viewEngine := view.NewEngine(findTemplatesDir(), true)

	mux := http.NewServeMux()
	handler := NewHandler(userRepo, sessionMgr, viewEngine)
	handler.RegisterRoutes(mux)

	// Pasang CSRF middleware
	wrapped := csrf.Middleware(false)(mux)

	return wrapped, userRepo, sessionMgr
}

func getCSRFTokenAndCookie(handler http.Handler) (string, *http.Cookie) {
	req := httptest.NewRequest("GET", "/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	for _, c := range rec.Result().Cookies() {
		if c.Name == csrf.CSRFCookieName {
			return c.Value, c
		}
	}

	return "", nil
}

func TestWeb_LoginPage_Render(t *testing.T) {
	handler, _, _ := setupTestWebEnv(t)

	req := httptest.NewRequest("GET", "/login", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Masuk") {
		t.Errorf("Expected body to contain 'Masuk', got: %s", body)
	}

	if !strings.Contains(body, "name=\"csrf_token\"") {
		t.Errorf("Expected body to contain csrf_token hidden input")
	}
}

func TestWeb_LoginForm_SuccessAndRedirect(t *testing.T) {
	handler, _, _ := setupTestWebEnv(t)
	token, csrfCookie := getCSRFTokenAndCookie(handler)
	if token == "" {
		t.Fatalf("Failed to retrieve CSRF token")
	}

	form := url.Values{}
	form.Set("username", "admin")
	form.Set("password", "Admin#Password123")
	form.Set("csrf_token", token)

	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("Expected 302 Found redirect, got %d. Body: %s", rec.Code, rec.Body.String())
	}

	location := rec.Header().Get("Location")
	if location != "/admin/users" {
		t.Errorf("Expected redirect to /admin/users, got: %s", location)
	}

	// Pastikan cookie sesi diset
	var sessionCookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.SessionCookieName && c.Value != "" {
			sessionCookie = c
			break
		}
	}

	if sessionCookie == nil {
		t.Fatalf("Expected session cookie %s to be set", session.SessionCookieName)
	}
	if !sessionCookie.HttpOnly {
		t.Errorf("Expected session cookie to have HttpOnly = true")
	}
}

func TestWeb_LoginForm_InvalidCredentials(t *testing.T) {
	handler, _, _ := setupTestWebEnv(t)
	token, csrfCookie := getCSRFTokenAndCookie(handler)

	form := url.Values{}
	form.Set("username", "admin")
	form.Set("password", "WrongPassword!")
	form.Set("csrf_token", token)

	req := httptest.NewRequest("POST", "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("Expected 302 Found, got %d", rec.Code)
	}

	location := rec.Header().Get("Location")
	if location != "/login" {
		t.Errorf("Expected redirect back to /login, got %s", location)
	}

	// Pastikan tidak ada cookie sesi yang diset
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.SessionCookieName && c.Value != "" {
			t.Errorf("Did not expect session cookie to be set on invalid credentials")
		}
	}
}

func TestWeb_AdminUsersPage_ProtectionAndRender(t *testing.T) {
	handler, userRepo, sessionMgr := setupTestWebEnv(t)

	// 1. Tanpa autentikasi -> Harus redirect ke /login
	req := httptest.NewRequest("GET", "/admin/users", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("Expected 302 redirect for unauthenticated request, got %d", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("Expected redirect to /login, got %s", rec.Header().Get("Location"))
	}

	// 2. Dengan autentikasi Admin
	admin, err := userRepo.GetByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatalf("Failed to fetch admin user: %v", err)
	}

	_, opaqueToken, err := sessionMgr.CreateSession(context.Background(), admin.ID, "127.0.0.1", "test-agent")
	if err != nil {
		t.Fatalf("Failed to create session: %v", err)
	}

	reqAuth := httptest.NewRequest("GET", "/admin/users", nil)
	reqAuth.AddCookie(&http.Cookie{
		Name:  session.SessionCookieName,
		Value: opaqueToken,
	})
	recAuth := httptest.NewRecorder()
	handler.ServeHTTP(recAuth, reqAuth)

	if recAuth.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK for authenticated admin, got %d. Body: %s", recAuth.Code, recAuth.Body.String())
	}

	body := recAuth.Body.String()
	if !strings.Contains(body, "Manajemen Pengguna") {
		t.Errorf("Expected body to contain 'Manajemen Pengguna'")
	}
	if !strings.Contains(body, "admin") {
		t.Errorf("Expected body to contain username 'admin'")
	}
}

func TestWeb_LogoutForm(t *testing.T) {
	handler, userRepo, sessionMgr := setupTestWebEnv(t)
	token, csrfCookie := getCSRFTokenAndCookie(handler)

	admin, _ := userRepo.GetByUsername(context.Background(), "admin")
	_, opaqueToken, _ := sessionMgr.CreateSession(context.Background(), admin.ID, "127.0.0.1", "test-agent")

	form := url.Values{}
	form.Set("csrf_token", token)

	req := httptest.NewRequest("POST", "/logout", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(csrfCookie)
	req.AddCookie(&http.Cookie{
		Name:  session.SessionCookieName,
		Value: opaqueToken,
	})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("Expected 302 Found on logout, got %d", rec.Code)
	}

	if rec.Header().Get("Location") != "/login" {
		t.Errorf("Expected redirect to /login, got: %s", rec.Header().Get("Location"))
	}

	// Pastikan cookie sesi dibersihkan (MaxAge < 0)
	var cleared bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.SessionCookieName && (c.MaxAge < 0 || c.Value == "") {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Errorf("Expected session cookie to be cleared on logout")
	}
}
