package migration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/httpx"
)

const explainStatementTimeout = 10 * time.Second

type ExplainRequest struct {
	SQL     string `json:"sql"`
	Analyze bool   `json:"analyze"`
}

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

type PlanSummary struct {
	ScanTypes      []string `json:"scan_types"`
	IndexesUsed    []string `json:"indexes_used"`
	JoinStrategies []string `json:"join_strategies"`
	StartupCost    float64  `json:"startup_cost"`
	TotalCost      float64  `json:"total_cost"`
	ActualTimeMS   *float64 `json:"actual_time_ms,omitempty"`
	PlanningTimeMS *float64 `json:"planning_time_ms,omitempty"`
	RowsPlanned    int64    `json:"rows_planned"`
	RowsProcessed  *int64   `json:"rows_processed,omitempty"`
	HasSeqScan     bool     `json:"has_seq_scan"`
	Warnings       []string `json:"warnings"`
}

type SingleExplainResult struct {
	Statement     string      `json:"statement"`
	IsExplainable bool        `json:"is_explainable"`
	Note          string      `json:"note,omitempty"`
	AnalyzeMode   bool        `json:"analyze_mode"`
	Summary       PlanSummary `json:"summary"`
	RootNode      *PlanNode   `json:"root_node,omitempty"`
	RawText       string      `json:"raw_text,omitempty"`
	SafeRollback  bool        `json:"safe_rollback"`
}

type ExplainResponse struct {
	DatabaseID   int64                 `json:"database_id"`
	TotalQueries int                   `json:"total_queries"`
	Results      []SingleExplainResult `json:"results"`
}

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
	PlanRows          float64   `json:"Plan Rows"`
	ActualStartupTime *float64  `json:"Actual Startup Time"`
	ActualTotalTime   *float64  `json:"Actual Total Time"`
	ActualRows        *float64  `json:"Actual Rows"`
	ActualLoops       *float64  `json:"Actual Loops"`
	Plans             []rawNode `json:"Plans"`
}

func (m *Module) apiExplain(w http.ResponseWriter, r *http.Request) {
	id := dbID(r)
	if id <= 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "ID database tidak valid")
		return
	}

	sqlText := ""
	analyze := false

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

	access, _ := authz.DBAccessFrom(r.Context())
	canExecute := access.Can(authz.PermDBExecuteMigration)
	if analyze && !canExecute {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "EXPLAIN ANALYZE benar-benar menjalankan query, jadi cuma bisa dipakai dengan akses View + Edit. Pakai EXPLAIN biasa saja.")
		return
	}

	targetDB, err := m.Conns.Pool(r.Context(), id)
	if err != nil {
		writeDBError(w, r, err)
		return
	}

	results, err := runExplain(r.Context(), targetDB, sqlText, analyze, !canExecute)
	if err != nil {
		httpx.WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error":  "EXPLAIN gagal dijalankan",
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

func runExplain(ctx context.Context, db *sql.DB, sqlText string, analyze, readOnly bool) ([]SingleExplainResult, error) {
	if analyze && readOnly {
		return nil, fmt.Errorf("EXPLAIN ANALYZE tidak dapat dijalankan dalam mode read-only")
	}
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
				Note:          fmt.Sprintf("%s tidak punya execution plan, jadi dilewati.", firstWord),
			})
			continue
		}

		res, err := explainSingleStatement(ctx, conn, clean, analyze, readOnly)
		if err != nil {
			return nil, fmt.Errorf("error pada statement '%s...': %w", truncate(clean, 40), err)
		}
		results = append(results, res)
	}

	return results, nil
}

func explainSingleStatement(ctx context.Context, conn *sql.Conn, query string, analyze, readOnly bool) (SingleExplainResult, error) {
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
	if err != nil {
		return SingleExplainResult{}, err
	}

	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", explainStatementTimeout.Milliseconds())); err != nil {
		return SingleExplainResult{}, err
	}

	explainCmd := "EXPLAIN (VERBOSE, BUFFERS, COSTS, FORMAT JSON"
	if analyze {
		explainCmd = "EXPLAIN (ANALYZE, VERBOSE, BUFFERS, COSTS, FORMAT JSON"
	}
	explainCmd += ") " + query

	var jsonRaw string
	if err := tx.QueryRowContext(ctx, explainCmd).Scan(&jsonRaw); err != nil {
		return SingleExplainResult{}, err
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
		RawText:       formatPlanText(w),
		SafeRollback:  true,
	}, nil
}

func formatPlanText(w rawWrapper) string {
	var lines []string
	var walk func(n rawNode, depth int)
	walk = func(n rawNode, depth int) {
		indent := ""
		detailIndent := "  "
		if depth > 0 {
			indent = strings.Repeat("      ", depth-1) + "  ->  "
			detailIndent = strings.Repeat("      ", depth) + "  "
		}

		label := n.NodeType
		if n.IndexName != "" {
			label += " using " + n.IndexName
		}
		if n.RelationName != "" {
			rel := n.RelationName
			if n.Schema != "" {
				rel = n.Schema + "." + rel
			}
			label += " on " + rel
			if n.Alias != "" && n.Alias != n.RelationName {
				label += " " + n.Alias
			}
		}

		line := fmt.Sprintf("%s%s  (cost=%.2f..%.2f rows=%d)", indent, label, n.StartupCost, n.TotalCost, int64(n.PlanRows))
		if n.ActualTotalTime != nil && n.ActualRows != nil {
			startup := 0.0
			if n.ActualStartupTime != nil {
				startup = *n.ActualStartupTime
			}
			loops := int64(1)
			if n.ActualLoops != nil {
				loops = int64(*n.ActualLoops)
			}
			line += fmt.Sprintf(" (actual time=%.3f..%.3f rows=%d loops=%d)", startup, *n.ActualTotalTime, int64(*n.ActualRows), loops)
		}
		lines = append(lines, line)

		if n.HashCond != "" {
			lines = append(lines, detailIndent+"Hash Cond: "+n.HashCond)
		}
		if n.IndexCond != "" {
			lines = append(lines, detailIndent+"Index Cond: "+n.IndexCond)
		}
		if n.Filter != "" {
			lines = append(lines, detailIndent+"Filter: "+n.Filter)
		}

		for _, child := range n.Plans {
			walk(child, depth+1)
		}
	}
	walk(w.Plan, 0)

	if w.PlanningTime != nil {
		lines = append(lines, fmt.Sprintf("Planning Time: %.3f ms", *w.PlanningTime))
	}
	if w.ExecutionTime != nil {
		lines = append(lines, fmt.Sprintf("Execution Time: %.3f ms", *w.ExecutionTime))
	}
	return strings.Join(lines, "\n")
}

func convertNode(r rawNode) PlanNode {
	var actualRows *int64
	if r.ActualRows != nil {
		v := int64(*r.ActualRows)
		actualRows = &v
	}
	var actualLoops *int64
	if r.ActualLoops != nil {
		v := int64(*r.ActualLoops)
		actualLoops = &v
	}

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
		PlanRows:          int64(r.PlanRows),
		ActualStartupTime: r.ActualStartupTime,
		ActualTotalTime:   r.ActualTotalTime,
		ActualRows:        actualRows,
		ActualLoops:       actualLoops,
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

		if strings.Contains(n.NodeType, "Scan") {
			if !containsStr(scanTypes, n.NodeType) {
				scanTypes = append(scanTypes, n.NodeType)
			}
			if n.NodeType == "Seq Scan" && n.RelationName != "" {
				hasSeqScan = true
				warnings = append(warnings, fmt.Sprintf("Ada Seq Scan di tabel %s.", n.RelationName))
			}
		}

		if n.IndexName != "" {
			if !containsStr(indexesUsed, n.IndexName) {
				indexesUsed = append(indexesUsed, n.IndexName)
			}
		}

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
		warnings = append(warnings, fmt.Sprintf("Total cost-nya cukup tinggi (%.2f), mungkin perlu index.", w.Plan.TotalCost))
	}

	var rowsProcessed *int64
	if w.Plan.ActualRows != nil {
		v := int64(*w.Plan.ActualRows)
		rowsProcessed = &v
	}

	s := PlanSummary{
		ScanTypes:      scanTypes,
		IndexesUsed:    indexesUsed,
		JoinStrategies: joinStrategies,
		StartupCost:    w.Plan.StartupCost,
		TotalCost:      w.Plan.TotalCost,
		PlanningTimeMS: w.PlanningTime,
		ActualTimeMS:   w.ExecutionTime,
		RowsPlanned:    int64(w.Plan.PlanRows),
		RowsProcessed:  rowsProcessed,
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
