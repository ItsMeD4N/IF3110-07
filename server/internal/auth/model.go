package auth

import "time"

type User struct {
	ID              string    `json:"id"`
	Username        string    `json:"username"`
	PasswordHash    string    `json:"-"`
	IsPlatformAdmin bool      `json:"is_platform_admin"`
	IsActive        bool      `json:"is_active"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}
