package view

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/csrf"
)

// TemplateData adalah data standar yang dikirim ke setiap template Go
type TemplateData struct {
	Title        string
	CurrentUser  *auth.User
	CSRFToken    string
	FlashSuccess string
	FlashError   string
	ActiveMenu   string
	Data         interface{}
}

// Engine bertanggung jawab untuk kompilasi dan rendering Go HTML Templates
type Engine struct {
	templatesDir string
	isDev        bool
	cache        map[string]*template.Template
	mu           sync.RWMutex
	funcMap      template.FuncMap
}

// NewEngine membuat instance template engine baru
func NewEngine(templatesDir string, isDev bool) *Engine {
	e := &Engine{
		templatesDir: templatesDir,
		isDev:        isDev,
		cache:        make(map[string]*template.Template),
		funcMap: template.FuncMap{
			"formatDate": func(t time.Time) string {
				if t.IsZero() {
					return "-"
				}
				return t.Format("02 Jan 2006 15:04")
			},
			"formatDateShort": func(t time.Time) string {
				if t.IsZero() {
					return "-"
				}
				return t.Format("02 Jan 2006")
			},
			"add": func(a, b int) int {
				return a + b
			},
			"csrfField": func(token string) template.HTML {
				return csrf.TemplateField(token)
			},
		},
	}
	return e
}

// Render me-render template halaman dengan base layout
func (e *Engine) Render(w http.ResponseWriter, r *http.Request, statusCode int, pageTemplate string, data TemplateData) {
	// Auto-inject CurrentUser dari context jika belum diset
	if data.CurrentUser == nil {
		data.CurrentUser = auth.CurrentUser(r.Context())
	}

	// Auto-inject CSRFToken dari context jika belum diset
	if data.CSRFToken == "" {
		data.CSRFToken = csrf.Token(r.Context())
		if data.CSRFToken == "" {
			if cookie, err := r.Cookie(csrf.CSRFCookieName); err == nil {
				data.CSRFToken = cookie.Value
			}
		}

	}

	// Auto-inject Flash Message dari Cookie jika belum ada
	if data.FlashSuccess == "" && data.FlashError == "" {
		s, errStr := PopFlashes(w, r)
		data.FlashSuccess = s
		data.FlashError = errStr
	}

	tmpl, err := e.getTemplate(pageTemplate)
	if err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)

	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {
		// Log template execution error jika terjadi
		fmt.Fprintf(os.Stderr, "[View] Error executing template %s: %v\n", pageTemplate, err)
	}
}

// getTemplate mengambil template dari cache atau mem-parse file dari filesystem
func (e *Engine) getTemplate(pageTemplate string) (*template.Template, error) {
	if !e.isDev {
		e.mu.RLock()
		tmpl, ok := e.cache[pageTemplate]
		e.mu.RUnlock()
		if ok {
			return tmpl, nil
		}
	}

	// Kumpulkan file: base layout + partials + target page template
	files := []string{
		filepath.Join(e.templatesDir, "layouts", "base.html"),
		filepath.Join(e.templatesDir, "layouts", "navbar.html"),
		filepath.Join(e.templatesDir, "layouts", "alerts.html"),
		filepath.Join(e.templatesDir, pageTemplate),
	}

	// Verifikasi apakah file layout dan template ada
	for _, f := range files {
		if _, err := os.Stat(f); err != nil {
			return nil, fmt.Errorf("file template tidak ditemukan (%s): %w", f, err)
		}
	}

	tmpl, err := template.New("base").Funcs(e.funcMap).ParseFiles(files...)
	if err != nil {
		return nil, fmt.Errorf("gagal parse file template %s: %w", pageTemplate, err)
	}

	if !e.isDev {
		e.mu.Lock()
		e.cache[pageTemplate] = tmpl
		e.mu.Unlock()
	}

	return tmpl, nil
}

// SetFlashSuccess memasang flash message sukses pada cookie
func SetFlashSuccess(w http.ResponseWriter, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "flash_success",
		Value:    url.QueryEscape(message),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60,
	})
}

// SetFlashError memasang flash message error pada cookie
func SetFlashError(w http.ResponseWriter, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "flash_error",
		Value:    url.QueryEscape(message),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60,
	})
}

// PopFlashes membaca dan menghapus flash message dari cookie
func PopFlashes(w http.ResponseWriter, r *http.Request) (success string, errStr string) {
	if c, err := r.Cookie("flash_success"); err == nil && c.Value != "" {
		if val, err := url.QueryUnescape(c.Value); err == nil {
			success = val
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "flash_success",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
		})
	}

	if c, err := r.Cookie("flash_error"); err == nil && c.Value != "" {
		if val, err := url.QueryUnescape(c.Value); err == nil {
			errStr = val
		}
		http.SetCookie(w, &http.Cookie{
			Name:     "flash_error",
			Value:    "",
			Path:     "/",
			HttpOnly: true,
			MaxAge:   -1,
		})
	}

	return success, errStr
}
