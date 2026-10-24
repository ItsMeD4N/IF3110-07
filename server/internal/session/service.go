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

// SetNowFunc memungkinkan penyesuaian fungsi waktu (berguna untuk testing skenario idle/rotasi)
func (m *Manager) SetNowFunc(f func() time.Time) {
	m.nowFunc = f
}

// CreateSession membuat sesi baru di backend dan mengembalikan pointer Session serta plain token untuk client
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

// SetCookie memasang cookie app_session_id ke response writer
func (m *Manager) SetCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   m.isProd,
		MaxAge:   int(AbsoluteExpiration.Seconds()),
	})
}

// ClearCookie membersihkan cookie app_session_id dari browser
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

// ExtractToken mengambil opaque token dari header cookie request
func (m *Manager) ExtractToken(r *http.Request) (string, error) {
	cookie, err := r.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		return "", ErrMissingSessionCookie
	}
	return cookie.Value, nil
}

// ValidateSession memvalidasi token sesi terhadap database sesuai diagram alir 4.3:
// 1. Cek keberadaan sesi & status revoked
// 2. Cek absolute expiration -> jika hangus, revoke & tolak
// 3. Cek idle timeout -> jika timeout, revoke & tolak
// 4. Cek apakah token adalah token lama dalam masa rotasi:
//    - Jika sekarang > grace_period_until -> tolak
//    - Jika sekarang <= grace_period_until -> izinkan tanpa update idle
// 5. Cek rotasi berkala (interval 15 menit):
//    - Jika umur token >= 15 menit -> rotasi token baru, set grace period 60 detik pada token lama, pasang cookie baru
//    - Jika belum perlu rotasi -> perbarui idle_expires_at = now + 30 menit
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

	// 1. Cek Absolute Expiration
	if now.After(sess.AbsoluteExpiresAt) {
		_ = m.repo.RevokeSession(ctx, sess.TokenHash)
		return nil, ErrAbsoluteExpired
	}

	// 2. Cek Idle Timeout
	if now.After(sess.IdleExpiresAt) {
		_ = m.repo.RevokeSession(ctx, sess.TokenHash)
		return nil, ErrIdleTimeout
	}

	// 3. Cek apakah request menggunakan token lama dalam masa rotasi
	if sess.RotatedFromTokenHash != nil && *sess.RotatedFromTokenHash == tokenHash {
		if sess.GracePeriodUntil != nil && now.After(*sess.GracePeriodUntil) {
			return nil, ErrGracePeriodExpired
		}
		// Masih dalam batas grace period: izinkan tanpa update idle
		return sess, nil
	}

	// 4. Token adalah token aktif utama. Cek apakah perlu rotasi berkala.
	tokenAge := now.Sub(sess.UpdatedAt)
	if tokenAge >= RotationInterval || sess.RotationRequired {
		newToken, err := GenerateOpaqueToken()
		if err != nil {
			return nil, fmt.Errorf("gagal generate token rotasi: %w", err)
		}
		newTokenHash := HashToken(newToken)
		graceUntil := now.Add(GracePeriod)
		newIdleExpiry := now.Add(IdleTimeout)

		// Rotasi di DB: absolute expiration tetap dipertahankan
		err = m.repo.RotateSession(ctx, sess.ID, newTokenHash, tokenHash, graceUntil, newIdleExpiry, now)
		if err != nil {
			return nil, fmt.Errorf("gagal mengeksekusi rotasi sesi di DB: %w", err)
		}

		if w != nil {
			m.SetCookie(w, newToken)
		}

		// Update field struct in-memory
		sess.TokenHash = newTokenHash
		sess.RotatedFromTokenHash = &tokenHash
		sess.GracePeriodUntil = &graceUntil
		sess.IdleExpiresAt = newIdleExpiry
		sess.UpdatedAt = now
		sess.RotationRequired = false

		return sess, nil
	}

	// Tidak perlu rotasi: perbarui idle timeout
	newIdleExpiry := now.Add(IdleTimeout)
	if err := m.repo.UpdateIdleExpiry(ctx, sess.ID, newIdleExpiry); err != nil {
		return nil, fmt.Errorf("gagal memperbarui idle timeout di DB: %w", err)
	}
	sess.IdleExpiresAt = newIdleExpiry

	return sess, nil
}

// ForceRotate memaksa rotasi token baru (misalnya saat terjadi eskalasi privilege pengguna)
func (m *Manager) ForceRotate(ctx context.Context, sess *Session, w http.ResponseWriter) error {
	newToken, err := GenerateOpaqueToken()
	if err != nil {
		return fmt.Errorf("gagal generate token rotasi paksa: %w", err)
	}

	newTokenHash := HashToken(newToken)
	now := m.nowFunc()
	graceUntil := now.Add(GracePeriod)
	newIdleExpiry := now.Add(IdleTimeout)

	err = m.repo.RotateSession(ctx, sess.ID, newTokenHash, sess.TokenHash, graceUntil, newIdleExpiry, now)
	if err != nil {
		return fmt.Errorf("gagal rotasi paksa di DB: %w", err)
	}

	if w != nil {
		m.SetCookie(w, newToken)
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

// Revoke mencabut sesi di DB dan membersihkan cookie client jika w != nil
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

// RevokeAllForUser mencabut seluruh sesi milik pengguna tertentu di DB
func (m *Manager) RevokeAllForUser(ctx context.Context, userID string) error {
	return m.repo.RevokeAllUserSessions(ctx, userID)
}

func (m *Manager) RequireRotation(ctx context.Context, userIDs []string) error {
	return m.repo.MarkRotationRequired(ctx, userIDs)
}
