package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
)

// ExplainRequest mendefinisikan payload untuk analisis query execution plan
type ExplainRequest struct {
	SQL     string `json:"sql"`
	Analyze bool   `json:"analyze"` // Jika true, jalankan EXPLAIN ANALYZE dalam safe transaction rollback
}

// PlanNode merepresentasikan node dalam pohon execution plan PostgreSQL
type PlanNode struct {
	NodeType          string     `json:"node_type"`
	RelationName      string     `json:"relation_name,omitempty"`
	Schema            string     `json:"schema,omitempty"`
	Alias             string     `json:"alias,omitempty"`
	IndexName         string     `json:"index_name,omitempty"`
	IndexCond         string     `json:"index_cond,omitempty"`
	Filter            string     `json:"filter,omitempty"`
	JoinType          string     `json:"join_type,omitempty"`
	HashCond          string     `json:"hash_cond,omitempty"`
	StartupCost       float64    `json:"startup_cost"`
	TotalCost         float64    `json:"total_cost"`
	PlanRows          int64      `json:"plan_rows"`
	ActualStartupTime *float64   `json:"actual_startup_time,omitempty"`
	ActualTotalTime   *float64   `json:"actual_total_time,omitempty"`
	ActualRows        *int64     `json:"actual_rows,omitempty"`
	ActualLoops       *int64     `json:"actual_loops,omitempty"`
	Plans             []PlanNode `json:"plans,omitempty"`
}

// PlanSummary merangkum poin-poin penting analisis sesuai spesifikasi tugas besar
type PlanSummary struct {
	ScanTypes      []string `json:"scan_types"`       // Jenis scan: Seq Scan, Index Scan, Bitmap Heap Scan, dll
	IndexesUsed    []string `json:"indexes_used"`     // Index yang digunakan
	JoinStrategies []string `json:"join_strategies"`  // Join strategy: Hash Join, Nested Loop, Merge Join, dll
	StartupCost    float64  `json:"startup_cost"`     // Estimated cost awal
	TotalCost      float64  `json:"total_cost"`       // Estimated cost total
	ActualTimeMS   *float64 `json:"actual_time_ms,omitempty"`   // Actual execution time (ms) jika ANALYZE aktif
	PlanningTimeMS *float64 `json:"planning_time_ms,omitempty"` // Planning time (ms)
	RowsPlanned    int64    `json:"rows_planned"`               // Estimasi jumlah row
	RowsProcessed  *int64   `json:"rows_processed,omitempty"`   // Jumlah row aktual yang diproses
	HasSeqScan     bool     `json:"has_seq_scan"`               // Indikasi ada full table scan
	Warnings       []string `json:"warnings"`                   // Saran optimasi / catatan performa
}

// SingleExplainResult menyimpan hasil analisis untuk satu statement SQL
type SingleExplainResult struct {
	Statement    string      `json:"statement"`
	IsExplainable bool       `json:"is_explainable"`
	Note         string      `json:"note,omitempty"`
	AnalyzeMode  bool        `json:"analyze_mode"`
	Summary      PlanSummary `json:"summary"`
	RootNode     *PlanNode   `json:"root_node,omitempty"`
	RawText      string      `json:"raw_text,omitempty"`
	SafeRollback bool        `json:"safe_rollback"`
}

// ExplainResponse adalah respons menyeluruh dari endpoint EXPLAIN
type ExplainResponse struct {
	DatabaseID   int64                 `json:"database_id"`
	TotalQueries int                   `json:"total_queries"`
	Results      []SingleExplainResult `json:"results"`
}

// Struktur internal untuk unmarshaling output JSON dari PostgreSQL
type rawWrapper struct {
	Plan          rawNode  `json:"Plan"`
	PlanningTime  *float64 `json:"Planning Time"`
	ExecutionTime *float64 `json:"Execution Time"`
}

type rawNode struct {
	NodeType          string    `json:"Node Type"`
	RelationName      string    `json:"Relation Name"`
	Schema            string    `json:"Schema"`
	Alias             string    `json:"Alias"`
	IndexName         string    `json:"Index Name"`
	IndexCond         string    `json:"Index Cond"`
	Filter            string    `json:"Filter"`
	JoinType          string    `json:"Join Type"`
	HashCond          string    `json:"Hash Cond"`
	StartupCost       float64   `json:"Startup Cost"`
	TotalCost         float64   `json:"Total Cost"`
	PlanRows          int64     `json:"Plan Rows"`
	ActualStartupTime *float64  `json:"Actual Startup Time"`
	ActualTotalTime   *float64  `json:"Actual Total Time"`
	ActualRows        *int64    `json:"Actual Rows"`
	ActualLoops       *int64    `json:"Actual Loops"`
	Plans             []rawNode `json:"Plans"`
}

// apiExplain menangani request POST /api/databases/{dbID}/migrations/explain
func (m *Module) apiExplain(w http.ResponseWriter, r *http.Request) {
	id, ok := databaseID(r)
	if !ok {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "ID database tidak valid")
		return
	}

	sqlText := ""
	analyze := false

	// Cek apakah request berupa form-urlencoded atau JSON
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "application/json") {
		var req ExplainRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "Body JSON tidak valid")
			return
		}
		sqlText = req.SQL
		analyze = req.Analyze
	} else {
		if err := r.ParseForm(); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "Body request tidak valid")
			return
		}
		sqlText = r.FormValue("sql")
		analyze = r.FormValue("analyze") == "true" || r.FormValue("analyze") == "1"
	}

	sqlText = strings.TrimSpace(sqlText)
	if sqlText == "" {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.CodeBadRequest, "Query SQL tidak boleh kosong")
		return
	}
	if len(sqlText) > maxSQLBytes {
		httpx.WriteError(w, http.StatusRequestEntityTooLarge, httpx.CodeBadRequest, "SQL maksimal 1 MiB")
		return
	}

	// Eksekusi analisis pada database target
	targetDB, err := m.Conns.Pool(r.Context(), id)
	if err != nil {
		writeDBError(w, r, err)
		return
	}

	results, err := runExplain(r.Context(), targetDB, sqlText, analyze)
	if err != nil {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":  "Gagal menganalisis query execution plan",
			"detail": err.Error(),
		})
		return
	}

	httpx.WriteJSON(w, http.StatusOK, ExplainResponse{
		DatabaseID:   id,
		TotalQueries: len(results),
		Results:      results,
	})
}

// runExplain mengeksekusi EXPLAIN terhadap setiap statement SQL dalam transaksi aman dengan rollback
func runExplain(ctx context.Context, db *sql.DB, sqlText string, analyze bool) ([]SingleExplainResult, error) {
	stmts := splitStatements(sqlText)
	if len(stmts) == 0 {
		return nil, fmt.Errorf("tidak ada statement SQL yang ditemukan")
	}

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka koneksi ke database target: %w", err)
	}
	defer conn.Close()

	var results []SingleExplainResult

	for _, stmt := range stmts {
		clean := cleanSQL(stmt)
		if clean == "" {
			continue
		}

		firstWord := strings.ToUpper(strings.Fields(clean)[0])
		if !isExplainable(firstWord) {
			results = append(results, SingleExplainResult{
				Statement:     clean,
				IsExplainable: false,
				Note:          fmt.Sprintf("Pernyataan '%s' merupakan DDL/Utility statement. PostgreSQL tidak menggunakan cost-based query optimizer untuk operasi DDL/Utility ini.", firstWord),
			})
			continue
		}

		// Jalankan dalam transaksi dengan ROLLBACK agar EXPLAIN ANALYZE tidak mengubah state target!
		res, err := explainSingleStatement(ctx, conn, clean, analyze)
		if err != nil {
			return nil, fmt.Errorf("error pada statement '%s...': %w", truncate(clean, 40), err)
		}
		results = append(results, res)
	}

	return results, nil
}

func explainSingleStatement(ctx context.Context, conn *sql.Conn, query string, analyze bool) (SingleExplainResult, error) {
	// Buka transaksi eksplisit
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return SingleExplainResult{}, err
	}
	// Pastikan SELALU di-rollback!
	defer func() {
		_ = tx.Rollback()
	}()

	explainCmd := "EXPLAIN (VERBOSE, BUFFERS, COSTS, FORMAT JSON"
	if analyze {
		explainCmd = "EXPLAIN (ANALYZE, VERBOSE, BUFFERS, COSTS, FORMAT JSON"
	}
	explainCmd += ") " + query

	var jsonRaw string
	if err := tx.QueryRowContext(ctx, explainCmd).Scan(&jsonRaw); err != nil {
		return SingleExplainResult{}, err
	}

	// Ambil juga versi TEXT untuk DBA view
	textCmd := "EXPLAIN (VERBOSE, BUFFERS, COSTS, FORMAT TEXT"
	if analyze {
		textCmd = "EXPLAIN (ANALYZE, VERBOSE, BUFFERS, COSTS, FORMAT TEXT"
	}
	textCmd += ") " + query

	rows, err := tx.QueryContext(ctx, textCmd)
	var textLines []string
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err == nil {
				textLines = append(textLines, line)
			}
		}
	}

	var wrappers []rawWrapper
	if err := json.Unmarshal([]byte(jsonRaw), &wrappers); err != nil {
		return SingleExplainResult{}, fmt.Errorf("gagal mem-parse JSON hasil EXPLAIN: %w", err)
	}
	if len(wrappers) == 0 {
		return SingleExplainResult{}, fmt.Errorf("hasil EXPLAIN kosong")
	}

	w := wrappers[0]
	rootNode := convertNode(w.Plan)
	summary := buildSummary(w)

	return SingleExplainResult{
		Statement:     query,
		IsExplainable: true,
		AnalyzeMode:   analyze,
		Summary:       summary,
		RootNode:      &rootNode,
		RawText:       strings.Join(textLines, "\n"),
		SafeRollback:  true, // Transaksi selalu di-rollback
	}, nil
}

func convertNode(r rawNode) PlanNode {
	n := PlanNode{
		NodeType:          r.NodeType,
		RelationName:      r.RelationName,
		Schema:            r.Schema,
		Alias:             r.Alias,
		IndexName:         r.IndexName,
		IndexCond:         r.IndexCond,
		Filter:            r.Filter,
		JoinType:          r.JoinType,
		HashCond:          r.HashCond,
		StartupCost:       r.StartupCost,
		TotalCost:         r.TotalCost,
		PlanRows:          r.PlanRows,
		ActualStartupTime: r.ActualStartupTime,
		ActualTotalTime:   r.ActualTotalTime,
		ActualRows:        r.ActualRows,
		ActualLoops:       r.ActualLoops,
	}
	for _, child := range r.Plans {
		n.Plans = append(n.Plans, convertNode(child))
	}
	return n
}

func buildSummary(w rawWrapper) PlanSummary {
	var scanTypes []string
	var indexesUsed []string
	var joinStrategies []string
	var warnings []string
	hasSeqScan := false

	var walk func(n rawNode)
	walk = func(n rawNode) {
		// 1. Jenis scan
		if strings.Contains(n.NodeType, "Scan") {
			if !containsStr(scanTypes, n.NodeType) {
				scanTypes = append(scanTypes, n.NodeType)
			}
			if n.NodeType == "Seq Scan" && n.RelationName != "" {
				hasSeqScan = true
				warnings = append(warnings, fmt.Sprintf("Sequential Scan (full-table scan) terdeteksi pada tabel '%s'.", n.RelationName))
			}
		}

		// 2. Penggunaan index
		if n.IndexName != "" {
			if !containsStr(indexesUsed, n.IndexName) {
				indexesUsed = append(indexesUsed, n.IndexName)
			}
		}

		// 3. Join strategy
		if strings.Contains(n.NodeType, "Join") || strings.Contains(n.NodeType, "Nested Loop") {
			if !containsStr(joinStrategies, n.NodeType) {
				joinStrategies = append(joinStrategies, n.NodeType)
			}
		}

		for _, ch := range n.Plans {
			walk(ch)
		}
	}

	walk(w.Plan)

	if w.Plan.TotalCost > 1000.0 {
		warnings = append(warnings, fmt.Sprintf("Estimated Total Cost cukup tinggi (%.2f). Pertimbangkan optimasi query atau penambahan index.", w.Plan.TotalCost))
	}

	s := PlanSummary{
		ScanTypes:      scanTypes,
		IndexesUsed:    indexesUsed,
		JoinStrategies: joinStrategies,
		StartupCost:    w.Plan.StartupCost,
		TotalCost:      w.Plan.TotalCost,
		PlanningTimeMS: w.PlanningTime,
		ActualTimeMS:   w.ExecutionTime,
		RowsPlanned:    w.Plan.PlanRows,
		RowsProcessed:  w.Plan.ActualRows,
		HasSeqScan:     hasSeqScan,
		Warnings:       warnings,
	}

	return s
}

func writeDBError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, connmgr.ErrNotFound) {
		httpx.NotFound(w)
		return
	}
	var ce *connmgr.ConnError
	if errors.As(err, &ce) {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeConnectionFailed, "Database target tidak dapat dijangkau")
		return
	}
	httpx.Internal(w, r, fmt.Errorf("migration: %w", err))
}

func isExplainable(word string) bool {
	switch word {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "VALUES", "EXECUTE", "DECLARE":
		return true
	default:
		return false
	}
}

func splitStatements(sql string) []string {
	var stmts []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false

	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		if ch == '\'' && !inDoubleQuote {
			inSingleQuote = !inSingleQuote
		} else if ch == '"' && !inSingleQuote {
			inDoubleQuote = !inDoubleQuote
		}

		if ch == ';' && !inSingleQuote && !inDoubleQuote {
			if s := strings.TrimSpace(current.String()); s != "" {
				stmts = append(stmts, s)
			}
			current.Reset()
			continue
		}
		current.WriteByte(ch)
	}
	if s := strings.TrimSpace(current.String()); s != "" {
		stmts = append(stmts, s)
	}
	return stmts
}

func cleanSQL(s string) string {
	lines := strings.Split(s, "\n")
	var valid []string
	for _, l := range lines {
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		valid = append(valid, l)
	}
	return strings.TrimSpace(strings.Join(valid, "\n"))
}

func containsStr(arr []string, s string) bool {
	for _, v := range arr {
		if v == s {
			return true
		}
	}
	return false
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
