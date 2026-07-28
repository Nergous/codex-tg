package autostart

import (
	"context"
	"strings"
	"testing"
)

func TestInstallUsesCurrentExecutableAndLimitedRestart(t *testing.T) {
	var got []string
	s := Scheduler{Executable: `C:\tools\codex-tg.exe`, WorkDir: `C:\Users\me\AppData\Local\codex-tg`, Run: func(_ context.Context, args ...string) ([]byte, error) { got = args; return nil, nil }}
	if err := s.Install(context.Background()); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(got, " ")
	for _, want := range []string{"/Create", `CodexTgBridge`, `/XML`, `/RL LIMITED`} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args=%q missing %q", joined, want)
		}
	}
	for _, want := range []string{`codex-tg.exe`, `serve`, `WorkingDirectory`, `Hidden>true`, `RestartOnFailure`, `Count>3`} {
		if !strings.Contains(s.xml(), want) {
			t.Fatalf("xml missing %q", want)
		}
	}
}

func TestRemoveRejectsDifferentExecutable(t *testing.T) {
	s := Scheduler{Executable: `C:\tools\codex-tg.exe`, Run: func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "/Query" {
			return []byte(`Task To Run: C:\other\codex-tg.exe serve`), nil
		}
		t.Fatal("must not delete")
		return nil, nil
	}}
	if err := s.Remove(context.Background()); err == nil {
		t.Fatal("Remove() error=nil")
	}
}
