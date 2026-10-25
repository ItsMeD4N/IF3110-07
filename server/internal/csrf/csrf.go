package csrf

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
)

const (
	CSRFCookieName = "csrf_token"
	CSRFHeaderName = "X-CSRF-Token"
	CSRFFormField  = "csrf_token"
)

type contextKey string

const csrfContextKey contextKey = "csrf_token"

func Token(ctx context.Context) string {
	if t, ok := ctx.Value(csrfContextKey).(string); ok {
		return t
	}
	return ""
}

func WithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, csrfContextKey, token)
}

func TemplateField(token string) template.HTML {
	return template.HTML(fmt.Sprintf(`<input type="hidden" name="%s" value="%s">`, CSRFFormField, token))
}

func GenerateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func Middleware(isProd bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
				token := ""
				cookie, err := r.Cookie(CSRFCookieName)
				if err != nil || cookie.Value == "" {
					newToken, genErr := GenerateToken()
					if genErr == nil {
						token = newToken
						http.SetCookie(w, &http.Cookie{
							Name:     CSRFCookieName,
							Value:    token,
							Path:     "/",
							HttpOnly: false,
							Secure:   isProd,
							SameSite: http.SameSiteLaxMode,
						})
					}
				} else {
					token = cookie.Value
				}

				if token != "" {
					r = r.WithContext(WithToken(r.Context(), token))
				}
				next.ServeHTTP(w, r)
				return
			}

			cookie, err := r.Cookie(CSRFCookieName)
			if err != nil || cookie.Value == "" {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "Missing CSRF cookie",
				})
				return
			}

			clientToken := r.Header.Get(CSRFHeaderName)
			if clientToken == "" {
				clientToken = r.FormValue(CSRFFormField)
			}

			if clientToken == "" || subtle.ConstantTimeCompare([]byte(clientToken), []byte(cookie.Value)) != 1 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusForbidden)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error": "Invalid or missing CSRF token",
				})
				return
			}

			r = r.WithContext(WithToken(r.Context(), cookie.Value))
			next.ServeHTTP(w, r)
		})
	}
}

func HandleGetToken(w http.ResponseWriter, r *http.Request) {
	token := Token(r.Context())
	if token == "" {
		if c, err := r.Cookie(CSRFCookieName); err == nil {
			token = c.Value
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"csrf_token": token,
	})
}

func RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/csrf-token", HandleGetToken)
}
