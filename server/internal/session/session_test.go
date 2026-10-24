package session

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func setupTestDB(t *testing.T) SessionRepository {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable"
	}

	db, err := testutil.InitDB(connStr)
	if err != nil {
		t.Fatalf("Gagal terhubung ke DB: %v", err)
	}

	return NewSessionRepository(db)
}

func TestSession_CreateAndCookie(t *testing.T) {
	repo := setupTestDB(t)
	mgr := NewManager(repo, false)
	ctx := context.Background()

	adminID := "a0000000-0000-0000-0000-000000000001"
	sess, token, err := mgr.CreateSession(ctx, adminID, "127.0.0.1", "Unit-Tester")
	if err != nil {
		t.Fatalf("Gagal membuat sesi: %v", err)
	}
	defer mgr.Revoke(ctx, token, nil)

	if sess.ID == "" || token == "" {
		t.Fatalf("Sesi ID atau token tidak boleh kosong")
	}

	// Test SetCookie
	rec := httptest.NewRecorder()
	mgr.SetCookie(rec, token)

	cookies := rec.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			sessionCookie = c
			break
		}
	}

	if sessionCookie == nil {
		t.Fatalf("Cookie %s harus terpasang di response header", SessionCookieName)
	}
	if !sessionCookie.HttpOnly {
		t.Errorf("Cookie harus memiliki HttpOnly = true")
	}
	if sessionCookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("Cookie harus memiliki SameSite = Lax")
	}
	if sessionCookie.Path != "/" {
		t.Errorf("Cookie harus memiliki Path = /")
	}
	if sessionCookie.Value != token {
		t.Errorf("Cookie value tidak cocok dengan plain token")
	}

	// Test ExtractToken
	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(sessionCookie)

	extracted, err := mgr.ExtractToken(req)
	if err != nil {
		t.Fatalf("Gagal extract token dari request: %v", err)
	}
	if extracted != token {
		t.Fatalf("Expected token %s, got %s", token, extracted)
	}

	// Test ClearCookie
	recClear := httptest.NewRecorder()
	mgr.ClearCookie(recClear)
	clearCookies := recClear.Result().Cookies()
	var clearedCookie *http.Cookie
	for _, c := range clearCookies {
		if c.Name == SessionCookieName {
			clearedCookie = c
			break
		}
	}
	if clearedCookie == nil || clearedCookie.MaxAge != -1 {
		t.Fatalf("ClearCookie harus memasang MaxAge = -1")
	}
}

func TestSession_IdleTimeout(t *testing.T) {
	repo := setupTestDB(t)
	mgr := NewManager(repo, false)
	ctx := context.Background()

	adminID := "a0000000-0000-0000-0000-000000000001"
	simulatedTime := time.Now()
	mgr.SetNowFunc(func() time.Time {
		return simulatedTime
	})

	sess, token, err := mgr.CreateSession(ctx, adminID, "127.0.0.1", "Idle-Tester")
	if err != nil {
		t.Fatalf("Gagal create session: %v", err)
	}
	defer mgr.Revoke(ctx, token, nil)

	// Verifikasi saat ini masih valid
	_, err = mgr.ValidateSession(ctx, token, nil)
	if err != nil {
		t.Fatalf("Sesi seharusnya valid pada waktu awal: %v", err)
	}

	// Majukan waktu 31 menit (melewati IdleTimeout 30 menit)
	simulatedTime = simulatedTime.Add(31 * time.Minute)

	// Validasi sesi harus ditolak karena idle timeout
	_, err = mgr.ValidateSession(ctx, token, nil)
	if err != ErrIdleTimeout {
		t.Fatalf("Expected ErrIdleTimeout, got %v", err)
	}

	// Pastikan sesi otomatis di-revoke di DB
	fetched, _ := repo.GetSessionByTokenHash(ctx, sess.TokenHash)
	if !fetched.IsRevoked {
		t.Fatalf("Sesi harus ditandai is_revoked = true setelah idle timeout")
	}
}

func TestSession_AbsoluteExpiration(t *testing.T) {
	repo := setupTestDB(t)
	mgr := NewManager(repo, false)
	ctx := context.Background()

	adminID := "a0000000-0000-0000-0000-000000000001"
	simulatedTime := time.Now()
	mgr.SetNowFunc(func() time.Time {
		return simulatedTime
	})

	_, token, err := mgr.CreateSession(ctx, adminID, "127.0.0.1", "Absolute-Tester")
	if err != nil {
		t.Fatalf("Gagal create session: %v", err)
	}
	defer mgr.Revoke(ctx, token, nil)

	// Simulasi user aktif terus menerus selama 12 jam (setiap 20 menit kirim request)
	// Namun total waktu telah melewati AbsoluteExpiration (12 jam + 1 menit)
	simulatedTime = simulatedTime.Add(12*time.Hour + 1*time.Minute)

	// Meskipun terus aktif, sesi harus hangus mutlak
	_, err = mgr.ValidateSession(ctx, token, nil)
	if err != ErrAbsoluteExpired {
		t.Fatalf("Expected ErrAbsoluteExpired setelah 12 jam, got %v", err)
	}
}

func TestSession_RotationAndGracePeriod(t *testing.T) {
	repo := setupTestDB(t)
	mgr := NewManager(repo, false)
	ctx := context.Background()

	adminID := "a0000000-0000-0000-0000-000000000001"
	simulatedTime := time.Now()
	mgr.SetNowFunc(func() time.Time {
		return simulatedTime
	})

	sessInitial, tokenOld, err := mgr.CreateSession(ctx, adminID, "127.0.0.1", "Rotation-Tester")
	if err != nil {
		t.Fatalf("Gagal create session: %v", err)
	}
	initialAbsoluteExpiry := sessInitial.AbsoluteExpiresAt

	// 1. Majukan waktu 16 menit (melewati RotationInterval 15 menit, tetapi belum idle 30 menit)
	simulatedTime = simulatedTime.Add(16 * time.Minute)

	rec := httptest.NewRecorder()
	sessRotated, err := mgr.ValidateSession(ctx, tokenOld, rec)
	if err != nil {
		t.Fatalf("ValidateSession gagal pada saat rotasi: %v", err)
	}

	// Pastikan absolute expiration TIDAK berubah sama sekali
	diff := sessRotated.AbsoluteExpiresAt.Sub(initialAbsoluteExpiry)
	if diff < -time.Second || diff > time.Second {
		t.Fatalf("Aturan Rotasi: Absolute expiration tidak boleh berubah! Awal: %v, Setelah rotasi: %v",
			initialAbsoluteExpiry, sessRotated.AbsoluteExpiresAt)
	}

	// Ambil token baru dari response cookie
	cookies := rec.Result().Cookies()
	var tokenNew string
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			tokenNew = c.Value
			break
		}
	}
	if tokenNew == "" || tokenNew == tokenOld {
		t.Fatalf("Rotasi harus menghasilkan token baru yang berbeda dari token lama")
	}
	defer mgr.Revoke(ctx, tokenNew, nil)

	// 2. Uji Grace Period: Kirim request menggunakan token lama 30 detik kemudian (≤ 60 detik)
	simulatedTime = simulatedTime.Add(30 * time.Second)
	sessGrace, err := mgr.ValidateSession(ctx, tokenOld, nil)
	if err != nil {
		t.Fatalf("Token lama harus masih valid selama masa grace period: %v", err)
	}
	if sessGrace.ID != sessRotated.ID {
		t.Fatalf("Sesi grace period harus merujuk ke record sesi yang sama")
	}

	// 3. Uji Token Baru: Token baru juga harus valid
	_, err = mgr.ValidateSession(ctx, tokenNew, nil)
	if err != nil {
		t.Fatalf("Token baru harus valid: %v", err)
	}

	// 4. Uji Grace Period Expired: Majukan waktu lagi 40 detik (total 70 detik sejak rotasi > 60 detik)
	simulatedTime = simulatedTime.Add(40 * time.Second)
	_, err = mgr.ValidateSession(ctx, tokenOld, nil)
	if err != ErrGracePeriodExpired {
		t.Fatalf("Expected ErrGracePeriodExpired ketika grace period habis, got %v", err)
	}

	// Token baru tetap valid
	_, err = mgr.ValidateSession(ctx, tokenNew, nil)
	if err != nil {
		t.Fatalf("Token baru harus tetap valid setelah grace period token lama habis: %v", err)
	}
}

func TestSession_RevocationAndLogout(t *testing.T) {
	repo := setupTestDB(t)
	mgr := NewManager(repo, false)
	ctx := context.Background()

	adminID := "a0000000-0000-0000-0000-000000000001"
	_, token, err := mgr.CreateSession(ctx, adminID, "127.0.0.1", "Revoke-Tester")
	if err != nil {
		t.Fatalf("Gagal create session: %v", err)
	}

	rec := httptest.NewRecorder()
	err = mgr.Revoke(ctx, token, rec)
	if err != nil {
		t.Fatalf("Gagal mencabut sesi: %v", err)
	}

	// Pastikan cookie dibersihkan
	cookies := rec.Result().Cookies()
	var cleared *http.Cookie
	for _, c := range cookies {
		if c.Name == SessionCookieName {
			cleared = c
			break
		}
	}
	if cleared == nil || cleared.MaxAge != -1 {
		t.Fatalf("Revoke harus membersihkan cookie dengan MaxAge = -1")
	}

	// Request berikutnya dengan token yang dicabut harus ditolak
	_, err = mgr.ValidateSession(ctx, token, nil)
	if err != ErrSessionRevoked {
		t.Fatalf("Expected ErrSessionRevoked setelah logout, got %v", err)
	}
}
