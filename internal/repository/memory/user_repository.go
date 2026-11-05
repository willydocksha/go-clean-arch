package memory

import (
	"context"
	"sort"
	"sync"

	"github.com/willydocksha/go-clean-arch/internal/domain"
)

// in-memory user repository, handy for tests and local dev without db
type userRepository struct {
	mu    sync.RWMutex
	users map[string]domain.User
}

// constructor for in-memory repository
func NewUserRepository() domain.UserRepository {
	return &userRepository{users: make(map[string]domain.User)}
}

// store a new user
func (r *userRepository) Create(_ context.Context, user *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// enforce unique email like the db constraint would
	for _, u := range r.users {
		if u.Email == user.Email {
			return domain.ErrConflict
		}
	}
	r.users[user.ID] = *user
	return nil
}

// find user by id
func (r *userRepository) GetByID(_ context.Context, id string) (*domain.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	u, ok := r.users[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &u, nil
}

// find user by email
func (r *userRepository) GetByEmail(_ context.Context, email string) (*domain.User, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, u := range r.users {
		if u.Email == email {
			return &u, nil
		}
	}
	return nil, domain.ErrNotFound
}

// list users ordered by newest first
func (r *userRepository) List(_ context.Context, params domain.ListParams) ([]domain.User, int, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// collect and sort
	all := make([]domain.User, 0, len(r.users))
	for _, u := range r.users {
		all = append(all, u)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].CreatedAt.After(all[j].CreatedAt) })

	// apply pagination window
	total := len(all)
	start := min(params.Offset, total)
	end := min(start+params.Limit, total)
	return all[start:end], total, nil
}

// replace an existing user
func (r *userRepository) Update(_ context.Context, user *domain.User) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.users[user.ID]; !ok {
		return domain.ErrNotFound
	}
	r.users[user.ID] = *user
	return nil
}

// remove a user
func (r *userRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.users[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.users, id)
	return nil
}
