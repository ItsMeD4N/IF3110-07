package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
)

const KeySize = 32

const version byte = 1

var ErrDecrypt = errors.New("secretbox: decrypt failed")

type Box struct {
	aead cipher.AEAD
}

func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secretbox: key harus %d byte, didapat %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

func ParseKey(s string) ([]byte, error) {
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == KeySize {
			return b, nil
		}
	}
	return nil, fmt.Errorf("secretbox: APP_ENCRYPTION_KEY harus base64 dari %d byte acak (contoh: openssl rand -base64 32)", KeySize)
}

func DevKey() []byte {
	sum := sha256.Sum256([]byte("if3110-07-development-only-key"))
	return sum[:]
}

func (b *Box) Seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 1+len(nonce)+len(plaintext)+b.aead.Overhead())
	out = append(out, version)
	out = append(out, nonce...)
	return b.aead.Seal(out, nonce, plaintext, []byte{version}), nil
}

func (b *Box) Open(data []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(data) < 1+ns+b.aead.Overhead() || data[0] != version {
		return nil, ErrDecrypt
	}
	pt, err := b.aead.Open(nil, data[1:1+ns], data[1+ns:], []byte{version})
	if err != nil {
		return nil, ErrDecrypt
	}
	return pt, nil
}
