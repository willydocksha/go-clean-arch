package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/willydocksha/go-clean-arch/internal/domain"
)

// postgres unique violation code
const uniqueViolation = "23505"

// postgres-backed user repository
type userRepository struct {
	db *pgxpool.Pool
}

// constructor for postgres repository
func NewUserRepository(db *pgxpool.Pool) domain.UserRepository {
	return &userRepository{db: db}
}

// insert a new user
func (r *userRepository) Create(ctx context.Context, user *domain.User) error {
	const q = `INSERT INTO users (id, name, email, created_at, updated_at) VALUES ($1, $2, $3, $4, $5)`
	_, err := r.db.Exec(ctx, q, user.ID, user.Name, user.Email, user.CreatedAt, user.UpdatedAt)
	return mapError(err)
}

// select user by id
func (r *userRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	const q = `SELECT id, name, email, created_at, updated_at FROM users WHERE id = $1`
	return r.getOne(ctx, q, id)
}

// select user by email
func (r *userRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	const q = `SELECT id, name, email, created_at, updated_at FROM users WHERE email = $1`
	return r.getOne(ctx, q, email)
}

// select a page of users plus total count
func (r *userRepository) List(ctx context.Context, params domain.ListParams) ([]domain.User, int, error) {
	// count total rows
	var total int
	if err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
		return nil, 0, mapError(err)
	}

	// fetch page
	const q = `SELECT id, name, email, created_at, updated_at FROM users
		ORDER BY created_at DESC LIMIT $1 OFFSET $2`
	rows, err := r.db.Query(ctx, q, params.Limit, params.Offset)
	if err != nil {
		return nil, 0, mapError(err)
	}
	users, err := pgx.CollectRows(rows, scanUser)
	if err != nil {
		return nil, 0, mapError(err)
	}
	return users, total, nil
}

// update user fields
func (r *userRepository) Update(ctx context.Context, user *domain.User) error {
	const q = `UPDATE users SET name = $2, email = $3, updated_at = $4 WHERE id = $1`
	tag, err := r.db.Exec(ctx, q, user.ID, user.Name, user.Email, user.UpdatedAt)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// delete user by id
func (r *userRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// run a single-row query and scan into user
func (r *userRepository) getOne(ctx context.Context, q string, arg any) (*domain.User, error) {
	rows, err := r.db.Query(ctx, q, arg)
	if err != nil {
		return nil, mapError(err)
	}
	user, err := pgx.CollectExactlyOneRow(rows, scanUser)
	if err != nil {
		return nil, mapError(err)
	}
	return &user, nil
}

// scan a row into user entity
func scanUser(row pgx.CollectableRow) (domain.User, error) {
	var u domain.User
	err := row.Scan(&u.ID, &u.Name, &u.Email, &u.CreatedAt, &u.UpdatedAt)
	return u, err
}

// translate driver errors into domain errors
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return domain.ErrConflict
	}
	return fmt.Errorf("postgres: %w", err)
}
