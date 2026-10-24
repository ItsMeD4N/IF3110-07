package session

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ItsMeD4N/IF3110-07/server/internal/testutil"
)

func newRotationUser(t *testing.T) (*Manager, string) {
	t.Helper()
	db := testutil.OpenDB(t)
	var id string
	if err := db.QueryRow(`INSERT INTO users (username, password_hash) VALUES ($1, 'x') RETURNING id`,
		testutil.Unique("rot")).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Exec(`DELETE FROM users WHERE id = $1`, id) })
	return NewManager(NewSessionRepository(db), false), id
}

func cookieValue(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == SessionCookieName {
			return c.Value
		}
	}
	return ""
}

func TestSession_RotationRequiredAfterPrivilegeChange(t *testing.T) {
	ctx := context.Background()
	mgr, userID := newRotationUser(t)

	sess, token, err := mgr.CreateSession(ctx, userID, "127.0.0.1", "rotation-test")
	if err != nil {
		t.Fatal(err)
	}
	other, otherToken, err := mgr.CreateSession(ctx, userID, "127.0.0.1", "device-2")
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	if _, err := mgr.ValidateSession(ctx, token, rec); err != nil {
		t.Fatal(err)
	}
	if cookieValue(rec) != "" {
		t.Fatal("token dirotasi tanpa alasan")
	}

	if err := mgr.RequireRotation(ctx, []string{userID}); err != nil {
		t.Fatal(err)
	}

	rec = httptest.NewRecorder()
	got, err := mgr.ValidateSession(ctx, token, rec)
	if err != nil {
		t.Fatal(err)
	}
	newToken := cookieValue(rec)
	if newToken == "" || newToken == token || got.TokenHash != HashToken(newToken) || got.RotationRequired {
		t.Fatalf("rotasi karena privilege tidak terjadi: cookie=%q sess=%+v", newToken, got)
	}
	if d := got.AbsoluteExpiresAt.Sub(sess.AbsoluteExpiresAt); got.ID != sess.ID || d > time.Millisecond || d < -time.Millisecond {
		t.Fatal("rotasi tidak boleh membuat session baru atau memperpanjang absolute expiration")
	}

	rec = httptest.NewRecorder()
	if _, err := mgr.ValidateSession(ctx, token, rec); err != nil {
		t.Fatalf("token lama dalam grace period ditolak: %v", err)
	}
	if cookieValue(rec) != "" {
		t.Fatal("token lama memicu rotasi berulang")
	}

	rec = httptest.NewRecorder()
	if _, err := mgr.ValidateSession(ctx, newToken, rec); err != nil {
		t.Fatal(err)
	}
	if cookieValue(rec) != "" {
		t.Fatal("penanda rotasi tidak dibersihkan")
	}

	rec = httptest.NewRecorder()
	if _, err := mgr.ValidateSession(ctx, otherToken, rec); err != nil {
		t.Fatal(err)
	}
	if v := cookieValue(rec); v == "" || v == otherToken {
		t.Fatalf("session perangkat lain (%s) tidak dirotasi", other.ID)
	}
}

func TestSession_RequireRotationSkipsRevokedAndEmpty(t *testing.T) {
	ctx := context.Background()
	mgr, userID := newRotationUser(t)
	if err := mgr.RequireRotation(ctx, nil); err != nil {
		t.Fatalf("daftar kosong harus no-op: %v", err)
	}
	_, token, _ := mgr.CreateSession(ctx, userID, "127.0.0.1", "x")
	if err := mgr.Revoke(ctx, token, nil); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RequireRotation(ctx, []string{userID}); err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.ValidateSession(ctx, token, nil); err != ErrSessionRevoked {
		t.Fatalf("session yang dicabut harus tetap ditolak, err = %v", err)
	}
}

func TestSession_ForceRotateKeepsOldHash(t *testing.T) {
	ctx := context.Background()
	mgr, userID := newRotationUser(t)
	sess, token, err := mgr.CreateSession(ctx, userID, "127.0.0.1", "x")
	if err != nil {
		t.Fatal(err)
	}
	oldHash := HashToken(token)
	if err := mgr.ForceRotate(ctx, sess, httptest.NewRecorder()); err != nil {
		t.Fatal(err)
	}
	if sess.RotatedFromTokenHash == nil || *sess.RotatedFromTokenHash != oldHash || sess.TokenHash == oldHash {
		t.Fatalf("RotatedFromTokenHash = %v, want hash lama %s", sess.RotatedFromTokenHash, oldHash)
	}
}
