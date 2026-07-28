//go:build !windows

package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAddToUserPathWritesShellSyntax(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/bash")
	bin := filepath.Join(home, ".local", "bin")
	if err := AddToUserPath(bin); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(home, ".bashrc"))
	if err != nil {
		t.Fatal(err)
	}
	want := `export PATH="` + bin + `:$PATH"`
	if !strings.Contains(string(content), want) {
		t.Fatalf("profile=%q want %q", content, want)
	}
}
