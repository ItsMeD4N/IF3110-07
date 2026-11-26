package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Port              string
	DatabaseURL       string
	AppEnv            string
	LogLevel          string
	WorkerConcurrency int
	EmbeddedWorker    bool
	RequireApproval   bool
	OTelServiceName   string
	OTelExporter      string
	OTelOTLPEndpoint  string
}

func LoadConfig() *Config {
	appEnv := getEnv("APP_ENV", "development")
	defaultExporter := "stdout"
	if appEnv == "test" {
		defaultExporter = "noop"
	}
	return &Config{
		Port:              getEnv("PORT", "8080"),
		DatabaseURL:       getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable"),
		AppEnv:            appEnv,
		LogLevel:          getEnv("LOG_LEVEL", "info"),
		WorkerConcurrency: getEnvInt("WORKER_CONCURRENCY", 4),
		EmbeddedWorker:    getEnvBool("EMBEDDED_WORKER", true),
		RequireApproval:   getEnvBool("REQUIRE_APPROVAL", false),
		OTelServiceName:   getEnv("OTEL_SERVICE_NAME", "if3110-db-platform"),
		OTelExporter:      getEnv("OTEL_TRACES_EXPORTER", defaultExporter),
		OTelOTLPEndpoint:  getEnv("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4318"),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getEnvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

func getEnvBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	default:
		return def
	}
}
