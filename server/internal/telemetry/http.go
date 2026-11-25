package telemetry

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type ActorExtractor func(ctx context.Context) string

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (sr *statusRecorder) WriteHeader(code int) {
	sr.status = code
	sr.ResponseWriter.WriteHeader(code)
}

func (sr *statusRecorder) Write(b []byte) (int, error) {
	if sr.status == 0 {
		sr.status = http.StatusOK
	}
	n, err := sr.ResponseWriter.Write(b)
	sr.bytes += n
	return n, err
}

func (sr *statusRecorder) Unwrap() http.ResponseWriter {
	return sr.ResponseWriter
}

func HTTPMiddleware(extractActor ActorExtractor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			corrID := r.Header.Get("X-Correlation-ID")
			if corrID == "" {
				corrID = r.Header.Get("X-Request-ID")
			}
			if corrID == "" {
				corrID = NewCorrelationID()
			}
			ctx = WithCorrelationID(ctx, corrID)
			ctx = WithActorSlot(ctx)

			spanName := "HTTP " + r.Method + " " + r.URL.Path
			ctx, span := Tracer().Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					attribute.String("http.request.method", r.Method),
					attribute.String("url.path", r.URL.Path),
					attribute.String("correlation_id", corrID),
				),
			)
			defer span.End()

			w.Header().Set("X-Correlation-ID", corrID)
			if tid := TraceID(ctx); tid != "" {
				w.Header().Set("X-Trace-ID", tid)
			}

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			req := r.WithContext(ctx)
			next.ServeHTTP(rec, req)

			actor := ActorFrom(ctx)
			if actor == "" && extractActor != nil {
				actor = extractActor(req.Context())
			}
			if actor != "" {
				ctx = WithActor(ctx, actor)
				span.SetAttributes(attribute.String("enduser.id", actor))
			}

			dur := time.Since(start)
			span.SetAttributes(
				attribute.Int("http.response.status_code", rec.status),
				attribute.Int("http.response.body.size", rec.bytes),
				attribute.Float64("http.duration_ms", float64(dur.Microseconds())/1000.0),
			)

			statusLabel := "ok"
			level := slog.LevelInfo
			if rec.status >= 500 {
				statusLabel = "error"
				level = slog.LevelError
				span.SetStatus(codes.Error, http.StatusText(rec.status))
			} else if rec.status >= 400 {
				statusLabel = "client_error"
				level = slog.LevelWarn
			}

			LogEvent(ctx, level, "http.request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status_code", rec.status),
				slog.String("status", statusLabel),
				slog.Float64("duration_ms", float64(dur.Microseconds())/1000.0),
				slog.Int("response_bytes", rec.bytes),
			)
		})
	}
}
