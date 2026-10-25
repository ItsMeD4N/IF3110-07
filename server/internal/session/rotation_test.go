package session

import (
	"context"
	"net/http/httptest"
	"sync"
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

func TestSession_ConcurrentRotationProducesOneToken(t *testing.T) {
	ctx := context.Background()
	for round := 0; round < 10; round++ {
		mgr, userID := newRotationUser(t)
		start := time.Now()
		mgr.SetNowFunc(func() time.Time { return start })
		sess, token, err := mgr.CreateSession(ctx, userID, "127.0.0.1", "race")
		if err != nil {
			t.Fatal(err)
		}
		mgr.SetNowFunc(func() time.Time { return start.Add(RotationInterval + time.Second) })

		const n = 8
		issued := make([]string, n)
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				rec := httptest.NewRecorder()
				_, errs[i] = mgr.ValidateSession(ctx, token, rec)
				issued[i] = cookieValue(rec)
			}(i)
		}
		wg.Wait()

		cur, err := NewSessionRepository(testutil.OpenDB(t)).GetSessionByTokenHash(ctx, HashToken(token))
		if err != nil {
			t.Fatal(err)
		}
		newTokens := 0
		for i, tok := range issued {
			if errs[i] != nil {
				t.Fatalf("request %d ditolak: %v", i, errs[i])
			}
			if tok == "" {
				continue
			}
			newTokens++
			if HashToken(tok) != cur.TokenHash {
				t.Fatalf("round %d: request %d menerima token yang tidak dikenal DB", round, i)
			}
		}
		if newTokens != 1 || cur.ID != sess.ID {
			t.Fatalf("round %d: %d token baru diterbitkan, want 1", round, newTokens)
		}
	}
}

func TestSession_CookieExpiresWithAbsoluteExpiration(t *testing.T) {
	ctx := context.Background()
	mgr, userID := newRotationUser(t)
	start := time.Now()
	mgr.SetNowFunc(func() time.Time { return start })
	sess, token, err := mgr.CreateSession(ctx, userID, "127.0.0.1", "x")
	if err != nil {
		t.Fatal(err)
	}
	later := start.Add(AbsoluteExpiration - time.Hour)
	mgr.SetNowFunc(func() time.Time { return later })
	if err := mgr.repo.UpdateIdleExpiry(ctx, sess.ID, later.Add(IdleTimeout)); err != nil {
		t.Fatal(err)
	}
	if err := mgr.RequireRotation(ctx, []string{userID}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if _, err := mgr.ValidateSession(ctx, token, rec); err != nil {
		t.Fatal(err)
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name != SessionCookieName {
			continue
		}
		if c.MaxAge > int(time.Hour.Seconds()) || c.Expires.Sub(sess.AbsoluteExpiresAt).Abs() > time.Second {
			t.Fatalf("cookie hasil rotasi: MaxAge=%d Expires=%v, absolute=%v", c.MaxAge, c.Expires, sess.AbsoluteExpiresAt)
		}
		return
	}
	t.Fatal("rotasi tidak mengirim cookie baru")
}
