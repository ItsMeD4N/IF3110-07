package telemetry

import (
	"context"

	"github.com/google/uuid"
)

type ctxKey int

const (
	keyCorrelationID ctxKey = iota
	keyActor
	keyActorSlot
	keyDatabaseID
	keyExecMeta
)

type actorSlot struct {
	actor string
}

func WithActorSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyActorSlot, &actorSlot{})
}

func SetRequestActor(ctx context.Context, actor string) {
	if ctx == nil || actor == "" {
		return
	}
	if slot, ok := ctx.Value(keyActorSlot).(*actorSlot); ok && slot != nil {
		slot.actor = actor
	}
}

type ExecMeta struct {
	MigrationID int64
	JobID       string
	ExecutionID int64
	Attempt     int
	BatchNumber int
	Action      string
	ReadOnly    bool
}

func NewCorrelationID() string {
	return uuid.NewString()
}

func WithCorrelationID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, keyCorrelationID, id)
}

func CorrelationIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(keyCorrelationID).(string); ok {
		return v
	}
	return ""
}

func WithActor(ctx context.Context, actor string) context.Context {
	if actor == "" {
		return ctx
	}
	return context.WithValue(ctx, keyActor, actor)
}

func ActorFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(keyActor).(string); ok && v != "" {
		return v
	}
	if slot, ok := ctx.Value(keyActorSlot).(*actorSlot); ok && slot != nil && slot.actor != "" {
		return slot.actor
	}
	return ""
}

func WithDatabaseID(ctx context.Context, dbID int64) context.Context {
	if dbID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, keyDatabaseID, dbID)
}

func DatabaseIDFrom(ctx context.Context) int64 {
	if ctx == nil {
		return 0
	}
	if v, ok := ctx.Value(keyDatabaseID).(int64); ok {
		return v
	}
	return 0
}

func WithExecMeta(ctx context.Context, meta ExecMeta) context.Context {
	return context.WithValue(ctx, keyExecMeta, meta)
}

func ExecMetaFrom(ctx context.Context) ExecMeta {
	if ctx == nil {
		return ExecMeta{}
	}
	if v, ok := ctx.Value(keyExecMeta).(ExecMeta); ok {
		return v
	}
	return ExecMeta{}
}
