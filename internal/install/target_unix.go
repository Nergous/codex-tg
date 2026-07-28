//go:build !windows

package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func executableName() string { return "codex-tg" }
func Target() (string, error) {
	bin := strings.TrimSpace(os.Getenv("XDG_BIN_HOME"))
	if bin == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		bin = filepath.Join(home, ".local", "bin")
	}
	return filepath.Join(bin, executableName()), nil
}
func AddToUserPath(bin string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	profile := filepath.Join(home, ".profile")
	shell := filepath.Base(os.Getenv("SHELL"))
	if shell == "bash" {
		profile = filepath.Join(home, ".bashrc")
	} else if shell == "zsh" {
		profile = filepath.Join(home, ".zshrc")
	}
	line := fmt.Sprintf(`export PATH="%s:$PATH"`, bin)
	data, err := os.ReadFile(profile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if strings.Contains(string(data), line) {
		return nil
	}
	file, err := os.OpenFile(profile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = fmt.Fprintln(file, line)
	return err
}
