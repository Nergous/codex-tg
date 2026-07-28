package app

import (
	"path/filepath"
	"testing"
)

func TestConfigPathUsesLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\me\AppData\Local`)
	if got, want := ConfigPath(), filepath.Join(`C:\Users\me\AppData\Local`, "codex-tg", "config.json"); got != want {
		t.Fatalf("ConfigPath()=%q want %q", got, want)
	}
}
