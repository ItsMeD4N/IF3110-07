package app_test

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/app"
	"github.com/ItsMeD4N/IF3110-07/server/internal/secretbox"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func TestApp_ServeSPAMode(t *testing.T) {
	db := testutil.OpenDB(t)

	distDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte(`<!doctype html><html><body><div id="root"></div></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}

	a, err := app.New(db, app.Options{
		AppEnv:        "test",
		SPADir:        distDir,
		ServeSPA:      true,
		DisableWorker: true,
		EncryptionKey: secretbox.DevKey(),
	})
	if err != nil {
		t.Fatalf("app.New(ServeSPA=true) gagal: %v", err)
	}
	t.Cleanup(a.Close)

	// 1. API health tetap jalan
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"healthy"`) {
		t.Fatalf("GET /api/health = %d %s", rec.Code, rec.Body.String())
	}

	// 2. Deep-link SPA route mengembalikan index.html + cookie CSRF
	for _, route := range []string{"/", "/login", "/databases/1/migrations/history", "/approvals", "/admin/users"} {
		rec := httptest.NewRecorder()
		a.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", route, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), `<div id="root"></div>`) {
			t.Fatalf("GET %s tidak mengembalikan SPA index.html", route)
		}
	}

	// 3. Unknown /api/* route tetap mengembalikan 404 JSON, bukan HTML
	rec404 := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec404, httptest.NewRequest(http.MethodGet, "/api/tidak-ada", nil))
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("GET /api/tidak-ada = %d, want 404", rec404.Code)
	}
	if ct := rec404.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("GET /api/tidak-ada Content-Type = %q, want application/json", ct)
	}
}
