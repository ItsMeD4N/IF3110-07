package authz

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestParseAccessLevel(t *testing.T) {
	valid := map[string]AccessLevel{
		"view_only": LevelViewOnly, "View-Only": LevelViewOnly, " viewonly ": LevelViewOnly,
		"view_edit": LevelViewEdit, "VIEW+EDIT": LevelViewEdit, "view-edit": LevelViewEdit, "viewedit": LevelViewEdit,
	}
	for in, want := range valid {
		got, err := ParseAccessLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseAccessLevel(%q) = (%v, %v), want %v", in, got, err, want)
		}
	}
	for _, in := range []string{"", "none", "admin", "edit", "1", "view"} {
		if got, err := ParseAccessLevel(in); err == nil {
			t.Errorf("ParseAccessLevel(%q) = %v, want error", in, got)
		}
	}
}

func TestAccessLevel_StringLabelValid(t *testing.T) {
	cases := []struct {
		l            AccessLevel
		str, label   string
		isValidGrant bool
	}{
		{LevelNone, "none", "Tidak ada akses", false},
		{LevelViewOnly, "view_only", "View Only", true},
		{LevelViewEdit, "view_edit", "View + Edit", true},
		{AccessLevel(9), "none", "Tidak ada akses", false},
	}
	for _, c := range cases {
		if c.l.String() != c.str || c.l.Label() != c.label || c.l.ValidGrant() != c.isValidGrant {
			t.Errorf("level %d: got (%q, %q, %v)", c.l, c.l.String(), c.l.Label(), c.l.ValidGrant())
		}
	}
}

func TestAccessLevel_JSONRoundTrip(t *testing.T) {
	type payload struct {
		Level AccessLevel `json:"level"`
	}
	b, err := json.Marshal(payload{Level: LevelViewEdit})
	if err != nil || string(b) != `{"level":"view_edit"}` {
		t.Fatalf("marshal = %s, %v", b, err)
	}
	var p payload
	if err := json.Unmarshal([]byte(`{"level":"view_only"}`), &p); err != nil || p.Level != LevelViewOnly {
		t.Fatalf("unmarshal = %v, %v", p.Level, err)
	}
	if err := json.Unmarshal([]byte(`{"level":"superuser"}`), &p); err == nil {
		t.Fatal("unmarshal level tidak valid harus error")
	}
}

func TestPermissionsForLevel(t *testing.T) {
	if got := PermissionsForLevel(LevelNone); len(got) != 0 {
		t.Fatalf("none -> %v, want kosong", got)
	}
	if got := PermissionsForLevel(LevelViewOnly); !reflect.DeepEqual(got, viewPerms) {
		t.Fatalf("view_only -> %v, want %v", got, viewPerms)
	}
	want := append(append([]Permission{}, viewPerms...), editPerms...)
	if got := PermissionsForLevel(LevelViewEdit); !reflect.DeepEqual(got, want) {
		t.Fatalf("view_edit -> %v, want %v", got, want)
	}
}

func TestPermissionCatalogIsConsistent(t *testing.T) {
	for _, p := range orderedPlatformPermissions {
		if !IsPlatformPermission(p) || IsDatabasePermission(p) {
			t.Errorf("%s harus permission platform saja", p)
		}
	}
	for _, p := range orderedDBPermissions {
		if !IsDatabasePermission(p) || IsPlatformPermission(p) {
			t.Errorf("%s harus permission database saja", p)
		}
	}
	if len(orderedPlatformPermissions) != len(platformPermissions) || len(orderedDBPermissions) != len(dbPermissionMinLevel) {
		t.Fatal("daftar berurutan dan map permission tidak sinkron")
	}
	if LevelAllows(LevelViewEdit, Permission("unknown")) {
		t.Fatal("permission tak dikenal tidak boleh diizinkan level apa pun")
	}
}

func TestPlatformPermissions(t *testing.T) {
	if got := PlatformPermissions(nil); len(got) != 0 {
		t.Fatalf("nil -> %v", got)
	}
	if got := PlatformPermissions(user); len(got) != 0 {
		t.Fatalf("user biasa -> %v", got)
	}
	got := PlatformPermissions(admin)
	if !reflect.DeepEqual(got, orderedPlatformPermissions) {
		t.Fatalf("admin -> %v", got)
	}
	got[0] = "diubah"
	if orderedPlatformPermissions[0] == "diubah" {
		t.Fatal("PlatformPermissions harus mengembalikan salinan")
	}
}
