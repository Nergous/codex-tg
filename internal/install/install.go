package install

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func CopyExecutable(source, target string, replace bool) error {
	if filepath.Clean(source) == filepath.Clean(target) {
		return nil
	}
	if _, err := os.Stat(target); err == nil && !replace {
		return errors.New("install target already contains a different file")
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), "codex-tg-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := io.Copy(temporary, input); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Chmod(0o700); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if replace {
		if existing, err := os.ReadFile(target); err == nil {
			incoming, readErr := os.ReadFile(source)
			if readErr != nil {
				return readErr
			}
			if bytes.Equal(existing, incoming) {
				return nil
			}
			if filepath.Base(target) != executableName() {
				return fmt.Errorf("refuse replacement of unexpected target %q", target)
			}
		}
		_ = os.Remove(target)
	}
	return os.Rename(temporaryPath, target)
}
