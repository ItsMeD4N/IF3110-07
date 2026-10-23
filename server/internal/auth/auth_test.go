package auth

import (
	"strings"
	"testing"
)

func TestPasswordHashing_NotPlaintext(t *testing.T) {
	rawPassword := "SuperSecret123!"

	hash, err := HashPassword(rawPassword)
	if err != nil {
		t.Fatalf("Gagal melakukan hashing password: %v", err)
	}

	// 1. Pastikan hash tidak kosong
	if hash == "" {
		t.Fatal("Password hash tidak boleh kosong")
	}

	// 2. Pastikan hash tidak mengandung password plaintext
	if strings.Contains(hash, rawPassword) {
		t.Fatal("Password hash tidak boleh mengandung password asli dalam bentuk plaintext")
	}

	// 3. Pastikan hash berformat bcrypt ($2a$ atau $2b$)
	if !strings.HasPrefix(hash, "$2a$") && !strings.HasPrefix(hash, "$2b$") {
		t.Fatalf("Format hash bukan bcrypt yang valid: %s", hash)
	}
}

func TestPasswordVerification_SuccessAndFail(t *testing.T) {
	rawPassword := "MySecurePassword#2026"

	hash, err := HashPassword(rawPassword)
	if err != nil {
		t.Fatalf("Gagal hashing: %v", err)
	}

	// Password benar harus lolos
	if !CheckPassword(hash, rawPassword) {
		t.Fatal("Password yang benar seharusnya berhasil diverifikasi")
	}

	// Password salah harus ditolak
	if CheckPassword(hash, "WrongPassword#999") {
		t.Fatal("Password yang salah tidak boleh berhasil diverifikasi")
	}
}
