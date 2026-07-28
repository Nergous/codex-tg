package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type RuntimeInfo struct {
	IPCURL      string `json:"ipc_url"`
	IPCToken    string `json:"ipc_token"`
	CodexBinary string `json:"codex_binary"`
}

func SaveRuntime(path string, info RuntimeInfo) error {
	if err := info.validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create runtime directory: %w", err)
	}
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("encode runtime info: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("write runtime info: %w", err)
	}
	return nil
}

func LoadRuntime(path string) (RuntimeInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return RuntimeInfo{}, fmt.Errorf("open runtime info: %w", err)
	}
	defer file.Close()

	var info RuntimeInfo
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&info); err != nil {
		return RuntimeInfo{}, fmt.Errorf("decode runtime info: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return RuntimeInfo{}, errors.New("decode runtime info: trailing data")
	}
	if err := info.validate(); err != nil {
		return RuntimeInfo{}, err
	}
	return info, nil
}

func RemoveRuntime(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (info RuntimeInfo) validate() error {
	if strings.TrimSpace(info.IPCToken) == "" || strings.TrimSpace(info.CodexBinary) == "" {
		return errors.New("invalid runtime info")
	}
	parsed, err := url.Parse(info.IPCURL)
	if err != nil || parsed.Scheme != "http" || parsed.Host == "" {
		return errors.New("invalid runtime IPC URL")
	}
	host, _, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		return errors.New("invalid runtime IPC URL")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("runtime IPC URL is not loopback")
	}
	return nil
}
