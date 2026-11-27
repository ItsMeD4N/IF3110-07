package migration

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLegacyGate(t *testing.T) {
	cases := []struct {
		name     string
		module   Module
		wantCode int
		wantBody string
		wantRun  bool
	}{
		{"queue wajib saat legacy dimatikan", Module{Engine: &Engine{}, DisableLegacySync: true}, http.StatusForbidden, "queue_required", false},
		{"approval wajib lebih dulu dicek", Module{Engine: &Engine{RequireApproval: true}, DisableLegacySync: true}, http.StatusForbidden, "approval_required", false},
		{"legacy eksplisit diizinkan", Module{Engine: &Engine{}}, http.StatusOK, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ran := false
			h := tc.module.legacyGate(func(w http.ResponseWriter, r *http.Request) {
				ran = true
				w.WriteHeader(http.StatusOK)
			})
			rec := httptest.NewRecorder()
			h(rec, httptest.NewRequest(http.MethodPost, "/api/databases/1/migrations", nil))
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantCode)
			}
			if ran != tc.wantRun {
				t.Fatalf("handler dijalankan = %v, want %v", ran, tc.wantRun)
			}
			if tc.wantBody != "" && !strings.Contains(rec.Body.String(), tc.wantBody) {
				t.Fatalf("body %q tidak mengandung %q", rec.Body.String(), tc.wantBody)
			}
		})
	}
}
