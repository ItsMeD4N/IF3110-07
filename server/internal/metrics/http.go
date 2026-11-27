package metrics

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

var apiRoutes = map[string]bool{
	"auth": true, "authz": true, "admin": true, "databases": true, "approvals": true,
	"csrf-token": true, "health": true, "users": true, "me": true,
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.ResponseWriter.Write(b)
}

func (r *recorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func RouteLabel(path string) string {
	switch {
	case path == "/metrics":
		return "metrics"
	case strings.HasPrefix(path, "/api/"):
		rest := strings.TrimPrefix(path, "/api/")
		seg := rest
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			seg = rest[:i]
		}
		if !apiRoutes[seg] {
			return "api_other"
		}
		if seg == "databases" && isJobSubmission(rest) {
			return "databases_enqueue"
		}
		return seg
	default:
		return "spa"
	}
}

func isJobSubmission(rest string) bool {
	for _, suffix := range []string{"/execute", "/rollback", "/forward", "/resume", "/revert"} {
		if strings.HasSuffix(rest, suffix) {
			return true
		}
	}
	return false
}

func methodLabel(m string) string {
	switch m {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
		return m
	}
	return "OTHER"
}

func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &recorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		route := RouteLabel(r.URL.Path)
		method := methodLabel(r.Method)
		httpRequests.Inc(method, route, strconv.Itoa(status/100)+"xx")
		httpDuration.ObserveDuration(time.Since(start), method, route)
	})
}
