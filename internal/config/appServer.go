package config

import (
	"strconv"
	"strings"
)

type AppServerConfig struct {
	Listen      string `json:"listen"`
	CodexBinary string `json:"codex_binary"`
}

func (a *AppServerConfig) validate() error {
	if a == nil {
		return ErrEmptyAppServerConfig
	}

	if a.Listen == "" || a.CodexBinary == "" {
		return ErrEmptyAppServerConfig
	}

	addr := strings.Split(a.Listen, ":")
	if len(addr) != 2 {
		return ErrInvalidAppServerListen
	}

	host := addr[0]
	port := addr[1]

	if host != "127.0.0.1" {
		return ErrUnsafeListenAddress
	}

	intPort, err := strconv.Atoi(port)
	if err != nil {
		return ErrInvalidAppServerListen
	}

	if intPort < 1 || intPort > 65535 {
		return ErrInvalidAppServerListen
	}

	info, err := isExist(a.CodexBinary, ErrCodexBinaryNotFound)
	if err != nil {
		return err
	}

	if !info.Mode().IsRegular() {
		return ErrInvalidCodexBinary
	}

	return nil
}
