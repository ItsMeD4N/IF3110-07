package authz

import (
	"fmt"
	"strings"
)

type Permission string

const (
	PermManageUsers       Permission = "platform.users.manage"
	PermManageGroups      Permission = "platform.groups.manage"
	PermManageGrants      Permission = "platform.grants.manage"
	PermManageConnections Permission = "platform.connections.manage"
)

const (
	PermDBViewSchema       Permission = "db.schema.view"
	PermDBViewData         Permission = "db.data.view"
	PermDBViewMigrations   Permission = "db.migrations.view"
	PermDBCreateMigration  Permission = "db.migrations.create"
	PermDBEditMigration    Permission = "db.migrations.edit"
	PermDBExecuteMigration Permission = "db.migrations.execute"
)

type AccessLevel int

const (
	LevelNone     AccessLevel = 0
	LevelViewOnly AccessLevel = 1
	LevelViewEdit AccessLevel = 2
)

var platformPermissions = map[Permission]bool{
	PermManageUsers:       true,
	PermManageGroups:      true,
	PermManageGrants:      true,
	PermManageConnections: true,
}

var dbPermissionMinLevel = map[Permission]AccessLevel{
	PermDBViewSchema:       LevelViewOnly,
	PermDBViewData:         LevelViewOnly,
	PermDBViewMigrations:   LevelViewOnly,
	PermDBCreateMigration:  LevelViewEdit,
	PermDBEditMigration:    LevelViewEdit,
	PermDBExecuteMigration: LevelViewEdit,
}

var orderedDBPermissions = []Permission{
	PermDBViewSchema,
	PermDBViewData,
	PermDBViewMigrations,
	PermDBCreateMigration,
	PermDBEditMigration,
	PermDBExecuteMigration,
}

var orderedPlatformPermissions = []Permission{
	PermManageUsers,
	PermManageGroups,
	PermManageGrants,
	PermManageConnections,
}

func IsPlatformPermission(p Permission) bool { return platformPermissions[p] }

func IsDatabasePermission(p Permission) bool {
	_, ok := dbPermissionMinLevel[p]
	return ok
}

func LevelAllows(level AccessLevel, perm Permission) bool {
	min, ok := dbPermissionMinLevel[perm]
	if !ok {
		return false
	}
	return level >= min
}

func PermissionsForLevel(level AccessLevel) []Permission {
	out := make([]Permission, 0, len(orderedDBPermissions))
	for _, p := range orderedDBPermissions {
		if LevelAllows(level, p) {
			out = append(out, p)
		}
	}
	return out
}

func PlatformPermissions(p *Principal) []Permission {
	if p == nil || !p.IsPlatformAdmin {
		return []Permission{}
	}
	out := make([]Permission, len(orderedPlatformPermissions))
	copy(out, orderedPlatformPermissions)
	return out
}

func (l AccessLevel) ValidGrant() bool { return l == LevelViewOnly || l == LevelViewEdit }

func (l AccessLevel) String() string {
	switch l {
	case LevelViewOnly:
		return "view_only"
	case LevelViewEdit:
		return "view_edit"
	default:
		return "none"
	}
}

func (l AccessLevel) Label() string {
	switch l {
	case LevelViewOnly:
		return "View Only"
	case LevelViewEdit:
		return "View + Edit"
	default:
		return "Tidak ada akses"
	}
}

func ParseAccessLevel(s string) (AccessLevel, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "view_only", "view-only", "viewonly":
		return LevelViewOnly, nil
	case "view_edit", "view-edit", "view+edit", "viewedit":
		return LevelViewEdit, nil
	default:
		return LevelNone, fmt.Errorf("access level %q tidak valid (gunakan view_only atau view_edit)", s)
	}
}

func (l AccessLevel) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

func (l *AccessLevel) UnmarshalText(b []byte) error {
	v, err := ParseAccessLevel(string(b))
	if err != nil {
		return err
	}
	*l = v
	return nil
}
