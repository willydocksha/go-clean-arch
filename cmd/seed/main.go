// command seed fills the database with sample users and products.
// it is idempotent: running it twice does not create duplicates.
//
// usage:
//
//	go run ./cmd/seed            insert sample data
//	go run ./cmd/seed -fresh     wipe users & products first, then insert
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/willydocksha/go-clean-arch/internal/config"
	"github.com/willydocksha/go-clean-arch/pkg/database"
)

// number of products to generate
const productCount = 60

// sample users
var users = []struct{ name, email string }{
	{"Admin", "admin@example.com"},
	{"Budi Santoso", "budi@example.com"},
	{"Siti Aminah", "siti@example.com"},
	{"Andi Wijaya", "andi@example.com"},
	{"Dewi Lestari", "dewi@example.com"},
}

// product name parts per category
var catalog = map[string][]string{
	"electronics": {"Wireless Mouse", "Mechanical Keyboard", "USB-C Hub", "Bluetooth Speaker", "Webcam HD", "Power Bank"},
	"books":       {"Belajar Golang", "Clean Architecture", "Design Patterns", "Laskar Pelangi", "Bumi Manusia", "Atomic Habits"},
	"fashion":     {"Kaos Polos", "Kemeja Batik", "Celana Chino", "Jaket Denim", "Topi Baseball", "Sneakers Canvas"},
	"home":        {"Lampu Tidur", "Rak Buku", "Bantal Sofa", "Jam Dinding", "Tanaman Hias", "Gelas Keramik"},
	"sports":      {"Matras Yoga", "Dumbbell 5kg", "Tali Skipping", "Bola Futsal", "Raket Badminton", "Botol Minum"},
}

// fixed order so output is deterministic
var categories = []string{"electronics", "books", "fashion", "home", "sports"}

// variants appended to names
var variants = []string{"Hitam", "Putih", "Biru", "Merah", "Abu-abu", "Hijau"}

// entrypoint
func main() {
	fresh := flag.Bool("fresh", false, "truncate users and products before seeding")
	flag.Parse()

	if err := run(*fresh); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

// connect and seed inside one transaction
func run(fresh bool) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	pool, err := database.NewPostgres(ctx, database.PostgresConfig{URL: cfg.Database.URL, MaxConns: 2, MinConns: 1})
	if err != nil {
		return err
	}
	defer pool.Close()

	return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
		// optional wipe
		if fresh {
			if _, err := tx.Exec(ctx, "TRUNCATE users, products"); err != nil {
				return err
			}
			fmt.Println("truncated users and products")
		}

		// users
		u, err := seedUsers(ctx, tx)
		if err != nil {
			return fmt.Errorf("users: %w", err)
		}

		// products
		p, err := seedProducts(ctx, tx)
		if err != nil {
			return fmt.Errorf("products: %w", err)
		}
		fmt.Printf("seeded %d users and %d products (existing rows skipped)\n", u, p)
		return nil
	})
}

// insert sample users, skipping existing emails
func seedUsers(ctx context.Context, tx pgx.Tx) (int, error) {
	inserted := 0
	now := time.Now().UTC()
	for _, u := range users {
		tag, err := tx.Exec(ctx,
			`INSERT INTO users (id, name, email, created_at, updated_at) VALUES ($1, $2, $3, $4, $4)
			 ON CONFLICT (email) DO NOTHING`,
			uuid.NewString(), u.name, u.email, now)
		if err != nil {
			return inserted, err
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}

// insert generated products, skipping existing skus
func seedProducts(ctx context.Context, tx pgx.Tx) (int, error) {
	// fixed seed -> same data every run
	rng := rand.New(rand.NewPCG(42, 2026))
	inserted := 0
	base := time.Now().UTC().Add(-productCount * time.Hour)

	for i := 1; i <= productCount; i++ {
		// pick category, name and variant
		cat := categories[(i-1)%len(categories)]
		names := catalog[cat]
		name := fmt.Sprintf("%s %s", names[rng.IntN(len(names))], variants[rng.IntN(len(variants))])

		// realistic prices rounded to 500 rupiah
		price := int64(rng.IntN(1_000)+10) * 500
		stock := rng.IntN(200)
		status := "active"
		if i%7 == 0 {
			status = "inactive"
		}
		created := base.Add(time.Duration(i) * time.Hour)

		tag, err := tx.Exec(ctx,
			`INSERT INTO products (id, sku, name, description, category, price, stock, status, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $9)
			 ON CONFLICT (sku) DO NOTHING`,
			uuid.NewString(), fmt.Sprintf("PRD-%04d", i), name,
			fmt.Sprintf("Produk contoh kategori %s nomor %d", cat, i),
			cat, price, stock, status, created)
		if err != nil {
			return inserted, err
		}
		inserted += int(tag.RowsAffected())
	}
	return inserted, nil
}
