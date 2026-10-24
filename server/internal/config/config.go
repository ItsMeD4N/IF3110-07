package config

import (
	"os"
)

type Config struct {
	Port          string
	DatabaseURL   string
	AppEnv        string
	SessionSecret string
	CSRFSecret    string
	AllowedOrigin string
	StaticDir     string
	TemplatesDir  string
}

func LoadConfig() *Config {
	port := getEnv("PORT", "8080")
	dbURL := getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable")
	appEnv := getEnv("APP_ENV", "development")
	sessionSecret := getEnv("SESSION_SECRET", "super-secret-session-key-change-in-prod-32bytes")
	csrfSecret := getEnv("CSRF_SECRET", "super-secret-csrf-key-change-in-prod-32bytes")
	allowedOrigin := getEnv("ALLOWED_ORIGIN", "http://localhost:5173")
	staticDir := getEnv("STATIC_DIR", resolveDir("client/static", "../client/static"))
	templatesDir := getEnv("TEMPLATES_DIR", resolveDir("client/templates", "../client/templates"))

	return &Config{
		Port:          port,
		DatabaseURL:   dbURL,
		AppEnv:        appEnv,
		SessionSecret: sessionSecret,
		CSRFSecret:    csrfSecret,
		AllowedOrigin: allowedOrigin,
		StaticDir:     staticDir,
		TemplatesDir:  templatesDir,
	}
}

func resolveDir(candidates ...string) string {
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && info.IsDir() {
			return c
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

