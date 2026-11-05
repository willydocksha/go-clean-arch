// command migrate applies the embedded sql migrations.
//
// usage:
//
//	go run ./cmd/migrate up          apply all pending migrations
//	go run ./cmd/migrate down [n]    roll back the last n migrations (default 1)
//	go run ./cmd/migrate status      show applied / pending migrations
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/willydocksha/go-clean-arch/internal/config"
	"github.com/willydocksha/go-clean-arch/migrations"
	"github.com/willydocksha/go-clean-arch/pkg/database"
	"github.com/willydocksha/go-clean-arch/pkg/migrator"
)

// entrypoint
func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

// parse command and run it
func run(args []string) error {
	cmd := "up"
	if len(args) > 0 {
		cmd = args[0]
	}

	// connect to postgres
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

	m, err := migrator.New(pool, migrations.FS)
	if err != nil {
		return err
	}

	// dispatch command
	switch cmd {
	case "up":
		applied, err := m.Up(ctx)
		for _, a := range applied {
			fmt.Printf("applied  %06d_%s\n", a.Version, a.Name)
		}
		if err == nil && len(applied) == 0 {
			fmt.Println("nothing to migrate, database is up to date")
		}
		return err

	case "down":
		steps := 1
		if len(args) > 1 {
			if steps, err = strconv.Atoi(args[1]); err != nil || steps < 1 {
				return fmt.Errorf("invalid step count %q", args[1])
			}
		}
		reverted, err := m.Down(ctx, steps)
		for _, r := range reverted {
			fmt.Printf("reverted %06d_%s\n", r.Version, r.Name)
		}
		return err

	case "status":
		rows, err := m.Status(ctx)
		for _, r := range rows {
			state := "pending"
			if r.AppliedAt != nil {
				state = "applied " + r.AppliedAt.Format("2006-01-02 15:04:05")
			}
			fmt.Printf("%06d_%-40s %s\n", r.Version, r.Name, state)
		}
		return err
	}
	return fmt.Errorf("unknown command %q (use up, down [n], status)", cmd)
}
