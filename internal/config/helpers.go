package config

import (
	"os"
	"path/filepath"
)

func isExist(path string, notExistError error) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, notExistError
		}

		return nil, err
	}

	return info, nil
}

func canonicalPath(path string) (string, error) {
	return canonicalPathWith(path, filepath.Abs, filepath.EvalSymlinks)
}

func canonicalPathWith(
	path string,
	absolute func(string) (string, error),
	evalSymlinks func(string) (string, error),
) (string, error) {
	abs, err := absolute(path)
	if err != nil {
		return "", err
	}

	resolved, err := evalSymlinks(abs)
	if err != nil {
		return "", err
	}

	return filepath.Clean(resolved), nil
}
