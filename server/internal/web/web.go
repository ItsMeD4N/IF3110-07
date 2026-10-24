package web

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/csrf"
)

type Views struct {
	dir    string
	reload bool

	mu    sync.RWMutex
	pages map[string]*template.Template
}

func FindClientDir() (string, error) {
	cands := []string{os.Getenv("CLIENT_DIR"), "../client", "client"}
	for _, c := range cands {
		if c == "" {
			continue
		}
		if st, err := os.Stat(filepath.Join(c, "templates")); err == nil && st.IsDir() {
			return c, nil
		}
	}
	return "", fmt.Errorf("web: folder client/templates tidak ditemukan (set CLIENT_DIR)")
}

func New(clientDir string, reload bool) (*Views, error) {
	v := &Views{dir: filepath.Join(clientDir, "templates"), reload: reload}
	if err := v.load(); err != nil {
		return nil, err
	}
	return v, nil
}

func (v *Views) load() error {
	layouts, err := filepath.Glob(filepath.Join(v.dir, "layouts", "*.html"))
	if err != nil {
		return err
	}
	if len(layouts) == 0 {
		return fmt.Errorf("web: tidak ada layout di %s/layouts", v.dir)
	}
	base, err := template.New("").Funcs(funcs).ParseFiles(layouts...)
	if err != nil {
		return fmt.Errorf("web: parse layout: %w", err)
	}

	pages := map[string]*template.Template{}
	err = filepath.WalkDir(v.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		rel, _ := filepath.Rel(v.dir, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "layouts/") {
			return nil
		}
		t, err := base.Clone()
		if err != nil {
			return err
		}
		if _, err := t.ParseFiles(path); err != nil {
			return fmt.Errorf("web: parse %s: %w", rel, err)
		}
		pages[rel] = t
		return nil
	})
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.pages = pages
	v.mu.Unlock()
	return nil
}

type Page struct {
	Title     string
	User      *auth.User
	CSRFField template.HTML
	Flash     *Flash
	Path      string
	Data      any
}

func (v *Views) Render(w http.ResponseWriter, r *http.Request, status int, page string, title string, data any) {
	if v.reload {
		if err := v.load(); err != nil {
			log.Printf("[Web] reload template gagal: %v", err)
		}
	}
	v.mu.RLock()
	t, ok := v.pages[page]
	v.mu.RUnlock()
	if !ok {
		log.Printf("[Web] template %q tidak ditemukan", page)
		http.Error(w, "Template tidak ditemukan", http.StatusInternalServerError)
		return
	}

	p := Page{
		Title:     title,
		User:      auth.CurrentUser(r.Context()),
		CSRFField: csrf.TemplateField(csrf.Token(r.Context())),
		Flash:     takeFlash(w, r),
		Path:      r.URL.Path,
		Data:      data,
	}
	var buf bytes.Buffer
	if err := t.ExecuteTemplate(&buf, "base", p); err != nil {
		log.Printf("[Web] render %s: %v", page, err)
		http.Error(w, "Gagal merender halaman", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

type ErrorData struct {
	Status  int
	Heading string
	Message string
}

func (v *Views) Error(w http.ResponseWriter, r *http.Request, status int, heading, message string) {
	v.Render(w, r, status, "errors/error.html", heading, ErrorData{Status: status, Heading: heading, Message: message})
}

func StaticHandler(clientDir string) http.Handler {
	return http.StripPrefix("/static/", http.FileServer(http.Dir(filepath.Join(clientDir, "static"))))
}

type Flash struct {
	Kind    string
	Message string
}

const flashCookie = "flash"

func SetFlash(w http.ResponseWriter, kind, message string) {
	val := base64.RawURLEncoding.EncodeToString([]byte(kind + "\x00" + message))
	http.SetCookie(w, &http.Cookie{
		Name: flashCookie, Value: val, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, MaxAge: 60,
	})
}

func takeFlash(w http.ResponseWriter, r *http.Request) *Flash {
	c, err := r.Cookie(flashCookie)
	if err != nil || c.Value == "" {
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: flashCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1, Expires: time.Unix(0, 0)})
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return nil
	}
	kind, msg, ok := strings.Cut(string(raw), "\x00")
	if !ok {
		return nil
	}
	return &Flash{Kind: kind, Message: msg}
}

func Redirect(w http.ResponseWriter, r *http.Request, url string) {
	http.Redirect(w, r, url, http.StatusSeeOther)
}

var funcs = template.FuncMap{

	"hasPrefix": strings.HasPrefix,
	"initial": func(s string) string {
		if s == "" {
			return "?"
		}
		return strings.ToUpper(s[:1])
	},
	"formatTime": func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.Local().Format("02 Jan 2006 15:04")
	},
	"join": strings.Join,
}
