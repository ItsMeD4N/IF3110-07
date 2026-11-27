package app_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/app"
	"github.com/ItsMeD4N/IF3110-07/server/internal/secretbox"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func TestApp_APIOnlyNeverServesLegacyTemplates(t *testing.T) {
	db := testutil.OpenDB(t)
	a, err := app.New(db, app.Options{
		AppEnv:        "test",
		APIOnly:       true,
		DisableWorker: true,
		EncryptionKey: secretbox.DevKey(),
	})
	if err != nil {
		t.Fatalf("app.New(APIOnly=true) gagal: %v", err)
	}
	t.Cleanup(a.Close)

	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/health = %d", rec.Code)
	}

	for _, route := range []string{"/", "/login", "/admin/users", "/databases/1/migrations"} {
		rec := httptest.NewRecorder()
		a.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, route, nil))
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("GET %s = %d, want 503", route, rec.Code)
		}
		body := rec.Body.String()
		if strings.Contains(body, "<form") || strings.Contains(body, "<html") {
			t.Fatalf("GET %s tidak boleh merender Go template: %s", route, body)
		}
		if !strings.Contains(body, "npm run build") {
			t.Fatalf("GET %s harus memberi petunjuk build SPA", route)
		}
	}

	rec404 := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec404, httptest.NewRequest(http.MethodGet, "/api/tidak-ada", nil))
	if rec404.Code != http.StatusNotFound {
		t.Fatalf("GET /api/tidak-ada = %d, want 404", rec404.Code)
	}
}
