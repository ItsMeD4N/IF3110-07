package migration

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type ExecutionFilter struct {
	MigrationID int64
	Statuses    map[ExecutionStatus]bool
	Actions     map[Action]bool
	Actor       string
	Since       *time.Time
	Until       *time.Time
	Limit       int
	Offset      int
}

const maxHistoryLimit = 1000

func ParseExecutionFilter(q url.Values) (ExecutionFilter, error) {
	var f ExecutionFilter
	if v := strings.TrimSpace(q.Get("migration_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return f, &ValidationError{"migration_id tidak valid."}
		}
		f.MigrationID = id
	}
	if v := strings.TrimSpace(q.Get("status")); v != "" {
		f.Statuses = map[ExecutionStatus]bool{}
		for _, s := range strings.Split(v, ",") {
			st := ExecutionStatus(strings.TrimSpace(s))
			switch st {
			case ExecPending, ExecRunning, ExecSuccess, ExecFailed, ExecPartial, ExecPartiallyReverted, ExecReverted, ExecUncertain:
				f.Statuses[st] = true
			default:
				return f, &ValidationError{fmt.Sprintf("status %q tidak dikenal.", st)}
			}
		}
	}
	if v := strings.TrimSpace(q.Get("action")); v != "" {
		f.Actions = map[Action]bool{}
		for _, s := range strings.Split(v, ",") {
			a := Action(strings.TrimSpace(s))
			switch a {
			case ActionUp, ActionRollback, ActionForward, ActionBatchUp, ActionBatchResume, ActionBatchRevert:
				f.Actions[a] = true
			default:
				return f, &ValidationError{fmt.Sprintf("action %q tidak dikenal.", a)}
			}
		}
	}
	f.Actor = strings.TrimSpace(q.Get("actor"))
	for _, p := range []struct {
		key string
		dst **time.Time
	}{{"since", &f.Since}, {"until", &f.Until}} {
		if v := strings.TrimSpace(q.Get(p.key)); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return f, &ValidationError{p.key + " harus berformat RFC3339 (contoh 2026-11-26T05:00:00Z)."}
			}
			*p.dst = &t
		}
	}
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return f, &ValidationError{"limit tidak valid."}
		}
		f.Limit = n
	}
	if v := strings.TrimSpace(q.Get("offset")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return f, &ValidationError{"offset tidak valid."}
		}
		f.Offset = n
	}
	if f.Limit > maxHistoryLimit {
		f.Limit = maxHistoryLimit
	}
	return f, nil
}

func (f ExecutionFilter) match(ex Execution) bool {
	if f.MigrationID > 0 && ex.MigrationID != f.MigrationID {
		return false
	}
	if len(f.Statuses) > 0 && !f.Statuses[ex.Status] {
		return false
	}
	if len(f.Actions) > 0 && !f.Actions[ex.Action] {
		return false
	}
	if f.Actor != "" && !strings.EqualFold(ex.Actor, f.Actor) {
		return false
	}
	if f.Since != nil && ex.CreatedAt.Before(*f.Since) {
		return false
	}
	if f.Until != nil && ex.CreatedAt.After(*f.Until) {
		return false
	}
	return true
}

func FilterExecutions(execs []Execution, f ExecutionFilter) []Execution {
	out := make([]Execution, 0, len(execs))
	for _, ex := range execs {
		if f.match(ex) {
			out = append(out, ex)
		}
	}
	return out
}

func Paginate(execs []Execution, f ExecutionFilter) ([]Execution, int) {
	total := len(execs)
	if f.Offset >= total {
		return []Execution{}, total
	}
	execs = execs[f.Offset:]
	if f.Limit > 0 && f.Limit < len(execs) {
		execs = execs[:f.Limit]
	}
	return execs, total
}

type HistorySummary struct {
	TotalAttempts int                     `json:"total_attempts"`
	ByStatus      map[ExecutionStatus]int `json:"by_status"`
	ByAction      map[Action]int          `json:"by_action"`
	ActiveVersion int64                   `json:"active_version"`
}

func summarizeHistory(all []Execution, st migrationStateSummary) HistorySummary {
	s := HistorySummary{
		TotalAttempts: len(all),
		ByStatus:      map[ExecutionStatus]int{},
		ByAction:      map[Action]int{},
	}
	for _, ex := range all {
		s.ByStatus[ex.Status]++
		s.ByAction[ex.Action]++
	}
	if st.latestActiveExec != nil {
		s.ActiveVersion = st.latestActiveExec.MigrationVersion
	}
	return s
}

func (e *Engine) HistorySummary(ctx context.Context, databaseID int64) (HistorySummary, error) {
	execs, err := e.listExecutionsAsc(ctx, databaseID)
	if err != nil {
		return HistorySummary{}, err
	}
	defs, err := e.listDefinitionsAsc(ctx, databaseID)
	if err != nil {
		return HistorySummary{}, err
	}
	return summarizeHistory(execs, deriveMigrationState(defs, execs)), nil
}
