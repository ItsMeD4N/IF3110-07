package database

import (
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/lib/pq"
)

// InitDB menginisialisasi koneksi pool ke database platform PostgreSQL
func InitDB(connStr string) (*sql.DB, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka koneksi database: %w", err)
	}

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("gagal melakukan ping database: %w", err)
	}

	return db, nil
}

// RunMigrationFile mengeksekusi file SQL migrasi terhadap database
func RunMigrationFile(db *sql.DB, filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("gagal membaca file migrasi %s: %w", filePath, err)
	}

	if _, err := db.Exec(string(content)); err != nil {
		return fmt.Errorf("gagal mengeksekusi migrasi %s: %w", filePath, err)
	}

	return nil
}

