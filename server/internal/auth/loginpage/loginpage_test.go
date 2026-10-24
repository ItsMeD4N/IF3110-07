package loginpage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

const password = "Passw0rd!Login"

type env struct {
	mux      *http.ServeMux
	users    auth.UserRepository
	sessions *session.Manager
}

func newEnv(t *testing.T) *env {
	t.Helper()
	db := testutil.OpenDB(t)
	users := auth.NewUserRepository(db)
	sessions := session.NewManager(session.NewSessionRepository(db), false)
	views, err := web.New("../../../../client", false)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(users, sessions, views).RegisterRoutes(mux)
	return &env{mux: mux, users: users, sessions: sessions}
}

var (
	hashOnce sync.Once
	hash     string
)

func (e *env) user(t *testing.T, active bool) *auth.User {
	t.Helper()
	hashOnce.Do(func() {
		h, err := auth.HashPassword(password)
		if err != nil {
			t.Fatal(err)
		}
		hash = h
	})
	u, err := e.users.Create(context.Background(), testutil.Unique("login"), hash, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.users.Delete(context.Background(), u.ID) })
	if !active {
		if err := e.users.Update(context.Background(), u.ID, u.Username, false, false); err != nil {
			t.Fatal(err)
		}
	}
	return u
}

func (e *env) do(method, path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.mux.ServeHTTP(rec, req)
	return rec
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == session.SessionCookieName {
			return c
		}
	}
	return nil
}

func TestLoginPage_Renders(t *testing.T) {
	e := newEnv(t)
	rec := e.do("GET", "/login", nil, nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `action="/login"`) || !strings.Contains(rec.Body.String(), `name="password"`) {
		t.Fatalf("GET /login = %d", rec.Code)
	}
	if rec := e.do("GET", "/", nil, nil); rec.Code != http.StatusFound || rec.Header().Get("Location") != AfterLoginPath {
		t.Fatalf("GET / = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestLogin_SuccessSetsSecureSessionCookie(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, true)

	rec := e.do("POST", "/login", url.Values{"username": {u.Username}, "password": {password}}, nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != AfterLoginPath {
		t.Fatalf("login = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	c := sessionCookie(rec)
	if c == nil || c.Value == "" || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie session = %+v", c)
	}
	if strings.Contains(rec.Header().Get("Location"), c.Value) || strings.Contains(rec.Body.String(), c.Value) {
		t.Fatal("session identifier muncul di URL/response")
	}
	if _, err := e.sessions.ValidateSession(context.Background(), c.Value, nil); err != nil {
		t.Fatalf("session hasil login tidak valid: %v", err)
	}

	if rec := e.do("GET", "/login", nil, c); rec.Code != http.StatusFound || rec.Header().Get("Location") != AfterLoginPath {
		t.Fatalf("GET /login saat sudah login = %d", rec.Code)
	}

	rec2 := e.do("POST", "/login", url.Values{"username": {u.Username}, "password": {password}}, c)
	c2 := sessionCookie(rec2)
	if c2 == nil || c2.Value == c.Value {
		t.Fatal("login ulang tidak merotasi identifier")
	}
	if _, err := e.sessions.ValidateSession(context.Background(), c.Value, nil); err == nil {
		t.Fatal("identifier lama masih berlaku setelah login ulang")
	}
}

func TestLogin_Failures(t *testing.T) {
	e := newEnv(t)
	active, inactive := e.user(t, true), e.user(t, false)

	cases := []struct {
		name, username, pw string
		want               int
		msg                string
	}{
		{"password salah", active.Username, "Wr0ng#Pw-xyz", http.StatusUnauthorized, msgInvalid},
		{"user tidak ada", "tidak_ada_" + active.Username, password, http.StatusUnauthorized, msgInvalid},
		{"kosong", "", "", http.StatusUnauthorized, msgInvalid},
		{"nonaktif password salah", inactive.Username, "Wr0ng#Pw-xyz", http.StatusUnauthorized, msgInvalid},
		{"nonaktif password benar", inactive.Username, password, http.StatusForbidden, msgInactive},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := e.do("POST", "/login", url.Values{"username": {c.username}, "password": {c.pw}}, nil)
			body := rec.Body.String()
			if rec.Code != c.want || !strings.Contains(body, c.msg) {
				t.Fatalf("status %d (want %d), body memuat pesan %q? %v", rec.Code, c.want, c.msg, strings.Contains(body, c.msg))
			}
			if sessionCookie(rec) != nil {
				t.Fatal("login gagal tetap memasang cookie session")
			}
			if c.pw != "" && strings.Contains(body, c.pw) {
				t.Fatal("password dipantulkan kembali ke halaman")
			}
		})
	}
}

func TestLogout_RevokesSession(t *testing.T) {
	e := newEnv(t)
	u := e.user(t, true)
	c := sessionCookie(e.do("POST", "/login", url.Values{"username": {u.Username}, "password": {password}}, nil))

	rec := e.do("POST", "/logout", url.Values{}, c)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login" {
		t.Fatalf("logout = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if cleared := sessionCookie(rec); cleared == nil || cleared.MaxAge >= 0 {
		t.Fatalf("cookie tidak dihapus: %+v", cleared)
	}
	if _, err := e.sessions.ValidateSession(context.Background(), c.Value, nil); err == nil {
		t.Fatal("session masih berlaku di backend setelah logout")
	}
	if rec := e.do("POST", "/logout", url.Values{}, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("logout tanpa session = %d", rec.Code)
	}
}
