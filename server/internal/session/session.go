package session

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"
)

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
	RotationRequired     bool
	IPAddress            string
	UserAgent            string
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

func GenerateOpaqueToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}
