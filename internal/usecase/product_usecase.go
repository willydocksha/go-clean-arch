package usecase

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/willydocksha/go-clean-arch/internal/domain"
	"github.com/willydocksha/go-clean-arch/pkg/spreadsheet"
)

// export / import limits
const (
	exportPageSize    = 1000
	exportMaxRows     = 50_000
	importMaxRows     = 5_000
	importMaxErrors   = 100
	productCacheTTL   = 10 * time.Minute
	productCacheTag   = "product:"
	productTimeFormat = time.RFC3339
)

// columns used by export, import and template (import ignores id and timestamps)
var productExportHeaders = []string{"id", "sku", "name", "description", "category", "price", "stock", "status", "created_at", "updated_at"}
var productImportHeaders = []string{"sku", "name", "description", "category", "price", "stock", "status"}

// product usecase
type productUsecase struct {
	repo    domain.ProductRepository
	cache   domain.Cache
	timeout time.Duration
	now     func() time.Time
}

// constructor; pass cache.Noop{} when redis is not configured
func NewProductUsecase(repo domain.ProductRepository, cache domain.Cache, timeout time.Duration) domain.ProductUsecase {
	return &productUsecase{repo: repo, cache: cache, timeout: timeout, now: time.Now}
}

// list products with normalized filter
func (u *productUsecase) List(ctx context.Context, f domain.ProductFilter) ([]domain.Product, int, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	if err := normalizeFilter(&f); err != nil {
		return nil, 0, err
	}
	return u.repo.List(ctx, f)
}

// get one product, served from cache when possible
func (u *productUsecase) GetByID(ctx context.Context, id string) (*domain.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	if err := validateID(id); err != nil {
		return nil, err
	}

	// cache hit
	var cached domain.Product
	if err := u.cache.Get(ctx, productCacheTag+id, &cached); err == nil {
		return &cached, nil
	}

	// cache miss: load from db and populate cache
	p, err := u.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	_ = u.cache.Set(ctx, productCacheTag+id, p, productCacheTTL)
	return p, nil
}

// create a product
func (u *productUsecase) Create(ctx context.Context, in domain.ProductInput) (*domain.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	// validate and build entity
	p, err := buildProduct(in)
	if err != nil {
		return nil, err
	}
	now := u.now().UTC()
	p.ID, p.CreatedAt, p.UpdatedAt = uuid.NewString(), now, now

	// persist (unique sku -> ErrConflict)
	if err := u.repo.Create(ctx, p); err != nil {
		return nil, wrapSKUConflict(err, p.SKU)
	}
	return p, nil
}

// replace every field of a product (put)
func (u *productUsecase) Replace(ctx context.Context, id string, in domain.ProductInput) (*domain.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	if err := validateID(id); err != nil {
		return nil, err
	}

	// load to keep created_at and confirm existence
	existing, err := u.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	// validate new state
	p, err := buildProduct(in)
	if err != nil {
		return nil, err
	}
	p.ID, p.CreatedAt, p.UpdatedAt = existing.ID, existing.CreatedAt, u.now().UTC()

	// persist and invalidate cache
	if err := u.repo.Update(ctx, p); err != nil {
		return nil, wrapSKUConflict(err, p.SKU)
	}
	_ = u.cache.Delete(ctx, productCacheTag+id)
	return p, nil
}

// change only the status field (patch)
func (u *productUsecase) UpdateStatus(ctx context.Context, id, status string) (*domain.Product, error) {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	if err := validateID(id); err != nil {
		return nil, err
	}
	status = strings.ToLower(strings.TrimSpace(status))
	if err := validateStatus(status); err != nil {
		return nil, err
	}

	// persist and invalidate cache
	p, err := u.repo.UpdateStatus(ctx, id, status, u.now().UTC())
	if err != nil {
		return nil, err
	}
	_ = u.cache.Delete(ctx, productCacheTag+id)
	return p, nil
}

// delete a product
func (u *productUsecase) Delete(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, u.timeout)
	defer cancel()

	if err := validateID(id); err != nil {
		return err
	}
	if err := u.repo.Delete(ctx, id); err != nil {
		return err
	}
	_ = u.cache.Delete(ctx, productCacheTag+id)
	return nil
}

// export products matching the filter as csv or xlsx
func (u *productUsecase) Export(ctx context.Context, f domain.ProductFilter, format string) (*domain.File, error) {
	// exports can be slow, so allow a longer deadline
	ctx, cancel := context.WithTimeout(ctx, 6*u.timeout)
	defer cancel()

	fmtType, err := spreadsheet.ParseFormat(format)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}
	if err := normalizeFilter(&f); err != nil {
		return nil, err
	}

	// page through all matching rows
	table := spreadsheet.Table{Headers: productExportHeaders}
	f.Limit, f.Offset = exportPageSize, 0
	for {
		items, total, err := u.repo.List(ctx, f)
		if err != nil {
			return nil, err
		}
		if total > exportMaxRows {
			return nil, fmt.Errorf("%w: export is limited to %d rows, narrow the filter", domain.ErrInvalidInput, exportMaxRows)
		}
		for _, p := range items {
			table.Rows = append(table.Rows, productToRow(p))
		}
		f.Offset += len(items)
		if len(items) < exportPageSize || f.Offset >= total {
			break
		}
	}

	// encode file
	data, err := spreadsheet.Encode(fmtType, table)
	if err != nil {
		return nil, err
	}
	return &domain.File{
		Name:        "products_" + u.now().Format("20060102_150405") + fmtType.Ext(),
		ContentType: fmtType.ContentType(),
		Data:        data,
	}, nil
}

// import products from csv/xlsx; rows are upserted by sku
func (u *productUsecase) Import(ctx context.Context, r io.Reader, filename string) (*domain.ImportResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 6*u.timeout)
	defer cancel()

	// parse file
	fmtType, err := spreadsheet.FormatFromFilename(filename)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}
	table, err := spreadsheet.Read(r, fmtType)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}

	// required columns present
	cols := map[string]int{}
	for _, h := range productImportHeaders {
		cols[h] = table.Col(h)
	}
	for _, h := range []string{"sku", "name", "category", "price", "stock"} {
		if cols[h] < 0 {
			return nil, fmt.Errorf("%w: missing column %q", domain.ErrInvalidInput, h)
		}
	}
	if len(table.Rows) > importMaxRows {
		return nil, fmt.Errorf("%w: import is limited to %d rows", domain.ErrInvalidInput, importMaxRows)
	}

	// process each row independently so one bad row does not block the rest
	res := &domain.ImportResult{TotalRows: len(table.Rows)}
	now := u.now().UTC()
	for i, row := range table.Rows {
		rowNum := i + 2
		p, err := rowToProduct(row, cols)
		if err == nil {
			p.ID, p.CreatedAt, p.UpdatedAt = uuid.NewString(), now, now
			var created bool
			if created, err = u.repo.UpsertBySKU(ctx, p); err == nil {
				if created {
					res.Created++
				} else {
					res.Updated++
				}
				continue
			}
		}

		// record failure
		res.Failed++
		if len(res.Errors) < importMaxErrors {
			res.Errors = append(res.Errors, domain.ImportRowError{Row: rowNum, SKU: cell(row, cols["sku"]), Message: err.Error()})
		}
	}

	// updated rows may be cached, drop the whole product cache namespace
	if res.Updated > 0 {
		if pc, ok := u.cache.(interface {
			DeleteByPattern(context.Context, string) (int, error)
		}); ok {
			_, _ = pc.DeleteByPattern(ctx, productCacheTag+"*")
		}
	}
	return res, nil
}

// empty template with one example row
func (u *productUsecase) ImportTemplate(format string) (*domain.File, error) {
	fmtType, err := spreadsheet.ParseFormat(format)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrInvalidInput, err)
	}
	table := spreadsheet.Table{
		Headers: productImportHeaders,
		Rows:    [][]string{{"PRD-9001", "Contoh Produk", "deskripsi opsional", "electronics", "150000", "10", "active"}},
	}
	data, err := spreadsheet.Encode(fmtType, table)
	if err != nil {
		return nil, err
	}
	return &domain.File{Name: "products_template" + fmtType.Ext(), ContentType: fmtType.ContentType(), Data: data}, nil
}

// ---------- helpers ----------

// validate and normalize input into an entity
func buildProduct(in domain.ProductInput) (*domain.Product, error) {
	p := &domain.Product{
		SKU:         strings.ToUpper(strings.TrimSpace(in.SKU)),
		Name:        strings.TrimSpace(in.Name),
		Description: strings.TrimSpace(in.Description),
		Category:    strings.ToLower(strings.TrimSpace(in.Category)),
		Status:      strings.ToLower(strings.TrimSpace(in.Status)),
	}

	// collect every problem so clients can fix them at once
	var problems []string
	switch {
	case p.SKU == "":
		problems = append(problems, "sku is required")
	case len(p.SKU) > 50:
		problems = append(problems, "sku must be at most 50 characters")
	}
	switch {
	case p.Name == "":
		problems = append(problems, "name is required")
	case len(p.Name) > 150:
		problems = append(problems, "name must be at most 150 characters")
	}
	if p.Category == "" {
		problems = append(problems, "category is required")
	}
	if in.Price == nil || *in.Price < 0 {
		problems = append(problems, "price is required and must be >= 0")
	} else {
		p.Price = *in.Price
	}
	if in.Stock == nil || *in.Stock < 0 {
		problems = append(problems, "stock is required and must be >= 0")
	} else {
		p.Stock = *in.Stock
	}
	if p.Status == "" {
		p.Status = domain.ProductStatusActive
	}
	if err := validateStatus(p.Status); err != nil {
		problems = append(problems, "status must be active or inactive")
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidInput, strings.Join(problems, "; "))
	}
	return p, nil
}

// clamp pagination and validate sort / status
func normalizeFilter(f *domain.ProductFilter) error {
	if f.Limit <= 0 {
		f.Limit = defaultLimit
	}
	f.Limit = min(f.Limit, maxLimit)
	f.Offset = max(f.Offset, 0)
	f.Search = strings.TrimSpace(f.Search)
	f.Category = strings.ToLower(strings.TrimSpace(f.Category))
	f.Status = strings.ToLower(strings.TrimSpace(f.Status))
	if f.Status != "" {
		if err := validateStatus(f.Status); err != nil {
			return err
		}
	}
	switch f.SortBy {
	case "", "created_at", "name", "price", "stock":
	default:
		return fmt.Errorf("%w: sort must be one of created_at, name, price, stock", domain.ErrInvalidInput)
	}
	return nil
}

// status must be a known value
func validateStatus(s string) error {
	if s != domain.ProductStatusActive && s != domain.ProductStatusInactive {
		return fmt.Errorf("%w: status must be active or inactive", domain.ErrInvalidInput)
	}
	return nil
}

// id must be a uuid
func validateID(id string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("%w: invalid id", domain.ErrInvalidInput)
	}
	return nil
}

// add sku to conflict errors for a clearer message
func wrapSKUConflict(err error, sku string) error {
	if errors.Is(err, domain.ErrConflict) {
		return fmt.Errorf("%w: sku %q is already used", domain.ErrConflict, sku)
	}
	return err
}

// entity -> export row
func productToRow(p domain.Product) []string {
	return []string{
		p.ID, p.SKU, p.Name, p.Description, p.Category,
		strconv.FormatInt(p.Price, 10), strconv.Itoa(p.Stock), p.Status,
		p.CreatedAt.Format(productTimeFormat), p.UpdatedAt.Format(productTimeFormat),
	}
}

// import row -> validated entity
func rowToProduct(row []string, cols map[string]int) (*domain.Product, error) {
	in := domain.ProductInput{
		SKU:         cell(row, cols["sku"]),
		Name:        cell(row, cols["name"]),
		Description: cell(row, cols["description"]),
		Category:    cell(row, cols["category"]),
		Status:      cell(row, cols["status"]),
	}

	// numbers may come as "150.000" or "150,000" from excel
	clean := strings.NewReplacer(".", "", ",", "", " ", "")
	if v, err := strconv.ParseInt(clean.Replace(cell(row, cols["price"])), 10, 64); err == nil {
		in.Price = &v
	}
	if v, err := strconv.Atoi(clean.Replace(cell(row, cols["stock"]))); err == nil {
		in.Stock = &v
	}
	return buildProduct(in)
}

// safe cell access (-1 means missing column)
func cell(row []string, i int) string {
	if i < 0 || i >= len(row) {
		return ""
	}
	return row[i]
}
