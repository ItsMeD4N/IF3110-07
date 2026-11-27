package migration

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/ItsMeD4N/IF3110-07/server/internal/queue"
)

type ApprovalConsumer interface {
	ConsumeApproval(ctx context.Context, databaseID, migrationID int64, action Action, idempotencyKey string) error
	ReleaseApproval(ctx context.Context, idempotencyKey string)
	AttachApprovalJob(ctx context.Context, idempotencyKey, jobID string)
}

type approvalKeyCtx struct{}
func WithApprovalKey(ctx context.Context, key string) context.Context {
	if key == "" {
		return ctx
	}
	return context.WithValue(ctx, approvalKeyCtx{}, key)
}

func ApprovalKeyFrom(ctx context.Context) string {
	v, _ := ctx.Value(approvalKeyCtx{}).(string)
	return v
}

type approvalClaim struct {
	consumer ApprovalConsumer
	key      string
	claimed  bool
}

func (e *Engine) claimApproval(ctx context.Context, def Definition, action Action, opts *EnqueueOptions) (context.Context, *approvalClaim, error) {
	if opts.IdempotencyKey == "" {
		opts.IdempotencyKey = uuid.NewString()
	}
	ctx = WithApprovalKey(ctx, opts.IdempotencyKey)
	if err := e.checkApproval(ctx, def, action); err != nil {
		return ctx, nil, err
	}
	claim := &approvalClaim{key: opts.IdempotencyKey}
	consumer, ok := e.ApprovalChecker.(ApprovalConsumer)
	if !ok {
		return ctx, claim, nil
	}
	if err := consumer.ConsumeApproval(ctx, def.DatabaseID, def.ID, action, opts.IdempotencyKey); err != nil {
		return ctx, nil, err
	}
	claim.consumer = consumer
	claim.claimed = true
	return ctx, claim, nil
}

func (c *approvalClaim) settle(ctx context.Context, job queue.Job, err error) {
	if c == nil || !c.claimed {
		return
	}
	if err != nil {
		c.consumer.ReleaseApproval(context.WithoutCancel(ctx), c.key)
		return
	}
	if job.ID != "" {
		c.consumer.AttachApprovalJob(context.WithoutCancel(ctx), c.key, job.ID)
	}
}

func IsApprovalRequired(err error) bool { return errors.Is(err, ErrApprovalRequired) }
