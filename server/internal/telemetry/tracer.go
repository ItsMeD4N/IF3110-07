package telemetry

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const TracerName = "github.com/ItsMeD4N/IF3110-07/server"

type TracingConfig struct {
	ServiceName  string
	Environment  string
	Exporter     string
	OTLPEndpoint string
	StdoutWriter io.Writer
}

func InitTracing(ctx context.Context, cfg TracingConfig) (*sdktrace.TracerProvider, func(context.Context) error, error) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "if3110-db-platform"
	}
	if cfg.Environment == "" {
		cfg.Environment = "development"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("telemetry: init resource: %w", err)
	}

	opts := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	}

	switch strings.ToLower(strings.TrimSpace(cfg.Exporter)) {
	case "", "stdout", "console":
		w := cfg.StdoutWriter
		if w == nil {
			w = os.Stderr
		}
		exp, err := stdouttrace.New(stdouttrace.WithWriter(w))
		if err != nil {
			return nil, nil, fmt.Errorf("telemetry: init stdout exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	case "otlp":
		otlpOpts := []otlptracehttp.Option{otlptracehttp.WithInsecure()}
		if cfg.OTLPEndpoint != "" {
			otlpOpts = append(otlpOpts, otlptracehttp.WithEndpoint(cfg.OTLPEndpoint))
		}
		exp, err := otlptracehttp.New(ctx, otlpOpts...)
		if err != nil {
			return nil, nil, fmt.Errorf("telemetry: init otlp exporter: %w", err)
		}
		opts = append(opts, sdktrace.WithBatcher(exp))
	case "none", "noop", "off":
	default:
		return nil, nil, fmt.Errorf("telemetry: exporter %q tidak dikenal (gunakan stdout, otlp, atau noop)", cfg.Exporter)
	}

	tp := sdktrace.NewTracerProvider(opts...)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return tp, tp.Shutdown, nil
}

func Tracer() trace.Tracer {
	return otel.Tracer(TracerName)
}

func InjectTraceContext(ctx context.Context) map[string]string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	out := make(map[string]string, len(carrier)+1)
	for k, v := range carrier {
		out[k] = v
	}
	if corrID := CorrelationIDFrom(ctx); corrID != "" {
		out["correlation_id"] = corrID
	}
	return out
}

func ExtractTraceContext(ctx context.Context, carrier map[string]string) context.Context {
	if len(carrier) == 0 {
		return ctx
	}
	mc := propagation.MapCarrier(carrier)
	ctx = otel.GetTextMapPropagator().Extract(ctx, mc)
	if corrID := carrier["correlation_id"]; corrID != "" {
		ctx = WithCorrelationID(ctx, corrID)
	}
	return ctx
}

func TraceID(ctx context.Context) string {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.IsValid() {
		return sc.TraceID().String()
	}
	return ""
}

func SpanID(ctx context.Context) string {
	sc := trace.SpanFromContext(ctx).SpanContext()
	if sc.IsValid() {
		return sc.SpanID().String()
	}
	return ""
}
