package connmgr

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

type fakeSource struct {
	mu   sync.Mutex
	cfgs map[int64]Config
	err  error
}

func (f *fakeSource) ConnectionConfig(_ context.Context, id int64) (Config, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return Config{}, false, f.err
	}
	c, ok := f.cfgs[id]
	return c, ok, nil
}

func (f *fakeSource) set(id int64, c Config) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cfgs[id] = c
}

func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

const secretPassword = "S3cret-Pa55word!"

func unreachable(t *testing.T) Config {
	return Config{Host: "127.0.0.1", Port: closedPort(t), Database: "x", Username: "u", Password: secretPassword, SSLMode: "disable"}
}

func TestPool_UnknownConnection(t *testing.T) {
	m := New(&fakeSource{cfgs: map[int64]Config{}}, Options{})
	defer m.Close()
	if _, err := m.Pool(context.Background(), 7); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestPool_SourceErrorPropagates(t *testing.T) {
	boom := errors.New("platform db down")
	m := New(&fakeSource{err: boom}, Options{})
	if _, err := m.Pool(context.Background(), 1); !errors.Is(err, boom) || errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("err = %v, want error sumber (bukan ErrConnectionFailed)", err)
	}
}

func TestPool_ConnectionFailed(t *testing.T) {
	src := &fakeSource{cfgs: map[int64]Config{1: unreachable(t)}}
	m := New(src, Options{ConnectTimeout: 2 * time.Second})
	defer m.Close()

	_, err := m.Pool(context.Background(), 1)
	if !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("err = %v, want ErrConnectionFailed", err)
	}
	var ce *ConnError
	if !errors.As(err, &ce) || ce.DatabaseID != 1 {
		t.Fatalf("err harus *ConnError untuk database 1, got %#v", err)
	}
	if strings.Contains(err.Error(), secretPassword) {
		t.Fatalf("pesan error membocorkan password: %v", err)
	}
	if !strings.Contains(err.Error(), "koneksi ditolak") {
		t.Fatalf("pesan error tidak menjelaskan penyebab: %v", err)
	}
	if _, err := m.Pool(context.Background(), 1); !errors.Is(err, ErrConnectionFailed) {
		t.Fatalf("panggilan kedua: err = %v, want ErrConnectionFailed", err)
	}
}

func TestTest_Failure(t *testing.T) {
	m := New(&fakeSource{}, Options{ConnectTimeout: 2 * time.Second})
	res := m.Test(context.Background(), unreachable(t))
	if res.OK || res.Error == "" || res.ServerVersion != "" {
		t.Fatalf("Test ke port tertutup = %+v, want OK=false dengan pesan", res)
	}
	if strings.Contains(res.Error, secretPassword) {
		t.Fatalf("TestResult.Error membocorkan password: %q", res.Error)
	}
}

func TestTestSaved_Unknown(t *testing.T) {
	m := New(&fakeSource{cfgs: map[int64]Config{}}, Options{})
	if _, err := m.TestSaved(context.Background(), 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestSanitize(t *testing.T) {
	pw := "p@ss w/rd?&"
	cfg := Config{Password: pw}
	cases := []struct {
		raw     string
		want    string
		mustNot []string
	}{
		{"pq: password authentication failed for user \"u\"", "autentikasi gagal: username atau password salah", nil},
		{"dial tcp 127.0.0.1:1: connect: connection refused", "koneksi ditolak", nil},
		{"dial tcp: lookup nowhere.invalid: no such host", "host tidak ditemukan", nil},
		{"pq: database \"zzz\" does not exist", "database tidak ditemukan", nil},
		{"dial tcp 10.0.0.1:5432: i/o timeout", "timeout", nil},
		{"parse error near " + pw + " and " + url.QueryEscape(pw) + " and " + url.PathEscape(pw), "****",
			[]string{pw, url.QueryEscape(pw), url.PathEscape(pw)}},
	}
	for _, c := range cases {
		got := sanitize(errors.New(c.raw), cfg)
		if !strings.Contains(got, c.want) {
			t.Errorf("sanitize(%q) = %q, want memuat %q", c.raw, got, c.want)
		}
		for _, s := range c.mustNot {
			if strings.Contains(got, s) {
				t.Errorf("sanitize(%q) masih memuat %q: %q", c.raw, s, got)
			}
		}
	}
	if got := sanitize(context.DeadlineExceeded, cfg); !strings.HasPrefix(got, "timeout") {
		t.Errorf("DeadlineExceeded -> %q", got)
	}
}

func TestDSN_EscapesCredentials(t *testing.T) {
	c := Config{Host: "db.example", Port: 6543, Database: "app db", Username: "us:er", Password: "p@ss/w:rd?#&", SSLMode: ""}
	u, err := url.Parse(c.DSN(500 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	if u.User.Username() != c.Username || pw != c.Password || u.Hostname() != c.Host || u.Port() != "6543" || u.Path != "/app db" {
		t.Fatalf("DSN tidak bisa diurai balik dengan benar: %s", u.Redacted())
	}
	q := u.Query()
	if q.Get("sslmode") != "disable" || q.Get("connect_timeout") != "1" {
		t.Fatalf("query DSN = %v, want sslmode=disable connect_timeout=1 (minimum)", q)
	}
}

func TestSameTarget(t *testing.T) {
	base := Config{Host: "db.local", Port: 5432, Database: "app", Username: "svc", Password: "a", SSLMode: "require"}
	same := []Config{
		base,
		{Host: " DB.LOCAL ", Port: 5432, Database: "app", Username: "svc", Password: "lain", SSLMode: " REQUIRE "},
	}
	for _, o := range same {
		if !base.SameTarget(o) {
			t.Errorf("SameTarget(%+v) = false, want true", o)
		}
	}
	diff := map[string]func(c *Config){
		"host":     func(c *Config) { c.Host = "evil.example" },
		"port":     func(c *Config) { c.Port = 6543 },
		"database": func(c *Config) { c.Database = "other" },
		"username": func(c *Config) { c.Username = "root" },
		"sslmode":  func(c *Config) { c.SSLMode = "disable" },
	}
	for name, mut := range diff {
		o := base
		mut(&o)
		if base.SameTarget(o) {
			t.Errorf("perubahan %s harus dianggap target berbeda", name)
		}
	}
	if !(Config{SSLMode: ""}).SameTarget(Config{SSLMode: "disable"}) {
		t.Error("ssl mode kosong setara dengan disable")
	}
}

func TestConfigFromURL(t *testing.T) {
	c, err := ConfigFromURL("postgresql://svc:p%40ss@db.local:6543/app?sslmode=require")
	if err != nil {
		t.Fatal(err)
	}
	want := Config{Host: "db.local", Port: 6543, Database: "app", Username: "svc", Password: "p@ss", SSLMode: "require"}
	if c != want {
		t.Fatalf("ConfigFromURL = %+v, want %+v", c, want)
	}
	if c, _ := ConfigFromURL("postgres://u@h/d"); c.Port != 5432 {
		t.Fatalf("port default = %d, want 5432", c.Port)
	}
	for _, bad := range []string{"mysql://u:p@h/d", "postgres://u@h:notaport/d", "::"} {
		if _, err := ConfigFromURL(bad); err == nil {
			t.Errorf("ConfigFromURL(%q) harus error", bad)
		}
	}
}

func realConfig(t *testing.T) Config {
	t.Helper()
	c, err := ConfigFromURL(testutil.DatabaseURL())
	if err != nil {
		t.Fatal(err)
	}
	if c.SSLMode == "" {
		c.SSLMode = "disable"
	}
	c.Version = time.Unix(100, 0)
	return c
}

func TestPool_Integration(t *testing.T) {
	ctx := context.Background()
	src := &fakeSource{cfgs: map[int64]Config{1: realConfig(t)}}
	m := New(src, Options{})
	defer m.Close()

	db1, err := m.Pool(ctx, 1)
	if err != nil {
		t.Fatalf("Pool ke database test gagal (pastikan PostgreSQL berjalan): %v", err)
	}
	var one int
	if err := db1.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil || one != 1 {
		t.Fatalf("query lewat pool gagal: %v", err)
	}
	if !m.recentlyHealthy(1, db1) {
		t.Fatal("setelah ping berhasil, pool harus ditandai sehat")
	}

	db2, _ := m.Pool(ctx, 1)
	if db1 != db2 {
		t.Fatal("konfigurasi tidak berubah: pool harus dipakai ulang")
	}

	c := realConfig(t)
	c.Version = time.Unix(200, 0)
	src.set(1, c)
	db3, err := m.Pool(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if db3 == db1 {
		t.Fatal("versi konfigurasi berubah: pool harus dibuat ulang")
	}
	if err := db1.PingContext(ctx); err == nil {
		t.Fatal("pool lama harus sudah ditutup")
	}

	m.Invalidate(1)
	db4, err := m.Pool(ctx, 1)
	if err != nil || db4 == db3 {
		t.Fatalf("setelah Invalidate harus membuka pool baru (err=%v)", err)
	}

	src.mu.Lock()
	delete(src.cfgs, 1)
	src.mu.Unlock()
	if _, err := m.Pool(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("setelah dihapus: err = %v, want ErrNotFound", err)
	}
}

func TestPool_HealthCacheDisabled(t *testing.T) {
	src := &fakeSource{cfgs: map[int64]Config{1: realConfig(t)}}
	m := New(src, Options{HealthTTL: -1})
	defer m.Close()
	db, err := m.Pool(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if m.recentlyHealthy(1, db) {
		t.Fatal("HealthTTL negatif: setiap Pool() harus ping ulang")
	}
}

func TestTestSaved_Integration(t *testing.T) {
	m := New(&fakeSource{cfgs: map[int64]Config{1: realConfig(t)}}, Options{})
	res, err := m.TestSaved(context.Background(), 1)
	if err != nil || !res.OK || res.ServerVersion == "" {
		t.Fatalf("TestSaved = (%+v, %v), want OK dengan versi server", res, err)
	}
	if _, err := strconv.Atoi(strings.SplitN(res.ServerVersion, ".", 2)[0]); err != nil {
		t.Fatalf("versi server tidak wajar: %q", res.ServerVersion)
	}
}
