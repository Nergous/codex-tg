package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAppServerConfig_validate(t *testing.T) {
	t.Parallel()

	tempDir := t.TempDir()
	codexBinary := filepath.Join(tempDir, "codex")
	if err := os.WriteFile(codexBinary, []byte("test binary"), 0o755); err != nil {
		t.Fatalf("create test codex binary: %v", err)
	}

	missingBinary := filepath.Join(tempDir, "missing-codex")
	invalidPath := string([]byte{0})
	_, invalidPathError := os.Stat(invalidPath)
	if invalidPathError == nil || os.IsNotExist(invalidPathError) {
		t.Fatalf("invalid path must produce a non-IsNotExist error, got %v", invalidPathError)
	}

	tests := []struct {
		name      string
		config    *AppServerConfig
		wantError error
		check     func(t *testing.T, err error)
	}{
		{
			name:      "nil config",
			config:    nil,
			wantError: ErrEmptyAppServerConfig,
		},
		{
			name:      "empty config",
			config:    &AppServerConfig{},
			wantError: ErrEmptyAppServerConfig,
		},
		{
			name: "empty listen address",
			config: &AppServerConfig{
				CodexBinary: codexBinary,
			},
			wantError: ErrEmptyAppServerConfig,
		},
		{
			name: "empty codex binary",
			config: &AppServerConfig{
				Listen: "127.0.0.1:8080",
			},
			wantError: ErrEmptyAppServerConfig,
		},
		{
			name: "listen address without port",
			config: &AppServerConfig{
				Listen:      "127.0.0.1",
				CodexBinary: codexBinary,
			},
			wantError: ErrInvalidAppServerListen,
		},
		{
			name: "listen address with too many parts",
			config: &AppServerConfig{
				Listen:      "127.0.0.1:8080:extra",
				CodexBinary: codexBinary,
			},
			wantError: ErrInvalidAppServerListen,
		},
		{
			name: "unsafe listen host",
			config: &AppServerConfig{
				Listen:      "0.0.0.0:8080",
				CodexBinary: codexBinary,
			},
			wantError: ErrUnsafeListenAddress,
		},
		{
			name: "non-numeric port",
			config: &AppServerConfig{
				Listen:      "127.0.0.1:http",
				CodexBinary: codexBinary,
			},
			wantError: ErrInvalidAppServerListen,
		},
		{
			name: "port below range",
			config: &AppServerConfig{
				Listen:      "127.0.0.1:0",
				CodexBinary: codexBinary,
			},
			wantError: ErrInvalidAppServerListen,
		},
		{
			name: "port above range",
			config: &AppServerConfig{
				Listen:      "127.0.0.1:65536",
				CodexBinary: codexBinary,
			},
			wantError: ErrInvalidAppServerListen,
		},
		{
			name: "missing codex binary",
			config: &AppServerConfig{
				Listen:      "127.0.0.1:8080",
				CodexBinary: missingBinary,
			},
			wantError: ErrCodexBinaryNotFound,
		},
		{
			name: "codex binary stat failure",
			config: &AppServerConfig{
				Listen:      "127.0.0.1:8080",
				CodexBinary: invalidPath,
			},
			check: func(t *testing.T, err error) {
				t.Helper()
				if err == nil {
					t.Fatal("expected stat error, got nil")
				}
				if errors.Is(err, ErrCodexBinaryNotFound) {
					t.Fatalf("expected raw stat error, got %v", err)
				}
			},
		},
		{
			name: "codex binary is directory",
			config: &AppServerConfig{
				Listen:      "127.0.0.1:8080",
				CodexBinary: tempDir,
			},
			wantError: ErrInvalidCodexBinary,
		},
		{
			name: "valid config with minimum port",
			config: &AppServerConfig{
				Listen:      "127.0.0.1:1",
				CodexBinary: codexBinary,
			},
		},
		{
			name: "valid config with maximum port",
			config: &AppServerConfig{
				Listen:      "127.0.0.1:65535",
				CodexBinary: codexBinary,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.config.validate()
			if test.check != nil {
				test.check(t, err)
				return
			}

			if !errors.Is(err, test.wantError) {
				t.Fatalf("validate() error = %v, want %v", err, test.wantError)
			}
		})
	}
}
