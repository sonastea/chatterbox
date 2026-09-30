package database

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

type Config struct {
	Driver string
	URL    string
}

type Dialect string

const (
	SQLite   Dialect = "sqlite"
	Postgres Dialect = "postgres"
)

// Table qualifies a static application table name for the selected dialect.
func (d Dialect) Table(name string) string {
	quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
	if d == Postgres {
		return "chatterbox." + quoted
	}
	return quoted
}

type DB struct {
	*sql.DB
	Dialect Dialect
}

//go:embed migrations/*.sql
var migrations embed.FS

// Open connects and initializes the schema. SQLite is the zero-configuration
// default; PostgreSQL connects to an existing database without CREATE DATABASE
// privileges. Schemas are embedded so startup does not depend on the working directory.
func Open(ctx context.Context, cfg Config) (*DB, error) {
	driver := strings.ToLower(strings.TrimSpace(cfg.Driver))
	if driver == "" {
		driver = "sqlite"
		// Preserve existing DATABASE_URL-only PostgreSQL configurations.
		if strings.HasPrefix(cfg.URL, "postgres://") || strings.HasPrefix(cfg.URL, "postgresql://") {
			driver = "postgres"
		}
	}
	var dialect Dialect
	dsn := cfg.URL
	switch driver {
	case "sqlite", "sqlite3":
		driver, dialect = "sqlite", SQLite
		if dsn == "" {
			dsn = "file:chatterbox.db"
		}
		separator := "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
		// Apply these to every connection, including connections reopened by database/sql.
		dsn += separator + "_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	case "postgres", "postgresql", "pgx":
		driver, dialect = "pgx", Postgres
		if dsn == "" {
			return nil, fmt.Errorf("postgres requires DATABASE_URL")
		}
	default:
		return nil, fmt.Errorf("unsupported database driver %q (use sqlite or postgres)", cfg.Driver)
	}

	conn, err := sql.Open(driver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s database: %w", dialect, err)
	}
	if dialect == SQLite {
		// Serialize local writes and keep :memory: on a single connection.
		conn.SetMaxOpenConns(1)
		conn.SetMaxIdleConns(1)
	}
	db := &DB{DB: conn, Dialect: dialect}
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, fmt.Errorf("connect to %s database: %w", dialect, err)
	}
	if err := db.Migrate(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Migrate(ctx context.Context) error {
	schema, err := migrations.ReadFile("migrations/" + string(db.Dialect) + ".sql")
	if err != nil {
		return fmt.Errorf("read database schema: %w", err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration: %w", err)
	}
	defer tx.Rollback()
	if db.Dialect == Postgres {
		// IF NOT EXISTS alone does not protect concurrent PostgreSQL DDL startup.
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(0x4368617474657262)); err != nil {
			return fmt.Errorf("lock migrations: %w", err)
		}
	}
	for _, statement := range strings.Split(string(schema), ";") {
		if strings.TrimSpace(statement) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migrate %s database: %w", db.Dialect, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration: %w", err)
	}
	return nil
}
