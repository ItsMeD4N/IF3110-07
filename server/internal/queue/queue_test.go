package queue_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ItsMeD4N/IF3110-07/server/internal/dblock"
	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
	"github.com/ItsMeD4N/IF3110-07/server/internal/secretbox"
	"github.com/ItsMeD4N/IF3110-07/server/internal/store"
	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func TestBackoffPolicy_AndTransientClassification(t *testing.T) {
	p := queue.BackoffPolicy{
		BaseDelay:   100 * time.Millisecond,
		MaxDelay:    500 * time.Millisecond,
		MaxAttempts: 5,
		JitterRatio: 0,
	}
	if d := p.NextDelay(1); d != 100*time.Millisecond {
		t.Errorf("attempt 1 = %v, want 100ms", d)
	}
	if d := p.NextDelay(2); d != 200*time.Millisecond {
		t.Errorf("attempt 2 = %v, want 200ms", d)
	}
	if d := p.NextDelay(3); d != 400*time.Millisecond {
		t.Errorf("attempt 3 = %v, want 400ms", d)
	}
	if d := p.NextDelay(4); d != 500*time.Millisecond {
		t.Errorf("attempt 4 = %v, want capped 500ms", d)
	}

	transientErrs := []error{
		&pq.Error{Code: "40001"},
		&pq.Error{Code: "40P01"},
		&pq.Error{Code: "08006"},
		&pq.Error{Code: "57P01"},
		driver.ErrBadConn,
		io.EOF,
		queue.MarkTransient(errors.New("temporary")),
	}
	for _, err := range transientErrs {
		if !queue.IsTransientError(err) {
			t.Errorf("error %v harus diklasifikasikan transient", err)
		}
	}

	permanentErrs := []error{
		&pq.Error{Code: "42601"},
		&pq.Error{Code: "42P01"},
		&pq.Error{Code: "23505"},
		&pq.Error{Code: "23502"},
		errors.New("invalid migration syntax"),
	}
	for _, err := range permanentErrs {
		if queue.IsTransientError(err) {
			t.Errorf("error %v tidak boleh diklasifikasikan transient", err)
		}
	}

	if !queue.IsUncertainCommitError(io.EOF) || !queue.IsUncertainCommitError(&pq.Error{Code: "08006"}) {
		t.Error("EOF dan SQLSTATE 08006 pada COMMIT harus dianggap uncertain")
	}
	if queue.IsUncertainCommitError(&pq.Error{Code: "40001"}) || queue.IsUncertainCommitError(&pq.Error{Code: "23505"}) {
		t.Error("error SQLSTATE non-connection bukan uncertain commit")
	}
}

type memBroker struct {
	mu      sync.Mutex
	jobs    map[string]*queue.Job
	order   []string
	retries int
	contend int
}

func newMemBroker() *memBroker {
	return &memBroker{jobs: map[string]*queue.Job{}}
}

func (m *memBroker) Enqueue(ctx context.Context, req queue.EnqueueRequest) (queue.Job, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.IdempotencyKey == req.IdempotencyKey {
			return *j, true, nil
		}
	}
	id := req.IdempotencyKey
	if id == "" {
		id = testutil.Unique("job")
	}
	maxAtt := req.MaxAttempts
	if maxAtt <= 0 {
		maxAtt = 3
	}
	j := &queue.Job{
		ID:             id,
		DatabaseID:     req.DatabaseID,
		MigrationID:    req.MigrationID,
		Action:         req.Action,
		Actor:          req.Actor,
		IdempotencyKey: id,
		TraceContext:   telemetry.InjectTraceContext(ctx),
		Status:         queue.StatusPending,
		MaxAttempts:    maxAtt,
		RunAt:          time.Now().UTC(),
		CreatedAt:      time.Now().UTC(),
	}
	m.jobs[id] = j
	m.order = append(m.order, id)
	return *j, false, nil
}

func (m *memBroker) Dequeue(_ context.Context, workerID string, _ time.Duration) (*queue.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().UTC()
	for _, id := range m.order {
		j := m.jobs[id]
		if (j.Status == queue.StatusPending || j.Status == queue.StatusRetrying) && !j.RunAt.After(now) {
			j.Status = queue.StatusRunning
			j.Attempt++
			j.LockedBy = workerID
			copied := *j
			return &copied, nil
		}
	}
	return nil, nil
}

func (m *memBroker) RenewLease(context.Context, string, string, time.Duration) error { return nil }
func (m *memBroker) Complete(_ context.Context, jobID string, execID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs[jobID].Status = queue.StatusCompleted
	m.jobs[jobID].ExecutionID = execID
	return nil
}
func (m *memBroker) MarkStatus(_ context.Context, jobID string, st queue.JobStatus, execID int64, errMsg, sqlState string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	j.Status = st
	j.ExecutionID = execID
	j.LastError = errMsg
	j.LastSQLState = sqlState
	return nil
}
func (m *memBroker) Retry(_ context.Context, jobID string, execID int64, nextRunAt time.Time, errMsg, sqlState string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.retries++
	j := m.jobs[jobID]
	j.Status = queue.StatusRetrying
	j.ExecutionID = execID
	j.RunAt = nextRunAt
	j.LastError = errMsg
	j.LastSQLState = sqlState
	return nil
}
func (m *memBroker) RescheduleContended(_ context.Context, jobID string, nextRunAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.contend++
	j := m.jobs[jobID]
	j.Status = queue.StatusPending
	if j.Attempt > 0 {
		j.Attempt--
	}
	j.RunAt = nextRunAt
	return nil
}
func (m *memBroker) ReclaimExpired(context.Context) ([]queue.Job, error) { return nil, nil }
func (m *memBroker) GetJob(_ context.Context, _ int64, jobID string) (queue.Job, error) {
	return m.GetJobByID(context.Background(), jobID)
}
func (m *memBroker) GetJobByID(_ context.Context, jobID string) (queue.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.jobs[jobID]; ok {
		return *j, nil
	}
	return queue.Job{}, queue.ErrJobNotFound
}
func (m *memBroker) ListJobs(context.Context, int64, int) ([]queue.Job, error) { return nil, nil }
func (m *memBroker) Subscribe(context.Context) (<-chan struct{}, func()) {
	return make(chan struct{}), func() {}
}
func (m *memBroker) Close() error { return nil }

type handlerFunc func(ctx context.Context, job *queue.Job) queue.JobOutcome

func (f handlerFunc) HandleJob(ctx context.Context, job *queue.Job) queue.JobOutcome {
	return f(ctx, job)
}

func TestWorkerPool_Unit_RetriesTransientAndRespectsContention(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})

	broker := newMemBroker()
	var calls atomic.Int32
	var seenTraceID string

	h := handlerFunc(func(ctx context.Context, job *queue.Job) queue.JobOutcome {
		seenTraceID = telemetry.TraceID(ctx)
		n := calls.Add(1)
		switch n {
		case 1:
			return queue.JobOutcome{LockContended: true}
		case 2:
			return queue.JobOutcome{Status: queue.StatusFailed, Retryable: true, Err: driver.ErrBadConn}
		default:
			return queue.JobOutcome{Status: queue.StatusCompleted, ExecutionID: 99}
		}
	})

	pool := queue.NewWorkerPool(broker, h, queue.PoolOptions{
		Concurrency:     1,
		PollInterval:    10 * time.Millisecond,
		ContentionDelay: 15 * time.Millisecond,
		Backoff: queue.BackoffPolicy{
			BaseDelay:   15 * time.Millisecond,
			MaxDelay:    50 * time.Millisecond,
			MaxAttempts: 3,
		},
	})

	apiCtx, apiSpan := telemetry.Tracer().Start(telemetry.WithCorrelationID(context.Background(), "unit-corr"), "api.enqueue")
	apiTraceID := apiSpan.SpanContext().TraceID().String()
	job, _, err := broker.Enqueue(apiCtx, queue.EnqueueRequest{
		DatabaseID:     1,
		MigrationID:    1,
		Action:         "up",
		Actor:          "alice",
		IdempotencyKey: "k1",
		MaxAttempts:    3,
	})
	apiSpan.End()
	if err != nil {
		t.Fatal(err)
	}

	pool.Start(context.Background())
	defer pool.Stop(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := broker.GetJobByID(context.Background(), job.ID)
		if got.Status == queue.StatusCompleted {
			if got.Attempt != 2 {
				t.Fatalf("attempt = %d, want 2 (1 contention + 1 retry + 1 success)", got.Attempt)
			}
			if broker.contend != 1 || broker.retries != 1 {
				t.Fatalf("contend=%d retries=%d, want 1 and 1", broker.contend, broker.retries)
			}
			if seenTraceID != apiTraceID {
				t.Fatalf("trace_id worker (%s) != api (%s)", seenTraceID, apiTraceID)
			}
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("job tidak kunjung completed")
}

func setupQueueFixture(t *testing.T, db *sql.DB) (dbID, migID int64) {
	t.Helper()
	box, _ := secretbox.New(secretbox.DevKey())
	st := store.New(db, box)
	conn, err := st.CreateConnection(context.Background(), store.ConnectionInput{
		Name:         testutil.Unique("qdb"),
		Host:         "127.0.0.1",
		Port:         5432,
		DatabaseName: "sample_db",
		Username:     "sample_app",
		SSLMode:      "disable",
	}, "sample_app")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.DeleteConnection(context.Background(), conn.ID) })

	err = db.QueryRow(`
		INSERT INTO platform_migrations (database_id, version, name, sql_up, sql_down, created_by, updated_by)
		VALUES ($1, 1, 'qtest', 'SELECT 1', 'SELECT 1', 'tester', 'tester')
		RETURNING id`, conn.ID).Scan(&migID)
	if err != nil {
		t.Fatal(err)
	}
	return conn.ID, migID
}

func TestPostgresBroker_DurableDeduplicateAndFairAcrossDatabases(t *testing.T) {
	db := testutil.OpenDB(t)
	ctx := context.Background()
	db1, mig1 := setupQueueFixture(t, db)
	db2, mig2 := setupQueueFixture(t, db)

	broker1 := queue.NewPostgresBroker(db, "")
	defer broker1.Close()

	key := testutil.Unique("idem")
	j1, dedup1, err := broker1.Enqueue(ctx, queue.EnqueueRequest{
		DatabaseID:     db1,
		MigrationID:    mig1,
		Action:         "up",
		Actor:          "alice",
		IdempotencyKey: key,
	})
	if err != nil || dedup1 {
		t.Fatalf("enqueue 1: dedup=%v err=%v", dedup1, err)
	}

	j1Dup, dedup2, err := broker1.Enqueue(ctx, queue.EnqueueRequest{
		DatabaseID:     db1,
		MigrationID:    mig1,
		Action:         "up",
		Actor:          "alice",
		IdempotencyKey: key,
	})
	if err != nil || !dedup2 || j1Dup.ID != j1.ID {
		t.Fatalf("expected deduplicated job %s, got id=%s dedup=%v err=%v", j1.ID, j1Dup.ID, dedup2, err)
	}

	j2, _, err := broker1.Enqueue(ctx, queue.EnqueueRequest{
		DatabaseID:     db2,
		MigrationID:    mig2,
		Action:         "up",
		Actor:          "bob",
		IdempotencyKey: testutil.Unique("idem"),
	})
	if err != nil {
		t.Fatal(err)
	}

	locker := dblock.NewPostgresLocker(db)
	lease1, err := locker.Acquire(ctx, dblock.AcquireRequest{DatabaseID: db1, OwnerID: "busy-worker", TTL: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Release(ctx, lease1)

	broker2 := queue.NewPostgresBroker(db, "")
	defer broker2.Close()

	claimed, err := broker2.Dequeue(ctx, "fresh-worker", 10*time.Second)
	if err != nil || claimed == nil {
		t.Fatalf("dequeue after restart gagal: job=%v err=%v", claimed, err)
	}
	if claimed.ID != j2.ID {
		t.Fatalf("dequeue seharusnya melewati db1 yang terkunci dan mengambil j2 (%s), didapat %s", j2.ID, claimed.ID)
	}
	_ = broker2.Complete(ctx, claimed.ID, 0)

	_ = locker.Release(ctx, lease1)
	claimed1, err := broker2.Dequeue(ctx, "fresh-worker", 10*time.Second)
	if err != nil || claimed1 == nil || claimed1.ID != j1.ID {
		t.Fatalf("setelah lock db1 dilepas harus dapat j1 (%s), got %+v err=%v", j1.ID, claimed1, err)
	}
	_ = broker2.Complete(ctx, claimed1.ID, 0)
}
