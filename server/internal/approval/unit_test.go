package approval

import (
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/migration"
)

func TestNormalizeAction(t *testing.T) {
	normal := migration.Definition{ExecutionMode: migration.ModeNormal}
	batch := migration.Definition{ExecutionMode: migration.ModeBatch}
	cases := []struct {
		def     migration.Definition
		in, out migration.Action
		wantErr bool
	}{
		{normal, migration.ActionUp, migration.ActionUp, false},
		{normal, migration.ActionBatchUp, migration.ActionUp, false},
		{batch, migration.ActionUp, migration.ActionBatchUp, false},
		{normal, migration.ActionRollback, migration.ActionRollback, false},
		{batch, migration.ActionRollback, migration.ActionBatchRevert, false},
		{batch, migration.ActionForward, migration.ActionForward, false},
		{batch, migration.ActionBatchResume, migration.ActionBatchResume, false},
		{normal, migration.ActionBatchResume, "", true},
		{normal, migration.ActionBatchRevert, "", true},
		{normal, "drop", "", true},
	}
	for _, c := range cases {
		got, err := NormalizeAction(c.def, c.in)
		if (err != nil) != c.wantErr || got != c.out {
			t.Errorf("NormalizeAction(%s, %s) = %q, %v", c.def.ExecutionMode, c.in, got, err)
		}
	}
}

func TestParseAction(t *testing.T) {
	for _, a := range []string{"up", "batch_up", "rollback", "forward", "batch_resume", "batch_revert"} {
		if _, ok := ParseAction(a); !ok {
			t.Errorf("%s harus valid", a)
		}
	}
	for _, a := range []string{"", "UP", "delete", "execute"} {
		if _, ok := ParseAction(a); ok {
			t.Errorf("%q harus ditolak", a)
		}
	}
}

func i64(v int64) *int64   { return &v }
func str(v string) *string { return &v }

func TestSummarizeAndFinalize(t *testing.T) {
	r := Request{
		Status: StatusPendingReview,
		Approvals: []Decision{
			{GroupID: i64(1), LeaderUserID: str("u1"), Decision: DecisionApproved},
			{GroupID: i64(2), LeaderUserID: str("u2"), Decision: DecisionPending},
			{GroupID: nil, Decision: DecisionPending}, // group sudah dihapus: tidak dihitung
		},
	}
	finalize(&r, &authz.Principal{ID: "u2"})
	if r.RequiredApprovals != 2 || r.ApprovedCount != 1 || !r.CanCurrentUserDecide {
		t.Fatalf("u2: %+v", r)
	}
	finalize(&r, &authz.Principal{ID: "u1"})
	if r.CanCurrentUserDecide {
		t.Fatal("u1 sudah memutuskan, tidak boleh bisa memutuskan lagi")
	}
	finalize(&r, nil)
	if r.CanCurrentUserDecide {
		t.Fatal("anonim tidak boleh memutuskan")
	}
	r.Status = StatusApproved
	finalize(&r, &authz.Principal{ID: "u2"})
	if r.CanCurrentUserDecide {
		t.Fatal("pengajuan yang tidak pending tidak dapat diputuskan")
	}

	_, _, _, rejected := summarize([]Decision{{GroupID: i64(1), Decision: DecisionRejected}})
	if !rejected {
		t.Fatal("rejected harus terdeteksi")
	}
}

func TestIsWorkflowAction(t *testing.T) {
	if !IsWorkflowAction(migration.ActionUp) || !IsWorkflowAction(migration.ActionBatchUp) {
		t.Fatal("up/batch_up mengubah workflow")
	}
	if IsWorkflowAction(migration.ActionRollback) || IsWorkflowAction(migration.ActionForward) {
		t.Fatal("rollback/forward tidak mengubah workflow_status definisi")
	}
}
