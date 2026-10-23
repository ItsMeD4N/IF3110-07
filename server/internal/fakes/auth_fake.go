package fakes

import (
	"net/http"

	"github.com/ItsMeD4N/IF3110-07/server/internal/auth"
)

// FakeAuthMiddleware menginjeksi dummy user (Platform Admin atau User Biasa) ke request context.
// Berguna agar Orang B dan Orang C bisa mengetes fiturnya sebelum modul auth Orang A selesai.
func FakeAuthMiddleware(admin bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			dummy := &auth.User{
				ID:              "00000000-0000-0000-0000-000000000001",
				Username:        "fake_user",
				IsPlatformAdmin: admin,
				IsActive:        true,
			}
			ctx := auth.WithUser(r.Context(), dummy)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
