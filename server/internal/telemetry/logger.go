package telemetry

import (
	"context"
	"io"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel/trace"
)

type MaskingJSONHandler struct {
	next slog.Handler
}

func NewMaskingJSONHandler(w io.Writer, opts *slog.HandlerOptions) *MaskingJSONHandler {
	if w == nil {
		w = os.Stdout
	}
	if opts == nil {
		opts = &slog.HandlerOptions{Level: slog.LevelInfo}
	}
	origReplace := opts.ReplaceAttr
	copied := *opts
	copied.ReplaceAttr = func(groups []string, a slog.Attr) slog.Attr {
		if a.Key == slog.TimeKey && a.Value.Kind() == slog.KindTime {
			a.Value = slog.StringValue(a.Value.Time().UTC().Format(time.RFC3339Nano))
		}
		a = MaskAttr(a)
		if origReplace != nil {
			a = origReplace(groups, a)
		}
		return a
	}
	return &MaskingJSONHandler{
		next: slog.NewJSONHandler(w, &copied),
	}
}

func (h *MaskingJSONHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *MaskingJSONHandler) Handle(ctx context.Context, r slog.Record) error {
	r2 := slog.NewRecord(r.Time, r.Level, MaskString(r.Message), r.PC)

	seen := map[string]bool{}
	r.Attrs(func(a slog.Attr) bool {
		seen[a.Key] = true
		r2.AddAttrs(MaskAttr(a))
		return true
	})

	if ctx != nil {
		if sc := trace.SpanFromContext(ctx).SpanContext(); sc.IsValid() {
			if !seen["trace_id"] {
				r2.AddAttrs(slog.String("trace_id", sc.TraceID().String()))
			}
			if !seen["span_id"] {
				r2.AddAttrs(slog.String("span_id", sc.SpanID().String()))
			}
		}
		if corrID := CorrelationIDFrom(ctx); corrID != "" && !seen["correlation_id"] {
			r2.AddAttrs(slog.String("correlation_id", corrID))
		}
		if actor := ActorFrom(ctx); actor != "" && !seen["actor"] {
			r2.AddAttrs(slog.String("actor", actor))
		}
		if dbID := DatabaseIDFrom(ctx); dbID > 0 && !seen["database_id"] {
			r2.AddAttrs(slog.Int64("database_id", dbID))
		}
	}

	return h.next.Handle(ctx, r2)
}

func (h *MaskingJSONHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	masked := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		masked[i] = MaskAttr(a)
	}
	return &MaskingJSONHandler{next: h.next.WithAttrs(masked)}
}

func (h *MaskingJSONHandler) WithGroup(name string) slog.Handler {
	return &MaskingJSONHandler{next: h.next.WithGroup(name)}
}

func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(NewMaskingJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

func InitLogger(w io.Writer, level slog.Level) *slog.Logger {
	logger := NewLogger(w, level)
	slog.SetDefault(logger)
	return logger
}

func ParseLogLevel(s string) slog.Level {
	switch s {
	case "debug", "DEBUG":
		return slog.LevelDebug
	case "warn", "WARN", "warning", "WARNING":
		return slog.LevelWarn
	case "error", "ERROR":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func LogEvent(ctx context.Context, level slog.Level, event string, attrs ...any) {
	all := make([]any, 0, len(attrs)+2)
	all = append(all, slog.String("event", event))
	all = append(all, attrs...)
	slog.Default().Log(ctx, level, event, all...)
}
