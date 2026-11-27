package metrics

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func render(t *testing.T, r *Registry) string {
	t.Helper()
	var buf bytes.Buffer
	if err := r.WriteText(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestCounterAndGaugeExposition(t *testing.T) {
	r := NewRegistry()
	c := r.Counter("test_requests_total", "Request count.", "method")
	c.Inc("GET")
	c.Add(2, "GET")
	c.Add(-5, "GET")
	g := r.Gauge("test_in_flight", "In flight.")
	g.Inc()
	g.Inc()
	g.Dec()

	out := render(t, r)
	for _, want := range []string{
		"# TYPE test_requests_total counter",
		`test_requests_total{method="GET"} 3`,
		"# TYPE test_in_flight gauge",
		"test_in_flight 1",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output tidak mengandung %q:\n%s", want, out)
		}
	}
}

func TestHistogramCumulativeBuckets(t *testing.T) {
	r := NewRegistry()
	h := r.Histogram("test_latency_seconds", "Latency.", []float64{0.1, 1}, "route")
	h.Observe(0.05, "x")
	h.Observe(0.5, "x")
	h.Observe(5, "x")
	h.Observe(-1, "x")

	out := render(t, r)
	for _, want := range []string{
		`test_latency_seconds_bucket{route="x",le="0.1"} 2`,
		`test_latency_seconds_bucket{route="x",le="1"} 3`,
		`test_latency_seconds_bucket{route="x",le="+Inf"} 4`,
		`test_latency_seconds_sum{route="x"} 5.55`,
		`test_latency_seconds_count{route="x"} 4`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output tidak mengandung %q:\n%s", want, out)
		}
	}
}

func TestLabelEscapingAndRegisterIdempotent(t *testing.T) {
	r := NewRegistry()
	a := r.Counter("test_total", "x", "v")
	b := r.Counter("test_total", "x", "v")
	if a != b {
		t.Fatal("register ulang harus mengembalikan family yang sama")
	}
	a.Inc("a\"b\\c\nd")
	out := render(t, r)
	if !strings.Contains(out, `test_total{v="a\"b\\c\nd"} 1`) {
		t.Fatalf("escape label salah:\n%s", out)
	}
}

func TestCollectorReplacedAndRemoved(t *testing.T) {
	r := NewRegistry()
	r.RegisterCollector("c", func(ctx context.Context) []Snapshot {
		return []Snapshot{{Name: "test_dyn", Help: "d", Kind: KindGauge, Samples: []Sample{{Value: 1}}}}
	})
	r.RegisterCollector("c", func(ctx context.Context) []Snapshot {
		return []Snapshot{{Name: "test_dyn", Help: "d", Kind: KindGauge, Samples: []Sample{{Value: 2}}}}
	})
	if out := render(t, r); !strings.Contains(out, "test_dyn 2") || strings.Contains(out, "test_dyn 1") {
		t.Fatalf("collector tidak diganti:\n%s", out)
	}
	r.RegisterCollector("c", nil)
	if out := render(t, r); strings.Contains(out, "test_dyn") {
		t.Fatalf("collector tidak dihapus:\n%s", out)
	}
}

func TestDBPoolCollector(t *testing.T) {
	RegisterDBPool("unit", func() sql.DBStats {
		return sql.DBStats{OpenConnections: 3, InUse: 2, Idle: 1, MaxOpenConnections: 10, WaitCount: 4, WaitDuration: 1500 * time.Millisecond}
	})
	defer RegisterDBPool("unit", nil)
	out := render(t, Default)
	for _, want := range []string{
		`if3110_db_pool_active_connections{pool="unit"} 2`,
		`if3110_db_pool_idle_connections{pool="unit"} 1`,
		`if3110_db_pool_wait_duration_seconds_total{pool="unit"} 1.5`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output tidak mengandung %q", want)
		}
	}
}

func TestRouteLabelBoundedCardinality(t *testing.T) {
	cases := map[string]string{
		"/api/databases/12/migrations/definitions/3/execute": "databases_enqueue",
		"/api/databases/12/schema":                           "databases",
		"/api/auth/login":                                    "auth",
		"/api/random-" + strings.Repeat("x", 40):             "api_other",
		"/databases/12/history":                              "spa",
		"/metrics":                                           "metrics",
	}
	for path, want := range cases {
		if got := RouteLabel(path); got != want {
			t.Fatalf("RouteLabel(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestUnknownLabelsCollapsed(t *testing.T) {
	JobEnqueued("drop table users")
	JobFinished("up", "weird-status", time.Time{})
	out := render(t, Default)
	if strings.Contains(out, "drop table") || strings.Contains(out, "weird-status") {
		t.Fatalf("label bebas tidak boleh masuk metrics:\n%s", out)
	}
	if !strings.Contains(out, `if3110_migration_jobs_enqueued_total{action="other"}`) {
		t.Fatal("action tak dikenal harus dipetakan ke other")
	}
}

func TestJobLifecycleMetrics(t *testing.T) {
	JobStarted("batch_up", time.Now().Add(-2*time.Second))
	JobAttemptDone("batch_up", "completed", 3*time.Second)
	JobFinished("batch_up", "completed", time.Now().Add(-5*time.Second))
	BatchCommitted("batch_up", 1000)
	LockWait("acquired", 10*time.Millisecond)
	out := render(t, Default)
	for _, want := range []string{
		`if3110_worker_jobs_in_flight{action="batch_up"} 0`,
		`if3110_migration_jobs_finished_total{action="batch_up",status="completed"}`,
		`if3110_migration_rows_processed_total{action="batch_up"} 1000`,
		`if3110_migration_queue_wait_seconds_count{action="batch_up"}`,
		`if3110_migration_lock_wait_seconds_count{result="acquired"}`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output tidak mengandung %q", want)
		}
	}
}

func TestHandlerAndMiddleware(t *testing.T) {
	h := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/health", nil))

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain; version=0.0.4") {
		t.Fatalf("content-type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), `if3110_http_requests_total{method="GET",route="health",code="4xx"} 1`) {
		t.Fatalf("request tidak tercatat:\n%s", rec.Body.String())
	}
}
