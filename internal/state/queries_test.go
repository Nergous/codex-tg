package state

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Nergous/codex-tg/internal/models"
)

func TestPutProject_InsertsProject(t *testing.T) {
	store := openTestStore(t)
	project := &models.Project{Name: "demo", Path: "/project/demo", Enabled: true}

	if err := store.PutProject(context.Background(), project); err != nil {
		t.Fatalf("PutProject() error = %v", err)
	}

	var got models.Project
	if err := store.db.QueryRow(`
		SELECT name, path, enabled FROM projects WHERE path = ?
	`, project.Path).Scan(&got.Name, &got.Path, &got.Enabled); err != nil {
		t.Fatalf("read project: %v", err)
	}
	if got != *project {
		t.Fatalf("stored project = %+v, want %+v", got, *project)
	}
}

func TestPutProject_UpdatesAndEnablesExistingProject(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`
		INSERT INTO projects (path, name, enabled)
		VALUES ('/project/demo', 'old-name', 0)
	`); err != nil {
		t.Fatalf("prepare project: %v", err)
	}
	project := &models.Project{Name: "new-name", Path: "/project/demo", Enabled: true}

	if err := store.PutProject(context.Background(), project); err != nil {
		t.Fatalf("PutProject() error = %v", err)
	}

	var got models.Project
	var count int
	if err := store.db.QueryRow(`
		SELECT name, path, enabled, count(*) OVER ()
		FROM projects
		WHERE path = ?
	`, project.Path).Scan(&got.Name, &got.Path, &got.Enabled, &count); err != nil {
		t.Fatalf("read project: %v", err)
	}
	if got != *project {
		t.Fatalf("stored project = %+v, want %+v", got, *project)
	}
	if count != 1 {
		t.Fatalf("project count = %d, want 1", count)
	}
}

func TestListProjects_ReturnsCorrectFieldsOrderedByName(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`
		INSERT INTO projects (path, name, enabled) VALUES
			('/project/zulu', 'zulu', 0),
			('/project/alpha', 'alpha', 1)
	`); err != nil {
		t.Fatalf("prepare projects: %v", err)
	}

	got, err := store.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("ListProjects() error = %v", err)
	}
	want := []models.Project{
		{Name: "alpha", Path: "/project/alpha", Enabled: true},
		{Name: "zulu", Path: "/project/zulu", Enabled: false},
	}
	if len(got) != len(want) {
		t.Fatalf("ListProjects() length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ListProjects()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSetActiveSession_InsertsNewSession(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`
		INSERT INTO projects (path, name, enabled)
		VALUES ('/project/target', 'target', 1)
	`); err != nil {
		t.Fatalf("prepare project: %v", err)
	}
	session := &models.Session{
		ProjectPath: "/project/target",
		ThreadID:    "thread-new",
		Active:      true,
	}

	if err := store.SetActiveSession(context.Background(), session); err != nil {
		t.Fatalf("SetActiveSession() error = %v", err)
	}

	var got models.Session
	var updatedAt int64
	if err := store.db.QueryRow(`
		SELECT project_path, thread_id, active, updated_at
		FROM sessions
		WHERE thread_id = ?
	`, session.ThreadID).Scan(
		&got.ProjectPath,
		&got.ThreadID,
		&got.Active,
		&updatedAt,
	); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if got != *session {
		t.Fatalf("stored session = %+v, want %+v", got, *session)
	}
	if updatedAt <= 0 {
		t.Fatalf("updated_at = %d, want positive Unix timestamp", updatedAt)
	}
}

func TestSetActiveSession_UpdatesExistingSession(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`
		INSERT INTO projects (path, name, enabled)
		VALUES ('/project/target', 'target', 1);
		INSERT INTO sessions (thread_id, project_path, active, updated_at)
		VALUES ('thread-existing', '/project/target', 0, 1);
	`); err != nil {
		t.Fatalf("prepare session: %v", err)
	}

	if err := store.SetActiveSession(context.Background(), &models.Session{
		ProjectPath: "/project/target",
		ThreadID:    "thread-existing",
		Active:      true,
	}); err != nil {
		t.Fatalf("SetActiveSession() error = %v", err)
	}

	var active bool
	var updatedAt int64
	if err := store.db.QueryRow(`
		SELECT active, updated_at FROM sessions WHERE thread_id = 'thread-existing'
	`).Scan(&active, &updatedAt); err != nil {
		t.Fatalf("read session: %v", err)
	}
	if !active {
		t.Fatal("active = false, want true")
	}
	if updatedAt <= 1 {
		t.Fatalf("updated_at = %d, want value greater than 1", updatedAt)
	}
}

func TestSetActiveSession_SwitchesOnlyTargetProject(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`
		INSERT INTO projects (path, name, enabled) VALUES
			('/project/target', 'target', 1),
			('/project/other', 'other', 1);
		INSERT INTO sessions (thread_id, project_path, active, updated_at) VALUES
			('thread-old', '/project/target', 1, 1),
			('thread-other', '/project/other', 1, 1);
	`); err != nil {
		t.Fatalf("prepare sessions: %v", err)
	}

	if err := store.SetActiveSession(context.Background(), &models.Session{
		ProjectPath: "/project/target",
		ThreadID:    "thread-new",
		Active:      true,
	}); err != nil {
		t.Fatalf("SetActiveSession() error = %v", err)
	}

	var targetThreadID string
	if err := store.db.QueryRow(`
		SELECT thread_id FROM sessions
		WHERE project_path = '/project/target' AND active = 1
	`).Scan(&targetThreadID); err != nil {
		t.Fatalf("read target active session: %v", err)
	}
	if targetThreadID != "thread-new" {
		t.Fatalf("target active thread = %q, want %q", targetThreadID, "thread-new")
	}

	var oldActive bool
	if err := store.db.QueryRow(`
		SELECT active FROM sessions WHERE thread_id = 'thread-old'
	`).Scan(&oldActive); err != nil {
		t.Fatalf("read old target session: %v", err)
	}
	if oldActive {
		t.Fatal("old target session remains active")
	}

	var otherActive bool
	if err := store.db.QueryRow(`
		SELECT active FROM sessions WHERE thread_id = 'thread-other'
	`).Scan(&otherActive); err != nil {
		t.Fatalf("read other project session: %v", err)
	}
	if !otherActive {
		t.Fatal("other project session was deactivated")
	}
}

func TestSetActiveSession_RollsBackDeactivationWhenUpsertFails(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`
		INSERT INTO projects (path, name, enabled)
		VALUES ('/project/target', 'target', 1);
		INSERT INTO sessions (thread_id, project_path, active, updated_at)
		VALUES ('thread-old', '/project/target', 1, 1);
		CREATE TRIGGER fail_test_session_insert
		BEFORE INSERT ON sessions
		WHEN NEW.thread_id = 'thread-fail'
		BEGIN
			SELECT RAISE(ABORT, 'forced test failure');
		END;
	`); err != nil {
		t.Fatalf("prepare rollback case: %v", err)
	}

	err := store.SetActiveSession(context.Background(), &models.Session{
		ProjectPath: "/project/target",
		ThreadID:    "thread-fail",
		Active:      true,
	})
	if err == nil {
		t.Fatal("SetActiveSession() error = nil, want forced failure")
	}

	var oldActive bool
	if queryErr := store.db.QueryRow(`
		SELECT active FROM sessions WHERE thread_id = 'thread-old'
	`).Scan(&oldActive); queryErr != nil {
		t.Fatalf("read old session after rollback: %v", queryErr)
	}
	if !oldActive {
		t.Fatal("old session was not restored by rollback")
	}
}

func TestActiveSession_ReturnsActiveSessionForProject(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`
		INSERT INTO projects (path, name, enabled) VALUES
			('/project/target', 'target', 1),
			('/project/other', 'other', 1);
		INSERT INTO sessions (thread_id, project_path, active, updated_at) VALUES
			('thread-other-project', '/project/other', 1, 1),
			('thread-inactive', '/project/target', 0, 2),
			('thread-active', '/project/target', 1, 3);
	`); err != nil {
		t.Fatalf("prepare sessions: %v", err)
	}

	got, err := store.ActiveSession(context.Background(), "/project/target")
	if err != nil {
		t.Fatalf("ActiveSession() error = %v", err)
	}
	want := models.Session{
		ProjectPath: "/project/target",
		ThreadID:    "thread-active",
		Active:      true,
	}
	if got != want {
		t.Fatalf("ActiveSession() = %+v, want %+v", got, want)
	}
}

func TestActiveSession_ReturnsNotFoundWithoutActiveSession(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`
		INSERT INTO projects (path, name, enabled)
		VALUES ('/project/one', 'one', 1);
		INSERT INTO sessions (thread_id, project_path, active, updated_at)
		VALUES ('thread-inactive', '/project/one', 0, 1);
	`); err != nil {
		t.Fatalf("prepare inactive session: %v", err)
	}

	got, err := store.ActiveSession(context.Background(), "/project/one")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("ActiveSession() error = %v, want ErrNotFound", err)
	}
	if got != (models.Session{}) {
		t.Fatalf("ActiveSession() = %+v, want zero Session", got)
	}
}

func TestActiveSession_PropagatesCanceledContext(t *testing.T) {
	store := openTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	got, err := store.ActiveSession(ctx, "/project/one")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ActiveSession() error = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("ActiveSession() error = %v, must not be ErrNotFound", err)
	}
	if got != (models.Session{}) {
		t.Fatalf("ActiveSession() = %+v, want zero Session", got)
	}
}

func TestQueue_DequeueReturnsMessagesInFIFOOrder(t *testing.T) {
	store := openTestStore(t)
	prepareQueueSession(t, store, "thread-queue")
	messages := []models.QueuedMessage{
		{ThreadID: "thread-queue", ChatID: 10, Text: "four"},
		{ThreadID: "thread-queue", ChatID: 11, Text: "five"},
	}
	for _, message := range messages {
		if err := store.Enqueue(context.Background(), message); err != nil {
			t.Fatalf("Enqueue() error = %v", err)
		}
	}

	first, err := store.Dequeue(context.Background(), "thread-queue")
	if err != nil {
		t.Fatalf("first Dequeue() error = %v", err)
	}
	second, err := store.Dequeue(context.Background(), "thread-queue")
	if err != nil {
		t.Fatalf("second Dequeue() error = %v", err)
	}

	assertQueuedMessage(t, first, messages[0])
	assertQueuedMessage(t, second, messages[1])
	if first.ID >= second.ID {
		t.Fatalf("queue IDs = %d, %d; want increasing", first.ID, second.ID)
	}
}

func TestQueue_DequeueIsScopedToThread(t *testing.T) {
	store := openTestStore(t)
	prepareQueueSession(t, store, "thread-one")
	prepareQueueSession(t, store, "thread-two")
	if err := store.Enqueue(context.Background(), models.QueuedMessage{
		ThreadID: "thread-one",
		ChatID:   10,
		Text:     "one",
	}); err != nil {
		t.Fatalf("enqueue thread one: %v", err)
	}
	if err := store.Enqueue(context.Background(), models.QueuedMessage{
		ThreadID: "thread-two",
		ChatID:   20,
		Text:     "two",
	}); err != nil {
		t.Fatalf("enqueue thread two: %v", err)
	}

	got, err := store.Dequeue(context.Background(), "thread-two")
	if err != nil {
		t.Fatalf("Dequeue() error = %v", err)
	}
	if got.ThreadID != "thread-two" || got.Text != "two" {
		t.Fatalf("Dequeue() = %+v, want thread-two message", got)
	}
}

func TestQueue_EmptyReturnsErrQueueEmpty(t *testing.T) {
	store := openTestStore(t)
	prepareQueueSession(t, store, "thread-empty")

	got, err := store.Dequeue(context.Background(), "thread-empty")
	if !errors.Is(err, ErrQueueEmpty) {
		t.Fatalf("Dequeue() error = %v, want ErrQueueEmpty", err)
	}
	if got != (models.QueuedMessage{}) {
		t.Fatalf("Dequeue() = %+v, want zero QueuedMessage", got)
	}
}

func TestQueue_ConcurrentDequeueConsumesMessageOnce(t *testing.T) {
	store := openTestStore(t)
	prepareQueueSession(t, store, "thread-concurrent")
	if err := store.Enqueue(context.Background(), models.QueuedMessage{
		ThreadID: "thread-concurrent",
		ChatID:   10,
		Text:     "once",
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	type result struct {
		message models.QueuedMessage
		err     error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			message, err := store.Dequeue(context.Background(), "thread-concurrent")
			results <- result{message: message, err: err}
		}()
	}
	close(start)

	successes := 0
	empty := 0
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			if result.message.Text != "once" {
				t.Fatalf("dequeued text = %q, want %q", result.message.Text, "once")
			}
		case errors.Is(result.err, ErrQueueEmpty):
			empty++
		default:
			t.Fatalf("Dequeue() error = %v", result.err)
		}
	}
	if successes != 1 || empty != 1 {
		t.Fatalf("results: success=%d empty=%d, want 1 and 1", successes, empty)
	}
}

func TestQueue_DeleteFailureRollsBackAndDoesNotExposePrompt(t *testing.T) {
	store := openTestStore(t)
	prepareQueueSession(t, store, "thread-rollback")
	const prompt = "private prompt must not appear"
	if err := store.Enqueue(context.Background(), models.QueuedMessage{
		ThreadID: "thread-rollback",
		ChatID:   10,
		Text:     prompt,
	}); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	if _, err := store.db.Exec(`
		CREATE TRIGGER fail_test_queue_delete
		BEFORE DELETE ON queued_messages
		BEGIN
			SELECT RAISE(ABORT, 'forced test failure');
		END;
	`); err != nil {
		t.Fatalf("create delete trigger: %v", err)
	}

	_, err := store.Dequeue(context.Background(), "thread-rollback")
	if err == nil {
		t.Fatal("Dequeue() error = nil, want forced failure")
	}
	if strings.Contains(err.Error(), prompt) {
		t.Fatalf("Dequeue() error exposes prompt: %v", err)
	}
	if _, err := store.db.Exec(`DROP TRIGGER fail_test_queue_delete`); err != nil {
		t.Fatalf("drop delete trigger: %v", err)
	}

	got, err := store.Dequeue(context.Background(), "thread-rollback")
	if err != nil {
		t.Fatalf("Dequeue() after rollback error = %v", err)
	}
	if got.Text != prompt {
		t.Fatalf("dequeued text = %q, want original prompt", got.Text)
	}
}

func TestQueue_EnqueueErrorDoesNotExposePrompt(t *testing.T) {
	store := openTestStore(t)
	prepareQueueSession(t, store, "thread-error")
	if _, err := store.db.Exec(`
		CREATE TRIGGER fail_test_queue_insert
		BEFORE INSERT ON queued_messages
		BEGIN
			SELECT RAISE(ABORT, 'forced test failure');
		END;
	`); err != nil {
		t.Fatalf("create insert trigger: %v", err)
	}
	const prompt = "another private prompt"

	err := store.Enqueue(context.Background(), models.QueuedMessage{
		ThreadID: "thread-error",
		ChatID:   10,
		Text:     prompt,
	})
	if err == nil {
		t.Fatal("Enqueue() error = nil, want forced failure")
	}
	if strings.Contains(err.Error(), prompt) {
		t.Fatalf("Enqueue() error exposes prompt: %v", err)
	}
}

func TestUpdateOffset_MissingReturnsZero(t *testing.T) {
	store := openTestStore(t)

	got, err := store.UpdateOffset(context.Background())
	if err != nil {
		t.Fatalf("UpdateOffset() error = %v", err)
	}
	if got != 0 {
		t.Fatalf("UpdateOffset() = %d, want 0", got)
	}
}

func TestUpdateOffset_UpsertSurvivesStoreReopen(t *testing.T) {
	dsn := uniqueTestDSN(t)
	first, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("open first store: %v", err)
	}
	t.Cleanup(func() { _ = first.Close() })

	// Keep the named shared-memory database alive while Store is reopened.
	keeper := openRawTestDatabase(t, dsn)
	if err := keeper.PingContext(context.Background()); err != nil {
		t.Fatalf("ping keeper connection: %v", err)
	}

	for _, offset := range []int64{41, 42} {
		if err := first.SaveUpdateOffset(context.Background(), offset); err != nil {
			t.Fatalf("SaveUpdateOffset(%d) error = %v", offset, err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first store: %v", err)
	}

	second, err := Open(context.Background(), dsn)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	t.Cleanup(func() { _ = second.Close() })

	got, err := second.UpdateOffset(context.Background())
	if err != nil {
		t.Fatalf("UpdateOffset() error = %v", err)
	}
	if got != 42 {
		t.Fatalf("UpdateOffset() = %d, want 42", got)
	}

	var rows int
	if err := second.db.QueryRow(`
		SELECT count(*) FROM bot_state WHERE key = 'telegram_update_offset'
	`).Scan(&rows); err != nil {
		t.Fatalf("count update offset rows: %v", err)
	}
	if rows != 1 {
		t.Fatalf("update offset rows = %d, want 1", rows)
	}
}

func prepareQueueSession(t *testing.T, store *Store, threadID string) {
	t.Helper()

	projectPath := "/project/" + threadID
	if _, err := store.db.Exec(
		`INSERT INTO projects (path, name, enabled) VALUES (?, ?, 1)`,
		projectPath,
		threadID,
	); err != nil {
		t.Fatalf("prepare queue project %q: %v", threadID, err)
	}
	if _, err := store.db.Exec(`
		INSERT INTO sessions (thread_id, project_path, active, updated_at)
		VALUES (?, ?, 0, 1)
	`, threadID, projectPath); err != nil {
		t.Fatalf("prepare queue session %q: %v", threadID, err)
	}
}

func assertQueuedMessage(t *testing.T, got, want models.QueuedMessage) {
	t.Helper()

	if got.ThreadID != want.ThreadID || got.ChatID != want.ChatID || got.Text != want.Text {
		t.Fatalf("queued message = %+v, want fields from %+v", got, want)
	}
	if got.ID <= 0 {
		t.Fatalf("queued message ID = %d, want positive", got.ID)
	}
	if got.CreatedAt <= 0 {
		t.Fatalf("queued message CreatedAt = %d, want positive", got.CreatedAt)
	}
}
