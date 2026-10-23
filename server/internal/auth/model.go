package auth

import "time"

// User merepresentasikan data akun pengguna di sistem
type User struct {
	ID              string    `json:"id"`
	Username        string    `json:"username"`
	PasswordHash    string    `json:"-"` // Tidak boleh diekspos ke JSON/response
	IsPlatformAdmin bool      `json:"is_platform_admin"`
	IsActive        bool      `json:"is_active"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
