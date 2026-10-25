package migration

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/session"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

const advisoryLockKey int64 = 31100703
const maxSQLBytes = 1 << 20

type Module struct {
	PlatformDB *sql.DB
	Conns      *connmgr.Manager
	Authz      *authz.Authorizer
	MW         *authz.Middleware
	AuthMW     *auth.Middleware
	Views      *web.Views
}
type Record struct {
	ID         int64     `json:"id"`
	DatabaseID int64     `json:"database_id"`
	Version    int64     `json:"version"`
	SQL        string    `json:"sql"`
	Executor   string    `json:"executor"`
	ExecutedAt time.Time `json:"executed_at"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
}

func New(platformDB *sql.DB, conns *connmgr.Manager, az *authz.Authorizer, users auth.UserRepository, sessions *session.Manager, views *web.Views) *Module {
	return &Module{PlatformDB: platformDB, Conns: conns, Authz: az, MW: authz.NewMiddleware(az), AuthMW: auth.NewMiddleware(users, sessions), Views: views}
}
func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	db := func(p authz.Permission, h http.HandlerFunc) http.Handler {
		return m.AuthMW.RequireAuth(m.MW.RequireDB(p, h))
	}
	mux.Handle("GET /api/databases/{dbID}/migrations", db(authz.PermDBViewMigrations, m.apiHistory))
	mux.Handle("POST /api/databases/{dbID}/migrations", db(authz.PermDBExecuteMigration, m.apiExecute))
	mux.Handle("POST /api/databases/{dbID}/migrations/explain", db(authz.PermDBViewMigrations, m.apiExplain))
	if m.Views != nil {
		mux.Handle("GET /databases/{dbID}/migrations", db(authz.PermDBViewMigrations, m.pageEditor))
		mux.Handle("GET /databases/{dbID}/migrations/history", db(authz.PermDBViewMigrations, m.pageHistory))
		mux.Handle("POST /databases/{dbID}/migrations", db(authz.PermDBExecuteMigration, m.pageExecute))
	}
}
func (m *Module) apiHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := databaseID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "ID database tidak valid")
		return
	}
	rows, err := m.list(r.Context(), id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"migrations": rows})
}
func (m *Module) apiExecute(w http.ResponseWriter, r *http.Request) {
	id, ok := databaseID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "ID database tidak valid")
		return
	}
	if !m.canExecute(w, r, id) {
		return
	}
	if err := r.ParseForm(); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "Body request tidak valid")
		return
	}
	sqlText := strings.TrimSpace(r.FormValue("sql"))
	if sqlText == "" {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeBadRequest, "SQL migration tidak boleh kosong")
		return
	}
	if len(sqlText) > maxSQLBytes {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, httpx.CodeBadRequest, "SQL migration maksimal 1 MiB")
		return
	}
	p := authz.PrincipalFrom(r.Context())
	rec, err := m.execute(r.Context(), id, p.Username, sqlText)
	if err != nil {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "Migration gagal", "detail": err.Error(), "migration": rec})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"message": "Migration berhasil", "migration": rec})
}
func (m *Module) canExecute(w http.ResponseWriter, r *http.Request, id int64) bool {
	p := authz.PrincipalFrom(r.Context())
	for _, perm := range []authz.Permission{authz.PermDBCreateMigration, authz.PermDBExecuteMigration} {
		ok, err := m.Authz.Can(r.Context(), p, perm, authz.Database(id))
		if err != nil {
			httpx.Internal(w, r, err)
			return false
		}
		if !ok {
			httpx.Forbidden(w)
			return false
		}
	}
	return true
}
func (m *Module) execute(ctx context.Context, id int64, executor, sqlText string) (Record, error) {
	target, err := m.Conns.Pool(ctx, id)
	if err != nil {
		return Record{}, err
	}
	conn, err := target.Conn(ctx)
	if err != nil {
		return Record{}, err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, advisoryLockKey); err != nil {
		return Record{}, fmt.Errorf("gagal memperoleh migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, advisoryLockKey)
	}()
	var version int64
	if err = m.PlatformDB.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0)+1 FROM platform_migration_history WHERE database_id=$1`, id).Scan(&version); err != nil {
		return Record{}, fmt.Errorf("gagal menentukan versi migration: %w", err)
	}
	var rec Record
	err = m.PlatformDB.QueryRowContext(ctx, `INSERT INTO platform_migration_history(database_id,version,sql_text,executor,status) VALUES($1,$2,$3,$4,'running') RETURNING id,database_id,version,sql_text,executor,executed_at,status`, id, version, sqlText, executor).Scan(&rec.ID, &rec.DatabaseID, &rec.Version, &rec.SQL, &rec.Executor, &rec.ExecutedAt, &rec.Status)
	if err != nil {
		return Record{}, fmt.Errorf("gagal mencatat migration: %w", err)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		m.markFailure(ctx, rec.ID, err)
		rec.Status = "failed"
		rec.Error = err.Error()
		return rec, err
	}
	if _, err = tx.ExecContext(ctx, sqlText); err != nil {
		_ = tx.Rollback()
		m.markFailure(ctx, rec.ID, err)
		rec.Status = "failed"
		rec.Error = err.Error()
		return rec, fmt.Errorf("SQL dieksekusi gagal dan transaksi di-rollback: %w", err)
	}
	if err = tx.Commit(); err != nil {
		m.markFailure(ctx, rec.ID, err)
		rec.Status = "failed"
		rec.Error = err.Error()
		return rec, fmt.Errorf("commit gagal: %w", err)
	}
	if err = m.PlatformDB.QueryRowContext(ctx, `UPDATE platform_migration_history SET status='success', error_message=NULL, executed_at=now() WHERE id=$1 RETURNING executed_at`, rec.ID).Scan(&rec.ExecutedAt); err != nil {
		return rec, fmt.Errorf("migration sudah commit tetapi gagal memperbarui history: %w", err)
	}
	rec.Status = "success"
	return rec, nil
}
func (m *Module) markFailure(ctx context.Context, id int64, runErr error) {
	logCtx := ctx
	if ctx.Err() != nil {
		logCtx = context.Background()
	}
	_, err := m.PlatformDB.ExecContext(logCtx, `UPDATE platform_migration_history SET status='failed',error_message=$2,executed_at=now() WHERE id=$1`, id, runErr.Error())
	if err != nil {
		log.Printf("[Migration] gagal mencatat status failed untuk migration %d: %v", id, err)
	}
}
func (m *Module) list(ctx context.Context, id int64) ([]Record, error) {
	rows, err := m.PlatformDB.QueryContext(ctx, `SELECT id,database_id,version,sql_text,executor,executed_at,status,COALESCE(error_message,'') FROM platform_migration_history WHERE database_id=$1 ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	for rows.Next() {
		var v Record
		if err := rows.Scan(&v.ID, &v.DatabaseID, &v.Version, &v.SQL, &v.Executor, &v.ExecutedAt, &v.Status, &v.Error); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type editorPage struct {
	DatabaseID int64
	SQL        string
	CanEdit    bool
	Error      string
}

func (m *Module) pageEditor(w http.ResponseWriter, r *http.Request) {
	id, ok := databaseID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	access, _ := authz.DBAccessFrom(r.Context())
	m.Views.Render(w, r, http.StatusOK, "migration/editor.html", "SQL Migration", editorPage{DatabaseID: id, CanEdit: access.Can(authz.PermDBExecuteMigration)})
}
func (m *Module) pageHistory(w http.ResponseWriter, r *http.Request) {
	id, ok := databaseID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	rows, err := m.list(r.Context(), id)
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	m.Views.Render(w, r, http.StatusOK, "migration/history.html", "Migration History", map[string]any{"DatabaseID": id, "Migrations": rows})
}
func (m *Module) pageExecute(w http.ResponseWriter, r *http.Request) {
	id, ok := databaseID(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !m.canExecute(w, r, id) {
		return
	}
	sqlText := strings.TrimSpace(r.FormValue("sql"))
	if sqlText == "" || len(sqlText) > maxSQLBytes {
		http.Error(w, "SQL migration kosong atau terlalu besar", http.StatusUnprocessableEntity)
		return
	}
	p := authz.PrincipalFrom(r.Context())
	rec, err := m.execute(r.Context(), id, p.Username, sqlText)
	if err != nil {
		m.Views.Render(w, r, http.StatusUnprocessableEntity, "migration/editor.html", "SQL Migration", editorPage{DatabaseID: id, SQL: sqlText, CanEdit: true, Error: err.Error()})
		return
	}
	web.SetFlash(w, "success", fmt.Sprintf("Migration v%d berhasil dijalankan.", rec.Version))
	web.Redirect(w, r, fmt.Sprintf("/databases/%d/migrations/history", id))
}
func databaseID(r *http.Request) (int64, bool) {
	id, e := strconv.ParseInt(r.PathValue("dbID"), 10, 64)
	return id, e == nil && id > 0
}
