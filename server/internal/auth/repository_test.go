package auth

import (
	"context"
	"os"
	"testing"

	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func TestUserRepository_CRUD(t *testing.T) {
	connStr := os.Getenv("DATABASE_URL")
	if connStr == "" {
		connStr = "postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable"
	}

	db, err := testutil.InitDB(connStr)
	if err != nil {
		t.Fatalf("Gagal terhubung ke DB: %v", err)
	}
	defer db.Close()

	repo := NewUserRepository(db)
	ctx := context.Background()

	admin, err := repo.GetByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("Gagal mengambil seed admin: %v", err)
	}
	if !admin.IsPlatformAdmin {
		t.Fatalf("User admin harus memiliki status is_platform_admin = true")
	}

	testUsername := "repo_test_user"
	_ = repo.Delete(ctx, testUsername)

	if existing, err := repo.GetByUsername(ctx, testUsername); err == nil && existing != nil {
		_ = repo.Delete(ctx, existing.ID)
	}

	dummyHash, _ := HashPassword("SecretPass123!")
	newUser, err := repo.Create(ctx, testUsername, dummyHash, false)
	if err != nil {
		t.Fatalf("Gagal membuat user baru: %v", err)
	}
	defer repo.Delete(ctx, newUser.ID)

	if newUser.Username != testUsername {
		t.Fatalf("Expected username %s, got %s", testUsername, newUser.Username)
	}

	_, err = repo.Create(ctx, testUsername, dummyHash, false)
	if err != ErrUsernameTaken {
		t.Fatalf("Expected ErrUsernameTaken, got %v", err)
	}

	fetched, err := repo.GetByID(ctx, newUser.ID)
	if err != nil {
		t.Fatalf("Gagal get user by ID: %v", err)
	}
	if fetched.Username != testUsername {
		t.Fatalf("Expected %s, got %s", testUsername, fetched.Username)
	}

	err = repo.Update(ctx, newUser.ID, "repo_test_user_updated", true, true)
	if err != nil {
		t.Fatalf("Gagal update user: %v", err)
	}

	updated, _ := repo.GetByID(ctx, newUser.ID)
	if updated.Username != "repo_test_user_updated" || !updated.IsPlatformAdmin {
		t.Fatalf("User tidak terupdate dengan benar: %+v", updated)
	}

	newHash, _ := HashPassword("NewSecretPass456!")
	err = repo.UpdatePassword(ctx, newUser.ID, newHash)
	if err != nil {
		t.Fatalf("Gagal update password: %v", err)
	}

	withNewPass, _ := repo.GetByID(ctx, newUser.ID)
	if !CheckPassword(withNewPass.PasswordHash, "NewSecretPass456!") {
		t.Fatalf("Password baru tidak cocok setelah update password")
	}

	err = repo.Delete(ctx, newUser.ID)
	if err != nil {
		t.Fatalf("Gagal delete user: %v", err)
	}

	_, err = repo.GetByID(ctx, newUser.ID)
	if err != ErrUserNotFound {
		t.Fatalf("Expected ErrUserNotFound setelah delete, got %v", err)
	}
}
