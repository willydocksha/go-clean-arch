package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/willydocksha/go-clean-arch/internal/domain"
	"github.com/willydocksha/go-clean-arch/internal/repository/memory"
	"github.com/willydocksha/go-clean-arch/internal/usecase"
)

// build a usecase backed by the in-memory repository
func newUC() domain.UserUsecase {
	return usecase.NewUserUsecase(memory.NewUserRepository(), time.Second)
}

// create validates input and normalizes email
func TestCreate(t *testing.T) {
	uc := newUC()
	ctx := context.Background()

	// table of create cases
	tests := []struct {
		name    string
		input   domain.CreateUserInput
		wantErr error
	}{
		{"valid", domain.CreateUserInput{Name: "Willy", Email: " Willy@Example.com "}, nil},
		{"duplicate email", domain.CreateUserInput{Name: "Other", Email: "willy@example.com"}, domain.ErrConflict},
		{"empty name", domain.CreateUserInput{Name: "  ", Email: "a@b.com"}, domain.ErrInvalidInput},
		{"bad email", domain.CreateUserInput{Name: "A", Email: "nope"}, domain.ErrInvalidInput},
	}

	// run each case in order (duplicate depends on valid)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user, err := uc.Create(ctx, tt.input)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("want err %v, got %v", tt.wantErr, err)
			}
			if tt.wantErr == nil && user.Email != "willy@example.com" {
				t.Fatalf("email not normalized: %q", user.Email)
			}
		})
	}
}

// update changes fields and rejects taken emails
func TestUpdate(t *testing.T) {
	uc := newUC()
	ctx := context.Background()

	// seed two users
	a, _ := uc.Create(ctx, domain.CreateUserInput{Name: "A", Email: "a@x.com"})
	_, _ = uc.Create(ctx, domain.CreateUserInput{Name: "B", Email: "b@x.com"})

	// rename works
	name := "A2"
	got, err := uc.Update(ctx, a.ID, domain.UpdateUserInput{Name: &name})
	if err != nil || got.Name != "A2" {
		t.Fatalf("rename failed: %v, %+v", err, got)
	}

	// taking another user's email conflicts
	email := "b@x.com"
	if _, err := uc.Update(ctx, a.ID, domain.UpdateUserInput{Email: &email}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
}

// get, delete, and not-found paths
func TestGetDelete(t *testing.T) {
	uc := newUC()
	ctx := context.Background()

	// invalid id
	if _, err := uc.GetByID(ctx, "not-a-uuid"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("want invalid input, got %v", err)
	}

	// create, delete, then not found
	u, _ := uc.Create(ctx, domain.CreateUserInput{Name: "A", Email: "a@x.com"})
	if err := uc.Delete(ctx, u.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := uc.GetByID(ctx, u.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}

// list clamps limit and returns total
func TestList(t *testing.T) {
	uc := newUC()
	ctx := context.Background()

	// seed users
	for _, e := range []string{"a@x.com", "b@x.com", "c@x.com"} {
		if _, err := uc.Create(ctx, domain.CreateUserInput{Name: "U", Email: e}); err != nil {
			t.Fatal(err)
		}
	}

	// page of 2
	users, total, err := uc.List(ctx, domain.ListParams{Limit: 2})
	if err != nil || len(users) != 2 || total != 3 {
		t.Fatalf("got %d users, total %d, err %v", len(users), total, err)
	}
}
