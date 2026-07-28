package state

import (
	"context"
	"testing"

	"github.com/Nergous/codex-tg/internal/models"
)

func TestFaultRunningTurnsDoesNotReplayQueue(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	p := &models.Project{Name: "demo", Path: t.TempDir()}
	if err := store.PutProject(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := store.SetActiveSession(ctx, &models.Session{ProjectPath: p.Path, ThreadID: "thr-1", Active: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetRunningTurn(ctx, "thr-1", "turn-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.FaultRunningTurns(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := store.TurnState(ctx, "thr-1"); err != nil || got != "faulted" {
		t.Fatalf("state=%q err=%v", got, err)
	}
}
