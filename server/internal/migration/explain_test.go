package migration

import (
	"context"
	"testing"

	_ "github.com/lib/pq"

	"github.com/ItsMeD4N/IF3110-07/server/internal/config"
	"github.com/ItsMeD4N/IF3110-07/server/internal/database"
)

func TestSplitAndCleanStatements(t *testing.T) {
	input := `
		-- Ini komentar baris pertama
		SELECT * FROM users WHERE username = 'admin;test';
		/* Komentar multiline */
		INSERT INTO demo (val) VALUES ('Halo;');
	`
	stmts := splitStatements(input)
	if len(stmts) != 2 {
		t.Fatalf("Ekspektasi 2 statement, didapat %d", len(stmts))
	}

	clean0 := cleanSQL(stmts[0])
	if clean0 != "SELECT * FROM users WHERE username = 'admin;test'" {
		t.Errorf("Statement 0 salah di-parse: %q", clean0)
	}

	clean1 := cleanSQL(stmts[1])
	if clean1 != "/* Komentar multiline */\n\t\tINSERT INTO demo (val) VALUES ('Halo;')" {
		t.Errorf("Statement 1 salah di-parse: %q", clean1)
	}
}

func TestIsExplainable(t *testing.T) {
	explainable := []string{"SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "VALUES"}
	for _, word := range explainable {
		if !isExplainable(word) {
			t.Errorf("Harusnya %s dianggap explainable", word)
		}
	}

	notExplainable := []string{"CREATE", "ALTER", "DROP", "TRUNCATE", "GRANT"}
	for _, word := range notExplainable {
		if isExplainable(word) {
			t.Errorf("Harusnya %s tidak dianggap explainable oleh query optimizer", word)
		}
	}
}

func TestBuildSummaryFromJSON(t *testing.T) {
	execTime := 0.125
	planTime := 0.450
	actualRows := int64(1)

	raw := rawWrapper{
		PlanningTime:  &planTime,
		ExecutionTime: &execTime,
		Plan: rawNode{
			NodeType:    "Seq Scan",
			RelationName: "users",
			StartupCost: 0.00,
			TotalCost:   1.05,
			PlanRows:    1,
			ActualRows:  &actualRows,
		},
	}

	summary := buildSummary(raw)
	if !summary.HasSeqScan {
		t.Errorf("Ekspektasi HasSeqScan = true untuk Seq Scan pada users")
	}
	if len(summary.ScanTypes) != 1 || summary.ScanTypes[0] != "Seq Scan" {
		t.Errorf("ScanTypes salah: %v", summary.ScanTypes)
	}
	if summary.TotalCost != 1.05 {
		t.Errorf("TotalCost salah: %f", summary.TotalCost)
	}
	if summary.ActualTimeMS == nil || *summary.ActualTimeMS != 0.125 {
		t.Errorf("ActualTimeMS salah: %v", summary.ActualTimeMS)
	}
}

func TestRunExplainLiveRollback(t *testing.T) {
	cfg := config.LoadConfig()
	db, err := database.InitDB(cfg.DatabaseURL)
	if err != nil {
		t.Skipf("Database tidak tersedia untuk live test: %v", err)
	}
	defer db.Close()

	ctx := context.Background()

	// 1. Uji EXPLAIN ANALYZE pada SELECT
	querySelect := "SELECT id, username FROM users WHERE username = 'admin'"
	results, err := runExplain(ctx, db, querySelect, true)
	if err != nil {
		t.Fatalf("Gagal runExplain SELECT: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("Ekspektasi 1 hasil, didapat %d", len(results))
	}
	res := results[0]
	if !res.IsExplainable {
		t.Errorf("Ekspektasi SELECT dianggap explainable")
	}
	if len(res.Summary.ScanTypes) == 0 {
		t.Errorf("Ekspektasi terdeteksi jenis scan")
	}
	if res.Summary.ActualTimeMS == nil {
		t.Errorf("Ekspektasi ActualTimeMS terisi pada ANALYZE mode")
	}

	// 2. Uji SAFE ROLLBACK: Pastikan data INSERT via EXPLAIN ANALYZE tidak tersimpan di database
	testUniqueUser := "rollback_test_user_explain"
	insertQuery := "INSERT INTO users (username, password_hash) VALUES ('" + testUniqueUser + "', 'dummy_hash')"
	_, err = runExplain(ctx, db, insertQuery, true)
	if err != nil {
		t.Fatalf("Gagal runExplain INSERT: %v", err)
	}

	var exists bool
	err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM users WHERE username=$1)", testUniqueUser).Scan(&exists)
	if err != nil {
		t.Fatalf("Gagal query cek user: %v", err)
	}
	if exists {
		t.Errorf("BAHAYA: Data INSERT dari EXPLAIN ANALYZE tersimpan permanen di database, safe rollback gagal!")
	}
}
