package migration

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
)

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	api := func(p authz.Permission, h http.HandlerFunc) http.Handler {
		return m.AuthMW.RequireAuth(m.MW.RequireDB(p, h))
	}
	mux.Handle("GET /api/databases/{dbID}/migrations", api(authz.PermDBViewMigrations, m.apiHistory))
	mux.Handle("GET /api/databases/{dbID}/migrations/{version}", api(authz.PermDBViewMigrations, m.apiDetail))
	mux.Handle("POST /api/databases/{dbID}/migrations", api(authz.PermDBCreateMigration, m.requireExecute(m.apiRun)))
	mux.Handle("POST /api/databases/{dbID}/migrations/explain", api(authz.PermDBViewMigrations, m.apiExplain))

	mux.Handle("GET /api/databases/{dbID}/migration-drafts", api(authz.PermDBViewMigrations, m.apiDrafts))
	mux.Handle("POST /api/databases/{dbID}/migration-drafts", api(authz.PermDBCreateMigration, m.apiCreateDraft))
	mux.Handle("GET /api/databases/{dbID}/migration-drafts/{draftID}", api(authz.PermDBViewMigrations, m.apiDraft))
	mux.Handle("PUT /api/databases/{dbID}/migration-drafts/{draftID}", api(authz.PermDBEditMigration, m.apiUpdateDraft))
	mux.Handle("DELETE /api/databases/{dbID}/migration-drafts/{draftID}", api(authz.PermDBEditMigration, m.apiDeleteDraft))
	mux.Handle("POST /api/databases/{dbID}/migration-drafts/{draftID}/execute", api(authz.PermDBExecuteMigration, m.apiRunDraft))

	if m.Views == nil {
		return
	}
	pmw := m.MW.WithDeniedHandler(m.Views.Denied)
	page := func(p authz.Permission, h http.HandlerFunc) http.Handler {
		return m.AuthMW.RequireAuth(pmw.RequireDB(p, h))
	}
	mux.Handle("GET /databases/{dbID}/migrations", page(authz.PermDBViewMigrations, m.pageEditor))
	mux.Handle("POST /databases/{dbID}/migrations", page(authz.PermDBCreateMigration, m.pageCreate))
	mux.Handle("GET /databases/{dbID}/migrations/history", page(authz.PermDBViewMigrations, m.pageHistory))
	mux.Handle("GET /databases/{dbID}/migrations/history/{version}", page(authz.PermDBViewMigrations, m.pageDetail))
	mux.Handle("GET /databases/{dbID}/migrations/drafts/{draftID}", page(authz.PermDBViewMigrations, m.pageDraft))
	mux.Handle("POST /databases/{dbID}/migrations/drafts/{draftID}", page(authz.PermDBEditMigration, m.pageUpdateDraft))
	mux.Handle("POST /databases/{dbID}/migrations/drafts/{draftID}/run", page(authz.PermDBExecuteMigration, m.pageRunDraft))
	mux.Handle("POST /databases/{dbID}/migrations/drafts/{draftID}/delete", page(authz.PermDBEditMigration, m.pageDeleteDraft))
}

func (m *Module) requireExecute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a, _ := authz.DBAccessFrom(r.Context()); !a.Can(authz.PermDBExecuteMigration) {
			httpx.Forbidden(w)
			return
		}
		next(w, r)
	}
}

func dbID(r *http.Request) int64 {
	a, _ := authz.DBAccessFrom(r.Context())
	return a.DatabaseID
}

func pathInt(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func executor(r *http.Request) string {
	if p := authz.PrincipalFrom(r.Context()); p != nil {
		return p.Username
	}
	return ""
}

type migrationInput struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}

func readInput(w http.ResponseWriter, r *http.Request) (migrationInput, bool) {
	var in migrationInput
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return in, httpx.DecodeJSON(w, r, &in)
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSQLBytes+64*1024)
	if err := r.ParseForm(); err != nil {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, httpx.CodeBadRequest, "Body request terlalu besar atau tidak valid.")
		return in, false
	}
	in.Name, in.SQL = r.FormValue("name"), r.FormValue("sql")
	return in, true
}

func writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	switch {
	case errors.As(err, &ve):
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeValidation, ve.Msg)
	case errors.Is(err, ErrDraftNotFound), errors.Is(err, ErrMigrationNotFound):
		httpx.NotFound(w)
	default:
		httpx.ConnError(w, r, err)
	}
}

func writeRunResult(w http.ResponseWriter, r *http.Request, rec Record, err error) {
	var ee *ExecError
	switch {
	case err == nil:
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"message": "Migration berhasil dan sudah di-commit.", "migration": rec})
	case errors.As(err, &ee):
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": "Migration gagal; semua perubahan di-rollback.", "code": "migration_failed", "detail": ee.Error(), "migration": rec,
		})
	default:
		writeErr(w, r, err)
	}
}

func (m *Module) apiHistory(w http.ResponseWriter, r *http.Request) {
	rows, err := m.History(r.Context(), dbID(r))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"migrations": rows})
}

func (m *Module) apiDetail(w http.ResponseWriter, r *http.Request) {
	version, ok := pathInt(r, "version")
	if !ok {
		httpx.NotFound(w)
		return
	}
	rec, err := m.Get(r.Context(), dbID(r), version)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, rec)
}

func (m *Module) apiRun(w http.ResponseWriter, r *http.Request) {
	in, ok := readInput(w, r)
	if !ok {
		return
	}
	rec, err := m.Run(r.Context(), dbID(r), executor(r), in.Name, in.SQL)
	writeRunResult(w, r, rec, err)
}

func (m *Module) apiDrafts(w http.ResponseWriter, r *http.Request) {
	drafts, err := m.Drafts(r.Context(), dbID(r))
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"drafts": drafts})
}

func (m *Module) apiCreateDraft(w http.ResponseWriter, r *http.Request) {
	in, ok := readInput(w, r)
	if !ok {
		return
	}
	d, err := m.CreateDraft(r.Context(), dbID(r), executor(r), in.Name, in.SQL)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, d)
}

func (m *Module) apiDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "draftID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	d, err := m.getDraft(r.Context(), dbID(r), id)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (m *Module) apiUpdateDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "draftID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	in, ok := readInput(w, r)
	if !ok {
		return
	}
	d, err := m.UpdateDraft(r.Context(), dbID(r), id, executor(r), in.Name, in.SQL)
	if err != nil {
		writeErr(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, d)
}

func (m *Module) apiDeleteDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "draftID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	if err := m.DeleteDraft(r.Context(), dbID(r), id); err != nil {
		writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) apiRunDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "draftID")
	if !ok {
		httpx.NotFound(w)
		return
	}
	rec, err := m.RunDraft(r.Context(), dbID(r), id, executor(r))
	writeRunResult(w, r, rec, err)
}

type editorPage struct {
	DatabaseID int64
	CanCreate  bool
	CanRun     bool
	Drafts     []Draft
	Name       string
	SQL        string
	Error      string
}

type draftPage struct {
	DatabaseID int64
	Draft      Draft
	CanEdit    bool
	CanRun     bool
	Error      string
}

type historyPage struct {
	DatabaseID int64
	Migrations []Record
}

type detailPage struct {
	DatabaseID int64
	Migration  Record
}

func (m *Module) pageErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrDraftNotFound), errors.Is(err, ErrMigrationNotFound):
		m.Views.Error(w, r, http.StatusNotFound, "Tidak ditemukan", "Draft atau migration ini tidak ada, mungkin sudah dijalankan atau dihapus.")
	default:
		m.Views.ConnError(w, r, err)
	}
}

func (m *Module) renderEditor(w http.ResponseWriter, r *http.Request, status int, p editorPage) {
	a, _ := authz.DBAccessFrom(r.Context())
	drafts, err := m.Drafts(r.Context(), a.DatabaseID)
	if err != nil {
		m.Views.Internal(w, r, err)
		return
	}
	p.DatabaseID, p.Drafts = a.DatabaseID, drafts
	p.CanCreate, p.CanRun = a.Can(authz.PermDBCreateMigration), a.Can(authz.PermDBExecuteMigration)
	m.Views.Render(w, r, status, "migration/editor.html", "Migration", p)
}

func (m *Module) pageEditor(w http.ResponseWriter, r *http.Request) {
	m.renderEditor(w, r, http.StatusOK, editorPage{})
}

func (m *Module) pageCreate(w http.ResponseWriter, r *http.Request) {
	in := editorPage{Name: r.FormValue("name"), SQL: r.FormValue("sql")}
	id := dbID(r)
	var ve *ValidationError

	if r.FormValue("action") == "run" {
		if a, _ := authz.DBAccessFrom(r.Context()); !a.Can(authz.PermDBExecuteMigration) {
			m.Views.Error(w, r, http.StatusForbidden, "Akses ditolak", httpx.MsgForbidden)
			return
		}
		rec, err := m.Run(r.Context(), id, executor(r), in.Name, in.SQL)
		if m.handleRunPageResult(w, r, id, rec, err) {
			return
		}
		if errors.As(err, &ve) {
			in.Error = ve.Msg
		} else {
			in.Error = "Migration gagal dan di-rollback: " + err.Error()
		}
		m.renderEditor(w, r, http.StatusUnprocessableEntity, in)
		return
	}

	d, err := m.CreateDraft(r.Context(), id, executor(r), in.Name, in.SQL)
	if errors.As(err, &ve) {
		in.Error = ve.Msg
		m.renderEditor(w, r, http.StatusUnprocessableEntity, in)
		return
	}
	if err != nil {
		m.Views.Internal(w, r, err)
		return
	}
	web.SetFlash(w, "success", "Draft disimpan. Tinjau lagi sebelum dijalankan.")
	web.Redirect(w, r, fmt.Sprintf("/databases/%d/migrations/drafts/%d", id, d.ID))
}

func (m *Module) handleRunPageResult(w http.ResponseWriter, r *http.Request, id int64, rec Record, err error) bool {
	var ve *ValidationError
	var ee *ExecError
	switch {
	case err == nil:
		web.SetFlash(w, "success", fmt.Sprintf("Migration v%d berhasil dan sudah di-commit.", rec.Version))
		web.Redirect(w, r, fmt.Sprintf("/databases/%d/migrations/history/%d", id, rec.Version))
		return true
	case errors.As(err, &ve), errors.As(err, &ee):
		return false
	default:
		m.pageErr(w, r, err)
		return true
	}
}

func (m *Module) loadDraftPage(w http.ResponseWriter, r *http.Request) (draftPage, bool) {
	id, ok := pathInt(r, "draftID")
	if !ok {
		m.pageErr(w, r, ErrDraftNotFound)
		return draftPage{}, false
	}
	a, _ := authz.DBAccessFrom(r.Context())
	d, err := m.getDraft(r.Context(), a.DatabaseID, id)
	if err != nil {
		m.pageErr(w, r, err)
		return draftPage{}, false
	}
	return draftPage{
		DatabaseID: a.DatabaseID, Draft: d,
		CanEdit: a.Can(authz.PermDBEditMigration), CanRun: a.Can(authz.PermDBExecuteMigration),
	}, true
}

func (m *Module) renderDraft(w http.ResponseWriter, r *http.Request, status int, p draftPage) {
	title := p.Draft.Name
	if title == "" {
		title = fmt.Sprintf("Draft #%d", p.Draft.ID)
	}
	m.Views.Render(w, r, status, "migration/draft.html", title, p)
}

func (m *Module) pageDraft(w http.ResponseWriter, r *http.Request) {
	if p, ok := m.loadDraftPage(w, r); ok {
		m.renderDraft(w, r, http.StatusOK, p)
	}
}

func (m *Module) pageUpdateDraft(w http.ResponseWriter, r *http.Request) {
	p, ok := m.loadDraftPage(w, r)
	if !ok {
		return
	}
	d, err := m.UpdateDraft(r.Context(), p.DatabaseID, p.Draft.ID, executor(r), r.FormValue("name"), r.FormValue("sql"))
	var ve *ValidationError
	if errors.As(err, &ve) {
		p.Draft.Name, p.Draft.SQL, p.Error = r.FormValue("name"), r.FormValue("sql"), ve.Msg
		m.renderDraft(w, r, http.StatusUnprocessableEntity, p)
		return
	}
	if err != nil {
		m.pageErr(w, r, err)
		return
	}
	web.SetFlash(w, "success", "Draft diperbarui.")
	web.Redirect(w, r, fmt.Sprintf("/databases/%d/migrations/drafts/%d", p.DatabaseID, d.ID))
}

func (m *Module) pageRunDraft(w http.ResponseWriter, r *http.Request) {
	p, ok := m.loadDraftPage(w, r)
	if !ok {
		return
	}
	rec, err := m.RunDraft(r.Context(), p.DatabaseID, p.Draft.ID, executor(r))
	if m.handleRunPageResult(w, r, p.DatabaseID, rec, err) {
		return
	}
	var ve *ValidationError
	if errors.As(err, &ve) {
		p.Error = ve.Msg
	} else {
		p.Error = fmt.Sprintf("Migration v%d gagal dan di-rollback: %s. Draft tetap disimpan, perbaiki lalu jalankan lagi.", rec.Version, err.Error())
	}
	m.renderDraft(w, r, http.StatusUnprocessableEntity, p)
}

func (m *Module) pageDeleteDraft(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "draftID")
	if !ok {
		m.pageErr(w, r, ErrDraftNotFound)
		return
	}
	if err := m.DeleteDraft(r.Context(), dbID(r), id); err != nil {
		m.pageErr(w, r, err)
		return
	}
	web.SetFlash(w, "success", "Draft dihapus.")
	web.Redirect(w, r, fmt.Sprintf("/databases/%d/migrations", dbID(r)))
}

func (m *Module) pageHistory(w http.ResponseWriter, r *http.Request) {
	rows, err := m.History(r.Context(), dbID(r))
	if err != nil {
		m.Views.Internal(w, r, err)
		return
	}
	m.Views.Render(w, r, http.StatusOK, "migration/history.html", "Riwayat migration", historyPage{DatabaseID: dbID(r), Migrations: rows})
}

func (m *Module) pageDetail(w http.ResponseWriter, r *http.Request) {
	version, ok := pathInt(r, "version")
	if !ok {
		m.pageErr(w, r, ErrMigrationNotFound)
		return
	}
	rec, err := m.Get(r.Context(), dbID(r), version)
	if err != nil {
		m.pageErr(w, r, err)
		return
	}
	m.Views.Render(w, r, http.StatusOK, "migration/detail.html", fmt.Sprintf("Migration v%d", rec.Version), detailPage{DatabaseID: dbID(r), Migration: rec})
}
