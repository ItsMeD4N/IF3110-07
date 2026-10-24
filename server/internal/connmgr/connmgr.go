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
)

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

type ConfigSource interface {
	ConnectionConfig(ctx context.Context, id int64) (cfg Config, found bool, err error)
}

type Options struct {
	ConnectTimeout time.Duration

	MaxOpenConns int
}

type entry struct {
	db      *sql.DB
	version time.Time
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

	pctx, cancel := context.WithTimeout(ctx, m.opts.ConnectTimeout)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {

		return nil, &ConnError{DatabaseID: id, Detail: sanitize(err, cfg)}
	}
	return db, nil
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
	case strings.Contains(msg, "connection refused"):
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
