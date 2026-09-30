package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSQLite(t *testing.T) {
	for _, dsn := range []string{":memory:", "file::memory:?cache=shared", filepath.Join(t.TempDir(), "chatterbox.db")} {
		t.Run(dsn, func(t *testing.T) {
			db, err := Open(context.Background(), Config{URL: dsn})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if db.Dialect != SQLite {
				t.Fatalf("default dialect = %q", db.Dialect)
			}
			if err := db.Migrate(context.Background()); err != nil {
				t.Fatalf("repeat migration: %v", err)
			}
			var foreignKeys int
			if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
				t.Fatalf("foreign keys = %d, error = %v", foreignKeys, err)
			}
			if _, err := db.Exec(`INSERT INTO "Room"(xid, name, owner_id) VALUES('room', 'room', 'missing')`); err == nil {
				t.Fatal("missing owner should violate foreign key")
			}
			// Force database/sql to reopen the connection. Pragmas must still apply.
			db.SetMaxIdleConns(0)
			if err := db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
				t.Fatalf("foreign keys after reconnect = %d, error = %v", foreignKeys, err)
			}
		})
	}
}

func TestSQLitePersistence(t *testing.T) {
	cfg := Config{URL: filepath.Join(t.TempDir(), "persistent.db")}
	db, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO "User"(xid, name, email, password) VALUES('user', 'name', 'user@example.com', '')`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM "User" WHERE xid = 'user'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("persisted users = %d, error = %v", count, err)
	}
	var mode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode = %q, error = %v", mode, err)
	}
}

func TestInvalidConfig(t *testing.T) {
	for _, cfg := range []Config{{Driver: "unknown"}, {Driver: "postgres"}} {
		if db, err := Open(context.Background(), cfg); err == nil {
			db.Close()
			t.Fatalf("expected error for %+v", cfg)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if db, err := Open(ctx, Config{URL: ":memory:"}); err == nil {
		db.Close()
		t.Fatal("expected cancellation error")
	}
}

func TestConcurrentStartup(t *testing.T) {
	for _, test := range []struct {
		name string
		cfg  Config
	}{
		{"sqlite", Config{URL: filepath.Join(t.TempDir(), "concurrent.db")}},
		// An unset driver must infer PostgreSQL from an existing connection URL.
		{"postgres", Config{URL: os.Getenv("TEST_POSTGRES_URL")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if test.name == "postgres" && test.cfg.URL == "" {
				t.Skip("set TEST_POSTGRES_URL or run scripts/test-integration.sh")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			errors := make(chan error, 6)
			for i := 0; i < 6; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					db, err := Open(ctx, test.cfg)
					if err != nil {
						errors <- err
						return
					}
					defer db.Close()
					if string(db.Dialect) != test.name {
						errors <- fmt.Errorf("inferred dialect = %s, expected %s", db.Dialect, test.name)
					}
				}()
			}
			wg.Wait()
			close(errors)
			for err := range errors {
				t.Error(err)
			}
		})
	}
}
