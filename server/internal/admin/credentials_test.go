package admin

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type trap struct {
	port int
	hits atomic.Int32
}

func newTrap(t *testing.T) *trap {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	tr := &trap{port: l.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			tr.hits.Add(1)
			c.Close()
		}
	}()
	t.Cleanup(func() { l.Close() })
	return tr
}

func (tr *trap) assertUntouched(t *testing.T) {
	t.Helper()
	time.Sleep(50 * time.Millisecond)
	if n := tr.hits.Load(); n != 0 {
		t.Fatalf("password tersimpan dikirim ke host lain (%d koneksi masuk ke server palsu)", n)
	}
}

func TestStoredPasswordNeverSentToNewTarget(t *testing.T) {
	e := newEnv(t)
	admin := e.newUser(true)
	id := e.createConn(admin, realTarget(t))
	tr := newTrap(t)

	moved := realTarget(t)
	delete(moved, "password")
	moved["host"] = "127.0.0.1"
	moved["port"] = tr.port

	probe := map[string]any{"connection_id": id, "host": "127.0.0.1", "port": tr.port,
		"database_name": moved["database_name"], "username": moved["username"], "ssl_mode": "disable"}
	rec := e.api(admin, "POST", "/api/admin/connections/test", probe)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if !strings.Contains(rec.Body.String(), "Password wajib diisi ulang") {
		t.Fatalf("pesan: %s", rec.Body.String())
	}

	moved["test_before_save"] = true
	expectStatus(t, e.api(admin, "PUT", fmt.Sprintf("/api/admin/connections/%d", id), moved), http.StatusUnprocessableEntity)
	moved["test_before_save"] = false
	expectStatus(t, e.api(admin, "PUT", fmt.Sprintf("/api/admin/connections/%d", id), moved), http.StatusUnprocessableEntity)

	for field, val := range map[string]any{"username": "someone_else", "database_name": "other_db", "ssl_mode": "require"} {
		p := realTarget(t)
		delete(p, "password")
		p[field] = val
		expectStatus(t, e.api(admin, "PUT", fmt.Sprintf("/api/admin/connections/%d", id), p), http.StatusUnprocessableEntity)
	}

	form := url.Values{
		"name": {"x"}, "host": {"127.0.0.1"}, "port": {strconv.Itoa(tr.port)},
		"database_name": {fmt.Sprint(moved["database_name"])}, "username": {fmt.Sprint(moved["username"])},
		"ssl_mode": {"disable"}, "password": {""}, "action": {"test"},
	}
	rec = e.page(admin, "POST", fmt.Sprintf("/admin/connections/%d", id), form)
	expectStatus(t, rec, http.StatusUnprocessableEntity)
	if !strings.Contains(rec.Body.String(), "Password wajib diisi ulang") {
		t.Fatal("halaman edit tidak menampilkan pesan password wajib")
	}
	form.Set("action", "save")
	expectStatus(t, e.page(admin, "POST", fmt.Sprintf("/admin/connections/%d", id), form), http.StatusUnprocessableEntity)

	tr.assertUntouched(t)

	cfg, found, err := e.mod.Store.ConnectionConfig(ctx, id)
	if err != nil || !found || cfg.Port == tr.port {
		t.Fatalf("konfigurasi berubah padahal ditolak: %+v", cfg)
	}
	ok := decode[struct {
		OK bool `json:"ok"`
	}](t, e.api(admin, "POST", fmt.Sprintf("/api/admin/connections/%d/test", id), nil))
	if !ok.OK {
		t.Fatal("koneksi asli rusak setelah percobaan yang ditolak")
	}

	same := map[string]any{"connection_id": id}
	for k, v := range realTarget(t) {
		if k != "password" && k != "name" {
			same[k] = v
		}
	}
	res := decode[struct {
		OK bool `json:"ok"`
	}](t, e.api(admin, "POST", "/api/admin/connections/test", same))
	if !res.OK {
		t.Fatal("tes dengan target sama + password tersimpan harus berhasil")
	}

	moved["password"] = "password-baru"
	moved["test_before_save"] = false
	expectStatus(t, e.api(admin, "PUT", fmt.Sprintf("/api/admin/connections/%d", id), moved), http.StatusOK)
}
