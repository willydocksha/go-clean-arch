// package migrator is a tiny sql migration runner for postgres.
// files follow golang-migrate naming: 000001_name.up.sql / 000001_name.down.sql
package migrator

import (
	"context"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// advisory lock id so only one migrator runs at a time
const lockID = 7_311_402_001

// filename pattern: <version>_<name>.<up|down>.sql
var filePattern = regexp.MustCompile(`^(\d+)_(.+)\.(up|down)\.sql$`)

// a single migration step
type Migration struct {
	Version int64
	Name    string
	Up      string
	Down    string
}

// migration with its applied state
type Status struct {
	Version   int64
	Name      string
	AppliedAt *time.Time
}

// migration runner
type Migrator struct {
	pool       *pgxpool.Pool
	migrations []Migration
}

// load migrations from fs and build a runner
func New(pool *pgxpool.Pool, fsys fs.FS) (*Migrator, error) {
	migs, err := load(fsys)
	if err != nil {
		return nil, err
	}
	return &Migrator{pool: pool, migrations: migs}, nil
}

// parse and sort migration files
func load(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}

	// group up/down files by version
	byVersion := map[int64]*Migration{}
	for _, e := range entries {
		m := filePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		version, _ := strconv.ParseInt(m[1], 10, 64)
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, err
		}
		mig := byVersion[version]
		if mig == nil {
			mig = &Migration{Version: version, Name: m[2]}
			byVersion[version] = mig
		}
		if m[3] == "up" {
			mig.Up = string(body)
		} else {
			mig.Down = string(body)
		}
	}

	// sort ascending by version
	out := make([]Migration, 0, len(byVersion))
	for _, m := range byVersion {
		if m.Up == "" {
			return nil, fmt.Errorf("migration %d_%s has no up file", m.Version, m.Name)
		}
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// apply all pending migrations
func (m *Migrator) Up(ctx context.Context) ([]Migration, error) {
	var applied []Migration
	err := m.withLock(ctx, func(conn *pgxpool.Conn, done map[int64]time.Time) error {
		for _, mig := range m.migrations {
			if _, ok := done[mig.Version]; ok {
				continue
			}
			// run sql and record version in one transaction
			err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, mig.Up); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, mig.Version, mig.Name)
				return err
			})
			if err != nil {
				return fmt.Errorf("migrate up %d_%s: %w", mig.Version, mig.Name, err)
			}
			applied = append(applied, mig)
		}
		return nil
	})
	return applied, err
}

// roll back the last n applied migrations
func (m *Migrator) Down(ctx context.Context, steps int) ([]Migration, error) {
	var reverted []Migration
	err := m.withLock(ctx, func(conn *pgxpool.Conn, done map[int64]time.Time) error {
		for i := len(m.migrations) - 1; i >= 0 && len(reverted) < steps; i-- {
			mig := m.migrations[i]
			if _, ok := done[mig.Version]; !ok {
				continue
			}
			// run down sql and delete version in one transaction
			err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
				if _, err := tx.Exec(ctx, mig.Down); err != nil {
					return err
				}
				_, err := tx.Exec(ctx, `DELETE FROM schema_migrations WHERE version = $1`, mig.Version)
				return err
			})
			if err != nil {
				return fmt.Errorf("migrate down %d_%s: %w", mig.Version, mig.Name, err)
			}
			reverted = append(reverted, mig)
		}
		return nil
	})
	return reverted, err
}

// list every migration with its applied time
func (m *Migrator) Status(ctx context.Context) ([]Status, error) {
	var out []Status
	err := m.withLock(ctx, func(_ *pgxpool.Conn, done map[int64]time.Time) error {
		for _, mig := range m.migrations {
			s := Status{Version: mig.Version, Name: mig.Name}
			if at, ok := done[mig.Version]; ok {
				s.AppliedAt = &at
			}
			out = append(out, s)
		}
		return nil
	})
	return out, err
}

// take advisory lock, ensure bookkeeping table, load applied versions, run fn
func (m *Migrator) withLock(ctx context.Context, fn func(*pgxpool.Conn, map[int64]time.Time) error) error {
	conn, err := m.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()

	// serialize concurrent runners
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, lockID); err != nil {
		return err
	}
	defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, lockID) //nolint:errcheck

	// bookkeeping table
	const ddl = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    BIGINT PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`
	if _, err := conn.Exec(ctx, ddl); err != nil {
		return err
	}

	// read applied versions
	rows, err := conn.Query(ctx, `SELECT version, applied_at FROM schema_migrations`)
	if err != nil {
		return err
	}
	done := map[int64]time.Time{}
	for rows.Next() {
		var v int64
		var at time.Time
		if err := rows.Scan(&v, &at); err != nil {
			rows.Close()
			return err
		}
		done[v] = at
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return fn(conn, done)
}
