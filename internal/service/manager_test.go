package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Nergous/codex-tg/internal/app"
)

type fakeChild struct{ killed bool }

func (c *fakeChild) Kill() error { c.killed = true; return nil }

func TestManager_ReturnsHealthyRuntimeWithoutStarting(t *testing.T) {
	want := app.RuntimeInfo{IPCURL: "http://127.0.0.1:1", IPCToken: "x", CodexBinary: "codex"}
	starts := 0
	m := Manager{Load: func() (app.RuntimeInfo, error) { return want, nil }, Probe: func(context.Context, app.RuntimeInfo) error { return nil }, Start: func() (Child, error) { starts++; return &fakeChild{}, nil }}
	got, err := m.Ensure(context.Background())
	if err != nil || got != want || starts != 0 {
		t.Fatalf("got=%+v starts=%d err=%v", got, starts, err)
	}
}

func TestManager_TimeoutKillsOnlyStartedChild(t *testing.T) {
	child := &fakeChild{}
	m := Manager{
		Load:    func() (app.RuntimeInfo, error) { return app.RuntimeInfo{}, errors.New("stale") },
		Probe:   func(context.Context, app.RuntimeInfo) error { return errors.New("down") },
		Start:   func() (Child, error) { return child, nil },
		Timeout: 20 * time.Millisecond, PollInterval: time.Millisecond,
	}
	_, err := m.Ensure(context.Background())
	if err == nil || !child.killed {
		t.Fatalf("killed=%t err=%v", child.killed, err)
	}
}
