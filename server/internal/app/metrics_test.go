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

func newSPAApp(t *testing.T, opts app.Options) *app.App {
	t.Helper()
	db := testutil.OpenDB(t)
	distDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(distDir, "index.html"), []byte(`<!doctype html><div id="root"></div>`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts.AppEnv = "test"
	opts.SPADir = distDir
	opts.ServeSPA = true
	opts.DisableWorker = true
	opts.EncryptionKey = secretbox.DevKey()
	a, err := app.New(db, opts)
	if err != nil {
		t.Fatalf("app.New gagal: %v", err)
	}
	t.Cleanup(a.Close)
	return a
}

func TestApp_MetricsEndpointExposesPrometheusFormat(t *testing.T) {
	a := newSPAApp(t, app.Options{EnableMetrics: true})

	a.Handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/health", nil))

	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /metrics = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("content-type = %q", ct)
	}
	body := rec.Body.String()
	for _, want := range []string{
		`if3110_migration_jobs{status="pending"}`,
		`if3110_migration_jobs{status="running"}`,
		`if3110_migration_jobs{status="completed"}`,
		`if3110_migration_jobs{status="failed"}`,
		`if3110_migration_jobs_scrape_ok 1`,
		`if3110_db_pool_active_connections{pool="platform"}`,
		`if3110_db_pool_idle_connections{pool="platform"}`,
		`if3110_db_pool_wait_duration_seconds_total{pool="target"}`,
		`if3110_http_requests_total{method="GET",route="health",code="2xx"}`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("/metrics tidak mengandung %q", want)
		}
	}
	for _, forbidden := range []string{"app_session_id", "password", "SELECT "} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("/metrics tidak boleh mengandung %q", forbidden)
		}
	}
}

func TestApp_MetricsEndpointDisabled(t *testing.T) {
	a := newSPAApp(t, app.Options{EnableMetrics: false})
	rec := httptest.NewRecorder()
	a.Handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if strings.Contains(rec.Body.String(), "if3110_migration_jobs") {
		t.Fatal("/metrics seharusnya tidak tersedia saat EnableMetrics=false")
	}
}
