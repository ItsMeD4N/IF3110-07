package telemetry

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/lib/pq"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type RowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func ExtractSQLState(err error) string {
	if err == nil {
		return ""
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return string(pqErr.Code)
	}
	return ""
}

func sqlSpanAttrs(ctx context.Context, query string, meta ExecMeta) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String("db.system", "postgresql"),
		attribute.String("db.statement", MaskSQL(query)),
	}
	if dbID := DatabaseIDFrom(ctx); dbID > 0 {
		attrs = append(attrs, attribute.Int64("db.database_id", dbID))
	}
	if actor := ActorFrom(ctx); actor != "" {
		attrs = append(attrs, attribute.String("actor", actor))
	}
	if corrID := CorrelationIDFrom(ctx); corrID != "" {
		attrs = append(attrs, attribute.String("correlation_id", corrID))
	}
	if meta.MigrationID > 0 {
		attrs = append(attrs, attribute.Int64("migration.id", meta.MigrationID))
	}
	if meta.ExecutionID > 0 {
		attrs = append(attrs, attribute.Int64("execution.id", meta.ExecutionID))
	}
	if meta.JobID != "" {
		attrs = append(attrs, attribute.String("job.id", meta.JobID))
	}
	if meta.Attempt > 0 {
		attrs = append(attrs, attribute.Int("migration.attempt", meta.Attempt))
	}
	if meta.BatchNumber > 0 {
		attrs = append(attrs, attribute.Int("migration.batch_number", meta.BatchNumber))
	}
	if meta.Action != "" {
		attrs = append(attrs, attribute.String("migration.action", meta.Action))
	}
	if meta.ReadOnly {
		attrs = append(attrs, attribute.Bool("db.read_only", true))
	}
	return attrs
}

func sqlLogAttrs(meta ExecMeta, query string, args []any) []any {
	out := make([]any, 0, 12)
	if query != "" {
		out = append(out, slog.String("query", MaskSQL(query)))
	}
	if len(args) > 0 {
		out = append(out, slog.Any("params", MaskParams(args)))
	}
	if meta.MigrationID > 0 {
		out = append(out, slog.Int64("migration_id", meta.MigrationID))
	}
	if meta.ExecutionID > 0 {
		out = append(out, slog.Int64("execution_id", meta.ExecutionID))
	}
	if meta.JobID != "" {
		out = append(out, slog.String("job_id", meta.JobID))
	}
	if meta.Attempt > 0 {
		out = append(out, slog.Int("attempt", meta.Attempt))
	}
	if meta.BatchNumber > 0 {
		out = append(out, slog.Int("batch_number", meta.BatchNumber))
	}
	if meta.Action != "" {
		out = append(out, slog.String("action", meta.Action))
	}
	if meta.ReadOnly {
		out = append(out, slog.Bool("read_only", true))
	}
	return out
}

func ExecSQL(ctx context.Context, db Execer, query string, args ...any) (sql.Result, error) {
	meta := ExecMetaFrom(ctx)
	ctx, span := Tracer().Start(ctx, "db.exec",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(sqlSpanAttrs(ctx, query, meta)...),
	)
	defer span.End()

	startAttrs := append(sqlLogAttrs(meta, query, args), slog.String("status", "started"))
	LogEvent(ctx, slog.LevelInfo, "sql.start", startAttrs...)

	start := time.Now()
	res, err := db.ExecContext(ctx, query, args...)
	dur := time.Since(start)
	durMS := float64(dur.Microseconds()) / 1000.0

	resAttrs := append(sqlLogAttrs(meta, query, args), slog.Float64("duration_ms", durMS))
	span.SetAttributes(attribute.Float64("db.duration_ms", durMS))

	if err != nil {
		sqlState := ExtractSQLState(err)
		span.RecordError(err)
		span.SetStatus(codes.Error, MaskString(err.Error()))
		if sqlState != "" {
			span.SetAttributes(attribute.String("db.sqlstate", sqlState))
			resAttrs = append(resAttrs, slog.String("sqlstate", sqlState))
		}
		resAttrs = append(resAttrs, slog.String("status", "failed"), slog.String("error", MaskString(err.Error())))
		LogEvent(ctx, slog.LevelError, "sql.result", resAttrs...)
		return res, err
	}

	if res != nil {
		if rows, rErr := res.RowsAffected(); rErr == nil {
			span.SetAttributes(attribute.Int64("db.rows_affected", rows))
			resAttrs = append(resAttrs, slog.Int64("rows_affected", rows))
		}
	}
	resAttrs = append(resAttrs, slog.String("status", "success"))
	LogEvent(ctx, slog.LevelInfo, "sql.result", resAttrs...)
	return res, nil
}

func QuerySQL(ctx context.Context, db Querier, query string, args ...any) (*sql.Rows, error) {
	meta := ExecMetaFrom(ctx)
	ctx, span := Tracer().Start(ctx, "db.query",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(sqlSpanAttrs(ctx, query, meta)...),
	)
	defer span.End()

	startAttrs := append(sqlLogAttrs(meta, query, args), slog.String("status", "started"))
	LogEvent(ctx, slog.LevelInfo, "sql.start", startAttrs...)

	start := time.Now()
	rows, err := db.QueryContext(ctx, query, args...)
	dur := time.Since(start)
	durMS := float64(dur.Microseconds()) / 1000.0

	resAttrs := append(sqlLogAttrs(meta, query, args), slog.Float64("duration_ms", durMS))
	span.SetAttributes(attribute.Float64("db.duration_ms", durMS))

	if err != nil {
		sqlState := ExtractSQLState(err)
		span.RecordError(err)
		span.SetStatus(codes.Error, MaskString(err.Error()))
		if sqlState != "" {
			span.SetAttributes(attribute.String("db.sqlstate", sqlState))
			resAttrs = append(resAttrs, slog.String("sqlstate", sqlState))
		}
		resAttrs = append(resAttrs, slog.String("status", "failed"), slog.String("error", MaskString(err.Error())))
		LogEvent(ctx, slog.LevelError, "sql.result", resAttrs...)
		return nil, err
	}

	resAttrs = append(resAttrs, slog.String("status", "success"))
	LogEvent(ctx, slog.LevelInfo, "sql.result", resAttrs...)
	return rows, nil
}

func QueryRowScan(ctx context.Context, db RowQuerier, query string, args []any, dest ...any) error {
	meta := ExecMetaFrom(ctx)
	ctx, span := Tracer().Start(ctx, "db.query_row",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(sqlSpanAttrs(ctx, query, meta)...),
	)
	defer span.End()

	startAttrs := append(sqlLogAttrs(meta, query, args), slog.String("status", "started"))
	LogEvent(ctx, slog.LevelInfo, "sql.start", startAttrs...)

	start := time.Now()
	err := db.QueryRowContext(ctx, query, args...).Scan(dest...)
	dur := time.Since(start)
	durMS := float64(dur.Microseconds()) / 1000.0

	resAttrs := append(sqlLogAttrs(meta, query, args), slog.Float64("duration_ms", durMS))
	span.SetAttributes(attribute.Float64("db.duration_ms", durMS))

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			resAttrs = append(resAttrs, slog.String("status", "no_rows"), slog.Int64("rows_affected", 0))
			LogEvent(ctx, slog.LevelInfo, "sql.result", resAttrs...)
			return err
		}
		sqlState := ExtractSQLState(err)
		span.RecordError(err)
		span.SetStatus(codes.Error, MaskString(err.Error()))
		if sqlState != "" {
			span.SetAttributes(attribute.String("db.sqlstate", sqlState))
			resAttrs = append(resAttrs, slog.String("sqlstate", sqlState))
		}
		resAttrs = append(resAttrs, slog.String("status", "failed"), slog.String("error", MaskString(err.Error())))
		LogEvent(ctx, slog.LevelError, "sql.result", resAttrs...)
		return err
	}

	resAttrs = append(resAttrs, slog.String("status", "success"), slog.Int64("rows_affected", 1))
	LogEvent(ctx, slog.LevelInfo, "sql.result", resAttrs...)
	return nil
}

func CommitTx(ctx context.Context, tx *sql.Tx) error {
	meta := ExecMetaFrom(ctx)
	ctx, span := Tracer().Start(ctx, "db.commit",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(sqlSpanAttrs(ctx, "COMMIT", meta)...),
	)
	defer span.End()

	start := time.Now()
	err := tx.Commit()
	durMS := float64(time.Since(start).Microseconds()) / 1000.0

	attrs := append(sqlLogAttrs(meta, "COMMIT", nil), slog.Float64("duration_ms", durMS))
	span.SetAttributes(attribute.Float64("db.duration_ms", durMS))

	if err != nil {
		sqlState := ExtractSQLState(err)
		span.RecordError(err)
		span.SetStatus(codes.Error, MaskString(err.Error()))
		if sqlState != "" {
			span.SetAttributes(attribute.String("db.sqlstate", sqlState))
			attrs = append(attrs, slog.String("sqlstate", sqlState))
		}
		attrs = append(attrs, slog.String("status", "failed"), slog.String("error", MaskString(err.Error())))
		LogEvent(ctx, slog.LevelError, "sql.commit", attrs...)
		return err
	}

	attrs = append(attrs, slog.String("status", "success"))
	LogEvent(ctx, slog.LevelInfo, "sql.commit", attrs...)
	return nil
}

func RollbackTx(ctx context.Context, tx *sql.Tx, reason string) error {
	meta := ExecMetaFrom(ctx)
	ctx, span := Tracer().Start(ctx, "db.rollback",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(sqlSpanAttrs(ctx, "ROLLBACK", meta)...),
	)
	defer span.End()

	start := time.Now()
	err := tx.Rollback()
	durMS := float64(time.Since(start).Microseconds()) / 1000.0

	attrs := append(sqlLogAttrs(meta, "ROLLBACK", nil), slog.Float64("duration_ms", durMS))
	if reason != "" {
		attrs = append(attrs, slog.String("reason", MaskString(reason)))
	}
	span.SetAttributes(attribute.Float64("db.duration_ms", durMS))

	if err != nil && !errors.Is(err, sql.ErrTxDone) {
		sqlState := ExtractSQLState(err)
		span.RecordError(err)
		span.SetStatus(codes.Error, MaskString(err.Error()))
		if sqlState != "" {
			span.SetAttributes(attribute.String("db.sqlstate", sqlState))
			attrs = append(attrs, slog.String("sqlstate", sqlState))
		}
		attrs = append(attrs, slog.String("status", "failed"), slog.String("error", MaskString(err.Error())))
		LogEvent(ctx, slog.LevelError, "sql.rollback", attrs...)
		return err
	}

	attrs = append(attrs, slog.String("status", "success"))
	LogEvent(ctx, slog.LevelInfo, "sql.rollback", attrs...)
	return nil
}
