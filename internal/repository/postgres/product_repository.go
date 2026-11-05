package postgres

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/willydocksha/go-clean-arch/internal/domain"
)

// selected columns, kept in one place to match scanProduct
const productColumns = `id, sku, name, description, category, price, stock, status, created_at, updated_at`

// whitelist of sortable columns (prevents sql injection via sort param)
var productSortColumns = map[string]string{
	"created_at": "created_at",
	"name":       "name",
	"price":      "price",
	"stock":      "stock",
}

// postgres-backed product repository
type productRepository struct {
	db *pgxpool.Pool
}

// constructor for product repository
func NewProductRepository(db *pgxpool.Pool) domain.ProductRepository {
	return &productRepository{db: db}
}

// list products with filters, sorting and pagination
func (r *productRepository) List(ctx context.Context, f domain.ProductFilter) ([]domain.Product, int, error) {
	// build where clause with positional args
	var (
		conds []string
		args  []any
	)
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		conds = append(conds, fmt.Sprintf("(name ILIKE $%d OR sku ILIKE $%d)", len(args), len(args)))
	}
	if f.Category != "" {
		args = append(args, f.Category)
		conds = append(conds, fmt.Sprintf("category = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, f.Status)
		conds = append(conds, fmt.Sprintf("status = $%d", len(args)))
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}

	// total count for pagination
	var total int
	if err := r.db.QueryRow(ctx, "SELECT COUNT(*) FROM products"+where, args...).Scan(&total); err != nil {
		return nil, 0, mapError(err)
	}

	// order by whitelisted column, id as tie-breaker for stable paging
	col, ok := productSortColumns[f.SortBy]
	if !ok {
		col = "created_at"
	}
	dir := "ASC"
	if f.SortDesc {
		dir = "DESC"
	}

	// fetch page
	args = append(args, f.Limit, f.Offset)
	q := fmt.Sprintf("SELECT %s FROM products%s ORDER BY %s %s, id %s LIMIT $%d OFFSET $%d",
		productColumns, where, col, dir, dir, len(args)-1, len(args))
	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, 0, mapError(err)
	}
	items, err := pgx.CollectRows(rows, scanProduct)
	if err != nil {
		return nil, 0, mapError(err)
	}
	return items, total, nil
}

// get product by id
func (r *productRepository) GetByID(ctx context.Context, id string) (*domain.Product, error) {
	rows, err := r.db.Query(ctx, "SELECT "+productColumns+" FROM products WHERE id = $1", id)
	if err != nil {
		return nil, mapError(err)
	}
	p, err := pgx.CollectExactlyOneRow(rows, scanProduct)
	if err != nil {
		return nil, mapError(err)
	}
	return &p, nil
}

// insert product
func (r *productRepository) Create(ctx context.Context, p *domain.Product) error {
	const q = `INSERT INTO products (id, sku, name, description, category, price, stock, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`
	_, err := r.db.Exec(ctx, q, p.ID, p.SKU, p.Name, p.Description, p.Category, p.Price, p.Stock, p.Status, p.CreatedAt, p.UpdatedAt)
	return mapError(err)
}

// replace all editable fields
func (r *productRepository) Update(ctx context.Context, p *domain.Product) error {
	const q = `UPDATE products SET sku = $2, name = $3, description = $4, category = $5,
		price = $6, stock = $7, status = $8, updated_at = $9 WHERE id = $1`
	tag, err := r.db.Exec(ctx, q, p.ID, p.SKU, p.Name, p.Description, p.Category, p.Price, p.Stock, p.Status, p.UpdatedAt)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// update a single column and return the fresh row
func (r *productRepository) UpdateStatus(ctx context.Context, id, status string, updatedAt time.Time) (*domain.Product, error) {
	q := "UPDATE products SET status = $2, updated_at = $3 WHERE id = $1 RETURNING " + productColumns
	rows, err := r.db.Query(ctx, q, id, status, updatedAt)
	if err != nil {
		return nil, mapError(err)
	}
	p, err := pgx.CollectExactlyOneRow(rows, scanProduct)
	if err != nil {
		return nil, mapError(err)
	}
	return &p, nil
}

// delete product
func (r *productRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, "DELETE FROM products WHERE id = $1", id)
	if err != nil {
		return mapError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// insert or update by sku; xmax = 0 means the row was freshly inserted
func (r *productRepository) UpsertBySKU(ctx context.Context, p *domain.Product) (bool, error) {
	const q = `INSERT INTO products (id, sku, name, description, category, price, stock, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (sku) DO UPDATE SET
			name = EXCLUDED.name, description = EXCLUDED.description, category = EXCLUDED.category,
			price = EXCLUDED.price, stock = EXCLUDED.stock, status = EXCLUDED.status, updated_at = EXCLUDED.updated_at
		RETURNING (xmax = 0)`
	var created bool
	err := r.db.QueryRow(ctx, q, p.ID, p.SKU, p.Name, p.Description, p.Category, p.Price, p.Stock, p.Status, p.CreatedAt, p.UpdatedAt).Scan(&created)
	return created, mapError(err)
}

// scan a row into product entity
func scanProduct(row pgx.CollectableRow) (domain.Product, error) {
	var p domain.Product
	err := row.Scan(&p.ID, &p.SKU, &p.Name, &p.Description, &p.Category, &p.Price, &p.Stock, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	return p, err
}
