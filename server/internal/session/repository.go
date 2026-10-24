package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type SessionRepository interface {
	CreateSession(ctx context.Context, s *Session) error
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error)
	UpdateIdleExpiry(ctx context.Context, id string, newIdleExpiry time.Time) error
	RotateSession(ctx context.Context, id string, newTokenHash string, oldTokenHash string, gracePeriodUntil time.Time, newIdleExpiry time.Time) error
	RevokeSession(ctx context.Context, tokenHash string) error
	RevokeAllUserSessions(ctx context.Context, userID string) error
}

type postgresSessionRepository struct {
	db *sql.DB
}

func NewSessionRepository(db *sql.DB) SessionRepository {
	return &postgresSessionRepository{db: db}
}

func (r *postgresSessionRepository) CreateSession(ctx context.Context, s *Session) error {
	query := `
		INSERT INTO sessions (
			token_hash, user_id, idle_expires_at, absolute_expires_at,
			rotated_from_token_hash, grace_period_until, is_revoked,
			ip_address, user_agent
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id, created_at, updated_at
	`
	err := r.db.QueryRowContext(
		ctx,
		query,
		s.TokenHash,
		s.UserID,
		s.IdleExpiresAt,
		s.AbsoluteExpiresAt,
		s.RotatedFromTokenHash,
		s.GracePeriodUntil,
		s.IsRevoked,
		s.IPAddress,
		s.UserAgent,
	).Scan(&s.ID, &s.CreatedAt, &s.UpdatedAt)

	if err != nil {
		return fmt.Errorf("gagal membuat sesi baru: %w", err)
	}

	return nil
}

func (r *postgresSessionRepository) GetSessionByTokenHash(ctx context.Context, tokenHash string) (*Session, error) {
	query := `
		SELECT id, token_hash, user_id, idle_expires_at, absolute_expires_at,
		       rotated_from_token_hash, grace_period_until, is_revoked,
		       COALESCE(ip_address, ''), COALESCE(user_agent, ''), created_at, updated_at
		FROM sessions
		WHERE token_hash = $1 OR rotated_from_token_hash = $1
	`
	row := r.db.QueryRowContext(ctx, query, tokenHash)

	var s Session
	var rotatedFrom sql.NullString
	var gracePeriod sql.NullTime

	err := row.Scan(
		&s.ID,
		&s.TokenHash,
		&s.UserID,
		&s.IdleExpiresAt,
		&s.AbsoluteExpiresAt,
		&rotatedFrom,
		&gracePeriod,
		&s.IsRevoked,
		&s.IPAddress,
		&s.UserAgent,
		&s.CreatedAt,
		&s.UpdatedAt,
	)

	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("gagal mencari sesi: %w", err)
	}

	if rotatedFrom.Valid {
		s.RotatedFromTokenHash = &rotatedFrom.String
	}
	if gracePeriod.Valid {
		s.GracePeriodUntil = &gracePeriod.Time
	}

	return &s, nil
}

func (r *postgresSessionRepository) UpdateIdleExpiry(ctx context.Context, id string, newIdleExpiry time.Time) error {
	query := `
		UPDATE sessions
		SET idle_expires_at = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND is_revoked = FALSE
	`
	_, err := r.db.ExecContext(ctx, query, id, newIdleExpiry)
	if err != nil {
		return fmt.Errorf("gagal update idle expiry: %w", err)
	}
	return nil
}

func (r *postgresSessionRepository) RotateSession(ctx context.Context, id string, newTokenHash string, oldTokenHash string, gracePeriodUntil time.Time, newIdleExpiry time.Time) error {
	query := `
		UPDATE sessions
		SET token_hash = $2,
		    rotated_from_token_hash = $3,
		    grace_period_until = $4,
		    idle_expires_at = $5,
		    updated_at = CURRENT_TIMESTAMP
		WHERE id = $1 AND is_revoked = FALSE
	`
	_, err := r.db.ExecContext(ctx, query, id, newTokenHash, oldTokenHash, gracePeriodUntil, newIdleExpiry)
	if err != nil {
		return fmt.Errorf("gagal rotasi sesi: %w", err)
	}
	return nil
}

func (r *postgresSessionRepository) RevokeSession(ctx context.Context, tokenHash string) error {
	query := `
		UPDATE sessions
		SET is_revoked = TRUE, updated_at = CURRENT_TIMESTAMP
		WHERE token_hash = $1 OR rotated_from_token_hash = $1
	`
	_, err := r.db.ExecContext(ctx, query, tokenHash)
	if err != nil {
		return fmt.Errorf("gagal mencabut sesi: %w", err)
	}
	return nil
}

func (r *postgresSessionRepository) RevokeAllUserSessions(ctx context.Context, userID string) error {
	query := `
		UPDATE sessions
		SET is_revoked = TRUE, updated_at = CURRENT_TIMESTAMP
		WHERE user_id = $1
	`
	_, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("gagal mencabut seluruh sesi pengguna: %w", err)
	}
	return nil
}
