package session

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

var (
	ErrIdleTimeout          = errors.New("sesi telah berakhir karena tidak aktif (idle timeout)")
	ErrAbsoluteExpired      = errors.New("sesi telah melewati batas maksimum kedaluwarsa (absolute expiration)")
	ErrGracePeriodExpired   = errors.New("token rotasi lama telah melewati masa toleransi grace period")
	ErrMissingSessionCookie = errors.New("cookie sesi tidak ditemukan")
)

type Manager struct {
	repo    SessionRepository
	isProd  bool
	nowFunc func() time.Time
}

func NewManager(repo SessionRepository, isProd bool) *Manager {
	return &Manager{
		repo:    repo,
		isProd:  isProd,
		nowFunc: time.Now,
	}
}

func (m *Manager) SetNowFunc(f func() time.Time) {
	m.nowFunc = f
}

func (m *Manager) CreateSession(ctx context.Context, userID, ipAddress, userAgent string) (*Session, string, error) {
	token, err := GenerateOpaqueToken()
	if err != nil {
		return nil, "", fmt.Errorf("gagal generate opaque token: %w", err)
	}

	tokenHash := HashToken(token)
	now := m.nowFunc()

	sess := &Session{
		TokenHash:         tokenHash,
		UserID:            userID,
		IdleExpiresAt:     now.Add(IdleTimeout),
		AbsoluteExpiresAt: now.Add(AbsoluteExpiration),
		IsRevoked:         false,
		IPAddress:         ipAddress,
		UserAgent:         userAgent,
	}

	if err := m.repo.CreateSession(ctx, sess); err != nil {
		return nil, "", err
	}

	return sess, token, nil
}

func (m *Manager) SetCookie(w http.ResponseWriter, token string, expiresAt time.Time) {
	maxAge := int(expiresAt.Sub(m.nowFunc()).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   m.isProd,
		MaxAge:   maxAge,
		Expires:  expiresAt,
	})
}

func (m *Manager) ClearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   m.isProd,
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
	})
}

func (m *Manager) ExtractToken(r *http.Request) (string, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", ErrMissingSessionCookie
	}
	return cookie.Value, nil
}

func (m *Manager) ValidateSession(ctx context.Context, token string, w http.ResponseWriter) (*Session, error) {
	tokenHash := HashToken(token)
	sess, err := m.repo.GetSessionByTokenHash(ctx, tokenHash)
	if err != nil {
		return nil, err
	}

	if sess.IsRevoked {
		return nil, ErrSessionRevoked
	}

	now := m.nowFunc()

	if now.After(sess.AbsoluteExpiresAt) {
		_ = m.repo.RevokeSession(ctx, sess.TokenHash)
		return nil, ErrAbsoluteExpired
	}

	if now.After(sess.IdleExpiresAt) {
		_ = m.repo.RevokeSession(ctx, sess.TokenHash)
		return nil, ErrIdleTimeout
	}

	if sess.RotatedFromTokenHash != nil && *sess.RotatedFromTokenHash == tokenHash {
		if sess.GracePeriodUntil != nil && now.After(*sess.GracePeriodUntil) {
			return nil, ErrGracePeriodExpired
		}

		return sess, nil
	}

	tokenAge := now.Sub(sess.UpdatedAt)
	if tokenAge >= RotationInterval || sess.RotationRequired {
		newToken, err := GenerateOpaqueToken()
		if err != nil {
			return nil, fmt.Errorf("gagal generate token rotasi: %w", err)
		}
		newTokenHash := HashToken(newToken)
		graceUntil := now.Add(GracePeriod)
		newIdleExpiry := now.Add(IdleTimeout)

		rotated, err := m.repo.RotateSession(ctx, sess.ID, newTokenHash, tokenHash, graceUntil, newIdleExpiry, now)
		if err != nil {
			return nil, fmt.Errorf("gagal mengeksekusi rotasi sesi di DB: %w", err)
		}
		if !rotated {

			return sess, nil
		}

		if w != nil {
			m.SetCookie(w, newToken, sess.AbsoluteExpiresAt)
		}

		sess.TokenHash = newTokenHash
		sess.RotatedFromTokenHash = &tokenHash
		sess.GracePeriodUntil = &graceUntil
		sess.IdleExpiresAt = newIdleExpiry
		sess.UpdatedAt = now
		sess.RotationRequired = false

		return sess, nil
	}

	newIdleExpiry := now.Add(IdleTimeout)
	if err := m.repo.UpdateIdleExpiry(ctx, sess.ID, newIdleExpiry); err != nil {
		return nil, fmt.Errorf("gagal memperbarui idle timeout di DB: %w", err)
	}
	sess.IdleExpiresAt = newIdleExpiry

	return sess, nil
}

func (m *Manager) ForceRotate(ctx context.Context, sess *Session, w http.ResponseWriter) error {
	newToken, err := GenerateOpaqueToken()
	if err != nil {
		return fmt.Errorf("gagal generate token rotasi paksa: %w", err)
	}

	newTokenHash := HashToken(newToken)
	now := m.nowFunc()
	graceUntil := now.Add(GracePeriod)
	newIdleExpiry := now.Add(IdleTimeout)

	rotated, err := m.repo.RotateSession(ctx, sess.ID, newTokenHash, sess.TokenHash, graceUntil, newIdleExpiry, now)
	if err != nil {
		return fmt.Errorf("gagal rotasi paksa di DB: %w", err)
	}
	if !rotated {
		return nil
	}

	if w != nil {
		m.SetCookie(w, newToken, sess.AbsoluteExpiresAt)
	}

	oldTokenHash := sess.TokenHash
	sess.RotatedFromTokenHash = &oldTokenHash
	sess.TokenHash = newTokenHash
	sess.RotationRequired = false
	sess.GracePeriodUntil = &graceUntil
	sess.IdleExpiresAt = newIdleExpiry
	sess.UpdatedAt = now

	return nil
}

func (m *Manager) Revoke(ctx context.Context, token string, w http.ResponseWriter) error {
	tokenHash := HashToken(token)
	if err := m.repo.RevokeSession(ctx, tokenHash); err != nil {
		return err
	}
	if w != nil {
		m.ClearCookie(w)
	}
	return nil
}

func (m *Manager) RevokeAllForUser(ctx context.Context, userID string) error {
	return m.repo.RevokeAllUserSessions(ctx, userID)
}

func (m *Manager) RequireRotation(ctx context.Context, userIDs []string) error {
	return m.repo.MarkRotationRequired(ctx, userIDs)
}
