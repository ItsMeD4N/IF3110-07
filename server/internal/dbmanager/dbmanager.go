package dbmanager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/lib/pq"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

type Module struct {
	Conns  *connmgr.Manager
	Authz  *authz.Authorizer
	MW     *authz.Middleware
	AuthMW *auth.Middleware
	Views  *web.Views
}

func New(conns *connmgr.Manager, az *authz.Authorizer, users auth.UserRepository, sessions *session.Manager, views *web.Views) *Module {
	return &Module{Conns: conns, Authz: az, MW: authz.NewMiddleware(az), AuthMW: auth.NewMiddleware(users, sessions), Views: views}
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	db := func(p authz.Permission, h http.HandlerFunc) http.Handler {
		return m.AuthMW.RequireAuth(m.MW.RequireDB(p, h))
	}
	mux.Handle("GET /api/databases/{dbID}/schema", db(authz.PermDBViewSchema, m.apiSchema))
	mux.Handle("GET /api/databases/{dbID}/tables/{table}/data", db(authz.PermDBViewData, m.apiTableData))
	if m.Views != nil {
		mux.Handle("GET /databases/{dbID}/viewer", db(authz.PermDBViewSchema, m.pageViewer))
	}
}

type Column struct {
	Name         string         `json:"name"`
	Type         string         `json:"type"`
	Nullable     bool           `json:"nullable"`
	Default      sql.NullString `json:"-"`
	DefaultValue *string        `json:"default,omitempty"`
	Ordinal      int            `json:"ordinal"`
}
type Constraint struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Definition string `json:"definition"`
}
type Index struct {
	Name       string `json:"name"`
	Definition string `json:"definition"`
}
type Table struct {
	Name        string       `json:"name"`
	Type        string       `json:"type"`
	Columns     []Column     `json:"columns"`
	Constraints []Constraint `json:"constraints"`
	Indexes     []Index      `json:"indexes"`
}
type schemaResponse struct {
	DatabaseID     int64    `json:"database_id"`
	Schemas        []string `json:"schemas"`
	SelectedSchema string   `json:"selected_schema"`
	Tables         []Table  `json:"tables"`
}

func (m *Module) apiSchema(w http.ResponseWriter, r *http.Request) {
	id, ok := dbID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "ID database tidak valid")
		return
	}
	db, err := m.Conns.Pool(r.Context(), id)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	rows, err := db.QueryContext(r.Context(), `SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('pg_catalog','information_schema') AND schema_name NOT LIKE 'pg_toast%' ORDER BY schema_name`)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var schemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			httpx.Internal(w, r, err)
			return
		}
		schemas = append(schemas, s)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		httpx.Internal(w, r, err)
		return
	}
	rows.Close()
	if schemas == nil {
		schemas = []string{}
	}
	selected := strings.TrimSpace(r.URL.Query().Get("schema"))
	if selected == "" {
		if len(schemas) > 0 {
			selected = schemas[0]
		}
	}
	out := schemaResponse{DatabaseID: id, Schemas: schemas, SelectedSchema: selected, Tables: []Table{}}
	if selected != "" {
		if !contains(schemas, selected) {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "Schema tidak ditemukan")
			return
		}
		tables, err := inspectTablesContext(r.Context(), db, selected)
		if err != nil {
			httpx.Internal(w, r, err)
			return
		}
		out.Tables = tables
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func inspectTablesContext(ctx context.Context, db *sql.DB, schema string) ([]Table, error) {
	rows, err := db.QueryContext(ctx, `SELECT table_name, table_type FROM information_schema.tables WHERE table_schema=$1 ORDER BY table_name`, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Table
	for rows.Next() {
		var t Table
		if err := rows.Scan(&t.Name, &t.Type); err != nil {
			return nil, err
		}
		t.Columns = []Column{}
		t.Constraints = []Constraint{}
		t.Indexes = []Index{}
		cr, err := db.QueryContext(ctx, `SELECT column_name, data_type, is_nullable='YES', column_default, ordinal_position FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 ORDER BY ordinal_position`, schema, t.Name)
		if err != nil {
			return nil, err
		}
		for cr.Next() {
			var c Column
			if err := cr.Scan(&c.Name, &c.Type, &c.Nullable, &c.Default, &c.Ordinal); err != nil {
				cr.Close()
				return nil, err
			}
			if c.Default.Valid {
				v := c.Default.String
				c.DefaultValue = &v
			}
			t.Columns = append(t.Columns, c)
		}
		if err := cr.Err(); err != nil {
			cr.Close()
			return nil, err
		}
		cr.Close()
		kr, err := db.QueryContext(ctx, `SELECT tc.constraint_name, tc.constraint_type, pg_get_constraintdef(c.oid) FROM information_schema.table_constraints tc JOIN pg_constraint c ON c.conname=tc.constraint_name JOIN pg_class rel ON rel.oid=c.conrelid AND rel.relname=tc.table_name JOIN pg_namespace n ON n.oid=rel.relnamespace AND n.nspname=tc.table_schema WHERE tc.table_schema=$1 AND tc.table_name=$2 ORDER BY tc.constraint_name`, schema, t.Name)
		if err != nil {
			return nil, err
		}
		for kr.Next() {
			var c Constraint
			if err := kr.Scan(&c.Name, &c.Type, &c.Definition); err != nil {
				kr.Close()
				return nil, err
			}
			t.Constraints = append(t.Constraints, c)
		}
		if err := kr.Err(); err != nil {
			kr.Close()
			return nil, err
		}
		kr.Close()
		ir, err := db.QueryContext(ctx, `SELECT indexname, indexdef FROM pg_indexes WHERE schemaname=$1 AND tablename=$2 ORDER BY indexname`, schema, t.Name)
		if err != nil {
			return nil, err
		}
		for ir.Next() {
			var i Index
			if err := ir.Scan(&i.Name, &i.Definition); err != nil {
				ir.Close()
				return nil, err
			}
			t.Indexes = append(t.Indexes, i)
		}
		if err := ir.Err(); err != nil {
			ir.Close()
			return nil, err
		}
		ir.Close()
		out = append(out, t)
	}
	return out, rows.Err()
}

type tableDataResponse struct {
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	Total   int64    `json:"total"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}

func (m *Module) apiTableData(w http.ResponseWriter, r *http.Request) {
	id, ok := dbID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "ID database tidak valid")
		return
	}
	schema := r.URL.Query().Get("schema")
	if schema == "" {
		schema = "public"
	}
	table := r.PathValue("table")
	limit := queryInt(r, "limit", 50)
	if limit < 1 {
		limit = 1
	}
	if limit > 100 {
		limit = 100
	}
	offset := queryInt(r, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	db, err := m.Conns.Pool(r.Context(), id)
	if err != nil {
		writeDBError(w, r, err)
		return
	}
	var exists bool
	err = db.QueryRowContext(r.Context(), `SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2 AND table_schema NOT IN ('pg_catalog','information_schema') AND table_schema NOT LIKE 'pg_toast%' AND table_type IN ('BASE TABLE','VIEW'))`, schema, table).Scan(&exists)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	if !exists {
		httpx.NotFound(w)
		return
	}
	qname := pq.QuoteIdentifier(schema) + "." + pq.QuoteIdentifier(table)
	var total int64
	if err := db.QueryRowContext(r.Context(), "SELECT count(*) FROM "+qname).Scan(&total); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	var pkCol string
	_ = db.QueryRowContext(r.Context(), `
		SELECT kcu.column_name 
		FROM information_schema.table_constraints tc 
		JOIN information_schema.key_column_usage kcu 
		  ON tc.constraint_name = kcu.constraint_name 
		  AND tc.table_schema = kcu.table_schema 
		WHERE tc.constraint_type = 'PRIMARY KEY' 
		  AND tc.table_schema = $1 
		  AND tc.table_name = $2 
		ORDER BY kcu.ordinal_position 
		LIMIT 1`, schema, table).Scan(&pkCol)

	dataQuery := "SELECT * FROM " + qname
	if pkCol != "" {
		dataQuery += " ORDER BY " + pq.QuoteIdentifier(pkCol) + " ASC"
	}
	dataQuery += " LIMIT $1 OFFSET $2"

	rows, err := db.QueryContext(r.Context(), dataQuery, limit, offset)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	out := tableDataResponse{Schema: schema, Table: table, Columns: cols, Rows: [][]any{}, Total: total, Limit: limit, Offset: offset}
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			httpx.Internal(w, r, err)
			return
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				vals[i] = string(b)
			}
		}
		out.Rows = append(out.Rows, vals)
	}
	if err := rows.Err(); err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
func (m *Module) pageViewer(w http.ResponseWriter, r *http.Request) {
	id, ok := dbID(r)
	if !ok {
		httpx.NotFound(w)
		return
	}
	if _, err := m.Conns.Pool(r.Context(), id); err != nil {
		writeDBError(w, r, err)
		return
	}
	m.Views.Render(w, r, http.StatusOK, "dbmanager/viewer.html", "Database Viewer", map[string]any{"DatabaseID": id, "Schema": r.URL.Query().Get("schema"), "Table": r.URL.Query().Get("table")})
}
func dbID(r *http.Request) (int64, bool) {
	id, e := strconv.ParseInt(r.PathValue("dbID"), 10, 64)
	return id, e == nil && id > 0
}
func queryInt(r *http.Request, key string, def int) int {
	v, e := strconv.Atoi(r.URL.Query().Get(key))
	if e != nil {
		return def
	}
	return v
}
func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
func writeDBError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, connmgr.ErrNotFound) {
		httpx.NotFound(w)
		return
	}
	var ce *connmgr.ConnError
	if errors.As(err, &ce) {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeConnectionFailed, "Database target tidak dapat dijangkau")
		return
	}
	httpx.Internal(w, r, fmt.Errorf("dbmanager: %w", err))
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
