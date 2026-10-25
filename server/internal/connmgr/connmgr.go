package connmgr

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "github.com/lib/pq"
)

var (
	ErrNotFound = errors.New("connmgr: connection not found")

	ErrConnectionFailed = errors.New("connmgr: connection failed")

	ErrPlatformTarget = errors.New("connmgr: koneksi menunjuk ke database platform")
)

const msgPlatformTarget = "koneksi ini menunjuk ke database platform dan tidak boleh dikelola"

func Fingerprint(ctx context.Context, db *sql.DB) (string, error) {
	var fp string
	err := db.QueryRowContext(ctx, `
		SELECT d.oid::text || ':' || current_database() || ':' || extract(epoch FROM pg_postmaster_start_time())::text
		FROM pg_database d WHERE d.datname = current_database()`).Scan(&fp)
	return fp, err
}

type ConnError struct {
	DatabaseID int64
	Detail     string
}

func (e *ConnError) Error() string {
	return fmt.Sprintf("connmgr: koneksi ke database %d gagal: %s", e.DatabaseID, e.Detail)
}

func (e *ConnError) Unwrap() error { return ErrConnectionFailed }

type Config struct {
	Host     string
	Port     int
	Database string
	Username string
	Password string
	SSLMode  string

	Version time.Time
}

func (c Config) DSN(connectTimeout time.Duration) string {
	sslmode := c.SSLMode
	if sslmode == "" {
		sslmode = "disable"
	}
	q := url.Values{}
	q.Set("sslmode", sslmode)
	secs := int(connectTimeout / time.Second)
	if secs < 1 {
		secs = 1
	}
	q.Set("connect_timeout", strconv.Itoa(secs))
	q.Set("application_name", "if3110-db-platform")
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.Username, c.Password),
		Host:     net.JoinHostPort(c.Host, strconv.Itoa(c.Port)),
		Path:     "/" + c.Database,
		RawQuery: q.Encode(),
	}
	return u.String()
}

func (c Config) SameTarget(o Config) bool {
	return strings.EqualFold(strings.TrimSpace(c.Host), strings.TrimSpace(o.Host)) &&
		c.Port == o.Port &&
		c.Database == o.Database &&
		c.Username == o.Username &&
		normalizeSSLMode(c.SSLMode) == normalizeSSLMode(o.SSLMode)
}

func normalizeSSLMode(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "disable"
	}
	return s
}

type ConfigSource interface {
	ConnectionConfig(ctx context.Context, id int64) (cfg Config, found bool, err error)
}

type Options struct {
	ConnectTimeout time.Duration

	MaxOpenConns int

	HealthTTL time.Duration

	PlatformFingerprint string
}

type entry struct {
	db        *sql.DB
	version   time.Time
	checkedAt time.Time
	verified  bool
}

type Manager struct {
	src  ConfigSource
	opts Options

	mu    sync.Mutex
	pools map[int64]*entry
}

func New(src ConfigSource, opts Options) *Manager {
	if opts.ConnectTimeout <= 0 {
		opts.ConnectTimeout = 5 * time.Second
	}
	if opts.MaxOpenConns <= 0 {
		opts.MaxOpenConns = 5
	}
	if opts.HealthTTL == 0 {
		opts.HealthTTL = 5 * time.Second
	}
	return &Manager{src: src, opts: opts, pools: map[int64]*entry{}}
}

func (m *Manager) Pool(ctx context.Context, id int64) (*sql.DB, error) {
	cfg, found, err := m.src.ConnectionConfig(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found {
		m.Invalidate(id)
		return nil, ErrNotFound
	}

	db, err := m.getOrOpen(id, cfg)
	if err != nil {
		return nil, err
	}
	if m.recentlyHealthy(id, db) {
		return db, nil
	}

	pctx, cancel := context.WithTimeout(ctx, m.opts.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		m.setHealthy(id, db, false)
		return nil, &ConnError{DatabaseID: id, Detail: sanitize(err, cfg)}
	}
	if !m.isVerified(id, db) {
		if err := m.checkNotPlatform(pctx, db); err != nil {
			if errors.Is(err, ErrPlatformTarget) {
				return nil, err
			}
			return nil, &ConnError{DatabaseID: id, Detail: sanitize(err, cfg)}
		}
	}
	m.setHealthy(id, db, true)
	return db, nil
}

func (m *Manager) checkNotPlatform(ctx context.Context, db *sql.DB) error {
	if m.opts.PlatformFingerprint == "" {
		return nil
	}
	fp, err := Fingerprint(ctx, db)
	if err != nil {
		return err
	}
	if fp == m.opts.PlatformFingerprint {
		return ErrPlatformTarget
	}
	return nil
}

func (m *Manager) isVerified(id int64, db *sql.DB) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.pools[id]
	return ok && e.db == db && e.verified
}

func (m *Manager) recentlyHealthy(id int64, db *sql.DB) bool {
	if m.opts.HealthTTL < 0 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.pools[id]
	return ok && e.db == db && !e.checkedAt.IsZero() && time.Since(e.checkedAt) < m.opts.HealthTTL
}

func (m *Manager) setHealthy(id int64, db *sql.DB, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, exists := m.pools[id]; exists && e.db == db {
		if ok {
			e.checkedAt = time.Now()
			e.verified = true
		} else {
			e.checkedAt = time.Time{}
		}
	}
}

func (m *Manager) getOrOpen(id int64, cfg Config) (*sql.DB, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.pools[id]; ok {
		if e.version.Equal(cfg.Version) {
			return e.db, nil
		}

		e.db.Close()
		delete(m.pools, id)
	}
	db, err := m.open(cfg)
	if err != nil {
		return nil, &ConnError{DatabaseID: id, Detail: sanitize(err, cfg)}
	}
	m.pools[id] = &entry{db: db, version: cfg.Version}
	return db, nil
}

func (m *Manager) open(cfg Config) (*sql.DB, error) {
	db, err := sql.Open("postgres", cfg.DSN(m.opts.ConnectTimeout))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(m.opts.MaxOpenConns)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	return db, nil
}

func (m *Manager) Invalidate(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.pools[id]; ok {
		e.db.Close()
		delete(m.pools, id)
	}
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, e := range m.pools {
		e.db.Close()
		delete(m.pools, id)
	}
}

type TestResult struct {
	OK            bool   `json:"ok"`
	LatencyMS     int64  `json:"latency_ms"`
	ServerVersion string `json:"server_version,omitempty"`
	Error         string `json:"error,omitempty"`
}

func (m *Manager) Test(ctx context.Context, cfg Config) TestResult {
	start := time.Now()
	db, err := m.open(cfg)
	if err != nil {
		return TestResult{Error: sanitize(err, cfg)}
	}
	defer db.Close()

	tctx, cancel := context.WithTimeout(ctx, m.opts.ConnectTimeout)
	defer cancel()
	var version string
	if err := db.QueryRowContext(tctx, `SHOW server_version`).Scan(&version); err != nil {
		return TestResult{LatencyMS: time.Since(start).Milliseconds(), Error: sanitize(err, cfg)}
	}
	if err := m.checkNotPlatform(tctx, db); err != nil {
		msg := msgPlatformTarget
		if !errors.Is(err, ErrPlatformTarget) {
			msg = sanitize(err, cfg)
		}
		return TestResult{LatencyMS: time.Since(start).Milliseconds(), Error: msg}
	}
	return TestResult{OK: true, LatencyMS: time.Since(start).Milliseconds(), ServerVersion: version}
}

func (m *Manager) TestSaved(ctx context.Context, id int64) (TestResult, error) {
	cfg, found, err := m.src.ConnectionConfig(ctx, id)
	if err != nil {
		return TestResult{}, err
	}
	if !found {
		return TestResult{}, ErrNotFound
	}
	return m.Test(ctx, cfg), nil
}

func sanitize(err error, cfg Config) string {
	msg := err.Error()
	if cfg.Password != "" {
		msg = strings.ReplaceAll(msg, cfg.Password, "****")
		msg = strings.ReplaceAll(msg, url.QueryEscape(cfg.Password), "****")
		msg = strings.ReplaceAll(msg, url.PathEscape(cfg.Password), "****")
	}
	msg = strings.TrimPrefix(msg, "pq: ")
	switch {
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "i/o timeout"):
		return "timeout: server database tidak merespons (" + msg + ")"
	case strings.Contains(msg, "connection refused") || strings.Contains(msg, "actively refused"):
		return "koneksi ditolak: pastikan host dan port benar serta server PostgreSQL berjalan (" + msg + ")"

	case strings.Contains(msg, "no such host"):
		return "host tidak ditemukan (" + msg + ")"
	case strings.Contains(msg, "password authentication failed"):
		return "autentikasi gagal: username atau password salah"
	case strings.Contains(msg, "does not exist") && strings.Contains(msg, "database"):
		return "database tidak ditemukan di server (" + msg + ")"
	}
	return msg
}

func ConfigFromURL(raw string) (Config, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return Config{}, err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return Config{}, fmt.Errorf("connmgr: skema URL harus postgres://")
	}
	port := 5432
	if p := u.Port(); p != "" {
		if port, err = strconv.Atoi(p); err != nil {
			return Config{}, err
		}
	}
	pw, _ := u.User.Password()
	return Config{
		Host:     u.Hostname(),
		Port:     port,
		Database: strings.TrimPrefix(u.Path, "/"),
		Username: u.User.Username(),
		Password: pw,
		SSLMode:  u.Query().Get("sslmode"),
	}, nil
}
