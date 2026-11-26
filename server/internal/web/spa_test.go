package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupFakeSPADist(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(`<!doctype html><html><body><div id="root"></div></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "assets", "index-abc123.js"), []byte(`console.log("spa");`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSPAHandler_RootAndDeepLinkFallback(t *testing.T) {
	h := SPAHandler(setupFakeSPADist(t))

	for _, p := range []string{"/", "/login", "/databases", "/databases/1/migrations/history", "/approvals", "/admin/users"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, p, nil)
		h.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
			t.Fatalf("GET %s Content-Type = %q, want text/html", p, ct)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
			t.Fatalf("GET %s Cache-Control = %q, want no-store", p, cc)
		}
		if !strings.Contains(rec.Body.String(), `<div id="root"></div>`) {
			t.Fatalf("GET %s tidak mengembalikan index.html SPA", p)
		}
	}
}

func TestSPAHandler_StaticAssetsAndMissingAsset404(t *testing.T) {
	h := SPAHandler(setupFakeSPADist(t))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/index-abc123.js", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET asset = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset Cache-Control = %q, want immutable", rec.Header().Get("Cache-Control"))
	}

	recMissing := httptest.NewRecorder()
	h.ServeHTTP(recMissing, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if recMissing.Code != http.StatusNotFound {
		t.Fatalf("missing asset = %d, want 404", recMissing.Code)
	}
}

func TestSPAHandler_UnknownAPIReturnsJSON404(t *testing.T) {
	h := SPAHandler(setupFakeSPADist(t))

	for _, p := range []string{"/api", "/api/unknown", "/api/databases/999/unknown"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))

		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s = %d, want 404", p, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Fatalf("GET %s Content-Type = %q, want application/json", p, ct)
		}
		if !strings.Contains(rec.Body.String(), `"code":"not_found"`) {
			t.Fatalf("GET %s body = %s, want JSON not_found", p, rec.Body.String())
		}
	}
}

func TestSPAHandler_RejectsMutatingMethods(t *testing.T) {
	h := SPAHandler(setupFakeSPADist(t))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/databases/1/migrations", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST ke route SPA = %d, want 405", rec.Code)
	}
}
