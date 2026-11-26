package admin

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/secretbox"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

const clientDir = "../../../client"

var ctx = context.Background()

type privEvent struct {
	users  []string
	reason string
}

type testEnv struct {
	t        *testing.T
	db       *sql.DB
	mod      *Module
	mux      *http.ServeMux
	users    auth.UserRepository
	sessions *session.Manager

	mu      sync.Mutex
	cookies map[string]*http.Cookie
	events  []privEvent
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	db := testutil.OpenDB(t)
	users := auth.NewUserRepository(db)
	sessions := session.NewManager(session.NewSessionRepository(db), false)
	views, err := web.New(clientDir, false)
	if err != nil {
		t.Fatalf("memuat template: %v", err)
	}
	mod, err := New(db, users, sessions, Options{EncryptionKey: secretbox.DevKey(), Views: views})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mod.Close)

	e := &testEnv{t: t, db: db, mod: mod, users: users, sessions: sessions, cookies: map[string]*http.Cookie{}}
	mod.OnPrivilegeChange = func(_ context.Context, ids []string, reason string) {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.events = append(e.events, privEvent{users: append([]string{}, ids...), reason: reason})
	}

	e.mux = http.NewServeMux()
	mod.RegisterRoutes(e.mux)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, _ := authz.DBAccessFrom(r.Context())
		fmt.Fprintf(w, `{"level":%q}`, a.Level)
	})
	e.mux.Handle("GET /test/databases/{dbID}/migrations", mod.requireAuth(mod.MW.RequireDB(authz.PermDBViewMigrations, ok)))
	e.mux.Handle("POST /test/databases/{dbID}/migrations", mod.requireAuth(mod.MW.RequireDB(authz.PermDBExecuteMigration, ok)))
	return e
}

var (
	hashOnce sync.Once
	pwHash   string
)

func passwordHash(t *testing.T) string {
	hashOnce.Do(func() {
		h, err := auth.HashPassword("Passw0rd!Passw0rd")
		if err != nil {
			t.Fatal(err)
		}
		pwHash = h
	})
	return pwHash
}

func (e *testEnv) newUser(isAdmin bool) *auth.User {
	e.t.Helper()
	u, err := e.users.Create(ctx, testutil.Unique("user"), passwordHash(e.t), isAdmin)
	if err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() { _ = e.users.Delete(context.Background(), u.ID) })
	return u
}

func (e *testEnv) deactivate(u *auth.User) {
	e.t.Helper()
	if err := e.users.Update(ctx, u.ID, u.Username, u.IsPlatformAdmin, false); err != nil {
		e.t.Fatal(err)
	}
	u.IsActive = false
}

func (e *testEnv) cookie(u *auth.User) *http.Cookie {
	e.t.Helper()
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.cookies[u.ID]; ok {
		return c
	}
	_, token, err := e.sessions.CreateSession(ctx, u.ID, "127.0.0.1", "go-test")
	if err != nil {
		e.t.Fatal(err)
	}
	c := &http.Cookie{Name: session.SessionCookieName, Value: token}
	e.cookies[u.ID] = c
	return c
}

func (e *testEnv) serve(req *http.Request, as *auth.User) *httptest.ResponseRecorder {
	if as != nil {
		req.AddCookie(e.cookie(as))
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func (e *testEnv) api(as *auth.User, method, path string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, path, r)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	return e.serve(req, as)
}

func (e *testEnv) page(as *auth.User, method, path string, form url.Values) *httptest.ResponseRecorder {
	e.t.Helper()
	var r io.Reader
	if form != nil {
		r = strings.NewReader(form.Encode())
	}
	req := httptest.NewRequest(method, path, r)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return e.serve(req, as)
}

func expectStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, want, rec.Body.String())
	}
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("respons bukan JSON valid: %v; body: %s", err, rec.Body.String())
	}
	return v
}

func errCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[struct {
		Code string `json:"code"`
	}](t, rec).Code
}

type idResp struct {
	ID int64 `json:"id"`
}

func (e *testEnv) createGroup(admin *auth.User) (int64, string) {
	e.t.Helper()
	name := testutil.Unique("Group")
	rec := e.api(admin, "POST", "/api/admin/groups", map[string]string{"name": name})
	expectStatus(e.t, rec, http.StatusCreated)
	id := decode[idResp](e.t, rec).ID
	e.t.Cleanup(func() { e.db.Exec(`DELETE FROM user_groups WHERE id = $1`, id) })
	return id, name
}

func (e *testEnv) addMember(admin *auth.User, groupID int64, u *auth.User) {
	e.t.Helper()
	rec := e.api(admin, "POST", fmt.Sprintf("/api/admin/groups/%d/members", groupID), map[string]string{"user_id": u.ID})
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		e.t.Fatalf("add member: %d %s", rec.Code, rec.Body.String())
	}
}

func (e *testEnv) setGrant(admin *auth.User, groupID, connID int64, level string) {
	e.t.Helper()
	rec := e.api(admin, "PUT", fmt.Sprintf("/api/admin/groups/%d/grants/%d", groupID, connID), map[string]string{"access_level": level})
	expectStatus(e.t, rec, http.StatusOK)
}

func realTarget(t *testing.T) map[string]any {
	t.Helper()
	c, err := connmgr.ConfigFromURL(testutil.TargetDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	ssl := c.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	return map[string]any{
		"name": testutil.Unique("Target"), "host": c.Host, "port": c.Port,
		"database_name": c.Database, "username": c.Username, "password": c.Password, "ssl_mode": ssl,
	}
}

func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}

const deadPassword = "Dead-Target-Pw!"

func deadTarget(t *testing.T) map[string]any {
	return map[string]any{
		"name": testutil.Unique("Dead"), "host": "127.0.0.1", "port": closedPort(t),
		"database_name": "nothing", "username": "nobody", "password": deadPassword, "ssl_mode": "disable",
	}
}

func (e *testEnv) createConn(admin *auth.User, payload map[string]any) int64 {
	e.t.Helper()
	rec := e.api(admin, "POST", "/api/admin/connections", payload)
	expectStatus(e.t, rec, http.StatusCreated)
	id := decode[idResp](e.t, rec).ID
	e.t.Cleanup(func() { e.db.Exec(`DELETE FROM db_connections WHERE id = $1`, id) })
	return id
}

func (e *testEnv) takeEvents() []privEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	ev := e.events
	e.events = nil
	return ev
}

func flash(rec *httptest.ResponseRecorder) (kind, msg string) {
	for _, c := range rec.Result().Cookies() {
		if c.Name == "flash" && c.Value != "" {
			raw, _ := base64.RawURLEncoding.DecodeString(c.Value)
			k, m, _ := strings.Cut(string(raw), "\x00")
			return k, m
		}
	}
	return "", ""
}
