package auth

import (
	"golang.org/x/crypto/bcrypt"
)

const DefaultBcryptCost = 12

// HashPassword mengenkripsi password plaintext menggunakan bcrypt dengan cost 12
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), DefaultBcryptCost)
	return string(bytes), err
}

// CheckPassword membandingkan password hash dengan password plaintext secara aman (waktu konstan)
func CheckPassword(hash, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}
