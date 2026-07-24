package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"

	_ "modernc.org/sqlite"
)

var ErrUnsupportedSchemaVersion = errors.New("unsupported schema version")
var ErrMigrationGap = errors.New("migration gap")

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", normalizeDSN(dsn))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	success := false
	defer func() {
		if !success {
			_ = db.Close()
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping database: %w", err)
	}

	databaseVersion, err := readDatabaseVersion(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("read schema version: %w", err)
	}

	currentSchemaVersion := readSchemaVersion()

	if databaseVersion > currentSchemaVersion {
		return nil, fmt.Errorf(
			"%w: database=%d, supported=%d",
			ErrUnsupportedSchemaVersion,
			databaseVersion,
			currentSchemaVersion,
		)
	}

	if err := migrate(databaseVersion, currentSchemaVersion, db); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	success = true
	return &Store{db: db}, nil
}

func normalizeDSN(dsn string) string {
	normalized := strings.TrimPrefix(dsn, "sqlite:///")
	base, rawQuery, _ := strings.Cut(normalized, "?")
	query, err := url.ParseQuery(rawQuery)
	if err != nil {
		return normalized
	}

	setSQLitePragma(query, "foreign_keys", "foreign_keys(1)")
	setSQLitePragma(query, "busy_timeout", "busy_timeout(5000)")
	query.Set("_txlock", "immediate")

	return base + "?" + query.Encode()
}

func setSQLitePragma(query url.Values, name, value string) {
	prefix := strings.ToLower(name)
	pragmas := query["_pragma"]
	kept := pragmas[:0]
	for _, pragma := range pragmas {
		normalized := strings.ToLower(strings.TrimSpace(pragma))
		if strings.HasPrefix(normalized, prefix+"(") ||
			strings.HasPrefix(normalized, prefix+"=") {
			continue
		}
		kept = append(kept, pragma)
	}
	query["_pragma"] = append(kept, value)
}

func readDatabaseVersion(ctx context.Context, db *sql.DB) (int, error) {
	var tableExists bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM sqlite_schema
			WHERE type = 'table' AND name = 'schema_version'
		)
	`).Scan(&tableExists); err != nil {
		return 0, fmt.Errorf("check schema version table: %w", err)
	}
	if !tableExists {
		return 0, nil
	}

	var version int
	if err := db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&version); err != nil {
		return 0, fmt.Errorf("query schema version: %w", err)
	}
	return version, nil
}

type Store struct {
	db *sql.DB
}

func migrate(schemaVersion, currentSchemaVersion int, db *sql.DB) error {
	for _, m := range migrations {
		if m.version <= schemaVersion {
			continue
		}

		if m.version != schemaVersion+1 {
			return ErrMigrationGap
		}

		if err := applyMigration(db, m); err != nil {
			return err
		}
		schemaVersion = m.version
	}
	return nil
}

func applyMigration(db *sql.DB, m migration) (err error) {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.Exec(m.sql); err != nil {
		return fmt.Errorf("exec migration: %w", err)
	}

	if _, err = tx.Exec(
		"UPDATE schema_version SET version = ?",
		m.version,
	); err != nil {
		return fmt.Errorf("update schema version: %w", err)
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.db.Close()
}
