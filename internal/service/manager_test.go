package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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

func TestManager_ProbeTimeoutPreservesReadinessDeadline(t *testing.T) {
	child := &fakeChild{}
	m := Manager{
		Load: func() (app.RuntimeInfo, error) {
			return app.RuntimeInfo{IPCURL: "http://127.0.0.1:1"}, nil
		},
		Probe: func(ctx context.Context, _ app.RuntimeInfo) error {
			<-ctx.Done()
			return ctx.Err()
		},
		Start:        func() (Child, error) { return child, nil },
		Timeout:      20 * time.Millisecond,
		PollInterval: time.Millisecond,
		ProbeTimeout: 2 * time.Millisecond,
	}
	started := time.Now()
	_, err := m.Ensure(context.Background())
	if err == nil || !child.killed {
		t.Fatalf("killed=%t err=%v", child.killed, err)
	}
	if elapsed := time.Since(started); elapsed > 200*time.Millisecond {
		t.Fatalf("Ensure() ignored readiness deadline: %s", elapsed)
	}
}

func TestAcquireFileLockReplacesStaleLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "service-start.lock")
	if err := os.WriteFile(path, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	unlock, err := acquireFileLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("lock remains after unlock: %v", err)
	}
}
