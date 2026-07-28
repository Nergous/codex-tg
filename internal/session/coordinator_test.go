package session

import (
	"context"
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
