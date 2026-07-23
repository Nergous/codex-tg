package config

import "errors"

var (
	ErrEmptyTelegramConfig    = errors.New("telegram config must not be empty")
	ErrInvalidTelegramID      = errors.New("telegram ID must be non-negative")
	ErrEmptyAppServerConfig   = errors.New("app server config must not be empty")
	ErrInvalidAppServerListen = errors.New("invalid app server listen address")
	ErrUnsafeListenAddress    = errors.New("unsafe listen address")
	ErrCodexBinaryNotFound    = errors.New("codex binary not found")
	ErrInvalidCodexBinary     = errors.New("invalid codex binary")
	ErrProjectsNotUnique      = errors.New("projects must be unique")
	ErrProjectNotFound        = errors.New("project not found")
	ErrInvalidProject         = errors.New("invalid project")
	ErrConfigNotFound         = errors.New("config not found")
	ErrInvalidConfigFile      = errors.New("invalid config file")
)
