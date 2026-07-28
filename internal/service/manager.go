package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Nergous/codex-tg/internal/app"
	"github.com/Nergous/codex-tg/internal/ipc"
)

type Child interface{ Kill() error }
type Unlock func() error

type Manager struct {
	Load         func() (app.RuntimeInfo, error)
	Probe        func(context.Context, app.RuntimeInfo) error
	Start        func() (Child, error)
	Acquire      func() (Unlock, error)
	Timeout      time.Duration
	PollInterval time.Duration
}

func (m Manager) Ensure(ctx context.Context) (app.RuntimeInfo, error) {
	if m.Load == nil || m.Probe == nil || m.Start == nil {
		return app.RuntimeInfo{}, errors.New("service manager is not configured")
	}
	if info, err := m.Load(); err == nil && m.Probe(ctx, info) == nil {
		return info, nil
	}
	unlock := Unlock(func() error { return nil })
	if m.Acquire != nil {
		var err error
		unlock, err = m.Acquire()
		if err != nil {
			return app.RuntimeInfo{}, fmt.Errorf("service startup already in progress: %w", err)
		}
	}
	defer unlock()
	if info, err := m.Load(); err == nil && m.Probe(ctx, info) == nil {
		return info, nil
	}
	child, err := m.Start()
	if err != nil {
		return app.RuntimeInfo{}, fmt.Errorf("start background service: %w", err)
	}
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 8 * time.Second
	}
	interval := m.PollInterval
	if interval <= 0 {
		interval = 100 * time.Millisecond
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = child.Kill()
			return app.RuntimeInfo{}, ctx.Err()
		case <-deadline.C:
			_ = child.Kill()
			return app.RuntimeInfo{}, errors.New("service readiness timeout; run `codex-tg serve` or `codex-tg status`")
		case <-ticker.C:
			if info, loadErr := m.Load(); loadErr == nil && m.Probe(ctx, info) == nil {
				return info, nil
			}
		}
	}
}

func DefaultManager(runtimePath string) Manager {
	return Manager{
		Load: func() (app.RuntimeInfo, error) { return app.LoadRuntime(runtimePath) },
		Probe: func(ctx context.Context, info app.RuntimeInfo) error {
			_, err := ipc.NewClient(info.IPCURL, info.IPCToken).Status(ctx)
			return err
		},
		Start: startDetached,
		Acquire: func() (Unlock, error) {
			return acquireFileLock(filepath.Join(filepath.Dir(runtimePath), "service-start.lock"))
		},
	}
}

func acquireFileLock(path string) (Unlock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	return func() error {
		closeErr := file.Close()
		removeErr := os.Remove(path)
		if closeErr != nil {
			return closeErr
		}
		return removeErr
	}, nil
}
