package auth

import "context"

type contextKey string

const userContextKey contextKey = "current_user"

// CurrentUser mengambil user aktif dari context request.
// Kontrak utama yang digunakan oleh Orang B (authz) dan Orang C (catatan audit migrasi).
func CurrentUser(ctx context.Context) *User {
	if u, ok := ctx.Value(userContextKey).(*User); ok {
		return u
	}
	return nil
}

// WithUser memasukkan data user ke dalam context request.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userContextKey, u)
}
