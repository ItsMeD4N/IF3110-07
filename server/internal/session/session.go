package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

// Parameter sesi sesuai kesepakatan desain arsitektur keamanan
const (
	IdleTimeout        = 30 * time.Minute
	AbsoluteExpiration = 12 * time.Hour
	RotationInterval   = 15 * time.Minute
	GracePeriod        = 60 * time.Second
	SessionCookieName  = "app_session_id"
)

var (
	ErrSessionNotFound = errors.New("sesi tidak ditemukan")
	ErrSessionExpired  = errors.New("sesi telah kadaluarsa")
	ErrSessionRevoked  = errors.New("sesi telah dicabut")
)

type Session struct {
	ID                   string
	TokenHash            string
	UserID               string
	IdleExpiresAt        time.Time
	AbsoluteExpiresAt    time.Time
	RotatedFromTokenHash *string
	GracePeriodUntil     *time.Time
	IsRevoked            bool
	IPAddress            string
	UserAgent            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// GenerateOpaqueToken membuat token acak 32 bytes (256-bit entropy) dengan CSPRNG
func GenerateOpaqueToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// HashToken menghasilkan digest SHA-256 hex string dari opaque token untuk disimpan di database
func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
