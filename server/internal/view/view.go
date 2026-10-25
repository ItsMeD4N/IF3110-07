package view

import (
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/csrf"
)

type Flash struct {
	Kind    string
	Message string
}

type TemplateData struct {
	Title        string
	User         *auth.User
	CurrentUser  *auth.User
	CSRFField    template.HTML
	CSRFToken    string
	Flash        *Flash
	FlashSuccess string
	FlashError   string
	ActiveMenu   string
	Path         string
	Data         interface{}
}

type Engine struct {
	templatesDir string
	isDev        bool
	cache        map[string]*template.Template
	mu           sync.RWMutex
	funcMap      template.FuncMap
}

func NewEngine(templatesDir string, isDev bool) *Engine {
	e := &Engine{
		templatesDir: templatesDir,
		isDev:        isDev,
		cache:        make(map[string]*template.Template),
		funcMap: template.FuncMap{
			"hasPrefix": strings.HasPrefix,
			"initial": func(s string) string {
				if s == "" {
					return "?"
				}
				return strings.ToUpper(s[:1])
			},
			"formatTime": func(t time.Time) string {
				if t.IsZero() {
					return "-"
				}
				return t.Format("02 Jan 2006 15:04")
			},
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

func (e *Engine) Render(w http.ResponseWriter, r *http.Request, statusCode int, pageTemplate string, data TemplateData) {

	if data.CurrentUser == nil {
		data.CurrentUser = auth.CurrentUser(r.Context())
	}
	data.User = data.CurrentUser
	data.Path = r.URL.Path

	if data.CSRFToken == "" {
		data.CSRFToken = csrf.Token(r.Context())
		if data.CSRFToken == "" {
			if cookie, err := r.Cookie(csrf.CSRFCookieName); err == nil {
				data.CSRFToken = cookie.Value
			}
		}
	}
	data.CSRFField = csrf.TemplateField(data.CSRFToken)

	if data.FlashSuccess == "" && data.FlashError == "" {
		s, errStr := PopFlashes(w, r)
		data.FlashSuccess = s
		data.FlashError = errStr
		if s != "" {
			data.Flash = &Flash{Kind: "ok", Message: s}
		} else if errStr != "" {
			data.Flash = &Flash{Kind: "error", Message: errStr}
		}
	}

	tmpl, err := e.getTemplate(pageTemplate)
	if err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(statusCode)

	if err := tmpl.ExecuteTemplate(w, "base", data); err != nil {

		fmt.Fprintf(os.Stderr, "[View] Error executing template %s: %v\n", pageTemplate, err)
	}
}

func (e *Engine) getTemplate(pageTemplate string) (*template.Template, error) {
	if !e.isDev {
		e.mu.RLock()
		tmpl, ok := e.cache[pageTemplate]
		e.mu.RUnlock()
		if ok {
			return tmpl, nil
		}
	}

	files := []string{
		filepath.Join(e.templatesDir, "layouts", "base.html"),
		filepath.Join(e.templatesDir, "layouts", "navbar.html"),
		filepath.Join(e.templatesDir, "layouts", "alerts.html"),
		filepath.Join(e.templatesDir, pageTemplate),
	}

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

func SetFlashSuccess(w http.ResponseWriter, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "flash_success",
		Value:    url.QueryEscape(message),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60,
	})
	val := base64.RawURLEncoding.EncodeToString([]byte("ok\x00" + message))
	http.SetCookie(w, &http.Cookie{
		Name:     "flash",
		Value:    val,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60,
	})
}

func SetFlashError(w http.ResponseWriter, message string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "flash_error",
		Value:    url.QueryEscape(message),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60,
	})
	val := base64.RawURLEncoding.EncodeToString([]byte("error\x00" + message))
	http.SetCookie(w, &http.Cookie{
		Name:     "flash",
		Value:    val,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   60,
	})
}

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
