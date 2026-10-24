package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"testing"
)

func newBox(t *testing.T) *Box {
	t.Helper()
	b, err := New(DevKey())
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpenRoundTrip(t *testing.T) {
	b := newBox(t)
	for _, pt := range []string{"", "postgres", "p@ss:/w#rd?%20 ünïcode"} {
		ct, err := b.Seal([]byte(pt))
		if err != nil {
			t.Fatal(err)
		}
		if pt != "" && bytes.Contains(ct, []byte(pt)) {
			t.Fatalf("ciphertext memuat plaintext %q", pt)
		}
		got, err := b.Open(ct)
		if err != nil || string(got) != pt {
			t.Fatalf("Open = (%q, %v), want %q", got, err, pt)
		}
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	b := newBox(t)
	a, _ := b.Seal([]byte("same"))
	c, _ := b.Seal([]byte("same"))
	if bytes.Equal(a, c) {
		t.Fatal("dua Seal plaintext sama menghasilkan ciphertext identik (nonce dipakai ulang)")
	}
}

func TestOpenRejectsTamperedOrForeignData(t *testing.T) {
	b := newBox(t)
	ct, _ := b.Seal([]byte("rahasia"))

	tampered := append([]byte{}, ct...)
	tampered[len(tampered)-1] ^= 0x01
	wrongVersion := append([]byte{}, ct...)
	wrongVersion[0] = 9

	otherKey := make([]byte, KeySize)
	_, _ = rand.Read(otherKey)
	other, _ := New(otherKey)

	cases := map[string]func() ([]byte, error){
		"byte diubah":    func() ([]byte, error) { return b.Open(tampered) },
		"versi salah":    func() ([]byte, error) { return b.Open(wrongVersion) },
		"terlalu pendek": func() ([]byte, error) { return b.Open(ct[:5]) },
		"kosong":         func() ([]byte, error) { return b.Open(nil) },
		"kunci lain":     func() ([]byte, error) { return other.Open(ct) },
	}
	for name, f := range cases {
		if pt, err := f(); !errors.Is(err, ErrDecrypt) || pt != nil {
			t.Errorf("%s: Open = (%q, %v), want ErrDecrypt", name, pt, err)
		}
	}
}

func TestNewRequires32ByteKey(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33} {
		if _, err := New(make([]byte, n)); err == nil {
			t.Errorf("New(key %d byte) harus error", n)
		}
	}
}

func TestParseKey(t *testing.T) {
	key := make([]byte, KeySize)
	_, _ = rand.Read(key)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		got, err := ParseKey(enc.EncodeToString(key))
		if err != nil || !bytes.Equal(got, key) {
			t.Fatalf("ParseKey gagal untuk encoding %v: %v", enc, err)
		}
	}
	for _, bad := range []string{"", "bukan-base64!!", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := ParseKey(bad); err == nil {
			t.Errorf("ParseKey(%q) harus error", bad)
		}
	}
	if len(DevKey()) != KeySize {
		t.Fatal("DevKey harus 32 byte")
	}
}
