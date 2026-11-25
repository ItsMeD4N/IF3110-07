package migration

import (
	"strings"
	"testing"
)

func int64Ptr(v int64) *int64 { return &v }

func TestDeriveMigrationState_LinearRollbackForwardAndFailedAttempts(t *testing.T) {
	defs := []Definition{
		{ID: 1, Version: 1, Name: "A", SQLUp: "SELECT 1", SQLDown: "SELECT 1"},
		{ID: 2, Version: 2, Name: "B", SQLUp: "SELECT 1", SQLDown: "SELECT 1"},
		{ID: 3, Version: 3, Name: "C", SQLUp: "SELECT 1", SQLDown: "SELECT 1"},
	}

	execs := []Execution{
		{ID: 1, MigrationID: 1, MigrationVersion: 1, Action: ActionUp, Status: ExecSuccess},
		{ID: 2, MigrationID: 2, MigrationVersion: 2, Action: ActionUp, Status: ExecSuccess},
		{ID: 3, MigrationID: 3, MigrationVersion: 3, Action: ActionUp, Status: ExecSuccess},
	}
	st := deriveMigrationState(defs, execs)
	if st.latestActiveExec == nil || st.latestActiveExec.ID != 3 {
		t.Fatalf("latestActive = %+v, want E3", st.latestActiveExec)
	}
	if !enrichDefinition(defs[2], st).CanRollback {
		t.Error("C (latest active) harus bisa di-rollback")
	}
	if enrichDefinition(defs[1], st).CanRollback || enrichDefinition(defs[0], st).CanRollback {
		t.Error("A dan B tidak boleh bisa di-rollback sebelum C di-rollback")
	}

	execs = append(execs, Execution{
		ID: 4, MigrationID: 3, MigrationVersion: 3, Action: ActionRollback, Status: ExecFailed, RevertedExecutionID: int64Ptr(3),
	})
	st = deriveMigrationState(defs, execs)
	if st.latestActiveExec == nil || st.latestActiveExec.ID != 3 {
		t.Fatalf("rollback gagal tidak boleh mengubah posisi aktif, got %+v", st.latestActiveExec)
	}

	execs = append(execs,
		Execution{ID: 5, MigrationID: 3, MigrationVersion: 3, Action: ActionRollback, Status: ExecSuccess, RevertedExecutionID: int64Ptr(3)},
		Execution{ID: 6, MigrationID: 2, MigrationVersion: 2, Action: ActionRollback, Status: ExecSuccess, RevertedExecutionID: int64Ptr(2)},
	)
	st = deriveMigrationState(defs, execs)
	if st.latestActiveExec == nil || st.latestActiveExec.ID != 1 {
		t.Fatalf("setelah rollback C dan B, active terakhir harus A[E1], got %+v", st.latestActiveExec)
	}
	if !enrichDefinition(defs[1], st).CanForward {
		t.Error("B (v2) harus bisa di-forward")
	}
	if enrichDefinition(defs[2], st).CanForward {
		t.Error("C (v3) tidak boleh melompati B (v2) saat forward")
	}

	execs = append(execs, Execution{ID: 7, MigrationID: 2, MigrationVersion: 2, Action: ActionForward, Status: ExecFailed})
	st = deriveMigrationState(defs, execs)
	if !enrichDefinition(defs[1], st).CanForward {
		t.Error("B harus tetap boleh di-forward lagi setelah attempt forward gagal")
	}

	execs = append(execs, Execution{ID: 8, MigrationID: 2, MigrationVersion: 2, Action: ActionForward, Status: ExecSuccess})
	st = deriveMigrationState(defs, execs)
	if st.latestActiveExec.ID != 8 || !enrichDefinition(defs[2], st).CanForward {
		t.Fatalf("setelah forward B[E8], C harus bisa di-forward; st=%+v", st)
	}
}

func TestFilterHideRollback_MatchesSpecExampleByExecutionID(t *testing.T) {
	execs := []Execution{
		{ID: 1, MigrationID: 1, Action: ActionUp, Status: ExecSuccess},
		{ID: 2, MigrationID: 2, Action: ActionUp, Status: ExecSuccess},
		{ID: 3, MigrationID: 2, Action: ActionRollback, Status: ExecSuccess, RevertedExecutionID: int64Ptr(2)},
		{ID: 4, MigrationID: 2, Action: ActionForward, Status: ExecSuccess},
		{ID: 5, MigrationID: 3, Action: ActionUp, Status: ExecSuccess},
		{ID: 6, MigrationID: 3, Action: ActionRollback, Status: ExecFailed, RevertedExecutionID: int64Ptr(5)},
	}

	filtered := FilterHideRollback(execs)
	gotIDs := make([]int64, len(filtered))
	for i, ex := range filtered {
		gotIDs[i] = ex.ID
	}
	wantIDs := []int64{1, 4, 5, 6}
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("filtered IDs = %v, want %v", gotIDs, wantIDs)
	}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("filtered IDs = %v, want %v", gotIDs, wantIDs)
		}
	}
}

func TestValidateDefinitionInput(t *testing.T) {
	if err := validateDefinitionInput("ok", "CREATE TABLE x (id int)", "DROP TABLE x", false, "", ModeNormal, nil); err != nil {
		t.Errorf("definisi valid ditolak: %v", err)
	}
	if err := validateDefinitionInput("irrev", "DELETE FROM x", "", true, "data terhapus permanen", ModeNormal, nil); err != nil {
		t.Errorf("definisi irreversible valid ditolak: %v", err)
	}
	if err := validateDefinitionInput("no-down", "CREATE TABLE x (id int)", "", false, "", ModeNormal, nil); err == nil {
		t.Error("reversible tanpa sql_down harus ditolak")
	}
	if err := validateDefinitionInput("no-reason", "DELETE FROM x", "", true, "", ModeNormal, nil); err == nil {
		t.Error("irreversible tanpa alasan harus ditolak")
	}
	if err := validateDefinitionInput("tx-ctrl", "BEGIN; SELECT 1", "SELECT 1", false, "", ModeNormal, nil); err == nil {
		t.Error("SQL up dengan BEGIN harus ditolak")
	}
	if err := validateDefinitionInput("tx-down", "SELECT 1", "COMMIT;", false, "", ModeNormal, nil); err == nil {
		t.Error("SQL down dengan COMMIT harus ditolak")
	}
	if err := validateDefinitionInput("batch-no-ph", "UPDATE t SET a = 1", "UPDATE t SET a = 0", false, "", ModeBatch, &BatchConfig{
		Table: "t", CursorColumn: "id", BatchSize: 100,
	}); err == nil {
		t.Error("batch SQL up tanpa placeholder cakupan batch harus ditolak")
	}
	if err := validateDefinitionInput("batch-ok",
		"UPDATE t SET a = 1 WHERE id = ANY({{batch_ids}})",
		"UPDATE t SET a = 0 WHERE id = ANY({{batch_ids}})",
		false, "", ModeBatch, &BatchConfig{Table: "t", CursorColumn: "id", BatchSize: 100},
	); err != nil {
		t.Errorf("definisi batch valid ditolak: %v", err)
	}
}

func TestCompileBatchSQL(t *testing.T) {
	q, args, err := CompileBatchSQL(
		`UPDATE orders SET status = 'done' WHERE id >= {{cursor_start}} AND id <= {{cursor_end}} AND id = ANY({{batch_ids}})`,
		BatchBindParams{
			CursorStart: "1",
			CursorEnd:   "10",
			BatchIDs:    []string{"1", "2", "10"},
			CursorUDT:   "int8",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(args) != 3 {
		t.Fatalf("len(args) = %d, want 3", len(args))
	}
	if !strings.Contains(q, "($1::text::int8)") || !strings.Contains(q, "($2::text::int8)") || !strings.Contains(q, "($3::text[]::int8[])") {
		t.Fatalf("compiled SQL tidak sesuai: %s", q)
	}
	if _, _, err := CompileBatchSQL(`UPDATE t SET a = {{unknown_ph}}`, BatchBindParams{}); err == nil {
		t.Error("placeholder tidak dikenal harus ditolak")
	}
}
