package admin

import (
	"net/http"

	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
)

type myDatabase struct {
	ID           int64              `json:"id"`
	Name         string             `json:"name"`
	Description  string             `json:"description"`
	DatabaseName string             `json:"database_name"`
	AccessLevel  authz.AccessLevel  `json:"access_level"`
	AccessLabel  string             `json:"access_label"`
	Permissions  []authz.Permission `json:"permissions"`
	ViaGroups    []string           `json:"via_groups,omitempty"`
}

func (m *Module) apiListMyDatabases(w http.ResponseWriter, r *http.Request) {
	p := authz.PrincipalFrom(r.Context())
	dbs, err := m.Store.AccessibleDatabases(r.Context(), p.ID)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := make([]myDatabase, 0, len(dbs))
	for _, d := range dbs {
		out = append(out, myDatabase{
			ID: d.ID, Name: d.Name, Description: d.Description, DatabaseName: d.DatabaseName,
			AccessLevel: d.AccessLevel, AccessLabel: d.AccessLevel.Label(),
			Permissions: authz.PermissionsForLevel(d.AccessLevel), ViaGroups: d.ViaGroups,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"databases": out})
}

func (m *Module) apiGetMyDatabase(w http.ResponseWriter, r *http.Request) {
	access, _ := authz.DBAccessFrom(r.Context())
	c, err := m.Store.GetConnection(r.Context(), access.DatabaseID)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, myDatabase{
		ID: c.ID, Name: c.Name, Description: c.Description, DatabaseName: c.DatabaseName,
		AccessLevel: access.Level, AccessLabel: access.Level.Label(),
		Permissions: authz.PermissionsForLevel(access.Level),
	})
}

func (m *Module) apiDatabaseStatus(w http.ResponseWriter, r *http.Request) {
	access, _ := authz.DBAccessFrom(r.Context())
	if _, err := m.Conns.Pool(r.Context(), access.DatabaseID); err != nil {
		WriteConnError(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}
