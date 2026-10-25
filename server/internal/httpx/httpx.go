package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
)

const (
	CodeUnauthenticated  = "unauthenticated"
	CodeForbidden        = "forbidden"
	CodeNotFound         = "not_found"
	CodeBadRequest       = "bad_request"
	CodeValidation       = "validation_error"
	CodeConflict         = "conflict"
	CodeConnectionFailed = "db_connection_failed"
	CodePlatformTarget   = "platform_database"
	CodeInternal         = "internal_error"
)

const (
	MsgUnauthenticated = "Anda harus login terlebih dahulu."
	MsgForbidden       = "Anda tidak memiliki izin untuk melakukan aksi ini."
	MsgNotFound        = "Resource tidak ditemukan atau Anda tidak memiliki akses."
	MsgInternal        = "Terjadi kesalahan pada server."

	MsgConnectionFailed = "Tidak dapat terhubung ke database. Coba lagi nanti atau hubungi Platform Admin."
	MsgPlatformTarget   = "Koneksi ini menunjuk ke database platform sehingga tidak boleh dikelola. Hubungi Platform Admin."
)

type ErrorBody struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("httpx: encode response", "err", err)
	}
}

func WriteError(w http.ResponseWriter, status int, code, message string) {
	WriteJSON(w, status, ErrorBody{Error: message, Code: code})
}

func Unauthenticated(w http.ResponseWriter) {
	WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, MsgUnauthenticated)
}

func Forbidden(w http.ResponseWriter) {
	WriteError(w, http.StatusForbidden, CodeForbidden, MsgForbidden)
}

func NotFound(w http.ResponseWriter) {
	WriteError(w, http.StatusNotFound, CodeNotFound, MsgNotFound)
}

func Internal(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	WriteError(w, http.StatusInternalServerError, CodeInternal, MsgInternal)
}

func ConnStatus(err error) (status int, code, msg string, ok bool) {
	switch {
	case errors.Is(err, connmgr.ErrNotFound):
		return http.StatusNotFound, CodeNotFound, MsgNotFound, true
	case errors.Is(err, connmgr.ErrPlatformTarget):
		return http.StatusConflict, CodePlatformTarget, MsgPlatformTarget, true
	case errors.Is(err, connmgr.ErrConnectionFailed):
		return http.StatusServiceUnavailable, CodeConnectionFailed, MsgConnectionFailed, true
	}
	return 0, "", "", false
}

func ConnError(w http.ResponseWriter, r *http.Request, err error) {
	status, code, msg, ok := ConnStatus(err)
	if !ok {
		Internal(w, r, err)
		return
	}
	if status == http.StatusServiceUnavailable {
		slog.Warn("koneksi database target gagal", "path", r.URL.Path, "err", err)
	}
	WriteError(w, status, code, msg)
}

const MaxBodyBytes = 1 << 20

func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		msg := "Body JSON tidak valid."
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			msg = "Body terlalu besar."
		} else if errors.Is(err, io.EOF) {
			msg = "Body JSON kosong."
		} else if strings.HasPrefix(err.Error(), "json: unknown field") {
			msg = "Field tidak dikenal: " + strings.TrimPrefix(err.Error(), "json: unknown field ")
		}
		WriteError(w, http.StatusBadRequest, CodeBadRequest, msg)
		return false
	}
	return true
}

func PathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		NotFound(w)
		return 0, false
	}
	return id, true
}

func ValidationError(w http.ResponseWriter, message string) {
	WriteError(w, http.StatusUnprocessableEntity, CodeValidation, message)
}
