//go:build windows

package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

func executableName() string { return "codex-tg.exe" }
func Target() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if strings.TrimSpace(base) == "" {
		return "", errors.New("LOCALAPPDATA is not set")
	}
	return filepath.Join(base, "Programs", "codex-tg", "bin", executableName()), nil
}
func AddToUserPath(bin string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	value, _, err := key.GetStringValue("Path")
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	for _, entry := range filepath.SplitList(value) {
		if strings.EqualFold(filepath.Clean(entry), filepath.Clean(bin)) {
			return nil
		}
	}
	if value != "" && !strings.HasSuffix(value, ";") {
		value += ";"
	}
	return key.SetExpandStringValue("Path", value+bin)
}
