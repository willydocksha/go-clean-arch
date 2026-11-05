package domain

import (
	"context"
	"time"
)

// user is the core entity, free of any framework or db tags
type User struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// create user input
type CreateUserInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// update user input, nil fields are left unchanged
type UpdateUserInput struct {
	Name  *string `json:"name"`
	Email *string `json:"email"`
}

// pagination params
type ListParams struct {
	Limit  int
	Offset int
}

// repository port, implemented by the infrastructure layer
type UserRepository interface {
	Create(ctx context.Context, user *User) error
	GetByID(ctx context.Context, id string) (*User, error)
	GetByEmail(ctx context.Context, email string) (*User, error)
	List(ctx context.Context, params ListParams) ([]User, int, error)
	Update(ctx context.Context, user *User) error
	Delete(ctx context.Context, id string) error
}

// usecase port, consumed by the delivery layer
type UserUsecase interface {
	Create(ctx context.Context, input CreateUserInput) (*User, error)
	GetByID(ctx context.Context, id string) (*User, error)
	List(ctx context.Context, params ListParams) ([]User, int, error)
	Update(ctx context.Context, id string, input UpdateUserInput) (*User, error)
	Delete(ctx context.Context, id string) error
}
