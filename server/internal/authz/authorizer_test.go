package authz

import (
	"context"
	"errors"
	"net/http"
	"testing"
)

type fakeSource struct {
	dbs    map[int64]bool
	grants map[string]map[int64][]AccessLevel
	err    error
	calls  int
}

func newFakeSource(dbIDs ...int64) *fakeSource {
	f := &fakeSource{dbs: map[int64]bool{}, grants: map[string]map[int64][]AccessLevel{}}
	for _, id := range dbIDs {
		f.dbs[id] = true
	}
	return f
}

func (f *fakeSource) grant(userID string, dbID int64, l AccessLevel) *fakeSource {
	if f.grants[userID] == nil {
		f.grants[userID] = map[int64][]AccessLevel{}
	}
	f.grants[userID][dbID] = append(f.grants[userID][dbID], l)
	return f
}

func (f *fakeSource) DatabaseExists(_ context.Context, id int64) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	return f.dbs[id], nil
}

func (f *fakeSource) GroupAccessLevels(_ context.Context, userID string, id int64) ([]AccessLevel, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.grants[userID][id], nil
}

var (
	admin = &Principal{ID: "admin", Username: "admin", IsPlatformAdmin: true}
	user  = &Principal{ID: "user", Username: "user"}
)

var (
	viewPerms = []Permission{PermDBViewSchema, PermDBViewData, PermDBViewMigrations}
	editPerms = []Permission{PermDBCreateMigration, PermDBEditMigration, PermDBExecuteMigration}
)

func TestEffectiveLevel(t *testing.T) {
	cases := []struct {
		name   string
		levels []AccessLevel
		want   AccessLevel
	}{
		{"tanpa group", nil, LevelNone},
		{"satu group view only", []AccessLevel{LevelViewOnly}, LevelViewOnly},
		{"satu group view edit", []AccessLevel{LevelViewEdit}, LevelViewEdit},
		{"view only lalu view edit", []AccessLevel{LevelViewOnly, LevelViewEdit}, LevelViewEdit},
		{"view edit lalu view only (urutan tidak berpengaruh)", []AccessLevel{LevelViewEdit, LevelViewOnly}, LevelViewEdit},
		{"dua group view only", []AccessLevel{LevelViewOnly, LevelViewOnly}, LevelViewOnly},
		{"nilai tidak valid diabaikan", []AccessLevel{AccessLevel(7), LevelNone, AccessLevel(-1)}, LevelNone},
		{"nilai tidak valid tidak mengalahkan grant sah", []AccessLevel{AccessLevel(99), LevelViewOnly}, LevelViewOnly},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := EffectiveLevel(c.levels...); got != c.want {
				t.Fatalf("EffectiveLevel(%v) = %v, want %v", c.levels, got, c.want)
			}
		})
	}
}

func TestCheck_Unauthenticated(t *testing.T) {
	az := New(newFakeSource(1))
	for _, perm := range []Permission{PermManageGroups, PermDBViewSchema} {
		res := Platform
		if IsDatabasePermission(perm) {
			res = Database(1)
		}
		if err := az.Check(context.Background(), nil, perm, res); !errors.Is(err, ErrUnauthenticated) {
			t.Fatalf("%s tanpa principal: err = %v, want ErrUnauthenticated", perm, err)
		}
	}
}

func TestCheck_PlatformPermissions(t *testing.T) {
	src := newFakeSource()
	az := New(src)
	ctx := context.Background()
	for _, perm := range orderedPlatformPermissions {
		if err := az.Check(ctx, admin, perm, Platform); err != nil {
			t.Errorf("admin %s: err = %v, want nil", perm, err)
		}
		if err := az.Check(ctx, user, perm, Platform); !errors.Is(err, ErrForbidden) {
			t.Errorf("user biasa %s: err = %v, want ErrForbidden", perm, err)
		}
	}
	if src.calls != 0 {
		t.Fatalf("permission platform tidak boleh menyentuh AccessSource (calls=%d)", src.calls)
	}
}

func TestCheck_PlatformAdminHasNoImplicitDatabaseAccess(t *testing.T) {
	az := New(newFakeSource(1))
	for _, perm := range append(viewPerms, editPerms...) {
		if err := az.Check(context.Background(), admin, perm, Database(1)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("admin tanpa group %s: err = %v, want ErrNotFound", perm, err)
		}
	}
}

func TestCheck_ViewOnly(t *testing.T) {
	az := New(newFakeSource(1).grant(user.ID, 1, LevelViewOnly))
	ctx := context.Background()
	for _, perm := range viewPerms {
		if err := az.Check(ctx, user, perm, Database(1)); err != nil {
			t.Errorf("view only %s: err = %v, want nil", perm, err)
		}
	}
	for _, perm := range editPerms {
		if err := az.Check(ctx, user, perm, Database(1)); !errors.Is(err, ErrForbidden) {
			t.Errorf("view only %s: err = %v, want ErrForbidden", perm, err)
		}
	}
}

func TestCheck_ViewEdit(t *testing.T) {
	az := New(newFakeSource(1).grant(user.ID, 1, LevelViewEdit))
	for _, perm := range append(viewPerms, editPerms...) {
		if err := az.Check(context.Background(), user, perm, Database(1)); err != nil {
			t.Errorf("view + edit %s: err = %v, want nil", perm, err)
		}
	}
}

func TestCheck_MultiGroupEffectiveAccess(t *testing.T) {
	ctx := context.Background()

	az := New(newFakeSource(1).grant(user.ID, 1, LevelViewOnly).grant(user.ID, 1, LevelViewEdit))
	if err := az.Check(ctx, user, PermDBExecuteMigration, Database(1)); err != nil {
		t.Fatalf("view only + view edit -> execute: err = %v, want nil", err)
	}
	if lvl, _ := az.EffectiveAccess(ctx, user, 1); lvl != LevelViewEdit {
		t.Fatalf("EffectiveAccess = %v, want view_edit", lvl)
	}

	az = New(newFakeSource(1).grant(user.ID, 1, LevelViewOnly).grant(user.ID, 1, LevelViewOnly))
	if err := az.Check(ctx, user, PermDBExecuteMigration, Database(1)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("view only + view only -> execute: err = %v, want ErrForbidden", err)
	}
}

func TestCheck_DifferentLevelPerDatabase(t *testing.T) {
	az := New(newFakeSource(1, 2, 3).
		grant(user.ID, 1, LevelViewEdit).
		grant(user.ID, 2, LevelViewOnly))
	ctx := context.Background()

	if err := az.Check(ctx, user, PermDBExecuteMigration, Database(1)); err != nil {
		t.Errorf("db 1 (view edit) execute: %v", err)
	}
	if err := az.Check(ctx, user, PermDBExecuteMigration, Database(2)); !errors.Is(err, ErrForbidden) {
		t.Errorf("db 2 (view only) execute: err = %v, want ErrForbidden", err)
	}
	if err := az.Check(ctx, user, PermDBViewSchema, Database(3)); !errors.Is(err, ErrNotFound) {
		t.Errorf("db 3 (tanpa grant) view: err = %v, want ErrNotFound", err)
	}
}

func TestCheck_UnknownOrInvalidDatabase(t *testing.T) {
	src := newFakeSource(1).grant(user.ID, 1, LevelViewEdit)
	az := New(src)
	ctx := context.Background()

	if err := az.Check(ctx, user, PermDBViewSchema, Database(42)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("database tidak ada: err = %v, want ErrNotFound", err)
	}
	src.calls = 0
	for _, id := range []int64{0, -5} {
		if err := az.Check(ctx, user, PermDBViewSchema, Database(id)); !errors.Is(err, ErrNotFound) {
			t.Fatalf("id %d: err = %v, want ErrNotFound", id, err)
		}
	}
	if src.calls != 0 {
		t.Fatalf("id tidak valid tidak boleh menyentuh AccessSource (calls=%d)", src.calls)
	}
}

func TestCheck_UnknownPermissionIsDenied(t *testing.T) {
	az := New(newFakeSource(1).grant(admin.ID, 1, LevelViewEdit))
	if err := az.Check(context.Background(), admin, Permission("db.drop_everything"), Database(1)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("permission tak dikenal: err = %v, want ErrForbidden (deny by default)", err)
	}
}

func TestCan(t *testing.T) {
	ctx := context.Background()
	az := New(newFakeSource(1).grant(user.ID, 1, LevelViewOnly))

	if ok, err := az.Can(ctx, user, PermDBViewData, Database(1)); !ok || err != nil {
		t.Fatalf("Can view = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := az.Can(ctx, user, PermDBExecuteMigration, Database(1)); ok || err != nil {
		t.Fatalf("Can execute = (%v, %v), want (false, nil)", ok, err)
	}
	if ok, err := az.Can(ctx, nil, PermDBViewData, Database(1)); ok || err != nil {
		t.Fatalf("Can tanpa principal = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestCan_SourceErrorIsNotDenied(t *testing.T) {
	src := newFakeSource(1)
	src.err = errors.New("db down")
	az := New(src)

	ok, err := az.Can(context.Background(), user, PermDBViewSchema, Database(1))
	if ok || err == nil {
		t.Fatalf("Can = (%v, %v), want (false, error)", ok, err)
	}
	if IsDenied(err) {
		t.Fatalf("error infrastruktur tidak boleh dianggap penolakan akses: %v", err)
	}
	if got := StatusFor(err); got != http.StatusInternalServerError {
		t.Fatalf("StatusFor(source error) = %d, want 500", got)
	}
}
