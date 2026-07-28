//go:build linux

package autostart

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestLinuxInstallWritesAndEnablesUserUnit(t *testing.T) {
	unitPath := filepath.Join(t.TempDir(), UnitName)
	var calls [][]string
	s := Scheduler{
		Executable: "/opt/codex tg/codex-tg",
		WorkDir:    "/home/me/.config/codex-tg",
		UnitPath:   unitPath,
		Run: func(_ context.Context, args ...string) ([]byte, error) {
			calls = append(calls, slices.Clone(args))
			return nil, nil
		},
	}

	if err := s.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(unitPath)
	if err != nil {
		t.Fatal(err)
	}
	unit := string(content)
	for _, want := range []string{
		`ExecStart="/opt/codex tg/codex-tg" serve`,
		`WorkingDirectory="/home/me/.config/codex-tg"`,
		"Restart=on-failure",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
	wantCalls := [][]string{{"--user", "daemon-reload"}, {"--user", "enable", "--now", UnitName}}
	if !slices.EqualFunc(calls, wantCalls, slices.Equal) {
		t.Fatalf("calls = %#v, want %#v", calls, wantCalls)
	}
}

func TestLinuxStatusUsesUserManager(t *testing.T) {
	s := Scheduler{Run: func(_ context.Context, args ...string) ([]byte, error) {
		if !slices.Equal(args, []string{"--user", "is-enabled", "--quiet", UnitName}) {
			t.Fatalf("args = %q", args)
		}
		return nil, nil
	}}

	ok, err := s.Status(context.Background())
	if err != nil || !ok {
		t.Fatalf("Status() = %v, %v", ok, err)
	}
}

func TestLinuxRemoveRejectsDifferentExecutable(t *testing.T) {
	unitPath := filepath.Join(t.TempDir(), UnitName)
	if err := os.WriteFile(unitPath, []byte("[Service]\nExecStart=/other/codex-tg serve\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := Scheduler{
		Executable: "/usr/local/bin/codex-tg",
		UnitPath:   unitPath,
		Run: func(context.Context, ...string) ([]byte, error) {
			t.Fatal("systemctl must not run")
			return nil, nil
		},
	}

	if err := s.Remove(context.Background()); err == nil {
		t.Fatal("Remove() error = nil")
	}
}

func TestLinuxStatusReportsDisabled(t *testing.T) {
	s := Scheduler{Run: func(context.Context, ...string) ([]byte, error) {
		return nil, errors.New("exit status 1")
	}}
	ok, err := s.Status(context.Background())
	if err != nil || ok {
		t.Fatalf("Status() = %v, %v", ok, err)
	}
}
