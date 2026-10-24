package authz

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
)

func withUser(u *auth.User, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u != nil {
			r = r.WithContext(auth.WithUser(r.Context(), u))
		}
		next.ServeHTTP(w, r)
	})
}

var (
	adminUser    = &auth.User{ID: "admin", Username: "admin", IsPlatformAdmin: true, IsActive: true}
	regularUser  = &auth.User{ID: "user", Username: "user", IsActive: true}
	inactiveUser = &auth.User{ID: "admin", Username: "admin", IsPlatformAdmin: true, IsActive: false}
)

func okHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("respons penolakan bukan JSON: %q", rec.Body.String())
	}
	return body.Code
}

func TestRequire_PlatformPermission(t *testing.T) {
	mw := NewMiddleware(New(newFakeSource()))
	cases := []struct {
		name     string
		user     *auth.User
		wantCode int
		wantErr  string
	}{
		{"tanpa login", nil, http.StatusUnauthorized, "unauthenticated"},
		{"user nonaktif diperlakukan belum login", inactiveUser, http.StatusUnauthorized, "unauthenticated"},
		{"user biasa", regularUser, http.StatusForbidden, "forbidden"},
		{"platform admin", adminUser, http.StatusOK, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := withUser(c.user, mw.Require(PermManageConnections, http.HandlerFunc(okHandler)))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/connections", nil))
			if rec.Code != c.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, c.wantCode)
			}
			if c.wantErr != "" && errorCode(t, rec) != c.wantErr {
				t.Fatalf("code = %q, want %q", errorCode(t, rec), c.wantErr)
			}
		})
	}
}

func TestRequirePlatform_WithoutAccessSource(t *testing.T) {
	for _, c := range []struct {
		user *auth.User
		want int
	}{{nil, 401}, {regularUser, 403}, {adminUser, 200}} {
		h := withUser(c.user, RequirePlatform(PermManageUsers, http.HandlerFunc(okHandler)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/users", nil))
		if rec.Code != c.want {
			t.Fatalf("user %v: status = %d, want %d", c.user, rec.Code, c.want)
		}
	}
}

func dbRoute(mw *Middleware, perm Permission, u *auth.User, seen *DBAccess) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /db/{dbID}", mw.RequireDB(perm, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := DBAccessFrom(r.Context())
		if !ok {
			http.Error(w, "DBAccess tidak ada di context", http.StatusInternalServerError)
			return
		}
		*seen = a
		w.WriteHeader(http.StatusOK)
	})))
	return withUser(u, mux)
}

func TestRequireDB(t *testing.T) {
	src := newFakeSource(1, 2).
		grant(regularUser.ID, 1, LevelViewOnly).
		grant(regularUser.ID, 2, LevelViewOnly).
		grant(regularUser.ID, 2, LevelViewEdit)
	mw := NewMiddleware(New(src))

	cases := []struct {
		name     string
		user     *auth.User
		perm     Permission
		path     string
		want     int
		wantCode string
		level    AccessLevel
	}{
		{"tanpa login", nil, PermDBViewSchema, "/db/1", 401, "unauthenticated", 0},
		{"id bukan angka", regularUser, PermDBViewSchema, "/db/abc", 404, "not_found", 0},
		{"id nol", regularUser, PermDBViewSchema, "/db/0", 404, "not_found", 0},
		{"database tidak ada", regularUser, PermDBViewSchema, "/db/99", 404, "not_found", 0},
		{"admin tanpa group", adminUser, PermDBViewSchema, "/db/1", 404, "not_found", 0},
		{"view only boleh lihat", regularUser, PermDBViewSchema, "/db/1", 200, "", LevelViewOnly},
		{"view only tidak boleh eksekusi", regularUser, PermDBExecuteMigration, "/db/1", 403, "forbidden", 0},
		{"multi-group: view edit menang", regularUser, PermDBExecuteMigration, "/db/2", 200, "", LevelViewEdit},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var seen DBAccess
			rec := httptest.NewRecorder()
			dbRoute(mw, c.perm, c.user, &seen).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
			if rec.Code != c.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, c.want, rec.Body.String())
			}
			if c.wantCode != "" && errorCode(t, rec) != c.wantCode {
				t.Fatalf("code = %q, want %q", errorCode(t, rec), c.wantCode)
			}
			if c.want == 200 && seen.Level != c.level {
				t.Fatalf("DBAccess.Level = %v, want %v", seen.Level, c.level)
			}
		})
	}
}

func TestRequireDB_NotFoundAndNoAccessLookTheSame(t *testing.T) {
	mw := NewMiddleware(New(newFakeSource(1)))
	var seen DBAccess
	bodies := map[string]string{}
	for _, path := range []string{"/db/1", "/db/99"} {
		rec := httptest.NewRecorder()
		dbRoute(mw, PermDBViewSchema, regularUser, &seen).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		bodies[path] = fmt.Sprintf("%d %s", rec.Code, rec.Body.String())
	}
	if bodies["/db/1"] != bodies["/db/99"] {
		t.Fatalf("respons berbeda:\n tanpa akses: %s\n tidak ada : %s", bodies["/db/1"], bodies["/db/99"])
	}
}

func TestRequireDB_SourceErrorIs500(t *testing.T) {
	src := newFakeSource(1)
	src.err = errors.New("db down")
	var seen DBAccess
	rec := httptest.NewRecorder()
	dbRoute(NewMiddleware(New(src)), PermDBViewSchema, regularUser, &seen).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/db/1", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestMiddleware_MisuseFailsFast(t *testing.T) {
	mw := NewMiddleware(New(newFakeSource()))
	assertPanics(t, "Require dengan permission database", func() { mw.Require(PermDBViewSchema, http.HandlerFunc(okHandler)) })
	assertPanics(t, "RequireDB dengan permission platform", func() { mw.RequireDB(PermManageGroups, http.HandlerFunc(okHandler)) })
}

func assertPanics(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("%s: harus panic", name)
		}
	}()
	f()
}

func TestWithDeniedHandler(t *testing.T) {
	var got error
	mw := NewMiddleware(New(newFakeSource())).WithDeniedHandler(func(w http.ResponseWriter, _ *http.Request, err error) {
		got = err
		w.WriteHeader(http.StatusTeapot)
	})
	rec := httptest.NewRecorder()
	withUser(regularUser, mw.Require(PermManageGroups, http.HandlerFunc(okHandler))).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot || !errors.Is(got, ErrForbidden) {
		t.Fatalf("denied handler kustom tidak dipakai: status=%d err=%v", rec.Code, got)
	}
}

func TestRequireAuth(t *testing.T) {
	mw := NewMiddleware(New(newFakeSource()))
	for _, c := range []struct {
		user *auth.User
		want int
	}{{nil, 401}, {inactiveUser, 401}, {regularUser, 200}} {
		rec := httptest.NewRecorder()
		withUser(c.user, mw.RequireAuth(http.HandlerFunc(okHandler))).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		if rec.Code != c.want {
			t.Fatalf("user %v: status = %d, want %d", c.user, rec.Code, c.want)
		}
	}
}

func TestStatusFor(t *testing.T) {
	cases := map[error]int{
		nil:                                     200,
		ErrUnauthenticated:                      401,
		ErrForbidden:                            403,
		ErrNotFound:                             404,
		fmt.Errorf("wrap: %w", ErrForbidden):    403,
		fmt.Errorf("wrap: %w", ErrNotFound):     404,
		errors.New("koneksi platform DB putus"): 500,
	}
	for err, want := range cases {
		if got := StatusFor(err); got != want {
			t.Errorf("StatusFor(%v) = %d, want %d", err, got, want)
		}
	}
}

func TestDBAccess_Can(t *testing.T) {
	a := DBAccess{DatabaseID: 1, Level: LevelViewOnly}
	if !a.Can(PermDBViewData) || a.Can(PermDBExecuteMigration) {
		t.Fatal("DBAccess.Can view only salah")
	}
	if _, ok := DBAccessFrom(httptest.NewRequest(http.MethodGet, "/", nil).Context()); ok {
		t.Fatal("DBAccessFrom tanpa RequireDB harus (_, false)")
	}
}
