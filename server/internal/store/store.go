package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/ItsMeD4N/IF3110-07/server/internal/authz"
	"github.com/ItsMeD4N/IF3110-07/server/internal/connmgr"
	"github.com/ItsMeD4N/IF3110-07/server/internal/secretbox"
)

var (
	ErrNotFound = errors.New("store: not found")

	ErrConflict = errors.New("store: conflict")

	ErrReference = errors.New("store: referenced row not found")
)

type Store struct {
	db  *sql.DB
	box *secretbox.Box
}

func New(db *sql.DB, box *secretbox.Box) *Store { return &Store{db: db, box: box} }

func (s *Store) DB() *sql.DB { return s.db }

func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		switch pqErr.Code {
		case "23505":
			return ErrConflict
		case "23503":
			return ErrReference
		case "22P02":
			return ErrReference
		}
	}
	return err
}

type Group struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	MemberCount int       `json:"member_count"`
	GrantCount  int       `json:"grant_count"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

const groupSelect = `SELECT g.id, g.name, g.description,
	(SELECT count(*) FROM group_members m WHERE m.group_id = g.id),
	(SELECT count(*) FROM group_database_grants x WHERE x.group_id = g.id),
	g.created_at, g.updated_at
	FROM user_groups g`

func scanGroup(sc interface{ Scan(...any) error }) (Group, error) {
	var g Group
	err := sc.Scan(&g.ID, &g.Name, &g.Description, &g.MemberCount, &g.GrantCount, &g.CreatedAt, &g.UpdatedAt)
	return g, err
}

func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, groupSelect+` ORDER BY lower(g.name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		g, err := scanGroup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) GetGroup(ctx context.Context, id int64) (Group, error) {
	g, err := scanGroup(s.db.QueryRowContext(ctx, groupSelect+` WHERE g.id = $1`, id))
	return g, mapErr(err)
}

func (s *Store) CreateGroup(ctx context.Context, name, description string) (Group, error) {
	var id int64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO user_groups (name, description) VALUES ($1, $2) RETURNING id`, name, description).Scan(&id)
	if err != nil {
		return Group{}, mapErr(err)
	}
	return s.GetGroup(ctx, id)
}

func (s *Store) UpdateGroup(ctx context.Context, id int64, name, description string) (Group, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE user_groups SET name = $2, description = $3, updated_at = now() WHERE id = $1`, id, name, description)
	if err != nil {
		return Group{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Group{}, ErrNotFound
	}
	return s.GetGroup(ctx, id)
}

func (s *Store) DeleteGroup(ctx context.Context, id int64) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	members, err := memberIDs(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM user_groups WHERE id = $1`, id)
	if err != nil {
		return nil, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return members, tx.Commit()
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func memberIDs(ctx context.Context, q querier, groupID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT user_id FROM group_members WHERE group_id = $1`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) GroupMemberIDs(ctx context.Context, groupID int64) ([]string, error) {
	return memberIDs(ctx, s.db, groupID)
}

type Member struct {
	UserID          string    `json:"user_id"`
	Username        string    `json:"username"`
	IsPlatformAdmin bool      `json:"is_platform_admin"`
	IsActive        bool      `json:"is_active"`
	AddedAt         time.Time `json:"added_at"`
}

func (s *Store) ListMembers(ctx context.Context, groupID int64) ([]Member, error) {
	if err := s.groupExists(ctx, groupID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT u.id, u.username, u.is_platform_admin, u.is_active, m.added_at
		FROM group_members m JOIN users u ON u.id = m.user_id
		WHERE m.group_id = $1 ORDER BY lower(u.username)`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Member{}
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.UserID, &m.Username, &m.IsPlatformAdmin, &m.IsActive, &m.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddMember(ctx context.Context, groupID int64, userID string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO group_members (group_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`, groupID, userID)
	if err != nil {
		return false, mapErr(err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) RemoveMember(ctx context.Context, groupID int64, userID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM group_members WHERE group_id = $1 AND user_id = $2`, groupID, userID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) groupExists(ctx context.Context, id int64) error {
	var ok bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM user_groups WHERE id = $1)`, id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

type Grant struct {
	GroupID        int64             `json:"group_id"`
	GroupName      string            `json:"group_name"`
	ConnectionID   int64             `json:"connection_id"`
	ConnectionName string            `json:"connection_name"`
	AccessLevel    authz.AccessLevel `json:"access_level"`
	GrantedAt      time.Time         `json:"granted_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

func (s *Store) ListGroupGrants(ctx context.Context, groupID int64) ([]Grant, error) {
	if err := s.groupExists(ctx, groupID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT g.id, g.name, c.id, c.name, x.access_level, x.granted_at, x.updated_at
		FROM group_database_grants x
		JOIN user_groups g ON g.id = x.group_id
		JOIN db_connections c ON c.id = x.connection_id
		WHERE x.group_id = $1 ORDER BY lower(c.name)`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		var lvl int
		if err := rows.Scan(&g.GroupID, &g.GroupName, &g.ConnectionID, &g.ConnectionName, &lvl, &g.GrantedAt, &g.UpdatedAt); err != nil {
			return nil, err
		}
		g.AccessLevel = authz.AccessLevel(lvl)
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) SetGrant(ctx context.Context, groupID, connectionID int64, level authz.AccessLevel) error {
	if !level.ValidGrant() {
		return fmt.Errorf("store: level grant tidak valid: %d", level)
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO group_database_grants (group_id, connection_id, access_level)
		VALUES ($1, $2, $3)
		ON CONFLICT (group_id, connection_id) DO UPDATE SET access_level = EXCLUDED.access_level, updated_at = now()`,
		groupID, connectionID, int(level))
	return mapErr(err)
}

func (s *Store) DeleteGrant(ctx context.Context, groupID, connectionID int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM group_database_grants WHERE group_id = $1 AND connection_id = $2`, groupID, connectionID)
	if err != nil {
		return mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type Connection struct {
	ID           int64     `json:"id"`
	Name         string    `json:"name"`
	Description  string    `json:"description"`
	Host         string    `json:"host"`
	Port         int       `json:"port"`
	DatabaseName string    `json:"database_name"`
	Username     string    `json:"username"`
	SSLMode      string    `json:"ssl_mode"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type ConnectionInput struct {
	Name         string
	Description  string
	Host         string
	Port         int
	DatabaseName string
	Username     string
	SSLMode      string
}

const connSelect = `SELECT id, name, description, host, port, database_name, username, ssl_mode,
	created_at, updated_at FROM db_connections`

func scanConn(sc interface{ Scan(...any) error }) (Connection, error) {
	var c Connection
	err := sc.Scan(&c.ID, &c.Name, &c.Description, &c.Host, &c.Port, &c.DatabaseName, &c.Username, &c.SSLMode,
		&c.CreatedAt, &c.UpdatedAt)
	return c, err
}

func (s *Store) ListConnections(ctx context.Context) ([]Connection, error) {
	rows, err := s.db.QueryContext(ctx, connSelect+` ORDER BY lower(name)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := scanConn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) GetConnection(ctx context.Context, id int64) (Connection, error) {
	c, err := scanConn(s.db.QueryRowContext(ctx, connSelect+` WHERE id = $1`, id))
	return c, mapErr(err)
}

func (s *Store) CreateConnection(ctx context.Context, in ConnectionInput, password string) (Connection, error) {
	enc, err := s.box.Seal([]byte(password))
	if err != nil {
		return Connection{}, err
	}
	var id int64
	err = s.db.QueryRowContext(ctx, `INSERT INTO db_connections
		(name, description, host, port, database_name, username, password_encrypted, ssl_mode)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		in.Name, in.Description, in.Host, in.Port, in.DatabaseName, in.Username, enc, in.SSLMode).Scan(&id)
	if err != nil {
		return Connection{}, mapErr(err)
	}
	return s.GetConnection(ctx, id)
}

func (s *Store) UpdateConnection(ctx context.Context, id int64, in ConnectionInput, password *string) (Connection, error) {
	q := `UPDATE db_connections SET name=$2, description=$3, host=$4, port=$5, database_name=$6, username=$7,
		ssl_mode=$8, updated_at=now()`
	args := []any{id, in.Name, in.Description, in.Host, in.Port, in.DatabaseName, in.Username, in.SSLMode}
	if password != nil {
		enc, err := s.box.Seal([]byte(*password))
		if err != nil {
			return Connection{}, err
		}
		q += `, password_encrypted=$9`
		args = append(args, enc)
	}
	res, err := s.db.ExecContext(ctx, q+` WHERE id=$1`, args...)
	if err != nil {
		return Connection{}, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Connection{}, ErrNotFound
	}
	return s.GetConnection(ctx, id)
}

func (s *Store) DeleteConnection(ctx context.Context, id int64) ([]string, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT DISTINCT m.user_id FROM group_members m
		JOIN group_database_grants x ON x.group_id = m.group_id WHERE x.connection_id = $1`, id)
	if err != nil {
		return nil, err
	}
	affected := []string{}
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err != nil {
			rows.Close()
			return nil, err
		}
		affected = append(affected, uid)
	}
	rows.Close()
	res, err := tx.ExecContext(ctx, `DELETE FROM db_connections WHERE id = $1`, id)
	if err != nil {
		return nil, mapErr(err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, ErrNotFound
	}
	return affected, tx.Commit()
}

func (s *Store) ConnectionConfig(ctx context.Context, id int64) (connmgr.Config, bool, error) {
	var cfg connmgr.Config
	var enc []byte
	err := s.db.QueryRowContext(ctx, `SELECT host, port, database_name, username, password_encrypted, ssl_mode, updated_at
		FROM db_connections WHERE id = $1`, id).
		Scan(&cfg.Host, &cfg.Port, &cfg.Database, &cfg.Username, &enc, &cfg.SSLMode, &cfg.Version)
	if errors.Is(err, sql.ErrNoRows) {
		return connmgr.Config{}, false, nil
	}
	if err != nil {
		return connmgr.Config{}, false, err
	}
	pw, err := s.box.Open(enc)
	if err != nil {
		return connmgr.Config{}, false, fmt.Errorf("store: dekripsi kredensial koneksi %d gagal (APP_ENCRYPTION_KEY berubah?): %w", id, err)
	}
	cfg.Password = string(pw)
	return cfg, true, nil
}

func (s *Store) DatabaseExists(ctx context.Context, id int64) (bool, error) {
	var ok bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM db_connections WHERE id = $1)`, id).Scan(&ok)
	return ok, err
}

func (s *Store) GroupAccessLevels(ctx context.Context, userID string, databaseID int64) ([]authz.AccessLevel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT x.access_level FROM group_members m
		JOIN group_database_grants x ON x.group_id = m.group_id
		WHERE m.user_id = $1::uuid AND x.connection_id = $2`, userID, databaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []authz.AccessLevel{}
	for rows.Next() {
		var l int
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		out = append(out, authz.AccessLevel(l))
	}
	return out, rows.Err()
}

type AccessibleDatabase struct {
	ID           int64             `json:"id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	DatabaseName string            `json:"database_name"`
	AccessLevel  authz.AccessLevel `json:"access_level"`

	ViaGroups []string `json:"via_groups"`
}

func (s *Store) AccessibleDatabases(ctx context.Context, userID string) ([]AccessibleDatabase, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.id, c.name, c.description, c.database_name, x.access_level, g.name
		FROM group_members m
		JOIN group_database_grants x ON x.group_id = m.group_id
		JOIN user_groups g ON g.id = m.group_id
		JOIN db_connections c ON c.id = x.connection_id
		WHERE m.user_id = $1
		ORDER BY lower(c.name), c.id, lower(g.name)`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AccessibleDatabase{}
	levels := map[int64][]authz.AccessLevel{}
	idx := map[int64]int{}
	for rows.Next() {
		var d AccessibleDatabase
		var lvl int
		var group string
		if err := rows.Scan(&d.ID, &d.Name, &d.Description, &d.DatabaseName, &lvl, &group); err != nil {
			return nil, err
		}
		i, seen := idx[d.ID]
		if !seen {
			d.ViaGroups = []string{}
			out = append(out, d)
			i = len(out) - 1
			idx[d.ID] = i
		}
		out[i].ViaGroups = append(out[i].ViaGroups, group)
		levels[d.ID] = append(levels[d.ID], authz.AccessLevel(lvl))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	filtered := out[:0]
	for _, d := range out {
		d.AccessLevel = authz.EffectiveLevel(levels[d.ID]...)
		if d.AccessLevel != authz.LevelNone {
			filtered = append(filtered, d)
		}
	}
	return filtered, nil
}

func NormalizeName(s string) string { return strings.Join(strings.Fields(s), " ") }
