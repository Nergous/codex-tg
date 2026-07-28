package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nergous/codex-tg/internal/app"
	"github.com/Nergous/codex-tg/internal/codex"
)

func TestRunNotArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("run() code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

func TestRunOpenRequiresConfig(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"open"}, &stdout, &stderr); code != exitError {
		t.Fatalf("run() code = %d, want %d", code, exitError)
	}
	if !strings.Contains(stderr.String(), "usage: open [--new] <project_path>") &&
		!strings.Contains(stderr.String(), "missing CODEX_TG_IPC_TOKEN") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestLoadOpenRuntimeFallsBackToRuntimeFile(t *testing.T) {
	t.Setenv("LOCALAPPDATA", t.TempDir())
	t.Setenv("CODEX_TG_IPC_URL", "")
	t.Setenv("CODEX_TG_IPC_TOKEN", "")
	t.Setenv("CODEX_TG_CODEX_BINARY", "")
	want := app.RuntimeInfo{
		IPCURL:      "http://127.0.0.1:49152",
		IPCToken:    "control-token",
		CodexBinary: filepath.Join(t.TempDir(), "codex.exe"),
	}
	if err := app.SaveRuntime(app.RuntimePath(), want); err != nil {
		t.Fatal(err)
	}

	got, err := loadOpenRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("runtime=%+v want=%+v", got, want)
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"unknown"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("run() code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(stderr.String(), "unknown command") ||
		!strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRunHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--help"}, &stdout, &stderr); code != exitOK {
		t.Fatalf("run() code = %d, want %d", code, exitOK)
	}
	if !strings.Contains(stdout.String(), "usage:") || stderr.Len() != 0 {
		t.Fatalf("stdout = %q, stderr = %q", stdout.String(), stderr.String())
	}
}

type openSupervisor struct{}

func (openSupervisor) Start(context.Context) (codex.AppServerEndpoint, error) {
	return codex.AppServerEndpoint{URL: "ws://127.0.0.1:4500", Token: "runtime-token"}, nil
}

func (openSupervisor) Stop() error { return nil }

func TestRunOpenLaunchesServiceCreatedThread(t *testing.T) {
	ctx := context.Background()
	projectPath := t.TempDir()
	service := app.New(openSupervisor{})
	service.Configure(func(_ context.Context, path string, fresh bool) (string, error) {
		if path != projectPath || fresh {
			t.Fatalf("open path=%q fresh=%t", path, fresh)
		}
		return "thr-service-created", nil
	}, nil, nil)
	if err := service.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Stop(context.Background()) })

	ipcURL, err := service.StartIPC(ctx, "control-token")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_TG_IPC_URL", ipcURL)
	t.Setenv("CODEX_TG_IPC_TOKEN", "control-token")
	t.Setenv("CODEX_TG_CODEX_BINARY", "codex.exe")

	originalLaunch := launchTUI
	t.Cleanup(func() { launchTUI = originalLaunch })
	var launched struct {
		binary, endpoint, cwd, threadID, token string
	}
	launchTUI = func(_ context.Context, binary, endpoint, cwd, threadID, token string) error {
		launched.binary, launched.endpoint, launched.cwd, launched.threadID, launched.token = binary, endpoint, cwd, threadID, token
		return nil
	}

	if err := runOpen([]string{projectPath}); err != nil {
		t.Fatal(err)
	}
	if launched.binary != "codex.exe" || launched.endpoint != "ws://127.0.0.1:4500" || launched.cwd != projectPath || launched.threadID != "thr-service-created" || launched.token != "runtime-token" {
		t.Fatalf("launch=%+v", launched)
	}
}
