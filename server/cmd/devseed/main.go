package main

import (
	"context"
	"errors"
	"log"
	"os"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/config"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/database"
	"github.com/ItsMeD4N/IF3110-07/server/internal/secretbox"
	"github.com/ItsMeD4N/IF3110-07/server/internal/store"
	"github.com/ItsMeD4N/IF3110-07/server/migrations"
)

const devPassword = "Password#123"

func main() {
	ctx := context.Background()
	cfg := config.LoadConfig()
	if cfg.AppEnv == "production" {
		log.Fatal("devseed tidak boleh dijalankan di production")
	}
	db, err := database.InitDB(cfg.DatabaseURL)
	must(err)
	defer db.Close()
	must(migrations.Apply(ctx, db))

	key := secretbox.DevKey()
	if raw := os.Getenv("APP_ENCRYPTION_KEY"); raw != "" {
		key, err = secretbox.ParseKey(raw)
		must(err)
	}
	box, err := secretbox.New(key)
	must(err)
	st := store.New(db, box)
	users := auth.NewUserRepository(db)

	hash, err := auth.HashPassword(devPassword)
	must(err)
	ids := map[string]string{}
	for _, name := range []string{"alice", "bob", "carol"} {
		u, err := users.Create(ctx, name, hash, false)
		if errors.Is(err, auth.ErrUsernameTaken) {
			u, err = users.GetByUsername(ctx, name)
		}
		must(err)
		ids[name] = u.ID
	}

	groups := map[string]int64{}
	existing, err := st.ListGroups(ctx)
	must(err)
	for _, g := range existing {
		groups[g.Name] = g.ID
	}
	for _, g := range []struct{ name, desc string }{
		{"Analyst", "Hanya membaca schema & data (View Only)"},
		{"Backend Dev", "Boleh membuat & menjalankan migration (View + Edit)"},
	} {
		if _, ok := groups[g.name]; !ok {
			created, err := st.CreateGroup(ctx, g.name, g.desc)
			must(err)
			groups[g.name] = created.ID
		}
	}
	for _, m := range [][2]string{{"Analyst", "alice"}, {"Backend Dev", "bob"}, {"Analyst", "carol"}, {"Backend Dev", "carol"}} {
		_, err := st.AddMember(ctx, groups[m[0]], ids[m[1]])
		must(err)
	}

	if target := os.Getenv("SEED_TARGET_URL"); target != "" {
		c, err := connmgr.ConfigFromURL(target)
		must(err)
		if c.SSLMode == "" {
			c.SSLMode = "disable"
		}
		var connID int64
		conns, err := st.ListConnections(ctx)
		must(err)
		for _, x := range conns {
			if x.Name == "Sample DB" {
				connID = x.ID
			}
		}
		if connID == 0 {
			created, err := st.CreateConnection(ctx, store.ConnectionInput{
				Name: "Sample DB", Description: "Database contoh dari SEED_TARGET_URL",
				Host: c.Host, Port: c.Port, DatabaseName: c.Database, Username: c.Username, SSLMode: c.SSLMode,
			}, c.Password)
			must(err)
			connID = created.ID
		}
		must(st.SetGrant(ctx, groups["Analyst"], connID, authz.LevelViewOnly))
		must(st.SetGrant(ctx, groups["Backend Dev"], connID, authz.LevelViewEdit))
	}
	log.Printf("seed selesai: user alice/bob/carol (password %s), group %v", devPassword, groups)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
