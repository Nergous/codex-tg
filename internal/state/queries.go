package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Nergous/codex-tg/internal/models"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var ErrNotFound = errors.New("state not found")
var ErrQueueEmpty = errors.New("queue empty")

const updateOffsetKey = "telegram_update_offset"

const sqliteLockRetryTimeout = 5 * time.Second
const maxSQLiteLockRetryDelay = 16 * time.Millisecond

const (
	selectProjectsQuery = `SELECT name, path, enabled FROM projects ORDER BY name ASC`
	putProjectQuery     = `
		INSERT INTO projects (path, name, enabled) VALUES (?, ?, 1)
		ON CONFLICT(path) DO UPDATE SET
			name = excluded.name,
			enabled = 1
	`

	deactivateProjectSessionsQuery = `UPDATE sessions SET active = 0 WHERE project_path = ? AND active = 1`
	putActiveSessionQuery          = `
		INSERT INTO sessions (thread_id, project_path, active, updated_at)
		VALUES (?, ?, 1, ?)
		ON CONFLICT(thread_id) DO UPDATE SET
			project_path = excluded.project_path,
			active = 1,
			updated_at = excluded.updated_at
	`
	selectActiveSessionQuery = `SELECT project_path, thread_id, active FROM sessions WHERE project_path = ? AND active = 1`

	enqueueMessageQuery = `
		INSERT INTO queued_messages (thread_id, chat_id, text, created_at)
		VALUES (?, ?, ?, ?)
	`
	selectQueuedMessageQuery = `
		SELECT id, thread_id, chat_id, text, created_at
		FROM queued_messages
		WHERE thread_id = ?
		ORDER BY id ASC
		LIMIT 1
	`
	deleteQueuedMessageQuery = `DELETE FROM queued_messages WHERE id = ?`

	saveUpdateOffsetQuery = `
		INSERT INTO bot_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`
	selectUpdateOffsetQuery = `SELECT value FROM bot_state WHERE key = ?`
)

func (s *Store) PutProject(ctx context.Context, p *models.Project) error {
	res, err := s.db.ExecContext(ctx, putProjectQuery, p.Path, p.Name)
	if err != nil {
		return fmt.Errorf("put project: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("put project rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("put project: no rows affected")
	}

	return nil
}

func (s *Store) ListProjects(ctx context.Context) (projects []models.Project, err error) {
	rows, err := s.db.QueryContext(ctx, selectProjectsQuery)
	if err != nil {
		return nil, fmt.Errorf("list projects: query: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("list projects: close rows: %w", closeErr)
		}
	}()

	for rows.Next() {
		var p models.Project
		if err := rows.Scan(&p.Name, &p.Path, &p.Enabled); err != nil {
			return nil, fmt.Errorf("list projects: scan: %w", err)
		}
		projects = append(projects, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list projects: iterate: %w", err)
	}
	return projects, nil
}

func (s *Store) SetActiveSession(ctx context.Context, session *models.Session) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin set active session: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	if _, err = tx.ExecContext(ctx, deactivateProjectSessionsQuery, session.ProjectPath); err != nil {
		return fmt.Errorf("deactivate project sessions: %w", err)
	}

	res, err := tx.ExecContext(
		ctx,
		putActiveSessionQuery,
		session.ThreadID,
		session.ProjectPath,
		time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("put active session: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("put active session rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("put active session: no rows affected")
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit active session: %w", err)
	}
	return nil
}

func (s *Store) ActiveSession(ctx context.Context, projectPath string) (models.Session, error) {
	var session models.Session
	if err := s.db.QueryRowContext(ctx, selectActiveSessionQuery, projectPath).Scan(
		&session.ProjectPath,
		&session.ThreadID,
		&session.Active,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.Session{}, fmt.Errorf("select active session: %w", ErrNotFound)
		}
		return models.Session{}, fmt.Errorf("select active session: %w", err)
	}

	return session, nil
}

func (s *Store) Enqueue(ctx context.Context, message models.QueuedMessage) error {
	res, err := s.db.ExecContext(
		ctx,
		enqueueMessageQuery,
		message.ThreadID,
		message.ChatID,
		message.Text,
		time.Now().Unix(),
	)
	if err != nil {
		return fmt.Errorf("enqueue message: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("enqueue message rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("enqueue message: no rows affected")
	}
	return nil
}

func (s *Store) Dequeue(ctx context.Context, threadID string) (_ models.QueuedMessage, err error) {
	deadline := time.Now().Add(sqliteLockRetryTimeout)
	delay := time.Millisecond
	for {
		message, err := s.dequeueOnce(ctx, threadID)
		if err == nil || !isSQLiteLockError(err) {
			return message, err
		}
		if time.Now().After(deadline) {
			return models.QueuedMessage{}, err
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return models.QueuedMessage{}, fmt.Errorf("dequeue message: wait for lock: %w", ctx.Err())
		case <-timer.C:
		}
		if delay < maxSQLiteLockRetryDelay {
			delay *= 2
		}
	}
}

func (s *Store) dequeueOnce(ctx context.Context, threadID string) (_ models.QueuedMessage, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return models.QueuedMessage{}, fmt.Errorf("dequeue message: begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var message models.QueuedMessage
	if err = tx.QueryRowContext(ctx, selectQueuedMessageQuery, threadID).Scan(
		&message.ID,
		&message.ThreadID,
		&message.ChatID,
		&message.Text,
		&message.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return models.QueuedMessage{}, fmt.Errorf("dequeue message: %w", ErrQueueEmpty)
		}
		return models.QueuedMessage{}, fmt.Errorf("dequeue message: select: %w", err)
	}

	res, err := tx.ExecContext(ctx, deleteQueuedMessageQuery, message.ID)
	if err != nil {
		return models.QueuedMessage{}, fmt.Errorf("dequeue message: delete: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return models.QueuedMessage{}, fmt.Errorf("dequeue message: delete rows affected: %w", err)
	}
	if rows != 1 {
		return models.QueuedMessage{}, fmt.Errorf("dequeue message: deleted %d rows, want 1", rows)
	}

	if err = tx.Commit(); err != nil {
		return models.QueuedMessage{}, fmt.Errorf("dequeue message: commit: %w", err)
	}
	return message, nil
}

func isSQLiteLockError(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	primaryCode := sqliteErr.Code() & 0xff
	return primaryCode == sqlite3.SQLITE_BUSY || primaryCode == sqlite3.SQLITE_LOCKED
}

func (s *Store) SaveUpdateOffset(ctx context.Context, offset int64) error {
	res, err := s.db.ExecContext(ctx, saveUpdateOffsetQuery, updateOffsetKey, offset)
	if err != nil {
		return fmt.Errorf("save update offset: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("save update offset rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("save update offset: no rows affected")
	}
	return nil
}

func (s *Store) UpdateOffset(ctx context.Context) (int64, error) {
	var offset int64
	if err := s.db.QueryRowContext(ctx, selectUpdateOffsetQuery, updateOffsetKey).Scan(&offset); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("load update offset: %w", err)
	}
	return offset, nil
}
