package database

import (
	"os"
	"testing"
)

func TestInitDB_Integration(t *testing.T) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable"
	}

	db, err := InitDB(connStr)
	if err != nil {
		t.Fatalf("Gagal terhubung ke database PostgreSQL: %v", err)
	}
	defer db.Close()

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if err != nil {
		t.Fatalf("Gagal query tabel users: %v", err)
	}

	if count == 0 {
		t.Fatalf("Tabel users seharusnya berisi setidaknya 1 seed admin, ditemukan %d", count)
	}

	err = db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count)
	if err != nil {
		t.Fatalf("Gagal query tabel sessions: %v", err)
	}
}
