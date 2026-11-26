package migration

import (
	"context"
	"errors"
	"net/url"
	"testing"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
)

func TestParseExecutionFilter(t *testing.T) {
	f, err := ParseExecutionFilter(url.Values{
		"migration_id": {"7"}, "status": {"success, failed"}, "action": {"up,rollback"},
		"actor": {"bob"}, "since": {"2026-11-01T00:00:00Z"}, "limit": {"5000"}, "offset": {"2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.MigrationID != 7 || !f.Statuses[ExecFailed] || !f.Actions[ActionRollback] || f.Actor != "bob" || f.Since == nil || f.Limit != maxHistoryLimit || f.Offset != 2 {
		t.Fatalf("filter = %+v", f)
	}
	for _, bad := range []url.Values{
		{"migration_id": {"x"}}, {"status": {"done"}}, {"action": {"nuke"}},
		{"since": {"kemarin"}}, {"limit": {"-1"}}, {"offset": {"a"}},
	} {
		if _, err := ParseExecutionFilter(bad); err == nil {
			t.Errorf("%v harus ditolak", bad)
		}
	}
}

func TestFilterAndPaginate_AfterHideRollback(t *testing.T) {
	t0 := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	e2 := int64(2)
	all := []Execution{
		{ID: 1, MigrationID: 10, Action: ActionUp, Status: ExecSuccess, Actor: "bob", CreatedAt: t0},
		{ID: 2, MigrationID: 11, Action: ActionUp, Status: ExecSuccess, Actor: "bob", CreatedAt: t0.Add(time.Hour)},
		{ID: 3, MigrationID: 11, Action: ActionRollback, Status: ExecSuccess, RevertedExecutionID: &e2, Actor: "alice", CreatedAt: t0.Add(2 * time.Hour)},
		{ID: 4, MigrationID: 11, Action: ActionForward, Status: ExecSuccess, Actor: "alice", CreatedAt: t0.Add(3 * time.Hour)},
		{ID: 5, MigrationID: 12, Action: ActionUp, Status: ExecFailed, Actor: "bob", CreatedAt: t0.Add(4 * time.Hour)},
	}
	got := FilterExecutions(FilterHideRollback(all), ExecutionFilter{MigrationID: 11})
	if len(got) != 1 || got[0].ID != 4 {
		t.Fatalf("hide rollback + filter = %+v", got)
	}
	since := t0.Add(90 * time.Minute)
	got = FilterExecutions(all, ExecutionFilter{Actor: "ALICE", Since: &since})
	if len(got) != 2 {
		t.Fatalf("actor+since = %+v", got)
	}
	got = FilterExecutions(all, ExecutionFilter{Statuses: map[ExecutionStatus]bool{ExecFailed: true}})
	if len(got) != 1 || got[0].ID != 5 {
		t.Fatalf("status = %+v", got)
	}
	page, total := Paginate(all, ExecutionFilter{Limit: 2, Offset: 1})
	if total != 5 || len(page) != 2 || page[0].ID != 2 {
		t.Fatalf("paginate = %d %+v", total, page)
	}
	if page, _ := Paginate(all, ExecutionFilter{Offset: 99}); len(page) != 0 {
		t.Fatal("offset melewati total harus kosong")
	}
}

type fakeGate struct {
	checkErr, consumeErr error
	consumedKey          string
	released, attached   string
}

func (f *fakeGate) CheckApproval(ctx context.Context, _, _ int64, _ Action) error {
	if ApprovalKeyFrom(ctx) == "" {
		return errors.New("key tidak diteruskan ke checker")
	}
	return f.checkErr
}
func (f *fakeGate) ConsumeApproval(_ context.Context, _, _ int64, _ Action, key string) error {
	f.consumedKey = key
	return f.consumeErr
}
func (f *fakeGate) ReleaseApproval(_ context.Context, key string) { f.released = key }
func (f *fakeGate) AttachApprovalJob(_ context.Context, key, jobID string) {
	f.attached = key + ":" + jobID
}

func TestClaimApproval_ConsumeReleaseAttach(t *testing.T) {
	ctx := context.Background()
	def := Definition{ID: 1, DatabaseID: 2}

	g := &fakeGate{}
	e := &Engine{ApprovalChecker: g}
	opts := EnqueueOptions{}
	_, claim, err := e.claimApproval(ctx, def, ActionUp, &opts)
	if err != nil || opts.IdempotencyKey == "" || g.consumedKey != opts.IdempotencyKey {
		t.Fatalf("claim: err=%v key=%q consumed=%q", err, opts.IdempotencyKey, g.consumedKey)
	}
	claim.settle(ctx, queue.Job{ID: "job-1"}, nil)
	if g.attached != opts.IdempotencyKey+":job-1" || g.released != "" {
		t.Fatalf("attach=%q release=%q", g.attached, g.released)
	}

	g = &fakeGate{}
	e.ApprovalChecker = g
	opts = EnqueueOptions{IdempotencyKey: "k2"}
	_, claim, _ = e.claimApproval(ctx, def, ActionUp, &opts)
	claim.settle(ctx, queue.Job{}, errors.New("broker down"))
	if g.released != "k2" || g.attached != "" {
		t.Fatalf("release=%q attach=%q", g.released, g.attached)
	}

	g = &fakeGate{checkErr: ErrApprovalRequired}
	e.ApprovalChecker = g
	if _, _, err := e.claimApproval(ctx, def, ActionUp, &EnqueueOptions{}); !errors.Is(err, ErrApprovalRequired) || g.consumedKey != "" {
		t.Fatalf("err=%v consumed=%q", err, g.consumedKey)
	}

	g = &fakeGate{consumeErr: ErrApprovalRequired}
	e.ApprovalChecker = g
	if _, _, err := e.claimApproval(ctx, def, ActionUp, &EnqueueOptions{}); !errors.Is(err, ErrApprovalRequired) {
		t.Fatalf("err=%v", err)
	}

	e.ApprovalChecker = nil
	_, claim, err = e.claimApproval(ctx, def, ActionUp, &EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	claim.settle(ctx, queue.Job{ID: "x"}, nil)
}
