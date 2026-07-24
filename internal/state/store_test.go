package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
)

var testDatabaseID atomic.Uint64

func TestOpen_FreshDatabaseAppliesMigrations(t *testing.T) {
	store := openTestStore(t)

	var version int
	if err := store.db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 1 {
		t.Fatalf("schema version = %d, want 1", version)
	}

	var projectsTableCount int
	if err := store.db.QueryRow(`
		SELECT count(*)
		FROM sqlite_schema
		WHERE type = 'table' AND name = 'projects'
	`).Scan(&projectsTableCount); err != nil {
		t.Fatalf("find projects table: %v", err)
	}
	if projectsTableCount != 1 {
		t.Fatalf("projects table count = %d, want 1", projectsTableCount)
	}
}

func TestOpen_CurrentDatabaseDoesNotReapplyMigrations(t *testing.T) {
	dsn := uniqueTestDSN(t)
	first, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open fresh database: %v", err)
	}
	t.Cleanup(func() {
		if err := first.Close(); err != nil {
			t.Errorf("close first store: %v", err)
		}
	})

	second, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open current database: %v", err)
	}
	t.Cleanup(func() {
		if err := second.Close(); err != nil {
			t.Errorf("close second store: %v", err)
		}
	})
}

func TestOpen_RejectsNewerSchemaVersion(t *testing.T) {
	dsn := uniqueTestDSN(t)
	db := openRawTestDatabase(t, dsn)
	if _, err := db.Exec(`
		CREATE TABLE schema_version (version INTEGER NOT NULL);
		INSERT INTO schema_version(version) VALUES (2);
	`); err != nil {
		t.Fatalf("prepare newer database: %v", err)
	}

	store, err := Open(context.Background(), dsn)
	if store != nil {
		store.Close()
		t.Fatal("Open() returned a store for an unsupported schema version")
	}
	if !errors.Is(err, ErrUnsupportedSchemaVersion) {
		t.Fatalf("Open() error = %v, want ErrUnsupportedSchemaVersion", err)
	}
}

func TestOpen_DoesNotTreatSchemaQueryErrorAsFreshDatabase(t *testing.T) {
	dsn := uniqueTestDSN(t)
	db := openRawTestDatabase(t, dsn)
	if _, err := db.Exec(`
		CREATE TABLE schema_version (version TEXT NOT NULL);
		INSERT INTO schema_version(version) VALUES ('not-a-version');
	`); err != nil {
		t.Fatalf("prepare malformed database: %v", err)
	}

	store, err := Open(context.Background(), dsn)
	if store != nil {
		store.Close()
		t.Fatal("Open() returned a store after a schema query error")
	}
	if err == nil {
		t.Fatal("Open() error = nil, want schema query error")
	}

	var projectsTableCount int
	if queryErr := db.QueryRow(`
		SELECT count(*)
		FROM sqlite_schema
		WHERE type = 'table' AND name = 'projects'
	`).Scan(&projectsTableCount); queryErr != nil {
		t.Fatalf("find projects table: %v", queryErr)
	}
	if projectsTableCount != 0 {
		t.Fatalf("projects table count = %d, want 0", projectsTableCount)
	}
}

func TestOpen_PingsDatabaseWithContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	store, err := Open(ctx, uniqueTestDSN(t))
	if store != nil {
		store.Close()
		t.Fatal("Open() returned a store for a cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Open() error = %v, want context.Canceled", err)
	}
	if !strings.Contains(err.Error(), "ping database") {
		t.Fatalf("Open() error = %q, want ping database context", err)
	}
}

func TestOpen_InvalidDSNFailsDuringPing(t *testing.T) {
	dsn := uniqueTestDSN(t) + "&vfs=missing-test-vfs"

	store, err := Open(context.Background(), dsn)
	if store != nil {
		store.Close()
		t.Fatal("Open() returned a store for an invalid DSN")
	}
	if err == nil {
		t.Fatal("Open() error = nil, want invalid DSN error")
	}
	if !strings.Contains(err.Error(), "ping database") {
		t.Fatalf("Open() error = %q, want ping database context", err)
	}
}

func TestOpen_EnforcesForeignKeysOnPooledConnections(t *testing.T) {
	store := openTestStore(t)
	store.db.SetMaxOpenConns(3)

	connections := make([]*sql.Conn, 0, 3)
	for i := range 3 {
		connection, err := store.db.Conn(context.Background())
		if err != nil {
			t.Fatalf("open pooled connection %d: %v", i, err)
		}
		connections = append(connections, connection)
		t.Cleanup(func() { _ = connection.Close() })
	}

	for i, connection := range connections {
		_, err := connection.ExecContext(context.Background(), `
			INSERT INTO sessions (thread_id, project_path, active, updated_at)
			VALUES (?, '/missing-project', 0, 1)
		`, fmt.Sprintf("orphan-thread-%d", i))
		if err == nil {
			t.Fatalf("connection %d accepted a foreign-key violation", i)
		}
	}
}

func TestOpen_RollsBackFailedMigration(t *testing.T) {
	originalMigrations := migrations
	migrations = []migration{
		{
			version: 1,
			sql: `
				CREATE TABLE schema_version (version INTEGER NOT NULL);
				INSERT INTO schema_version(version) VALUES (0);
				CREATE TABLE committed_migration (id INTEGER PRIMARY KEY);
			`,
		},
		{
			version: 2,
			sql: `
				CREATE TABLE rolled_back_migration (id INTEGER PRIMARY KEY);
				THIS IS NOT VALID SQL;
			`,
		},
	}
	t.Cleanup(func() { migrations = originalMigrations })

	dsn := uniqueTestDSN(t)
	db := openRawTestDatabase(t, dsn)

	store, err := Open(context.Background(), dsn)
	if store != nil {
		store.Close()
		t.Fatal("Open() returned a store after a failed migration")
	}
	if err == nil {
		t.Fatal("Open() error = nil, want migration error")
	}

	var version int
	if err := db.QueryRow(`SELECT version FROM schema_version`).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}
	if version != 1 {
		t.Fatalf("schema version = %d, want 1", version)
	}

	assertTableCount(t, db, "committed_migration", 1)
	assertTableCount(t, db, "rolled_back_migration", 0)
}

func TestOpen_ClosesDatabaseAfterMigrationError(t *testing.T) {
	originalMigrations := migrations
	migrations = []migration{
		{
			version: 1,
			sql: `
				CREATE TABLE schema_version (version INTEGER NOT NULL);
				INSERT INTO schema_version(version) VALUES (0);
			`,
		},
		{
			version: 2,
			sql:     `THIS IS NOT VALID SQL;`,
		},
	}
	t.Cleanup(func() { migrations = originalMigrations })

	dsn := uniqueTestDSN(t)
	store, err := Open(context.Background(), dsn)
	if store != nil {
		store.Close()
		t.Fatal("Open() returned a store after a failed migration")
	}
	if err == nil {
		t.Fatal("Open() error = nil, want migration error")
	}

	// A named in-memory database disappears when its last connection closes.
	// Reopening the URI must therefore produce a fresh database.
	db := openRawTestDatabase(t, dsn)
	assertTableCount(t, db, "schema_version", 0)
}

func openTestStore(t *testing.T) *Store {
	t.Helper()

	store, err := Open(context.Background(), uniqueTestDSN(t))
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close test store: %v", err)
		}
	})

	return store
}

func uniqueTestDSN(t *testing.T) string {
	t.Helper()

	name := strings.NewReplacer("/", "-", `\`, "-", " ", "-").Replace(t.Name())
	return fmt.Sprintf(
		"sqlite:///file:codex-tg-%s-%d?mode=memory&cache=shared",
		name,
		testDatabaseID.Add(1),
	)
}

func openRawTestDatabase(t *testing.T, dsn string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", strings.TrimPrefix(dsn, "sqlite:///"))
	if err != nil {
		t.Fatalf("open raw test database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close raw test database: %v", err)
		}
	})
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatalf("ping raw test database: %v", err)
	}

	return db
}

func assertTableCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()

	var count int
	if err := db.QueryRow(`
		SELECT count(*)
		FROM sqlite_schema
		WHERE type = 'table' AND name = ?
	`, table).Scan(&count); err != nil {
		t.Fatalf("find table %q: %v", table, err)
	}
	if count != want {
		t.Fatalf("table %q count = %d, want %d", table, count, want)
	}
}
