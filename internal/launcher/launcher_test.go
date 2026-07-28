package launcher

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestLauncherArgs(t *testing.T) {
	t.Parallel()

	l := New(`C:\tools\codex.exe`, "ws://127.0.0.1:4500")
	got := l.Args(`D:\repo`, "thr-123")
	want := LaunchArgs{
		"--cd", `D:\repo`,
		"--remote", "ws://127.0.0.1:4500",
		"--remote-auth-token-env", "CODEX_TG_REMOTE_TOKEN",
		"resume", "thr-123",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("Args() = %#v, want %#v", got, want)
	}
}

func TestLauncherRunInjectsTokenInEnvNotArgs(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	capture := struct {
		binary string
		args   []string
		env    []string
	}{
		// zero-value
	}
	l := Launcher{
		CodexBinary: "codex.exe",
		Endpoint:    "ws://127.0.0.1:4500",
		TokenEnv:    "CODEX_TG_REMOTE_TOKEN",
		runner: func(_ context.Context, binary string, args []string, env []string) error {
			capture.binary = binary
			capture.args = append(capture.args[:0], args...)
			capture.env = append(capture.env[:0], env...)
			return nil
		},
	}

	const token = "secret-token"
	if err := l.Run(context.Background(), cwd, "thr-123", token); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	if capture.binary != "codex.exe" {
		t.Fatalf("binary = %q, want %q", capture.binary, "codex.exe")
	}

	gotArgs := LaunchArgs(capture.args)
	if gotArgs[0] == "--remote-auth-token-env" {
		// token value must not be passed as CLI argument.
		// this path only proves token presence is not in the first pair.
	}
	for _, arg := range capture.args {
		if arg == token {
			t.Fatalf("token leaked into args: %q", arg)
		}
	}

	found := false
	prefix := "CODEX_TG_REMOTE_TOKEN="
	for _, value := range capture.env {
		if strings.HasPrefix(value, prefix) && strings.TrimPrefix(value, prefix) == token {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("env token not found; env sample=%q", capture.env)
	}

	if got := os.Getenv("CODEX_TG_REMOTE_TOKEN"); got != "" && got == token {
		t.Fatalf("token leaked into current process env: %q", got)
	}
}

func TestLauncherRunRejectsMissingTokenAndRelativePath(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()

	l := New(`C:\tools\codex.exe`, "ws://127.0.0.1:4500")
	if err := l.Run(context.Background(), `repo\relative`, "thr-1", "secret"); !errors.Is(err, ErrInvalidLauncherConfig) {
		t.Fatalf("Run(relative cwd) error = %v, want invalid launcher config", err)
	}

	l.TokenEnv = ""
	if err := l.Run(context.Background(), cwd, "thr-1", "secret"); !errors.Is(err, ErrInvalidLauncherConfig) {
		t.Fatalf("Run(empty token env) error = %v", err)
	}
}
