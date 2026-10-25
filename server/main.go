package main

import (
	"context"
	"log"
	"net/http"

	"github.com/ItsMeD4N/IF3110-07/server/internal/app"
	"github.com/ItsMeD4N/IF3110-07/server/internal/config"
	"github.com/ItsMeD4N/IF3110-07/server/internal/database"
	"github.com/ItsMeD4N/IF3110-07/server/internal/web"
	"github.com/ItsMeD4N/IF3110-07/server/migrations"
)

func main() {
	cfg := config.LoadConfig()

	db, err := database.InitDB(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("koneksi ke database platform gagal: %v", err)
	}
	defer db.Close()

	if err := migrations.Apply(context.Background(), db); err != nil {
		log.Fatalf("migrasi database platform gagal: %v", err)
	}

	clientDir, err := web.FindClientDir()
	if err != nil {
		log.Fatal(err)
	}

	a, err := app.New(db, app.Options{AppEnv: cfg.AppEnv, ClientDir: clientDir})
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()

	log.Printf("server berjalan di :%s (env: %s)", cfg.Port, cfg.AppEnv)
	if err := http.ListenAndServe(":"+cfg.Port, a.Handler); err != nil {
		log.Fatal(err)
	}
}
