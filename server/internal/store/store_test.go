package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"sort"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/secretbox"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

type fixture struct {
	t  *testing.T
	db *sql.DB
	st *Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db := testutil.OpenDB(t)
	box, err := secretbox.New(secretbox.DevKey())
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, db: db, st: New(db, box)}
}

var ctx = context.Background()

func (f *fixture) user(active bool) string {
	f.t.Helper()
	var id string
	err := f.db.QueryRowContext(ctx,
		`INSERT INTO users (username, password_hash, is_active) VALUES ($1, 'x', $2) RETURNING id`,
		testutil.Unique("u"), active).Scan(&id)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { f.db.Exec(`DELETE FROM users WHERE id = $1`, id) })
	return id
}

func (f *fixture) group() Group {
	f.t.Helper()
	g, err := f.st.CreateGroup(ctx, testutil.Unique("grp"), "")
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { f.db.Exec(`DELETE FROM user_groups WHERE id = $1`, g.ID) })
	return g
}

func (f *fixture) conn(password string) Connection {
	f.t.Helper()
	c, err := f.st.CreateConnection(ctx, ConnectionInput{
		Name: testutil.Unique("conn"), Host: "127.0.0.1", Port: 5432,
		DatabaseName: "app", Username: "svc", SSLMode: "disable",
	}, password)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { f.db.Exec(`DELETE FROM db_connections WHERE id = $1`, c.ID) })
	return c
}

func (f *fixture) member(groupID int64, userID string) {
	f.t.Helper()
	if _, err := f.st.AddMember(ctx, groupID, userID); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) grant(groupID, connID int64, l authz.AccessLevel) {
	f.t.Helper()
	if err := f.st.SetGrant(ctx, groupID, connID, l); err != nil {
		f.t.Fatal(err)
	}
}

func TestGroupCRUD(t *testing.T) {
	f := newFixture(t)
	name := testutil.Unique("Analyst")
	g, err := f.st.CreateGroup(ctx, name, "desc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Exec(`DELETE FROM user_groups WHERE id = $1`, g.ID) })
	if g.ID == 0 || g.Name != name || g.Description != "desc" || g.MemberCount != 0 || g.GrantCount != 0 {
		t.Fatalf("CreateGroup = %+v", g)
	}

	if _, err := f.st.CreateGroup(ctx, string(bytes.ToUpper([]byte(name))), ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("nama sama beda huruf: err = %v, want ErrConflict", err)
	}

	other := f.group()
	if _, err := f.st.UpdateGroup(ctx, other.ID, name, ""); !errors.Is(err, ErrConflict) {
		t.Fatalf("rename ke nama yang dipakai: err = %v, want ErrConflict", err)
	}
	upd, err := f.st.UpdateGroup(ctx, g.ID, name+"-baru", "d2")
	if err != nil || upd.Name != name+"-baru" || upd.Description != "d2" || upd.UpdatedAt.Before(g.UpdatedAt) {
		t.Fatalf("UpdateGroup = (%+v, %v)", upd, err)
	}
	if _, err := f.st.UpdateGroup(ctx, -1, "x", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update group tak ada: %v", err)
	}
	if _, err := f.st.GetGroup(ctx, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get group tak ada: %v", err)
	}
	if _, err := f.st.DeleteGroup(ctx, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete group tak ada: %v", err)
	}

	all, err := f.st.ListGroups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, x := range all {
		if x.ID == g.ID || x.ID == other.ID {
			found++
		}
	}
	if found != 2 {
		t.Fatalf("ListGroups tidak memuat group yang dibuat")
	}
}

func TestMembers(t *testing.T) {
	f := newFixture(t)
	g := f.group()
	u := f.user(true)

	created, err := f.st.AddMember(ctx, g.ID, u)
	if err != nil || !created {
		t.Fatalf("AddMember pertama = (%v, %v)", created, err)
	}
	created, err = f.st.AddMember(ctx, g.ID, u)
	if err != nil || created {
		t.Fatalf("AddMember kedua harus idempoten = (%v, %v)", created, err)
	}
	ms, err := f.st.ListMembers(ctx, g.ID)
	if err != nil || len(ms) != 1 || ms[0].UserID != u || !ms[0].IsActive {
		t.Fatalf("ListMembers = (%+v, %v)", ms, err)
	}
	if got, _ := f.st.GetGroup(ctx, g.ID); got.MemberCount != 1 {
		t.Fatalf("MemberCount = %d, want 1", got.MemberCount)
	}

	if _, err := f.st.AddMember(ctx, g.ID, "00000000-0000-0000-0000-00000000dead"); !errors.Is(err, ErrReference) {
		t.Fatalf("user tak ada: err = %v, want ErrReference", err)
	}
	if _, err := f.st.AddMember(ctx, -1, u); !errors.Is(err, ErrReference) {
		t.Fatalf("group tak ada: err = %v, want ErrReference", err)
	}
	if _, err := f.st.ListMembers(ctx, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ListMembers group tak ada: %v", err)
	}

	if err := f.st.RemoveMember(ctx, g.ID, u); err != nil {
		t.Fatal(err)
	}
	if err := f.st.RemoveMember(ctx, g.ID, u); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hapus anggota dua kali: %v", err)
	}
}

func TestGrants(t *testing.T) {
	f := newFixture(t)
	g := f.group()
	a, b := f.conn("pa"), f.conn("pb")

	f.grant(g.ID, a.ID, authz.LevelViewEdit)
	f.grant(g.ID, b.ID, authz.LevelViewOnly)
	grants, err := f.st.ListGroupGrants(ctx, g.ID)
	if err != nil || len(grants) != 2 {
		t.Fatalf("ListGroupGrants = (%+v, %v)", grants, err)
	}
	levels := map[int64]authz.AccessLevel{}
	for _, gr := range grants {
		levels[gr.ConnectionID] = gr.AccessLevel
	}
	if levels[a.ID] != authz.LevelViewEdit || levels[b.ID] != authz.LevelViewOnly {
		t.Fatalf("level per database = %v", levels)
	}

	f.grant(g.ID, a.ID, authz.LevelViewOnly)
	grants, _ = f.st.ListGroupGrants(ctx, g.ID)
	if len(grants) != 2 {
		t.Fatalf("upsert menambah baris: %d grant", len(grants))
	}

	if err := f.st.SetGrant(ctx, g.ID, a.ID, authz.LevelNone); err == nil {
		t.Fatal("SetGrant level none harus ditolak")
	}
	if err := f.st.SetGrant(ctx, g.ID, -1, authz.LevelViewOnly); !errors.Is(err, ErrReference) {
		t.Fatalf("grant ke koneksi tak ada: %v", err)
	}
	if err := f.st.DeleteGrant(ctx, g.ID, b.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.st.DeleteGrant(ctx, g.ID, b.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hapus grant dua kali: %v", err)
	}
	if _, err := f.st.ListGroupGrants(ctx, -1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("grant group tak ada: %v", err)
	}
}

func TestEffectiveAccessAcrossGroups(t *testing.T) {
	f := newFixture(t)
	alice, bob, carol := f.user(true), f.user(true), f.user(true)
	viewers, editors := f.group(), f.group()
	dbA, dbB, dbC := f.conn("a"), f.conn("b"), f.conn("c")

	f.grant(viewers.ID, dbA.ID, authz.LevelViewOnly)
	f.grant(viewers.ID, dbB.ID, authz.LevelViewOnly)
	f.grant(editors.ID, dbA.ID, authz.LevelViewEdit)
	f.member(viewers.ID, alice)
	f.member(editors.ID, alice)
	f.member(viewers.ID, bob)
	_ = carol

	levels, err := f.st.GroupAccessLevels(ctx, alice, dbA.ID)
	if err != nil || len(levels) != 2 || authz.EffectiveLevel(levels...) != authz.LevelViewEdit {
		t.Fatalf("alice db A: levels %v err %v", levels, err)
	}

	got := func(user string) map[int64]AccessibleDatabase {
		t.Helper()
		dbs, err := f.st.AccessibleDatabases(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		m := map[int64]AccessibleDatabase{}
		for _, d := range dbs {
			m[d.ID] = d
		}
		return m
	}

	a := got(alice)
	if len(a) != 2 || a[dbA.ID].AccessLevel != authz.LevelViewEdit || a[dbB.ID].AccessLevel != authz.LevelViewOnly {
		t.Fatalf("alice = %+v", a)
	}
	via := append([]string{}, a[dbA.ID].ViaGroups...)
	sort.Strings(via)
	want := []string{editors.Name, viewers.Name}
	sort.Strings(want)
	if !reflect.DeepEqual(via, want) {
		t.Fatalf("via_groups db A = %v, want %v", via, want)
	}
	if _, ok := a[dbC.ID]; ok {
		t.Fatal("database tanpa grant tidak boleh muncul")
	}

	b := got(bob)
	if len(b) != 2 || b[dbA.ID].AccessLevel != authz.LevelViewOnly {
		t.Fatalf("bob = %+v", b)
	}
	if c := got(carol); len(c) != 0 {
		t.Fatalf("carol tanpa group = %+v, want kosong", c)
	}

	if err := f.st.RemoveMember(ctx, editors.ID, alice); err != nil {
		t.Fatal(err)
	}
	if a := got(alice); a[dbA.ID].AccessLevel != authz.LevelViewOnly {
		t.Fatalf("setelah keluar editors: %+v", a[dbA.ID])
	}
}

func TestDeleteGroupAndConnectionCascade(t *testing.T) {
	f := newFixture(t)
	u1, u2 := f.user(true), f.user(true)
	g := f.group()
	c := f.conn("pw")
	f.member(g.ID, u1)
	f.member(g.ID, u2)
	f.grant(g.ID, c.ID, authz.LevelViewEdit)

	affected, err := f.st.DeleteConnection(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(affected)
	want := []string{u1, u2}
	sort.Strings(want)
	if !reflect.DeepEqual(affected, want) {
		t.Fatalf("DeleteConnection affected = %v, want %v", affected, want)
	}
	if gr, _ := f.st.ListGroupGrants(ctx, g.ID); len(gr) != 0 {
		t.Fatalf("grant tidak ikut terhapus: %+v", gr)
	}
	if ok, _ := f.st.DatabaseExists(ctx, c.ID); ok {
		t.Fatal("DatabaseExists setelah dihapus = true")
	}
	if _, err := f.st.DeleteConnection(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("hapus koneksi dua kali: %v", err)
	}

	affected, err = f.st.DeleteGroup(ctx, g.ID)
	if err != nil || len(affected) != 2 {
		t.Fatalf("DeleteGroup = (%v, %v)", affected, err)
	}
	var n int
	f.db.QueryRow(`SELECT count(*) FROM group_members WHERE group_id = $1`, g.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("keanggotaan tersisa %d", n)
	}
}

func TestConnectionCredentialsAreEncrypted(t *testing.T) {
	f := newFixture(t)
	const pw = "Sup3r-Secret!"
	c := f.conn(pw)

	var raw []byte
	if err := f.db.QueryRow(`SELECT password_encrypted FROM db_connections WHERE id = $1`, c.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(pw)) {
		t.Fatal("password tersimpan dalam bentuk plaintext")
	}

	cfg, found, err := f.st.ConnectionConfig(ctx, c.ID)
	if err != nil || !found || cfg.Password != pw || cfg.Host != "127.0.0.1" || cfg.Database != "app" {
		t.Fatalf("ConnectionConfig = (%+v, %v, %v)", cfg, found, err)
	}

	in := ConnectionInput{Name: c.Name, Host: "127.0.0.2", Port: 5432, DatabaseName: "app", Username: "svc", SSLMode: "disable"}
	if _, err := f.st.UpdateConnection(ctx, c.ID, in, nil); err != nil {
		t.Fatal(err)
	}
	cfg2, _, _ := f.st.ConnectionConfig(ctx, c.ID)
	if cfg2.Password != pw || cfg2.Host != "127.0.0.2" || !cfg2.Version.After(cfg.Version) {
		t.Fatalf("update tanpa password: %+v", cfg2)
	}
	np := "baru-123"
	if _, err := f.st.UpdateConnection(ctx, c.ID, in, &np); err != nil {
		t.Fatal(err)
	}
	if cfg3, _, _ := f.st.ConnectionConfig(ctx, c.ID); cfg3.Password != np {
		t.Fatalf("password baru tidak tersimpan")
	}
	if _, err := f.st.UpdateConnection(ctx, -1, in, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update koneksi tak ada: %v", err)
	}
	if _, found, err := f.st.ConnectionConfig(ctx, -1); found || err != nil {
		t.Fatalf("config koneksi tak ada = (%v, %v)", found, err)
	}

	otherBox, _ := secretbox.New(bytes.Repeat([]byte{7}, secretbox.KeySize))
	if _, _, err := New(f.db, otherBox).ConnectionConfig(ctx, c.ID); err == nil {
		t.Fatal("dekripsi dengan kunci lain harus error")
	}
}

func TestConnectionNameUnique(t *testing.T) {
	f := newFixture(t)
	c := f.conn("x")
	in := ConnectionInput{Name: string(bytes.ToUpper([]byte(c.Name))), Host: "h", Port: 1, DatabaseName: "d", Username: "u", SSLMode: "disable"}
	if _, err := f.st.CreateConnection(ctx, in, "y"); !errors.Is(err, ErrConflict) {
		t.Fatalf("nama koneksi duplikat: %v", err)
	}
}

func TestNormalizeName(t *testing.T) {
	if got := NormalizeName("  Backend \t  Dev\n"); got != "Backend Dev" {
		t.Fatalf("NormalizeName = %q", got)
	}
}
