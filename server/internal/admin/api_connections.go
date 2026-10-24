package admin

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
	"github.com/ItsMeD4N/IF3110-07/server/internal/store"
)

var validSSLModes = map[string]bool{
	"disable": true, "allow": true, "prefer": true, "require": true, "verify-ca": true, "verify-full": true,
}

type connectionRequest struct {
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	Host         string  `json:"host"`
	Port         int     `json:"port"`
	DatabaseName string  `json:"database_name"`
	Username     string  `json:"username"`
	Password     *string `json:"password"`
	SSLMode      string  `json:"ssl_mode"`

	TestBeforeSave bool `json:"test_before_save"`

	ConnectionID int64 `json:"connection_id"`
}

func (req *connectionRequest) normalize() {
	req.Name = store.NormalizeName(req.Name)
	req.Host = strings.TrimSpace(req.Host)
	req.DatabaseName = strings.TrimSpace(req.DatabaseName)
	req.Username = strings.TrimSpace(req.Username)
	req.SSLMode = strings.ToLower(strings.TrimSpace(req.SSLMode))
	if req.SSLMode == "" {
		req.SSLMode = "disable"
	}
	if req.Port == 0 {
		req.Port = 5432
	}
}

func (req *connectionRequest) validate(requireName bool) string {
	req.normalize()
	switch {
	case requireName && req.Name == "":
		return "Nama koneksi wajib diisi."
	case utf8.RuneCountInString(req.Name) > maxNameLen:
		return "Nama koneksi maksimal 64 karakter."
	case utf8.RuneCountInString(req.Description) > maxDescriptionLen:
		return "Deskripsi maksimal 500 karakter."
	case req.Host == "":
		return "Host wajib diisi."
	case len(req.Host) > 255 || strings.ContainsAny(req.Host, " /?#@"):
		return "Host tidak valid."
	case req.Port < 1 || req.Port > 65535:
		return "Port harus di antara 1 dan 65535."
	case req.DatabaseName == "":
		return "Nama database wajib diisi."
	case len(req.DatabaseName) > 63:
		return "Nama database maksimal 63 karakter."
	case req.Username == "":
		return "Username wajib diisi."
	case len(req.Username) > 63:
		return "Username maksimal 63 karakter."
	case !validSSLModes[req.SSLMode]:
		return "ssl_mode tidak valid."
	case req.Password != nil && len(*req.Password) > 1024:
		return "Password terlalu panjang."
	}
	return ""
}

func (req *connectionRequest) input() store.ConnectionInput {
	return store.ConnectionInput{
		Name: req.Name, Description: req.Description, Host: req.Host, Port: req.Port,
		DatabaseName: req.DatabaseName, Username: req.Username, SSLMode: req.SSLMode,
	}
}

func (req *connectionRequest) config(password string) connmgr.Config {
	return connmgr.Config{
		Host: req.Host, Port: req.Port, Database: req.DatabaseName,
		Username: req.Username, Password: password, SSLMode: req.SSLMode,
	}
}

func (req *connectionRequest) password() string {
	if req.Password == nil {
		return ""
	}
	return *req.Password
}

const connConflictMsg = "Nama koneksi sudah digunakan."

func (m *Module) apiListConnections(w http.ResponseWriter, r *http.Request) {
	conns, err := m.Store.ListConnections(r.Context())
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"connections": conns})
}

func (m *Module) apiGetConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, "connectionID")
	if !ok {
		return
	}
	c, err := m.Store.GetConnection(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (m *Module) apiCreateConnection(w http.ResponseWriter, r *http.Request) {
	var req connectionRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if msg := req.validate(true); msg != "" {
		httpx.ValidationError(w, msg)
		return
	}
	if req.TestBeforeSave {
		if res := m.Conns.Test(r.Context(), req.config(req.password())); !res.OK {
			writeTestFailed(w, res)
			return
		}
	}
	c, err := m.Store.CreateConnection(r.Context(), req.input(), req.password())
	if err != nil {
		writeStoreErr(w, r, err, connConflictMsg)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, c)
}

func (m *Module) apiUpdateConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, "connectionID")
	if !ok {
		return
	}
	var req connectionRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if msg := req.validate(true); msg != "" {
		httpx.ValidationError(w, msg)
		return
	}

	var newPassword *string
	if req.Password != nil && *req.Password != "" {
		newPassword = req.Password
	}
	pw, ok := m.resolvePassword(w, r, id, newPassword, req.config(""))
	if !ok {
		return
	}
	if req.TestBeforeSave {
		if res := m.Conns.Test(r.Context(), req.config(pw)); !res.OK {
			writeTestFailed(w, res)
			return
		}
	}
	c, err := m.Store.UpdateConnection(r.Context(), id, req.input(), newPassword)
	if err != nil {
		writeStoreErr(w, r, err, connConflictMsg)
		return
	}
	m.Conns.Invalidate(id)
	httpx.WriteJSON(w, http.StatusOK, c)
}

func (m *Module) apiDeleteConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, "connectionID")
	if !ok {
		return
	}
	affected, err := m.Store.DeleteConnection(r.Context(), id)
	if err != nil {
		writeStoreErr(w, r, err, "")
		return
	}
	m.Conns.Invalidate(id)
	m.privilegeChanged(r.Context(), affected, "connection_deleted")
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) apiTestNewConnection(w http.ResponseWriter, r *http.Request) {
	var req connectionRequest
	if !httpx.DecodeJSON(w, r, &req) {
		return
	}
	if msg := req.validate(false); msg != "" {
		httpx.ValidationError(w, msg)
		return
	}
	var newPassword *string
	if req.Password != nil && *req.Password != "" {
		newPassword = req.Password
	}
	pw := ""
	if newPassword != nil {
		pw = *newPassword
	} else if req.ConnectionID > 0 {
		var ok bool
		if pw, ok = m.resolvePassword(w, r, req.ConnectionID, nil, req.config("")); !ok {
			return
		}
	}
	httpx.WriteJSON(w, http.StatusOK, m.Conns.Test(r.Context(), req.config(pw)))
}

func (m *Module) apiTestSavedConnection(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, "connectionID")
	if !ok {
		return
	}
	res, err := m.Conns.TestSaved(r.Context(), id)
	if errors.Is(err, connmgr.ErrNotFound) {
		httpx.NotFound(w)
		return
	}
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

const MsgPasswordRequired = "Password wajib diisi ulang karena host, port, database, username, atau SSL mode berubah."

var errPasswordRequired = errors.New("admin: password wajib diisi ulang untuk target koneksi baru")

func (m *Module) storedPassword(ctx context.Context, id int64, target connmgr.Config) (string, error) {
	cfg, found, err := m.Store.ConnectionConfig(ctx, id)
	if err != nil {
		return "", err
	}
	if !found {
		return "", store.ErrNotFound
	}
	if !cfg.SameTarget(target) {
		return "", errPasswordRequired
	}
	return cfg.Password, nil
}

func (m *Module) resolvePassword(w http.ResponseWriter, r *http.Request, id int64, newPassword *string, target connmgr.Config) (string, bool) {
	if newPassword != nil {
		return *newPassword, true
	}
	pw, err := m.storedPassword(r.Context(), id, target)
	switch {
	case err == nil:
		return pw, true
	case errors.Is(err, errPasswordRequired):
		httpx.ValidationError(w, MsgPasswordRequired)
	case errors.Is(err, store.ErrNotFound):
		httpx.NotFound(w)
	default:
		httpx.Internal(w, r, err)
	}
	return "", false
}

func writeTestFailed(w http.ResponseWriter, res connmgr.TestResult) {
	httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeConnectionFailed,
		"Tes koneksi gagal, koneksi tidak disimpan: "+res.Error)
}

func logWarn(r *http.Request, msg string, err error) {
	log.Printf("[Admin] %s: method=%s path=%s err=%v", msg, r.Method, r.URL.Path, err)
}
