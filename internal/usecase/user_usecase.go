package usecase

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/willydocksha/go-clean-arch/internal/domain"
)

// pagination bounds
const (
	defaultLimit = 10
	maxLimit     = 100
)

// user usecase holds business rules, depends only on domain ports
type userUsecase struct {
	repo    domain.UserRepository
	timeout time.Duration
	now     func() time.Time
}

// constructor for user usecase
func NewUserUsecase(repo domain.UserRepository, timeout time.Duration) domain.UserUsecase {
	return &userUsecase{repo: repo, timeout: timeout, now: time.Now}
}

// create a new user after validation and uniqueness check
func (u *userUsecase) Create(ctx context.Context, input domain.CreateUserInput) (*domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	// normalize and validate input
	name := strings.TrimSpace(input.Name)
	email := strings.ToLower(strings.TrimSpace(input.Email))
	if err := validateUser(name, email); err != nil {
		return nil, err
	}

	// ensure email is unique
	if err := u.ensureEmailAvailable(ctx, email, ""); err != nil {
		return nil, err
	}

	// build and persist entity
	now := u.now().UTC()
	user := &domain.User{
		ID:        uuid.NewString(),
		Name:      name,
		Email:     email,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := u.repo.Create(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

// get a single user by id
func (u *userUsecase) GetByID(ctx context.Context, id string) (*domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	// reject malformed ids early
	if _, err := uuid.Parse(id); err != nil {
		return nil, fmt.Errorf("%w: invalid id", domain.ErrInvalidInput)
	}
	return u.repo.GetByID(ctx, id)
}

// list users with clamped pagination
func (u *userUsecase) List(ctx context.Context, params domain.ListParams) ([]domain.User, int, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	// clamp limit and offset
	if params.Limit <= 0 {
		params.Limit = defaultLimit
	}
	if params.Limit > maxLimit {
		params.Limit = maxLimit
	}
	if params.Offset < 0 {
		params.Offset = 0
	}
	return u.repo.List(ctx, params)
}

// partially update a user
func (u *userUsecase) Update(ctx context.Context, id string, input domain.UpdateUserInput) (*domain.User, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	// load existing user
	user, err := u.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// apply changes
	if input.Name != nil {
		user.Name = strings.TrimSpace(*input.Name)
	}
	if input.Email != nil {
		email := strings.ToLower(strings.TrimSpace(*input.Email))
		if email != user.Email {
			if err := u.ensureEmailAvailable(ctx, email, user.ID); err != nil {
				return nil, err
			}
		}
		user.Email = email
	}

	// validate final state and persist
	if err := validateUser(user.Name, user.Email); err != nil {
		return nil, err
	}
	user.UpdatedAt = u.now().UTC()
	if err := u.repo.Update(ctx, user); err != nil {
		return nil, err
	}
	return user, nil
}

// delete a user by id
func (u *userUsecase) Delete(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	// reject malformed ids early
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("%w: invalid id", domain.ErrInvalidInput)
	}
	return u.repo.Delete(ctx, id)
}

// check that no other user owns this email
func (u *userUsecase) ensureEmailAvailable(ctx context.Context, email, selfID string) error {
	existing, err := u.repo.GetByEmail(ctx, email)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return nil
	case err != nil:
		return err
	case existing.ID != selfID:
		return fmt.Errorf("%w: email already registered", domain.ErrConflict)
	}
	return nil
}

// validate user fields
func validateUser(name, email string) error {
	if name == "" {
		return fmt.Errorf("%w: name is required", domain.ErrInvalidInput)
	}
	if len(name) > 100 {
		return fmt.Errorf("%w: name must be at most 100 characters", domain.ErrInvalidInput)
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return fmt.Errorf("%w: email is invalid", domain.ErrInvalidInput)
	}
	return nil
}
