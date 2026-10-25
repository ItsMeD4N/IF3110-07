package config

import "os"

type Config struct {
	Port        string
	DatabaseURL string
	AppEnv      string
}

func LoadConfig() *Config {
	return &Config{
		Port:        getEnv("PORT", "8080"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable"),
		AppEnv:      getEnv("APP_ENV", "development"),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
