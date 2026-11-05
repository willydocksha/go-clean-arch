package usecase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/willydocksha/go-clean-arch/internal/domain"
	"github.com/willydocksha/go-clean-arch/internal/usecase"
	"github.com/willydocksha/go-clean-arch/pkg/spreadsheet"
)

// ---------- fakes ----------

// in-memory product repository that counts GetByID calls
type fakeProductRepo struct {
	mu     sync.Mutex
	items  map[string]domain.Product
	gets   int
	failOn string // sku that makes UpsertBySKU fail
}

func newFakeProductRepo() *fakeProductRepo {
	return &fakeProductRepo{items: map[string]domain.Product{}}
}

// list sorted by sku, filtered by status only (enough for tests)
func (r *fakeProductRepo) List(_ context.Context, f domain.ProductFilter) ([]domain.Product, int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var all []domain.Product
	for _, p := range r.items {
		if f.Status == "" || p.Status == f.Status {
			all = append(all, p)
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].SKU < all[j].SKU })
	start := min(f.Offset, len(all))
	end := min(start+f.Limit, len(all))
	return all[start:end], len(all), nil
}

// get by id and count calls
func (r *fakeProductRepo) GetByID(_ context.Context, id string) (*domain.Product, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gets++
	p, ok := r.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return &p, nil
}

// insert with unique sku
func (r *fakeProductRepo) Create(_ context.Context, p *domain.Product) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.items {
		if e.SKU == p.SKU {
			return domain.ErrConflict
		}
	}
	r.items[p.ID] = *p
	return nil
}

// replace with unique sku
func (r *fakeProductRepo) Update(_ context.Context, p *domain.Product) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[p.ID]; !ok {
		return domain.ErrNotFound
	}
	for id, e := range r.items {
		if e.SKU == p.SKU && id != p.ID {
			return domain.ErrConflict
		}
	}
	r.items[p.ID] = *p
	return nil
}

// update status only
func (r *fakeProductRepo) UpdateStatus(_ context.Context, id, status string, at time.Time) (*domain.Product, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	p.Status, p.UpdatedAt = status, at
	r.items[id] = p
	return &p, nil
}

// delete by id
func (r *fakeProductRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(r.items, id)
	return nil
}

// upsert keyed by sku
func (r *fakeProductRepo) UpsertBySKU(_ context.Context, p *domain.Product) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if p.SKU == r.failOn {
		return false, errors.New("boom")
	}
	for id, e := range r.items {
		if e.SKU == p.SKU {
			p.ID, p.CreatedAt = id, e.CreatedAt
			r.items[id] = *p
			return false, nil
		}
	}
	r.items[p.ID] = *p
	return true, nil
}

// map-backed cache storing json like redis would
type fakeCache struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newFakeCache() *fakeCache { return &fakeCache{data: map[string][]byte{}} }

func (c *fakeCache) Get(_ context.Context, key string, dst any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, ok := c.data[key]
	if !ok {
		return errors.New("miss")
	}
	return json.Unmarshal(raw, dst)
}

func (c *fakeCache) Set(_ context.Context, key string, v any, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, _ := json.Marshal(v)
	c.data[key] = raw
	return nil
}

func (c *fakeCache) Delete(_ context.Context, keys ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range keys {
		delete(c.data, k)
	}
	return nil
}

// ---------- helpers ----------

func ptr[T any](v T) *T { return &v }

// valid input with overridable sku
func validInput(sku string) domain.ProductInput {
	return domain.ProductInput{SKU: sku, Name: "Kopi", Category: "Food", Price: ptr(int64(18000)), Stock: ptr(5)}
}

// ---------- tests ----------

// create normalizes fields and rejects bad input / duplicate sku
func TestProductCreate(t *testing.T) {
	uc := usecase.NewProductUsecase(newFakeProductRepo(), newFakeCache(), time.Second)
	ctx := context.Background()

	p, err := uc.Create(ctx, validInput(" prd-1 "))
	if err != nil {
		t.Fatal(err)
	}
	if p.SKU != "PRD-1" || p.Category != "food" || p.Status != domain.ProductStatusActive {
		t.Fatalf("not normalized: %+v", p)
	}

	// duplicate sku
	if _, err := uc.Create(ctx, validInput("PRD-1")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}

	// every missing field is reported at once
	_, err = uc.Create(ctx, domain.ProductInput{})
	if !errors.Is(err, domain.ErrInvalidInput) || !strings.Contains(err.Error(), "price") || !strings.Contains(err.Error(), "sku") {
		t.Fatalf("want aggregated validation error, got %v", err)
	}
}

// detail is cached and invalidated on write
func TestProductCacheAside(t *testing.T) {
	repo := newFakeProductRepo()
	uc := usecase.NewProductUsecase(repo, newFakeCache(), time.Second)
	ctx := context.Background()
	p, _ := uc.Create(ctx, validInput("PRD-1"))

	// two reads, one db hit
	_, _ = uc.GetByID(ctx, p.ID)
	_, _ = uc.GetByID(ctx, p.ID)
	if repo.gets != 1 {
		t.Fatalf("want 1 db read, got %d", repo.gets)
	}

	// status change must invalidate cache
	if _, err := uc.UpdateStatus(ctx, p.ID, "INACTIVE"); err != nil {
		t.Fatal(err)
	}
	got, _ := uc.GetByID(ctx, p.ID)
	if got.Status != domain.ProductStatusInactive {
		t.Fatalf("stale cache: %+v", got)
	}
}

// put replaces fields and keeps created_at
func TestProductReplace(t *testing.T) {
	uc := usecase.NewProductUsecase(newFakeProductRepo(), newFakeCache(), time.Second)
	ctx := context.Background()
	a, _ := uc.Create(ctx, validInput("A"))
	_, _ = uc.Create(ctx, validInput("B"))

	// valid replace
	in := validInput("A")
	in.Name, in.Price = "Kopi Susu", ptr(int64(22000))
	got, err := uc.Replace(ctx, a.ID, in)
	if err != nil || got.Name != "Kopi Susu" || !got.CreatedAt.Equal(a.CreatedAt) {
		t.Fatalf("replace failed: %v %+v", err, got)
	}

	// taking another sku conflicts
	if _, err := uc.Replace(ctx, a.ID, validInput("B")); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}

	// invalid status on patch
	if _, err := uc.UpdateStatus(ctx, a.ID, "archived"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("want invalid input, got %v", err)
	}
}

// export then import the same file updates rows; bad rows are reported
func TestProductExportImport(t *testing.T) {
	repo := newFakeProductRepo()
	uc := usecase.NewProductUsecase(repo, newFakeCache(), time.Second)
	ctx := context.Background()
	for _, sku := range []string{"A", "B", "C"} {
		_, _ = uc.Create(ctx, validInput(sku))
	}

	// export xlsx and read it back
	file, err := uc.Export(ctx, domain.ProductFilter{}, "xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(file.Name, ".xlsx") {
		t.Fatalf("bad filename %q", file.Name)
	}
	table, _ := spreadsheet.Read(bytes.NewReader(file.Data), spreadsheet.XLSX)
	if len(table.Rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(table.Rows))
	}

	// csv import: 1 update, 1 create, 1 invalid, 1 repo failure
	repo.failOn = "E"
	csv := "sku,name,category,price,stock\nA,Kopi Baru,food,\"20.000\",1\nD,Teh,food,8000,2\nX,,food,abc,1\nE,Gula,food,1000,1\n"
	res, err := uc.Import(ctx, strings.NewReader(csv), "products.csv")
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 || res.Updated != 1 || res.Failed != 2 || res.Errors[0].Row != 4 {
		t.Fatalf("unexpected import result: %+v", res)
	}

	// missing required column
	if _, err := uc.Import(ctx, strings.NewReader("sku,name\nA,B\n"), "x.csv"); !errors.Is(err, domain.ErrInvalidInput) {
		t.Fatalf("want invalid input, got %v", err)
	}
}
