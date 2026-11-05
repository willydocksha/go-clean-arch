package domain

import (
	"context"
	"io"
	"time"
)

// product status values
const (
	ProductStatusActive   = "active"
	ProductStatusInactive = "inactive"
)

// product entity
type Product struct {
	ID          string    `json:"id"`
	SKU         string    `json:"sku"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	Price       int64     `json:"price"` // smallest currency unit (e.g. rupiah)
	Stock       int       `json:"stock"`
	Status      string    `json:"status"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// full payload for create (post) and replace (put); every field is required
type ProductInput struct {
	SKU         string `json:"sku"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	Price       *int64 `json:"price"`
	Stock       *int   `json:"stock"`
	Status      string `json:"status"`
}

// filters for listing products
type ProductFilter struct {
	Search   string // matches sku or name
	Category string
	Status   string
	SortBy   string // created_at | name | price | stock
	SortDesc bool
	Limit    int
	Offset   int
}

// summary of an import run
type ImportResult struct {
	TotalRows int              `json:"total_rows"`
	Created   int              `json:"created"`
	Updated   int              `json:"updated"`
	Failed    int              `json:"failed"`
	Errors    []ImportRowError `json:"errors,omitempty"`
}

// a single failed row in an import
type ImportRowError struct {
	Row     int    `json:"row"` // 1-based, header is row 1
	SKU     string `json:"sku,omitempty"`
	Message string `json:"message"`
}

// product persistence port
type ProductRepository interface {
	List(ctx context.Context, f ProductFilter) ([]Product, int, error)
	GetByID(ctx context.Context, id string) (*Product, error)
	Create(ctx context.Context, p *Product) error
	Update(ctx context.Context, p *Product) error
	UpdateStatus(ctx context.Context, id, status string, updatedAt time.Time) (*Product, error)
	Delete(ctx context.Context, id string) error
	UpsertBySKU(ctx context.Context, p *Product) (created bool, err error)
}

// product business port
type ProductUsecase interface {
	List(ctx context.Context, f ProductFilter) ([]Product, int, error)
	GetByID(ctx context.Context, id string) (*Product, error)
	Create(ctx context.Context, in ProductInput) (*Product, error)
	Replace(ctx context.Context, id string, in ProductInput) (*Product, error)
	UpdateStatus(ctx context.Context, id, status string) (*Product, error)
	Delete(ctx context.Context, id string) error
	Export(ctx context.Context, f ProductFilter, format string) (*File, error)
	Import(ctx context.Context, r io.Reader, filename string) (*ImportResult, error)
	ImportTemplate(format string) (*File, error)
}
