package web

import (
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
)

// FindSPADir locates the compiled SPA dist directory containing index.html.
// Checks SPA_DIST_DIR first, then <clientDir>/dist. Returns "" if not built yet.
func FindSPADir(clientDir string) string {
	cands := []string{
		strings.TrimSpace(os.Getenv("SPA_DIST_DIR")),
	}
	if clientDir != "" {
		cands = append(cands, filepath.Join(clientDir, "dist"))
	}
	cands = append(cands, "../client/dist", "client/dist")

	for _, dir := range cands {
		if dir == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(dir, "index.html")); err == nil && !st.IsDir() {
			return dir
		}
	}
	return ""
}

// SPAHandler serves built static SPA files from distDir and falls back to index.html
// for client-side routes. Unknown /api/* paths always receive JSON 404.
func SPAHandler(distDir string) http.Handler {
	rootFS := os.DirFS(distDir)
	fileServer := http.FileServer(http.FS(rootFS))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			httpx.NotFound(w)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			httpx.WriteError(w, http.StatusMethodNotAllowed, httpx.CodeBadRequest, "Method tidak diizinkan.")
			return
		}

		cleanPath := path.Clean("/" + r.URL.Path)
		rel := strings.TrimPrefix(cleanPath, "/")

		if rel != "" && rel != "." {
			if st, err := fs.Stat(rootFS, rel); err == nil && !st.IsDir() {
				if strings.HasPrefix(cleanPath, "/assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
			// Missing static assets under /assets/ must return 404, not HTML
			if strings.HasPrefix(cleanPath, "/assets/") {
				http.NotFound(w, r)
				return
			}
		}

		serveIndexHTML(w, r, rootFS)
	})
}

func serveIndexHTML(w http.ResponseWriter, r *http.Request, rootFS fs.FS) {
	f, err := rootFS.Open("index.html")
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		httpx.Internal(w, r, err)
		return
	}
	rs, ok := f.(io.ReadSeeker)
	if !ok {
		httpx.Internal(w, r, fs.ErrInvalid)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, "index.html", st.ModTime(), rs)
}
