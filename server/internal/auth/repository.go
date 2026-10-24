package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrUserNotFound  = errors.New("pengguna tidak ditemukan")
	ErrUsernameTaken = errors.New("username sudah digunakan")
)

type UserRepository interface {
	GetByUsername(ctx context.Context, username string) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
	List(ctx context.Context) ([]*User, error)
	Create(ctx context.Context, username, passwordHash string, isPlatformAdmin bool) (*User, error)
	Update(ctx context.Context, id, username string, isPlatformAdmin, isActive bool) error
	UpdatePassword(ctx context.Context, id, passwordHash string) error
	Delete(ctx context.Context, id string) error
}

type postgresUserRepository struct {
	db *sql.DB
}

func NewUserRepository(db *sql.DB) UserRepository {
	return &postgresUserRepository{db: db}
}

func (r *postgresUserRepository) GetByUsername(ctx context.Context, username string) (*User, error) {
	query := `
		SELECT id, username, password_hash, is_platform_admin, is_active, created_at, updated_at
		FROM users
		WHERE username = $1
	`
	row := r.db.QueryRowContext(ctx, query, username)

	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("gagal query user by username: %w", err)
	}

	return &u, nil
}

func (r *postgresUserRepository) GetByID(ctx context.Context, id string) (*User, error) {
	query := `
		SELECT id, username, password_hash, is_platform_admin, is_active, created_at, updated_at
		FROM users
		WHERE id = $1
	`
	row := r.db.QueryRowContext(ctx, query, id)

	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("gagal query user by id: %w", err)
	}

	return &u, nil
}

func (r *postgresUserRepository) List(ctx context.Context) ([]*User, error) {
	query := `
		SELECT id, username, is_platform_admin, is_active, created_at, updated_at
		FROM users
		ORDER BY created_at ASC
	`
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("gagal query list users: %w", err)
	}
	defer rows.Close()

	users := make([]*User, 0)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("gagal scan user: %w", err)
		}
		users = append(users, &u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterasi baris users: %w", err)
	}

	return users, nil
}

func (r *postgresUserRepository) Create(ctx context.Context, username, passwordHash string, isPlatformAdmin bool) (*User, error) {
	query := `
		INSERT INTO users (username, password_hash, is_platform_admin, is_active)
		VALUES ($1, $2, $3, TRUE)
		RETURNING id, username, is_platform_admin, is_active, created_at, updated_at
	`
	var u User
	err := r.db.QueryRowContext(ctx, query, username, passwordHash, isPlatformAdmin).Scan(
		&u.ID, &u.Username, &u.IsPlatformAdmin, &u.IsActive, &u.CreatedAt, &u.UpdatedAt,
	)
	if err != nil {
		if strings.Contains(err.Error(), "users_username_key") || strings.Contains(err.Error(), "duplicate key") {
			return nil, ErrUsernameTaken
		}
		return nil, fmt.Errorf("gagal insert user: %w", err)
	}

	return &u, nil
}

func (r *postgresUserRepository) Update(ctx context.Context, id, username string, isPlatformAdmin, isActive bool) error {
	query := `
		UPDATE users
		SET username = $2, is_platform_admin = $3, is_active = $4, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`
	res, err := r.db.ExecContext(ctx, query, id, username, isPlatformAdmin, isActive)
	if err != nil {
		if strings.Contains(err.Error(), "users_username_key") || strings.Contains(err.Error(), "duplicate key") {
			return ErrUsernameTaken
		}
		return fmt.Errorf("gagal update user: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrUserNotFound
	}

	return nil
}

func (r *postgresUserRepository) UpdatePassword(ctx context.Context, id, passwordHash string) error {
	query := `
		UPDATE users
		SET password_hash = $2, updated_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`
	res, err := r.db.ExecContext(ctx, query, id, passwordHash)
	if err != nil {
		return fmt.Errorf("gagal update password: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrUserNotFound
	}

	return nil
}

func (r *postgresUserRepository) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM users WHERE id = $1`
	res, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("gagal delete user: %w", err)
	}

	rowsAffected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrUserNotFound
	}

	return nil
}
