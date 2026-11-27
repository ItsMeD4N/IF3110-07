package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	_ "github.com/lib/pq"
)

var identRe = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

func main() {
	dsn := flag.String("dsn", envOr("BENCH_TARGET_URL", "postgres://sample_app:sample_app@localhost:5433/sample_db?sslmode=disable"), "DSN database target")
	table := flag.String("table", "bench_events", "nama tabel dataset (schema public)")
	rows := flag.Int("rows", 1000000, "jumlah row dataset")
	runs := flag.Int("runs", 3, "jumlah pengukuran UPDATE tanpa batching")
	reset := flag.Bool("reset", false, "hapus dan buat ulang tabel dataset")
	out := flag.String("out", "", "tulis hasil (markdown) ke file ini")
	flag.Parse()

	if !identRe.MatchString(*table) {
		log.Fatalf("nama tabel %q tidak valid", *table)
	}
	if *rows <= 0 || *runs <= 0 {
		log.Fatal("rows dan runs harus > 0")
	}

	ctx := context.Background()
	db, err := sql.Open("postgres", *dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		log.Fatalf("koneksi target gagal: %v", err)
	}

	qt := `public.` + *table
	if *reset {
		if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS `+qt); err != nil {
			log.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+qt+` (
		id BIGSERIAL PRIMARY KEY,
		account_id INTEGER NOT NULL,
		tag TEXT NOT NULL,
		payload TEXT NOT NULL,
		processed BOOLEAN NOT NULL DEFAULT FALSE,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
	)`); err != nil {
		log.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS `+*table+`_processed_idx ON `+qt+` (processed)`); err != nil {
		log.Fatal(err)
	}

	var existing int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+qt).Scan(&existing); err != nil {
		log.Fatal(err)
	}
	if missing := int64(*rows) - existing; missing > 0 {
		log.Printf("seeding %d row ke %s ...", missing, qt)
		start := time.Now()
		const chunk = 250000
		for done := int64(0); done < missing; done += chunk {
			n := missing - done
			if n > chunk {
				n = chunk
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO `+qt+` (account_id, tag, payload)
				SELECT (g % 5000) + 1, 'tag_' || (g % 1000), md5(g::text) || repeat('x', 64)
				FROM generate_series(1, $1::bigint) g`, n); err != nil {
				log.Fatal(err)
			}
		}
		if _, err := db.ExecContext(ctx, `ANALYZE `+qt); err != nil {
			log.Fatal(err)
		}
		log.Printf("seeding selesai dalam %s", time.Since(start).Round(time.Millisecond))
	}

	var total int64
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+qt).Scan(&total); err != nil {
		log.Fatal(err)
	}

	updateSQL := `UPDATE ` + qt + ` SET processed = NOT processed, tag = 'bench_' || (id % 1000), updated_at = now()`
	durations := make([]time.Duration, 0, *runs)
	var affected int64
	for i := 0; i < *runs; i++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			log.Fatal(err)
		}
		start := time.Now()
		res, err := tx.ExecContext(ctx, updateSQL)
		d := time.Since(start)
		if err != nil {
			_ = tx.Rollback()
			log.Fatal(err)
		}
		affected, _ = res.RowsAffected()
		if err := tx.Rollback(); err != nil {
			log.Fatal(err)
		}
		durations = append(durations, d)
		log.Printf("run %d: UPDATE %d row tanpa batching = %s", i+1, affected, d.Round(time.Millisecond))
	}

	minD, maxD, sum := durations[0], durations[0], time.Duration(0)
	for _, d := range durations {
		if d < minD {
			minD = d
		}
		if d > maxD {
			maxD = d
		}
		sum += d
	}
	avg := sum / time.Duration(len(durations))

	var pgVersion string
	_ = db.QueryRowContext(ctx, `SHOW server_version`).Scan(&pgVersion)

	var b strings.Builder
	fmt.Fprintf(&b, "| Tanggal | PostgreSQL | Tabel | Jumlah row | Query | Run | Min | Rata-rata | Max |\n")
	fmt.Fprintf(&b, "| --- | --- | --- | --- | --- | --- | --- | --- | --- |\n")
	fmt.Fprintf(&b, "| %s | %s | `%s` | %d | `UPDATE` seluruh row tanpa batching (di-rollback) | %d | %s | %s | %s |\n",
		time.Now().Format("2006-01-02 15:04"), pgVersion, qt, total, len(durations),
		minD.Round(time.Millisecond), avg.Round(time.Millisecond), maxD.Round(time.Millisecond))
	fmt.Print(b.String())

	if minD < time.Second {
		log.Printf("PERINGATAN: UPDATE tercepat %s < 1 detik, jalankan ulang dengan -rows lebih besar", minD.Round(time.Millisecond))
	}

	if *out != "" {
		if err := os.WriteFile(*out, []byte(b.String()), 0o644); err != nil {
			log.Fatal(err)
		}
		log.Printf("hasil ditulis ke %s", *out)
	}
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
