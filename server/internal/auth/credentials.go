package auth

import (
	"context"
	"errors"
	"strings"
)

var (
	ErrInvalidCredentials = errors.New("username atau password salah")
	ErrInactiveUser       = errors.New("akun telah dinonaktifkan")
)

const dummyHash = "$2a$12$wsagDUCgkx0JPek99ZnJHeIOpMX4GAqa0r1I3XQydfnqLbMUalxvG"

func VerifyCredentials(ctx context.Context, repo UserRepository, username, password string) (*User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return nil, ErrInvalidCredentials
	}
	user, err := repo.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			CheckPassword(dummyHash, password)
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if !CheckPassword(user.PasswordHash, password) {
		return nil, ErrInvalidCredentials
	}
	if !user.IsActive {
		return nil, ErrInactiveUser
	}
	return user, nil
}
