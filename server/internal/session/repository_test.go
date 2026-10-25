package session

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func TestSessionRepository_Lifecycle(t *testing.T) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable"
	}

	db, err := testutil.InitDB(connStr)
	if err != nil {
		t.Fatalf("Gagal terhubung ke DB: %v", err)
	}
	defer db.Close()

	repo := NewSessionRepository(db)
	ctx := context.Background()

	adminUserID := "a0000000-0000-0000-0000-000000000001"

	token1, err := GenerateOpaqueToken()
	if err != nil {
		t.Fatalf("Gagal generate token: %v", err)
	}
	tokenHash1 := HashToken(token1)

	now := time.Now()
	newSession := &Session{
		TokenHash:         tokenHash1,
		UserID:            adminUserID,
		IdleExpiresAt:     now.Add(IdleTimeout),
		AbsoluteExpiresAt: now.Add(AbsoluteExpiration),
		IsRevoked:         false,
		IPAddress:         "127.0.0.1",
		UserAgent:         "Go-Test-Agent",
	}

	err = repo.CreateSession(ctx, newSession)
	if err != nil {
		t.Fatalf("Gagal simpan sesi: %v", err)
	}
	defer repo.RevokeSession(ctx, tokenHash1)

	if newSession.ID == "" {
		t.Fatalf("Session ID harus diisi setelah insert")
	}

	fetched, err := repo.GetSessionByTokenHash(ctx, tokenHash1)
	if err != nil {
		t.Fatalf("Gagal mengambil sesi: %v", err)
	}
	if fetched.UserID != adminUserID {
		t.Fatalf("Expected UserID %s, got %s", adminUserID, fetched.UserID)
	}

	updatedIdle := now.Add(45 * time.Minute)
	err = repo.UpdateIdleExpiry(ctx, fetched.ID, updatedIdle)
	if err != nil {
		t.Fatalf("Gagal update idle expiry: %v", err)
	}

	token2, _ := GenerateOpaqueToken()
	tokenHash2 := HashToken(token2)
	graceUntil := time.Now().Add(GracePeriod)
	newIdle := time.Now().Add(IdleTimeout)

	rotated, err := repo.RotateSession(ctx, fetched.ID, tokenHash2, tokenHash1, graceUntil, newIdle, time.Now())
	if err != nil || !rotated {
		t.Fatalf("Gagal rotasi sesi: rotated=%v err=%v", rotated, err)
	}
	defer repo.RevokeSession(ctx, tokenHash2)

	token3, _ := GenerateOpaqueToken()
	again, err := repo.RotateSession(ctx, fetched.ID, HashToken(token3), tokenHash1, graceUntil, newIdle, time.Now())
	if err != nil || again {
		t.Fatalf("Rotasi ganda dari token lama seharusnya ditolak: rotated=%v err=%v", again, err)
	}

	rotatedByNew, err := repo.GetSessionByTokenHash(ctx, tokenHash2)
	if err != nil {
		t.Fatalf("Gagal cari sesi dengan token baru: %v", err)
	}
	if rotatedByNew.RotatedFromTokenHash == nil || *rotatedByNew.RotatedFromTokenHash != tokenHash1 {
		t.Fatalf("RotatedFromTokenHash harus menampung hash token lama")
	}

	rotatedByOld, err := repo.GetSessionByTokenHash(ctx, tokenHash1)
	if err != nil {
		t.Fatalf("Gagal cari sesi dengan token lama: %v", err)
	}
	if rotatedByOld.ID != rotatedByNew.ID {
		t.Fatalf("ID sesi tidak sama antara lookup token baru dan token lama")
	}

	err = repo.RevokeSession(ctx, tokenHash2)
	if err != nil {
		t.Fatalf("Gagal revoke session: %v", err)
	}

	revoked, err := repo.GetSessionByTokenHash(ctx, tokenHash2)
	if err != nil {
		t.Fatalf("Gagal get revoked session: %v", err)
	}
	if !revoked.IsRevoked {
		t.Fatalf("Sesi seharusnya berstatus is_revoked = true")
	}
}
