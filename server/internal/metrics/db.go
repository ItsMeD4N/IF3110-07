package metrics

import (
	"context"
	"database/sql"
	"sort"
)

type StatsFunc func() sql.DBStats

func RegisterDBPool(pool string, stats StatsFunc) {
	if stats == nil {
		Default.RegisterCollector("dbpool:"+pool, nil)
		return
	}
	Default.RegisterCollector("dbpool:"+pool, func(ctx context.Context) []Snapshot {
		s := stats()
		l := []Label{{Name: "pool", Value: pool}}
		return []Snapshot{
			{Name: "if3110_db_pool_open_connections", Help: "Jumlah koneksi terbuka pada pool database.", Kind: KindGauge, Samples: []Sample{{Labels: l, Value: float64(s.OpenConnections)}}},
			{Name: "if3110_db_pool_active_connections", Help: "Jumlah koneksi yang sedang dipakai (aktif).", Kind: KindGauge, Samples: []Sample{{Labels: l, Value: float64(s.InUse)}}},
			{Name: "if3110_db_pool_idle_connections", Help: "Jumlah koneksi idle pada pool database.", Kind: KindGauge, Samples: []Sample{{Labels: l, Value: float64(s.Idle)}}},
			{Name: "if3110_db_pool_max_open_connections", Help: "Batas maksimum koneksi terbuka pada pool database.", Kind: KindGauge, Samples: []Sample{{Labels: l, Value: float64(s.MaxOpenConnections)}}},
			{Name: "if3110_db_pool_wait_count_total", Help: "Jumlah total request koneksi yang harus menunggu koneksi kosong.", Kind: KindCounter, Samples: []Sample{{Labels: l, Value: float64(s.WaitCount)}}},
			{Name: "if3110_db_pool_wait_duration_seconds_total", Help: "Total waktu menunggu koneksi kosong dari pool database.", Kind: KindCounter, Samples: []Sample{{Labels: l, Value: s.WaitDuration.Seconds()}}},
		}
	})
}

func RegisterJobStatusGauge(db *sql.DB) {
	if db == nil {
		Default.RegisterCollector("jobs:status", nil)
		return
	}
	Default.RegisterCollector("jobs:status", func(ctx context.Context) []Snapshot {
		counts := map[string]float64{}
		for st := range knownJobStatuses {
			counts[st] = 0
		}
		up := 1.0
		rows, err := db.QueryContext(ctx, `SELECT status, count(*) FROM platform_migration_jobs GROUP BY status`)
		if err != nil {
			up = 0
		} else {
			for rows.Next() {
				var st string
				var n int64
				if rows.Scan(&st, &n) == nil {
					counts[statusLabel(st)] += float64(n)
				}
			}
			if rows.Err() != nil {
				up = 0
			}
			rows.Close()
		}
		samples := make([]Sample, 0, len(counts))
		for _, st := range sortedKeys(counts) {
			samples = append(samples, Sample{Labels: []Label{{Name: "status", Value: st}}, Value: counts[st]})
		}
		return []Snapshot{
			{Name: "if3110_migration_jobs", Help: "Jumlah job migration di queue per status (dibaca dari database platform).", Kind: KindGauge, Samples: samples},
			{Name: "if3110_migration_jobs_scrape_ok", Help: "1 jika pembacaan status job dari database platform berhasil.", Kind: KindGauge, Samples: []Sample{{Value: up}}},
		}
	})
}

func sortedKeys(m map[string]float64) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
