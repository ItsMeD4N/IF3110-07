package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ItsMeD4N/IF3110-07/server/internal/telemetry"
)

func setupTestTracer(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	prevTP := otel.GetTracerProvider()
	prevProp := otel.GetTextMapPropagator()
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(prevTP)
		otel.SetTextMapPropagator(prevProp)
	})
	return sr
}

func TestMaskString_AndSensitiveKeys(t *testing.T) {
	cases := []struct {
		input     string
		mustHide  string
		mustKeep  string
	}{
		{
			input:    "postgres://sample_app:SuperSecret123@localhost:5432/sample_db?sslmode=disable",
			mustHide: "SuperSecret123",
			mustKeep: "postgres://sample_app:[REDACTED]@localhost:5432/sample_db",
		},
		{
			input:    "ALTER USER alice WITH PASSWORD 'MySecretPass!';",
			mustHide: "MySecretPass!",
			mustKeep: "PASSWORD '[REDACTED]'",
		},
		{
			input:    "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.sig",
			mustHide: "eyJhbGciOiJIUzI1NiJ9",
			mustKeep: "Bearer [REDACTED]",
		},
		{
			input:    "Cookie: app_session_id=abc987xyz; csrf_token=tok555",
			mustHide: "abc987xyz",
			mustKeep: "app_session_id=[REDACTED]",
		},
	}

	for _, tc := range cases {
		got := telemetry.MaskString(tc.input)
		if strings.Contains(got, tc.mustHide) {
			t.Errorf("MaskString(%q) = %q masih membocorkan %q", tc.input, got, tc.mustHide)
		}
		if !strings.Contains(got, tc.mustKeep) {
			t.Errorf("MaskString(%q) = %q tidak memuat %q", tc.input, got, tc.mustKeep)
		}
	}

	if !telemetry.IsSensitiveKey("password") || !telemetry.IsSensitiveKey("Session_Token") || !telemetry.IsSensitiveKey("csrf_token") {
		t.Error("sensitive keys harus terdeteksi")
	}
	if telemetry.IsSensitiveKey("lock_token") || telemetry.IsSensitiveKey("database_id") || telemetry.IsSensitiveKey("query") {
		t.Error("non-sensitive keys tidak boleh diblok total")
	}
}

func TestMaskingJSONHandler_InjectsTraceAndMasksSecrets(t *testing.T) {
	_ = setupTestTracer(t)

	var buf bytes.Buffer
	logger := telemetry.NewLogger(&buf, slog.LevelDebug)

	ctx := telemetry.WithCorrelationID(context.Background(), "corr-123")
	ctx = telemetry.WithActor(ctx, "alice")
	ctx = telemetry.WithDatabaseID(ctx, 42)
	ctx, span := telemetry.Tracer().Start(ctx, "test.op")
	wantTraceID := span.SpanContext().TraceID().String()
	wantSpanID := span.SpanContext().SpanID().String()

	logger.InfoContext(ctx, "connect to postgres://admin:TopSecret99@db:5432/prod",
		slog.String("event", "db.connect"),
		slog.String("password", "raw-password-value"),
		slog.String("session_id", "sess-opaque-token"),
		slog.Any("err", errors.New("failed with app_session_id=leakedcookie123")),
		slog.Group("nested", slog.String("api_key", "key-999"), slog.String("safe", "ok")),
	)
	span.End()

	raw := buf.String()
	for _, secret := range []string{"TopSecret99", "raw-password-value", "sess-opaque-token", "leakedcookie123", "key-999"} {
		if strings.Contains(raw, secret) {
			t.Fatalf("structured JSON log membocorkan rahasia %q:\n%s", secret, raw)
		}
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &parsed); err != nil {
		t.Fatalf("log bukan JSON valid: %v\nraw=%s", err, raw)
	}
	if parsed["trace_id"] != wantTraceID {
		t.Errorf("trace_id = %v, want %s", parsed["trace_id"], wantTraceID)
	}
	if parsed["span_id"] != wantSpanID {
		t.Errorf("span_id = %v, want %s", parsed["span_id"], wantSpanID)
	}
	if parsed["correlation_id"] != "corr-123" || parsed["actor"] != "alice" {
		t.Errorf("context attrs tidak lengkap: %+v", parsed)
	}
	if parsed["password"] != telemetry.RedactedValue || parsed["session_id"] != telemetry.RedactedValue {
		t.Errorf("sensitive attrs tidak di-redact: %+v", parsed)
	}
}

func TestTraceContextPropagation_ThroughQueueCarrier(t *testing.T) {
	sr := setupTestTracer(t)

	apiCtx := telemetry.WithCorrelationID(context.Background(), "req-corr-777")
	apiCtx, apiSpan := telemetry.Tracer().Start(apiCtx, "api.request")
	carrier := telemetry.InjectTraceContext(apiCtx)
	apiSpan.End()

	if carrier["traceparent"] == "" {
		t.Fatalf("carrier harus berisi W3C traceparent, got %+v", carrier)
	}
	if carrier["correlation_id"] != "req-corr-777" {
		t.Fatalf("correlation_id tidak terpropagasi: %+v", carrier)
	}

	workerCtx := telemetry.ExtractTraceContext(context.Background(), carrier)
	if got := telemetry.CorrelationIDFrom(workerCtx); got != "req-corr-777" {
		t.Fatalf("extracted correlation_id = %q, want req-corr-777", got)
	}
	_, workerSpan := telemetry.Tracer().Start(workerCtx, "worker.process_job")
	workerSpan.End()

	spans := sr.Ended()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
	if spans[0].SpanContext().TraceID() != spans[1].SpanContext().TraceID() {
		t.Fatalf("trace_id tidak sama antara API (%s) dan Worker (%s)",
			spans[0].SpanContext().TraceID(), spans[1].SpanContext().TraceID())
	}
	if spans[1].Parent().SpanID() != spans[0].SpanContext().SpanID() {
		t.Fatalf("worker span parent (%s) != api span (%s)",
			spans[1].Parent().SpanID(), spans[0].SpanContext().SpanID())
	}
}

func TestHTTPMiddleware_RecordsSpanAndActorInJSONLog(t *testing.T) {
	sr := setupTestTracer(t)

	var buf bytes.Buffer
	prevLogger := slog.Default()
	telemetry.InitLogger(&buf, slog.LevelInfo)
	t.Cleanup(func() { slog.SetDefault(prevLogger) })

	h := telemetry.HTTPMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		telemetry.SetRequestActor(r.Context(), "bob")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))

	req := httptest.NewRequest("POST", "/api/databases/1/migrations/definitions/1/execute", nil)
	req.Header.Set("X-Correlation-ID", "corr-http-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rec.Code)
	}
	if rec.Header().Get("X-Correlation-ID") != "corr-http-1" {
		t.Errorf("X-Correlation-ID = %q", rec.Header().Get("X-Correlation-ID"))
	}
	if rec.Header().Get("X-Trace-ID") == "" {
		t.Error("X-Trace-ID header kosong")
	}

	spans := sr.Ended()
	if len(spans) != 1 || spans[0].Name() != "HTTP POST /api/databases/1/migrations/definitions/1/execute" {
		t.Fatalf("unexpected spans: %+v", spans)
	}

	var logEntry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &logEntry); err != nil {
		t.Fatalf("invalid json log: %v (%s)", err, buf.String())
	}
	if logEntry["event"] != "http.request" || logEntry["actor"] != "bob" || logEntry["correlation_id"] != "corr-http-1" {
		t.Errorf("unexpected http log entry: %+v", logEntry)
	}
}
