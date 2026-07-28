package app

import (
	"path/filepath"
	"testing"
)

func TestRuntimeInfoRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	want := RuntimeInfo{
		IPCURL:      "http://127.0.0.1:49152",
		IPCToken:    "control-token",
		CodexBinary: `C:\tools\codex.exe`,
	}
	if err := SaveRuntime(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadRuntime(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("runtime=%+v want=%+v", got, want)
	}
}

func TestRuntimePathUsesLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\me\AppData\Local`)
	want := filepath.Join(`C:\Users\me\AppData\Local`, "codex-tg", "runtime.json")
	if got := RuntimePath(); got != want {
		t.Fatalf("RuntimePath()=%q want=%q", got, want)
	}
}
