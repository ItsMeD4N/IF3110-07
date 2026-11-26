package apptest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/app"
	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/csrf"
	"github.com/ItsMeD4N/IF3110-07/server/internal/secretbox"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

const (
	clientDir = "../../../client"
	csrfToken = "test-csrf-token"
	Password  = "Passw0rd!Passw0rd"
)

var (
	hashOnce sync.Once
	pwHash   string
)

type Env struct {
	T   *testing.T
	App *app.App

	mu      sync.Mutex
	cookies map[string]string
}

func New(t *testing.T) *Env {
	t.Helper()
	db := testutil.OpenDB(t)
	a, err := app.New(db, app.Options{AppEnv: "test", ClientDir: clientDir, EncryptionKey: secretbox.DevKey()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Close)
	return &Env{T: t, App: a, cookies: map[string]string{}}
}

func (e *Env) NewUser(isAdmin bool) *auth.User {
	e.T.Helper()
	hashOnce.Do(func() {
		h, err := auth.HashPassword(Password)
		if err != nil {
			e.T.Fatal(err)
		}
		pwHash = h
	})
	u, err := e.App.Users.Create(context.Background(), testutil.Unique("u"), pwHash, isAdmin)
	if err != nil {
		e.T.Fatal(err)
	}
	e.T.Cleanup(func() { _ = e.App.Users.Delete(context.Background(), u.ID) })
	return u
}

func (e *Env) sessionToken(u *auth.User) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if tok, ok := e.cookies[u.ID]; ok {
		return tok
	}
	_, tok, err := e.App.Sessions.CreateSession(context.Background(), u.ID, "127.0.0.1", "apptest")
	if err != nil {
		e.T.Fatal(err)
	}
	e.cookies[u.ID] = tok
	return tok
}

func (e *Env) Do(as *auth.User, method, path string, body any) *httptest.ResponseRecorder {
	e.T.Helper()
	return e.do(as, method, path, body, true)
}

func (e *Env) DoNoCSRF(as *auth.User, method, path string, body any) *httptest.ResponseRecorder {
	e.T.Helper()
	return e.do(as, method, path, body, false)
}

func (e *Env) do(as *auth.User, method, path string, body any, withCSRF bool) *httptest.ResponseRecorder {
	var r io.Reader
	contentType := ""
	switch b := body.(type) {
	case nil:
	case url.Values:
		r, contentType = strings.NewReader(b.Encode()), "application/x-www-form-urlencoded"
	default:
		raw, err := json.Marshal(b)
		if err != nil {
			e.T.Fatal(err)
		}
		r, contentType = bytes.NewReader(raw), "application/json"
	}
	req := httptest.NewRequest(method, path, r)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if withCSRF {
		req.AddCookie(&http.Cookie{Name: csrf.CSRFCookieName, Value: csrfToken})
		req.Header.Set(csrf.CSRFHeaderName, csrfToken)
	}
	if as != nil {
		req.AddCookie(&http.Cookie{Name: session.SessionCookieName, Value: e.sessionToken(as)})
	}
	rec := httptest.NewRecorder()
	e.App.Handler.ServeHTTP(rec, req)
	return rec
}

func Expect(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, status, rec.Body.String())
	}
}

func Decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("respons bukan JSON: %v; body: %s", err, rec.Body.String())
	}
	return v
}

func (e *Env) Database(admin *auth.User, conn map[string]any) int64 {
	e.T.Helper()
	rec := e.Do(admin, "POST", "/api/admin/connections", conn)
	Expect(e.T, rec, http.StatusCreated)
	id := Decode[struct{ ID int64 }](e.T, rec).ID
	e.T.Cleanup(func() { e.Do(admin, "DELETE", fmt.Sprintf("/api/admin/connections/%d", id), nil) })
	return id
}

func (e *Env) Grant(admin, u *auth.User, dbID int64, level string) {
	e.T.Helper()
	rec := e.Do(admin, "POST", "/api/admin/groups", map[string]string{"name": testutil.Unique("g")})
	Expect(e.T, rec, http.StatusCreated)
	gid := Decode[struct{ ID int64 }](e.T, rec).ID
	e.T.Cleanup(func() { e.Do(admin, "DELETE", fmt.Sprintf("/api/admin/groups/%d", gid), nil) })

	Expect(e.T, e.Do(admin, "POST", fmt.Sprintf("/api/admin/groups/%d/members", gid), map[string]string{"user_id": u.ID}), http.StatusCreated)
	Expect(e.T, e.Do(admin, "PUT", fmt.Sprintf("/api/admin/groups/%d/grants/%d", gid, dbID), map[string]string{"access_level": level}), http.StatusOK)
}

func TargetConn(t *testing.T) map[string]any {
	t.Helper()
	c, err := connmgr.ConfigFromURL(testutil.TargetDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"name": testutil.Unique("target"), "host": c.Host, "port": c.Port, "database_name": c.Database,
		"username": c.Username, "password": c.Password, "ssl_mode": "disable",
	}
}

func DeadConn() map[string]any {
	return map[string]any{
		"name": testutil.Unique("dead"), "host": "127.0.0.1", "port": 1,
		"database_name": "nothing", "username": "nobody", "password": "x", "ssl_mode": "disable",
	}
}
