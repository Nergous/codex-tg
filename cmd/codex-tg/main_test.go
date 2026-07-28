package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Nergous/codex-tg/internal/app"
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

func TestRunRecognizesCommands(t *testing.T) {
	for _, command := range []string{} {
		t.Run(command, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run([]string{command}, &stdout, &stderr); code != exitError {
				t.Fatalf("run() code = %d, want %d", code, exitError)
			}
			if !strings.Contains(stderr.String(), "command not wired") {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
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
