package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestTargetUsesPerUserBin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("LOCALAPPDATA", filepath.Join(t.TempDir(), "local"))
		got, err := Target()
		if err != nil || filepath.Base(got) != "codex-tg.exe" || filepath.Base(filepath.Dir(got)) != "bin" {
			t.Fatalf("got=%q err=%v", got, err)
		}
	} else {
		t.Setenv("XDG_BIN_HOME", filepath.Join(t.TempDir(), "bin"))
		got, err := Target()
		if err != nil || got != filepath.Join(os.Getenv("XDG_BIN_HOME"), "codex-tg") {
			t.Fatalf("got=%q err=%v", got, err)
		}
	}
}

func TestCopyExecutableRefusesDifferentExistingFile(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(source, []byte("new"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("other"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CopyExecutable(source, target, false); err == nil {
		t.Fatal("expected overwrite refusal")
	}
}
