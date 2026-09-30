package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/kaziwise/kaziwise_backend/internal/config"
	"github.com/kaziwise/kaziwise_backend/migrations"
)

// RunMigrations applies every .sql file in cfg.MigrationDir (or the copy
// embedded in the binary when the directory is absent) exactly once,
// tracked in the schema_migrations table. Files run in filename order.
func RunMigrations(ctx context.Context, db *DB, cfg *config.Config) error {
	if _, err := db.pool.Exec(ctx, `
		create table if not exists schema_migrations (
			filename   text primary key,
			checksum   text not null,
			applied_at timestamptz not null default now()
		)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	files, checksums, err := loadMigrations(cfg.MigrationDir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}

	applied := map[string]bool{}
	rows, err := db.pool.Query(ctx, `select filename, checksum from schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name, sum string
		if err := rows.Scan(&name, &sum); err != nil {
			rows.Close()
			return err
		}
		applied[name] = true
		// The guard covers the files this run owns. Rows it does not know
		// about - the "seed:" entries written by SeedDemo, or a migration
		// dropped from the directory after release - are left alone.
		if want, ok := checksums[name]; ok && sum != want {
			rows.Close()
			return fmt.Errorf("migration %s changed after it was applied "+
				"(recorded %s, on disk %s); create a new migration instead", name, sum, want)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, name := range files {
		if applied[name] {
			continue
		}
		body, err := migrationBody(cfg.MigrationDir, name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if err := applyOne(ctx, db, name, body, checksums[name]); err != nil {
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}
	return nil
}

// applyOne runs one migration. The whole file is executed inside a
// transaction so a failure leaves no partial schema. Because the seed
// script uses `do $$ ... $$` blocks with their own semantics, the file is
// executed as one multi-statement command.
func applyOne(ctx context.Context, db *DB, name string, body []byte, checksum string) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	if _, err := tx.Exec(ctx, string(body)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`insert into schema_migrations (filename, checksum) values ($1, $2)`,
		name, checksum); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// loadMigrations returns the migration filenames in execution order plus
// a checksum per file. Files come from the on-disk directory when it
// exists, otherwise from the copy embedded in the binary.
func loadMigrations(dir string) ([]string, map[string]string, error) {
	names, err := listMigrationFiles(dir)
	if err != nil {
		return nil, nil, err
	}
	sums := map[string]string{}
	for _, name := range names {
		b, err := migrationBody(dir, name)
		if err != nil {
			return nil, nil, err
		}
		sums[name] = checksum(b)
	}
	return names, sums, nil
}

func listMigrationFiles(dir string) ([]string, error) {
	if entries, err := os.ReadDir(dir); err == nil {
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		return names, nil
	}
	entries, err := migrations.FS.ReadDir(migrations.Dir)
	if err != nil {
		return nil, fmt.Errorf("no migrations found: %w", err)
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func migrationBody(dir, name string) ([]byte, error) {
	if b, err := readFileFromDir(dir, name); err == nil {
		return b, nil
	}
	return migrations.FS.ReadFile(filepath.Join(migrations.Dir, name))
}

func readFileFromDir(dir, name string) ([]byte, error) {
	return os.ReadFile(filepath.Join(dir, name))
}

// checksum is a stable, dependency-free FNV-1a 64 hash.
func checksum(b []byte) string {
	const (
		offset = 14695981039346656037
		prime  = 1099511628211
	)
	var h uint64 = offset
	for _, c := range b {
		h ^= uint64(c)
		h *= prime
	}
	return fmt.Sprintf("%016x-%d", h, len(b))
}

// MigrationDirExists reports whether migrations are on disk (as opposed
// to only being embedded), so boot can log which source was used.
func MigrationDirExists(dir string) bool {
	_, err := readFileFromDir(dir, "0001_init.sql")
	return err == nil
}

// SeedDemo applies the demo content in migrations/seeds exactly once.
//
// It is a separate entry point from RunMigrations on purpose: a real
// organisation's database must not gain example employees and courses
// because someone forgot to turn a flag off. The demo rows are tagged
// is_demo so a later report or cleanup can find them.
func SeedDemo(ctx context.Context, db *DB, cfg *config.Config) error {
	dir := filepath.Join(cfg.MigrationDir, migrations.SeedDir)
	files, sums, err := loadSeedMigrations(dir)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return nil
	}
	applied := map[string]bool{}
	rows, err := db.pool.Query(ctx, `select filename from schema_migrations`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		applied[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, name := range files {
		if applied["seed:"+name] {
			continue
		}
		body, err := seedBody(dir, name)
		if err != nil {
			return fmt.Errorf("read seed %s: %w", name, err)
		}
		// The seed is tracked under a "seed:" prefix so a schema file and
		// a seed file can never collide in schema_migrations.
		if err := applyOne(ctx, db, "seed:"+name, body, sums[name]); err != nil {
			return fmt.Errorf("apply seed %s: %w", name, err)
		}
	}
	return nil
}

func loadSeedMigrations(dir string) ([]string, map[string]string, error) {
	names, err := listSeedFiles(dir)
	if err != nil {
		return nil, nil, err
	}
	sums := map[string]string{}
	for _, name := range names {
		b, err := seedBody(dir, name)
		if err != nil {
			return nil, nil, err
		}
		sums[name] = checksum(b)
	}
	return names, sums, nil
}

func listSeedFiles(dir string) ([]string, error) {
	var names []string
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		return names, nil
	}
	entries, err := migrations.SeedFS.ReadDir(migrations.SeedDir)
	if err != nil {
		// No seeds bundled is not an error: a deployment may ship without
		// any demo content at all.
		return nil, nil
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func seedBody(dir, name string) ([]byte, error) {
	if b, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
		return b, nil
	}
	return migrations.SeedFS.ReadFile(filepath.Join(migrations.SeedDir, name))
}
