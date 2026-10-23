package csrf

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

const (
	CSRFCookieName = "csrf_token"
	CSRFHeaderName = "X-CSRF-Token"
	CSRFFormField  = "csrf_token"
)

// GenerateToken membuat token anti-CSRF acak 32 bytes
func GenerateToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// Middleware menyediakan proteksi CSRF untuk request metode mutasi (POST, PUT, DELETE, PATCH)
func Middleware(isProd bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Lewatkan safe methods (GET, HEAD, OPTIONS)
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
				// Pastikan cookie CSRF selalu terpasang
				if _, err := r.Cookie(CSRFCookieName); err != nil {
					token, _ := GenerateToken()
					http.SetCookie(w, &http.Cookie{
						Name:     CSRFCookieName,
						Value:    token,
						Path:     "/",
						HttpOnly: false, // Boleh dibaca JS untuk header X-CSRF-Token
						Secure:   isProd,
						SameSite: http.SameSiteLaxMode,
					})
				}
				next.ServeHTTP(w, r)
				return
			}

			// Validasi untuk metode mutasi
			cookie, err := r.Cookie(CSRFCookieName)
			if err != nil || cookie.Value == "" {
				http.Error(w, `{"error":"Missing CSRF cookie"}`, http.StatusForbidden)
				return
			}

			// Cek dari header atau form body
			clientToken := r.Header.Get(CSRFHeaderName)
			if clientToken == "" {
				clientToken = r.FormValue(CSRFFormField)
			}

			if clientToken == "" || clientToken != cookie.Value {
				http.Error(w, `{"error":"Invalid or missing CSRF token"}`, http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
