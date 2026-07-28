package session

import (
	"context"
	"errors"
	"testing"

	"github.com/Nergous/codex-tg/internal/models"
	"github.com/Nergous/codex-tg/internal/state"
)

type fakeCodex struct{ started, resumed, turns, interrupted []string }

func (f *fakeCodex) StartThread(context.Context, string) (string, error) {
	f.started = append(f.started, "thr-new")
	return "thr-new", nil
}
func (f *fakeCodex) ResumeThread(_ context.Context, id string) error {
	f.resumed = append(f.resumed, id)
	return nil
}
func (f *fakeCodex) StartTurn(_ context.Context, thread, prompt string) (string, error) {
	f.turns = append(f.turns, thread+":"+prompt)
	return "turn-" + prompt, nil
}
func (f *fakeCodex) InterruptTurn(_ context.Context, thread, turn string) error {
	f.interrupted = append(f.interrupted, thread+":"+turn)
	return nil
}

func TestOpenProjectReusesPersistedThread(t *testing.T) {
	ctx := context.Background()
	db, _ := state.Open(ctx, ":memory:")
	t.Cleanup(func() { db.Close() })
	project := models.Project{Name: "demo", Path: t.TempDir()}
	_ = db.PutProject(ctx, &project)
	_ = db.SetActiveSession(ctx, &models.Session{ProjectPath: project.Path, ThreadID: "thr-old", Active: true})
	fake := &fakeCodex{}
	c := New(fake, db, []models.Project{project})
	s, err := c.OpenProject(ctx, project.Path, false)
	if err != nil {
		t.Fatal(err)
	}
	if s.ThreadID != "thr-old" || len(fake.resumed) != 1 {
		t.Fatalf("session=%+v resumed=%v", s, fake.resumed)
	}
}

func TestSubmitQueuesUntilTurnCompleted(t *testing.T) {
	ctx := context.Background()
	db, _ := state.Open(ctx, ":memory:")
	t.Cleanup(func() { db.Close() })
	project := models.Project{Name: "demo", Path: t.TempDir()}
	_ = db.PutProject(ctx, &project)
	_ = db.SetActiveSession(ctx, &models.Session{ProjectPath: project.Path, ThreadID: "thr-1", Active: true})
	fake := &fakeCodex{}
	c := New(fake, db, []models.Project{project})
	if err := c.Submit(ctx, "thr-1", "one"); err != nil {
		t.Fatal(err)
	}
	if err := c.Submit(ctx, "thr-1", "two"); err != nil {
		t.Fatal(err)
	}
	if len(fake.turns) != 1 {
		t.Fatalf("turns=%v", fake.turns)
	}
	if err := c.Complete(ctx, "thr-1", "turn-one"); err != nil {
		t.Fatal(err)
	}
	if len(fake.turns) != 2 || fake.turns[1] != "thr-1:two" {
		t.Fatalf("turns=%v", fake.turns)
	}
}

func TestCompleteIgnoresStaleTurn(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	project := models.Project{Name: "demo", Path: t.TempDir()}
	if err := db.PutProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	if err := db.SetActiveSession(ctx, &models.Session{ProjectPath: project.Path, ThreadID: "thr-1", Active: true}); err != nil {
		t.Fatal(err)
	}

	fake := &fakeCodex{}
	c := New(fake, db, []models.Project{project})
	if err := c.Submit(ctx, "thr-1", "one"); err != nil {
		t.Fatal(err)
	}
	if err := c.Submit(ctx, "thr-1", "two"); err != nil {
		t.Fatal(err)
	}

	if err := c.Complete(ctx, "thr-1", "turn-stale"); err != nil {
		t.Fatal(err)
	}
	if len(fake.turns) != 1 {
		t.Fatalf("stale completion started queued turn: %v", fake.turns)
	}
	if got, err := c.Status(ctx, "thr-1"); err != nil || got != "running: turn-one" {
		t.Fatalf("status=%q error=%v", got, err)
	}
}

func TestResumeThreadRejectsThreadOutsidePersistedSessions(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	project := models.Project{Name: "demo", Path: t.TempDir()}
	if err := db.PutProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	if err := db.SetActiveSession(ctx, &models.Session{ProjectPath: project.Path, ThreadID: "thr-allowed", Active: true}); err != nil {
		t.Fatal(err)
	}

	fake := &fakeCodex{}
	coordinator := New(fake, db, []models.Project{project})
	if err := coordinator.ResumeThread(ctx, "thr-outside"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ResumeThread() error = %v, want state.ErrNotFound", err)
	}
	if len(fake.resumed) != 0 {
		t.Fatalf("ResumeThread() called Codex for disallowed thread: %v", fake.resumed)
	}
}

func TestResumeThreadRejectsSessionRemovedFromConfiguredAllowList(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	staleProject := models.Project{Name: "removed", Path: t.TempDir()}
	if err := db.PutProject(ctx, &staleProject); err != nil {
		t.Fatal(err)
	}
	if err := db.SetActiveSession(ctx, &models.Session{ProjectPath: staleProject.Path, ThreadID: "thr-removed", Active: true}); err != nil {
		t.Fatal(err)
	}

	configuredProject := models.Project{Name: "configured", Path: t.TempDir()}
	fake := &fakeCodex{}
	coordinator := New(fake, db, []models.Project{configuredProject})
	if err := coordinator.ResumeThread(ctx, "thr-removed"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("ResumeThread() error = %v, want state.ErrNotFound", err)
	}
	if len(fake.resumed) != 0 {
		t.Fatalf("ResumeThread() called Codex for removed project: %v", fake.resumed)
	}
}

func TestResumeThreadStartsPersistedQueueInFIFOOrderAfterRecovery(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	project := models.Project{Name: "demo", Path: t.TempDir()}
	if err := db.PutProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	if err := db.SetActiveSession(ctx, &models.Session{ProjectPath: project.Path, ThreadID: "thr-1", Active: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.SetRunningTurn(ctx, "thr-1", "turn-interrupted-by-restart"); err != nil {
		t.Fatal(err)
	}
	if err := db.Enqueue(ctx, models.QueuedMessage{ThreadID: "thr-1", ChatID: 200, Text: "first"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Enqueue(ctx, models.QueuedMessage{ThreadID: "thr-1", ChatID: 200, Text: "second"}); err != nil {
		t.Fatal(err)
	}
	if err := db.FaultRunningTurns(ctx); err != nil {
		t.Fatal(err)
	}

	fake := &fakeCodex{}
	coordinator := New(fake, db, []models.Project{project})
	if err := coordinator.ResumeThread(ctx, "thr-1"); err != nil {
		t.Fatal(err)
	}
	if got, want := fake.resumed, []string{"thr-1"}; !sameStrings(got, want) {
		t.Fatalf("resumed = %v, want %v", got, want)
	}
	if got, want := fake.turns, []string{"thr-1:first"}; !sameStrings(got, want) {
		t.Fatalf("turns = %v, want %v", got, want)
	}

	if err := coordinator.Complete(ctx, "thr-1", "turn-first"); err != nil {
		t.Fatal(err)
	}
	if got, want := fake.turns, []string{"thr-1:first", "thr-1:second"}; !sameStrings(got, want) {
		t.Fatalf("turns = %v, want %v", got, want)
	}
}

func TestCoordinatorExposesRecentSessionsAndQueue(t *testing.T) {
	ctx := context.Background()
	db, err := state.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	project := models.Project{Name: "demo", Path: t.TempDir()}
	if err := db.PutProject(ctx, &project); err != nil {
		t.Fatal(err)
	}
	if err := db.SetActiveSession(ctx, &models.Session{ProjectPath: project.Path, ThreadID: "thr-1", Active: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.Enqueue(ctx, models.QueuedMessage{ThreadID: "thr-1", ChatID: 200, Text: "queued"}); err != nil {
		t.Fatal(err)
	}

	coordinator := New(&fakeCodex{}, db, []models.Project{project})
	sessions, err := coordinator.RecentSessions(ctx, project.Path, 10)
	if err != nil || len(sessions) != 1 || sessions[0].ThreadID != "thr-1" {
		t.Fatalf("RecentSessions() = %+v, %v", sessions, err)
	}
	queue, err := coordinator.QueuedMessages(ctx, "thr-1")
	if err != nil || len(queue) != 1 || queue[0].Text != "queued" {
		t.Fatalf("QueuedMessages() = %+v, %v", queue, err)
	}
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
